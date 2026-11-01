package tools

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 多 Key 搜索：SearchConfig.APIKeys 可放多个 Key。
//   rotate（默认）：逐次轮流使用；
//   order：总是先用列表里靠前的；
// 被上游以 401/403/429 拒绝过的 Key 在 60 秒内排到最后（仍会在没有别的
// Key 可用时被尝试），与 aether 的行为一致。

const searchKeyCooldown = 60 * time.Second

type upstreamStatusError struct {
	Code int
	Body string
}

func (e *upstreamStatusError) Error() string {
	return fmt.Sprintf("搜索上游返回 %d: %s", e.Code, truncate(e.Body, 200))
}

func statusError(code int, body []byte) error {
	return &upstreamStatusError{Code: code, Body: string(body)}
}

func isKeyRefusal(err error) bool {
	var se *upstreamStatusError
	if !errors.As(err, &se) {
		return false
	}
	return se.Code == http.StatusUnauthorized || se.Code == http.StatusForbidden || se.Code == http.StatusTooManyRequests
}

// SearchKeys 返回实际可用的 Key 列表（APIKeys 在前，兼容旧的 APIKey）。
func SearchKeys(cfg SearchConfig) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, k := range append(append([]string{}, cfg.APIKeys...), cfg.APIKey) {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

var searchKeySeq sync.Map         // provider|endpoint -> *uint64
var searchKeyCooledUntil sync.Map // provider|key -> int64 (unix)

func searchSeq(key string) *uint64 {
	v, _ := searchKeySeq.LoadOrStore(key, new(uint64))
	return v.(*uint64)
}

func coolerID(cfg SearchConfig, key string) string {
	return cfg.Provider + "|" + cfg.Endpoint + "|" + key
}

func keyCooling(cfg SearchConfig, key string) bool {
	v, ok := searchKeyCooledUntil.Load(coolerID(cfg, key))
	if !ok {
		return false
	}
	return time.Now().Unix() < v.(int64)
}

func coolKey(cfg SearchConfig, key string) {
	if key == "" {
		return
	}
	searchKeyCooledUntil.Store(coolerID(cfg, key), time.Now().Add(searchKeyCooldown).Unix())
}

// orderedSearchKeys 决定尝试顺序：未冷却的在前，rotate 时从上次的下一个开始。
func orderedSearchKeys(cfg SearchConfig, keys []string) []string {
	if len(keys) <= 1 {
		return keys
	}
	start := 0
	if strings.EqualFold(strings.TrimSpace(cfg.KeyOrder), "rotate") || cfg.KeyOrder == "" {
		n := atomic.AddUint64(searchSeq(cfg.Provider+"|"+cfg.Endpoint), 1) - 1
		start = int(n % uint64(len(keys)))
	}
	fresh, cooled := []string{}, []string{}
	for i := 0; i < len(keys); i++ {
		k := keys[(start+i)%len(keys)]
		if keyCooling(cfg, k) {
			cooled = append(cooled, k)
		} else {
			fresh = append(fresh, k)
		}
	}
	return append(fresh, cooled...)
}
