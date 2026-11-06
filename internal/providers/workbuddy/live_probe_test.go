package workbuddy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

// TestLiveQuota 只在 WB_LIVE=1 时运行：读取本机真实登录态并查询积分，不输出凭证。
func TestLiveQuota(t *testing.T) {
	if os.Getenv("WB_LIVE") != "1" {
		t.Skip("set WB_LIVE=1 to probe the real local WorkBuddy account")
	}
	for _, prof := range []profile{cnProfile, intlProfile} {
		path, err := localInfoPath(prof.infoFile)
		if err != nil {
			t.Logf("%s: path: %v", prof.id, err)
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Logf("%s: read: %v", prof.id, err)
			continue
		}
		draft, err := parseLocalAuth(raw, prof, path)
		if err != nil || draft == nil {
			t.Logf("%s: parse: %v", prof.id, err)
			continue
		}
		env := provider.NewAccountEnv(prof.id+"-live", prof.id)
		env.Credentials = draft.Credentials
		env.HTTPClient = &http.Client{}
		q, err := newProvider(prof).QueryQuota(context.Background(), env)
		if err != nil {
			t.Logf("%s: quota error: %v", prof.id, err)
			continue
		}
		t.Logf("%s: plan=%s windows=%d balance=%.1f %s message=%s", prof.id, q.Plan, len(q.Windows), q.Balance, q.BalanceUnit, q.Message)
		for _, w := range q.Windows {
			t.Logf("%s: window %s used=%.2f%% amount=%d limit=%d unit=%s resets=%d", prof.id, w.Name, w.Used, w.Amount, w.Limit, w.Unit, w.ResetsAt)
		}
	}
}

func TestLiveResourceShape(t *testing.T) {
	if os.Getenv("WB_LIVE") != "1" {
		t.Skip("set WB_LIVE=1")
	}
	path, err := localInfoPath(cnProfile.infoFile)
	if err != nil {
		t.Skip(err)
	}
	raw, _ := os.ReadFile(path)
	draft, err := parseLocalAuth(raw, cnProfile, path)
	if err != nil || draft == nil {
		t.Skipf("parse: %v", err)
	}
	body := `{"PageNumber":1,"PageSize":100,"ProductCode":"p_tcaca","Status":[0,3],"OnlyValidPeriod":true}`
	req, _ := http.NewRequest(http.MethodPost, "https://copilot.tencent.com/v2/billing/meter/get-user-resource", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+draft.Credentials["token"])
	req.Header.Set("X-User-Id", draft.Credentials["user_id"])
	req.Header.Set("X-Domain", draft.Credentials["domain"])
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	t.Logf("http=%d code=%v dataKeys=%v", resp.StatusCode, out["code"], keysOf(out["data"]))
	if data, ok := out["data"].(map[string]any); ok {
		if resp, ok := data["Response"].(map[string]any); ok {
			t.Logf("Response keys=%v", keysOf(resp))
			if d, ok := resp["Data"].(map[string]any); ok {
				t.Logf("Response.Data keys=%v", keysOf(d))
				for k, v := range d {
					if arr, ok := v.([]any); ok && len(arr) > 0 {
						if item, ok := arr[0].(map[string]any); ok {
							nums := map[string]any{}
							for ik, iv := range item {
								switch iv.(type) {
								case float64, bool:
									nums[ik] = iv
								}
							}
							t.Logf("array %s itemKeys=%v numbers=%v", k, keysOfSlice(arr), nums)
						}
					}
				}
			}
		}
	}
}

func keysOf(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	return keysOf(arr[0])
}

// TestProbeConfigCatalog 拉取上游 /v3/config，原样落盘供检查模型条目里
// 是否带积分倍率/价格字段。只在 WB_LIVE=1 时运行，不打印 token。
func TestProbeConfigCatalog(t *testing.T) {
	if os.Getenv("WB_LIVE") != "1" {
		t.Skip("set WB_LIVE=1 to probe the real WorkBuddy config")
	}
	for _, prof := range []profile{intlProfile, cnProfile} {
		path, err := localInfoPath(prof.infoFile)
		if err != nil {
			t.Logf("%s: path: %v", prof.id, err)
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Logf("%s: read: %v", prof.id, err)
			continue
		}
		draft, err := parseLocalAuth(raw, prof, path)
		if err != nil || draft == nil {
			t.Logf("%s: parse: %v", prof.id, err)
			continue
		}
		base := providerOrigin(prof.baseURL)
		url := base + "/v3/config"
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Logf("%s: req: %v", prof.id, err)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+draft.Credentials["token"])
		req.Header.Set("X-User-Id", draft.Credentials["user_id"])
		domain := strings.TrimSpace(draft.Credentials["domain"])
		if domain == "" {
			domain = prof.domain
		}
		req.Header.Set("X-Domain", domain)
		setPluginHeaders(req.Header)
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("X-Agent-Purpose", "conversation")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("%s: GET %s: %v", prof.id, url, err)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		out := "C:/tmp/wb-config-" + prof.id + ".json"
		_ = os.WriteFile(out, body, 0o600)
		t.Logf("%s: GET %s -> %d %d bytes -> %s", prof.id, url, resp.StatusCode, len(body), out)
	}
}
