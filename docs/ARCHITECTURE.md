# Aether Cockpit 总体架构方案

> 版本：v3（最终落地版）
> 本文档是项目的**单一权威设计来源**：它整合了最初三版方案的演进脉络，
> 并按"实际实现"修正了原型中的偏差。代码与本文冲突时，以代码为准并回头修文档。

---

## 0. 项目定位

一句话：**本地 AI 算力聚合网关 + 账号/签到管家**。

| 侧 | 干什么 |
|---|---|
| 上游 Provider | 把多个 AI 产品的客户端接口当作算力来源（含 CN/Intl 双版本） |
| 内核 Kernel | 统一 IR、协议转换、能力补偿、账号池调度、签到保活、反风控 |
| 下游 Consumer | 在 `127.0.0.1` 暴露 OpenAI / Anthropic 标准端点 |

产品形态：**Wails v2 桌面应用**（Go 内核 + 原生窗口 + 系统托盘），同时提供 headless 入口供服务器场景。

---

## 1. 四大架构支柱

这四个模式回答"为什么渠道从 3 个涨到 30 个也不会腐化"：

### 1.1 微内核 + 零侵入自注册
主程序不 import 任何具体渠道；渠道通过 `init()` 自注册（对标 `database/sql` 的 driver 机制）。新增渠道只在入口加一行匿名导入。

### 1.2 能力特征正交拆分（Capability ISP）
不定义 15 个方法的大接口，而是 1 个基础契约 + 6 个可选能力：

```go
BaseProvider                  // Manifest() —— 必须实现
ChatCapability                // 流式对话
CheckInCapability             // 签到任务
QuotaCapability               // 配额查询
LocalImportCapability         // 本机凭证扫描
TokenRefreshCapability        // 凭证刷新
NativeResponsesCapability     // Responses 协议直通
```

内核在运行时用类型断言探测（`provider.AsCheckIn(p)`）。没实现的能力，UI 上对应的按钮自动消失。

其中 `LocalImportCapability` 由逆向渠道通用引擎按 spec 声明的路径与字段实现；`TokenRefreshCapability` 除渠道自实现外，还有内核的定时保活循环兜底（见第 10 节）。

### 1.3 声明式 Manifest 驱动 UI
前端**不认识任何具体渠道**。侧边栏、账号表单、签到任务、模型列表全部由后端下发的 `ProviderManifest` 渲染。新增渠道时前端代码零改动。

登录方式不做成任意 Schema（会退化成 JSON 渲染器），而是 5 种内置流程枚举：

```go
AuthLocalImport / AuthOAuthPKCE / AuthDeviceCode / AuthManualInput / AuthWebViewLogin
```

前端内置流程组件覆盖 95% 场景。当前已实现本机导入、OAuth PKCE、设备码、手动凭证四种；`webview_login` 因 Wails v2 没有可拦截回调的内置浏览器，保留枚举并在 UI 上明确提示。

### 1.4 机制与策略分离（Hot-Reload Spec）
逆向渠道易变的部分（base_url、版本号、签名盐值、头模板、模型映射）全部抽到 `configs/specs/*.json`，支持本地热重载与远程 HTTPS 拉取。签名算法本身（机制）留在代码里。

实测：改 `spec_version` 与模型映射后点「重载」，上游立刻收到新模型名，进程未重启。

---

## 2. 系统分层全景

```text
+===================================================================================+
|                              表现层 (Presentation)                                 |
|   [Wails 桌面窗口]     [系统托盘（代码生成图标）]     [headless CLI + Web UI]      |
|        前端 = 同一套 internal/web/ui2 资源，三种入口共享                             |
+===================================================================================+
                                    │ 同源 fetch（无跨域）
+===================================================================================+
|                              网关入口层 (Inbound Gateway)                          |
|   POST /v1/chat/completions   POST /v1/responses   POST /v1/messages              |
|   GET  /v1/models              GET/DELETE /v1/responses/{id}                      |
+===================================================================================+
                                    │ 归一化
+===================================================================================+
|                        统一中间表示层 (Unified IR v3)                              |
|   UnifiedChatRequest{ Instructions, Items[], Tools[], PreviousResponseID }        |
|   UnifiedItem{ message | reasoning | function_call | function_call_output }       |
|   不可重建元数据：ThoughtSignature / EncryptedContent / RefID                      |
+===================================================================================+
                                    │
        ┌───────────────────────────┼───────────────────────────┐
        │  Fast-Path 零损直通        │  IR-Path 标准转换          │
        │  (下游+上游同为Responses)  │  中间件链 → Provider       │
        └───────────────────────────┴───────────────────────────┘
                                    │
+===================================================================================+
|                          核心编排层 (Core Kernel)                                  |
|  ┌──────────────────┐ ┌──────────────────┐ ┌──────────────────────────────────┐  |
|  │ 账号池 (五态+粘性  │ │ 签到调度 (DAG+cron │ │ 事件总线 (SSE /api/events 实时推送)│  │
|  │ +加权额度路由)     │ │ +唤醒补签+抖动队列)│ │ request.* account.* checkin.*    │  │
|  └──────────────────┘ └──────────────────┘ └──────────────────────────────────┘  |
|  ┌──────────────────┐ ┌──────────────────┐ ┌──────────────────────────────────┐  |
|  │ 状态缝合 (LRU +   │ │ 认证中心 (PKCE +  │ │ spec 热更新 (本地 + 远程 HTTPS)   │  │
|  │ SQLite 持久化)    │ │ 设备码 + 刷新)    │ │                                  │  │
|  └──────────────────┘ └──────────────────┘ └──────────────────────────────────┘  |
+===================================================================================+
                                    │ 能力接口调用
+===================================================================================+
|                        插件化渠道注册中心 (Provider Registry)                      |
| [openai-compat ✓] [antigravity ✓] [qoder ✓] [trae ✓] [workbuddy ✓代码] [kiro ✓代码]   |
| [mock ×3] [reverse 2 骨架]                                                          |
+===================================================================================+
                                    │
+===================================================================================+
|                        反风控基础设施层 (Anti-Ban Infra)                           |
|   [uTLS JA3/JA4 指纹]   [一号一沙箱独立 Client]   [CN直连/Intl代理分流]             |
|   [设备指纹持久化]       [TUN 模式检测]            [AES 本机绑定加密]               |
+===================================================================================+
```

---

## 3. 十个目标渠道矩阵

> 口径说明：早期文档写"13 个逆向渠道"，但 v3 矩阵实际是下面 10 个 ProviderID；
> 多出来的 3 个是尚未落入矩阵的草案区域变体。为避免凭空造 ID，代码与文档统一为 10 个。

| 渠道族 | ProviderID | 区域 | 网络策略 | 传输协议 | 状态 |
|---|---|---|---|---|---|
| WorkBuddy 族 | `workbuddy-cn` / `workbuddy-intl` | CN 直连 / Intl 代理 | 标准 OpenAI 兼容 SSE | ✅ **代码已实现**：独立 provider（完整客户端头 + 双区模型）；桌面 5.6+ 登录态是 suite 加密信封，待可用 token 验证 |
| Trae 族 | `trae-cn` / `trae-intl` | 同上 | 专有 SSE（event/data） | ✅ **真实可用**：tc 解密导入 + `Cloud-IDE-JWT` + `chat_v3`；`auto` 映射默认模型；UA 必须为 `Trae/<版本>`，否则上游返回流内 4011 |
| Qoder 族 | `qoder-cn` / `qoder-intl` | 同上 | 专有 SSE 信封 | ✅ **真实可用**：独立实现（COSY 签名 + 自定义 Base64 + 设备令牌导入） |
| Antigravity | `antigravity` | Intl 代理 | Gemini 变体 SSE | ✅ **真实可用**：独立实现（OAuth 刷新 + loadCodeAssist + `v1internal:streamGenerateContent`），Gemini/Claude 均可用 |
| Kiro | `kiro` | Intl 代理 | **AWS 二进制 EventStream** | ✅ **代码已实现**：独立 provider（`conversationState` + AWS EventStream + 18 模型 + 工具/用量/异常）；本机无 Kiro 账号，待 token 验证 |
| Codex | `codex` | Intl 代理 | 原生 Responses SSE | ✅ **可用**：本机 Codex CLI 自定义 provider 导入 + 流式/非流式 Fast-Path；`codex/auto` 读取导入模型 |
| OpenCode | `opencode` | 混合 | 标准 OpenAI | ✅ **真实可用**：直连 `opencode.ai/zen/v1`，本机 `auth.json` 导入；账号免费额度用尽时上游 429 |
| 通用 | `openai-compat` | 跟随系统 | 标准 OpenAI | ✅ **真实可用** |

**Engine + Region Profile 模式**：同一产品的 CN/Intl 共用一套协议引擎，通过 `RegionProfile`（Manifest 里的 `RegionTag` + `NetworkPolicy`）注入差异，注册为两个独立渠道，85% 代码复用。

### 3.1 通用逆向引擎（`internal/providers/reverse`）

逆向渠道的差异被拆成三层，只有第一层需要写代码：

| 层 | 内容 | 位置 |
|---|---|---|
| 机制 | HTTP、分帧、SSE/NDJSON/AWS 解码、IR 事件、状态机 | `reverse/chat.go` + `wirecodec` |
| 策略 | 端点、字段名、鉴权头、签名盐值、模型映射、中间件、OAuth | `configs/specs/<id>.json` |
| 静态差异 | 显示名、区域、网络策略、默认端点 | `reverse/profiles.go` |

spec 里的 `extra.chat` 描述请求/响应字段映射，`extra.local_import` 描述本机凭证路径，
`oauth` 覆盖授权/刷新参数。未配置时请求返回 `capability_unsupported` 并附带
`configs/specs/<id>.json` 的填写指引——**不会静默猜测协议**。

**本机导入内置规则**：`reverse` 保留 Codex（`~/.codex/config.toml`，TOML）与
OpenCode（`~/.local/share/opencode/auth.json`，JSON）两条规则；其他渠道可通过 spec
的 `extra.local_import` 声明 JSON/TOML/text 路径。扫描接口只返回展示信息，不回传凭证。
独立实现的渠道各自负责导入：Qoder（DPAPI + AES-GCM 的 `auth.v1.dat`）、
Trae（`storage.json` 的 `iCubeAuthInfo://icube.cloudide` tc 解密）、
Antigravity（Windows 凭据管理器 `gemini:antigravity`）、
WorkBuddy（`workbuddy-desktop*.info`）、Kiro（`kiro_accounts.json`）。

**为什么多数渠道不塞进 reverse**：OAuth 刷新、项目发现、Gemini 变体流、COSY 签名、
自定义 Base64、tc 解密、专有 SSE 信封、AWS EventStream 与 `conversationState`
都不是 spec 能表达的，因此 Antigravity / Qoder / Trae / WorkBuddy / Kiro
均为独立实现（`internal/providers/<id>`），通用引擎只承载字段映射型渠道。

**Auto 模型**：声明 `AutoModel` 的渠道会暴露 `<id>/auto`，真实上游模型名从账号的
`model` 凭证读取（Codex 本机导入会填入 config.toml 的 `model`）。这样无需把用户
特定的模型名写进 Manifest，也能在 `/v1/models` 中出现一个可用入口。

---

## 4. 中间表示（Unified IR v3）

IR 是全系统**唯一稳定契约**：新增下游协议只改入站层，新增渠道只动 provider 层，互不影响。

### 4.1 请求侧

```go
type UnifiedChatRequest struct {
    RequestID          string            // 网关生成的 resp_id / msg_id / chatcmpl-id
    DownstreamProtocol Protocol          // openai_chat | openai_responses | anthropic_messages
    Model              string            // 下游原始模型名（含路由前缀）
    ProviderID         string            // 路由后回填
    UpstreamModel      string            // 同上

    PreviousResponseID string            // Responses 的状态链
    StoreResponse      bool              // store=false 时桥接降级为无状态

    Instructions       string            // 归一化的 system（多条折叠）
    Items              []UnifiedItem     // 全量上下文
    Tools              []UnifiedToolSpec

    Stream             bool
    Temperature, TopP  *float64
    MaxOutputTokens    *int
    ReasoningEffort    string            // low | medium | high
}
```

### 4.2 UnifiedItem：最小上下文单位

| Type | 含义 | 不可重建字段 |
|---|---|---|
| `message` | 普通消息（可带多模态附件） | — |
| `reasoning` | 思考块 | **ThoughtSignature**（Gemini/Claude，丢了 400）、**EncryptedContent**（OpenAI o 系） |
| `function_call` | 模型发起的工具调用 | 同上（签名附着在调用上） |
| `function_call_output` | 客户端回传的结果 | — |
| （`RefID`） | Responses 的 item_reference | 展开由状态缝合层完成 |

**为什么这两类字段是命门**：它们是加密的、无法从明文重建的。一旦在协议桥接中丢失，上游直接拒绝续轮。

### 4.3 事件侧（Provider → 内核）

刻意保持极简：

```go
EventStart            // 携带上游会话 ID，供状态缝合
EventReasoningDelta   // 思考片段
EventTextDelta        // 正文片段
EventToolCallStart    // 工具调用开始
EventToolCallDelta    // 参数增量
EventUsage            // Token 统计
EventError / EventDone
```

复杂协议差异（Responses 的具名事件树、Anthropic 的 content_block 索引）**全部由出站 transducer 自动补全**。渠道插件零协议负担。

---

## 5. 状态缝合层（StateSuture）—— 项目的护城河

三个表面不同的问题是**同一个问题**：「下游无状态，上游有状态，网关必须替两边记住状态」。

| 表面问题 | 本质 |
|---|---|
| `previous_response_id` 寻址 | 客户端无状态 → 需要找回原会话 |
| 有状态上游（conversation_id） | 上游有状态 → 需要替它维持 |
| 思考签名回缝 | 跨轮信息 → 丢了就 400 |

因此它们被合并为**一个**中间件 `stateful_session`，共享同一份上下文快照。

### 5.1 三大收益

```text
[Codex CLI] --1轮--> 网关分配 Account_A，上游会话 Session_WB_998
                     网关缓存 resp_001 → {Account_A, Session_WB_998, 全量Items}

[Codex CLI] --2轮--> previous_response_id: resp_001
          │
          ├─ 收益1【100% 精准粘性】直接查表锁定 Account_A，无需消息 Hash
          ├─ 收益2【零冗余】上游 Session 已记住第一轮，只转发增量
          └─ 收益3【无状态兜底】Account_A 挂了 → 用缓存的全量 Items 切到 Account_B
```

### 5.2 存储设计（内存 LRU + SQLite）

| 层 | 职责 | 为什么 |
|---|---|---|
| 内存 map + LRU | 快速读（每请求都要查） | SQLite 查询无法满足流式延迟要求 |
| SQLite（WAL） | 跨重启持久化 | 重启后仍能续上正在进行的对话 |

三张表：`responses`（链式快照）、`signatures`（签名）、`sessions`（会话绑定）。
写入在**内存锁之外**（`persistMu` 独立），磁盘 IO 不阻塞读请求。
API 返回**深拷贝**，热替换时调用方不会读到撕裂状态。

### 5.3 签名回缝的两种情形

1. 客户端回传了完整 `function_call` → 直接补 `ThoughtSignature`；
2. 客户端只回了 `function_call_output`（多数 OpenAI 客户端）→ 在其**前面**补一个带签名的 `function_call` item。

---

## 6. 补偿中间件（可组合管道）

| 中间件 | 解决的上游痛点 | 机制 |
|---|---|---|
| `xml_tool_call` | 不支持原生 Function Calling | 入站：Tools 序列化为 XML 提示词注入；出站：流式 FSM 扫描 `<tool_call>`，截断文本流转为标准 ToolCall 事件 |
| `stateful_session` | 有状态上游 / previous_response_id / 签名回缝 | 见第 5 节 |
| `system_role_merge` | 不接受 system 角色、连续同角色消息 | Instructions 折入首条 user；合并相邻同角色 |
| `spec_overrides` | 上游必须带特定系统提示 / 缺省参数才工作 | spec 声明 `system_prompt_inject`（追加到 Instructions 末尾）与 `request_overrides`（仅作缺省值，客户端显式参数优先）；改 spec 即生效，见 docs/ADD_PROVIDER.md 4.6 |

**`xml_tool_call` 的流式 FSM** 是技术含量最高的部分：

- 支持 3 种标签别名（`tool_call` / `tool_use` / `function_call`）
- 处理标签被**分片切断**（`<tool_ca` + `ll>`）——通过"扣住可能是半个开标签的尾巴"实现
- 未闭合时不丢内容（超 256KB 判定跑飞，按文本吐出）
- 参数兼容对象 / 字符串两种形态，自动剥 ```json 围栏
- 6 种分片粒度 × 4 种格式变体全覆盖（单测）

挂载由渠道 Manifest 声明：`RequiredMiddlewares: ["xml_tool_call", "stateful_session"]`。

### 6.1 网关层的三个全局增强（2026-10-27 对标批次）

它们刻意**不做成渠道中间件**，因为必须对全部渠道一致生效、且要支持运行时开关（渠道管道是按 Manifest 缓存的）：

| 增强 | 位置 | 关键约束 |
|---|---|---|
| 隐私遮蔽 | `gateway/privacy.go`，serve() 解析后、调度前就地遮蔽，回复流式还原（占位符跨 chunk 缓冲） | 映射只存本请求内存；开启时 Fast-Path 自动关闭（直通无遮蔽时机） |
| 用量采集 | `gateway/usage.go`，包装 IR 事件流统计 usage，日聚合落盘 `data/usage.json`（90 天） | Fast-Path 字节流不经过 IR，只记请求数；成本按价格表查询时计算 |
| 运行时设置 | `gateway/settings.go`，`data/settings.json` 覆盖 cockpit.json 对应字段 | 隐私开关 / 价格表 / 虚拟组覆盖免重启生效 |

### 6.2 库（prompts / MCP / skills）

`internal/core/library`：三类条目统一存 `data/library.json`，应用时写入各客户端文件——记忆文件（CLAUDE.md / AGENTS.md / GEMINI.md）用 `<!-- aether-library:prompt <name> begin/end -->` 托管块（同名块整体替换，块外内容一字不动，孤立 begin 报错拒写）；MCP 合并进 Claude Code `~/.claude.json`、Codex `config.toml`（每 server 一个托管块）、Gemini CLI `settings.json` 的 `mcpServers`；Skills 写 `~/.claude/skills/<name>/SKILL.md` 与 `~/.agents/skills/<name>/SKILL.md`。所有写操作先备份（保留 5 份）再原子写。

---

## 7. 双轨路由（Fast-Path vs IR-Path）

```text
下游请求进入
      │
      ├─ 同构判定：下游是 Responses 且上游声明 NativeResponses 且无 previous_response_id
      │            （非流式还要求 ResponsesPassthroughNonStream）
      │     └─▶ 【Fast-Path】只改 Auth/model，其余字节原样透传
      │           价值：省一次完整序列化；保留上游实验性字段
      │
      └─ 其余 ─▶ 【IR-Path】解析 → 中间件 → 渠道 → 出站状态机
```

**"无 previous_response_id"是硬约束**：带它说明客户端依赖网关自己维护的会话，直通后上游不认识这个 resp_id，必然 400。

**零损的验证方法**：mock 上游在 SSE 里塞非标准字段 `x_vendor_meta` + 专有响应头。直通时客户端能收到，走 IR 时它必然消失。

### 模型路由的五级漏斗

1. 精确 ID（`workbuddy-cn/claude-4-sonnet`）
2. 虚拟聚合组（`auto/free` → 按配置的渠道链 fallback）
3. 裸模型名（`claude-4-sonnet` → 所有提供它的渠道）
4. 已注册渠道前缀透传（`mock-native/任意新模型`）
5. **模型家族匹配**（`claude-*` → 提供同族的渠道）→ 都失败则**报错并列出可用模型**

第 5 级的存在理由：客户端会发新版模型号（如 `claude-sonnet-4-6-20260101`），家族匹配能路由到对的渠道；而"完全未知就静默兜底"会让拼写错误变成静默用错模型。

---

## 8. 账号池与路由

### 8.1 五态状态机

`Ready` / `CoolingDown`（429，自动恢复）/ `Depleted`（额度耗尽，到点恢复）/ `Invalid`（凭证失效，需重登）/ `Refreshing`（刷新锁）

时间窗口到点自动迁移，不需要外部定时器驱动状态转换。`Refreshing` 由令牌保活循环与手动刷新共用；刷新临时失败时恢复到刷新前的状态，而不是直接判死。

### 8.2 三级漏斗

```
1. 状态过滤    只留 Available；失效/冷却的不参与（只会刷失败并加重风控）
2. 强绑定      previous_response_id 指定的账号，只要可用就必须用
3. 粘性命中    会话指纹 15 分钟内绑定的账号
4. 加权额度    剩余额度 × 临期权重 / (并发+1)
```

临期权重：24h 内清零的积分，权重从 1 线性抬到 2 —— **优先榨干即将过期的额度**。

### 8.3 一号一沙箱

每个账号独享 `http.Client`：独立 CookieJar + 独立 TLS 会话缓存 + 独立连接池 + 独立出口。账号之间在网络层**物理隔离**。

---

## 9. 签到调度引擎（DAG + cron 双层）

分两层的理由：**cron 表达时间，DAG 表达依赖与并发，缺一不可**。

```
Job 层（唤醒源）             DAG 层（执行编排）
per-task cron 9:00  ──────▶  refresh_token ──▶ daily_checkin ──▶ bonus
per-task cron 10:00 ──────▶      (9:00 触发时 bonus 未到点，跳过等下次)
系统唤醒补签        ──────▶  整图执行，已完成节点自动幂等跳过
手动触发            ──────▶  force 模式清空今日状态后重跑
```

### DAG 引擎特性

| 特性 | 实现 |
|---|---|
| 拓扑分层 | Kahn 变体，同层按定义顺序（结果可复现） |
| 环检测 | 分层时剩余节点无法归层即报错；图非法时**退化为无依赖执行**而非整个渠道瘫痪 |
| 失败传播 | 前置失败 → 后继 Skipped；前置是 **Optional** 失败 → 后继照常（"猫猫旅行"下线不连累"刷新额度"） |
| NotBefore | 节点携带当日计划时刻，9:00 触发时 10:00 的任务不会跟着跑 |
| 并发度控制 | 每层并发数可配；签到场景锁死为 1 |
| panic 保护 | 单节点实现里的空指针不会崩掉整个调度器 |

### 反风控语义

- 同渠道账号**串行**（队列），节点间 2~6 秒**裁剪正态分布**间隔（比均匀分布更接近真人）
- cron 触发带**确定性抖动**：`FNV(渠道|任务)` 派生偏移，重启后可复现，不漂移
- 同账号同时刻去重：`running` 表标记，多个 cron 同时到点只跑一次
- 失败不覆盖已有的成功记录（避免重试失败把状态冲掉）

---

## 10. 认证中心（auth 包）

四种登录方式收进一套接口，渠道只声明参数：

| 方式 | 机制 | 渠道需要提供 |
|---|---|---|
| **PKCE 本地回环** | 起临时 HTTP 服务拦授权码，换完即关 | `authorize_url` / `token_url` / `client_id` |
| **设备码轮询** | RFC 8628：拿 user_code 给用户看，后台按 interval 轮询 | 同上 + `device_code_url` |
| **本机凭证导入** | 读客户端已登录的配置文件 | 实现 `LocalImportCapability` |
| **手动粘贴** | 直接入库 | 表单 schema |

PKCE 的安全细节：state 严格校验（防 CSRF）、错误响应区分 `error` 与 `error_description`、会话 10 分钟过期、固定端口被占用自动退随机端口。
设备码的错误码分治：`authorization_pending` 继续等 / `slow_down` 按规范加 5 秒 / `expired_token` 与 `access_denied` 终止。
`RefreshToken` 是独立方法，供渠道的 `TokenRefreshCapability` 与定时刷新复用。

**spec 覆盖**：`spec.oauth` 可以覆盖 authorize / token / device_code 端点、client_id、scope、
字段名与 redirect_path。上游换 client_id 或端点时只改 JSON，不用重新编译。

**定时保活**：`Gateway.StartTokenRefresh` 每 10 分钟检查一次，距离过期不足 30 分钟
或账号处于 `Invalid` 且手里有 refresh_token 时触发刷新；成功发 `account.refreshed`，
失败发 `account.refresh_failed` 并恢复原状态。

---

## 11. 网络底座

### 11.1 区域感知分流

| 策略 | 行为 | 适用 |
|---|---|---|
| `direct_force` | `Proxy=nil`，彻底断开与 `HTTP_PROXY` 的联系 | CN 渠道（否则海外 IP 登录国内账号触发风控） |
| `proxy_first` | 走全局代理池 | 国际渠道 |
| `system` | 跟随环境 | 通用 |

**TUN 模式是绕不过去的坑**：Clash TUN 劫持网络栈而非环境变量，`direct_force` 失效。网关启动时检测（枚举 `tun`/`clash`/`mihomo` 等网卡名）并明确告警。

### 11.2 uTLS 指纹伪装

标准库 ClientHello 的 JA3/JA4 特征一眼可辨。uTLS 重放真实浏览器的握手：

```json
{"tls_profile": "chrome_131"}
```

实现只替换 `DialTLSContext`，代理与连接池策略不变 —— "要不要伪装"是可开关参数而非两套代码路径。12 种 profile（chrome/firefox/safari/edge/ios/android）。未知 profile 回退 Chrome Auto 而不是报错。

### 11.3 线缆编解码（wirecodec）

| 解码器 | 用于 |
|---|---|
| `SSEDecoder` | 标准 text/event-stream（手写，突破 bufio.Scanner 的 64KB 行限制） |
| `AWSEventStreamDecoder` | Kiro 的 AWS 二进制帧（Prelude CRC + Headers + Payload + Message CRC），手写 120 行代替引入 aws-sdk |
| `NDJSONDecoder` | 逐行 JSON |

CRC 双向校验（篡改/截断有单测）。

### 11.4 凭证安全

`accounts.json` 用 **AES-GCM + 本机派生密钥**（hostname + userConfigDir）加密。防"文件被同步到网盘""拷到别的机器"，不防本机攻击者（密钥就在本机，防不了）。

---

## 12. 桌面端（Wails v2）

```text
cmd/cockpit   headless：net/http 暴露服务，服务器/命令行
cmd/desktop   桌面端薄入口：转调 internal/desktop
main.go       Wails CLI 根入口：同样转调 internal/desktop
internal/desktop  桌面端实现：Wails 原生窗口 + 系统托盘
```

两者**共用同一个网关内核与同一套前端资源**。桌面端网关在进程内启动，前端同源 fetch `/api/*`，无需跨进程通信。
Wails CLI 要求在项目根目录有 main 包，所以根目录保留薄入口；`wails.json` 的 `assetdir` 指向
`internal/web/ui2`（原生 ES module，无构建步骤），`frontend/` 只剩 no-op 占位以满 wails 的前置命令。

| 特性 | 实现 |
|---|---|
| 前端托管 | `assetserver.Options.Handler` 组合：`/api/*`、`/v1/*` 走网关，其余走内嵌静态资源（SPA 回落 index.html） |
| 系统托盘 | **Wails v2 无内置托盘**（v3 才有），用 `energye/systray` 补；回调式 API |
| 托盘图标 | **代码生成 ICO**（32×32 径向渐变圆 + 中心暗点），换配色改代码不换资源文件 |
| 端口冲突 | 自动向后找可用端口，第二个实例也能起 |
| 菜单 | 打开界面 / 复制地址 / 一键签到 / 退出 |
| 打包 | `wails.json` + `build/appicon.png`；`wails build` 产出 exe，`-nsis` 产出安装包（已生成 `AetherCockpit-amd64-installer.exe`） |

---

## 13. 与最初方案的差异对照

诚实记录每一处偏离及原因：

| 原方案 | 实际落地 | 原因 |
|---|---|---|
| （第一版）Wails 桌面端 | 先内嵌 Web UI，后按要求补 Wails | 用户最终要求补齐，已实现 |
| （第一版）Gemini `/v1beta` | **砍掉** | 用户明确允许砍 |
| （第一版）DAG 签到引擎 | 先用有序数组，后按要求补真 DAG | 用户要求补齐，已实现 |
| （第一版）SQLite | 先 JSON 快照，后按要求补 SQLite | 同上 |
| （第一版）事件总线 | 先没做，后按要求补 | 同上 |
| （第二版）`reasoning_summary_part` 生命周期 | **补齐**（原方案遗漏，会导致 Codex CLI 解析失败） | 审方案时发现 |
| （第二版）Responses Stream Transducer | 补齐 + 加 `sequence_number` 与 `response.failed` | 同上 |
| （第三版）"当前请求无缝切换渠道" | **修正**：只能在首字节之前切换 | 流式输出一旦开始，上下文/签名/工具状态跨渠道不通用，硬切会产生语义错乱 |
| （第三版）"跨渠道虚拟模型池" | 实现 + **加约束**：流中不可切换 | 同上 |
| （第三版）13 个渠道 | **口径修正为 10 个 ProviderID**；10/10 个 ID 均有可执行代码，7 个已真实可用（Trae 账号被 4011 限流）；WorkBuddy 2 个与 Kiro 1 个待外部 token 验证 | 外部凭证阻塞；不凭空造 3 个 ID |
| （第一版）5 种登录方式 | 本机导入 / PKCE / 设备码 / 手动凭证已实现；`webview_login` 保留枚举但 UI 明确提示不可用 | Wails v2 没有可拦截回调的内置浏览器 |
| （第一版）本机导入 | 已内置 Codex（TOML）/ OpenCode（JSON）/ Antigravity（Windows 凭据管理器）/ Qoder（DPAPI + AES-GCM）/ Trae（tc 解密 + storage.json）规则；其他渠道由 spec 声明 | 其余客户端目录/格式需外部输入 |
| （第一版）Wails 安装包 | `wails.json` + 图标 + exe + NSIS 安装包均已产出 | 已在构建机验证 |

---

## 14. 请求生命周期示例（Claude Code → openai-compat）

```text
1. Claude Code → POST /v1/messages {"model":"openai-compat/deepseek-chat",...}
2. inbound.ParseAnthropic      → IR（thinking 块带 signature，tool_result 提取）
3. ModelRouter.Resolve         → openai-compat（第 1 级精确命中）
4. pipeline:
   stateful_session            → 计算 SessionKey；首轮无历史
5. pool.Select                 → 状态过滤 → 粘性 → 加权，命中 openai-compat-xxx
6. openaicompat.ChatStream     → 构造 OpenAI messages（tool 链折叠）→ HTTP POST
7. wirecodec.SSEDecoder        → 逐帧解析
8. [假上游返回] delta.content  → EventTextDelta / delta.tool_calls → ToolCallStart+Delta
9. StateSuture（出站侧）       → 聚合完整输出，写入 resp 快照 + 签名
10. AnthropicTransducer        → message_start → content_block_start → delta → stop → message_stop
11. Claude Code 收到标准 Anthropic 流 ✅
```

同一个上游，换 `/v1/responses` 请求 → `ResponsesTransducer` 输出完整 item 生命周期树（27 个事件）；换 `/v1/chat/completions` → Chat chunk 流。**一个真实上游，三套下游协议全支持。**

---

## 15. 工程目录

```
main.go          Wails CLI 根入口（转调 internal/desktop）
wails.json       打包配置
build/           应用图标 + Wails 产物
cmd/
  cockpit/      headless 入口
  desktop/      桌面端薄入口
  smoke/        一键验收（28 项）
configs/
  specs/        渠道参数（热更新）
docs/
  ADD_PROVIDER.md   接渠道手册
  ARCHITECTURE.md   本文档
scripts/
  fake-upstream.py  零依赖假上游
internal/
  desktop/      桌面端实现（窗口 + 托盘）
  core/
    ir/          中间表示（最稳定）
    gateway/     inbound + transducer + fastpath + tokenrefresh + 路由
    pipeline/    中间件
    pool/        账号池
    state/       状态缝合存储（LRU + SQLite）
    scheduler/   DAG + cron 调度
    spec/        热更新 + OAuth 覆盖 + 签名
    auth/        PKCE / 设备码 / 刷新
    eventbus/    事件总线
    transport/   dialer + uTLS + wirecodec
    simplecrypt/ 本机绑定加密
  provider/      契约与注册中心
  providers/     渠道实现（openaicompat / reverse 2 骨架 / antigravity qoder trae workbuddy kiro 独立实现 / mock 验证 / _template 模板）
  web/           前端（内嵌，Manifest 驱动渲染 + SSE）
```

---

## 16. 质量基线

| 指标 | 数值 |
|---|---|
| 单元/集成测试 | 368 个测试函数全绿 |
| 端到端验收 | `cmd/smoke` 28 项全过 |
| 并发正确性 | 36 并发混合协议无串扰（无 gcc，`-race` 不可用；并发回归依赖交错与超时用例） |
| 外部依赖 | 直接依赖 4 个：utls、sqlite、wails、systray（均为方案明确要求），其余为 Wails 传递依赖 |
| 第三方依赖里最重的 | `modernc.org/sqlite`（纯 Go，首次编译慢） |
