# aether 与 Aether Cockpit：实现级深度对比

> 调研日期：2026-11-02
> 对象：`github.com/aether/aether`，参考树 `1fac66c`（2026-11-01）；Aether Cockpit `0.1.7`
> 方法：逐文件读源码核对，不以 README 功能表为准；行号是本次快照，后续可能漂移

## 0. 结论

两个项目已经不是同一类产品：

- **aether 是“平台”**：插件运行时、约 71 个 Agent 接线、Claude Code 真进程桥、十种路由策略 + 嵌套组、本地 PII 模型、OTLP metrics。它可以把新渠道、新策略、新工具做成插件发出去，不必改内核。
- **Aether 是“产品”**：Windows 桌面交付、四协议入口（含 Responses WebSocket）、21 个内建渠道、额度窗口/模型目录/请求日志、2FA、媒体与实时接口。开箱即用和日常运维体验更好。

Aether 在共有能力上真正“有但更差”的是三处：**路由组语义、配置文件保真编辑、隐私模型**。另有三个自身问题：**Responses 直通绕过脱敏规则、路由组的 weight/free/manual 选项被静默降级、额度查询覆盖面偏窄**。

## 1. 能力矩阵

| 能力 | Aether 0.1.7 | aether `1fac66c` | 判定 |
| --- | --- | --- | --- |
| 下游协议 | Chat / Responses（HTTP+WS）/ Anthropic / Gemini `/v1beta`，另有 compact、count_tokens | Chat / Responses（仅 HTTP，WS 明确 426）/ Anthropic / Gemini | **Aether 领先** |
| 上游渠道 | 21 个内建渠道，含 Cursor/Qoder/WorkBuddy/ZCode/Zed/Kiro/Devin | 内建订阅 + 插件生态；11 类订阅已迁到社区插件 | **各有所长** |
| Agent 接线 | 17 个；JSON 合并、TOML、env、YAML 行编辑 | 约 71 个内建 + 16 个 WSL + 插件 Agent；JSON/JSONC/TOML/YAML/env 保真编辑 | **aether 领先** |
| 配置编辑安全 | 备份、原子写、Preview/Restore；OpenCode 注释 JSONC 被拒，VS Code 重写丢注释 | 原子写 + stash/undo + drift，格式保真 | **aether 领先** |
| 路由策略 | priority/rotate/pace/smart/fast 五种；无嵌套组 | order/drain/rotate/usage/pace/weight/free/cheap/fast/smart 十种；嵌套组、组规则 | **aether 领先** |
| 缓存亲和 | 账号级 sticky，按 cache read/write 调 TTL，1024 token 阈值 | session/turn/off/auto，持久化 512 条，按缓存温度让位 | **aether 略强** |
| 账号调度 | 优先级层、OfficialFirst、权重、确定性评分、RPM/并发/排队 | 分层、rank、权重、账户 caps/窗口 caps、adaptive effort | **平手** |
| 插件运行时 | 插件页端点是返回空数组的 stub；只有 6 个编译进内核的中间件 + 4 种配置规则 | Manifest 生命周期、JS `onRequest/onProvider/onAccount/onEvent/onResponse`、Bun provider ABI、运行时策略 | **aether 领先（最大架构差距）** |
| Claude 订阅 | OAuth 凭证复用，走标准 `/v1/messages` | 真 Claude Code 二进制 + PTY/TUI + MCP 工具桥 + resume/fork + 限流头 | **aether 领先** |
| Codex | 反代 Chat、下游 WS、compact 端点；自动续接仅 fast path | compact guard、原生 sealed compaction、auto-reset、subagent resume、退出后 relay | **aether 领先** |
| 隐私 | 20 条正则 + 可选外部 sidecar；模型数 0 | 正则 + 39MB 本地分类器 / 1.6GB Privacy Filter，自动下载、GPU/CPU、fail-closed | **aether 领先** |
| 可观测 | 逐请求账本、tok/s、分页、错误解释、详情抽屉；OTLP 仅 traces | usage/cost/sessions/bills + OTLP traces + metrics | **各有侧重** |
| 共享/部署/备份 | 调用方密钥加密存储、RPM/token/费用限额、LAN、WebDAV/S3/GitHub、可选明文备份 | 短期密钥、issuer 预算、Remote aether、Docker、daemon、强制加密备份 | **aether 领先** |
| Windows 产品化 | 便携 zip、SHA 校验更新、安装器、自启、托盘、2FA、中文 UI | Wails v3 beta、跨平台、主题/WSL；daemon 不支持 Windows | **Aether 领先** |
| 额度/模型目录 | 10 个渠道声明额度查询；Qoder/Antigravity/Gemini/Claude 实机通过；models.dev + 实时目录 | 订阅用量覆盖更广；models.dev + 厂商目录 + Codex 缓存 | **各有侧重** |
| MCP/媒体 | MCP server + relay + OAuth PKCE + 图片/视频/音频/实时 WS | MCP router + image tools；无实时/音频端点 | **各有侧重** |

## 2. aether 领先的五个架构点

### 2.1 插件运行时是可扩展平台的核心

aether 的插件不是配置项，而是可执行扩展：

- `internal/pluginrt/pluginrt.go`：`Register(Manifest)`、`Reconcile(on, config)` 生命周期、依赖序、`OnDispose/Contribute/Provide/Service`。
- `internal/gwmw/gwmw.go:27-141`：网关中间件 `Begin(Info) Run{Request,Wrap,End}`，含 `Tried`、`Accounts`、`Masker`、`Teller`。
- `internal/middleware`：moejs 纯 Go JS 引擎，导出 `onRequest/onProvider/onAccount/onEvent/onResponse/strategies/onUsage`；`onRequest` 限时 250ms，`onEvent` 50ms，策略 20ms。
- `internal/plugin/host.js`：Bun provider 插件 ABI，支持 `config/auth/provider/tool/chat.headers`，HTTP 经宿主 RPC 代理。
- 插件页面/面板是 iframe `allow-scripts`，声明式读取 `usage:summary`/`providers:list`，最多 8 个面板/动作。

Aether 的 [plugins_api.go:169](../internal/core/gateway/plugins_api.go#L169) 明确是 `handlePluginStub`，`plugin-pages/plugin-panels/plugins/listings` 一律返回空数组；[mw/engine.go:27](../internal/core/mw/engine.go#L27) 只有 `param_override/model_map/word_guard/system_prompt` 四种规则；[pipeline/chain.go:38](../internal/core/pipeline/chain.go#L38) 的中间件是 `init()` 注册的 Go 函数，第三方无法新增；[gateway.go:1702](../internal/core/gateway/gateway.go#L1702) 的链顺序是硬编码的 `redact → rewrite → spec_overrides → manifest`。

**影响**：aether 新增一个渠道/策略可以发 npm 包，Aether 必须发版。Aether 的渠道数量长期落后，根因不在“渠道实现难”，而在“没有第三方扩展面”。

### 2.2 Claude 订阅桥是“用 Claude Code 本体”

aether 不是只复用 OAuth token：

- `internal/claudecode/claudecode.go:1-15,38`：优先 PATH 里的 `claude`，否则下载 Anthropic 原生构建并校验 SHA-256。
- `internal/gateway/claude_subscription.go:5276`：按会话复用运行，`start/startRun/resume`，stream-json 进出。
- `internal/claudetui/tui.go:1-17`：默认走交互模式，真 PTY/ConPTY，粘贴用户文本 + Enter，tail transcript，Bun `--preload` 拦截 `Bun.FetchSession`。
- `internal/claudebridge/mcp.go:1-57`：把 aether 工具经 stdio MCP server 暴露给 Claude，工具名 `mcp__aether__<name>`。
- 会话 `resume/fork/rewind`、`rate_limit_event` 转 `anthropic-ratelimit-unified-*`、进程房/RSS 预算。

Aether 的 [claude/provider.go:1](../internal/providers/claude/provider.go#L1) 只是拿 OAuth token 调标准 Messages API。对“把订阅算力给任意客户端”这个目标够用；对“把 Claude Code 的会话/工具也接进来”没有。

### 2.3 Agent 接线广度与配置保真度

aether：`internal/agent/agents.go:216-288` 约 71 个内建 Agent，加 16 个 WSL 变体，加插件 Agent。`internal/edit/file.go:1-5` 对 JSON/JSONC/TOML/YAML/env 全部做格式保留编辑；写前校验，非法结果拒绝；原子 temp+fsync+rename，保留 mode/symlink/hardlink/BOM；`stash.json` + `applied.json` 支持 undo/drift。

Aether：17 个（`internal/core/clients/clients.go:228-278`、`agents_extra.go`、`zed.go:42`）。OpenCode 分支明确拒绝带注释 JSONC（`clients.go:865-868`）；VS Code 整文件重写会丢注释（`agents_extra.go:1104-1151`）；Goose YAML 是行级编辑。`drift_api.go:69-74` 只读且分类粗糙，不能一键修复。

**影响**：Aether 接得少、改得粗；用户手改过的配置可能被重写破坏。

### 2.4 路由组语义

aether：`internal/routes/strategies.go` 有 order/drain/rotate/usage/pace/weight/free/cheap/fast/smart 十种；`provider/grouprule.go:74-77,568` 支持按 intent/effort/时间/区域出规则；`gateway/affinity.go:1-27,433,524` 持久化 session/turn/off/auto 亲和；嵌套组按内层路由递归。

Aether：[model_route.go:114-144](../internal/core/gateway/model_route.go#L114) 只有五种；[groups_api.go:68](../internal/core/gateway/groups_api.go#L68) 把 `order/free/weight/manual` 全部落到 `priority`；[groups_action_api.go:57](../internal/core/gateway/groups_action_api.go#L57) 仍保存 `weights/rules/levels/match`，但没有路由读取；[groups_api.go:158](../internal/core/gateway/groups_api.go#L158) 把成员过滤成“只保留已注册 provider”，嵌套组不支持；组级 `affinity` 在 `defFromPayload` 被丢弃。

**影响**：UI 上有的选项，内核没实现。这是“有但更差”里最需要先修的一处。

### 2.5 隐私模型与 OTLP metrics

aether：`internal/pii/pii.go:1-44` 会自动下载 39MB int8 分类器（AstrLink Guard）或 1.6GB Privacy Filter（GGUF，pf-serve），自动选 GPU/CPU，崩溃重启，连续失败时挡住请求；正则 + 模型两层。

Aether：[privacy_sidecar.go:17](../internal/core/gateway/privacy_sidecar.go#L17) 要求用户自己提供可执行文件，`masking_api.go:262` 明确 `model:0`；[redact.go:988-1038](../internal/core/redact/redact.go#L988) 有 20 条规则（含 IBAN mod-97、私钥、JWT、身份证等），[privacy.go:193-197](../internal/core/gateway/privacy.go#L193) 只遮 `Instructions/Text/Arguments/Output/FileName`，不遮附件 URL/base64。OTLP 方面，[otlp.go:21](../internal/core/gateway/otlp.go#L21) 只发 traces；aether `otel.go:308-316` 还发 duration/token metrics。

**影响**：Aether 对“自由文本里的姓名、地址、无上下文密码”没有检测能力；正则规则之外的 PII 会漏。

## 3. Aether 不差、甚至更好的地方

- **协议面更宽**：Aether 有下游 Responses WebSocket（`GET /v1/responses`，`gateway.go:637`），aether 对 WS 直接返回 426（`gateway.go:954`）。Aether 还有 `/v1/responses/compact`、`/v1/systemone`、realtime、音频、视频。
- **内建渠道更省事**：aether 因厂商条款把 Cursor/Qoder/WorkBuddy/ZCode/Zed/Kiro/Devin/Grok/MiMo/Factory/Command Code 等 11 类订阅迁到社区插件；Aether 其中 7 类是一键内建。开箱体验更好，代价是厂商条款/封号风险自担。
- **额度窗口 UI 更深**：Qoder 的 Pro/Add-on 双窗口、Antigravity 的 5h/7d、Claude 的 7d、限流窗口可见，均有实机验证；aether 覆盖面更广，但 Aether 的展示粒度不差。
- **请求日志更适合排障**：tok/s、分页、错误解释、详情抽屉、会话用量，是 Aether 的强项；aether 强在 OTLP/账单导出。
- **调用方密钥更安全**：Aether 的 `aek_` 密钥 AES 加密落盘（机器绑定），列表只给掩码，支持轮换，限额含 RPM/token/费用（`callerkeys.go:29-38,297-390`）；aether 的 `caller-keys.json` 明文保存 `Secret` 字段，密钥限额只有 token/费用，没有 RPM。
- **调度正确性有护栏**：Aether 的策略结果必须是输入排列，否则丢弃；panic 自动回退原顺序（`model_route.go:541-575`）。aether 也有 fail-open，但 Aether 这层不差。
- **Windows 交付完整**：便携 zip、SHA 更新、安装/卸载、自启、托盘、单实例、2FA；aether 的 daemon 在 Windows 上不可用。
- **MCP 安全面更完整**：Aether 有 MCP server、relay、RFC 9728/8414 发现、动态注册、PKCE、SSRF 防护；aether 强在 MCP router 聚合，但插件宿主是“一个 Bun 进程、用户权限、无沙箱”（`docs/subsystems/plugin-tools.md:32`），Aether 不应照抄这一点。

## 4. 审计发现的自有缺陷

> 修复状态（2026-11-02）：本节 P0/P1/P2 六项已在当前工作树修复，详见 `CHANGELOG.md`「未发布」。下面保留原始审计描述，便于回溯。

### P0：Responses 直通绕过脱敏规则

`tryFastPath` 在 [gateway.go:1240](../internal/core/gateway/gateway.go#L1240) 早于 parse/pipeline 执行；[fastpath.go:42](../internal/core/gateway/fastpath.go#L42) 只检查 `privacyState()`（隐私引擎），不检查 redact 引擎。结果是：**只开“脱敏规则”而未开 `privacy_mask` 时，Responses 流式直通请求不会脱敏。**

修法：fastpath 入口增加 `redactEngine().Enabled()` 判断；或把 redact 的“是否有启用规则”暴露成快速布尔值。需要补一条回归测试：开启脱敏规则 + Responses 流式 + 原生直通渠道，断言上游收到的 body 不含原始 secret。

### P1：路由组选项静默降级

`groups_api.go:68-83` 的 `default` 分支把 `order/free/weight/manual` 全映射到 `priority`；`weights/rules/levels/match` 只序列化、无 reader；组级 `affinity` 被丢弃；嵌套组不支持。UI 上“权重/免费优先/手动”看起来可用，实际不生效。

修法（二选一）：

1. 真实现：weight 用 smooth WRR；free/cheap/fastest 按价格/延迟排序；嵌套组递归解析；组规则/levels 接进 router；组级 affinity 落库。
2. 先移除未实现的选项和字段，避免继续误导用户。

建议先做 2 再逐步做 1，至少让 UI 与内核一致。

### P1：额度覆盖仍窄

manifest 里声明 `Quota:true` 的只有 10 个真实渠道（antigravity/claude/cursor/githubcopilot/kiro/minimax/openaicompat/qoder/workbuddy/zed）。Codex、Gemini API、Trae、ZCode、CodeBuddy、Windsurf 等仍可能没有窗口，这就是“很多渠道获取不到额度”的根因，不是 UI 问题。

修法：按渠道补 usage 端点；补不了的，在账号卡上明确显示“该渠道不支持额度窗口”，不要留空白让人误以为加载失败。

### P2：备份与上下文续接的边界

- [sync_crypto.go:56](../internal/core/gateway/sync_crypto.go#L56)：空口令直接明文上传；PBKDF2 120k 低于 aether 的 600k。备份已排除凭证/代理/OTLP headers/远端密钥，但设置、Library、Spec、图标仍可能包含敏感信息。
- [context_continue.go:100](../internal/core/gateway/context_continue.go#L100)：只在 fast path 调用（`fastpath.go:161`）；IR 路径和 `/v1/messages` 不生效。
- `drift_api.go` 只读且分类粗糙，不能一键修复。

## 5. 优先级建议

> 进展（2026-11-02）：P0 第 1/2/3 项已完成——fastpath 已补 redact 判定并加回归测试；路由组已改为“未实现字段不持久化 + `routingEffective`/`warnings`”；账号卡已区分“不支持额度”。P1 第 4 项的 drift apply 已完成，Agent 广度与格式保真仍开放；第 5/6 项仍开放。

### P0（先修正确性/安全）

1. 堵住 fastpath 脱敏绕过，补回归测试。
2. 路由组“要么实现、要么移除”：先移除假选项，再做 weight/free/cheap/fastest/嵌套组/组规则。
3. 额度补齐 Codex/Gemini/Trae/ZCode/CodeBuddy/Windsurf；不支持的渠道明确标注。

### P1（补平台能力）

4. Agent 扩到 Top 20，并把 JSONC/TOML/YAML 全部换成格式保真写入 + drift apply。
5. 做一个最小可用的扩展层：声明式 provider spec（JSON/HTTP）+ JS/WASM 中间件沙箱；先把插件页的空 stub 改成真实能力或明确“未实现”。
6. 隐私模型：内置/可下载小分类器，默认 fail-closed；至少让“规则脱敏”在所有路径生效。

### P2（追平深度）

7. Claude Code 真进程桥（如果目标用户是 Claude Code 订阅用户）；否则文档明确“仅 API 复用”。
8. Codex compaction/auto-reset/subagent resume；OTLP metrics；备份强制加密 + 600k；Docker/daemon。

## 6. 不应照抄的 aether 设计

- **插件宿主无沙箱**：一个 Bun 进程、用户权限，插件能读用户文件。Aether 若做插件，必须先设计权限/沙箱模型。
- **订阅迁移复杂度**：11 类订阅迁到社区插件，带来 `moved/retired/unmoved` 状态、mover、parity test。Aether 内建渠道更简单，但需自行承担厂商条款风险。
- **协议文件巨石化**：aether 的 `anthropic.go`/`chat.go` 单文件 50–66KB，host 策略与协议形状不分层；Aether 的 transducer/极简事件方向更可维护，不要为了“功能多”把策略塞回单个转换器。
- **包级全局可变状态**：aether 的全局 redact map、全局 sticks 与多租户方向有张力。
- **Claude 桥的脆弱性**：依赖真实 CLI 版本、PTY、`--dangerously-skip-permissions`、回环 MCP；Aether 的 OAuth 直连更轻，是否要桥取决于产品目标。

## 7. 取证边界

- 本对比是静态源码审计：aether 侧未实际运行插件、Claude 桥、Codex relay；Aether 侧的额度/OAuth/Claude/Codex 核心有单元测试与部分实机证据。
- aether 参考树为 `1fac66c`（2026-11-01），Aether 为 `0.1.7`；行号是本次快照。
- 未覆盖：aether 的完整 provider 数量、所有 Agent 的实际配置格式、插件市场的真实包质量；Aether 的 Wails 桌面端在非 Windows 平台的行为。
- 结论会随两边发版变化；涉及“更差/领先”的判断，以本文列出的代码路径为准，不引用 README 功能表。
