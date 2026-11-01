package openaicompat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/transport/wirecodec"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/claude"
)

// Bedrock 的 OpenAI 兼容端点不提供 Claude 的完整 Messages 语义
// （thinking、cache、工具块结构），带 Claude 模型时必须走原生
// /model/{id}/invoke(-with-response-stream)，与 sub2api 行为一致。

const bedrockAnthropicVersion = "bedrock-2023-05-31"

// isBedrockClaudeModel 判断 Bedrock 模型是否需要原生 Anthropic 协议。
func isBedrockClaudeModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "anthropic.") || strings.Contains(m, "claude")
}

// bedrockRootURL 把账号填写的 OpenAI 兼容端点还原成 Bedrock Runtime 根地址。
// 默认账号填 https://bedrock-runtime.<region>.amazonaws.com/openai/v1，
// 原生 invoke 在 /model/... 下，必须去掉 /openai 及其后的路径；
// 自建反代若带路径前缀（https://proxy/bedrock），则保留前缀。
func bedrockRootURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("Bedrock Base URL 非法: %s", base)
	}
	path := strings.TrimRight(u.Path, "/")
	if i := strings.Index(strings.ToLower(path), "/openai"); i >= 0 {
		path = path[:i]
	}
	u.Path = path
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// chatBedrockClaude 走 Bedrock 原生 Anthropic 协议。
func (p *Provider) chatBedrockClaude(
	ctx context.Context,
	env *provider.AccountEnv,
	req *ir.UnifiedChatRequest,
	upstreamModel, baseURL, accessKey, secretKey string,
) (<-chan ir.UnifiedStreamEvent, error) {
	model, thinking := claude.SplitThinkingModel(upstreamModel)
	if model == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "Bedrock Claude 缺少模型名").
			WithProvider(p.cfg.ID)
	}

	body := claude.BuildMessagesBody(req, model, thinking)
	// 模型走 URL 路径；流式与否由端点决定，Body 里都不能带。
	delete(body, "model")
	delete(body, "stream")
	body["anthropic_version"] = bedrockAnthropicVersion
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Bedrock 请求序列化失败").
			WithCause(err).WithProvider(p.cfg.ID)
	}

	root, err := bedrockRootURL(baseURL)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, err.Error()).WithProvider(p.cfg.ID)
	}
	action := "invoke"
	if req.Stream {
		action = "invoke-with-response-stream"
	}
	endpoint := root + "/model/" + url.PathEscape(model) + "/" + action

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Bedrock 请求失败").
			WithCause(err).WithProvider(p.cfg.ID)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.Stream {
		httpReq.Header.Set("Accept", "application/vnd.amazon.eventstream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}

	if accessKey != "" && secretKey != "" {
		region := strings.TrimSpace(env.GetCredential("region"))
		if region == "" {
			region = bedrockRegionFromHost(httpReq.URL.Host)
		}
		if region == "" {
			return nil, ir.NewAPIError(ir.ErrCodeBadRequest,
				"Bedrock 签名需要 region，请在账号里选择区域或填写 region").
				WithProvider(p.cfg.ID)
		}
		if err := signAWSV4(httpReq, payload, accessKey, secretKey,
			env.GetCredential("aws_session_token"), region, p.cfg.SigV4Service, time.Now()); err != nil {
			return nil, ir.NewAPIError(ir.ErrCodeInternal, "AWS SigV4 签名失败: "+err.Error()).
				WithProvider(p.cfg.ID)
		}
	} else if key := strings.TrimSpace(env.GetCredential("api_key")); key != "" {
		// Bedrock API Key 走 Bearer，与 OpenAI 兼容端点共用同一把钥匙。
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := env.Client().Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Bedrock 连接失败").
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

		if !req.Stream {
			claude.ParseMessagesJSON(ctx, out, resp.Body)
			return
		}
		if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "eventstream") {
			// 个别反代丢了 EventStream 头，直接把整段 JSON 当非流式响应处理。
			claude.ParseMessagesJSON(ctx, out, resp.Body)
			return
		}
		pr, pw := io.Pipe()
		defer pr.Close()
		go bedrockEventStreamToSSE(ctx, pw, resp.Body)
		claude.ParseMessagesSSE(ctx, out, pr)
	}()

	return out, nil
}

// bedrockEventStreamToSSE 把 Bedrock EventStream 帧翻译成 Anthropic SSE。
// 每帧载荷 {"bytes":"<base64>"} 里是一段完整 Anthropic 流事件的 JSON。
func bedrockEventStreamToSSE(ctx context.Context, pw *io.PipeWriter, r io.Reader) {
	defer pw.Close()

	dec := wirecodec.NewAWSEventStreamDecoder(r)
	for {
		select {
		case <-ctx.Done():
			pw.CloseWithError(ctx.Err())
			return
		default:
		}

		msg, err := dec.Next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				writeBedrockStreamError(pw, "EventStream 解码失败: "+err.Error())
			}
			return
		}
		if msg.MessageType() == "exception" || msg.MessageType() == "error" {
			writeBedrockStreamError(pw, bedrockExceptionMessage(msg))
			return
		}

		event := msg.Payload
		var wrapper struct {
			Bytes string `json:"bytes"`
		}
		if json.Unmarshal(msg.Payload, &wrapper) == nil && wrapper.Bytes != "" {
			raw, decErr := base64.StdEncoding.DecodeString(wrapper.Bytes)
			if decErr != nil {
				raw, decErr = base64.RawStdEncoding.DecodeString(wrapper.Bytes)
			}
			if decErr != nil {
				continue
			}
			event = raw
		}
		event = bytes.TrimSpace(event)
		if len(event) == 0 {
			continue
		}
		if _, err := pw.Write([]byte("data: ")); err != nil {
			return
		}
		if _, err := pw.Write(event); err != nil {
			return
		}
		if _, err := pw.Write([]byte("\n\n")); err != nil {
			return
		}
	}
}

func writeBedrockStreamError(pw *io.PipeWriter, message string) {
	errEvent, _ := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "upstream_error", "message": message},
	})
	_, _ = pw.Write(append(append([]byte("data: "), errEvent...), []byte("\n\n")...))
}

func bedrockExceptionMessage(msg *wirecodec.AWSMessage) string {
	var detail struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(msg.Payload, &detail)
	text := strings.TrimSpace(detail.Message)
	if text == "" {
		text = strings.TrimSpace(string(msg.Payload))
	}
	kind := msg.ExceptionType()
	if kind == "" {
		kind = "upstream_error"
	}
	if text == "" {
		return kind
	}
	return kind + ": " + text
}
