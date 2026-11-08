package gateway

import (
	"net/http"
	"strings"
	"testing"
)

// 中文渠道名应直接进 slug，而不是回退成 relay-<hash>：
// 用户看到 `custom-我的中转站/deepseek-v4.1-flash` 才知道是哪个渠道。
func TestChineseNameBecomesSlug(t *testing.T) {
	cases := []struct{ name, want string }{
		{"我的中转站", "我的中转站"},
		{"公司 中转", "公司-中转"},
		{"My Relay 我的", "my-relay-我的"},
		{"中转站（测试）", "中转站-测试"},
	}
	for _, c := range cases {
		if got := slugifyCustomID(c.name); got != c.want {
			t.Errorf("slugifyCustomID(%q) = %q，want %q", c.name, got, c.want)
		}
	}
	// 纯符号仍应得到空串（调用方回退到 relay-<hash>）。
	for _, name := range []string{"🎉🎉", "...", "()"} {
		if got := slugifyCustomID(name); got != "" {
			t.Errorf("slugifyCustomID(%q) = %q，want 空", name, got)
		}
	}
}

// 端到端：中文名建出的渠道，其 id 直接是中文，模型 id 也随之可读。
func TestChineseNameRelayCreatesReadableID(t *testing.T) {
	g := newSupplierGateway(t)
	body := `{"name":"我的中转站","key":"sk-relay-1","chat":"https://relay.example.com/v1/chat/completions","models":["deepseek-v4.1-flash"]}`
	rec := saveSupplier(g, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	list := supplierChannels(g)
	if len(list) != 1 {
		t.Fatalf("应建出 1 条自定义渠道，实际 %d 条", len(list))
	}
	ch := list[0]
	if ch.ID != "我的中转站" {
		t.Fatalf("渠道 id 应直接是中文名，实际 %q", ch.ID)
	}
	pid := customChannelProviderID(ch.ID)
	if !strings.HasPrefix(pid, "custom-") {
		t.Fatalf("provider id 应以 custom- 开头，实际 %q", pid)
	}

	// 账号挂在自定义渠道上，不能落到内置 openai-compat。
	found := false
	for _, env := range g.pool.All() {
		if env.ProviderID == "openai-compat" {
			t.Fatalf("账号落到了内置 openai-compat（这就是前缀变奇怪的根因）")
		}
		if env.ProviderID == pid {
			found = true
		}
	}
	if !found {
		t.Fatalf("账号没挂到 %s 上", pid)
	}
}