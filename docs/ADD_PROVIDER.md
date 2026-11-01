# 接入一个渠道

本文回答一个问题：**我手上有一个 AI 产品的账号，怎么把它接进网关？**

先判断你属于哪一类，三条路径的工作量差 10 倍。

---

## 第一步：判断类型

| 类型 | 特征 | 做法 | 工作量 |
|---|---|---|---|
| **A. 标准协议** | 服务商公开文档里有 `/v1/chat/completions`，给 API Key 就能用 | 配置界面填 `base_url` + `api_key`，**不用写代码** | 2 分钟 |
| **B. 逆向客户端** | 只有桌面/网页客户端，没有公开 API；或公开 API 需要客户端专属签名 | 抓包 → 填 `configs/specs/<id>.json`（通用引擎）；特殊协议才用 `_template` 写插件 | 半小时到两天 |
| **C. 本机导入** | 客户端已在本机登录，配置文件里有现成 token | 在 spec 的 `extra.local_import` 填路径与字段（通用引擎）；SQLite/LevelDB 等特殊格式才写 `LocalImportCapability` | 取决于配置文件格式 |

A 类直接看 [README「接入一个真实端点」](../README.md#接入一个真实端点现在就能用)。本文只讲 B 和 C。

---

## 第二步：采集清单

抓包前先把这张表填满。**缺任何一项都会导致写出来的插件跑不通**，而且失败现象往往是"400"这种没有信息量的错误。

### 必填

- [ ] **对话端点**：完整 URL，含路径。例：`https://api.example.com/v2/conversation/chat`
- [ ] **HTTP 方法**：POST / GET / WebSocket
- [ ] **鉴权方式**：
  - [ ] `Authorization: Bearer <token>` ?
  - [ ] `Cookie: <session>` ?
  - [ ] 自定义头（`X-Token` / `X-Api-Key`）？
  - [ ] token 有效期多久？有没有 refresh 端点？
- [ ] **必带请求头**：客户端版本、设备 ID、平台标识等。缺了会被拒绝的那些
- [ ] **请求体结构**：至少一份完整样例
  - [ ] 消息数组叫什么？（`messages` / `contents` / `prompt`）
  - [ ] 角色怎么表达？（`role: "user"` / `author: "human"`）
  - [ ] 是否需要先建会话？（`conversation_id` / `session_id` / `chat_id`）
- [ ] **响应格式**：
  - [ ] SSE（`text/event-stream`）？还是 WebSocket？还是一次性 JSON？
  - [ ] 如果是 SSE，单个 chunk 的原文什么样？
  - [ ] 增量文本在哪个字段？（`delta.content` / `choices[0].text` / 自定义）
- [ ] **结束信号**：`data: [DONE]` / 特定 event / 连接关闭

### 有则更好

- [ ] **工具调用怎么表达**：原生 `tools` 字段？还是靠提示词让模型输出特定格式？
- [ ] **思考过程字段**：`reasoning_content` / `thinking` / 无
- [ ] **签名算法**：参与签名的字段、算法、盐值来源
- [ ] **限流特征**：429 的响应体长什么样
- [ ] **额度查询端点**（如果有）
- [ ] **签到端点**（如果有）

---

## 第三步：怎么抓

### 场景 1：网页端（浏览器）

Chrome DevTools → Network → 筛选 `Fetch/XHR` → 操作一次 → 找到目标请求 → 右键 `Copy as cURL`。

抓 SSE 时注意：DevTools 里响应是流式的，直接看 `Response` 标签会被截断。用 `Copy as cURL` 后在终端执行，能看到完整流。

### 场景 2：桌面客户端（Electron / 原生）

绝大多数桌面客户端走 HTTPS，需要中间人代理：

```bash
# 1. 装 mitmproxy
pip install mitmproxy
mitmweb --listen-port 8888

# 2. 让客户端走代理
#    Electron 应用通常直接读系统代理，或支持启动参数：
client.exe --proxy-server="http://127.0.0.1:8888"

# 3. 装证书
#    浏览器访问 http://mitm.it 下载并信任证书
#    Electron 需要注意：部分应用开了证书固定（certificate pinning），
#    这种情况抓不到，只能改用方案 3
```

抓到后同样 `Copy as cURL`。

### 场景 3：读本地配置文件（最省事，也最安全）

很多客户端把 token 明文存在配置目录里。这比抓包省事，而且不涉及中间人：

| 平台 | 常见位置 |
|---|---|
| Windows | `%APPDATA%\<产品名>\`、`%LOCALAPPDATA%\<产品名>\` |
| macOS | `~/Library/Application Support/<产品名>/` |
| Linux | `~/.config/<产品名>/` |

常见文件名：`config.json`、`auth.json`、`settings.json`、`state.db`（SQLite）、`Local Storage\leveldb\`（Chromium 系）。

**建议顺序**：先翻配置文件找 token → 找不到再抓包。找到 token 后仍需要抓一次包确认协议。

### A 类变种：CLI 工具

```bash
# 大多数 CLI 支持环境变量指定 base_url
HTTP_PROXY=http://127.0.0.1:8888 HTTPS_PROXY=http://127.0.0.1:8888 tool --some-command
```

---

## 第四步：填写 spec（逆向渠道首选）

如果目标是新的字段映射型渠道，
**不需要复制模板写代码**。`internal/providers/reverse` 提供通用引擎，
协议差异全部由 `configs/specs/<provider>.json` 描述（参考 `configs/specs/opencode.json`）。

Codex 与 OpenCode 已经内置真实端点，不需要 spec；Antigravity / Qoder / Trae / WorkBuddy / Kiro
因为要处理 OAuth、COSY 签名、自定义 Base64、tc 解密、专有 SSE 信封、完整客户端头
或 AWS EventStream，分别独立实现在 `internal/providers/antigravity`、`qoder`、`trae`、
`workbuddy`、`kiro`。

### 4.1 最小可用集

```json
{
  "provider_id": "my-reverse",
  "spec_version": "2026.10.26.1",
  "base_url": "https://抓包得到的域名",
  "endpoints": { "chat": "/抓包得到的对话路径" },
  "middlewares": ["stateful_session"],
  "models": { "claude-sonnet": "上游真实模型名" },
  "headers_template": {
    "X-Client-Version": "{{.Fingerprint.ClientVersion}}",
    "X-Device-Id": "{{.Fingerprint.DeviceID}}"
  },
  "extra": {
    "chat": {
      "auth": {
        "header": "Authorization",
        "prefix": "Bearer ",
        "credential": "access_token"
      },
      "request": {
        "model_field": "model",
        "messages_field": "messages",
        "stream_field": "stream",
        "system_in_messages": true,
        "extra_fields": {}
      },
      "response": {
        "format": "sse",
        "text_path": "choices.0.delta.content",
        "reasoning_paths": ["choices.0.delta.reasoning_content"],
        "tool_calls_path": "choices.0.delta.tool_calls",
        "tool_id_path": "id",
        "tool_name_path": "function.name",
        "tool_args_path": "function.arguments",
        "tool_index_path": "index",
        "finish_path": "choices.0.finish_reason",
        "usage_path": "usage",
        "usage_input_path": "prompt_tokens",
        "usage_output_path": "completion_tokens",
        "usage_total_path": "total_tokens",
        "session_id_path": "conversation_id",
        "done_markers": ["[DONE]"]
      }
    }
  }
}
```

改完点管理界面的「重载参数」，或等远程 spec 自动拉取；**进程不需要重启**。

### 4.2 `extra.chat` 字段表

| 字段 | 作用 |
|---|---|
| `auth.header` / `auth.prefix` / `auth.no_prefix` | 凭证放在哪个头、用什么前缀；`X-Api-Key` 这类直接放 token 的用 `no_prefix: true` |
| `auth.credential` | 读账号的哪个凭证字段；留空按 `access_token → token → api_key → cookie` 探测 |
| `auth.cookie` / `auth.none` | 写 Cookie 头 / 不需要鉴权 |
| `request.model_field` / `messages_field` / `stream_field` | 上游请求体字段名 |
| `request.stream_value` | 流式开关的值，默认 `true` |
| `request.system_field` | 把 Instructions 放到顶层哪个字段 |
| `request.system_in_messages` | 默认 `true`；上游不收 system 消息时设为 `false` 并配合 `system_field` |
| `request.tools_field` | 工具定义字段名 |
| `request.extra_fields` | 合并进请求体的固定字段（版本号、平台标识等） |
| `response.format` | `sse`（默认）/ `ndjson` / `json` / `aws_eventstream` |
| `response.text_path` / `reasoning_paths` | 正文与思考增量的字段路径 |
| `response.tool_calls_path` 及子路径 | 工具调用数组与 id/name/arguments/index 的字段路径 |
| `response.finish_path` / `usage_*_path` | 结束原因与 token 统计 |
| `response.session_id_path` | 上游会话 ID，供状态缝合记录 |
| `response.error_path` / `error_message_path` | 流内错误的判定字段 |
| `response.done_markers` | 结束标记，默认 `["[DONE]"]` |

路径用点号分隔，数组下标用数字，例如 `choices.0.delta.content`。

### 4.3 `extra.local_import`：本机凭证导入

```json
"local_import": {
  "paths": [
    "%APPDATA%/YourProduct/auth.json",
    "~/.yourproduct/credentials.json",
    "/path/with/*.json"
  ],
  "format": "json",
  "label_path": "user.email",
  "credentials": {
    "access_token": "auth.accessToken",
    "refresh_token": "auth.refreshToken"
  },
  "expires_at_path": "auth.expiresAt"
}
```

- 支持 `~`、`$VAR`、`%VAR%` 与 glob；目录会扫描其中 `*.json`。
- `format: "text"` 表示整个文件内容就是一个 token（`credentials` 的值随便填）。
- `format: "toml"` 用 `section.key` 路径读取 TOML，例如
  `model_providers.custom.experimental_bearer_token`。
- 支持 Unix 秒/毫秒、数字字符串、RFC3339 的过期时间。
- `state.vscdb` 这类 SQLite / LevelDB 需要先导出为 JSON，或按渠道补一个解析器。

**内置规则**（不写 spec 也能用）：

| 渠道 | 路径 | 格式 | 提取 |
|---|---|---|---|
| `codex` | `~/.codex/config.toml` | TOML | `base_url` / `experimental_bearer_token` / `model` |
| `opencode` | `~/.local/share/opencode/auth.json` | JSON | `opencode-go.key` → `api_key` |
| `antigravity` | Windows 凭据管理器 `gemini:antigravity` | 内置 | `token.access_token` / `token.refresh_token` / `token.expiry` |
| `qoder-cn` / `qoder-intl` | `%APPDATA%\com.qodercn.app.stable\auth.v1.dat` | 独立包 | DPAPI + AES-GCM，token/refreshToken/user/machine_id |
| `trae-cn` / `trae-intl` | `%APPDATA%\Trae CN\User\globalStorage\storage.json` | 独立包 | tc AES 解密 `iCubeAuthInfo://icube.cloudide` |
| `workbuddy-cn` / `workbuddy-intl` | `%LOCALAPPDATA%\CodeBuddyExtension\Data\Public\auth\workbuddy-desktop{,-ai}.info` | 独立包 | 明文 accessToken；5.6+ suite 信封会提示手动 token |

### 4.4 Codex / OpenCode / Antigravity / Qoder / Trae / WorkBuddy：零 spec 接入

**Codex**：CLI 自定义 provider 配置已被内置规则覆盖：

1. 管理界面 → Codex →「扫描本机登录态」；
2. 导入；
3. 用 `codex/auto`（模型名读取导入的 `model` 凭证）或 `codex/<模型名>` 请求
   `/v1/responses`。

上游声明原生 Responses 时走 Fast-Path；这条链路已对真实自定义 provider 端到端验证。

**OpenCode**：从 `~/.local/share/opencode/auth.json` 导入 `opencode-go.key`，直连
`https://opencode.ai/zen/v1/chat/completions`；模型清单已内置 14 个，新模型走前缀透传。

**Antigravity**：从 Windows 凭据管理器导入 `gemini:antigravity`；首次请求会自动
`loadCodeAssist` 发现项目，token 过期前自动刷新。协议细节见
`internal/providers/antigravity/chat.go`，不通过 spec 配置。

**Qoder**：从桌面 `auth.v1.dat`（DPAPI + AES-GCM）导入 token / machine_id；
请求体用自定义 Base64 编码，并用 COSY 信封签名（RSA + AES-128-CBC + MD5）。
详见 `internal/providers/qoder/auth.go`。

**Trae**：从 `storage.json` 的 `iCubeAuthInfo://icube.cloudide`（tc AES 解密）导入；
请求走 `/api/agent/v3/llm_utils_chat`，头带 `Cloud-IDE-JWT`、`x-device-id`、`x-machine-id`、`x-uid`。
详见 `internal/providers/trae/chat.go`。

**WorkBuddy**：标准 OpenAI 兼容 `<API>/v2/chat/completions`，但需要完整客户端头
（`X-User-Id`、`X-Domain`、`X-IDE-Type`、`X-Conversation-*` 等）。5.6+ 桌面登录态是
suite 加密信封，请用手动 token 或浏览器登录。详见 `internal/providers/workbuddy/chat.go`。

### 4.5 `oauth`：授权与刷新

```json
"oauth": {
  "authorize_url": "https://.../authorize",
  "token_url": "https://.../token",
  "device_code_url": "https://.../device",
  "client_id": "从客户端里扒出来的 ID",
  "scopes": ["openid", "profile"],
  "redirect_path": "/oauth/callback",
  "token_field": "access_token",
  "refresh_field": "refresh_token",
  "expires_field": "expires_at"
}
```

填了 `token_url` 后，管理界面的「授权登录 / 刷新凭证」按钮和内核的定时保活都会自动生效。

### 4.6 渠道级系统提示词注入与参数覆盖（spec_overrides）

有些上游"必须带一句特定系统提示才正常工作"，或者"不传 max_tokens 就会挂"。这类补偿不需要写插件：
在 spec JSON 里声明 `system_prompt_inject` 与 `request_overrides`，并在 `middlewares` 数组加上
`"spec_overrides"` 启用该中间件：

```json
{
  "provider_id": "somechannel",
  "middlewares": ["xml_tool_call", "spec_overrides"],
  "system_prompt_inject": "You must always answer in JSON.",
  "request_overrides": { "max_output_tokens": 8192, "temperature": 0.3 }
}
```

语义（实现见 `internal/core/pipeline/mw_spec_overrides.go`）：

- `system_prompt_inject` 追加到请求 Instructions 末尾（原指令在前，注入在后，空指令直接采用注入值）；
- `request_overrides` 只是**缺省值**：客户端显式给了 temperature / max_tokens 时以客户端为准，绝不覆盖用户意图；
- 改 spec 点「重载参数」即生效，无需重新编译——这是"机制与策略分离"的直接受益。

### 4.7 什么时候仍需要写代码

通用引擎覆盖的是 JSON / SSE 类协议。遇到以下情况仍需按第五步写一个小插件：

- 签名算法不在 `hmac_sha256 / sha256 / md5` 之内；
- WebSocket 双向流；
- 凭证藏在 SQLite / LevelDB / 浏览器 Local Storage / 系统凭据管理器，需要专用解析；
- 上游响应不是 JSON 字段，而是自定义二进制结构（AWS EventStream 已内置）；
- 请求要先做 OAuth 刷新、项目发现或多层信封转换（Antigravity 就是因此独立实现）。

---

## 第五步：写插件（仅特殊协议）

复制模板：

```bash
cp -r internal/providers/_template internal/providers/yourchannel
mv internal/providers/yourchannel/provider.go.tmpl internal/providers/yourchannel/provider.go
```

然后改四个地方：

### 5.1 Manifest

```go
func (p *Provider) Manifest() provider.ProviderManifest {
    return provider.ProviderManifest{
        ID:          "yourchannel",        // 全局唯一，会成为模型前缀
        DisplayName: "Your Product",
        RegionTag:   "CN",                 // 影响 UI 上的徽标
        BrandColor:  "#0EA5E9",

        // 决定网络出口，选错了会被风控
        NetworkPolicy: provider.NetPolicyDirectForce,  // CN 渠道必须直连

        SupportedAuths: []provider.AuthMethod{provider.AuthManualInput},
        AuthFormSchema: []provider.FormFieldSchema{
            {Key: "token", Label: "Token", Type: "textarea", Required: true},
        },

        Models: []ir.ModelMeta{
            {
                ID:            "yourchannel/model-name",   // 对外模型名
                UpstreamModel: "model-name",               // 发给上游的真实标识
                Capabilities:  ir.ModelCapabilities{ToolCall: true, Streaming: true},
            },
        },

        // 关键：决定挂哪些补偿中间件
        RequiredMiddlewares: []string{"stateful_session"},

        Capabilities: provider.CapabilitySet{Chat: true},
    }
}
```

**中间件怎么选**：

| 上游情况 | 挂什么 |
|---|---|
| 要先进 `conversation_id` 才能对话 | `stateful_session` |
| 不支持原生 `tools`，只能靠提示词 | `xml_tool_call` |
| 不接受 `role: system` | `system_role_merge` |
| 以上都没有 | 只挂 `stateful_session`（无害） |

### 5.2 ChatStream

模板里已经写好了完整骨架。你只需要替换三处：

```go
// ① 请求体构造：按你抓到的格式改
payload := map[string]any{
    "model":    upstreamModel,
    "stream":   true,
    "messages": toYourFormat(req),   // ← 改这里
}

// ② 端点与鉴权
endpoint := baseURL + sp.Endpoint("chat", "/v1/chat")
httpReq.Header.Set("Authorization", "Bearer "+token)   // ← 按实际情况

// ③ 响应解析：把上游 chunk 映射成 IR 事件
//    只需要产出这几种事件，其余都由出站 transducer 自动补全：
//    EventTextDelta / EventReasoningDelta / EventToolCallStart /
//    EventToolCallDelta / EventUsage / EventDone / EventError
```

**这是整个接入过程唯一需要动脑的地方**：把上游的 SSE chunk 翻译成 IR 事件。对照关系：

| 上游 chunk 里的字段 | 产出什么事件 |
|---|---|
| 文本增量 | `ir.EventTextDelta{TextDelta: s}` |
| 思考增量 | `ir.EventReasoningDelta{TextDelta: s}` |
| 工具调用开始（有 name） | `ir.EventToolCallStart{ToolCallID, ToolName}` |
| 工具参数增量 | `ir.EventToolCallDelta{ToolCallID, ArgsDelta}` |
| token 计数 | `ir.EventUsage{Usage: &ir.Usage{...}}` |
| 结束原因 | `ir.EventDone{FinishReason}` |
| 错误 | `ir.EventError{Err: ir.NewAPIError(...)}` |

**不要**在插件里处理 SSE 的分帧细节——用 `wirecodec`：

```go
dec := wirecodec.NewSSEDecoder(resp.Body)        // 标准 SSE
dec := wirecodec.NewAWSEventStreamDecoder(r)     // AWS 二进制帧
dec := wirecodec.NewNDJSONDecoder(r)             // 逐行 JSON
```

### 5.3 注册

```go
// cmd/cockpit/main.go
_ "aether-cockpit/internal/providers/yourchannel"
```

### 5.4 写 spec

把易变参数从代码里搬到 `configs/specs/yourchannel.json`：

```json
{
  "provider_id": "yourchannel",
  "spec_version": "2026.10.26.1",
  "base_url": "https://api.example.com",
  "client_version": "2.3.1",
  "headers_template": {
    "X-Client-Version": "{{.Fingerprint.ClientVersion}}",
    "X-Device-Id": "{{.Fingerprint.DeviceID}}"
  },
  "models": { "model-name": "model-name-v2" },
  "endpoints": { "chat": "/v2/chat" },
  "signature_rule": {
    "algorithm": "hmac_sha256",
    "salt_plain": "从客户端里扒出来的盐",
    "payload_template": "{{.Method}}\n{{.Path}}\n{{.Timestamp}}",
    "header_name": "X-Signature",
    "timestamp_header": "X-Timestamp"
  }
}
```

代码里读：

```go
sp := spec.Load("yourchannel")
baseURL := sp.BaseURL                       // nil 安全，会退化到内置默认
endpoint := baseURL + sp.Endpoint("chat", "/v1/chat")
sig, ts, _ := sp.SignatureRule.Sign(spec.SignInput{...})
```

**这么做的收益**：下次上游把 `client_version` 从 2.3.1 改成 2.4.0、或换了盐值，你只需要改这个 JSON 并点「重载参数」，不用重新编译。

---

## 第六步：验证

```bash
go build -o bin/cockpit.exe ./cmd/cockpit && ./bin/cockpit.exe
go run ./cmd/smoke -addr http://127.0.0.1:7860 -model yourchannel/model-name
```

`smoke` 会依次验证：健康检查 → 渠道 → 账号 → 模型列表 → 接入片段 → 渠道参数 → 三协议 → 响应快照 → 状态缝合 → 工具调用 → 事件流 → 自检（共 28 项）。

只测你这一个渠道时会更聚焦。逐项失败的含义：

| 失败项 | 大概率原因 |
|---|---|
| 健康检查 | 网关没起来 / addr 不对 |
| 账号列表 0 可用 | 凭证填错、或账号已被上游标记 |
| chat 非流式 400 | 请求体字段名对不上，回去核对你抓的包 |
| chat 流式没有 `[DONE]` | 你的 `EventDone` 没发出来 |
| responses 流式缺 `content_part.added` | 正常，这个由 transducer 自动补，若缺说明走了 Fast-Path 但上游不规范 |
| 状态缝合失败 | 上游不认 `conversation_id`，或 `stateful_session` 没挂 |
| 工具调用 SKIP | 模型自己没选择调用工具，不是故障 |

---

## 常见坑

**1. 请求体字段名差一个字母**

`messages` vs `message`、`content` vs `contents`、`role: "assistant"` vs `role: "ai"`。抓到的包长什么样就照抄，不要"顺手改成标准格式"。

**2. 少了一个看起来无关的头**

`X-Request-Id`、`X-Trace-Id`、`Origin`、`Referer` —— 有些服务端会校验 `Origin` 是否来自自家客户端。抓包时**完整复制所有头**，然后逐个删掉测试哪个是必需的。

**3. SSE 里混了非 JSON 行**

心跳、注释行（`: ping`）、空行。用 `wirecodec` 的解码器会自动跳过，自己手写 `bufio.Scanner` 容易在这里翻车。

**4. 工具调用只给了 index，没给 id**

上游通常只在第一个 delta 给 `id`，后续只给 `index`。必须自己维护 `index → call_id` 映射，否则多个工具调用会串成一条。

**5. CN 渠道走了代理**

如果用户开着 Clash 的 TUN 模式，`direct_force` 是失效的（TUN 劫持的是网络栈，不是环境变量）。网关启动时会检测并告警，但绕不过去。

**6. 凭证过期没有刷新**

很多 token 只有几小时有效期。优先在 spec 里填 `oauth.token_url` 和 `refresh_field`，内核的定时保活会自动续期；
如果刷新语义不是标准 OAuth，再实现 `TokenRefreshCapability`。两者都没有时，用户每天要重新登录一次。

---

## 做完之后

如果走的是第四步的 spec 路线，到这里就可以结束了：不需要写 Go 代码。
如果走第五步写了插件，回头看 `internal/providers/openaicompat/provider.go` —— 这是一个完整的、经过端到端验证的真实渠道实现。插件结构与它高度相似，差异主要在 `toMessages`（请求体构造）和 `parseSSE`（响应解析）这两个函数里。
通用引擎本身在 `internal/providers/reverse/`，遇到字段映射覆盖不到的情况，优先扩展 spec，而不是复制一份 provider。
