# 反代渠道：关键请求头基线表

用于回归对照。**改动任何一列之前先看这里**：真实客户端不可能按接口换 UA、换平台、
换设备标识；不一致就是最好的识别特征。

- 生成时间：2026-11-06
- 依据：代码审计（`internal/providers/*`）+ 公开逆向资料 + 抓包结论
- 版本号会随客户端升级过期，升级时**成组更新**（UA / Editor-Version / Plugin-Version 互相印证）

## 逐渠道基线

### workbuddy（CN / Intl）

> 2026-11-06 按参考实现（`com.joy4fire.wbproxy` 1.0.6）的方法级证据收敛：
> **UA 与 X-IDE-Version 同源同版本**（`clientVersion`），不再出现 UA 5.5.6 /
> IDE 5.8.3 这种自相矛盾组合。依据见 [ANTI_BAN.md §2.4](ANTI_BAN.md)。
>
> 同一渠道同时只有一个**出网身份档位**（`workbuddy_identity`，默认 `cli`，即参考实现的「官方 CLI」双段 UA）：
>
> | 档位 | UA | Stainless 族 | 间隔 |
> |---|---|---|---|
> | `cli`（默认，= 参考 `OFFICIAL`） | `WorkBuddy/5.9.3 WorkBuddy/5.9.3 CLI/2.165.0` | 发 | 350ms |
> | `desktop` | `WorkBuddy/5.9.3` | 不发 | 350ms |
> | `quiet` | `WorkBuddy/5.9.3` | 不发 | 900ms |
> | `browser`（可选） | `Mozilla/5.0 (Windows NT 10.0; Win64; x64) … Chrome/125.0.0.0 Safari/537.36` | 不发 | 350ms |
>
> 改版本/平台时必须整档改，禁止"UA 桌面 + 头发 CLI"的跨档组合。

| 头 | 取值 | 代码 |
|---|---|---|
| `User-Agent` | 由档位决定（见上表） | `workbuddy/identity.go` |
| `X-IDE-Type` / `X-IDE-Name` | `WorkBuddy` | 同上 |
| `X-IDE-Version` | `5.9.3`（与桌面段同一常量） | 同上 |
| `X-Product` | `SaaS` | 同上 |
| `X-Private-Data` | `true` | 同上 |
| `X-CodeBuddy-Request` | `1` | 同上 |
| `X-Requested-With` | `XMLHttpRequest`（**不再是编造的 `WorkBuddy`**） | 同上 |
| `X-Stainless-Lang` / `-Package-Version` / `-OS` / `-Arch` / `-Runtime` / `-Runtime-Version` / `-Retry-Count` | 仅 `cli` 档：`js / 6.25.0 / Windows / x64 / node / v22.14.0 / 0`（参考实现恒 0） | 同上 |
| `X-Agent-Purpose` / `X-Agent-Intent` | `conversation` / `craft` | `workbuddy/chat.go` |
| `X-Conversation-ID` | 账号级稳定 UUID v4（`conversationIDFor`） | `workbuddy/identity.go` |
| `X-Conversation-Message-ID` / `X-Request-ID` | 同一个 32 位 uuidV7（12 位毫秒时间 + 20 位随机，无连字符） | 同上 |
| `X-Conversation-Request-ID` | 独立的 32 位 uuidV7（与 message id 不同） | 同上 |
| `traceparent` | `00-<32hex>-<16hex>-01`，trace/span 每次请求随机（SecureRandom） | 同上 |
| `b3` / `X-B3-TraceId` / `-ParentSpanId` / `-SpanId` / `-Sampled` | `b3=<trace>-<span>-1-<parent>`（注意顺序）；B3 各头与 traceparent 同源 | 同上 |
| `baggage` | `codebuddy.session_id=<conversation-id>,codebuddy.conversation_request_id=<request-id>` | 同上 |
| `x-codebuddy-request`（小写） | `1`，与 `X-CodeBuddy-Request` **并存**（参考实现逐字如此） | 同上 |
| `Accept-Language` / `Origin` / `Referer`（登录/计费链路） | `zh-CN`（CN）/`en-US`（Intl） / 区域 origin / origin + `/` | `workbuddy/login.go`、`checkin.go` |
| 请求间隔 | 同账号 ≥ 档位值（cli/desktop/browser 350ms，quiet 900ms） | `workbuddy/identity.go` |
| `X-Machine-ID` / `X-Session-ID` | 默认不发；显式 `device_headers=true` 时按参考算法派生（machine=sha256(wb2a:machine:uid) 前 8 hex；session=同 hash 拼 UUID） | 同上 |
| `X-Device-Token` | 凭证配置了才发（服务端签发，不可伪造）；不再发 `X-Device-Id` | `workbuddy/chat.go` |
| `X-Enterprise-Id` / `X-Tenant-Id` | 企业版才发；默认两者同值（`tenant_id` 可覆盖） | 同上 |
| 计费接口 UA | 恒单段 `WorkBuddy/5.9.3`（参考 `billingHeaders` 不使用 uaMode） | `workbuddy/checkin.go` |
| 错误 → 账号冷却 | 11140/11142→30min；14015/401→30min；14001…14018→60min；14003/10105/6005-6008→1440min；quota_request_limit→10min | `workbuddy/cooldown.go` |
| 提示词指纹清洗 | 表单开关，默认关（替换 system 开场白指纹） | `workbuddy/scrub.go` |


### codebuddy（CN / Intl）

| 头 | 取值 |
|---|---|
| `User-Agent` | `CLI/<cliVer> CodeBuddy/<cliVer>`（CN 2.107.0 / Intl 2.137.1） |
| `X-IDE-Type` / `X-IDE-Name` | `CLI` |
| `X-IDE-Version` | 同上 |
| `X-Client-Platform` | `web` |
| `X-Agent-Intent` | `craft` |
| `X-Conversation-ID` / `-Request-ID` / `-Message-ID` | 三级（会话 ID 稳定、请求/消息每轮派生） |
| `x-stainless-*` | `lang=js`、`runtime=node`、`runtime-version=v24.11.1`、`package-version=<sdk>` |
| `X-Product` | `SaaS`（Intl 另有 `X-Product-Version`） |

### qoder

| 头 | 取值 |
|---|---|
| `User-Agent` | `qoder/<cosyVer>`（当前 1.1.77，对话/签到/额度统一） |
| `Cosy-MachineId` | 凭证 `machine_id` |
| `Cosy-MachineToken` | `machine_token` 优先，缺省退回 `machine_id`（三接口统一） |
| `Cosy-MachineType` / `Cosy-MachineOS` | `5` / `x86_64_<os>`（按平台） |
| `Cosy-ClientType` / `Cosy-Scene` / `Cosy-Version` | `10` / `app` / `<cosyVer>` |

### trae（CN / SOLO / SG / US）

| 头 | 取值 |
|---|---|
| `User-Agent` | `Trae/<ideVersion>`（必须是 Trae 前缀，否则流内 4011） |
| `Authorization` | `Cloud-IDE-JWT <token>` |
| `X-Cloudide-Token` | 同 token |
| `x-device-id` / `x-machine-id` | 凭证优先；缺省用网关按账号派生的稳定指纹（**不空**） |
| `x-device-type` / `x-os-version` | 按运行平台（Windows 保持抓包值） |
| `x-app-id` / `x-app-version` / `x-ide-version*` | 版本常量 |

### claude（订阅流）

| 头 / 字段 | 取值 |
|---|---|
| `User-Agent` | `claude-cli/<ver>`（不得低于上游版本地板） |
| `X-App` | `cli` |
| `X-Stainless-*` | 官方 CLI 抓包常量（lang js / linux arm64 / node v24.3.0 / timeout 600） |
| `anthropic-beta` | 默认对齐 CLI（含 `oauth-2025-04-20`），凭证可覆盖 |
| `X-Claude-Code-Session-Id` | 会话 UUID（按 账号+机器+会话 派生，跨轮稳定） |
| `x-client-request-id` | 每请求随机 UUID |
| `metadata.user_id` | **JSON 字符串**：`device_id`(64hex) / `account_uuid` / `session_id` |
| system 首块 | `x-anthropic-billing-header: cc_version=<ver>.<3位指纹>`，指纹与首条 user 文本+版本绑定 |

API Key 模式不注入上述订阅专属字段（官方 API 不需要，多塞反而像伪造）。

### kiro

| 头 | 取值 |
|---|---|
| `User-Agent` / `x-amz-user-agent` | `aws-sdk-rust/1.0.0 ua/2.1 ... app/AmazonQ-For-CLI ...`（对话与额度共用同一函数） |
| `tokentype` | `API_KEY` |
| `x-amzn-kiro-agent-mode` | `vibe` |
| `amz-sdk-invocation-id` | 每请求随机 UUID（SDK 语义） |
| `amz-sdk-request` | `attempt=1; max=3` |
| 会话状态 | 请求体 `conversationId` + `conversationState` |

### devin

| 头 | 取值 |
|---|---|
| `User-Agent` | **不发**（原生 devin-cli 抓包结论；实现里显式抑制 Go 默认值） |
| `Authorization` | `Basic <token>-<token>`（token 自带 `devin-session-token$` 前缀） |
| `Connect-Protocol-Version` | `1` |
| `Content-Type` | `application/connect+proto` |
| `Sentry-Trace` | `<32hex>-<16hex>-1` |
| 会话 | 由 `account + 首条用户消息` 派生稳定会话 UUID（跨轮稳定） |

### githubcopilot

| 头 | 取值 |
|---|---|
| `User-Agent` | `GitHubCopilotChat/0.35.0` |
| `Editor-Version` | `vscode/1.107.0` |
| `Editor-Plugin-Version` | `copilot-chat/0.35.0` |
| `Copilot-Integration-Id` | `vscode-chat` |
| `X-Initiator` | 动态：用户发起 `user`；工具/助手续轮 `agent` |
| `Copilot-Vision-Request` | 带图请求才发 `true` |

### antigravity

| 头 | 取值 |
|---|---|
| `User-Agent` | `antigravity/hub/2.24.0 (aidev_client; os_type=<host>; arch=<host>; cl=1000335682)` |

### zcode（CN / Intl）

| 头 | 取值 |
|---|---|
| `User-Agent` | `ZCode/3.10.2`（对话与额度统一） |

### minimax

| 接口 | UA |
|---|---|
| 对话 | `MiniMaxAgent` |
| 签到 | `MiniMaxCode` |

两者分属不同上游产品/endpoint，属**有意差异**（已在硬锁 `uaMultiAllowed` 登记）。

## 已知局限（明确的"暂时不做"）

| 项 | 现状 | 原因 |
|---|---|---|
| HTTP 头顺序 | Go 按字母序发送 | 真实 Node/Chromium 客户端按插入序发送；要匹配需自写 transport，收益与风险不成比例 |
| HTTP/2 SETTINGS 指纹 | 未定制 | 同上；uTLS 已覆盖 TLS ClientHello 层 |
| `zed` 额度接口 UA | 未设置（Go 默认） | 无公开证据表明 Zed 客户端的 UA 形态；该渠道只读、不转发对话，故不臆造 |
| 官方 API 渠道 TLS | 保持 Go 默认 | 官方 SDK 用的是各自运行时，套浏览器指纹反而更异常 |

## 不转发对话的渠道

`cursor` / `zed` / `windsurf` / `antigravityide` 只做登录态读取、额度查询、一键切号，
不产生对话流量，因此不在"对话防封"范围内。

## 跨渠道硬锁（改动时必须一起看）

| 测试 | 约束 |
|---|---|
| `TestNoBrandInUpstreamUserAgent` | UA 不得含本产品品牌 |
| `TestNoEmptyCredentialHeader` | 不得写出"可能为空值"的头 |
| `TestSingleUserAgentPerChannel` | 同渠道只允许一种 UA（例外需登记） |
| `TestChatChannelSetsUserAgent` | 有 ChatStream 就必须设 UA（例外需登记） |
