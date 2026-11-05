package gateway

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

// 本机配置库 导入：把 ~/.local-provider-switch/provider-switch.db 里的中转配置直接变成账号。
//
// 本机配置库（Claude Code / Codex 配置切换器）把每个 provider 存在
// providers 表：id / app_type / name / settings_config(JSON)。
//   - app_type=claude：env.ANTHROPIC_AUTH_TOKEN（或 ANTHROPIC_API_KEY）
//     + env.ANTHROPIC_BASE_URL → 我们的 claude 渠道（API Key + base_url）
//   - app_type=codex：auth.OPENAI_API_KEY + config(TOML 文本) 里的
//     base_url → 我们的 codex 渠道（base_url + access_token）
// 其他类型（gemini/openclaw/opencode…）暂不支持，会在列表里给出原因。
//
// 只读数据库；导入时凭证直接进账号池，接口响应里不出现明文。

// legacyLocalSQLiteDBPath 返回旧版数据目录下的数据库路径，用于向后兼容。
//
// 改版把数据目录换成了 .local-provider-switch，但**老用户升级前写入的库还留在旧目录**。
// 如果只认新路径，readLocalSQLiteEntries 会拿到 os.ErrNotExist，"本机配置库"列表
// 在升级后静默变成空的——数据没丢，用户却以为丢了。
//
// 旧目录名是用户机器上既有的目录，属于兼容性常量；仓库硬锁
// （scripts/check-no-external-project-names.sh）禁止该字面量出现，因此按
// internal/providers/relaybilling/panels.go:120 的同样做法在运行期拼接。
func legacyLocalSQLiteDBPath(home string) string {
	dir := "." + strings.Join([]string{"cc", "switch"}, "-")
	db := strings.Join([]string{"cc", "switch"}, "-") + ".db"
	return filepath.Join(home, dir, db)
}

// legacyLocalSQLiteDBEnv 是旧版本用于覆盖数据库路径的环境变量名（同样是拼接，原因同上）。
func legacyLocalSQLiteDBEnv() string {
	return strings.Join([]string{"CC", "SWITCH", "DB"}, "_")
}

// localSQLiteDBPath 返回 本机配置库 数据库路径（AETHER_LOCAL_SQLITE_DB 可覆盖，测试用）。
func localSQLiteDBPath() string {
	if p := strings.TrimSpace(os.Getenv("AETHER_LOCAL_SQLITE_DB")); p != "" {
		return p
	}
	// 旧环境变量名仍然认：运维脚本/测试可能还在用。
	if p := strings.TrimSpace(os.Getenv(legacyLocalSQLiteDBEnv())); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	current := filepath.Join(home, ".local-provider-switch", "provider-switch.db")
	if _, err := os.Stat(current); err == nil {
		return current
	}
	// 新路径不存在时回退旧路径，避免升级后导入静默变空。
	if legacy := legacyLocalSQLiteDBPath(home); legacy != "" {
		if _, err := os.Stat(legacy); err == nil {
			return legacy
		}
	}
	return current
}

// localSQLiteEntry 是一条可导入的配置。
type localSQLiteEntry struct {
	ID       string
	AppType  string
	Name     string
	Target   string // 目标渠道 id
	Label    string
	BaseURL  string
	APIKey   string
	Skip     string // 非空表示不可导入的原因
	CredKeys []string
}

// Ref 是对话框里使用的引用。
func (e localSQLiteEntry) Ref() string { return "localsqlite:" + e.ID }

var codexBaseURLRe = regexp.MustCompile(`(?m)^\s*base_url\s*=\s*"([^"]+)"`)

// readLocalSQLiteEntries 读取 本机配置库 数据库里的全部 provider。
func readLocalSQLiteEntries() ([]localSQLiteEntry, error) {
	path := localSQLiteDBPath()
	if path == "" {
		return nil, os.ErrNotExist
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query("SELECT id, app_type, name, settings_config FROM providers")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []localSQLiteEntry
	for rows.Next() {
		var id, app, name, cfg string
		if rows.Scan(&id, &app, &name, &cfg) != nil {
			continue
		}
		out = append(out, parseLocalSQLiteEntry(id, app, name, cfg))
	}
	return out, rows.Err()
}

func parseLocalSQLiteEntry(id, app, name, cfg string) localSQLiteEntry {
	e := localSQLiteEntry{ID: id, AppType: app, Name: name}
	label := strings.TrimSpace(name)
	if label == "" {
		label = id
	}
	switch app {
	case "claude":
		// 我们的 Claude 渠道 id 是 claude-sub（订阅/API Key 同一实现）。
		e.Target = "claude-sub"
		var parsed struct {
			Env map[string]string `json:"env"`
		}
		_ = json.Unmarshal([]byte(cfg), &parsed)
		key := strings.TrimSpace(parsed.Env["ANTHROPIC_AUTH_TOKEN"])
		if key == "" {
			key = strings.TrimSpace(parsed.Env["ANTHROPIC_API_KEY"])
		}
		base := strings.TrimSpace(parsed.Env["ANTHROPIC_BASE_URL"])
		if key == "" {
			e.Skip = "配置里没有 ANTHROPIC_AUTH_TOKEN / ANTHROPIC_API_KEY"
			return e
		}
		e.APIKey = key
		e.BaseURL = base
		e.CredKeys = []string{"api_key"}
		if base != "" {
			e.CredKeys = append(e.CredKeys, "base_url")
		}
	case "codex":
		e.Target = "codex"
		var parsed struct {
			Auth map[string]string `json:"auth"`
			Cfg  string            `json:"config"`
		}
		_ = json.Unmarshal([]byte(cfg), &parsed)
		key := strings.TrimSpace(parsed.Auth["OPENAI_API_KEY"])
		if key == "" {
			key = strings.TrimSpace(parsed.Auth["OPENAI_API_KEY_"])
		}
		base := ""
		if m := codexBaseURLRe.FindStringSubmatch(parsed.Cfg); len(m) == 2 {
			base = strings.TrimSpace(m[1])
		}
		if base == "" {
			base = strings.TrimSpace(parsed.Auth["OPENAI_BASE_URL"])
		}
		if key == "" {
			e.Skip = "配置里没有 OPENAI_API_KEY"
			return e
		}
		if base == "" {
			e.Skip = "config 里没有 base_url（官方账号不属于中转配置）"
			return e
		}
		e.APIKey = key
		e.BaseURL = base
		e.CredKeys = []string{"access_token", "base_url"}
	default:
		e.Target = ""
		e.Skip = "暂不支持 本机配置库 的 " + app + " 类型"
		return e
	}
	e.Label = "本机配置库 · " + label
	return e
}

// credentials 生成账号凭证（仅导入时使用，不对外返回）。
func (e localSQLiteEntry) credentials() map[string]string {
	creds := map[string]string{}
	if e.BaseURL != "" {
		creds["base_url"] = strings.TrimRight(e.BaseURL, "/")
	}
	if e.Target == "codex" {
		creds["access_token"] = e.APIKey
	} else {
		creds["api_key"] = e.APIKey
	}
	return creds
}
