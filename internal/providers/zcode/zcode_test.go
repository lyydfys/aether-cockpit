package zcode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func enc(t *testing.T, plain string) string {
	t.Helper()
	t.Setenv("ZCODE_CREDENTIAL_SECRET", "test-secret")
	v, err := encryptCredential(plain)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func writeCreds(t *testing.T, store map[string]string) string {
	t.Setenv("ZCODE_CREDENTIAL_SECRET", "test-secret")
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".zcode", "v2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(store)
	path := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscoverLocalAccountsByRegion(t *testing.T) {
	userInfo, _ := json.Marshal(map[string]string{"email": "dev@example.com"})
	writeCreds(t, map[string]string{
		"account-provider:coding-plan:account:bigmodel-team-coding-plan:account:58771773930227764:api-key": enc(t, "cn-key"),
		"account-provider:coding-plan:account:zai-coding-plan:account:uuid-1:api-key":                       enc(t, "intl-key"),
		"oauth:zai:user_info":  string(userInfo),
		"oauth:active_provider": "zai",
	})

	cn, err := newProvider("zcode-cn", "cn", "https://open.bigmodel.cn/api/coding/paas/v4", "BigModel").
		DiscoverLocalAccounts(context.Background())
	if err != nil || len(cn) != 1 {
		t.Fatalf("CN 草稿 = %+v err=%v", cn, err)
	}
	if cn[0].Credentials["api_key"] != "cn-key" {
		t.Fatal("CN 未取到 bigmodel key")
	}
	if cn[0].Credentials["base_url"] != "https://open.bigmodel.cn/api/coding/paas/v4" {
		t.Fatalf("CN base_url = %q", cn[0].Credentials["base_url"])
	}
	if !strings.Contains(cn[0].Label, "dev@example.com") {
		t.Fatalf("label = %q", cn[0].Label)
	}

	intl, err := newProvider("zcode", "intl", "https://api.z.ai/api/coding/paas/v4", "Z.ai").
		DiscoverLocalAccounts(context.Background())
	if err != nil || len(intl) != 1 || intl[0].Credentials["api_key"] != "intl-key" {
		t.Fatalf("Intl 草稿 = %+v err=%v", intl, err)
	}
}

func TestDiscoverLocalAccountsMissingRegion(t *testing.T) {
	writeCreds(t, map[string]string{
		"account-provider:coding-plan:account:bigmodel-team-coding-plan:account:1:api-key": enc(t, "cn-key"),
	})
	if _, err := newProvider("zcode", "intl", "https://api.z.ai/api/coding/paas/v4", "Z.ai").
		DiscoverLocalAccounts(context.Background()); err == nil {
		t.Fatal("没有对应区域应返回可执行错误")
	}
}

func TestInjectLocalAccountOnlyTouchesRegionKey(t *testing.T) {
	path := writeCreds(t, map[string]string{
		"account-provider:coding-plan:account:bigmodel-team-coding-plan:account:1:api-key": enc(t, "old-cn"),
		"account-provider:coding-plan:account:zai-coding-plan:account:2:api-key":           enc(t, "keep-intl"),
		"oauth:zai:access_token": "keep-oauth",
	})
	p := newProvider("zcode-cn", "cn", "https://open.bigmodel.cn/api/coding/paas/v4", "BigModel")
	res, err := p.InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:   "acc-1",
		ProviderID:  "zcode-cn",
		Credentials: map[string]string{"api_key": "new-cn"},
	})
	if err != nil {
		t.Fatalf("InjectLocalAccount: %v", err)
	}
	if res.Target != "file:"+path || len(res.Files) != 1 {
		t.Fatalf("res = %+v", res)
	}
	var got map[string]string
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	updated, err := decryptCredential(got["account-provider:coding-plan:account:bigmodel-team-coding-plan:account:1:api-key"])
	if err != nil || updated != "new-cn" {
		t.Fatalf("目标 Key 未更新: %q %v", updated, err)
	}
	kept, err := decryptCredential(got["account-provider:coding-plan:account:zai-coding-plan:account:2:api-key"])
	if err != nil || kept != "keep-intl" {
		t.Fatal("其他区域被改动")
	}
	if got["oauth:zai:access_token"] != "keep-oauth" {
		t.Fatal("OAuth 条目被改动")
	}
	backup, _ := os.ReadFile(res.Files[0])
	var old map[string]string
	if err := json.Unmarshal(backup, &old); err != nil {
		t.Fatal("备份不是合法 JSON")
	}
	oldPlain, _ := decryptCredential(old["account-provider:coding-plan:account:bigmodel-team-coding-plan:account:1:api-key"])
	if oldPlain != "old-cn" {
		t.Fatal("备份不是写入前状态")
	}
	if !p.Manifest().Capabilities.LocalInject {
		t.Fatal("Manifest 应声明 local_inject")
	}
}

func TestChatStreamUsesCodingPlanEndpoint(t *testing.T) {
	var gotPath, gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotModel, _ = payload["model"].(string)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newProvider("zcode-cn", "cn", srv.URL, "BigModel")
	env := provider.NewAccountEnv("zcode-test", "zcode-cn")
	env.Credentials = map[string]string{"api_key": "sk-test", "base_url": srv.URL}
	env.HTTPClient = &http.Client{}
	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model:         "zcode-cn/glm-5.3",
		UpstreamModel: "glm-5.3",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text strings.Builder
	for ev := range ch {
		if ev.Type == ir.EventTextDelta {
			text.WriteString(ev.TextDelta)
		}
	}
	if text.String() != "OK" {
		t.Fatalf("text = %q", text.String())
	}
	if gotPath != "/chat/completions" || gotAuth != "Bearer sk-test" || gotModel != "glm-5.3" {
		t.Fatalf("path=%q auth=%q model=%q", gotPath, gotAuth, gotModel)
	}
}
