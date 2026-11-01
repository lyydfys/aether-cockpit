// Package zed 提供 Zed 编辑器的账号接入与额度读取。
//
// 登录走 Zed 官方 native_app_signin：本地生成 RSA 密钥并监听回环端口，
// 浏览器授权后回调里带加密 access_token，本机解密后保存。
// 对话协议未验证，不声明 Chat 能力。
package zed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const channelID = "zed"

// zedCloudURL 可被测试替换。
var zedCloudURL = "https://cloud.zed.dev"

func init() {
	provider.Register(&Provider{})
}

// Provider 实现 Zed 渠道。
type Provider struct{}

func (p *Provider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:            channelID,
		DisplayName:   "Zed",
		Family:        "zed",
		RegionTag:     "GLOBAL",
		BrandColor:    "#084CCF",
		Description:   "Zed 编辑器：支持官方 native_app_signin 登录（RSA 回环回调）与手动粘贴 user_id/access_token；可读取 cloud.zed.dev 额度。对话协议尚未接线。",
		NetworkPolicy: provider.NetPolicyProxyFirst,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthManualInput,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{Key: "user_id", Label: "User ID", Type: "text", Required: true, Help: "zed.dev 账号 ID；登录授权会自动写入"},
			{Key: "access_token", Label: "Access Token", Type: "password", Required: true},
		},
		Capabilities: provider.CapabilitySet{
			Quota: true,
		},
	}
}

// QueryQuota 读取 cloud.zed.dev/client/users/me。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	userID := strings.TrimSpace(env.GetCredential("user_id"))
	token := strings.TrimSpace(env.GetCredential("access_token"))
	if userID == "" || token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Zed 账号缺少 user_id/access_token").WithProvider(channelID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, zedCloudURL+"/client/users/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", userID+" "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "读取 Zed 额度失败: "+err.Error()).
			WithProvider(channelID).WithRetryable(true)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Zed 额度接口返回 %d", resp.StatusCode).WithProvider(channelID)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Zed 额度响应不是合法 JSON").WithProvider(channelID)
	}
	return quotaFromUser(root), nil
}

func quotaFromUser(root map[string]any) *provider.QuotaStatus {
	status := &provider.QuotaStatus{ReadAt: time.Now().Unix(), Unit: "%"}
	if plan, ok := root["plan"].(string); ok {
		status.Plan = strings.TrimSpace(plan)
	}
	if plan, ok := root["plan"].(map[string]any); ok {
		if name, ok := plan["name"].(string); ok {
			status.Plan = strings.TrimSpace(name)
		}
	}
	usage, _ := root["current_usage"].(map[string]any)
	if usage == nil {
		usage, _ = root["usage"].(map[string]any)
	}
	window := func(name string, obj map[string]any, usedKeys, limitKeys []string) {
		used, okU := pickNumber(obj, usedKeys)
		limit, okL := pickNumber(obj, limitKeys)
		if !okU || !okL || limit <= 0 {
			return
		}
		pct := used / limit * 100
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		status.Windows = append(status.Windows, provider.QuotaWindow{
			Name: name, Used: pct, Amount: int64(used), Limit: int64(limit), Unit: "tokens",
		})
	}
	if spend, ok := usage["token_spend"].(map[string]any); ok {
		window("Token spend", spend, []string{"used"}, []string{"limit"})
	}
	if ep, ok := usage["edit_predictions"].(map[string]any); ok {
		window("Edit predictions", ep, []string{"used"}, []string{"limit"})
	}
	if status.Windows != nil {
		status.Total = 100
		status.Used = int(status.Windows[0].Used)
		status.Remaining = 100 - status.Used
	} else {
		status.Message = "Zed 额度接口没有返回可读窗口"
	}
	return status
}

func pickNumber(m map[string]any, keys []string) (float64, bool) {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return v, true
		case string:
			var f float64
			if _, err := fmt.Sscanf(strings.TrimSpace(v), "%f", &f); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

var (
	_ provider.BaseProvider    = (*Provider)(nil)
	_ provider.QuotaCapability = (*Provider)(nil)
)
