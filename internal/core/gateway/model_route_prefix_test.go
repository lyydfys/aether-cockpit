package gateway

import (
	"context"
	"sync"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 前缀渠道优先于裸名别名：cursor/auto 这类"非聊天渠道 + 通用模型名"的
// 组合，过去会被归一化成 auto 路由到别的渠道，自检报"无法路由回自己的渠道"。
type routeChatStub struct{}

func (routeChatStub) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:     "route-chat-stub",
		Models: []ir.ModelMeta{{ID: "zz-alias-token", UpstreamModel: "zz-alias-token"}},
	}
}

func (routeChatStub) ChatStream(context.Context, *provider.AccountEnv, *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	return nil, nil
}

type routePinStub struct{}

func (routePinStub) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:     "route-pin-stub",
		Models: []ir.ModelMeta{{ID: "route-pin-stub/zz-alias-token", UpstreamModel: "zz-alias-token"}},
	}
}

var routeStubsOnce sync.Once

func TestResolvePrefixedModelBeatsBareAlias(t *testing.T) {
	routeStubsOnce.Do(func() {
		provider.Register(routeChatStub{})
		provider.Register(routePinStub{})
	})
	r := NewModelRouter()
	r.Rebuild()
	got := r.Resolve("route-pin-stub/zz-alias-token")
	if len(got) != 1 || got[0].ProviderID != "route-pin-stub" {
		t.Fatalf("显式前缀未优先: %+v", got)
	}
}
