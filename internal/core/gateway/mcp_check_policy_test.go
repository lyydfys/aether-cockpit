package gateway

import (
	"testing"
	"time"
)

func TestCacheableMCPResult(t *testing.T) {
	if cacheableMCPResult(mcpCheckResult{Why: "novar", Detail: "TOKEN"}) {
		t.Fatal("novar 是本地缺变量，不应缓存")
	}
	for _, res := range []mcpCheckResult{
		{State: "ok", Tools: 1},
		{Why: "timeout"},
		{Why: "notfound"},
		{Why: "exited"},
	} {
		if !cacheableMCPResult(res) {
			t.Fatalf("应缓存: %+v", res)
		}
	}
}

func TestMCPStdioTimeoutAllowsColdStart(t *testing.T) {
	if mcpStdioTimeout < 15*time.Second || mcpStdioTimeout > time.Minute {
		t.Fatalf("stdio 超时应容忍 npx/uvx 冷启动: %s", mcpStdioTimeout)
	}
}
