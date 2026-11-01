package gateway

// Local Vault 跨工具账号迁移。
//
// local-vault 把账号详情写成 AES-256-GCM 信封，密钥在数据目录的
// secure-account-storage.key；索引文件只有邮箱/名称。我们只读该目录，
// 在内存中解密并映射成各渠道的凭证，HTTP 列表只暴露标签与指纹。

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type localVaultEntry struct {
	ProviderID  string
	Label       string
	Ref         string
	Fingerprint string
	SourcePath  string
	credentials map[string]string
}

type cockpitEnvelope struct {
	Version    int    `json:"version"`
	Kind       string `json:"kind"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

var cockpitPlatforms = []struct {
	dir        string
	providerID string
}{
	{"accounts", "antigravity"},
	{"codex_accounts", "codex"},
	{"codebuddy_accounts", "codebuddy-intl"},
	{"codebuddy_cn_accounts", "codebuddy-cn"},
	{"cursor_accounts", "cursor"},
	{"github_copilot_accounts", "github-copilot"},
	{"kiro_accounts", "kiro"},
	{"windsurf_accounts", "windsurf"},
	{"workbuddy_accounts", "workbuddy-cn"},
	{"zed_accounts", "zed"},
	{"zcode_accounts", "zcode"},
	{"trae_accounts", "trae-cn"},
	{"qoder_accounts", "qoder-cn"},
}

func localVaultRoot() string {
	for _, key := range []string{
		"AETHER_VAULT_DATA_DIR",
		"AETHER_VAULT_TEST_DATA_DIR",
		"COCKPIT_TEST_DATA_DIR",
	} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, p := range []string{
		filepath.Join(home, ".antigravity_cockpit"),
		filepath.Join(os.Getenv("APPDATA"), ".antigravity_cockpit"),
	} {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return filepath.Join(home, ".antigravity_cockpit")
}

func localVaultKey(root string) []byte {
	raw, err := os.ReadFile(filepath.Join(root, "secure-account-storage.key"))
	if err != nil {
		return nil
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil
	}
	return key
}

func cockpitDecrypt(key []byte, raw []byte) (map[string]any, bool) {
	var env cockpitEnvelope
	if json.Unmarshal(raw, &env) != nil {
		return nil, false
	}
	if env.Version != 1 || env.Algorithm != "AES-256-GCM" {
		return nil, false
	}
	nonce, err := base64.StdEncoding.DecodeString(strings.TrimSpace(env.Nonce))
	if err != nil || len(nonce) != 12 {
		return nil, false
	}
	ct, err := base64.StdEncoding.DecodeString(strings.TrimSpace(env.Ciphertext))
	if err != nil || len(ct) < 16 {
		return nil, false
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, false
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if json.Unmarshal(plain, &out) != nil {
		return nil, false
	}
	return out, true
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// cockpitCredentials 把某个平台的详情映射成我们的凭证；不支持的平台返回 nil。
func cockpitCredentials(platformDir string, m map[string]any) (string, map[string]string) {
	token := asMap(m["token"])
	tokens := asMap(m["tokens"])
	pick := func(keys ...string) string {
		if v := firstString(token, keys...); v != "" {
			return v
		}
		if v := firstString(tokens, keys...); v != "" {
			return v
		}
		return firstString(m, keys...)
	}
	label := firstString(m, "email", "name", "account_name", "user_email")
	if label == "" {
		label = firstString(m, "id", "account_id", "user_id")
	}

	switch platformDir {
	case "accounts": // Antigravity
		access := pick("access_token")
		if access == "" {
			return "", nil
		}
		creds := map[string]string{"access_token": access}
		if v := pick("refresh_token"); v != "" {
			creds["refresh_token"] = v
		}
		if v := pick("id_token"); v != "" {
			creds["id_token"] = v
		}
		if v := pick("token_type"); v != "" {
			creds["token_type"] = v
		}
		if n, ok := token["expiry_timestamp"].(float64); ok && n > 0 {
			creds["expires_at"] = time.Unix(int64(n), 0).UTC().Format(time.RFC3339)
		}
		return "antigravity", creds
	case "codex_accounts":
		access := pick("access_token")
		if access == "" {
			return "", nil
		}
		creds := map[string]string{"access_token": access, "base_url": "https://chatgpt.com/backend-api/codex"}
		if v := pick("refresh_token"); v != "" {
			creds["refresh_token"] = v
		}
		if v := pick("id_token"); v != "" {
			creds["id_token"] = v
		}
		if v := firstString(m, "account_id"); v != "" {
			creds["account_id"] = v
		}
		return "codex", creds
	case "codebuddy_accounts":
		if access := pick("access_token", "token"); access != "" {
			creds := map[string]string{"token": access}
			if v := pick("refresh_token"); v != "" {
				creds["refresh_token"] = v
			}
			return "codebuddy-intl", creds
		}
	case "codebuddy_cn_accounts":
		if access := pick("access_token", "token"); access != "" {
			creds := map[string]string{"token": access}
			if v := pick("refresh_token"); v != "" {
				creds["refresh_token"] = v
			}
			return "codebuddy-cn", creds
		}
	case "cursor_accounts":
		if access := pick("access_token", "token"); access != "" {
			creds := map[string]string{"access_token": access}
			if v := pick("refresh_token"); v != "" {
				creds["refresh_token"] = v
			}
			return "cursor", creds
		}
	case "github_copilot_accounts":
		if access := pick("access_token", "token", "api_key"); access != "" {
			return "github-copilot", map[string]string{"api_key": access}
		}
	case "kiro_accounts":
		if access := pick("access_token", "token"); access != "" {
			creds := map[string]string{"access_token": access}
			if v := pick("refresh_token"); v != "" {
				creds["refresh_token"] = v
			}
			if v := pick("profile_arn"); v != "" {
				creds["profile_arn"] = v
			}
			if v := pick("region"); v != "" {
				creds["region"] = v
			}
			return "kiro", creds
		}
	case "windsurf_accounts":
		if v := pick("auth1_token"); v != "" {
			creds := map[string]string{"auth1_token": v}
			if email := firstString(m, "email"); email != "" {
				creds["email"] = email
			}
			return "windsurf", creds
		}
	case "workbuddy_accounts":
		if access := pick("access_token", "token"); access != "" {
			creds := map[string]string{"token": access}
			if v := pick("user_id"); v != "" {
				creds["user_id"] = v
			}
			if v := pick("domain"); v != "" {
				creds["domain"] = v
			}
			return "workbuddy-cn", creds
		}
	case "zed_accounts":
		if access := pick("access_token"); access != "" {
			creds := map[string]string{"access_token": access}
			if v := pick("user_id"); v != "" {
				creds["user_id"] = v
			}
			return "zed", creds
		}
	case "zcode_accounts":
		if access := pick("access_token", "token"); access != "" {
			creds := map[string]string{"access_token": access}
			if v := pick("refresh_token"); v != "" {
				creds["refresh_token"] = v
			}
			if v := pick("base_url"); v != "" {
				creds["base_url"] = v
			}
			return "zcode", creds
		}
	case "trae_accounts":
		if access := pick("access_token", "token"); access != "" {
			creds := map[string]string{"token": access}
			if v := pick("user_id"); v != "" {
				creds["user_id"] = v
			}
			if v := pick("machine_id"); v != "" {
				creds["machine_id"] = v
			}
			return "trae-cn", creds
		}
	case "qoder_accounts":
		if access := pick("access_token", "token"); access != "" {
			creds := map[string]string{"token": access}
			if v := pick("user_id"); v != "" {
				creds["user_id"] = v
			}
			if v := pick("machine_id"); v != "" {
				creds["machine_id"] = v
			}
			return "qoder-cn", creds
		}
	}
	return "", nil
}

// localVaultEntries 扫描 local-vault 数据目录并解密账号详情。
func (g *Gateway) localVaultEntries() []localVaultEntry {
	root := localVaultRoot()
	key := localVaultKey(root)
	if key == nil {
		return nil
	}
	var out []localVaultEntry
	for _, platform := range cockpitPlatforms {
		dir := filepath.Join(root, platform.dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".json") || strings.HasSuffix(strings.ToLower(name), ".json.bak") {
				continue
			}
			path := filepath.Join(dir, name)
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			detail, ok := cockpitDecrypt(key, raw)
			if !ok {
				continue
			}
			target, creds := cockpitCredentials(platform.dir, detail)
			if target == "" || len(creds) == 0 {
				continue
			}
			label := firstString(detail, "email", "name", "account_name", "user_email")
			if label == "" {
				label = firstString(detail, "id", "account_id", "user_id")
			}
			if label == "" {
				label = strings.TrimSuffix(name, ".json")
			}
			out = append(out, localVaultEntry{
				ProviderID:  target,
				Label:       label,
				Ref:         "localvault:" + platform.dir + ":" + strings.TrimSuffix(name, ".json"),
				Fingerprint: localIdentity(target, label, func(k string) string { return creds[k] }),
				SourcePath:  path,
				credentials: creds,
			})
		}
	}
	return out
}

func (g *Gateway) localVaultEntriesFor(providerID string) []localVaultEntry {
	all := g.localVaultEntries()
	out := make([]localVaultEntry, 0, len(all))
	for _, e := range all {
		if e.ProviderID == providerID {
			out = append(out, e)
		}
	}
	return out
}
