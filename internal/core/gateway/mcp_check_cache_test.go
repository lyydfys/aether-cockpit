package gateway

import (
	"testing"
	"time"
)

func TestMCPCheckCache(t *testing.T) {
	c := &mcpCheckCache{}
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }

	if _, ok := c.get("k"); ok {
		t.Fatal("空缓存不应命中")
	}
	c.put("k", mcpCheckResult{State: "ok", Tools: 2})
	if res, ok := c.get("k"); !ok || res.Tools != 2 {
		t.Fatalf("get = %+v ok=%v", res, ok)
	}

	now = now.Add(mcpCheckTTL + time.Second)
	if _, ok := c.get("k"); ok {
		t.Fatal("过期后不应命中")
	}
}

func TestMCPCheckCacheZeroValue(t *testing.T) {
	var c mcpCheckCache
	c.put("k", mcpCheckResult{State: "ok"})
	if _, ok := c.get("k"); !ok {
		t.Fatal("零值缓存写入后应命中")
	}
}
