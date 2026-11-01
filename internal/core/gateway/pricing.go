package gateway

// 模型定价：官方目录 + 用户覆盖。
//
// 内部统一用美元 / 百万 token 计价；人民币展示按 settings.USDCNY 换算。
// 官方目录按 2026-10 的公开价整理，DeepSeek 使用官方峰谷价；模型名是
// 前缀匹配，用户可以按模型或通配前缀覆盖。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type PriceRates struct {
	InputPerM       float64 `json:"input_per_m"`
	OutputPerM      float64 `json:"output_per_m"`
	CachedReadPerM  float64 `json:"cached_read_per_m,omitempty"`
	CachedWritePerM float64 `json:"cached_write_per_m,omitempty"`
}

type PriceTier struct {
	MinInputTokens int `json:"min_input_tokens"`
	PriceRates
}

type PriceWindow struct {
	Weekdays []int  `json:"weekdays,omitempty"` // Go: 0=Sunday
	Start    string `json:"start"`
	End      string `json:"end"`
}

const deepseekCNYPerUSD = 7.2

func usdFromCNY(v float64) float64 { return v / deepseekCNYPerUSD }

func mkPrice(in, out, cached, write float64) ModelPrice {
	return ModelPrice{InputPerM: in, OutputPerM: out, CachedReadPerM: cached, CachedWritePerM: write}
}

func mkTier(min int, in, out, cached, write float64) PriceTier {
	return PriceTier{MinInputTokens: min, PriceRates: PriceRates{
		InputPerM: in, OutputPerM: out, CachedReadPerM: cached, CachedWritePerM: write,
	}}
}

func withTiers(p ModelPrice, tiers ...PriceTier) ModelPrice {
	p.Tiers = append(p.Tiers, tiers...)
	return p
}

func deepseekWindows() ([]PriceWindow, string) {
	return []PriceWindow{
		{Weekdays: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "12:00"},
		{Weekdays: []int{1, 2, 3, 4, 5}, Start: "14:00", End: "18:00"},
	}, "Asia/Shanghai"
}

func deepseekPrice(offIn, offOut, offCache, peakIn, peakOut, peakCache float64) ModelPrice {
	windows, tz := deepseekWindows()
	p := mkPrice(usdFromCNY(offIn), usdFromCNY(offOut), usdFromCNY(offCache), 0)
	p.Peak = &PriceRates{
		InputPerM: usdFromCNY(peakIn), OutputPerM: usdFromCNY(peakOut), CachedReadPerM: usdFromCNY(peakCache),
	}
	p.PeakWindows = windows
	p.Timezone = tz
	p.Note = "平时价 / 高峰价：高峰为北京时间周一至周五 09:00–12:00、14:00–18:00（不含法定节假日）"
	return p
}

var defaultPriceCatalog = map[string]ModelPrice{
	// DeepSeek V4：官方人民币价换算为美元；Flash 与 Pro 两档，峰谷时段一致。
	"deepseek-v4.1-flash*":    deepseekPrice(1, 4, 0.02, 2, 8, 0.04),
	"deepseek-v4.1-flash-sg*": deepseekPrice(1, 4, 0.02, 2, 8, 0.04),
	"deepseek-v4-flash*":      deepseekPrice(1, 4, 0.02, 2, 8, 0.04),
	"deepseek-v4-pro*":        deepseekPrice(4.5, 13.5, 0.15, 9, 27, 0.30),
	"deepseek-reasoner*":      deepseekPrice(4.5, 13.5, 0.15, 9, 27, 0.30),
	"deepseek-*":              deepseekPrice(1, 4, 0.02, 2, 8, 0.04),

	// OpenAI 2026 官方价；200K 以上长上下文档价格翻倍。
	"gpt-6-astra*":   withTiers(mkPrice(10, 50, 1, 0), mkTier(200000, 20, 100, 2, 0)),
	"gpt-6.1-sol*":   withTiers(mkPrice(5, 30, 0.50, 0), mkTier(200000, 10, 60, 1, 0)),
	"gpt-6-sol*":     withTiers(mkPrice(5, 30, 0.50, 0), mkTier(200000, 10, 60, 1, 0)),
	"gpt-6.1-terra*": withTiers(mkPrice(2, 12, 0.20, 0), mkTier(200000, 4, 24, 0.40, 0)),
	"gpt-6-terra*":   withTiers(mkPrice(2, 12, 0.20, 0), mkTier(200000, 4, 24, 0.40, 0)),
	"gpt-5.6-terra*": withTiers(mkPrice(2, 12, 0.20, 0), mkTier(200000, 4, 24, 0.40, 0)),
	"gpt-6.1-luna*":  withTiers(mkPrice(0.20, 1.20, 0.02, 0), mkTier(200000, 0.40, 2.40, 0.04, 0)),
	"gpt-6-luna*":    withTiers(mkPrice(0.20, 1.20, 0.02, 0), mkTier(200000, 0.40, 2.40, 0.04, 0)),
	"gpt-5.6-luna*":  withTiers(mkPrice(0.20, 1.20, 0.02, 0), mkTier(200000, 0.40, 2.40, 0.04, 0)),
	"gpt-5.6-sol*":   withTiers(mkPrice(4, 20, 0.40, 0), mkTier(200000, 8, 40, 0.80, 0)),
	"gpt-5.5*":       withTiers(mkPrice(5, 30, 0.50, 0), mkTier(200000, 10, 60, 1, 0)),
	"gpt-5.4*":       withTiers(mkPrice(2.50, 15, 0.25, 0), mkTier(200000, 5, 30, 0.50, 0)),
	"gpt-5.3-codex*": withTiers(mkPrice(3.50, 28, 0.35, 0), mkTier(200000, 7, 56, 0.70, 0)),
	"gpt-5-mini*":    mkPrice(0.25, 2, 0.025, 0),
	"gpt-5-nano*":    mkPrice(0.05, 0.40, 0.005, 0),
	"gpt-5*":         withTiers(mkPrice(1.25, 10, 0.125, 0), mkTier(200000, 2.50, 20, 0.25, 0)),
	"gpt-4o-mini*":   mkPrice(0.15, 0.60, 0.075, 0),
	"gpt-4o*":        mkPrice(2.50, 10, 1.25, 0),
	"gpt-4.1-mini*":  mkPrice(0.40, 1.60, 0.10, 0),
	"gpt-4.1-nano*":  mkPrice(0.10, 0.40, 0.025, 0),
	"gpt-4.1*":       mkPrice(2, 8, 0.50, 0),
	"o3-mini*":       mkPrice(1.10, 4.40, 0.275, 0),
	"o3*":            mkPrice(2, 8, 0.50, 0),
	"o4-mini*":       mkPrice(1.10, 4.40, 0.275, 0),
	"o1*":            mkPrice(15, 60, 7.50, 0),
	"gpt-oss*":       mkPrice(0.10, 0.30, 0.02, 0),
	"gpt-*":          mkPrice(1.25, 10, 0.125, 0),

	// Anthropic Claude 2026 价目（cache write 按 input 的 1.25x）。
	"claude-opus-5.5*":   mkPrice(4, 20, 0.40, 5),
	"claude-opus-5*":     mkPrice(5, 25, 0.50, 6.25),
	"claude-opus-4.6*":   mkPrice(5, 25, 0.50, 6.25),
	"claude-opus-4.5*":   mkPrice(5, 25, 0.50, 6.25),
	"claude-opus-4*":     mkPrice(15, 75, 1.50, 18.75),
	"claude-sonnet-5.5*": mkPrice(2, 10, 0.20, 2.50),
	"claude-sonnet-5*":   mkPrice(2, 10, 0.20, 2.50),
	"claude-sonnet-4.6*": mkPrice(3, 15, 0.30, 3.75),
	"claude-sonnet-4.5*": mkPrice(3, 15, 0.30, 3.75),
	"claude-sonnet-4*":   mkPrice(3, 15, 0.30, 3.75),
	"claude-haiku-5.5*":  mkPrice(0.10, 0.50, 0.01, 0.125),
	"claude-haiku-4.5*":  mkPrice(1, 5, 0.10, 1.25),
	"claude-haiku-4*":    mkPrice(0.80, 4, 0.08, 1),
	"claude-3-5-sonnet*": mkPrice(3, 15, 0.30, 3.75),
	"claude-3-5-haiku*":  mkPrice(0.80, 4, 0.08, 1),
	"claude-3-opus*":     mkPrice(15, 75, 1.50, 18.75),
	"claude-*":           mkPrice(3, 15, 0.30, 3.75),

	// Google Gemini；Pro 200K 以上按长上下文档。
	"gemini-3.1-pro*":        withTiers(mkPrice(2, 12, 0.50, 0), mkTier(200000, 4, 18, 1, 0)),
	"gemini-2.5-pro*":        withTiers(mkPrice(1.25, 10, 0.31, 0), mkTier(200000, 2.50, 15, 0.625, 0)),
	"gemini-4-argon*":        mkPrice(2, 10, 0.50, 0),
	"gemini-3.8-flash*":      mkPrice(0.75, 3.75, 0.1875, 0),
	"gemini-3.5-flash-lite*": mkPrice(0.10, 0.40, 0.025, 0),
	"gemini-3.1-flash-lite*": mkPrice(0.10, 0.40, 0.025, 0),
	"gemini-3.5-flash*":      mkPrice(0.75, 3.75, 0.1875, 0),
	"gemini-3.1-flash*":      mkPrice(0.75, 3.75, 0.1875, 0),
	"gemini-3-flash*":        mkPrice(0.75, 3.75, 0.1875, 0),
	"gemini-2.5-flash-lite*": mkPrice(0.10, 0.40, 0.025, 0),
	"gemini-2.5-flash*":      mkPrice(0.30, 2.50, 0.075, 0),
	"gemini-2.0-flash*":      mkPrice(0.10, 0.40, 0.025, 0),
	"gemini-*":               mkPrice(0.75, 3.75, 0.1875, 0),

	// 国内与开源模型：按公开官方价或同档参考价。
	"kimi-k3*":            mkPrice(0.60, 2.50, 0.15, 0),
	"kimi-k2*":            mkPrice(0.60, 2.50, 0.15, 0),
	"kimi-*":              mkPrice(0.60, 2.50, 0.15, 0),
	"glm-5*":              mkPrice(0.60, 2.20, 0.11, 0),
	"glm-4*":              mkPrice(0.60, 2.20, 0.11, 0),
	"glm-*":               mkPrice(0.60, 2.20, 0.11, 0),
	"minimax-m3.1-flash*": mkPrice(0.10, 0.40, 0.02, 0),
	"minimax-m3*":         mkPrice(0.30, 1.20, 0.06, 0),
	"minimax-m2*":         mkPrice(0.30, 1.20, 0.06, 0),
	"minimax-*":           mkPrice(0.30, 1.20, 0.06, 0),
	"qwen3.8-max*":        mkPrice(1.20, 6, 0.30, 0),
	"qwen3.7-plus*":       mkPrice(0.40, 1.20, 0.10, 0),
	"qwen3.5-plus*":       mkPrice(0.40, 1.20, 0.10, 0),
	"qwen3-coder*":        mkPrice(0.30, 1.20, 0.075, 0),
	"qwen-*":              mkPrice(0.40, 1.20, 0.10, 0),
	"qwen*":               mkPrice(0.40, 1.20, 0.10, 0),
	"grok-4*":             mkPrice(3, 15, 0.75, 0),
	"grok-3-mini*":        mkPrice(0.30, 0.50, 0.075, 0),
	"grok-*":              mkPrice(3, 15, 0.75, 0),
	"hy4*":                mkPrice(0.20, 1, 0.05, 0),
	"hy3*":                mkPrice(0.20, 1, 0.05, 0),
	"hunyuan*":            mkPrice(0.20, 1, 0.05, 0),
}

func bareModelName(model string) string {
	model = strings.TrimSpace(model)
	if i := strings.LastIndex(model, "/"); i >= 0 {
		return model[i+1:]
	}
	return model
}

func defaultPriceFor(bare string) (ModelPrice, bool) {
	if p, ok := defaultPriceCatalog[bare]; ok {
		return p, true
	}
	bestLen := -1
	var best ModelPrice
	for pattern, p := range defaultPriceCatalog {
		if !strings.HasSuffix(pattern, "*") {
			continue
		}
		prefix := strings.TrimSuffix(pattern, "*")
		if len(prefix) > bestLen && strings.HasPrefix(bare, prefix) {
			bestLen = len(prefix)
			best = p
		}
	}
	if bestLen >= 0 {
		return best, true
	}
	return ModelPrice{}, false
}

// priceForUsage 选择上下文档位与峰谷价，返回可直接用于 costOf 的平价。
// ledgerPrice 给历史账本重算费用：按请求发生时间选峰谷/档位，
// 并乘渠道计费倍率。用量看板因此能随价格表变化重新统计。
func (g *Gateway) ledgerPrice(rec ledgerRecord) (float64, bool) {
	price, ok := g.priceForUsage(rec.Model, rec.In+rec.CacheRead+rec.CacheWrite, time.UnixMilli(rec.T))
	if !ok {
		return 0, false
	}
	cost := costOf(price, rec.In, rec.Out, rec.CacheRead, rec.CacheWrite)
	if rate := g.providerPriceRate(rec.ProviderID); rate != 1 {
		cost *= rate
	}
	return cost, true
}

func (g *Gateway) priceForUsage(model string, inputTokens int64, at time.Time) (ModelPrice, bool) {
	p, ok := g.priceFor(model)
	if !ok {
		// 本地价目表没收录时，用 models.dev 的公开价兜底（实时目录已缓存）。
		if p, ok = g.priceFromCatalog(model); ok {
			return p, true
		}
		return ModelPrice{}, false
	}
	flat := p
	flat.Tiers = nil
	flat.Peak = nil
	flat.PeakWindows = nil
	bestTier := -1
	for _, t := range p.Tiers {
		if inputTokens >= int64(t.MinInputTokens) && t.MinInputTokens > bestTier {
			bestTier = t.MinInputTokens
			flat.InputPerM = t.InputPerM
			flat.OutputPerM = t.OutputPerM
			flat.CachedReadPerM = t.CachedReadPerM
			flat.CachedWritePerM = t.CachedWritePerM
		}
	}
	if p.Peak != nil && inPricePeakWindow(at, p) {
		flat.InputPerM = p.Peak.InputPerM
		flat.OutputPerM = p.Peak.OutputPerM
		flat.CachedReadPerM = p.Peak.CachedReadPerM
		flat.CachedWritePerM = p.Peak.CachedWritePerM
	}
	return flat, true
}

func inPricePeakWindow(at time.Time, p ModelPrice) bool {
	loc := time.Local
	if p.Timezone == "Asia/Shanghai" {
		loc = time.FixedZone("CST", 8*3600)
	}
	t := at.In(loc)
	minutes := t.Hour()*60 + t.Minute()
	for _, w := range p.PeakWindows {
		if len(w.Weekdays) > 0 {
			ok := false
			for _, d := range w.Weekdays {
				if int(t.Weekday()) == d {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		start, ok1 := parseHHMM(w.Start)
		end, ok2 := parseHHMM(w.End)
		if !ok1 || !ok2 {
			continue
		}
		if start <= end {
			if minutes >= start && minutes < end {
				return true
			}
		} else if minutes >= start || minutes < end {
			return true
		}
	}
	return false
}

func parseHHMM(s string) (int, bool) {
	var h, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func matchPriceMap(prices map[string]ModelPrice, model string) (ModelPrice, bool) {
	if len(prices) == 0 {
		return ModelPrice{}, false
	}
	if p, ok := prices[model]; ok {
		return p, true
	}
	bestLen := 0
	var best ModelPrice
	found := false
	for k, p := range prices {
		if k == "*" {
			continue
		}
		if strings.HasSuffix(k, "*") {
			prefix := strings.TrimSuffix(k, "*")
			if strings.HasPrefix(model, prefix) && len(prefix) > bestLen {
				bestLen = len(prefix)
				best = p
				found = true
			}
			continue
		}
		if strings.HasPrefix(model, k+"/") && len(k) > bestLen {
			bestLen = len(k)
			best = p
			found = true
		}
	}
	if !found {
		if p, ok := prices["*"]; ok {
			return p, true
		}
		return ModelPrice{}, false
	}
	return best, true
}

func copyModelPrices(in map[string]ModelPrice) map[string]ModelPrice {
	if len(in) == 0 {
		return map[string]ModelPrice{}
	}
	out := make(map[string]ModelPrice, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// handlePricing GET /api/pricing
func (g *Gateway) handlePricing(w http.ResponseWriter, r *http.Request) {
	g.settingsMu.Lock()
	overrides := copyModelPrices(g.settings.ModelPrices)
	currency := g.settings.PriceCurrency
	if currency == "" {
		currency = "USD"
	}
	rate := g.settings.USDCNY
	if rate <= 0 {
		rate = deepseekCNYPerUSD
	}
	g.settingsMu.Unlock()
	defaults := make([]map[string]any, 0, len(defaultPriceCatalog))
	for pattern, price := range defaultPriceCatalog {
		defaults = append(defaults, map[string]any{"pattern": pattern, "price": price, "source": "official"})
	}
	sort.Slice(defaults, func(i, j int) bool { return defaults[i]["pattern"].(string) < defaults[j]["pattern"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{
		"currency": currency, "usd_cny": rate,
		"defaults": defaults, "overrides": overrides,
	})
}

type pricingSaveInput struct {
	ModelPrices   *map[string]ModelPrice `json:"model_prices"`
	PriceCurrency *string                `json:"price_currency"`
	USDCNY        *float64               `json:"usd_cny"`
}

// handlePricingSave POST /api/pricing/save
func (g *Gateway) handlePricingSave(w http.ResponseWriter, r *http.Request) {
	var in pricingSaveInput
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if in.PriceCurrency != nil {
		c := strings.ToUpper(strings.TrimSpace(*in.PriceCurrency))
		if c != "USD" && c != "CNY" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "币种只能是 USD 或 CNY"})
			return
		}
	}
	if in.USDCNY != nil && (*in.USDCNY <= 0 || *in.USDCNY > 100) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "汇率需在 0 到 100 之间"})
		return
	}
	if in.ModelPrices != nil {
		for k, p := range *in.ModelPrices {
			if strings.TrimSpace(k) == "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "价格表里有空模型名"})
				return
			}
			if p.InputPerM < 0 || p.OutputPerM < 0 || p.CachedReadPerM < 0 || p.CachedWritePerM < 0 {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "价格不能为负数: " + k})
				return
			}
			for _, t := range p.Tiers {
				if t.InputPerM < 0 || t.OutputPerM < 0 {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "档位价格不能为负数: " + k})
					return
				}
			}
			if p.Peak != nil && (p.Peak.InputPerM < 0 || p.Peak.OutputPerM < 0) {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "高峰价不能为负数: " + k})
				return
			}
		}
	}
	g.settingsMu.Lock()
	if in.ModelPrices != nil {
		g.settings.ModelPrices = *in.ModelPrices
	}
	if in.PriceCurrency != nil {
		g.settings.PriceCurrency = strings.ToUpper(strings.TrimSpace(*in.PriceCurrency))
	}
	if in.USDCNY != nil {
		g.settings.USDCNY = *in.USDCNY
	}
	err = g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存定价失败: " + err.Error()})
		return
	}
	g.handlePricing(w, r)
}
