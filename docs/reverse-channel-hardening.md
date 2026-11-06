# 反代渠道的"像真客户端"清单

本文记录各反代渠道在**防封号**维度上做了什么、还缺什么。结论来自代码审计 +
公开资料 + 抓包对照，不是推测。

## 原则

1. **宁缺勿造**：没有确切依据的字段不硬加。多一个官方不发的头，比少一个更容易被识别。
2. **身份自洽**：同一账号在不同接口（对话 / 额度 / 签到 / 刷新）上报的设备、平台、
   版本必须互相一致。不一致是最容易被关联的特征。
3. **不写自家品牌**：UA 里出现第三方产品名等于自报身份。仓库有硬锁
   `internal/providers/uahygiene_test.go` 防回归。
4. **风控要"停"不要"撞"**：命中风控信号后继续重试，会把"可疑"升级成"确认滥用"。

## 跨渠道能力（所有渠道共享）

| 能力 | 位置 | 说明 |
|---|---|---|
| 默认 TLS 指纹 | `internal/core/gateway/fingerprint_defaults.go` | 反代渠道默认 `chrome_131`；官方 API Key 渠道不套浏览器指纹 |
| 通用风控暂停 | `internal/core/gateway/riskpause.go` | 命中 risk/异常行为/11140 等文案 → 该渠道暂停 10 分钟，不罚账号 |
| 会话粘滞 | `pool` + `sticky.json` | 同一会话固定账号，避免多账号漂移 |
| 账号级出口代理 | 账号 `proxy_override` | 一号一出口 |
| UA 品牌硬锁 | `internal/providers/uahygiene_test.go` | 禁止把本产品名写进上游 UA |

## 逐渠道状态

| 渠道 | 对话转发 | 身份头 | 设备指纹 | 会话 ID | 风控处理 | 备注 |
|---|---|---|---|---|---|---|
| workbuddy | ✅ | ✅ 完整 IDE 头 | ✅ `X-Device-Token` 透传 | ✅ 会话/请求/消息三级 | ✅ 渠道级 10 分钟 | 设备令牌只透传、不伪造 |
| codebuddy | ✅ | ✅ CLI 全套（X-IDE-*、stainless） | — | ✅ 三级 | ✅ 业务码映射 | 与开源实现 codebuddy2api 的头集合一致 |
| qoder | ✅ | ✅ `Cosy-*` | ✅ MachineId/Token/Type | ✅ | 通用 | 本轮统一各接口的系统上报 |
| trae | ✅ | ✅ | ✅ `x-device-id`/`x-machine-id` | 由上游无状态 | ✅ 1001/4008/4011 + Retry-After | UA 必须是 `Trae/<ver>`，否则流内 4011 |
| claude | ✅ | ✅ CLI 全套 + billing 指纹 | ✅ 设备/账号/会话三元组 | ✅ 三级 | 通用 | 订阅流量才注入，API Key 模式不伪装 |
| kiro | ✅ | ✅ AWS SDK | — | ✅ conversationState | 通用 | 额度接口与对话共用同一 UA |
| devin | ✅ | ✅ Connect-RPC | — | 由 `conversation_id` 承载 | 通用 | **刻意不发 UA**（原生 CLI 也不发） |
| githubcopilot | ✅ | ✅ Editor-Version/Integration-Id | — | — | 通用 | 官方扩展不带机器标识，故不造 |
| antigravity | ✅ | ✅ Cloud Code 头 | — | — | 通用 | 本轮 UA 改为按本机平台生成 |
| zcode | ✅ | ✅ | — | — | ✅ 业务码 | 订阅制，走官方 API Key 语义 |
| cursor / zed / windsurf / antigravityide | ❌ 不转发对话 | — | — | — | — | 只做登录态/额度/切号，无对话流量 |

## 本轮修掉的问题（2026-11-06）

1. **品牌 UA 泄漏 12 处**：`codex_cli_rs/0.16.0 (Aether Cockpit)`、`AetherCockpit/quota`、
   `aether-cockpit/*` 等，全部换成对应生态的真实形态（codex / opencode / openai-node /
   google-genai / Copilot / antigravity），并加硬锁。
2. **antigravity UA 写死 `darwin/arm64`**：改为按运行平台生成，避免"登录在 Windows
   却自称 macOS"。
3. **qoder 系统上报不一致**：请求体 `os_type` 与签到头写死 `windows`，而
   `machineOS()` 是按平台的——统一收口到 `qoderOSName()`。
4. **kiro / copilot / antigravity 额度接口的 UA 与对话不一致**：统一为同一客户端形态。
5. **反代渠道没有 TLS 指纹**：默认启用 uTLS `chrome_131`（官方 API 渠道不受影响）。

## 第三轮补充（2026-11-06）

1. **UA 按渠道生态分支**：`opencode/*`、`codex_cli_rs/*`、`GitHubCopilotChat/*`、
   `openai-node/*`。此前所有反代渠道共用 Codex 形态的 UA——给 z.ai coding plan
   发 `codex_cli_rs`，和没有 UA 一样可疑。
2. **zcode 额度接口补 UA**：对话走 `openai-node/*`，额度接口原来是 Go 默认 UA，
   同一账号两种指纹。
3. **codebuddy 补 `X-Client-Platform: web`**，与开源参考实现（codebuddy2api）对齐，
   并在测试里断言 CLI 身份三件套（`X-IDE-Name`/`X-Agent-Intent`/`X-Client-Platform`）。

## 第四轮补充（2026-11-06）

1. **githubcopilot 客户端版本对齐真实扩展**：`GitHubCopilotChat/0.35.0`、
   `Editor-Version: vscode/1.107.0`、补 `Editor-Plugin-Version: copilot-chat/0.35.0`
   （旧值 0.26.7 / 1.99.0 落后多个版本）。
2. **`X-Initiator` 改为按请求动态判定**：用户提问 → `user`，工具/助手续轮 → `agent`；
   带图请求补 `Copilot-Vision-Request: true`。为此给 reverse 引擎加了
   `Profile.DynamicHeaders` 钩子（静态 Headers 无法表达这类随请求变化的头）。
3. **token 交换与对话用同一组版本头**，避免同一账号两套客户端版本。

## 第五轮补充（2026-11-06）

1. **qoder 机器令牌口径统一**：对话/额度固定发 `machine_id`，签到优先 `machine_token`
   ——同一账号三套身份。现在三处统一为"`machine_token` 优先、缺省退回 `machine_id`"。
2. **trae 设备标识兜底**：导入账号没有 `machine_id` 时会发**空值头**（比没有头更显眼）。
   现在退回网关按账号派生的稳定指纹，且同一账号每次一致。
3. **trae `x-uid` 不再发空值**：没有 uid 就不发。
4. **codebuddy 额度接口 `X-Tenant-Id`** 同样改为非空才发。
5. 新增硬锁 `TestNoEmptyCredentialHeader`：禁止 `Set(头, TrimSpace(GetCredential(...)))`
   这种"可能发出空值头"的写法。

## 第六轮补充（2026-11-06）

1. **trae 平台字段按本机生成**：`x-device-type` / `x-os-version` / 登录 `OSInfo` 原来写死
   Windows 10，macOS/Linux 上会"自称 Windows"（与登录时的真实系统冲突）。
2. **qoder UA 统一**：对话/签到分别用 `qoder/1.1.77` 与裸 `Qoder`，现统一为
   `qoder/<cosyVer>`。
3. **copilot 残留旧版本**：额度路径还留着 `GitHubCopilotChat/0.26.7`，已统一到常量。
4. **zcode UA 统一**：对话走 openai-node 兜底、额度走 `ZCode/<ver>`——同一账号两个客户端。
   现在统一为 `ZCode/3.10.2`。
5. 新增 `TestPlatformMatchesHost`（trae）。

遗留说明：minimax 的 `MiniMaxAgent`（对话）与 `MiniMaxCode`（签到）分属两个不同上游
产品/endpoint，保持差异。

## 第七轮补充（2026-11-06）

新增两道**通用硬锁**（不再依赖人工扫）：

1. `TestSingleUserAgentPerChannel`：同一渠道目录里发给上游的 UA 字面量只允许一种；
   例外（minimax 的两个不同上游产品）在 `uaMultiAllowed` 显式登记原因。
2. `TestChatChannelSetsUserAgent`：实现了 `ChatStream` 的渠道必须设置 UA
   （否则上游看到 `Go-http-client`）；例外（devin 原生 CLI 不发 UA、协议兼容层、
   mock）在 `uaAbsentAllowed` 登记。

同时补齐 claude 身份三件套的测试：稳定性（换机器/换会话的差异面）、
`metadata.user_id` 必须是 JSON 字符串、请求 ID 每请求唯一，并在 OAuth 流式用例里
断言 `X-Claude-Code-Session-Id` / `x-client-request-id` / `metadata.user_id` 真实出现在请求上。

## 第八轮：workbuddy 提示词指纹清洗（默认关闭）

场景：把 Claude Code / OpenCode 这类 agent 客户端指到 WorkBuddy 后端时，客户端会在
system 里写死 `You are Claude Code, Anthropic's official CLI for Claude.` 这类开场白——
对上游来说等于自报"我不是官方客户端"。

用法：在账号凭证里加 `sanitize_claude_prompt=1`（也可用 `true/yes/on`）。

行为：

- 只清洗**第一条 system 消息**；
- 只替换已知开场白指纹（Claude Code / OpenCode 三种写法），替换为中性描述；
- 不动用户正文、不动代码、不动 `<env>` 等环境块；
- 默认关闭，未开启时行为与之前完全一致。

测试：4 个单元用例（命中/未命中/仅 system/开关解析）+ 1 个端到端用例
（开启后上游收到的 system 不再含 "Claude Code"/"Anthropic"）。

## 下一步

- workbuddy：可选的"提示词指纹清洗"（仅当把 Claude Code 指向 WorkBuddy 后端时启用）。
- zcode：确认订阅通道是否需要客户端版本头。
- 持续观察：封号主因通常是上游策略（如 WorkBuddy 2026-10-23 收紧），不是单点指纹。
