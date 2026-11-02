package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aether-cockpit/internal/core/clients"
)

// 一键启动前的接入：把「本网关」写进客户端的正常用户配置。
//
// Claude Code → ~/.claude/settings.json（env + modelPicker）
// Codex CLI / Codex 桌面端 → ~/.codex/config.toml（aether provider）
// Claude 桌面端 → %APPDATA%\Claude\claude_desktop_config.json 的 enterpriseConfig
//
// 全部先备份再原子写入；桌面端若已在运行，配置要重启后才会重新读取。

const localGatewayKey = "aether-local"

// launcherOriginKey 返回客户端应写入的网关地址与密钥。
func (g *Gateway) launcherOriginKey(r *http.Request) (string, string) {
	origin := requestOrigin(r, g.PublicAddr())
	key := strings.TrimSpace(g.cfg.APIKey)
	if key == "" {
		key = localGatewayKey
	}
	return origin, key
}

// launcherModelOptions 取网关当前可选的模型（最多 12 个），用于桌面端
// 的模型清单与 CLI 的默认模型。
func (g *Gateway) launcherModelOptions() []clients.ModelOption {
	opts := g.modelOptions()
	out := make([]clients.ModelOption, 0, 12)
	seen := map[string]bool{}
	for _, o := range opts {
		v := strings.TrimSpace(o.Value)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, clients.ModelOption{Value: v, Label: o.Label, Description: o.Note})
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func firstModelValue(opts []clients.ModelOption) string {
	if len(opts) == 0 {
		return ""
	}
	return opts[0].Value
}

// preferredModelValue 在模型清单里挑一个更贴合客户端的默认值：
// Claude 优先 claude，Codex 优先 codex / gpt，避免把 Gemini 写进 Codex。
func preferredModelValue(opts []clients.ModelOption, needles ...string) string {
	for _, needle := range needles {
		for _, o := range opts {
			hay := strings.ToLower(o.Value + " " + o.Label)
			if strings.Contains(hay, strings.ToLower(needle)) {
				return o.Value
			}
		}
	}
	return firstModelValue(opts)
}

// applyClaudeCodeGateway 把网关写入 Claude Code 的正常 settings.json，
// 并返回写入路径。启动终端时仍会叠加进程级环境变量，双重保险。
func (g *Gateway) applyClaudeCodeGateway(origin, key string) (string, error) {
	mgr := g.clientsManager()
	if mgr == nil {
		return "", fmt.Errorf("客户端配置管理不可用")
	}
	opts := g.launcherModelOptions()
	if _, err := mgr.Apply("claude_code", clients.ApplyParams{
		BaseURL: origin, APIKey: key, Model: preferredModelValue(opts, "claude-sonnet", "claude"), ModelOptions: opts,
	}); err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// applyCodexGateway 把网关写入 ~/.codex/config.toml。桌面端无法继承
// 网关进程的 AETHER_API_KEY，所以直接写 experimental_bearer_token。
func (g *Gateway) applyCodexGateway(origin, key string) (string, error) {
	mgr := g.clientsManager()
	if mgr == nil {
		return "", fmt.Errorf("客户端配置管理不可用")
	}
	opts := g.launcherModelOptions()
	if _, err := mgr.Apply("codex_cli", clients.ApplyParams{
		BaseURL: origin, APIKey: key, Model: preferredModelValue(opts, "gpt-5.3-codex", "codex", "gpt"),
		ModelOptions: opts, CodexBearerToken: key,
	}); err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "config.toml"), nil
}

// applyClaudeDesktopGateway 改写 Claude 桌面端正常配置里的 enterpriseConfig。
// 只动 enterpriseConfig，preferences / mcpServers 等原样保留。
func (g *Gateway) applyClaudeDesktopGateway(origin, key string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("无法定位用户配置目录: %w", err)
	}
	path := filepath.Join(base, "Claude", "claude_desktop_config.json")

	root := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&root); err != nil {
			return "", fmt.Errorf("现有 Claude 配置不是合法 JSON，未改写: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	ec, _ := root["enterpriseConfig"].(map[string]any)
	if ec == nil {
		ec = map[string]any{}
	}
	ec["inferenceProvider"] = "gateway"
	ec["inferenceGatewayBaseUrl"] = strings.TrimRight(origin, "/")
	ec["inferenceGatewayApiKey"] = key
	ec["inferenceGatewayAuthScheme"] = "bearer"
	if opts := g.launcherModelOptions(); len(opts) > 0 {
		rows := make([]any, 0, len(opts))
		for _, m := range opts {
			name := m.Value
			label := strings.TrimSpace(m.Label)
			if label == "" {
				label = name
			}
			rows = append(rows, map[string]any{
				"name": name, "displayName": label,
				"supports1m": false, "contextWindow": 200000, "maxInputTokens": 200000,
			})
		}
		ec["inferenceModels"] = rows
	}
	root["enterpriseConfig"] = ec

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	out = append(out, '\n')
	if err := backupLauncherFile(path); err != nil {
		return "", fmt.Errorf("备份失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp := path + ".aether-tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// backupLauncherFile 与客户端注入保持同一约定：同目录 .aether-bak-时间戳。
func backupLauncherFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	bak := path + ".aether-bak-" + time.Now().Format("20060102-150405")
	if err := os.WriteFile(bak, raw, 0o600); err != nil {
		return err
	}
	matches, _ := filepath.Glob(path + ".aether-bak-*")
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	for i := 5; i < len(matches); i++ {
		_ = os.Remove(matches[i])
	}
	return nil
}
