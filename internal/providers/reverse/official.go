package reverse

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

// codexOfficialBinding 从 Codex 官方凭证解析绑定信息：
// account_id（OAuth 登录时由 id_token 的 chatgpt_account_id 写入）、
// 邮箱与套餐（从 id_token claims 读，不验签，只用于展示）。
func codexOfficialBinding(creds map[string]string) *provider.OfficialBinding {
	binding := &provider.OfficialBinding{
		Official:  creds["account_id"] != "" || creds["refresh_token"] != "",
		AccountID: strings.TrimSpace(creds["account_id"]),
		Email:     strings.TrimSpace(creds["email"]),
	}
	if claims := jwtClaims(creds["id_token"]); claims != nil {
		if binding.Email == "" {
			binding.Email, _ = claims["email"].(string)
		}
		if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
			if binding.AccountID == "" {
				if v, _ := auth["chatgpt_account_id"].(string); v != "" {
					binding.AccountID = v
				}
			}
			if v, _ := auth["chatgpt_plan_type"].(string); v != "" {
				binding.Plan = v
			}
		}
		if binding.Plan == "" {
			if v, _ := claims["chatgpt_plan_type"].(string); v != "" {
				binding.Plan = v
			}
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(creds["expires_at"])); err == nil {
		binding.ExpiresAt = t.Unix()
	}
	return binding
}

// jwtClaims 读取 JWT payload（不校验签名）。
func jwtClaims(token string) map[string]any {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims
}
