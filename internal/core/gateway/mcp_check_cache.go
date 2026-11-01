package gateway

import (
	"sync"
	"time"
)

const mcpCheckTTL = 5 * time.Minute

type mcpCheckCacheEntry struct {
	res mcpCheckResult
	at  time.Time
}

// mcpCheckCache 按服务器配置缓存健康检查结果，避免每次渲染都重启进程/发请求。
type mcpCheckCache struct {
	mu  sync.Mutex
	m   map[string]mcpCheckCacheEntry
	now func() time.Time
}

func (c *mcpCheckCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *mcpCheckCache) get(key string) (mcpCheckResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		return mcpCheckResult{}, false
	}
	e, ok := c.m[key]
	if !ok {
		return mcpCheckResult{}, false
	}
	if c.clock().Sub(e.at) > mcpCheckTTL {
		delete(c.m, key)
		return mcpCheckResult{}, false
	}
	return e.res, true
}

func (c *mcpCheckCache) put(key string, res mcpCheckResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]mcpCheckCacheEntry{}
	}
	c.m[key] = mcpCheckCacheEntry{res: res, at: c.clock()}
}
