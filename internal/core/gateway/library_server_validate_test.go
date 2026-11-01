package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 没有地址/命令的 MCP 条目存下去就永远检查不出东西（/mcp/check 直接跳过），
// 界面表现为"保存成功但什么都没发生"。必须当场 400。
func TestServerSaveRequiresAddressOrCommand(t *testing.T) {
	_, _, h := agentApplyTestGateway(t)
	cases := []struct{ body, want string }{
		{`{"server":{"name":"no-url","transport":"http"}}`, "url"},
		{`{"server":{"name":"no-cmd","transport":"stdio"}}`, "command"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/library/servers/save", strings.NewReader(c.body)))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.want) {
			t.Fatalf("%s → %d %s", c.body, rec.Code, rec.Body.String())
		}
	}

	// 正常形状不受影响。
	applyReq(t, h, "/api/library/servers/save", `{"server":{"name":"ok","transport":"http","url":"http://127.0.0.1:9/mcp"}}`)
}
