package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/tools"
)

// SearchKeyQuota 与 aether 的 search-quota 契约一致：一个搜索 Key 的用量。
type SearchKeyQuota struct {
	Key       string `json:"key"`
	Known     bool   `json:"known"`
	Unlimited bool   `json:"unlimited,omitempty"`
	Used      int64  `json:"used"`
	Limit     int64  `json:"limit"`
	Of        string `json:"of,omitempty"`
	Plan      string `json:"plan,omitempty"`
	PaygoLeft int64  `json:"paygoLeft,omitempty"`
	UsedUp    bool   `json:"usedUp,omitempty"`
	Err       string `json:"err,omitempty"`
}

// searchQuotaKeep 是 Tavily /usage 的缓存时长（官方限制 10 次/10 分钟）。
const searchQuotaKeep = 2 * time.Minute

type keptSearchQuota struct {
	at time.Time
	q  SearchKeyQuota
}

var searchQuotaCache sync.Map // key 掩码前的 id -> keptSearchQuota

// handleSearchQuota POST /api/settings/search-quota
//
// 目前只有 Tavily 提供 Key 用量接口（GET /usage）；其余供应商返回空列表，
// 与 aether 的 CanReadSearchQuota 一致，不编造数字。
func (g *Gateway) handleSearchQuota(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Vendor string `json:"vendor"`
		Fresh  bool   `json:"fresh"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in)
	vendor := strings.ToLower(strings.TrimSpace(in.Vendor))

	g.settingsMu.Lock()
	var cfg *tools.SearchConfig
	if g.settings.Tools != nil {
		cfg = g.settings.Tools.WebSearch
	}
	g.settingsMu.Unlock()

	if cfg == nil || len(tools.SearchKeys(*cfg)) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "尚未配置网络搜索的 API Key"})
		return
	}
	cfgVendor := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if cfgVendor == "" {
		cfgVendor = "tavily"
	}
	if vendor != "" && vendor != cfgVendor {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "没有配置搜索服务 " + vendor})
		return
	}
	if cfgVendor != "tavily" {
		// Serper/SearXNG 没有公开的 Key 用量接口。
		writeJSON(w, http.StatusOK, map[string]any{"keys": []SearchKeyQuota{}})
		return
	}

	usageURL, err := tavilyUsageURL(cfg.Endpoint)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	keys := []SearchKeyQuota{}
	for _, k := range tools.SearchKeys(*cfg) {
		keys = append(keys, g.readTavilyQuota(r.Context(), usageURL, k, in.Fresh))
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

// tavilyUsageURL 从搜索端点推导 /usage：默认 /search → /usage。
func tavilyUsageURL(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "https://api.tavily.com/usage", nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("搜索端点不是有效的 http(s) 地址")
	}
	basePath := strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/search")
	return u.Scheme + "://" + u.Host + basePath + "/usage", nil
}

func (g *Gateway) readTavilyQuota(ctx context.Context, usageURL, key string, fresh bool) SearchKeyQuota {
	id := usageURL + "\x00" + key
	if !fresh {
		if v, ok := searchQuotaCache.Load(id); ok {
			if k := v.(keptSearchQuota); time.Since(k.at) < searchQuotaKeep {
				return k.q
			}
		}
	}
	q, err := askTavilyUsage(ctx, usageURL, key)
	if err != nil {
		msg := strings.ReplaceAll(err.Error(), key, maskKey(key))
		q = SearchKeyQuota{Err: msg}
	}
	q.Key = maskKey(key)
	searchQuotaCache.Store(id, keptSearchQuota{at: time.Now(), q: q})
	return q
}

// askTavilyUsage 读取 Tavily 的 GET /usage。
func askTavilyUsage(ctx context.Context, usageURL, key string) (SearchKeyQuota, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return SearchKeyQuota{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AetherCockpit/search-quota")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return SearchKeyQuota{}, errors.New("在 15 秒内没有答复")
		}
		return SearchKeyQuota{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 160 {
			msg = msg[:160]
		}
		return SearchKeyQuota{}, fmt.Errorf("HTTP %d：%s", resp.StatusCode, msg)
	}
	return readTavilyUsage(raw)
}

// readTavilyUsage 解析 Tavily /usage 的关键字段，口径与 aether 一致：
// 先看 Key 自己的 limit；否则看账户 plan_limit（含 pay-as-you-go）；
// 两者都为 null 视为不限量。认不出的回复报错，绝不把未知当 0。
func readTavilyUsage(raw []byte) (SearchKeyQuota, error) {
	var u struct {
		Key     map[string]json.RawMessage `json:"key"`
		Account map[string]json.RawMessage `json:"account"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return SearchKeyQuota{}, err
	}
	if u.Key == nil {
		return SearchKeyQuota{}, errors.New("回复里没有 key")
	}
	num := func(o map[string]json.RawMessage, field string) (n int64, isNull, ok bool) {
		v, there := o[field]
		if !there {
			return 0, false, false
		}
		if string(v) == "null" {
			return 0, true, true
		}
		var x float64
		if json.Unmarshal(v, &x) != nil {
			return 0, false, false
		}
		return int64(x), false, true
	}
	var q SearchKeyQuota
	_ = json.Unmarshal(u.Account["current_plan"], &q.Plan)
	if pl, null, ok := num(u.Account, "paygo_limit"); ok && !null {
		if pu, null, ok := num(u.Account, "paygo_usage"); ok && !null {
			q.PaygoLeft = max(0, pl-pu)
		}
	}
	keyUsed, keyUsedNull, keyUsedOK := num(u.Key, "usage")
	keyLimit, keyLimitNull, ok := num(u.Key, "limit")
	if !ok {
		return SearchKeyQuota{}, errors.New("回复里没有 key.limit")
	}
	switch planLimit, planLimitNull, planOK := num(u.Account, "plan_limit"); {
	case !keyLimitNull:
		if !keyUsedOK || keyUsedNull {
			return SearchKeyQuota{}, errors.New("回复里没有 key.usage")
		}
		q.Used, q.Limit, q.Of = keyUsed, keyLimit, "key"
	case planOK && !planLimitNull:
		planUsed, null, ok := num(u.Account, "plan_usage")
		if !ok || null {
			return SearchKeyQuota{}, errors.New("回复里没有 account.plan_usage")
		}
		q.Used, q.Limit, q.Of = planUsed, planLimit, "plan"
	case planOK && planLimitNull:
		q.Unlimited, q.Used = true, keyUsed
	default:
		return SearchKeyQuota{}, errors.New("回复里没有 plan_limit")
	}
	q.Known = true
	q.UsedUp = !q.Unlimited && q.Used >= q.Limit && (q.Of == "key" || q.PaygoLeft == 0)
	return q, nil
}
