package qoder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// SupportsModelList 表示可以从上游拉模型目录（/api/v2/model/list）。
func (p *Provider) SupportsModelList(env *provider.AccountEnv) bool { return true }

// ListModels 实现基础 ModelLister；目录层优先用 ListModelInfos。
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

type qoderCatalogEntry struct {
	Key            string  `json:"key"`
	DisplayName    string  `json:"display_name"`
	Enable         *bool   `json:"enable"`
	IsVL           bool    `json:"is_vl"`
	IsReasoning    bool    `json:"is_reasoning"`
	IsFree         *bool   `json:"is_free"`
	PriceFactor    float64 `json:"price_factor"`
	MaxInputTokens int     `json:"max_input_tokens"`
	ThinkingConfig *struct {
		Enabled *struct {
			Efforts map[string]json.RawMessage `json:"efforts"`
		} `json:"enabled"`
	} `json:"thinking_config"`
}

type qoderCatalog struct {
	Chat []qoderCatalogEntry `json:"chat"`
}

// ListModelInfos 读取 Qoder 的模型目录：对外用显示名，聊天上游用内部 key。
func (p *Provider) ListModelInfos(ctx context.Context, env *provider.AccountEnv) ([]provider.ModelInfo, error) {
	c, err := p.ensureToken(ctx, env)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = strings.TrimRight(p.profile.baseURL, "/")
	}
	endpoint := base + "/algo/api/v2/model/list"

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Qoder 模型目录请求失败").WithCause(err)
	}
	headers, err := p.buildCOSYHeaders(nil, endpoint, c)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.doHTTP(env, req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Qoder 模型目录连接失败").
			WithCause(err).WithProvider(p.profile.id)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, mapHTTPError(resp.StatusCode, truncate(string(raw), 300), resp.Header).
			WithProvider(p.profile.id)
	}
	var cat qoderCatalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Qoder 模型目录无法解析: "+err.Error()).WithProvider(p.profile.id)
	}
	out := make([]provider.ModelInfo, 0, len(cat.Chat))
	seenID := map[string]bool{}
	seenKey := map[string]bool{}
	for _, m := range cat.Chat {
		key := strings.TrimSpace(m.Key)
		if key == "" || seenKey[key] || (m.Enable != nil && !*m.Enable) {
			continue
		}
		seenKey[key] = true
		name := strings.TrimSpace(m.DisplayName)
		if name == "" {
			name = key
		}
		id := strings.ReplaceAll(name, " ", "-")
		if id == "" {
			id = key
		}
		if seenID[id] {
			id = id + "-" + key
		}
		seenID[id] = true
		info := provider.ModelInfo{
			ID:            id,
			UpstreamModel: key,
			Name:          name,
			Context:       m.MaxInputTokens,
			Capabilities: ir.ModelCapabilities{
				Streaming: true, ToolCall: true,
				Vision: m.IsVL, Reasoning: m.IsReasoning,
			},
			Rate: m.PriceFactor,
			Free: m.IsFree != nil && *m.IsFree,
		}
		if m.ThinkingConfig != nil && m.ThinkingConfig.Enabled != nil {
			for effort := range m.ThinkingConfig.Enabled.Efforts {
				info.Efforts = append(info.Efforts, effort)
			}
			sortEfforts(info.Efforts)
		}
		out = append(out, info)
	}
	if len(out) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Qoder 模型目录里没有 chat 模型").WithProvider(p.profile.id)
	}
	return out, nil
}

func sortEfforts(list []string) {
	rank := map[string]int{"low": 1, "medium": 2, "high": 3, "xhigh": 4, "max": 5}
	sort.SliceStable(list, func(i, j int) bool {
		ri, iok := rank[strings.ToLower(list[i])]
		rj, jok := rank[strings.ToLower(list[j])]
		if !iok {
			ri = 99
		}
		if !jok {
			rj = 99
		}
		return ri < rj
	})
}

// legacyModelKey 把旧静态目录里的模型名映射到上游 key。
// 这些显示名此前会落到 auto（上游流里 model 一直是 auto），实时目录上线后
// 改用内部 key，显式请求旧名字时在这里兜一下。
var legacyQoderModelKeys = map[string]string{
	"deepseek-v4-flash": "dfmodel", "deepseek-v4-pro": "dmodel",
	"deepseek-v4.1-flash": "dfmodel", "deepseek-v4.1-pro": "dmodel",
	"deepseek-flash": "dfmodel",
	"glm-5.1":        "gmodel", "glm-5.2": "gmodel", "glm-5.3": "gmodel",
	"glm-5.3-flash": "gfmodel",
	"kimi-k2":       "kmodel", "kimi-k2.8-preview": "kmodel",
	"kimi-k3.1-pro": "kmodel_latest",
	"minimax-m3":    "mmodel", "minimax-m3.1-flash": "mmodel",
	"qwen3.8-max": "qmodel_38max", "qwen3.8-flash": "qfmodel",
	"ultimate": "auto", "performance": "auto", "efficient": "auto",
}

func legacyModelKey(model string) string {
	if key, ok := legacyQoderModelKeys[strings.ToLower(strings.TrimSpace(model))]; ok {
		return key
	}
	return model
}
