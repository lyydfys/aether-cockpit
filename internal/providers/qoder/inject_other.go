//go:build !windows

package qoder

import (
	"context"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func (p *Provider) InjectLocalAccount(ctx context.Context, env *provider.AccountEnv) (*provider.InjectResult, error) {
	return nil, ir.NewAPIError(ir.ErrCodeUnsupported,
		"当前平台暂不支持写入 Qoder 桌面登录态").WithProvider(p.profile.id)
}

var _ provider.LocalInjectCapability = (*Provider)(nil)
