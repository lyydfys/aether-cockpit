package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Codex 的模型目录（models.json）。Codex 只在 config.toml 的
// model_catalog_json 指向它时才读；没有它，用 API key 接入的 Codex
// 不会拉 /v1/models，/model 里就看不到网关的模型。
//
// 字段按 Codex 0.164 实测（最小可用集）：catalog 能被加载、/model 列出，
// 并能完成一次 /v1/responses 往返。用户自己的 model_catalog_json 不动。

const codexCatalogName = "aether-models.json"

type codexLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type codexEntry struct {
	Slug                     string       `json:"slug"`
	DisplayName              string       `json:"display_name"`
	Description              string       `json:"description"`
	BaseInstructions         string       `json:"base_instructions"`
	DefaultReasoningLevel    *string      `json:"default_reasoning_level"`
	SupportedReasoningLevels []codexLevel `json:"supported_reasoning_levels"`
	ShellType                string       `json:"shell_type"`
	Visibility               string       `json:"visibility"`
	SupportedInAPI           bool         `json:"supported_in_api"`
	Priority                 int          `json:"priority"`
	SupportVerbosity         bool         `json:"support_verbosity"`
	DefaultVerbosity         *string      `json:"default_verbosity"`
	ApplyPatchToolType       string       `json:"apply_patch_tool_type"`
	TruncationPolicy         struct {
		Mode  string `json:"mode"`
		Limit int    `json:"limit"`
	} `json:"truncation_policy"`
	ExperimentalSupportedTools []string `json:"experimental_supported_tools"`
	InputModalities            []string `json:"input_modalities"`
	ServiceTiers               []any    `json:"service_tiers"`
	SupportsSearchTool         bool     `json:"supports_search_tool"`
	SupportsParallelToolCalls  bool     `json:"supports_parallel_tool_calls"`
	SupportsReasoningSummaries bool     `json:"supports_reasoning_summaries"`
}

func codexCatalog(opts []ModelOption) []byte {
	entries := make([]codexEntry, 0, len(opts))
	for i, o := range opts {
		if strings.TrimSpace(o.Value) == "" {
			continue
		}
		name := o.Label
		if name == "" {
			name = o.Value
		}
		desc := strings.TrimSpace(o.Description)
		if desc == "" {
			desc = "Aether Cockpit"
		}
		e := codexEntry{
			Slug: o.Value, DisplayName: name, Description: desc,
			BaseInstructions: "You are a helpful coding agent.",
			ShellType:        "shell_command", Visibility: "list", SupportedInAPI: true,
			Priority: i + 1, SupportVerbosity: false, ApplyPatchToolType: "freeform",
			SupportedReasoningLevels:   []codexLevel{},
			ExperimentalSupportedTools: []string{}, InputModalities: []string{"text"},
			ServiceTiers: []any{}, SupportsSearchTool: false, SupportsParallelToolCalls: true,
			SupportsReasoningSummaries: false,
		}
		e.TruncationPolicy.Mode = "tokens"
		e.TruncationPolicy.Limit = 10000
		entries = append(entries, e)
	}
	b, _ := json.MarshalIndent(map[string]any{"models": entries}, "", " ")
	return b
}

func writeCodexCatalog(path string, opts []ModelOption) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, codexCatalog(opts), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// tomlTopValue 读 TOML 顶层字符串键（只用于 model_catalog_json 这类简单键）。
func tomlTopValue(content []byte, key string) string {
	for _, line := range strings.Split(string(content), "\n") {
		t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(t, "[") {
			break
		}
		if !strings.HasPrefix(t, key) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, key))
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		return strings.Trim(strings.TrimSpace(strings.TrimPrefix(rest, "=")), `"'`)
	}
	return ""
}
