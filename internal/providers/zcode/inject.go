package zcode

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Manifest 在 reverse 渠道基础上补充「一键切号」与「额度读取」能力。
//
// 额度要在这里显式打开：reverse 用 Profile.QuotaKind != "" 推 Capabilities.Quota，
// 而 ZCode 的额度是本包自己的实现（且只对 OAuth JWT 模式生效）。
// 打开后界面才会去读；API Key 模式的账号由 QueryQuota 返回 nil 静默跳过。
func (p *Provider) Manifest() provider.ProviderManifest {
	m := p.Provider.Manifest()
	m.Capabilities.LocalInject = true
	m.Capabilities.Quota = true
	return m
}

// InjectLocalAccount 把 API Key 写回本机 ZCode 凭据里对应区域的
// coding plan 条目；其他条目（OAuth、其他区域、项目定位）原样保留。
func (p *Provider) InjectLocalAccount(ctx context.Context, env *provider.AccountEnv) (*provider.InjectResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(env.GetCredential("api_key"))
	if apiKey == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"账号缺少 API Key：先重新登录或粘贴 Key，再写入本机 ZCode").WithProvider(p.id)
	}
	path, err := credentialsPath()
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "找不到用户目录").WithProvider(p.id)
	}
	store, err := readCredentials(path)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount,
			"本机没有 ZCode 登录态：请先启动 ZCode 并登录对应区域，再回来切号").WithProvider(p.id)
	}
	target := ""
	for key := range store {
		if strings.HasPrefix(key, codingPlanPrefix) && strings.HasSuffix(key, apiKeySuffix) &&
			regionOfKey(key) == p.region {
			target = key
			break
		}
	}
	if target == "" {
		return nil, ir.NewAPIErrorf(ir.ErrCodeNoAccount,
			"本机 ZCode 没有 %s 区域的登录态；请先在客户端登录一次", p.plan).WithProvider(p.id)
	}

	backup := path + ".aether.bak"
	if err := copyFile(path, backup); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "备份 ZCode 凭据失败，已中止写入："+err.Error()).
			WithProvider(p.id)
	}
	sealed, err := encryptCredential(apiKey)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "加密 ZCode 凭据失败："+err.Error()).WithProvider(p.id)
	}
	store[target] = sealed
	raw, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(path, raw); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "写入 ZCode 凭据失败："+err.Error()).WithProvider(p.id)
	}
	return &provider.InjectResult{
		Target:      "file:" + path,
		Files:       []string{backup},
		RestartHint: "重启 ZCode 客户端后生效。",
	}, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".zcode-creds-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

var _ provider.LocalInjectCapability = (*Provider)(nil)
