package clients

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Zed：自定义 OpenAI 兼容 provider 写在 settings.json
// （language_models.openai_compatible.aether + agent.default_model），
// API Key 不在设置里，而是系统凭据存储：Windows 上是 Credential Manager，
// 目标名 `zed:url=<api_url>`、用户名 Bearer、blob 为 Key 字节
// （zed crates/gpui_windows/src/util.rs 与 language_model/src/api_key.rs）。
// 少写这一步 Zed 只会显示未配置 Key，所以 Key 写失败时整个 Apply 失败。
const zedProviderKey = "aether"

func zedSettingsPath(home string) string {
	if d := strings.TrimSpace(os.Getenv("ZED_CONFIG_DIR")); d != "" {
		return filepath.Join(d, "settings.json")
	}
	if runtime.GOOS == "windows" {
		base := strings.TrimSpace(os.Getenv("APPDATA"))
		if base == "" {
			base = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(base, "Zed", "settings.json")
	}
	return filepath.Join(home, ".config", "zed", "settings.json")
}

// 可在测试里替换，避免碰真实系统凭据。
var (
	zedWriteCredential  = writeZedCredential
	zedDeleteCredential = deleteZedCredential
)

func init() {
	defs = append(defs, clientDef{
		id:      "zed",
		name:    "Zed",
		kind:    KindJSON,
		relPath: filepath.Join(".config", "zed", "settings.json"),
		note:    "写入 settings.json 的 language_models.openai_compatible.aether 与 agent.default_model，并把 Key 写入系统凭据（zed:url=<网关>/v1）。应用后重启 Zed。",
		apply:   applyZed,
		managed: managedZed,
		pathFn:  zedSettingsPath,
		restoreHook: func(path string) {
			if raw, err := os.ReadFile(path); err == nil {
				if u := zedAPIURL(raw); u != "" {
					_ = zedDeleteCredential(u)
				}
			}
		},
	})
}

// zedAPIURL 从已有 settings 里取 aether provider 的 api_url（还原前删凭据用）。
func zedAPIURL(settings []byte) string {
	var root struct {
		LanguageModels struct {
			OpenAICompatible map[string]struct {
				APIURL string `json:"api_url"`
			} `json:"openai_compatible"`
		} `json:"language_models"`
	}
	if json.Unmarshal(settings, &root) != nil {
		return ""
	}
	return strings.TrimSpace(root.LanguageModels.OpenAICompatible[zedProviderKey].APIURL)
}

// applyZed 写 settings.json 并落 Key。JSONC（含注释）拒绝改写。
func applyZed(content []byte, p ApplyParams) ([]byte, error) {
	root, err := parseJSONCObject(content, "Zed settings.json")
	if err != nil {
		return nil, err
	}
	apiURL := p.BaseURL + "/v1"
	rows := modelRows(p)
	if len(rows) == 0 && p.Model != "" {
		rows = [][2]string{{p.Model, p.Model}}
	}
	models := make([]any, 0, len(rows))
	for _, r := range rows {
		models = append(models, map[string]any{
			"name":         r[0],
			"display_name": r[1],
			"max_tokens":   128000,
			"capabilities": map[string]any{
				"tools": true, "images": true,
				"parallel_tool_calls": false, "prompt_cache_key": false,
				"chat_completions": true,
			},
		})
	}

	lm, _ := root["language_models"].(map[string]any)
	if lm == nil {
		lm = map[string]any{}
	}
	oc, _ := lm["openai_compatible"].(map[string]any)
	if oc == nil {
		oc = map[string]any{}
	}
	oc[zedProviderKey] = map[string]any{"api_url": apiURL, "available_models": models}
	lm["openai_compatible"] = oc
	root["language_models"] = lm

	if len(rows) > 0 {
		agent, _ := root["agent"].(map[string]any)
		if agent == nil {
			agent = map[string]any{}
		}
		agent["default_model"] = map[string]any{"provider": zedProviderKey, "model": rows[0][0]}
		root["agent"] = agent
	}

	// Key 先写：写不进就不改文件，避免配置好了但没有 Key 的假接入。
	if err := zedWriteCredential(apiURL, p.APIKey); err != nil {
		return nil, fmt.Errorf("写入 Zed 系统凭据失败: %w", err)
	}
	out, err := finishJSON(content, root)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func managedZed(data []byte) *ManagedInfo {
	var root struct {
		LanguageModels struct {
			OpenAICompatible map[string]struct {
				APIURL string `json:"api_url"`
			} `json:"openai_compatible"`
		} `json:"language_models"`
		Agent struct {
			DefaultModel struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
			} `json:"default_model"`
		} `json:"agent"`
	}
	if json.Unmarshal(data, &root) != nil {
		return nil
	}
	p := root.LanguageModels.OpenAICompatible[zedProviderKey]
	if p.APIURL == "" {
		return nil
	}
	return &ManagedInfo{BaseURL: p.APIURL, Model: root.Agent.DefaultModel.Model}
}
