# relay-panel 反代能力调研

> 调研日期：2026-10-27（子智能体逐文件读源码产出）
> 对象：[Aether/relay-panel](https://github.com/Aether/relay-panel)（Go，约 44.9k stars，LGPL-3.0，2025-12 创建，最新 v0.2.18）
> 定性：**不是** local-bridge 式"Web 聊天页逆向"，而是**订阅级 OAuth 网关**——把 Claude/Codex/Gemini/Grok 的官方订阅 OAuth 会话反代成标准 API。与本项目 claude-sub 渠道同路线，工程化程度更深。

## 1. 支持的订阅源与凭据形态

账号模型为 `platform × type`（backend/internal/service/gateway_service.go 的 GetAccessToken）：

- **Anthropic**：oauth（Claude Code OAuth）、setup_token（仅推理 scope）、api_key、Vertex service_account、Bedrock（gateway_bedrock.go + bedrock_signer.go SigV4）
- **OpenAI/Codex**：OAuth（ChatGPT 订阅）、setup_token、agent-identity、API Key（回落 api.openai.com/v1/responses）
- **Gemini/Antigravity**：OAuth + Vertex 服务账号（gemini_oauth_service.go、antigravity_oauth_service.go）
- **Grok/xAI**：OAuth PKCE，支持邮箱密码登录换 Web SSO cookie 再转 build token（oauth_service.go 的 LoginWithPassword/ConvertSSOToBuild）
- 国产源：Kimi、智谱、MiniMax、DeepSeek、Cline 等

凭据存储：账号行上的通用 map[string]any（account_credentials_persistence.go）。Claude 存 access_token/refresh_token/expires_at/org_uuid/account_uuid；OpenAI 另存 chatgpt-account-id。

## 2. sessionKey → OAuth 三步自动换流（最关键的"订阅转 API"桥）

backend/internal/repository/claude_oauth_service.go 用 `imroc/req` 的 `ImpersonateChrome()`（Chrome TLS 指纹）+ 禁用 CookieJar，三步完成：

1. `GET https://claude.ai/api/organizations`（Cookie: sessionKey=...）→ 取 org UUID（多组织优先 team）
2. `POST https://claude.ai/v1/oauth/{orgUUID}/authorize`（Origin/Referer 伪装 claude.ai/new）→ authorization code
3. `POST https://platform.claude.com/v1/oauth/token` PKCE 换 access/refresh token

常量在 backend/internal/pkg/oauth/oauth.go：ClientID = `9d1c250a-e61b-44d9-88ed-5944d1962f5e`（Claude Code 官方 OAuth App）；scope = `user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload`；redirect_uri = `https://platform.claude.com/oauth/code/callback`；authorize 响应 state 非空时按 `code#state` 拼接、token 步拆回传。

**即：sessionKey 只用一次做引导，之后长期全走官方 OAuth，不再触碰 claude.ai Web 面。** 本项目 claude-sub 已按此实现（internal/providers/claude/sessionkey.go，42 例单测）。

## 3. 上游请求构造与指纹卫生

- 上游：`https://api.anthropic.com/v1/messages?beta=true`；OAuth 路径 Bearer；mimic 模式对齐真实 CLI 的 9 个精确头（`x-app`、`x-stainless-*`、每请求新 UUID 的 `x-client-request-id`、流式 `x-stainless-helper-method: stream`）——注释明确"透传客户端 header 会被判 third-party"
- **beta 头/body 双向 sanitize 纯函数**（gateway_request.go:1033 `sanitizeAnthropicBodyForBetaTokens`）：beta 缺 `context-management-2025-06-27` 就剥 body 的 `context_management`，否则 400；UA 版本与 body 内 billing header 的 cc_version 共用同一字符串，防"头/体版本自相矛盾"
- 版本追新：内置 CLI 版本 2.1.258 + claude_code_version_sync_service.go 在线追新；UA "只升不降"地板；逐账号持久指纹（随机 ClientID、metadata.user_id 掩码重写）
- Codex 侧：`https://chatgpt.com/backend-api/codex/responses`，UA 形态 `{originator}/{version} ({OS}; {arch}) {terminal}`，版本低于 0.144.0 直接 404

## 4. 账号池与 429 应对

- 调度（gateway_scheduling.go，2676 行）：过滤链 Schedulable→ForModel→ForQuota→ForWindowCost→ForRPM，Priority 升序稳定排序、同优先级组内洗牌；粘性会话优先带并发等待队列
- 会话哈希三级来源（gateway_service.go GenerateSessionHash）：`metadata.user_id` 的 session_id → 带 cache_control:ephemeral 的内容 → IP+UA+ApiKey+全文摘要；Anthropic 另有 digest 会话链（anthropic_session.go，TTL 300s，JSON 键序无关 canonical hash）
- 429/错误分类（ratelimit_service.go，3024 行）：解析 5h/7d 双窗口头选实际触发者并反推 session window；529 走全局冷却；401 区分"token 作废永久禁用"与"可刷新临时冷却"（默认 10 分钟）；400 文案识别"organization disabled/KYC"永久禁用；无重置头兜底冷却 5s、上限 7200s
- 阈值暂停器（account_scheduling_threshold_eval.go）：用量超阈值在窗口重置前自动摘除账号；关键词触发的临时停调 temp_unsched.go（account/model 两级隔离）

## 5. 下游端点与模型映射

`/v1/messages(+count_tokens)`、`/v1/chat/completions`、`/v1/responses`（HTTP+SSE+GET WebSocket）、`/v1/models`、`/v1/usage`、`/v1beta/*`（Gemini 原生）、`/backend-api/codex/*` 等。模型映射为账号级 model_mapping（客户端名→上游名，支持通配）；Web 专属模型名不存在——上游本就是 API 同款模型名。

## 6. 部署与稳定性

Docker Compose 推荐（PostgreSQL 16 + Redis 必需）；Issue 区高频问题：上游风控升级（Codex 强制手机验证、指纹收敛失效）、上游过载 529/503、各家限流头缺失导致节流失效。**结论：伪装参数与上游版本强耦合，需要持续追新**——项目专门做了 CLI 版本在线同步，这本身就是维护成本的直接证据。

## 7. Web 会话反代 vs OAuth 直连（客观对比）

| 维度 | Web 会话反代（local-bridge 式） | OAuth 直连（relay-panel 主路线 / 本项 claude-sub） |
|---|---|---|
| 覆盖面 | 理论上可拿 Web 独占功能，实际模型/参数受限、无官方工具协议 | 官方 /v1/messages 全功能：工具、流式、count_tokens、beta 特性 |
| 协议稳定性 | 每次网页改版即断 | API 契约稳定，仅需追 CLI 版本/指纹 |
| 风控面 | Web 防爬+验证码+Cloudflare 全套 | OAuth 属官方授权流；风险集中在指纹一致性 |
| 维护成本 | 极高 | 中（版本自动同步+指纹卫生把成本工程化） |
| 封号表征 | 会话失效 | 401 作废 / 429 窗口 / 风控分层 |

## 8. 本项已吸收与待吸收

**已吸收（2026-10-27）**：sessionKey 三步换 OAuth（claude-sub sessionkey.go，参数照抄源码）；beta 头/body 双向 sanitize（sanitizeBetaHeaders）；UA 地板 2.1.258。
**待吸收（按可行性）**：① 429 双窗口解析到账号冷却时长（现在固定 2 分钟）；② 粘性会话三级哈希（保 prompt cache）；③ SSE 断连排空抓 usage；④ UA/指纹在线追新服务。
