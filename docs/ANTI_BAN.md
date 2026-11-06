# 反代账号风控：机制、现状与工程建议

> **文档性质**：内部工程调研，用于降低"本机网关把订阅账号转发给本地客户端"时的
> 异常判定概率，属于**互操作性与稳定性**范畴。它不是绕过鉴权、伪造签名或规避
> 付费/额度限制的指南；这些做法在 [第 5 节](#5-明确不做的事) 明确排除。
> **结论有时效性**：上游风控策略与客户端版本都在变，本文所有"已做/建议"都应以
> 抓包回归为准，不能当作永久有效的配置。
>
> - 调研日期：2026-11-06
> - 代码基线：`D:\Aether Cockpit`（未提交工作区）
> - 相关既有文档：[反代渠道的"像真客户端"清单](reverse-channel-hardening.md)、
>   [反代渠道关键请求头基线表](reverse-channel-headers.md)

## 0. 结论摘要

1. 封号不是单点触发，而是**多层信号叠加后的风险评分**：出口 IP、TLS 握手特征、
   HTTP 头集合、账号/设备/会话标识、请求时序与并发、请求体特征。
2. **一致性 > 伪装强度**。同一账号在对话/额度/签到/刷新接口上出现两套 UA、两套
   平台、两套版本，比"少一个冷门头"更容易被关联。本仓库已有硬锁在防这类漂移。
3. 最值得先做的一条：**把"一个账号 ↔ 一个稳定出口 IP ↔ 一套稳定客户端身份"变成
   可观测、可告警的约束**，而不是只提供可选项。多数真实封号事故来自"多账号挤同一
   机房出口"或"同一账号出口跳变"，不是缺某个 header。
4. 不做随机轮换：随机换 UA / 随机换 TLS 指纹 / 随机换设备 ID，本身就是异常信号。
5. 任何"像官方客户端"的努力都只降低概率，**不能保证不封号**；最终取决于上游策略。

---

## 1. 为什么反代会触发风控

### 1.1 上游能看到的五层信号

| 层 | 上游能看到什么 | 反代常见的异常点 |
|---|---|---|
| 网络层 | 出口 IP、ASN、地理位置、IP 变更频率、同 IP 账号数 | 多账号共用机房 IP；住宅 IP 与数据中心 IP 来回跳；短时间跨地区 |
| 传输层 | TLS ClientHello（JA3/JA4）、ALPN、HTTP/2 设置 | Go 标准库的固定握手特征与任何官方客户端都对不上 |
| HTTP 层 | Header 名称/取值/顺序、UA、`Accept-*`、客户端提示（Client Hints） | 缺头、多余头、跨接口 UA 不一致、Go 默认 `Go-http-client/1.1` |
| 身份层 | 设备 ID、机器 ID、会话 ID、账号 UUID、billing/attribution 字段 | 设备 ID 每次重生成；会话 ID 每轮变；账号与设备绑定关系漂移 |
| 行为层 | 并发数、请求频率、时间分布、重试模式、多账号关联 | 机器级并发、固定间隔、429 后立刻重试、同一出口挂十几个账号 |
| 载荷层 | 请求体字段集合/顺序、系统提示词、工具定义 | 客户端开场白指纹（如 "You are Claude Code…"）、字段缺失或多余 |

### 1.2 风控是评分，不是开关

厂商通常把上述信号加权成风险分，再决定放行、限流、二次验证或封禁。这带来两个
工程推论：

- **不要"撞"**：命中风险信号后继续高频重试，会把"可疑"升级为"确认滥用"，
  还可能连坐同设备/同出口的其他账号。
- **不要"过度伪装"**：官方 API Key 通道本来就不是浏览器，硬套 Chrome TLS 指纹
  反而更异常；本仓库对官方 API 渠道刻意保留默认传输栈，就是这个原因。

> 依据：[Cloudflare 对 JA3/JA4 的定义](https://developers.cloudflare.com/bots/additional-configurations/ja3-ja4-fingerprint/)
> （TLS 客户端指纹可跨 IP/端口/证书稳定识别客户端类型）；[MDN Client Hints](https://developer.mozilla.org/en-US/docs/Web/HTTP/Guides/Client_hints)
> （UA 与 `Sec-CH-UA-*` 属于可用于 fingerprint 的请求头集合）。这两份是
> "传输层/HTTP 层确实可被识别"的公开技术依据，不代表任何厂商的具体风控规则。

---

## 2. 参考项目与研究结论

### 2.1 `morluto/rea` 是什么（已核实）

**结论：REA 不是反代项目，也不构造上游请求。它是一个"逆向工程分析"工具集，
可以被用来研究官方客户端，但不能直接降低封号率。**

已核实的事实（来源：[github.com/morluto/rea](https://github.com/morluto/rea)，
README 与仓库元数据于 2026-11-06 抓取成功；GitHub API 返回 HTTP 200）：

- 定位："Reverse engineer anything with agents, from app behavior down to native binaries."
  通过 **MCP server + CLI** 让 agent 在本地分析目标，返回带证据的结论。
- 覆盖目标：原生二进制（Hopper/Ghidra/IDA/JEB）、JavaScript/Electron、.NET、
  Android APK、Flutter、Go/Rust 二进制、**网络抓包（HAR / Fiddler SAZ / mitmproxy /
  qlog / PCAP）**、网站结构、进程行为等。
- 许可证 MIT；README 明确其用途是"lawful research, education, interoperability,
  software preservation, and analysis of software you own or are authorized to examine"，
  并附有 `DISCLAIMER.md`。
- **没有**代理/转发功能：抓包分析是**离线**的，文档明确 "recorded URLs are never
  fetched, and no browser or proxy listener is started"
  （来源：[docs/web-network-captures.md](https://github.com/morluto/rea/blob/main/docs/web-network-captures.md)）。
- **没有**"请求构造 / UA 生成 / 签名 / 重试"模块：README 的能力表全部是"分析并返回
  证据"，不是客户端实现。仓库文件列表（`src/`、`bridge/`、`native/`、`docs/` 等）
  中未发现此类能力；本次调研只读了 README、抓包文档与 contents API，**未逐文件审计
  源码**，因此对"完全不存在"只下"未发现"的结论。
- 它能帮到我们的地方：用**自己账号**的 HAR/SAZ 抓包，提取官方客户端在真实请求里
  的 header 集合、重复头顺序、请求体字段与 API surface，作为"我们实现得像不像"的
  对照证据。它的价值是**取证与对照**，不是伪装。

> 说明：REA 的抓包文档强调保留原始 header 顺序、重复 header、请求体字节与
> producer ordinal，正好适合做"官方客户端基线"的回归材料。

### 2.2 通用手段与可查来源

| 手段 | 说明 | 来源与可信度 |
|---|---|---|
| 出口 IP 稳定、类型一致 | 同一账号尽量固定出口；避免多账号共用同一机房 IP；避免住宅/机房来回跳 | [Wokey《如何有效降低 AI 中转站的封号？》](https://wokey.ai/blog/relay-ban-architecture?lang=zh)（**厂商文章，利益相关**，但"IP 是成本最低的筛选条件"与行业常识一致；作者也承认"没有任何方案能保证零封号率"） |
| 请求头与官方客户端对齐 | UA、`Accept`、`Accept-Language`、版本头、客户端自定义头成套一致；同一账号跨接口不换身份 | [MDN Client Hints](https://developer.mozilla.org/en-US/docs/Web/HTTP/Guides/Client_hints)；某开源 API 转发项目的 PR #2489（不点名，原因见下）（已合并的社区 PR，自述基于 source map 逆向，列举了 header/session/billing 一致性差距——**仅作为"上游会看这些维度"的旁证，不采信其具体规避做法**） |
| TLS ClientHello 指纹 | 官方客户端（Chromium/Node/Rust）各有稳定 JA3/JA4；标准库特征明显 | [Cloudflare JA3/JA4 文档](https://developers.cloudflare.com/bots/additional-configurations/ja3-ja4-fingerprint/)（企业版 Bot Management 才提供 JA3/JA4，说明这是真实存在的识别维度） |
| 连接复用 | 同一账号复用连接/连接池，避免"每请求新建连接"的机器味；及时读完并关闭 body 才能复用 | [Go `net/http` 文档](https://pkg.go.dev/net/http#Transport)：Client/Transport 应复用；body 不读到 EOF 并关闭会导致连接无法复用 |
| 请求时序与重试 | 指数退避 + 抖动；尊重 `Retry-After`；避免同步重试风暴 | [AWS Builders Library: Timeouts, retries, and backoff with jitter](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/)；[AWS Architecture Blog: Exponential Backoff And Jitter](https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/) |
| 会话粘性 | 同一会话固定同一账号、同一会话 ID；请求 ID 每请求唯一但会话 ID 跨轮稳定 | 通用分布式系统实践（session affinity）；本仓库已有实现（见第 3 节） |
| 多账号隔离 | 一号一出口、一号一设备指纹；避免"账号 A 的请求带账号 B 的机器 ID" | 同上；本仓库已有账号级代理覆盖与稳定指纹派生 |
| 错误处置 | 识别风控信号（403/429/业务风控码）→ 冷却/暂停，而不是换号硬撞 | [Antigravity-Manager #646](https://github.com/lbjlaq/Antigravity-Manager/issues/646)（社区报告：429 与"疑似被风控"同时出现）；本仓库 `riskpause.go` 已有通用冷静期 |

**社区线索（未能验证，未作为结论依据）**：V2EX 帖 `t/1216972`（"claude 自己搭中转站
如何防止封号? 第二天就被封"）与 linux.do 帖 `t/2983091`（"有网关支持完全透传吗"）
在搜索结果中出现，但抓取时分别遇到 Cloudflare 安全验证与超时，**未读到正文**，
故只在附录 B 记录，不引用其观点。

### 2.3 Top 5 有效手段（按"证据强度 × 可落地性"排序）

1. **出口 IP 与账号绑定且稳定**（一号一出口，优先用户自有网络）。
   IP 是最外层、最低成本的筛选维度；对它做优化收益最大。
   来源：Wokey 文章（厂商视角，需打折看待）+ 本仓库账号级代理能力。
2. **一套客户端身份贯穿所有接口**（UA / 版本 / 平台 / 设备 ID / 会话 ID 不漂移）。
   这是本仓库投入最多、也最容易被破坏的一环；已有硬锁防回归。
   来源：MDN Client Hints + 某开源 API 转发项目的 PR #2489（旁证）。
3. **TLS 指纹与真实客户端匹配**（不是"一律 Chrome"，而是"该客户端本来是什么"）。
   来源：Cloudflare JA3/JA4；本仓库 `utls.go` + `fingerprint_defaults.go`。
4. **行为礼貌化**：每账号/每渠道并发上限、请求间抖动、尊重 `Retry-After`、
   命中风控即暂停（不要重试风暴）。来源：AWS 退避实践 + 本仓库 `riskpause.go`。
5. **会话/请求标识语义正确**：会话 ID 跨轮稳定、请求 ID 每请求唯一、重试不重用
   请求 ID、会话粘滞到同一账号。来源：通用会话亲和实践 + 本仓库 `pool` sticky。

---

### 2.4 参考实现的方法级证据（2026-11-06）

**样本**：用户提供的参考 APK `WorkBuddy_1.0.6.apk.1`。它不是官方客户端，而是同类
产品 `com.joy4fire.wbproxy` 1.0.6（字符串自述「把 WorkBuddy 账号反向代理成
OpenAI / Anthropic 兼容接口」）。**只做只读静态分析，未复制其代码**。

**方法**（按 REA 的分析流程）：
1. 结构：`PK` ZIP；`AndroidManifest.xml` + `classes.dex…classes5.dex` +
   `resources.arsc` + `res/`；**无 assets/、无 res/raw、无 JS bundle**
   （Kotlin/OkHttp 原生实现，所以"找 JS 请求封装"这一步不适用）。
2. 字符串表：全量提取 dex/arsc 可打印串，枚举 header 名、URL、版本号、UA。
3. 配置 JSON：不存在；渠道/身份定义硬编码在 Kotlin 类
   （`Fingerprint` / `Identity` / `Account` / `Routing` / `Region`）。
4. 方法级证据：用 androguard 4.1.4 解析 DEX，按方法提取 `const-string`，
   定位到 `Identity.pluginHeaders` / `channelVerifyHeaders` / `stainlessHeaders` /
   `machineHeaders`、`Fingerprint` 默认值、`Health.defaults` 等具体函数。

**它比我们多带的头（方法级）**：

| 类别 | 头 / 字段 | 参考实现取值 | 我们此前 |
|---|---|---|---|
| 插件头 | `X-Private-Data` | `true` | 未发 |
| 插件头 | `X-CodeBuddy-Request` | `1` | 未发 |
| 插件头 | `X-Requested-With` | `XMLHttpRequest` | `WorkBuddy`（编造值） |
| 链路 | `traceparent` | `00-<32hex>-<16hex>-01` | 未发 |
| 链路 | `b3` / `X-B3-TraceId` / `-ParentSpanId` / `-SpanId` / `-Sampled` | 与 trace 同源 | 未发 |
| 链路 | `baggage` | `codebuddy.session_id=…,codebuddy.conversation_request_id=…` | 未发 |
| CLI 形态 | `X-Stainless-Lang/Package-Version/OS/Arch/Runtime/Runtime-Version/Retry-Count` | `js / 6.25.0 / Windows / x64 / node / v22.14.0 / 0` | 未发 → 现 CLI 档发（与双段 UA 成套） |
| 登录链路 | `Accept-Language` / `Origin` / `Referer` / `User-Agent` / `X-Requested-With` | `zh-CN` / 站点根 | 未发 |
| 设备标识 | `X-Machine-ID` / `X-Session-ID` | **默认关闭**（"官方对话请求不带"） | 不发（正确） |

**出网身份档位（REA 复刻，2026-11-06）**

用 REA（jadx-headless）逐方法比对后，参考实现的身份模型已完全确认：

- `Fingerprint.ua()` 三形态：`uaMode="client"` → `WorkBuddy/5.9.3`；
  `uaMode="browser"` → `Identity.BROWSER_UA = Mozilla/5.0 (Windows NT 10.0; Win64; x64)
  AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36`；
  其余（默认）→ 双段 `WorkBuddy/5.9.3 WorkBuddy/5.9.3 CLI/2.165.0`。
- `Fingerprint.getStainless()`：**uaMode=="cli"**，所以双段 UA 与 Stainless 族天然成套。
- `Fingerprints.of(region)` 默认返回 `Fingerprint.OFFICIAL`，而 OFFICIAL 全部走构造默认值
  （uaMode=`cli`、paceMs=350）→ **参考实现默认就是双段 UA + Stainless**。
- 三档：OFFICIAL（双段/350ms）、DESKTOP（uaMode=client/350ms）、QUIET（uaMode=client/900ms）。
  我们另加 `browser` 档覆盖第三种 UA 形态。

| 维度 | `cli`（默认，= OFFICIAL） | `desktop`（= DESKTOP） | `quiet`（= QUIET） | `browser` |
|---|---|---|---|---|
| UA | 双段 | `WorkBuddy/5.9.3` | `WorkBuddy/5.9.3` | Chrome 125 UA |
| IDE 版本 | 5.9.3 | 5.9.3 | 5.9.3 | 5.9.3 |
| CLI 版本 | 2.165.0（进 UA） | 2.165.0（仅字段） | 2.165.0（仅字段） | 2.165.0（仅字段） |
| Stainless 族 | 发（Retry-Count 恒 0） | 不发 | 不发 | 不发 |
| 运行时声明 | node / v22.14.0 / Windows / x64 | — | — | — |
| 请求间隔 | 350ms | 350ms | 900ms | 350ms |
| 设备标识 | 默认不附加 | 默认不附加 | 默认不附加 | 默认不附加 |

**对话头逐项（REA 方法体）**：

- `Upstream.chatHeaders` = 基础头（UA=cliUa、插件头、设备令牌、企业头）
  + `Identity.conversationHeaders` + `Identity.stainlessHeaders`
  + `Identity.channelVerifyHeaders`，最后过滤空值。
- `conversationHeaders(conv,msg)`：`X-Conversation-ID=conv`、
  `X-Conversation-Message-ID=msg`、**`X-Request-ID=msg`**、`X-Agent-Intent=craft`、
  `X-Agent-Purpose=conversation`。
- `channelVerifyHeaders(conv,msg,reqId)`：`X-Private-Data=true`、
  **小写 `x-codebuddy-request=1`**（与插件头的大写键并存）、
  `X-Conversation-Request-ID=reqId`、`traceparent=00-<32hex>-<16hex>-01`、
  **`b3=<trace>-<span>-1-<parent>`**、`X-B3-TraceId/ParentSpanId/SpanId/Sampled=1`、
  `baggage=codebuddy.session_id=<conv>,codebuddy.conversation_request_id=<reqId>`。
  trace/span/parent 由 `Identity.hex(32/16/16)` 现取随机值。
- 会话 ID：`Accounts.conversationId` 按 (account.key + sessionSalt) 缓存 uuidV4；
  message/request ID：`Identity.uuidV7()` = 12 位毫秒时间 + 20 位随机 hex（32 位无连字符）。
- `billingHeaders`（计费/积分）：UA **恒单段** `WorkBuddy/5.9.3`（不使用 uaMode），
  带 `Accept-Language`（CN zh-CN / Intl en-US）、`Origin`/`Referer`（区域 origin）、
  `X-CodeBuddy-Request`、企业头、设备令牌。
- `LoginApi.noAuth`：UA=cliUa(region)（按档位）、Accept-Language、Origin/Referer、
  `X-No-*`、`X-Domain=region.defaultDomain`（CN `www.codebuddy.cn` / Intl `www.workbuddy.ai`）。
- `machineHeaders`：`deviceHeaders=false` → 空；开启时
  `X-Machine-ID = sha256(machineSeed+":machine:"+uid)` 前 8 位 hex，
  `X-Session-ID` = 同 hash 拼 `xxxxxxxx-xxxx-4xxx-8xxx-xxxxxxxxxxxx`。
- 参考实现**不发送** `X-Agent-Type` / `X-Device-Id`（dex 检索：X-Agent-Type 只出现在
  `importHeaders` 的识别表中）。

配置入口：桌面端「设置 → 网络与共享 → WorkBuddy 出网身份」，手机端设置页同一项；
底层写 `settings.json` 的 `workbuddy_identity`（`cli|desktop|quiet|browser`，默认
`cli`），也可用环境变量 `AETHER_WORKBUDDY_IDENTITY`；网关加载设置时注入
（`internal/core/gateway/settings.go` 的 `applyWorkBuddyIdentity`）。保存只落盘，
**重启网关/应用后生效**——两端界面都要同时显示「运行中真实档位」并提示重启，
否则用户会以为切换失败（运行中档位由 `GET /api/providers/workbuddy/identity` 提供）。
实现集中在 `internal/providers/workbuddy/identity.go` 的 `IdentityProfile`：UA、版本、
Stainless、运行时、间隔全部由同一个 profile 派生，禁止跨档组合。


**官方身份默认值**（`Fingerprint` 构造默认参数）：clientVersion `5.9.3`、
cliVersion `2.165.0`、stainless `6.25.0`、`os=Windows`、`arch=x64`、
`runtime=node`、`runtimeVersion=v22.14.0`、`product=SaaS`、`machineSeed=wb2a`、
`privateData=true`、`paceMs=350`（另有 900ms 档）。校验规则：
「请求间隔 <300ms 偏激进」「机器 ID 8 位十六进制」「设备令牌必须 ASCII」。
这直接印证：**版本必须成套一致**，旧版本不是最大问题，自相矛盾才是。

**错误 → 冷却映射**（`Health.defaults`）：

| 错误码 | 冷却 |
|---|---|
| `11140`, `11142` | 30 分钟 |
| `14015`, HTTP `401` | 30 分钟 |
| `14001,14002,14012,14013,14014,14018` | 60 分钟 |
| `14003,10105,6005,6006,6007,6008` | 1440 分钟（24h） |
| `quota_request_limit` | 10 次 / 10 分钟窗口 |

**合规边界**：该 APK 未申请 `READ_PHONE_STATE` 等设备信息权限，也没有签名伪造；
它靠"复刻官方头族 + 冷却"而不是"破解"。本次落地同样只做这些。

---

## 3. 我们已做的（仓库现状）

> 详细逐渠道基线见 [reverse-channel-headers.md](reverse-channel-headers.md)，
> 逐轮修复记录见 [reverse-channel-hardening.md](reverse-channel-hardening.md)。
> 本节只做索引与补充，不重复那张大表。

| 能力 | 位置 | 现状 |
|---|---|---|
| uTLS TLS 指纹 | `internal/core/transport/utls.go` | 支持 Chrome/Firefox/Safari/Edge/iOS/Android OkHttp/randomized；通过 `DialTLSContext` 只替换 TLS 层，代理与连接池策略不变 |
| 反代渠道默认 TLS 指纹 | `internal/core/gateway/fingerprint_defaults.go` | 按"真实客户端本来是什么"给默认值（Electron/VS Code 系 → `chrome_131`）；官方 API Key 渠道不套浏览器指纹 |
| 账号/渠道级 TLS 覆盖 | `provider.AccountEnv.TLSProfile`、`spec.tls_profile` | 账号级 > spec 级 > 默认；`provider_tls.go` 负责解析 |
| 设备指纹模板 | `internal/core/transport/dialer.go`（`WithFingerprint`） | 支持 `{{.Fingerprint.MachineID}}` 等占位符，由 spec 的 `headers_template` 驱动，改版本号不用改代码 |
| 账号稳定设备指纹 | `internal/core/gateway/admin.go`（`deriveFingerprint`） | 由账号 ID 派生，跨进程/重启稳定 |
| UA 一致性硬锁 | `internal/providers/ua_consistency_test.go`、`uahygiene_test.go` | 同渠道只允许一种 UA；有 ChatStream 必须设 UA；禁止把本产品品牌写进上游 UA |
| Claude 订阅身份 | `internal/providers/claude/chat.go`、`mimicry.go`、`identity.go` | 订阅模式才注入 CLI 头、billing attribution、`X-Claude-Code-Session-Id`、`x-client-request-id`、`metadata.user_id` 三元组；API Key 模式不伪装 |
| WorkBuddy 会话三级 ID | `internal/providers/workbuddy/chat.go` | `X-Conversation-ID` / `-Request-ID` / `-Message-ID`，请求 ID 对同一次逻辑发送稳定 |
| WorkBuddy 身份档位（REA 复刻，2026-11-06） | `internal/providers/workbuddy/identity.go` | `IdentityProfile` 四档 `cli/desktop/quiet/browser`（默认 `cli` = 参考 OFFICIAL 双段 UA）；UA/IDE 版本/CLI 段/Stainless/运行时/间隔由**同一 profile** 派生；配置 `workbuddy_identity` / `AETHER_WORKBUDDY_IDENTITY`；`X-Machine-ID/X-Session-ID` 默认关（有精确算法，可显式开启） |
| WorkBuddy 链路头（新增） | `internal/providers/workbuddy/identity.go` | `traceparent` / `b3` / `X-B3-*` / `baggage` 全部由本会话 conversation/request id 派生（W3C 格式），不复制外部值 |
| WorkBuddy 请求节流（新增） | `internal/providers/workbuddy/identity.go` | 同账号两次上游请求最小间隔由档位决定（cli/desktop 350ms、quiet 900ms），打断机器式连发 |
| WorkBuddy 错误 → 账号冷却（新增） | `internal/providers/workbuddy/cooldown.go` | 表驱动：11140/11142→30min、14015/401→30min、14001…14018→60min、14003/10105/6005-6008→24h、quota_request_limit→10min；冷却只作用于当前账号，网关鉴权错误带 RetryAfter 时也改为冷却而非永久 invalid |
| 其他渠道身份头 | `internal/providers/*` | CodeBuddy CLI 全套、Qoder `Cosy-*`、Trae `Trae/<ver>` + 设备头、Kiro AWS SDK、Devin Connect-RPC（刻意不发 UA）、Copilot 扩展头等 |
| 提示词指纹清洗（WorkBuddy，可选） | `internal/providers/workbuddy/scrub.go` | 默认关闭；只清洗第一条 system 的已知开场白指纹 |
| 会话粘滞 | `internal/core/pool/manager.go`、`sticky_persist.go` | 会话指纹 → 账号绑定，带 warm/cold TTL 并落盘 |
| 账号级出口 | `AccountEnv.ProxyOverride`、`provider_proxy.go`、`NetworkPolicy` | 一号一代理可配置；`direct` 强制直连；渠道声明 `proxy_first` 等策略 |
| 并发闸 | `internal/core/gateway/channel_net.go` | 渠道级 `max_inflight` 信号量，等待上限后放行（不无限排队） |
| 退避与冷却 | 各 provider 的 `RetryAfterHint` + `ir.APIError.RetryAfter` + `pool` 冷却 | 429/402/5xx 会标记 `Retryable`；429 解析并携带 `Retry-After` 窗口；账号冷却后跳过 |
| 风控暂停 | `internal/core/gateway/riskpause.go` | 命中 risk/abnormal/banned/风控/11140 等文案 → 该渠道冷静 10 分钟，且不误标"凭证失效" |
| 连接复用 | `internal/core/transport/dialer.go` | `MaxIdleConns=16`、`MaxIdleConnsPerHost=8`、`IdleConnTimeout=90s`；H2 失败自动降级 H1 并记忆 10 分钟 |
| 版本热更新 | `internal/core/spec` + `spec_load` | UA/头模板/客户端版本可由 spec JSON 更新，无需重编译 |

**已知局限（仓库文档已承认）**：

- HTTP 头顺序按 Go 的字母序发送，未模拟 Node/Chromium 的插入序；
- HTTP/2 SETTINGS 指纹未定制（uTLS 只覆盖 TLS ClientHello）；
- uTLS 默认统一 `chrome_131`，未按各客户端运行时（Node undici / Rust reqwest /
  Electron）区分——这本身可能是一种"指纹与 UA 不匹配"。

---

## 4. 建议补的（按收益/成本排序）

> 成本按"人日"粗估；"需抓包"= 必须有自己账号的真实抓包才能验证，不能凭猜测改。

### P0：收益高、成本低，且不需要抓包

| # | 建议 | 为什么 | 收益/成本 | 需抓包 |
|---|---|---|---|---|
| 1 | **出口一致性约束与告警**：记录每个账号最近使用的出口 ASN/IP，出现"同账号短时间多 ASN"或"同 IP 多账号超阈值"时告警/暂停该账号 | IP 是最外层信号；当前只提供 `proxy_override` 可选项，没有强制与观测 | 高 / 低 | 否 |
| 2 | **版本漂移巡检**：把 UA/`Editor-Version`/`Client-Version`/TLS profile 的"最后核对日期"落盘，超过 N 天提示更新；升级时成组更新 | 版本落后是最常见的"不像官方"来源，且会随时间必然发生 | 高 / 低 | 否（公开版本页即可） |
| 3 | **风控信号词表集中化 + 可配置**：把 `riskpause.go` 的词表扩成 per-provider 配置，纳入业务码（如 WorkBuddy 10085、Trae 4011、Claude 11140 等），统一走"暂停+告警" | 各渠道风控码分散，靠文案命中会漏 | 高 / 低 | 否（抓包/日志已有） |
| 4 | **账号级（而非仅渠道级）并发上限 + 重试抖动**：同一账号同时在途请求限流；重试加随机抖动，禁止 0 延迟重试 | 机器级并发和同步重试是行为层最明显的特征；当前主要是渠道级闸门 | 中高 / 中 | 否（压测即可） |
| 5 | **请求 ID 重放防护**：断言重试不重用 `x-client-request-id`/`X-Request-ID`；同一逻辑请求的 retry-count 递增 | 复用请求 ID 会被当重放；Claude 代码注释已注意，但缺全局校验 | 中高 / 低 | 否 |

### P1：收益中高、需要抓包对照（不能猜）

| # | 建议 | 为什么 | 收益/成本 | 需抓包 |
|---|---|---|---|---|
| 6 | **按客户端运行时选 TLS profile**：Electron/Chromium 用 Chrome，Node CLI 用 Node/undici 相近形态，Rust 客户端用对应 profile；避免"UA 是 CLI、TLS 是 Chrome" | 当前统一 `chrome_131`，与部分客户端运行时不一致；TLS 与 UA 的矛盾比单看某一层更可疑 | 高 / 中 | **是**（各客户端自己的 ClientHello） |
| 7 | **HTTP/2 SETTINGS / 头顺序基线**：先抓包确认上游是否真用这些维度；若用，再评估自写 transport 的成本 | 仓库已列为"暂不做"；需要先证明有收益，避免过度工程 | 中 / 高 | **是** |
| 8 | **端到端 header 集合回归**：用 HAR 做 golden test，断言"官方客户端有的头我们有、官方没有的我们不多发"（尤其 `Accept-Language`、`Accept-Encoding`、`Content-Type`、`anthropic-beta` 等） | 多一个官方不发的头，比少一个更容易被识别（仓库原则"宁缺勿造"） | 中高 / 中 | **是** |
| 9 | **请求体字段保真**：字段集合、可选字段缺失、`system`/`metadata` 形状、工具定义形状；对 body 做 golden 对比（不追求字节序，先追求字段语义一致） | 现状多数字段靠手工对齐；上游可能做结构校验 | 中 / 中 | **是** |
| 10 | **跨接口身份一致性自动化**：把"对话/额度/签到/刷新共用同一 UA+设备+版本"从 UA 一项扩展到设备 ID、平台字段、版本三件套 | 已有 UA 硬锁；设备/版本仍是分散实现，容易出现"对话 Windows、签到 macOS" | 中 / 低 | 部分（对照抓包更稳） |

### P2：收益低或风险高，先不做

| # | 建议 | 判断 |
|---|---|---|
| 11 | 自写 HTTP/1.1 序列化以复刻头顺序 | 收益未证实，维护成本高；HTTP/2 下头顺序语义也不同。先做第 7 项的抓包验证再决定 |
| 12 | 随机轮换 UA / TLS / 设备 ID | **反向优化**：同一账号身份漂移本身就是异常信号 |
| 13 | 复刻 billing/attribution 中的签名类字段、扩展 beta 串以"更像" | 属于第 5 节排除项；已有字段保持自洽即可，不再新造 |

---

## 5. 明确不做的事

以下做法**不在本项目的工程范围内**，本文档也不提供任何操作步骤：

1. **伪造签名 / 破解校验 / 绕过服务端鉴权**（包括伪造 license、billing 签名、
   设备证明、attestation）。
2. **绕过付费、额度、地区或速率限制**；不修改/伪造用量与配额数据。
3. **使用非本人账号、共享/转售凭证**，或帮助规避上游的账号共享检测。
4. **对抗性绕过**：验证码识别、设备风控绕过、批量注册、养号。
5. **随机化伪装**：随机 UA/TLS/设备 ID 轮换（既不合规也更容易被识别）。
6. **重试风暴**：命中风控后换号硬撞、0 延迟重试。

如果某个需求必须依赖上述手段才能成立，应视为**产品/合规问题**，而不是工程优化问题。

---

## 6. 风险声明

- **不能保证不封号**。本文所有措施只降低"被判定异常"的概率；最终结果取决于上游
  策略、账号历史、支付方式、地区、使用强度等我们不可控的因素。
- **上游策略会变**。今天有效的 header 集合/版本号/TLS 指纹，明天可能因为客户端
  升级或风控规则调整而失效；需要持续抓包回归，不能"一次配好永久有效"。
- **反代订阅凭证本身可能违反上游服务条款**。请以各厂商服务条款为准
  （如 [OpenAI Terms of Use](https://openai.com/policies/terms-of-use/)、
  [Anthropic Consumer Terms](https://www.anthropic.com/legal/consumer-terms)，
  链接仅供自行核对，本文不构成法律意见）。建议只用于**本人账号 + 本人设备/网络**的
  本地互操作场景；不要把该能力用于对外提供共享服务。
- **数据安全**：凭证、设备指纹、抓包文件都属敏感数据；抓包请脱敏后再入库，
  不要提交到仓库或随文档分发。
- **本机网关的定位**：手机本地网关（用户自己的设备、自己的网络出口）在"出口一致性"
  上天然优于机房中转；这也是本仓库移动端方案在风控维度上的主要优势，应继续保持。

---

## 附录 A：来源核验记录（2026-11-06）

| 来源 | URL | 核验方式 | 用途 |
|---|---|---|---|
| REA 仓库 | https://github.com/morluto/rea | `web_fetch` HTTP 200；另取 GitHub API | 确认项目定位与能力表 |
| REA 抓包文档 | https://github.com/morluto/rea/blob/main/docs/web-network-captures.md | `web_fetch` HTTP 200 | 确认其离线分析、不启动代理 |
| Cloudflare JA3/JA4 | https://developers.cloudflare.com/bots/additional-configurations/ja3-ja4-fingerprint/ | `web_fetch` HTTP 200 | TLS 指纹识别维度 |
| MDN Client Hints | https://developer.mozilla.org/en-US/docs/Web/HTTP/Guides/Client_hints | `web_fetch` HTTP 200 | UA-CH 头集合与 fingerprint |
| Go net/http | https://pkg.go.dev/net/http#Transport | `web_fetch` HTTP 200 | 连接复用与 body 关闭语义 |
| AWS Builders Library | https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/ | `web_fetch` HTTP 200（正文截断，标题与主题已确认） | 退避+抖动 |
| AWS Architecture Blog | https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/ | `web_fetch` HTTP 200（正文截断） | 退避+抖动 |
| Wokey 封号架构文 | https://wokey.ai/blog/relay-ban-architecture?lang=zh | `web_fetch` HTTP 200，全文 | 出口 IP 视角（厂商文章，利益相关） |
| 某开源 API 转发项目的 PR #2489 | https://github.com/router-for-me/某开源 API 转发项目/pull/2489 | `web_fetch` HTTP 200，已合并 | 上游检测维度的旁证 |
| Antigravity-Manager #646 | https://github.com/lbjlaq/Antigravity-Manager/issues/646 | 搜索结果标题可见（`exa`） | 429 与风控的社区报告 |

## 附录 B：未能验证的来源

| 来源 | URL | 状态 |
|---|---|---|
| V2EX「claude 自己搭中转站如何防止封号?」 | https://www.v2ex.com/t/1216972 | **未能验证**：抓取遇到 Cloudflare 安全验证页，未读到正文；不采信其结论 |
| linux.do「有网关支持完全透传吗」 | https://linux.do/t/topic/2983091 | **未能验证**：`web_fetch` 超时；不采信其结论 |
| REA 全部源码实现 | https://github.com/morluto/rea | **未逐文件审计**：只读了 README、抓包文档与文件列表；"无请求构造/重试模块"是"未发现"，不是绝对证明 |

## 附录 C：术语

- **JA3/JA4**：由 TLS ClientHello 的版本、加密套件、扩展、椭圆曲线等字段计算出的
  客户端指纹，可跨 IP/端口/证书稳定识别客户端类型。
- **Client Hints**：`Sec-CH-UA-*` 等由浏览器发送的设备/网络/偏好提示头，可参与
  fingerprint。
- **会话粘滞（sticky session）**：同一会话固定路由到同一账号/后端，避免上下文漂移。
- **风控冷静期**：命中风控信号后主动暂停一段时间，避免重试把"可疑"升级为"确认滥用"。
- **出口（egress）**：请求最终离开本地网络时使用的公网 IP/ASN。
