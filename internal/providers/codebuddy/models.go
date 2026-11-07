package codebuddy

// CodeBuddy 模型目录：GET {origin}/v3/config（与 WorkBuddy 同族接口）。
//
// 之前 CodeBuddy 没有实现 ModelLister，网关"获取模型"只能按通用 OpenAI
// 约定去 GET {base}/models —— 而 CodeBuddy 站点上 /models 是 404
// （{"error_msg":"404 Route Not Found"}），于是登录成功后也永远拿不到模型。
// 实测（2026-11-06）：www.codebuddy.ai / www.codebuddy.cn 的
// /models、/v1/models、/v2/models 均 404；/v3/config 返回 200
// （未带鉴权时 data.models 为 null，带 token 后返回目录）。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// SupportsModelList 表示账号有 token 时可从 /v3/config 拉目录。
func (p *Provider) SupportsModelList(env *provider.AccountEnv) bool {
	return strings.TrimSpace(cbFirst(env.GetCredential("token"), env.GetCredential("access_token"))) != ""
}

// ListModels 实现 provider.ModelLister。
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

type cbConfigModel struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	MaxInputTokens    int    `json:"maxInputTokens"`
	SupportsImages    bool   `json:"supportsImages"`
	SupportsReasoning bool   `json:"supportsReasoning"`
	SupportsToolCall  bool   `json:"supportsToolCall"`
}

type cbConfigAgent struct {
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

type cbConfig struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Models []cbConfigModel `json:"models"`
		// 官方响应里 agent 是对象（{agents:[...]}），部分版本直接给 agents。
		Agent struct {
			Agents []cbConfigAgent `json:"agents"`
		} `json:"agent"`
		Agents []cbConfigAgent `json:"agents"`
	} `json:"data"`
}

// ListModelInfos 读取 /v3/config 的模型目录。
func (p *Provider) ListModelInfos(ctx context.Context, env *provider.AccountEnv) ([]provider.ModelInfo, error) {
	token := strings.TrimSpace(cbFirst(env.GetCredential("token"), env.GetCredential("access_token")))
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "CodeBuddy 账号缺少 access_token").
			WithProvider(p.profile.id)
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = p.profile.baseURL
	}
	// 账号 base_url 可能带 /v2（对话端点）；/v3/config 挂在站点根上。
	origin := strings.TrimSuffix(base, "/v2")
	endpoint := origin + "/v3/config"
	intl := p.profile.id == intlProfile.id

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 CodeBuddy 模型目录请求失败").WithCause(err)
	}
	// 复用对话链路的 CLI 指纹/会话头，避免模型目录接口因"头不像官方"被拒。
	applyChatHeaders(req, env, origin, token, "", intl)

	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "CodeBuddy 模型目录连接失败").
			WithCause(err).WithProvider(p.profile.id)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"CodeBuddy 模型目录返回 %d：%s", resp.StatusCode, truncateCBText(string(raw), 300)).
			WithProvider(p.profile.id)
	}
	var cfg cbConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "CodeBuddy 模型目录无法解析: "+err.Error()).
			WithProvider(p.profile.id)
	}
	if cfg.Code != 0 && cfg.Code != 200 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"CodeBuddy 模型目录返回业务码 %d：%s", cfg.Code, strings.TrimSpace(cfg.Msg)).
			WithProvider(p.profile.id)
	}

	out := make([]provider.ModelInfo, 0, len(cfg.Data.Models))
	seen := map[string]bool{}
	for _, m := range cfg.Data.Models {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, provider.ModelInfo{
			ID:            id,
			UpstreamModel: id,
			Name:          firstNonEmptyTrim(m.Name, id),
			Context:       m.MaxInputTokens,
			Capabilities: ir.ModelCapabilities{
				Streaming: true,
				ToolCall:  m.SupportsToolCall,
				Reasoning: m.SupportsReasoning,
				Vision:    m.SupportsImages,
			},
		})
	}
	// 兼容只给 agent 列表的版本：从 agent.models 里兜底取聊天模型名。
	if len(out) == 0 {
		agents := cfg.Data.Agent.Agents
		if len(agents) == 0 {
			agents = cfg.Data.Agents
		}
		for _, a := range agents {
			for _, name := range a.Models {
				id := strings.TrimSpace(name)
				if id == "" || seen[id] {
					continue
				}
				seen[id] = true
				out = append(out, provider.ModelInfo{
					ID: id, UpstreamModel: id, Name: id,
					Capabilities: ir.ModelCapabilities{Streaming: true, ToolCall: true},
				})
			}
		}
	}
	if len(out) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeUnsupported, "CodeBuddy 上游没有返回模型目录").
			WithProvider(p.profile.id)
	}
	return out, nil
}

func truncateCBText(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func firstNonEmptyTrim(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
