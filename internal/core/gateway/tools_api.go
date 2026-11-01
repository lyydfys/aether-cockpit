package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/tools"
)

// ToolsConfig 是内置工具服务的配置（持久化在 data/settings.json）。
//
// 密钥字段在 GET /api/settings 里打码为 ******，回传该占位符表示"保留已存值"，
// 与 WebDAV 密码、OTLP headers 的约定一致。
type ToolsConfig struct {
	WebSearch *tools.SearchConfig   `json:"web_search,omitempty"`
	Image     *tools.ImageConfig    `json:"image,omitempty"`
	Audio     *tools.AudioConfig    `json:"audio,omitempty"`
	Video     *tools.VideoConfig    `json:"video,omitempty"`
	Realtime  *tools.RealtimeConfig `json:"realtime,omitempty"`
}

// maskTools 返回密钥打码后的副本（不改真实值）。
func maskTools(s runtimeSettings) runtimeSettings {
	if s.Tools == nil {
		return s
	}
	cp := s
	tc := *s.Tools
	if s.Tools.WebSearch != nil {
		ws := *s.Tools.WebSearch
		count := len(tools.SearchKeys(ws))
		if ws.APIKey != "" {
			ws.APIKey = webdavPasswordMask
		}
		// 多 Key 永不下发；只给出数量，避免密钥出现在 GET /api/settings。
		ws.APIKeys = nil
		ws.KeyCount = count
		tc.WebSearch = &ws
	}
	if s.Tools.Image != nil {
		im := *s.Tools.Image
		if im.APIKey != "" {
			im.APIKey = webdavPasswordMask
		}
		tc.Image = &im
	}
	if s.Tools.Audio != nil {
		au := *s.Tools.Audio
		if au.APIKey != "" {
			au.APIKey = webdavPasswordMask
		}
		tc.Audio = &au
	}
	if s.Tools.Video != nil {
		vd := *s.Tools.Video
		if vd.APIKey != "" {
			vd.APIKey = webdavPasswordMask
		}
		tc.Video = &vd
	}
	if s.Tools.Realtime != nil {
		rt := *s.Tools.Realtime
		if rt.APIKey != "" {
			rt.APIKey = webdavPasswordMask
		}
		tc.Realtime = &rt
	}
	cp.Tools = &tc
	return cp
}

// applyToolsConfig 应用工具配置更新，打码占位符沿用旧密钥。调用方持有 settingsMu。
func (g *Gateway) applyToolsConfig(in *ToolsConfig) {
	if in == nil {
		return
	}
	if in.WebSearch != nil {
		old := (*tools.SearchConfig)(nil)
		if g.settings.Tools != nil && g.settings.Tools.WebSearch != nil {
			old = g.settings.Tools.WebSearch
		}
		ws := in.WebSearch
		if ws.APIKey == webdavPasswordMask {
			// 没有旧值时不能把打码占位符本身当成 Key 存下来。
			ws.APIKey = ""
			if old != nil {
				ws.APIKey = old.APIKey
			}
		} else if ws.APIKey == "" && old != nil {
			ws.APIKey = old.APIKey
		}
		if len(ws.APIKeys) > 0 {
			keep, hasMask := false, false
			filtered := make([]string, 0, len(ws.APIKeys))
			for _, k := range ws.APIKeys {
				if k == webdavPasswordMask {
					hasMask = true
					continue
				}
				filtered = append(filtered, k)
			}
			if hasMask && old != nil {
				keep = true
			}
			if keep {
				ws.APIKeys = old.APIKeys
			} else {
				ws.APIKeys = filtered
				// 迁移到多 Key：清掉旧的单 Key，否则 SearchKeys 会把两张旧票混用。
				ws.APIKey = ""
			}
		} else if old != nil {
			ws.APIKeys = old.APIKeys
		}
		ws.KeyCount = 0
	}
	if in.Image != nil {
		old := (*tools.ImageConfig)(nil)
		if g.settings.Tools != nil && g.settings.Tools.Image != nil {
			old = g.settings.Tools.Image
		}
		if in.Image.APIKey == webdavPasswordMask {
			in.Image.APIKey = ""
			if old != nil {
				in.Image.APIKey = old.APIKey
			}
		} else if in.Image.APIKey == "" && old != nil {
			in.Image.APIKey = old.APIKey
		}
	}
	if in.Audio != nil {
		old := (*tools.AudioConfig)(nil)
		if g.settings.Tools != nil && g.settings.Tools.Audio != nil {
			old = g.settings.Tools.Audio
		}
		if in.Audio.APIKey == webdavPasswordMask {
			in.Audio.APIKey = ""
			if old != nil {
				in.Audio.APIKey = old.APIKey
			}
		} else if in.Audio.APIKey == "" && old != nil {
			in.Audio.APIKey = old.APIKey
		}
	}
	if in.Video != nil {
		old := (*tools.VideoConfig)(nil)
		if g.settings.Tools != nil && g.settings.Tools.Video != nil {
			old = g.settings.Tools.Video
		}
		if in.Video.APIKey == webdavPasswordMask {
			in.Video.APIKey = ""
			if old != nil {
				in.Video.APIKey = old.APIKey
			}
		} else if in.Video.APIKey == "" && old != nil {
			in.Video.APIKey = old.APIKey
		}
	}
	if in.Realtime != nil {
		old := (*tools.RealtimeConfig)(nil)
		if g.settings.Tools != nil && g.settings.Tools.Realtime != nil {
			old = g.settings.Tools.Realtime
		}
		if in.Realtime.APIKey == webdavPasswordMask {
			in.Realtime.APIKey = ""
			if old != nil {
				in.Realtime.APIKey = old.APIKey
			}
		} else if in.Realtime.APIKey == "" && old != nil {
			in.Realtime.APIKey = old.APIKey
		}
	}
	g.settings.Tools = in
}

// searchConfig 返回当前搜索配置（无配置时 ok=false）。
func (g *Gateway) searchConfig() (tools.SearchConfig, bool) {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.Tools == nil || g.settings.Tools.WebSearch == nil {
		return tools.SearchConfig{}, false
	}
	return *g.settings.Tools.WebSearch, true
}

// imageConfig 返回当前生图配置（无配置时 ok=false）。
func (g *Gateway) imageConfig() (tools.ImageConfig, bool) {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.Tools == nil || g.settings.Tools.Image == nil {
		return tools.ImageConfig{}, false
	}
	cfg := *g.settings.Tools.Image
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return tools.ImageConfig{}, false
	}
	return cfg, true
}

// handleToolWebSearch POST /v1/tools/web_search 与 POST /api/tools/web-search
//
// 请求体：{"query":"...","max_results":5}
// 响应：{"provider":"tavily","results":[{"title","url","snippet"}]}
func (g *Gateway) handleToolWebSearch(w http.ResponseWriter, r *http.Request) {
	cfg, ok := g.searchConfig()
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "尚未配置网络搜索：请在设置页填写 provider 与 API Key（tavily / serper / searxng / brave / exa / firecrawl / deepseek / you）",
		})
		return
	}
	var body struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	results, err := tools.Search(r.Context(), cfg, body.Query, body.MaxResults)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": cfg.Provider, "query": body.Query, "results": results,
	})
}

// handleImageTest POST /api/tools/image-test：用当前配置发一张极小的图，
// 让设置页的“测试”按钮能确认 base_url / Key / 模型是否真的能用。
func (g *Gateway) handleImageTest(w http.ResponseWriter, r *http.Request) {
	cfg, ok := g.imageConfig()
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "尚未配置图像生成：请填写 base_url（OpenAI 兼容 /images/generations）"})
		return
	}
	var body struct {
		Prompt string `json:"prompt"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	prompt := strings.TrimSpace(body.Prompt)
	if prompt == "" {
		prompt = "a small red dot on a white background"
	}
	test := cfg
	if test.TimeoutSeconds <= 0 || test.TimeoutSeconds > 60 {
		test.TimeoutSeconds = 60
	}
	payload, _ := json.Marshal(map[string]any{"prompt": prompt})
	code, out, err := tools.GenerateImage(r.Context(), test, payload)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if code < 200 || code >= 300 {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": fmt.Sprintf("图像上游返回 %d: %s", code, clipText(string(out), 300)),
		})
		return
	}
	summary := fmt.Sprintf("上游 %d · %d 字节", code, len(out))
	var parsed struct {
		Data []struct {
			URL string `json:"url"`
			B64 string `json:"b64_json"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &parsed) == nil && len(parsed.Data) > 0 {
		if u := parsed.Data[0].URL; u != "" {
			summary += " · " + u
		} else if parsed.Data[0].B64 != "" {
			summary += fmt.Sprintf(" · b64 %d KB", len(parsed.Data[0].B64)/1024)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": code, "bytes": len(out), "summary": summary})
}

// handleImageGenerate POST /v1/images/generations
//
// OpenAI 兼容形状：请求体原样透传给配置的上游（可补模型），
// 响应体也原样透传（url / b64_json 两种形态都支持）。
func (g *Gateway) handleImageGenerate(w http.ResponseWriter, r *http.Request) {
	cfg, ok := g.imageConfig()
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "尚未配置图像生成：请在设置页填写 base_url（OpenAI 兼容 /images/generations）",
		})
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	status, out, err := tools.GenerateImage(r.Context(), cfg, raw)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(out)
}
