package gateway

import (
	"context"
	"encoding/json"
	"strings"

	"aether-cockpit/internal/core/tools"
)

func generateImage(ctx context.Context, cfg tools.ImageConfig, body []byte) (int, []byte, error) {
	return tools.GenerateImage(ctx, cfg, body)
}

func injectJSONModel(body []byte, model string) ([]byte, bool) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, false
	}
	if _, ok := payload["model"]; !ok {
		payload["model"] = model
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return body, false
	}
	return out, true
}

func proxyImageEdit(ctx context.Context, cfg tools.ImageConfig, body []byte, contentType string) (int, []byte, error) {
	if strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		// JSON 编辑请求也允许用配置里的默认模型补全。
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err == nil {
			if cfg.Model != "" {
				if _, ok := payload["model"]; !ok {
					payload["model"] = cfg.Model
				}
			}
			if out, err := json.Marshal(payload); err == nil {
				body = out
				contentType = "application/json"
			}
		}
	}
	return tools.ProxyImage(ctx, cfg, "/images/edits", contentType, body)
}
