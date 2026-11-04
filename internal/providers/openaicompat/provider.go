// Package openaicompat 是通用 OpenAI 兼容渠道。
//
// 它和 mock 的本质区别：这是一个**真实可用**的渠道。
// 只要某个服务暴露标准的 /v1/chat/completions + SSE，就能接进来：
//   - 官方 API：DeepSeek / Kimi / 智谱 GLM / 百川 / MiniMax ...
//   - 本地推理：Ollama / vLLM / LM Studio / llama.cpp server
//   - 中转服务：任何 OpenAI 兼容的聚合端点
//
// 它同时是 _template/provider.go.tmpl 的"可执行验证"：
// 如果这个实现能跑通，说明模板给出的骨架是对的。
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/core/transport"
	"aether-cockpit/internal/core/transport/wirecodec"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/claude"
)

// Config 描述一个 OpenAI 兼容渠道实例。同一份实现既提供通用的
// openai-compat，也提供 DeepSeek / Kimi / GLM / xAI / Groq / OpenRouter /
// 本地 Ollama 等一等公民渠道（见 vendors.go）。
type Config struct {
	ID          string
	DisplayName string
	Family      string
	RegionTag   string
	BrandColor  string
	Description string
	// BaseURL 是账号没填 base_url 时的默认上游地址。
	BaseURL string
	// NoKey：本地推理服务通常不校验 Key，账号可以留空。
	NoKey bool
	// QuotaKind：额度读取方式（"cline" 走 Cline 账号接口）。
	QuotaKind string
	// NoQuota：有额度但无 API 可读（只在厂商控制台里）。为真时不再对外
	// 声明额度能力，页面也不会去探测账单接口、自然不会有 404 报错。
	NoQuota bool
	Models    []ir.ModelMeta
	// AuthStyle：空 = Authorization: Bearer；"api-key" = Azure 的 api-key 头。
	AuthStyle string
	// Azure：端点按 /openai/deployments/{deployment}/…?api-version= 拼接。
	Azure      bool
	APIVersion string
	// OAuth 非空时声明设备码登录（如 xAI Grok CLI）。
	OAuth *provider.OAuthConfig
	// SigV4Service 非空时额外支持 AWS IAM AccessKey/SecretKey 签名
	//（目前只有 Bedrock 使用；API Key 方式仍保留）。
	SigV4Service string
	// AllowAnthropicUpstream 表示该厂商除 Chat Completions 外还有
	// Anthropic Messages 端点；账号可选 api_protocol=anthropic。
	AllowAnthropicUpstream bool
	// AllowResponsesUpstream 表示该厂商除 Chat Completions 外还有
	// OpenAI Responses 端点；账号可选 api_protocol=responses。
	AllowResponsesUpstream bool
}

// Provider 是 OpenAI 兼容渠道实现。
type Provider struct{ cfg Config }

// Config 返回实例配置（供 presets 与诊断使用）。
func (p *Provider) Config() Config { return p.cfg }

// Manifest 声明渠道元数据。
//
// 模型列表刻意只登记几个代表项：真正的通用性靠 ModelRouter 的
// "已注册前缀透传"规则实现 —— 用户写 <渠道>/任意模型名 都能work，
// 不需要把上游支持的几百个模型全登记一遍。
func (p *Provider) Manifest() provider.ProviderManifest {
	cfg := p.cfg
	keyOptional := cfg.NoKey || cfg.SigV4Service != "" || cfg.Azure
	authSchema := []provider.FormFieldSchema{
		{
			Key:         "base_url",
			Label:       "Base URL",
			Type:        "text",
			Placeholder: cfg.BaseURL,
			Required:    cfg.BaseURL == "",
			Help:        "留空用渠道默认地址；要包含版本路径，不要带结尾斜杠",
		},
		{
			Key:         "api_key",
			Label:       "API Key",
			Type:        "password",
			Placeholder: "sk-...",
			Required:    !keyOptional,
			Help:        "本地推理端点可以不填",
		},
	}
	if cfg.SigV4Service != "" {
		authSchema[1].Label = "Bedrock API Key（可选）"
		authSchema[1].Help = "与下方 IAM 凭据二选一；填了 API Key 则走 Bearer，否则走 SigV4"
		authSchema = append(authSchema,
			provider.FormFieldSchema{Key: "aws_access_key_id", Label: "AWS Access Key ID", Type: "text", Help: "IAM 用户/角色的访问密钥；与 Secret Key 一起填写"},
			provider.FormFieldSchema{Key: "aws_secret_access_key", Label: "AWS Secret Access Key", Type: "password"},
			provider.FormFieldSchema{Key: "aws_session_token", Label: "AWS Session Token（可选）", Type: "password"},
			provider.FormFieldSchema{Key: "region", Label: "Region（可选）", Type: "text", Placeholder: "us-east-1", Help: "留空时从 Base URL 解析"},
		)
	}
	if cfg.Azure {
		authSchema[1].Label = "Azure API Key（可选）"
		authSchema[1].Help = "资源 api-key；与下方 Entra 应用凭据二选一"
		authSchema = append(authSchema,
			provider.FormFieldSchema{Key: "azure_tenant_id", Label: "Entra Tenant ID（可选）", Type: "text", Help: "租户 ID 或 contoso.onmicrosoft.com"},
			provider.FormFieldSchema{Key: "azure_client_id", Label: "Entra Client ID（可选）", Type: "text"},
			provider.FormFieldSchema{Key: "azure_client_secret", Label: "Entra Client Secret（可选）", Type: "password"},
			provider.FormFieldSchema{Key: "azure_scope", Label: "Entra Scope（可选）", Type: "text", Placeholder: defaultAzureTokenScope},
			provider.FormFieldSchema{Key: "azure_token_url", Label: "Entra Token URL（可选）", Type: "text", Placeholder: "https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token", Help: "世纪互联/政府云等非公有云时填写"},
		)
	}
	protocols := []string{"chat"}
	if cfg.AllowAnthropicUpstream {
		protocols = append(protocols, "anthropic")
	}
	if cfg.AllowResponsesUpstream {
		protocols = append(protocols, "responses")
	}
	if len(protocols) > 1 {
		authSchema = append(authSchema,
			provider.FormFieldSchema{Key: "api_protocol", Label: "上游协议", Type: "select", Options: protocols,
				Help: "chat = /chat/completions；anthropic = /v1/messages；responses = /responses；选后两者时 Base URL 要填对应端点"},
		)
	}
	if cfg.AllowAnthropicUpstream {
		authSchema = append(authSchema,
			provider.FormFieldSchema{Key: "api_key_auth", Label: "Anthropic Key 鉴权", Type: "select", Options: []string{"x-api-key", "bearer"},
				Help: "多数 Anthropic 端点用 x-api-key；Zhipu 等少数端点要求 Bearer"},
		)
	}
	manifest := provider.ProviderManifest{
		ID:            cfg.ID,
		DisplayName:   cfg.DisplayName,
		Family:        cfg.Family,
		RegionTag:     cfg.RegionTag,
		BrandColor:    cfg.BrandColor,
		Description:   cfg.Description,
		NetworkPolicy: provider.NetPolicySystem,
		BaseURL:       cfg.BaseURL,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthManualInput,
		},
		AuthFormSchema: authSchema,
		Models:         cfg.Models,
		// 标准 OpenAI 协议原生支持 tools，不需要 xml_tool_call 补偿。
		RequiredMiddlewares: []string{"stateful_session"},
		Capabilities: provider.CapabilitySet{
			Chat:  true,
			Quota: !cfg.NoQuota,
		},
	}
	if cfg.OAuth != nil {
		manifest.OAuth = cfg.OAuth
		manifest.SupportedAuths = []provider.AuthMethod{provider.AuthDeviceCode, provider.AuthManualInput}
	}
	return manifest
}

// genericSpecForVendor 把"通用 openai-compat spec"收敛成可安全继承的形态。
//
// 只保留"怎么说话"的部分（头模板、签名、端点路径、补偿中间件），
// 去掉"说到哪里去"和"叫什么名字"的部分（base_url、模型映射、系统提示注入）。
// 后者是示例配置，继承过去就是静默改路由、改模型名——曾经的实现整份照搬，
// 导致所有没有自己 spec 的兼容渠道都被发到示例地址 api.deepseek.com。
func genericSpecForVendor(gen *spec.Spec) *spec.Spec {
	if gen == nil {
		return nil
	}
	cp := gen.Clone()
	cp.ProviderID = ""
	cp.BaseURL = ""
	cp.Models = nil
	cp.SystemPromptInject = ""
	cp.RequestOverrides = nil
	cp.OAuth = nil
	return cp
}

// ChatStream 调用上游并解析 SSE。
func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	// spec 是可热更新的参数层：优先读渠道自己的 spec（DeepSeek / Cline /
	// Bedrock 等预设都能单独声明 base_url、头模板、签名和模型映射），
	// 没有时回退到通用 openai-compat，保持旧配置兼容。
	sp := spec.Load(p.cfg.ID)
	// 回退**只能继承行为，不能继承地址**：通用 spec 里的 base_url 是示例值
	// （当前指向 api.deepseek.com），模型映射也是示例映射。整份照搬会把任何
	// 没有自己 spec 的 OpenAI 兼容渠道——包括用户新加的渠道——的请求连同用户
	// 密钥一起发到示例地址去。所以这里只保留头模板 / 签名 / 端点 / 中间件。
	if sp == nil && p.cfg.ID != "" && p.cfg.ID != "openai-compat" {
		sp = genericSpecForVendor(spec.Load("openai-compat"))
	}

	accountBase := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	protocol := strings.ToLower(strings.TrimSpace(env.GetCredential("api_protocol")))
	if p.cfg.AllowResponsesUpstream && protocol == "responses" {
		return p.chatResponses(ctx, env, req, sp)
	}
	// Anthropic 兼容上游：复用 Claude 渠道的 Messages 协议实现；
	// 必须显式填该厂商的 Anthropic 端点，避免误拼到 Chat 端点。
	if p.cfg.AllowAnthropicUpstream && protocol == "anthropic" {
		if accountBase == "" {
			return nil, ir.NewAPIError(ir.ErrCodeBadRequest,
				"api_protocol=anthropic 时请在账号 Base URL 填写该厂商的 Anthropic 端点（通常以 /anthropic 结尾）").
				WithProvider(p.cfg.ID)
		}
		req2 := *req
		if strings.TrimSpace(req2.UpstreamModel) == "" {
			req2.UpstreamModel = stripProviderPrefix(req2.Model, p.cfg.ID)
		}
		return (&claude.Provider{}).ChatStream(ctx, env, &req2)
	}

	baseURL := accountBase
	if baseURL == "" && sp != nil {
		// 账号没填 base_url 时，允许由 spec 统一提供（多端点场景下账号仍然优先）。
		baseURL = strings.TrimRight(strings.TrimSpace(sp.BaseURL), "/")
	}
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(p.cfg.BaseURL), "/")
	}
	apiKey := strings.TrimSpace(env.GetCredential("api_key"))
	if apiKey == "" && !p.cfg.Azure {
		// OAuth 设备码账号只有 access_token；OpenAI 兼容上游同样用 Bearer。
		// Azure 的 access_token 是 Entra 令牌，必须走 Authorization Bearer，
		// 不能塞进 api-key 头。
		apiKey = strings.TrimSpace(env.GetCredential("access_token"))
	}
	awsAccess := strings.TrimSpace(env.GetCredential("aws_access_key_id"))
	awsSecret := strings.TrimSpace(env.GetCredential("aws_secret_access_key"))
	sigV4 := p.cfg.SigV4Service != "" && awsAccess != "" && awsSecret != ""

	if baseURL == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "账号缺少 base_url").
			WithProvider(p.cfg.ID)
	}
	if apiKey == "" && !p.cfg.NoKey && !sigV4 && !p.cfg.Azure {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "账号缺少 api_key（或 AWS AccessKey/SecretKey）").
			WithProvider(p.cfg.ID)
	}

	// 上游模型：优先用路由层解析出的 upstream_model，其次用原始模型名去掉渠道前缀，
	// 最后套一层 spec 的映射表（用于"网关模型名 -> 上游真实模型名"的转换）。
	upstreamModel := req.UpstreamModel
	if upstreamModel == "" {
		upstreamModel = stripProviderPrefix(req.Model, p.cfg.ID)
	}
	if sp != nil {
		upstreamModel = sp.MapModel(upstreamModel)
	}

	// Bedrock 上的 Claude 模型：OpenAI 兼容端点不支持 thinking/cache/工具块，
	// 默认改走原生 invoke；显式填 api_protocol=chat/openai 时保留旧行为。
	if p.cfg.SigV4Service == "bedrock" && isBedrockClaudeModel(upstreamModel) &&
		protocol != "chat" && protocol != "openai" {
		return p.chatBedrockClaude(ctx, env, req, upstreamModel, baseURL, awsAccess, awsSecret)
	}

	payload := buildPayload(req, upstreamModel, p.maxTokensField(upstreamModel))
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "请求序列化失败").WithCause(err)
	}

	var endpoint string
	if p.cfg.Azure {
		endpoint = baseURL + "/openai/deployments/" + url.PathEscape(upstreamModel) + "/chat/completions"
		if p.cfg.APIVersion != "" {
			endpoint += "?api-version=" + url.QueryEscape(p.cfg.APIVersion)
		}
	} else {
		endpoint = baseURL + sp.Endpoint("chat", "/chat/completions")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造上游请求失败").WithCause(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if sigV4 {
		region := strings.TrimSpace(env.GetCredential("region"))
		if region == "" {
			region = bedrockRegionFromHost(httpReq.URL.Host)
		}
		if err := signAWSV4(httpReq, body, awsAccess, awsSecret,
			env.GetCredential("aws_session_token"), region, p.cfg.SigV4Service, time.Now()); err != nil {
			return nil, ir.NewAPIError(ir.ErrCodeInternal, "AWS SigV4 签名失败: "+err.Error()).
				WithProvider(p.cfg.ID)
		}
	} else if p.cfg.Azure && apiKey == "" {
		token, err := p.azureAccessToken(ctx, env)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
	} else if apiKey != "" {
		if p.cfg.AuthStyle == "api-key" {
			httpReq.Header.Set("api-key", apiKey)
		} else {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	if sp != nil && sp.ClientVersion != "" {
		httpReq.Header.Set("User-Agent", "aether-cockpit/"+sp.ClientVersion)
	}

	// 若 spec 声明了签名规则，按规则计算并写入头。
	if sp != nil && sp.SignatureRule != nil {
		sig, tsVal, err := sp.SignatureRule.Sign(spec.SignInput{
			Method:   http.MethodPost,
			Path:     endpoint,
			Body:     body,
			DeviceID: env.Fingerprint.DeviceID,
		})
		if err != nil {
			return nil, ir.NewAPIError(ir.ErrCodeInternal, "签名计算失败").WithCause(err)
		}
		if sig != "" && sp.SignatureRule.HeaderName != "" {
			httpReq.Header.Set(sp.SignatureRule.HeaderName, sig)
		}
		if tsVal != "" && sp.SignatureRule.TimestampHeader != "" {
			httpReq.Header.Set(sp.SignatureRule.TimestampHeader, tsVal)
		}
	}

	// spec 里的头模板（支持设备指纹占位符）统一应用在最后，
	// 这样它能覆盖前面的默认值。
	if sp != nil && len(sp.HeadersTemplate) > 0 {
		transport.WithFingerprint(env.Fingerprint, sp.HeadersTemplate)(httpReq)
	}

	client := env.Client()

	// 调试开关：把即将发往上游的请求原样追加到指定文件。
	//
	// 排查"同样用 curl 能通、走网关却 401"这类问题时，唯一可靠的办法是看
	// 网关到底把请求发去了哪里、带的是什么头——凭猜测会走很多弯路。
	if path := strings.TrimSpace(os.Getenv("AETHER_DUMP_UPSTREAM")); path != "" {
		specID := "-"
		if sp != nil {
			specID = sp.ProviderID
		}
		var b strings.Builder
		b.WriteString("=== " + time.Now().Format("15:04:05.000") + " provider=" + p.cfg.ID +
			" cfgBase=" + p.cfg.BaseURL + " account=" + env.AccountID +
			" accountBase=" + accountBase + " resolved=" + baseURL + " spec=" + specID + " ===\n")
		b.WriteString(httpReq.Method + " " + httpReq.URL.String() + "\n")
		for k, v := range httpReq.Header {
			b.WriteString(k + ": " + strings.Join(v, ",") + "\n")
		}
		b.WriteString("\n")
		b.Write(body)
		b.WriteString("\n\n")
		if f, ferr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); ferr == nil {
			_, _ = f.WriteString(b.String())
			_ = f.Close()
		}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游连接失败").
			WithCause(err).WithProvider(p.cfg.ID).WithRetryable(true)
	}

	if resp.StatusCode != http.StatusOK {
		msg := readErrorBody(resp)
		ae := mapStatusCode(resp.StatusCode, msg, resp.Header)
		ae.WithProvider(p.cfg.ID)
		return nil, ae
	}

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()

		// 上游兼容性差异：少数端点不发 SSE 而直接返回一整段 JSON。
		// 用 Content-Type 粗判一下，避免把它们当流解析导致空响应。
		if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "event-stream") {
			parseWholeJSON(ctx, out, resp.Body)
			return
		}

		parseSSE(ctx, out, resp.Body)
	}()

	return out, nil
}

// maxTokensField 返回该上游接受的输出上限字段。
// OpenAI 的 o 系列 / GPT-5 只认 max_completion_tokens，传 max_tokens 会 400；
// 其他 OpenAI 兼容厂商仍以 max_tokens 为主。
func (p *Provider) maxTokensField(model string) string {
	if p.cfg.ID == "openai" || p.cfg.ID == "azure" {
		m := strings.ToLower(strings.TrimSpace(model))
		for _, prefix := range []string{"o1", "o3", "o4", "gpt-5"} {
			if strings.HasPrefix(m, prefix) {
				return "max_completion_tokens"
			}
		}
	}
	return "max_tokens"
}

// buildPayload 把 IR 转成上游请求体。
func buildPayload(req *ir.UnifiedChatRequest, upstreamModel, maxTokensField string) map[string]any {
	payload := map[string]any{
		"model":  upstreamModel,
		"stream": true,
		// 让上游在流末尾补一条 usage，否则拿不到 token 统计。
		"stream_options": map[string]any{"include_usage": true},
		"messages":       toMessages(req),
	}

	if len(req.Tools) > 0 {
		payload["tools"] = toTools(req.Tools)
		if req.ParallelToolCalls != nil {
			payload["parallel_tool_calls"] = *req.ParallelToolCalls
		}
	}
	if tc := openAIToolChoice(req.ToolChoice); tc != nil {
		payload["tool_choice"] = tc
	}
	if rf := openAIResponseFormat(req.ResponseFormat); rf != nil {
		payload["response_format"] = rf
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		payload["top_p"] = *req.TopP
	}
	// 重复惩罚必须原样下发：客户端直连官方时不会复读，走网关丢参数就会。
	if req.FrequencyPenalty != nil {
		payload["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.PresencePenalty != nil {
		payload["presence_penalty"] = *req.PresencePenalty
	}
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		field := maxTokensField
		if field == "" {
			field = "max_tokens"
		}
		payload[field] = *req.MaxOutputTokens
	}
	if len(req.StopSequences) > 0 {
		payload["stop"] = req.StopSequences
	}
	if req.ReasoningEffort != "" {
		payload["reasoning_effort"] = req.ReasoningEffort
	}
	return payload
}

// toMessages 把 IR 的 Item 列表转成 OpenAI messages 数组。
//
// 关键细节：
//   - assistant 带 tool_calls 时 content 必须为 null（不是空串），否则部分上游报错；
//   - tool 消息必须紧跟对应的 assistant.tool_calls，否则 OpenAI 会 400；
//   - reasoning 明文优先放进 reasoning_content（DeepSeek / 智谱 的通用字段）。
func toMessages(req *ir.UnifiedChatRequest) []map[string]any {
	msgs := make([]map[string]any, 0, len(req.Items)+1)

	if s := strings.TrimSpace(req.Instructions); s != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": s})
	}

	// pendingToolCalls 累积当前 assistant 轮的 tool_calls，
	// 遇到 tool 结果或新的 assistant 文本时再落盘成一条消息。
	var pendingCalls []map[string]any
	var pendingReasoning string

	flushCalls := func() {
		if len(pendingCalls) == 0 && pendingReasoning == "" {
			return
		}
		msg := map[string]any{"role": "assistant"}
		if len(pendingCalls) > 0 {
			msg["content"] = nil
			msg["tool_calls"] = pendingCalls
		} else {
			msg["content"] = ""
		}
		if pendingReasoning != "" {
			msg["reasoning_content"] = pendingReasoning
		}
		msgs = append(msgs, msg)
		pendingCalls = nil
		pendingReasoning = ""
	}

	for _, it := range req.Items {
		switch it.Type {
		case ir.ItemTypeMessage:
			flushCalls()
			content := any(it.Text)
			if len(it.Attachments) > 0 {
				content = buildMultimodalContent(it)
			}
			msgs = append(msgs, map[string]any{"role": it.Role, "content": content})

		case ir.ItemTypeReasoning:
			pendingReasoning += it.Text

		case ir.ItemTypeFunctionCall:
			pendingCalls = append(pendingCalls, map[string]any{
				"id":   it.CallID,
				"type": "function",
				"function": map[string]any{
					"name":      it.Name,
					"arguments": defaultArgs(it.Arguments),
				},
			})

		case ir.ItemTypeFunctionCallOutput:
			// 先把挂起的 tool_calls 落成 assistant 消息，再追加 tool 结果。
			flushCalls()
			msgs = append(msgs, map[string]any{
				"role":         "tool",
				"tool_call_id": it.CallID,
				"content":      it.Output,
			})
		}
	}
	flushCalls()

	return msgs
}

// buildMultimodalContent 构造带图片的消息内容。
func buildMultimodalContent(it ir.UnifiedItem) []map[string]any {
	parts := make([]map[string]any, 0, len(it.Attachments)+1)
	if strings.TrimSpace(it.Text) != "" {
		parts = append(parts, map[string]any{"type": "text", "text": it.Text})
	}
	for _, att := range it.Attachments {
		url := att.URL
		if url == "" && att.DataB64 != "" {
			url = "data:" + att.MIME + ";base64," + att.DataB64
		}
		if url == "" {
			continue
		}
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": url},
		})
	}
	return parts
}

func toTools(tools []ir.UnifiedToolSpec) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		fn := map[string]any{"name": t.Name}
		if t.Description != "" {
			fn["description"] = t.Description
		}
		if len(t.Parameters) > 0 {
			var schema any
			if err := json.Unmarshal(t.Parameters, &schema); err == nil {
				fn["parameters"] = schema
			}
		} else {
			// 没有参数的工具也必须给出 object schema，否则部分上游拒绝。
			fn["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

func defaultArgs(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

func stripProviderPrefix(model, providerID string) string {
	prefix := providerID + "/"
	if strings.HasPrefix(model, prefix) {
		return strings.TrimPrefix(model, prefix)
	}
	return model
}

// readErrorBody 读取并截断错误响应体。
func readErrorBody(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	resp.Body.Close()
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// mapStatusCode 把 HTTP 状态码翻译成内核错误。
//
// 具体分类集中在 errclass.go 的 classifyUpstream：401/403/404/402/429/5xx
// 各有不同的可重试性与用户动作，混成一句"上游鉴权失败"会让用户和调度器
// 都做错决定。
func mapStatusCode(code int, body string, hdr http.Header) *ir.APIError {
	return classifyUpstream(code, body, hdr)
}

// openAIChunk 是上游流式响应的单个 chunk。
type openAIChunk struct {
	// 少数聚合端点（如 Cline）把标准响应包在 data/response 字段里。
	Data     json.RawMessage `json:"data"`
	Response json.RawMessage `json:"response"`
	Choices  []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"` // OpenRouter / 部分中转的字段名
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// chatUsage 是 OpenAI 兼容家族的 usage 形态。
//
// 缓存读的拼法不统一：OpenAI 放在 prompt_tokens_details.cached_tokens（是
// prompt_tokens 的子集），DeepSeek 用 prompt_cache_hit_tokens/miss_tokens
// 两个平级字段，Moonshot 也用 cached_tokens。归一只在这里做一次。
type chatUsage struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	PromptTokensDetails   *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// usageToIR 把上游 usage 归一成 IR 口径：
// InputTokens 不含缓存读，CachedTokens 是命中量，TotalTokens 按各类之和重算。
func usageToIR(u *chatUsage) *ir.Usage {
	out := &ir.Usage{OutputTokens: u.CompletionTokens}
	switch {
	case u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0:
		out.CachedTokens = u.PromptTokensDetails.CachedTokens
	case u.PromptCacheHitTokens > 0:
		out.CachedTokens = u.PromptCacheHitTokens
	}

	switch {
	case out.CachedTokens > 0 && u.PromptCacheMissTokens > 0:
		// DeepSeek 直接给出未命中量，最准确。
		out.InputTokens = u.PromptCacheMissTokens
	case out.CachedTokens > 0 && u.PromptTokens > out.CachedTokens:
		out.InputTokens = u.PromptTokens - out.CachedTokens
	default:
		out.InputTokens = u.PromptTokens
	}
	if u.CompletionTokensDetails != nil {
		out.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	out.NormalizeTotal()
	return out
}

// normalizeChunk 兼容把标准 chunk 包在 data / response 字段里的聚合端点
// （Cline 等）。只有外层没有任何语义字段时才解包，避免误读正常响应。
func normalizeChunk(chunk openAIChunk) openAIChunk {
	if len(chunk.Choices) > 0 || chunk.Usage != nil || chunk.Error != nil {
		return chunk
	}
	for _, raw := range [][]byte{chunk.Data, chunk.Response} {
		if len(raw) == 0 {
			continue
		}
		var inner openAIChunk
		if json.Unmarshal(raw, &inner) == nil && (len(inner.Choices) > 0 || inner.Usage != nil || inner.Error != nil) {
			return inner
		}
	}
	return chunk
}

// heldToolCall 是乱序容错的暂存项：攒齐单个 index 的全部增量后整段吐出。
type heldToolCall struct {
	id   string
	name string
	args strings.Builder
}

// parseSSE 解析标准 SSE 流。
func parseSSE(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	dec := wirecodec.NewSSEDecoder(r)

	// toolIndexToID 记录 index -> call_id。
	// 上游只在首个 delta 里给 id，后续只给 index，必须自己配对。
	toolIndexToID := make(map[int]string, 4)
	startedTool := make(map[int]bool, 4)

	// 乱序容错（借鉴 aether heldCall / giveHeld）：
	//   - 证据：更大 index 已出现、更小 index 的 delta 又到来 -> holding 开启
	//     并保持到流结束；
	//   - 前驱 index 还没 Start 的新 index delta 也先暂存，防止抢跑；
	//   - 流结束时按 index 升序 flush，每个 index 整段吐出 Start + Delta。
	// 正常顺序流（0 的增量完毕后才出现 1）两个条件都不命中，走原有直发路径。
	maxIndexSeen := -1
	holding := false
	held := make(map[int]*heldToolCall, 4)
	heldToolSeen := false
	finishReason := ""
	emittedDone := false

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		frame, err := dec.Next()
		if err != nil {
			break
		}
		data := strings.TrimSpace(string(frame.Data))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		var chunk openAIChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// 无法解析的帧（心跳、注释）直接跳过，不影响主流程。
			continue
		}
		chunk = normalizeChunk(chunk)

		if chunk.Error != nil {
			emit(ctx, out, ir.UnifiedStreamEvent{
				Type: ir.EventError,
				Err:  ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游流内错误: "+chunk.Error.Message),
			})
			return
		}

		if chunk.Usage != nil {
			emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: usageToIR(chunk.Usage)})
		}

		for _, ch := range chunk.Choices {
			// 思考内容：兼容 reasoning_content（DeepSeek/智谱）与 reasoning（OpenRouter）
			if s := firstNonEmpty(ch.Delta.ReasoningContent, ch.Delta.Reasoning); s != "" {
				emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: s})
			}
			if ch.Delta.Content != "" {
				emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: ch.Delta.Content})
			}

			for _, tc := range ch.Delta.ToolCalls {
				// 乱序证据：更大 index 已开始、更小 index 的 delta 又到来。
				// 一旦确认，holding 保持到流结束。
				if tc.Index < maxIndexSeen {
					holding = true
				}
				if tc.Index > maxIndexSeen {
					maxIndexSeen = tc.Index
				}

				id := tc.ID
				if id == "" {
					id = toolIndexToID[tc.Index]
				} else {
					toolIndexToID[tc.Index] = id
				}

				// 暂存判定：已确认乱序（holding），或前驱 index 尚未 Start
				//（说明上游在乱序吐增量，防止后面的调用抢跑）。
				// 已经 Start 过的 index 继续走原有直发路径，行为与从前一致。
				if (holding || (tc.Index > 0 && !startedTool[tc.Index-1])) && !startedTool[tc.Index] {
					h := held[tc.Index]
					if h == nil {
						h = &heldToolCall{}
						held[tc.Index] = h
					}
					if id != "" {
						h.id = id
					}
					if h.name == "" {
						h.name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						h.args.WriteString(tc.Function.Arguments)
					}
					if tc.Function.Name != "" || tc.Function.Arguments != "" {
						heldToolSeen = true
					}
					continue
				}

				// 首次见到该 index 且拿到函数名 -> 开启一个工具调用
				if !startedTool[tc.Index] && tc.Function.Name != "" {
					startedTool[tc.Index] = true
					emit(ctx, out, ir.UnifiedStreamEvent{
						Type:       ir.EventToolCallStart,
						ToolCallID: id,
						ToolName:   tc.Function.Name,
					})
				}
				if tc.Function.Arguments != "" {
					if !startedTool[tc.Index] {
						// 上游没给 name 就给参数（不规范但存在），补一个 Start 兜底
						startedTool[tc.Index] = true
						emit(ctx, out, ir.UnifiedStreamEvent{
							Type:       ir.EventToolCallStart,
							ToolCallID: id,
							ToolName:   "",
						})
					}
					emit(ctx, out, ir.UnifiedStreamEvent{
						Type:       ir.EventToolCallDelta,
						ToolCallID: id,
						ArgsDelta:  tc.Function.Arguments,
					})
				}
			}

			if ch.FinishReason != "" {
				finishReason = ch.FinishReason
			}
		}
	}

	// 流结束：把暂存的乱序工具调用按 index 升序整段吐出（aether giveHeld 同款）。
	if len(held) > 0 {
		idxs := make([]int, 0, len(held))
		for k := range held {
			idxs = append(idxs, k)
		}
		sort.Ints(idxs)
		for _, k := range idxs {
			h := held[k]
			if h.name == "" && h.args.Len() == 0 {
				// 只收到过孤立 id、没有任何函数名/参数的 index：
				// 与既有行为一致，不产出事件。
				continue
			}
			emit(ctx, out, ir.UnifiedStreamEvent{
				Type:       ir.EventToolCallStart,
				ToolCallID: h.id,
				ToolName:   h.name,
			})
			if h.args.Len() > 0 {
				emit(ctx, out, ir.UnifiedStreamEvent{
					Type:       ir.EventToolCallDelta,
					ToolCallID: h.id,
					ArgsDelta:  h.args.String(),
				})
			}
		}
	}

	// 有些上游在 finish_reason 之后还会发 usage chunk，所以 Done 放到流末尾统一发。
	if !emittedDone {
		emittedDone = true
		if finishReason == "" {
			if len(startedTool) > 0 || heldToolSeen {
				finishReason = "tool_calls"
			} else {
				finishReason = "stop"
			}
		}
		emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finishReason})
	}
}

// parseWholeJSON 处理不支持流式、直接返回完整 JSON 的端点。
type wholeChatResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage    *chatUsage      `json:"usage"`
	Data     json.RawMessage `json:"data"`
	Response json.RawMessage `json:"response"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func parseWholeJSON(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	body, err := io.ReadAll(io.LimitReader(r, 32<<20))
	if err != nil {
		emit(ctx, out, ir.UnifiedStreamEvent{
			Type: ir.EventError,
			Err:  ir.NewAPIError(ir.ErrCodeUpstreamFailed, "读取上游响应失败").WithCause(err),
		})
		return
	}

	var outer wholeChatResponse
	if err := json.Unmarshal(body, &outer); err != nil {
		emit(ctx, out, ir.UnifiedStreamEvent{
			Type: ir.EventError,
			Err:  ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "上游返回非 SSE 且无法解析: %s", truncateStr(string(body), 200)),
		})
		return
	}
	full := outer
	if len(full.Choices) == 0 && full.Usage == nil && full.Error == nil {
		for _, raw := range [][]byte{outer.Data, outer.Response} {
			if len(raw) == 0 {
				continue
			}
			var inner wholeChatResponse
			if json.Unmarshal(raw, &inner) == nil && (len(inner.Choices) > 0 || inner.Usage != nil || inner.Error != nil) {
				full = inner
				break
			}
		}
	}
	if full.Error != nil {
		emit(ctx, out, ir.UnifiedStreamEvent{
			Type: ir.EventError,
			Err:  ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游返回错误: "+full.Error.Message),
		})
		return
	}

	finish := "stop"
	for _, ch := range full.Choices {
		if ch.Message.ReasoningContent != "" {
			emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: ch.Message.ReasoningContent})
		}
		if ch.Message.Content != "" {
			emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: ch.Message.Content})
		}
		for _, tc := range ch.Message.ToolCalls {
			emit(ctx, out, ir.UnifiedStreamEvent{
				Type:       ir.EventToolCallStart,
				ToolCallID: tc.ID,
				ToolName:   tc.Function.Name,
			})
			emit(ctx, out, ir.UnifiedStreamEvent{
				Type:       ir.EventToolCallDelta,
				ToolCallID: tc.ID,
				ArgsDelta:  tc.Function.Arguments,
			})
			finish = "tool_calls"
		}
		if ch.FinishReason != "" {
			finish = ch.FinishReason
		}
	}

	if full.Usage != nil {
		emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: usageToIR(full.Usage)})
	}
	emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finish})
}

func emit(ctx context.Context, out chan<- ir.UnifiedStreamEvent, ev ir.UnifiedStreamEvent) {
	select {
	case out <- ev:
	case <-ctx.Done():
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// 编译期确认实现了对话能力。
var _ provider.ChatCapability = (*Provider)(nil)
