package githubcopilot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 一键切号：把 GitHub Token 写回 Copilot CLI 的 hosts.json。
//
// 与 DiscoverLocalAccounts 互逆：只更新 github.com 条目里的 oauth_token，
// 其余主机（企业版）与其他字段原样保留。
func (p *Provider) InjectLocalAccount(ctx context.Context, env *provider.AccountEnv) (*provider.InjectResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	token := strings.TrimSpace(env.GetCredential("github_token"))
	if token == "" {
		token = strings.TrimSpace(env.GetCredential("api_key"))
	}
	if token == "" {
		token = strings.TrimSpace(env.GetCredential("access_token"))
	}
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"账号缺少 GitHub Token：先重新登录或粘贴 Token，再写入本机 CLI").WithProvider(channelID)
	}

	path, root, err := copilotInjectFile()
	if err != nil {
		return nil, err
	}
	entry, _ := root["github.com"].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	entry["oauth_token"] = token
	if user := strings.TrimSpace(env.GetCredential("user")); user != "" {
		entry["user"] = user
	}
	root["github.com"] = entry

	raw, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "创建 Copilot 配置目录失败："+err.Error()).
			WithProvider(channelID)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hosts-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "写入 Copilot hosts.json 失败："+err.Error()).
			WithProvider(channelID)
	}
	return &provider.InjectResult{
		Target:      "file:" + path,
		RestartHint: "重启 Copilot CLI / 编辑器插件后生效。",
	}, nil
}

// copilotInjectFile 复用已有 hosts.json/apps.json（保留其他主机），
// 都没有时在 APPDATA（Windows）或 ~/.config 下新建 hosts.json。
func copilotInjectFile() (string, map[string]any, error) {
	var fallback string
	for _, path := range copilotHostsPaths() {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		root := map[string]any{}
		if json.Unmarshal(raw, &root) != nil {
			continue
		}
		if m, ok := root["github.com"].(map[string]any); ok {
			if tok, _ := m["oauth_token"].(string); strings.TrimSpace(tok) != "" {
				return path, root, nil
			}
		}
		if fallback == "" {
			fallback = path
		}
	}
	if fallback != "" {
		root := map[string]any{}
		if raw, err := os.ReadFile(fallback); err == nil {
			_ = json.Unmarshal(raw, &root)
		}
		return fallback, root, nil
	}

	if dir := strings.TrimSpace(os.Getenv("APPDATA")); dir != "" {
		return filepath.Join(dir, "github-copilot", "hosts.json"), map[string]any{}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil, ir.NewAPIError(ir.ErrCodeInternal, "找不到用户目录").WithProvider(channelID)
	}
	return filepath.Join(home, ".config", "github-copilot", "hosts.json"), map[string]any{}, nil
}

var _ provider.LocalInjectCapability = (*Provider)(nil)
