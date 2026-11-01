// Package cursor 提供 Cursor 编辑器的账号接入与额度读取（对齐 cockpit-tools）。
//
// 只读 Cursor 的 state.vscdb（VS Code 全局存储）里的 cursorAuth/* 登录态；
// 额度走 https://cursor.com/api/usage-summary（WorkosCursorSessionToken Cookie）。
// 对话转发未接线：Cursor 的 chat 是 connect/protobuf 协议，未验证前不宣称可用。
package cursor

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const channelID = "cursor"

// cursorUsageURL 可被测试替换。
var cursorUsageURL = "https://cursor.com/api/usage-summary"

func init() {
	provider.Register(&Provider{})
}

// Provider 实现 Cursor 渠道。
type Provider struct{}

func (p *Provider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:            channelID,
		DisplayName:   "Cursor",
		Family:        "cursor",
		RegionTag:     "GLOBAL",
		BrandColor:    "#000000",
		Description:   "Cursor 编辑器订阅：一键导入本机 Cursor 的登录态（state.vscdb），读取 Pro/Free 用量；也支持手动粘贴 access_token。对话转发尚未接线（避免假可用）。",
		NetworkPolicy: provider.NetPolicyProxyFirst,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthLocalImport,
			provider.AuthManualInput,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{
				Key: "access_token", Label: "Access Token", Type: "password", Required: true,
				Placeholder: "Cursor JWT（eyJ...）",
				Help:        "从 Cursor 的 state.vscdb 的 cursorAuth/accessToken 读取；本机导入会自动填好",
			},
			{Key: "refresh_token", Label: "Refresh Token（可选）", Type: "password", Help: "用于后续续期；本机导入会自动填好"},
		},
		Models: []ir.ModelMeta{
			{ID: channelID + "/auto", UpstreamModel: "auto", DisplayName: "Cursor Auto", ContextWindow: 200000,
				Capabilities: ir.ModelCapabilities{Streaming: true}},
		},
		Capabilities: provider.CapabilitySet{
			LocalImport: true,
			LocalInject: true,
			Quota:       true,
		},
	}
}

// cursorStateDBPath 返回 Cursor 全局存储数据库路径。
func cursorStateDBPath() string {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "Cursor", "User", "globalStorage", "state.vscdb")
	}
	return filepath.Join(appdata, "Cursor", "User", "globalStorage", "state.vscdb")
}

// readCursorAuth 从 state.vscdb 读取 cursorAuth 登录态。
func readCursorAuth(dbPath string) (*provider.CredentialDraft, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(dbPath) + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	get := func(key string) string {
		var v string
		err := db.QueryRow("SELECT value FROM ItemTable WHERE key = ?1", key).Scan(&v)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(v)
	}
	access := get("cursorAuth/accessToken")
	if access == "" {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount, "Cursor 本机数据库里没有 cursorAuth/accessToken（未登录或版本不支持）").WithProvider(channelID)
	}
	email := get("cursorAuth/cachedEmail")
	refresh := get("cursorAuth/refreshToken")
	authID := get("cursorAuth/authId")
	member := get("cursorAuth/stripeMembershipType")
	if authID == "" {
		authID = jwtSubject(access)
	}
	label := "Cursor"
	if email != "" {
		label += " (" + email + ")"
	}
	creds := map[string]string{
		"access_token": access,
		"email":        email,
		"auth_id":      authID,
	}
	if refresh != "" {
		creds["refresh_token"] = refresh
	}
	if member != "" {
		creds["membership"] = member
	}
	return &provider.CredentialDraft{
		Label: label, Credentials: creds,
		Source: "local_import:" + dbPath,
	}, nil
}

func (p *Provider) DiscoverLocalAccounts(ctx context.Context) ([]*provider.CredentialDraft, error) {
	d, err := readCursorAuth(cursorStateDBPath())
	if err != nil {
		if irErr, ok := err.(*ir.APIError); ok {
			return nil, irErr
		}
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount, "没有找到 Cursor 的本机登录态；请确认 Cursor 已登录，或手动粘贴 access_token").WithProvider(channelID)
	}
	return []*provider.CredentialDraft{d}, nil
}

func jwtPayload(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func jwtSubject(token string) string {
	m := jwtPayload(token)
	if m == nil {
		return ""
	}
	sub, _ := m["sub"].(string)
	if i := strings.LastIndex(sub, "|"); i >= 0 {
		sub = sub[i+1:]
	}
	return strings.TrimSpace(sub)
}

func sessionCookie(accessToken string) string {
	uid := jwtSubject(accessToken)
	return "WorkosCursorSessionToken=" + uid + "%3A%3A" + accessToken
}

// QueryQuota 读取 Cursor usage-summary。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	token := strings.TrimSpace(env.GetCredential("access_token"))
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Cursor 账号缺少 access_token").WithProvider(channelID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cursorUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", sessionCookie(token))
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "读取 Cursor 用量失败: "+err.Error()).
			WithProvider(channelID).WithRetryable(true)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Cursor 会话已过期，请重新从本机导入").WithProvider(channelID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Cursor 用量接口返回 %d", resp.StatusCode).WithProvider(channelID)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Cursor 用量响应不是合法 JSON").WithProvider(channelID)
	}
	return quotaFromUsage(root), nil
}

// quotaFromUsage 兼容 plan / individualUsage.plan 两类结构，按百分比窗口呈现。
func quotaFromUsage(root map[string]any) *provider.QuotaStatus {
	status := &provider.QuotaStatus{Unit: "%", ReadAt: time.Now().Unix()}
	if m, _ := root["membershipType"].(string); strings.TrimSpace(m) != "" {
		status.Plan = strings.TrimSpace(m)
	}
	plan, _ := root["plan"].(map[string]any)
	if plan == nil {
		if ind, ok := root["individualUsage"].(map[string]any); ok {
			plan, _ = ind["plan"].(map[string]any)
		}
	}
	if plan == nil {
		status.Message = "Cursor 用量接口没有返回 plan 字段"
		return status
	}
	pick := func(keys ...string) float64 {
		for _, k := range keys {
			if v, ok := plan[k].(float64); ok {
				return v
			}
			if s, ok := plan[k].(string); ok {
				var f float64
				if _, err := fmt.Sscanf(strings.TrimSpace(s), "%f", &f); err == nil {
					return f
				}
			}
		}
		return -1
	}
	used := pick("used", "totalSpend", "total_spend")
	limit := pick("limit", "totalLimit", "total_limit")
	totalPct := pick("totalPercentUsed", "total_percent_used")
	autoPct := pick("autoPercentUsed", "auto_percent_used")
	apiPct := pick("apiPercentUsed", "api_percent_used")
	if totalPct < 0 && used >= 0 && limit > 0 {
		totalPct = used / limit * 100
	}
	add := func(name string, pct float64) {
		if pct < 0 {
			return
		}
		if pct > 100 {
			pct = 100
		}
		status.Windows = append(status.Windows, provider.QuotaWindow{Name: name, Used: pct, Unit: "%"})
	}
	add("Total Usage", totalPct)
	add("Auto + Composer", autoPct)
	add("API Usage", apiPct)
	if status.Windows == nil {
		status.Message = "Cursor 用量接口没有可读窗口"
		return status
	}
	status.Used = int(status.Windows[0].Used)
	status.Total = 100
	status.Remaining = 100 - status.Used
	return status
}

var (
	_ provider.BaseProvider          = (*Provider)(nil)
	_ provider.LocalImportCapability = (*Provider)(nil)
	_ provider.QuotaCapability       = (*Provider)(nil)
)
