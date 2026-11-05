package reverse

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// OpenCode Go 的 Key 只认 x-api-key；发 Bearer 会 401 Invalid credential。
// 头名错了比凭证错了更隐蔽，所以钉住。
func TestOpencodeGoUsesAPIKeyHeader(t *testing.T) {
	prof := findProfile(t, "opencode-go")
	if prof.ModelListAuthHeader != "x-api-key" {
		t.Fatalf("opencode-go AuthHeader = %q，want x-api-key", prof.ModelListAuthHeader)
	}

	env := provider.NewAccountEnv("acc", "opencode-go")
	env.Credentials = map[string]string{"api_key": "oc_sk_test"}
	req := httptest.NewRequest(http.MethodPost, "https://example.com/", nil)

	auth := modelListAuth(nil, &prof)
	req.Header.Del("Authorization")
	if err := applyAuth(req, env, auth); err != nil {
		t.Fatalf("applyAuth: %v", err)
	}
	if got := req.Header.Get("x-api-key"); got != "oc_sk_test" {
		t.Fatalf("x-api-key = %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("不该同时发 Authorization，得到 %q", got)
	}
}

// 没有声明 AuthHeader 的渠道仍然走默认 Authorization，不能被这条规则带偏。
func TestModelListAuthKeepsDefaultHeader(t *testing.T) {
	prof := findProfile(t, "codex")
	if modelListAuth(nil, &prof).Header != "Authorization" {
		t.Fatalf("codex 应保持 Authorization，得到 %q", modelListAuth(nil, &prof).Header)
	}
}

// spec 显式配置的鉴权头优先于渠道默认值。
func TestModelListAuthPrefersSpec(t *testing.T) {
	prof := Profile{ID: "x", ModelListAuthHeader: "x-api-key"}
	sp := &spec.Spec{Extra: []byte(`{"chat":{"auth":{"header":"X-Custom"}}}`)}
	if got := modelListAuth(sp, &prof).Header; got != "X-Custom" {
		t.Fatalf("spec 显式头应优先，得到 %q", got)
	}
}
