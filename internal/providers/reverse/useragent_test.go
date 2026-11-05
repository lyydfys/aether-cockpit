package reverse

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// UA 会实际影响"官方/自部署混合池"的路由决策，所以三条来源的优先级要钉住。
func TestApplyUserAgentPrecedence(t *testing.T) {
	cases := []struct {
		name   string
		creds  map[string]string
		spec   *spec.Spec
		wantUA string
	}{
		{
			name:   "账号显式指定优先，原样发送",
			creds:  map[string]string{"user_agent": "opencode/1.2.3"},
			spec:   &spec.Spec{ClientVersion: "9.9"},
			wantUA: "opencode/1.2.3",
		},
		{
			name:   "没有显式值时用 spec 的客户端版本",
			creds:  map[string]string{},
			spec:   &spec.Spec{ClientVersion: "0.4.2"},
			wantUA: "aether-cockpit/0.4.2",
		},
		{
			name:   "两者都没有时兜底，绝不能是 Go-http-client",
			creds:  map[string]string{},
			wantUA: "aether-cockpit/" + reverseClientVersion,
		},
		{
			name:   "空白显式值当作没填",
			creds:  map[string]string{"user_agent": "   "},
			spec:   &spec.Spec{ClientVersion: "1.1"},
			wantUA: "aether-cockpit/1.1",
		},
	}
	for _, c := range cases {
		env := provider.NewAccountEnv("acc", "opencode-go")
		env.Credentials = c.creds
		req := httptest.NewRequest(http.MethodPost, "https://example.com/", nil)
		applyUserAgent(req, env, c.spec)
		if got := req.Header.Get("User-Agent"); got != c.wantUA {
			t.Errorf("%s: UA = %q, want %q", c.name, got, c.wantUA)
		}
	}
}

// env 为 nil 时也不能 panic（渠道构造失败等边缘路径）。
func TestApplyUserAgentNilEnv(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://example.com/", nil)
	applyUserAgent(req, nil, nil)
	if got := req.Header.Get("User-Agent"); got == "" {
		t.Fatal("兜底 UA 不能为空")
	}
}
