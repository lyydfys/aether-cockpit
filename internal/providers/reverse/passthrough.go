package reverse

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// ResponsesPassthrough 把 Responses 请求原样转发给上游。
//
// 只有声明了 NativeResponses 的渠道才应该走到这里；其余渠道返回明确错误，
// 让 Fast-Path 自动退回 IR 路径，而不是把请求发到错误端点。
func (p *Provider) ResponsesPassthrough(ctx context.Context, env *provider.AccountEnv, rawReq []byte, upstreamModel string) (*http.Response, error) {
	if !p.profile.NativeResponses {
		return nil, ir.NewAPIError(ir.ErrCodeUnsupported,
			"渠道 "+p.profile.ID+" 未声明原生 Responses 支持").WithProvider(p.profile.ID)
	}

	sp := spec.Load(p.profile.ID)
	baseURL := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if baseURL == "" && sp != nil {
		baseURL = strings.TrimRight(strings.TrimSpace(sp.BaseURL), "/")
	}
	if baseURL == "" {
		return nil, p.notConfigured("base_url")
	}
	endpointPath := sp.Endpoint("responses", p.profile.DefaultResponsesPath)
	if endpointPath == "" {
		return nil, p.notConfigured("endpoints.responses")
	}

	if upstreamModel == autoModelUpstream {
		upstreamModel = strings.TrimSpace(env.GetCredential("model"))
		if upstreamModel == "" {
			return nil, ir.NewAPIError(ir.ErrCodeUnsupported,
				"使用了 /auto 模型，但账号没有 model 凭证；请重新做本机导入，或直接写 "+p.profile.ID+"/<上游模型名>").
				WithProvider(p.profile.ID)
		}
	}

	// 用 RawMessage 保住原始字段（encrypted_content、内置工具等），只替换 model。
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rawReq, &payload); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "Responses 请求体不是合法 JSON").WithCause(err)
	}
	if upstreamModel != "" {
		if sp != nil {
			upstreamModel = sp.MapModel(upstreamModel)
		}
		m, _ := json.Marshal(upstreamModel)
		payload["model"] = m
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Responses 请求序列化失败").WithCause(err)
	}

	endpoint := baseURL + endpointPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造上游请求失败").WithCause(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	if err := applyAuth(httpReq, env, chatConfig(sp).Auth); err != nil {
		return nil, err.WithProvider(p.profile.ID)
	}
	if sp != nil {
		if sp.ClientVersion != "" {
			httpReq.Header.Set("User-Agent", "openai-node/"+sp.ClientVersion)
		}
		applyHeaders(httpReq, env, sp.HeadersTemplate)
		if sp.SignatureRule != nil {
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
	}

	applySubscriptionHeaders(httpReq, p.profile.ID, env)

	client := env.Client()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游连接失败").
			WithCause(err).WithProvider(p.profile.ID).WithRetryable(true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := readErrorBody(resp)
		return nil, mapStatusCode(resp.StatusCode, msg, resp.Header).WithProvider(p.profile.ID)
	}
	return resp, nil
}
