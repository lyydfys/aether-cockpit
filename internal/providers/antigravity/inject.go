package antigravity

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 一键切号：把网关里的账号写回本机 Antigravity 的登录态。
//
// 落盘格式与官方客户端（以及 local-vault 的切号实现）一致：
//
//	{"token":{"access_token","refresh_token","token_type","expiry"},
//	 "auth_method":"consumer","id_token":"..."}
//
// 两个落点：Windows 凭据管理器 target=gemini:antigravity 为主，
// ~/.gemini/jetski-standalone-oauth-token 为回退（Standalone 2.x 在
// Keyring 不可用时读它）。写入过程不打印任何凭证内容。
const (
	credentialTarget = "gemini:antigravity"
	fallbackTokenRel = ".gemini/jetski-standalone-oauth-token"
)

// 写入钩子：单测里替换掉，避免测试真的改系统凭据管理器。
var writeCredentialFn = writeLocalCredential

// InjectLocalAccount 实现 provider.LocalInjectCapability。
func (p *Provider) InjectLocalAccount(ctx context.Context, env *provider.AccountEnv) (*provider.InjectResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	payload, err := buildInjectPayload(env.Credentials, time.Now())
	if err != nil {
		return nil, err
	}

	res := &provider.InjectResult{
		Target:      "windows-credential:" + credentialTarget,
		RestartHint: "Antigravity 只在启动时读取登录态，写入后请重启 IDE 或重新打开窗口。",
	}
	var warnings []string
	if err := writeCredentialFn(payload); err != nil {
		warnings = append(warnings, "系统凭据管理器写入失败："+err.Error())
	}
	if path, err := writeFallbackTokenFile(payload); err != nil {
		warnings = append(warnings, "回退 token 文件写入失败："+err.Error())
	} else {
		res.Files = append(res.Files, path)
	}
	if len(warnings) == 2 {
		return nil, ir.NewAPIError(ir.ErrCodeInternal,
			"写入本机 Antigravity 登录态失败："+strings.Join(warnings, "；")).
			WithProvider(env.ProviderID)
	}
	res.Warnings = warnings
	return res, nil
}

// buildInjectPayload 生成官方客户端可解析的登录态 JSON。
func buildInjectPayload(creds map[string]string, now time.Time) ([]byte, error) {
	access := strings.TrimSpace(creds["access_token"])
	if access == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"账号缺少 access_token：先刷新或重新登录，再写入本机 IDE").
			WithProvider("antigravity")
	}
	tokenType := strings.TrimSpace(creds["token_type"])
	if tokenType == "" {
		tokenType = "Bearer"
	}
	token := map[string]any{
		"access_token": access,
		"token_type":   tokenType,
	}
	if refresh := strings.TrimSpace(creds["refresh_token"]); refresh != "" {
		token["refresh_token"] = refresh
	}
	if expiry := parseExpiry(creds["expires_at"]); !expiry.IsZero() {
		token["expiry"] = expiry.Format(time.RFC3339Nano)
	} else if len(token) > 0 {
		// 没有过期时间时补一个保守值：官方客户端会把零值当成"永不过期"，
		// 之后拿到 401 也不会自动续期，正常对话会突然中断。
		token["expiry"] = now.Add(time.Hour).Format(time.RFC3339Nano)
	}

	payload := map[string]any{
		"token":       token,
		"auth_method": "consumer",
	}
	if idToken := strings.TrimSpace(creds["id_token"]); idToken != "" {
		payload["id_token"] = idToken
	}
	return json.Marshal(payload)
}

// writeFallbackTokenFile 原子写入独立版回退文件（0600），返回展示用路径。
func writeFallbackTokenFile(payload []byte) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(home, fallbackTokenRel)
	tmp, err := os.CreateTemp(dir, ".jetski-oauth-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后是无操作
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	return "~/" + filepath.ToSlash(fallbackTokenRel), nil
}

// 编译期确认能力实现。
var _ provider.LocalInjectCapability = (*Provider)(nil)
