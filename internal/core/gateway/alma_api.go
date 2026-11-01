package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Alma 导入：Alma 是桌面 AI 聊天应用，provider 存在应用内部，
// 运行期间通过本机 REST 暴露（http://localhost:23001）：
//
//	GET /api/providers → [{"id","name","type","baseURL","apiKey","enabled",...}]
//
// 只有 type=openai 且启用的 provider 能直接变成我们的 openai-compat 账号；
// 指回本网关自身的条目（baseURL 含本机端口）会被跳过，避免自我导入。
// Alma 未运行时静默当作"没有可导入条目"。

// almaAPIBase 允许测试覆盖（ALMA_API）。
func almaAPIBase() string {
	if v := strings.TrimSpace(os.Getenv("ALMA_API")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:23001"
}

var almaClient = &http.Client{Timeout: 1500 * time.Millisecond}

type almaProvider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
	Enabled bool   `json:"enabled"`
}

// fetchAlmaProviders 读取 Alma 的 provider 列表；未运行返回 nil。
func fetchAlmaProviders() []almaProvider {
	req, err := http.NewRequest(http.MethodGet, almaAPIBase()+"/api/providers", nil)
	if err != nil {
		return nil
	}
	resp, err := almaClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil
	}
	var list []almaProvider
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	return list
}

// almaEntries 把 Alma provider 折成可导入条目（目标渠道固定 openai-compat）。
func (g *Gateway) almaEntries() []localProviderSwitchEntry {
	var out []localProviderSwitchEntry
	self := strings.TrimSpace(g.PublicAddr())
	for _, p := range fetchAlmaProviders() {
		e := localProviderSwitchEntry{
			ID:      p.ID,
			AppType: "alma",
			Name:    p.Name,
			Target:  "openai-compat",
		}
		label := strings.TrimSpace(p.Name)
		if label == "" {
			label = p.ID
		}
		e.Label = "Alma · " + label
		if !p.Enabled {
			e.Skip = "Alma 里该 provider 未启用"
			out = append(out, e)
			continue
		}
		if strings.ToLower(strings.TrimSpace(p.Type)) != "openai" {
			e.Skip = "暂不支持 Alma 的 " + p.Type + " 类型"
			out = append(out, e)
			continue
		}
		if strings.TrimSpace(p.APIKey) == "" || strings.TrimSpace(p.BaseURL) == "" {
			e.Skip = "缺少 API Key 或 Base URL"
			out = append(out, e)
			continue
		}
		if self != "" && strings.Contains(p.BaseURL, self) {
			e.Skip = "该 provider 指向本网关自身"
			out = append(out, e)
			continue
		}
		e.APIKey = strings.TrimSpace(p.APIKey)
		e.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
		e.CredKeys = []string{"api_key", "base_url"}
		out = append(out, e)
	}
	return out
}

// handleAlmaImportList GET /api/import/alma
func (g *Gateway) handleAlmaImportList(w http.ResponseWriter, r *http.Request) {
	entries := g.almaEntries()
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"id": e.ID, "name": e.Name, "label": e.Label,
			"base_url": e.BaseURL, "skip": e.Skip,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": len(entries) > 0,
		"api":       almaAPIBase(),
		"entries":   out,
		"note":      "Alma 需要保持运行；只导入 type=openai 且启用的 provider，写入 openai-compat 账号。",
	})
}

// handleAlmaImport POST /api/import/alma {"ids":["..."]}
func (g *Gateway) handleAlmaImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = json.Unmarshal(raw, &body)
	entries := g.almaEntries()
	want := map[string]bool{}
	for _, id := range body.IDs {
		want[id] = true
	}
	added := make([]string, 0, len(entries))
	for _, e := range entries {
		if len(want) > 0 && !want[e.ID] {
			continue
		}
		if e.Skip != "" {
			continue
		}
		if _, apiErr := g.createAccount("openai-compat", e.Label, e.credentials(), ""); apiErr != nil {
			continue
		}
		added = append(added, e.Label)
	}
	g.saveAccountsNow()
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "count": len(added)})
}
