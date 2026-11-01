package reverse

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// localImportConfig 描述"从本机已登录客户端读取凭证"的规则。
//
// 为什么做成 spec 驱动：各客户端的配置路径与字段名差异极大，而且会随版本变化。
// 把这些规则写进 JSON 后，用户拿到新版本的目录结构只需要改配置，不需要改代码。
//
//	spec.extra.local_import = {
//	  "paths": ["%APPDATA%/Trae/User/globalStorage/auth.json"],
//	  "format": "json",
//	  "label_path": "user.email",
//	  "credentials": {"access_token": "auth.accessToken", "refresh_token": "auth.refreshToken"},
//	  "expires_at_path": "auth.expiresAt"
//	}
type localImportConfig struct {
	Paths []string `json:"paths"`
	// Format: json（默认）| text | toml。
	// text 表示整个文件内容就是一个凭证；toml 用 section.key 路径读取配置。
	Format string `json:"format,omitempty"`
	// Label 是固定账号展示名；为空时用 LabelPath 或文件名。
	// 同一文件承载多个渠道（OpenCode Zen / Go）时靠它区分。
	Label string `json:"label,omitempty"`
	// LabelPath 用于展示账号名的字段路径。
	LabelPath string `json:"label_path,omitempty"`
	// Credentials: 目标凭证 key -> 源字段路径（为空表示整个文件）。
	Credentials map[string]string `json:"credentials,omitempty"`
	// ExpiresAtPath 凭证过期时间字段。
	ExpiresAtPath string `json:"expires_at_path,omitempty"`
}

// builtinLocalImport 是"本机确实存在该客户端"时可直接使用的默认规则。
//
// spec 里的 extra.local_import 优先级更高；没有 spec 时才使用这里的规则。
// 这样用户不需要先写一份 spec 才能点"扫描本机登录态"。
func builtinLocalImport(providerID string) (*localImportConfig, bool) {
	switch providerID {
	case "codex":
		// Codex CLI 的 config.toml 允许自定义 provider：
		// [model_providers.custom] base_url / experimental_bearer_token
		return &localImportConfig{
			Paths:     []string{"~/.codex/config.toml"},
			Format:    "toml",
			LabelPath: "model_providers.custom.name",
			Credentials: map[string]string{
				"base_url":     "model_providers.custom.base_url",
				"access_token": "model_providers.custom.experimental_bearer_token",
				"model":        "model",
			},
		}, true

	case "opencode":
		// Zen 与 Go 在 auth.json 里是两个 provider：
		// {"opencode":{"type":"api","key":"..."},"opencode-go":{"type":"api","key":"..."}}
		return &localImportConfig{
			Paths:       []string{"~/.local/share/opencode/auth.json"},
			Format:      "json",
			Label:       "OpenCode Zen",
			Credentials: map[string]string{"api_key": "opencode.key"},
		}, true

	case "opencode-go":
		return &localImportConfig{
			Paths:       []string{"~/.local/share/opencode/auth.json"},
			Format:      "json",
			Label:       "OpenCode Go",
			Credentials: map[string]string{"api_key": "opencode-go.key"},
		}, true
	}
	return nil, false
}

// DiscoverLocalAccounts 扫描 spec 声明的本机配置路径。
func (p *Provider) DiscoverLocalAccounts(ctx context.Context) ([]*provider.CredentialDraft, error) {
	sp := spec.Load(p.profile.ID)

	var cfg *localImportConfig
	if sp != nil && len(sp.Extra) > 0 {
		var extra struct {
			LocalImport *localImportConfig `json:"local_import"`
		}
		if err := json.Unmarshal(sp.Extra, &extra); err != nil {
			return nil, fmt.Errorf("spec.extra 解析失败: %w", err)
		}
		cfg = extra.LocalImport
	}
	// spec 没写 local_import 时退回内置规则（本机已知客户端的标准路径）。
	if cfg == nil || len(cfg.Paths) == 0 {
		if b, ok := builtinLocalImport(p.profile.ID); ok {
			cfg = b
		}
	}
	if cfg == nil || len(cfg.Paths) == 0 {
		return nil, p.localImportNotConfigured("spec.extra.local_import.paths 为空，且没有内置规则")
	}
	if len(cfg.Credentials) == 0 {
		return nil, p.localImportNotConfigured("spec.extra.local_import.credentials 为空")
	}

	var drafts []*provider.CredentialDraft
	seen := make(map[string]bool, 4)

	for _, raw := range cfg.Paths {
		select {
		case <-ctx.Done():
			return drafts, ctx.Err()
		default:
		}

		for _, path := range expandLocalPath(raw) {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.IsDir() {
				entries, err := os.ReadDir(path)
				if err != nil {
					continue
				}
				for _, e := range entries {
					if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
						continue
					}
					if d := p.readDraft(cfg, filepath.Join(path, e.Name())); d != nil {
						if !seen[d.Source] {
							seen[d.Source] = true
							drafts = append(drafts, d)
						}
					}
				}
				continue
			}
			if d := p.readDraft(cfg, path); d != nil {
				if !seen[d.Source] {
					seen[d.Source] = true
					drafts = append(drafts, d)
				}
			}
		}
	}

	// Codex 订阅：~/.codex/auth.json 是 ChatGPT 登录态，与 config.toml 的中转
	// provider 可以同时存在，各导入一个账号。
	if p.profile.ID == "codex" {
		if d := p.codexAuthJSONDraft(); d != nil && !seen[d.Source] {
			seen[d.Source] = true
			drafts = append(drafts, d)
		}
	}

	if len(drafts) == 0 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeNoAccount,
			"已按 spec 扫描 %d 个路径，但没有找到可用凭证。请确认客户端已登录，或更新 spec 里的路径与字段名。",
			len(cfg.Paths)).WithProvider(p.profile.ID)
	}
	return drafts, nil
}

func (p *Provider) localImportNotConfigured(why string) *ir.APIError {
	return ir.NewAPIErrorf(ir.ErrCodeUnsupported,
		"渠道 %s 的本机导入尚未配置（%s）。请在 configs/specs/%s.json 的 extra.local_import 里声明 paths 与 credentials，见 docs/ADD_PROVIDER.md。",
		p.profile.ID, why, p.profile.ID).WithProvider(p.profile.ID)
}

// readDraft 读取一个文件并提取凭证。
func (p *Provider) readDraft(cfg *localImportConfig, path string) *provider.CredentialDraft {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	if strings.EqualFold(cfg.Format, "text") {
		token := strings.TrimSpace(string(raw))
		if token == "" {
			return nil
		}
		creds := make(map[string]string, len(cfg.Credentials))
		for dst := range cfg.Credentials {
			creds[dst] = token
		}
		label := "local-" + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if cfg.Label != "" {
			label = cfg.Label
		}
		return &provider.CredentialDraft{
			Label:       label,
			Credentials: creds,
			Source:      "local_import:" + path,
		}
	}

	if strings.EqualFold(cfg.Format, "toml") {
		flat := parseTOMLFlat(raw)
		creds := make(map[string]string, len(cfg.Credentials))
		for dst, src := range cfg.Credentials {
			if v := strings.TrimSpace(flat[src]); v != "" {
				creds[dst] = v
			}
		}
		if len(creds) == 0 {
			return nil
		}
		label := ""
		if cfg.LabelPath != "" {
			label = flat[cfg.LabelPath]
		}
		if cfg.Label != "" {
			label = cfg.Label
		}
		if label == "" {
			label = "local-" + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		d := &provider.CredentialDraft{
			Label:       label,
			Credentials: creds,
			Source:      "local_import:" + path,
		}
		if cfg.ExpiresAtPath != "" {
			d.ExpiresAt = parseInt64(flat[cfg.ExpiresAtPath])
		}
		return d
	}

	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil
	}

	creds := make(map[string]string, len(cfg.Credentials))
	for dst, src := range cfg.Credentials {
		var v string
		if strings.TrimSpace(src) == "" {
			v = strings.TrimSpace(string(raw))
		} else {
			v = lookupString(root, src)
		}
		if v != "" {
			creds[dst] = v
		}
	}
	if len(creds) == 0 {
		return nil
	}

	label := ""
	if cfg.LabelPath != "" {
		label = lookupString(root, cfg.LabelPath)
	}
	if cfg.Label != "" {
		label = cfg.Label
	}
	if label == "" {
		label = "local-" + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	d := &provider.CredentialDraft{
		Label:       label,
		Credentials: creds,
		Source:      "local_import:" + path,
	}
	if cfg.ExpiresAtPath != "" {
		d.ExpiresAt = parseInt64(lookup(root, cfg.ExpiresAtPath))
	}
	return d
}

// parseTOMLFlat 把 TOML 解析成 "section.key -> value" 的扁平表。
//
// 只覆盖 Codex / 常见 CLI 配置用到的简单结构：表头、字符串、布尔、数字。
// 不解析数组/多行字符串/嵌套表——这些字段通常不是凭证，忽略不会影响本机导入。
func parseTOMLFlat(raw []byte) map[string]string {
	flat := make(map[string]string, 32)
	section := ""

	for _, line := range strings.Split(string(raw), "\n") {
		line = stripTOMLComment(line)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.Trim(line, "[]"))
			continue
		}

		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		key = strings.Trim(key, `"'`)
		val := strings.TrimSpace(line[eq+1:])
		val = strings.Trim(val, `"'`)

		if section != "" {
			flat[section+"."+key] = val
		}
		if _, exists := flat[key]; !exists {
			flat[key] = val
		}
	}
	return flat
}

// stripTOMLComment 去掉引号外的 # 注释。
func stripTOMLComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '#':
			return line[:i]
		}
	}
	return line
}

// expandLocalPath 展开 ~、$VAR 与 %VAR%，并支持 glob。
func expandLocalPath(raw string) []string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return nil
	}

	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), string(os.PathSeparator)))
		}
	}
	p = os.ExpandEnv(p)
	p = expandWindowsVars(p)

	if strings.ContainsAny(p, "*?[") {
		matches, err := filepath.Glob(p)
		if err != nil {
			return nil
		}
		return matches
	}
	return []string{p}
}

// expandWindowsVars 处理 %APPDATA% 这类 Windows 风格变量。
func expandWindowsVars(s string) string {
	for {
		start := strings.IndexByte(s, '%')
		if start < 0 {
			return s
		}
		end := strings.IndexByte(s[start+1:], '%')
		if end < 0 {
			return s
		}
		end += start + 1
		name := s[start+1 : end]
		val := os.Getenv(name)
		if val == "" {
			// 变量不存在时保持原样，便于错误提示里看出是哪个变量没解析。
			return s
		}
		s = s[:start] + val + s[end+1:]
	}
}

// parseInt64 兼容 Unix 秒 / 毫秒、数字字符串与 RFC3339。
func parseInt64(v any) int64 {
	switch x := v.(type) {
	case nil:
		return 0
	case float64:
		return normalizeUnix(int64(x))
	case int64:
		return normalizeUnix(x)
	case json.Number:
		n, _ := x.Int64()
		return normalizeUnix(n)
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return normalizeUnix(n)
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.Unix()
		}
		if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
			return t.Unix()
		}
	}
	return 0
}

func normalizeUnix(v int64) int64 {
	// 13 位视为毫秒。
	if v > 1e12 {
		return v / 1000
	}
	return v
}
