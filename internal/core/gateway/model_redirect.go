package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"aether-cockpit/internal/provider"
)

// 模型重定向（对齐 One API / New API 的「虚拟名 → 渠道上游名」）：
//
//	{
//	  "deepseek-v4.1-flash": {
//	    "qoder-cn": "DeepSeek-Flash",
//	    "workbuddy-intl": "deepseek-v4.1-flash-sg",
//	    "trae-cn": "deepseek-v4.1-flash"
//	  }
//	}
//
// 客户端只请求虚拟名；路由层按渠道注册顺序生成候选链，转发时使用该渠道
// 配置的上游名。重定向是显式配置：V4 Flash 与 V4.1 Flash 不会被自动
// 合并，只有用户明确写出来的映射才会生效。

// normalizeModelRedirects 校验并清理重定向表。
func normalizeModelRedirects(in map[string]map[string]string) (map[string]map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]map[string]string, len(in))
	seenAlias := make(map[string]string, len(in))
	for rawAlias, targets := range in {
		alias := strings.TrimSpace(rawAlias)
		if alias == "" {
			return nil, fmt.Errorf("模型重定向的虚拟名不能为空")
		}
		if strings.Contains(alias, "/") {
			return nil, fmt.Errorf("模型重定向的虚拟名 %q 不能包含 /（渠道前缀请用 provider/model 直接寻址）", alias)
		}
		key := normalizeModelKey(alias)
		if key == "" {
			return nil, fmt.Errorf("模型重定向的虚拟名 %q 无效", alias)
		}
		if prev, dup := seenAlias[key]; dup {
			return nil, fmt.Errorf("模型重定向的虚拟名 %q 与 %q 归一化后相同", alias, prev)
		}
		seenAlias[key] = alias

		clean := make(map[string]string, len(targets))
		for rawPid, rawUpstream := range targets {
			pid := strings.TrimSpace(rawPid)
			upstream := strings.TrimSpace(rawUpstream)
			if pid == "" || upstream == "" {
				return nil, fmt.Errorf("模型重定向 %q 的渠道和上游模型名都不能为空", alias)
			}
			if _, ok := provider.Get(pid); !ok {
				return nil, fmt.Errorf("模型重定向 %q 引用了未注册渠道 %q", alias, pid)
			}
			clean[pid] = upstream
		}
		if len(clean) == 0 {
			return nil, fmt.Errorf("模型重定向 %q 至少需要一个渠道目标", alias)
		}
		out[alias] = clean
	}
	return out, nil
}

// modelRedirects 返回重定向表副本。
func (g *Gateway) modelRedirects() map[string]map[string]string {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	return cloneModelRedirects(g.settings.ModelRedirects)
}

// setModelRedirects 保存重定向表并立即重建路由表里的虚拟名候选链。
func (g *Gateway) setModelRedirects(in map[string]map[string]string) error {
	clean, err := normalizeModelRedirects(in)
	if err != nil {
		return err
	}
	g.settingsMu.Lock()
	if len(clean) == 0 {
		g.settings.ModelRedirects = nil
	} else {
		g.settings.ModelRedirects = cloneModelRedirects(clean)
	}
	err = g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		return err
	}
	g.models.SetModelRedirects(clean)
	return nil
}

func cloneModelRedirects(in map[string]map[string]string) map[string]map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]string, len(in))
	for alias, targets := range in {
		cp := make(map[string]string, len(targets))
		for pid, upstream := range targets {
			cp[pid] = upstream
		}
		out[alias] = cp
	}
	return out
}

// redirectNames 返回已配置的虚拟名（排序后，供 UI/模型选择器展示）。
func (g *Gateway) redirectNames() []string {
	redirects := g.modelRedirects()
	out := make([]string, 0, len(redirects))
	for name := range redirects {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// availableRedirectNames 返回至少有一个已接入、已启用目标渠道的虚拟名。
func (g *Gateway) availableRedirectNames() map[string]bool {
	redirects := g.modelRedirects()
	out := make(map[string]bool, len(redirects))
	for name, targets := range redirects {
		for pid := range targets {
			if g.providerIsEnabled(pid) && g.accountHasProvider(pid) {
				out[name] = true
				break
			}
		}
	}
	return out
}

// handleModelRedirects GET /api/model-redirects
func (g *Gateway) handleModelRedirects(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"redirects": g.modelRedirects()})
}

// handleSaveModelRedirects POST /api/model-redirects
// body: {"redirects": {"虚拟名": {"渠道": "上游模型名"}}}
func (g *Gateway) handleSaveModelRedirects(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Redirects map[string]map[string]string `json:"redirects"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if err := g.setModelRedirects(body.Redirects); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"redirects": g.modelRedirects()})
}
