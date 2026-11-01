// Package zcode 接入 ZCode 编程助手（Z.ai / BigModel Coding Plan）。
//
// ZCode 是 z.ai 开源客户端（github.com/zai-org/ZCode）；它的模型调用复用
// Z.ai / BigModel 的 Coding Plan 端点（OpenAI 兼容），与 zcode 本机
// `~/.zcode/v2/credentials.json` 里的 `account-provider:coding-plan:...:api-key`
// 是同一把 Key。因此这里把它实现为两个订阅渠道：
//   - zcode-cn    → https://open.bigmodel.cn/api/coding/paas/v4
//   - zcode       → https://api.z.ai/api/coding/paas/v4
//
// 本机导入直接读 credentials.json（明文 JSON）；一键切号会把对应区域的
// Key 写回同一位置，其余条目原样保留。配额接口未接入，Manifest 不宣称。
package zcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/reverse"
)

const credentialsRel = ".zcode/v2/credentials.json"

// providerPrefix 是 credentials.json 里 coding plan Key 的公共前缀/后缀。
const (
	codingPlanPrefix = "account-provider:coding-plan:"
	apiKeySuffix     = ":api-key"
)

func init() {
	provider.Register(newProvider("zcode-cn", "cn",
		"https://open.bigmodel.cn/api/coding/paas/v4", "BigModel"))
	provider.Register(newProvider("zcode", "intl",
		"https://api.z.ai/api/coding/paas/v4", "Z.ai"))
}

// Provider 在 reverse 引擎上加一层 ZCode 本机登录态读写。
type Provider struct {
	*reverse.Provider
	id      string
	region  string // cn | intl
	baseURL string
	plan    string // 展示名里的套餐归属
}

func newProvider(id, region, baseURL, plan string) *Provider {
	regionTag := "GLOBAL"
	display := "ZCode (Z.ai)"
	if region == "cn" {
		regionTag = "CN"
		display = "ZCode (BigModel)"
	}
	p := &Provider{id: id, region: region, baseURL: baseURL, plan: plan}
	p.Provider = reverse.New(reverse.Profile{
		ID:            id,
		DisplayName:   display,
		Family:        "zcode",
		RegionTag:     regionTag,
		BrandColor:    "#3859FF",
		Description:   "ZCode 编程助手订阅（" + plan + " Coding Plan）：读取本机 ~/.zcode/v2 登录态或手动粘贴 API Key，对话走 Coding Plan 的 OpenAI 兼容端点；不支持「一键切号」以外的客户端管理。",
		NetworkPolicy: provider.NetPolicyProxyFirst,
		Auths: []provider.AuthMethod{
			provider.AuthLocalImport,
			provider.AuthManualInput,
		},
		DefaultBaseURL:  baseURL,
		DefaultChatPath: "/chat/completions",
		DefaultModels:   []string{"glm-5.3", "glm-5.2"},
	})
	return p
}

// credentialsPath 返回本机 ZCode 凭据文件路径。
func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, credentialsRel), nil
}

// readCredentials 读取凭据文件的字符串键值表。
func readCredentials(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	store := map[string]string{}
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, err
	}
	return store, nil
}

// regionOfKey 从 Key 名判断归属区域。
func regionOfKey(key string) string {
	lower := strings.ToLower(key)
	switch {
	case strings.Contains(lower, "bigmodel"):
		return "cn"
	case strings.Contains(lower, "z.ai"), strings.Contains(lower, "zai"):
		return "intl"
	}
	return ""
}

// projectOfKey 取 `...:account:<project>:api-key` 里的 project 段。
func projectOfKey(key string) string {
	parts := strings.Split(strings.TrimSuffix(key, apiKeySuffix), ":")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}

// userLabel 从 oauth:zai:user_info 里取一个可展示的账号名。
func userLabel(store map[string]string) string {
	raw := strings.TrimSpace(store["oauth:zai:user_info"])
	if raw == "" {
		return ""
	}
	var info map[string]any
	if json.Unmarshal([]byte(raw), &info) != nil {
		return ""
	}
	for _, k := range []string{"email", "name", "preferred_username", "username"} {
		if v, ok := info[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// DiscoverLocalAccounts 扫描 ~/.zcode/v2/credentials.json，按区域返回 coding plan Key。
func (p *Provider) DiscoverLocalAccounts(ctx context.Context) ([]*provider.CredentialDraft, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := credentialsPath()
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount, "找不到用户目录").WithProvider(p.id)
	}
	store, err := readCredentials(path)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount,
			"没有找到 ZCode 的本机登录态（~/.zcode/v2/credentials.json）；请先登录 ZCode 客户端，或手动粘贴 API Key").
			WithProvider(p.id)
	}

	var drafts []*provider.CredentialDraft
	var decryptErr error
	for key, value := range store {
		if !strings.HasPrefix(key, codingPlanPrefix) || !strings.HasSuffix(key, apiKeySuffix) {
			continue
		}
		if regionOfKey(key) != p.region || strings.TrimSpace(value) == "" {
			continue
		}
		plain, err := decryptCredential(strings.TrimSpace(value))
		if err != nil {
			decryptErr = err
			continue
		}
		label := userLabel(store)
		if label == "" {
			label = projectOfKey(key)
		}
		if label == "" {
			label = p.plan
		}
		drafts = append(drafts, &provider.CredentialDraft{
			Label: "ZCode · " + label,
			Credentials: map[string]string{
				"api_key":  plain,
				"base_url": p.baseURL,
			},
			Source: "local_import:" + path + "#" + key,
		})
	}
	if len(drafts) == 0 {
		if decryptErr != nil {
			return nil, ir.NewAPIError(ir.ErrCodeNoAccount,
				"ZCode 本机凭据解密失败："+decryptErr.Error()+"（若设置过 ZCODE_CREDENTIAL_SECRET，请保持同一值）").
				WithProvider(p.id)
		}
		return nil, ir.NewAPIErrorf(ir.ErrCodeNoAccount,
			"ZCode 本机登录态里没有 %s 区域的 Coding Plan Key；请确认客户端已登录对应区域", p.plan).
			WithProvider(p.id)
	}
	return drafts, nil
}

// 编译期确认能力实现。
var (
	_ provider.BaseProvider          = (*Provider)(nil)
	_ provider.ChatCapability        = (*Provider)(nil)
	_ provider.LocalImportCapability = (*Provider)(nil)
)
