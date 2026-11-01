package gateway

import (
	"encoding/json"
	"testing"
)

// SSE 服务器曾被保存成 type:"http"（server/save 对非 stdio 无条件写 http），
// 于是检查与下发都走了错协议，界面只显示"协议错误"。这条锁住类型不被吞掉。
func TestServerSaveKeepsSSETransport(t *testing.T) {
	g, _, h := agentApplyTestGateway(t)

	save := func(body, name string) map[string]any {
		applyReq(t, h, "/api/library/servers/save", body)
		for _, m := range g.library.List().MCPServers {
			if m.Name != name {
				continue
			}
			var cfg map[string]any
			if err := json.Unmarshal(m.Config, &cfg); err != nil {
				t.Fatalf("%s 的 config 不是 JSON: %v", name, err)
			}
			return cfg
		}
		t.Fatalf("库里找不到 %s", name)
		return nil
	}

	if cfg := save(`{"server":{"name":"sse-one","transport":"sse","url":"http://127.0.0.1:9/sse"}}`, "sse-one"); cfg["type"] != "sse" {
		t.Fatalf("SSE 条目 type = %v，want sse", cfg["type"])
	}
	if cfg := save(`{"server":{"name":"http-one","transport":"http","url":"http://127.0.0.1:9/mcp"}}`, "http-one"); cfg["type"] != "http" {
		t.Fatalf("HTTP 条目 type = %v，want http", cfg["type"])
	}
	if cfg := save(`{"server":{"name":"stdio-one","transport":"stdio","command":"uvx"}}`, "stdio-one"); cfg["type"] != nil {
		t.Fatalf("stdio 条目不应带 type：%v", cfg["type"])
	}
}
