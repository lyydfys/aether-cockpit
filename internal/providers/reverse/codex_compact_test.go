package reverse

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestCodexCompactSanitizesAndProxies(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer codex-token" || r.Header.Get("chatgpt-account-id") != "acct" {
			t.Errorf("headers = %v", r.Header)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_compact","object":"response.compaction","usage":{"total_tokens":3}}`))
	}))
	defer srv.Close()

	p := New(Profile{ID: "codex"})
	env := provider.NewAccountEnv("codex-test", "codex")
	env.Credentials = map[string]string{
		"access_token": "codex-token", "account_id": "acct", "base_url": srv.URL,
	}
	env.HTTPClient = &http.Client{}
	req := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":"history"},{"type":"compaction_trigger"}],"tools":[{"type":"function"}]}`)
	out, err := p.CompactResponses(context.Background(), env, req)
	if err != nil {
		t.Fatalf("CompactResponses: %v", err)
	}
	if gotPath != "/responses/compact" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["instructions"] != "" {
		t.Fatalf("instructions = %v", gotBody["instructions"])
	}
	if _, ok := gotBody["tools"]; ok {
		t.Fatal("tools 未移除")
	}
	if !strings.Contains(string(out), "resp_compact") {
		t.Fatalf("out = %s", out)
	}
	if _, ok := interface{}(p).(provider.ResponsesCompactCapability); !ok {
		t.Fatal("未实现 ResponsesCompactCapability")
	}
}
