package workbuddy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// SupportsModelList 表示可以从 /v3/config 拉模型目录（需要 token + user_id）。
func (p *Provider) SupportsModelList(env *provider.AccountEnv) bool {
	return strings.TrimSpace(env.GetCredential("token")) != "" &&
		strings.TrimSpace(env.GetCredential("user_id")) != ""
}

func (p *Provider) ListModels(ctx context.Context, env *provider.AccountEnv) ([]string, error) {
	infos, err := p.ListModelInfos(ctx, env)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(infos))
	for _, info := range infos {
		ids = append(ids, info.ID)
	}
	return ids, nil
}

type wbConfigModel struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	MaxInputTokens    int    `json:"maxInputTokens"`
	SupportsImages    bool   `json:"supportsImages"`
	SupportsReasoning bool   `json:"supportsReasoning"`
	SupportsToolCall  bool   `json:"supportsToolCall"`
	Credits           string `json:"credits"`
}

type wbConfigAgent struct {
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

type wbConfigPromo struct {
	ID       string   `json:"id"`
	Enabled  bool     `json:"enabled"`
	Kind     string   `json:"kind"`
	ModelIDs []string `json:"modelIds"`
	Priority int      `json:"priority"`
	Discount struct {
		Factor            float64 `json:"factor"`
		DiscountedCredits string  `json:"discountedCredits"`
		DisplayMode       string  `json:"displayMode"`
	} `json:"discount"`
	Schedule *struct {
		ValidFrom  string `json:"validFrom"`
		ValidUntil string `json:"validUntil"`
		Timezone   string `json:"timezone"`
	} `json:"schedule"`
}

type wbConfig struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Models          []wbConfigModel `json:"models"`
		Agents          []wbConfigAgent `json:"agents"`
		ModelPromotions []wbConfigPromo `json:"modelPromotions"`
	} `json:"data"`
}

var creditRateRe = regexp.MustCompile(`(?i)x\s*([0-9]+(?:\.[0-9]+)?)`)

// parseCredits 解析 "x0.34 credits" / "x0.00"；空字符串表示上游没给。
func parseCredits(s string) (float64, bool) {
	m := creditRateRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func promoActive(p wbConfigPromo, now time.Time) bool {
	if !p.Enabled || p.Schedule == nil {
		return p.Enabled
	}
	if from, err := time.Parse(time.RFC3339, strings.TrimSpace(p.Schedule.ValidFrom)); err == nil && now.Before(from) {
		return false
	}
	if until, err := time.Parse(time.RFC3339, strings.TrimSpace(p.Schedule.ValidUntil)); err == nil && !now.Before(until) {
		return false
	}
	return true
}

// ListModelInfos 读取 /v3/config 的模型目录：Intl 的 cli agent（聊天）模型，
// 并解析 credits 倍率与生效中的折扣。
func (p *Provider) ListModelInfos(ctx context.Context, env *provider.AccountEnv) ([]provider.ModelInfo, error) {
	token := firstNonEmpty(strings.TrimSpace(env.GetCredential("token")), strings.TrimSpace(env.GetCredential("access_token")))
	userID := strings.TrimSpace(env.GetCredential("user_id"))
	if token == "" || userID == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "WorkBuddy 账号缺少 token/user_id").WithProvider(p.profile.id)
	}
	// 站点根与登录链路同源：{站点根}/v3/config（国际版 www.workbuddy.ai，国内版 copilot.tencent.com）。
	base := providerOrigin(p.baseURL(env))
	url := base + "/v3/config"

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 WorkBuddy 配置请求失败").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-Id", userID)
	req.Header.Set("X-Domain", p.domain(env))
	setPluginHeaders(req.Header)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Agent-Purpose", "conversation")
	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "WorkBuddy 配置请求失败").
			WithCause(err).WithProvider(p.profile.id)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, mapUpstreamError(resp.StatusCode, truncateWB(string(raw), 300), resp.Header).WithProvider(p.profile.id)
	}
	var cfg wbConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "WorkBuddy 配置无法解析: "+err.Error()).WithProvider(p.profile.id)
	}

	allowed := map[string]bool{}
	agents := []string{"cli"}
	if !p.profile.intl {
		agents = []string{"craft", "ask", "plan"}
	}
	for _, a := range cfg.Data.Agents {
		for _, want := range agents {
			if strings.EqualFold(a.Name, want) {
				for _, id := range a.Models {
					allowed[id] = true
				}
			}
		}
	}
	if len(allowed) == 0 {
		for _, m := range cfg.Data.Models {
			if !strings.Contains(m.ID, "image") && !strings.Contains(m.ID, "video") && m.SupportsToolCall {
				allowed[m.ID] = true
			}
		}
	}

	now := time.Now()
	out := make([]provider.ModelInfo, 0, len(cfg.Data.Models))
	for _, m := range cfg.Data.Models {
		if m.ID == "" || !allowed[m.ID] {
			continue
		}
		rate, known := parseCredits(m.Credits)
		for _, promo := range cfg.Data.ModelPromotions {
			if !strings.EqualFold(promo.Kind, "discount") || !promoActive(promo, now) || !containsString(promo.ModelIDs, m.ID) {
				continue
			}
			if v, ok := parseCredits(promo.Discount.DiscountedCredits); ok {
				rate, known = v, true
			} else if promo.Discount.Factor >= 0 {
				rate, known = rate*promo.Discount.Factor, true
			}
		}
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = m.ID
		}
		out = append(out, provider.ModelInfo{
			ID:            m.ID,
			UpstreamModel: m.ID,
			Name:          name,
			Context:       m.MaxInputTokens,
			Capabilities: ir.ModelCapabilities{
				Streaming: true, ToolCall: m.SupportsToolCall,
				Reasoning: m.SupportsReasoning, Vision: m.SupportsImages,
			},
			Rate: rate,
			Free: known && rate == 0,
		})
	}
	if len(out) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "WorkBuddy 配置里没有可用的聊天模型").WithProvider(p.profile.id)
	}
	return out, nil
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func truncateWB(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
