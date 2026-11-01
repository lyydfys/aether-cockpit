package qoder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 临时探测：用本机 Qoder 登录态试候选模型目录端点。
// 仅在 QODER_PROBE=1 时运行；不打印 token。
func TestProbeModelCatalogEndpoints(t *testing.T) {
	if os.Getenv("QODER_PROBE") != "1" {
		t.Skip("set QODER_PROBE=1 to probe Qoder model catalog endpoints")
	}
	p := newProvider(cnProfile)
	auth, _, err := readDesktopAuth(p.profile)
	if err != nil || auth == nil || strings.TrimSpace(auth.Token) == "" {
		t.Fatalf("读取本机登录态失败: %v", err)
	}
	machineID := p.readMachineID()
	if machineID == "" {
		t.Fatal("缺少 machine_id")
	}
	env := provider.NewAccountEnv("probe", p.profile.id)
	env.Credentials = map[string]string{
		"token": auth.Token, "user_id": auth.User.ID, "machine_id": machineID,
	}
	env.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	c := p.credentials(env)
	ctx := context.Background()

	gwPaths := []string{
		"/algo/api/v2/model/list", "/algo/api/v2/models",
		"/algo/api/v2/service/pro/model/list", "/algo/api/v2/service/model/list",
		"/algo/api/v2/config/models", "/algo/api/v1/model/list", "/algo/api/v1/models",
		"/algo/api/v2/agent/model/list", "/algo/api/v2/model/config",
		"/algo/api/v2/client/config", "/algo/api/v2/app/config", "/algo/api/v2/user/config",
		"/algo/api/v2/service/pro/config", "/algo/api/v2/service/pro/model",
		"/algo/api/v2/model", "/algo/api/v2/models/available",
	}
	for _, path := range gwPaths {
		u := strings.TrimRight(p.profile.baseURL, "/") + path
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			t.Logf("GET %s: %v", path, err)
			continue
		}
		headers, err := p.buildCOSYHeaders(nil, u, c)
		if err != nil {
			t.Logf("GET %s: sign: %v", path, err)
			continue
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := env.Client().Do(req)
		if err != nil {
			t.Logf("GET %-40s -> network: %v", path, err)
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if path == "/algo/api/v2/model/list" && len(raw) > 0 {
			_ = os.WriteFile("C:/tmp/qoder-models.json", raw, 0o600)
		}
		t.Logf("GET %-40s -> %d %s | %s", path, resp.StatusCode,
			truncate(string(raw), 260), resp.Header.Get("Content-Type"))
	}

	openPaths := []string{
		"/sash/api/v1/models", "/sash/api/v1/model/list", "/sash/api/v1/me/models",
		"/api/v1/models", "/sash/api/v1/model/config",
	}
	for _, path := range openPaths {
		u := strings.TrimRight(p.profile.openAPI, "/") + path
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+auth.Token)
		req.Header.Set("Accept", "application/json")
		resp, err := env.Client().Do(req)
		if err != nil {
			t.Logf("OPEN %-38s -> network: %v", path, err)
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		t.Logf("OPEN %-38s -> %d %s | %s", path, resp.StatusCode,
			truncate(string(raw), 260), resp.Header.Get("Content-Type"))
	}
	fmt.Fprintln(os.Stderr, "probe complete")
}

// 临时探测：chat 请求的 model 字段应该用内部 key 还是显示名。
func TestProbeChatModelKeys(t *testing.T) {
	if os.Getenv("QODER_PROBE") != "1" {
		t.Skip("set QODER_PROBE=1 to probe Qoder chat model keys")
	}
	p := newProvider(cnProfile)
	auth, _, err := readDesktopAuth(p.profile)
	if err != nil || auth == nil || strings.TrimSpace(auth.Token) == "" {
		t.Fatalf("读取本机登录态失败: %v", err)
	}
	env := provider.NewAccountEnv("probe", p.profile.id)
	env.Credentials = map[string]string{
		"token": auth.Token, "user_id": auth.User.ID, "machine_id": p.readMachineID(),
	}
	env.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	c := p.credentials(env)
	n := 8
	for _, model := range []string{"dfmodel", "DeepSeek-V4-Flash", "nonexistent-xyz"} {
		req := &ir.UnifiedChatRequest{
			Model: p.profile.id + "/" + model, UpstreamModel: model, Stream: true,
			MaxOutputTokens: &n,
			Items:           []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "只回答数字：1+1=?"}},
		}
		body, _ := json.Marshal(buildPayload(req, model))
		resp, err := p.send(context.Background(), env, body, c)
		if err != nil {
			t.Logf("model=%-18s send: %v", model, err)
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		resp.Body.Close()
		t.Logf("model=%-18s -> %d | %s", model, resp.StatusCode, truncate(string(raw), 420))
	}
}
