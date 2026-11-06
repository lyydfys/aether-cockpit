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

## 下一步

- workbuddy：可选的"提示词指纹清洗"（仅当把 Claude Code 指向 WorkBuddy 后端时启用）。
- zcode：确认订阅通道是否需要客户端版本头。
- 持续观察：封号主因通常是上游策略（如 WorkBuddy 2026-10-23 收紧），不是单点指纹。
