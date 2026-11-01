package reverse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// CompactResponses 代理 Codex 的 /responses/compact：
// 输入是 Responses 形状（含 compaction_trigger），输出是 compaction 对象。
//
// 按官方执行器契约：instructions 缺省补空串、移除 tools，其余原样转发。
func (p *Provider) CompactResponses(ctx context.Context, env *provider.AccountEnv, rawReq []byte) ([]byte, error) {
	if p.profile.ID != "codex" {
		return nil, ir.NewAPIError(ir.ErrCodeUnsupported, "该渠道不支持上下文压缩").WithProvider(p.profile.ID)
	}
	token := strings.TrimSpace(env.GetCredential("access_token"))
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Codex 账号缺少 access_token").WithProvider(p.profile.ID)
	}
	var payload map[string]any
	if err := json.Unmarshal(rawReq, &payload); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "compact 请求不是合法 JSON").WithProvider(p.profile.ID)
	}
	if v, ok := payload["instructions"]; !ok || v == nil {
		payload["instructions"] = ""
	}
	delete(payload, "tools")
	delete(payload, "tool_choice")
	body, _ := json.Marshal(payload)

	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = "https://chatgpt.com/backend-api/codex"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/responses/compact", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("User-Agent", "codex_cli_rs/0.16.0 (Aether Cockpit)")
	if acct := strings.TrimSpace(env.GetCredential("account_id")); acct != "" {
		req.Header.Set("chatgpt-account-id", acct)
	}
	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Codex compact 请求失败: "+err.Error()).
			WithProvider(p.profile.ID).WithRetryable(true)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Codex compact 返回 %d: %s", resp.StatusCode, msg).WithProvider(p.profile.ID)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("Codex compact 响应不是合法 JSON")
	}
	return raw, nil
}

var _ provider.ResponsesCompactCapability = (*Provider)(nil)
