package provider

import (
	"fmt"
	"sort"
	"sync"
)

// registry 是全局插件注册表。
//
// 主程序核心包不 import 任何具体渠道包，渠道通过 init() 自注册
// （对标 Go 标准库 database/sql 的 driver 机制）。
// 新增渠道只需在 cmd 入口加一行匿名导入，核心代码零改动。
var (
	registryMu sync.RWMutex
	providers  = make(map[string]BaseProvider, 16)
	// registerOrder 保留注册顺序，让 UI 上的渠道排列稳定可预期。
	registerOrder []string
)

// Register 供各渠道插件在 init() 中调用。
//
// 同一 ID 重复注册会 panic：这属于编译期就该暴露的装配错误，
// 静默覆盖会导致线上出现"到底加载了哪个实现"的排查噩梦。
func Register(p BaseProvider) {
	if p == nil {
		panic("provider: Register(nil)")
	}
	m := p.Manifest()
	if m.ID == "" {
		panic("provider: Manifest().ID 不能为空")
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := providers[m.ID]; dup {
		panic(fmt.Sprintf("provider: 渠道 %q 被重复注册", m.ID))
	}
	providers[m.ID] = p
	registerOrder = append(registerOrder, m.ID)
}

// Get 按 ID 获取渠道插件。
func Get(id string) (BaseProvider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := providers[id]
	return p, ok
}

// MustGet 按 ID 获取渠道插件，不存在则 panic。仅供装配阶段使用。
func MustGet(id string) BaseProvider {
	p, ok := Get(id)
	if !ok {
		panic(fmt.Sprintf("provider: 渠道 %q 未注册（检查 cmd 入口的匿名导入）", id))
	}
	return p
}

// All 返回按注册顺序排列的全部渠道。
func All() []BaseProvider {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]BaseProvider, 0, len(registerOrder))
	for _, id := range registerOrder {
		out = append(out, providers[id])
	}
	return out
}

// AllManifests 返回全部渠道的 Manifest，供 /api/providers 下发给前端。
func AllManifests() []ProviderManifest {
	ps := All()
	out := make([]ProviderManifest, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Manifest())
	}
	return out
}

// IDs 返回全部已注册渠道 ID（排序后，便于日志与断言）。
func IDs() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, len(registerOrder))
	copy(out, registerOrder)
	sort.Strings(out)
	return out
}

// ChatProviders 返回实现了对话能力的渠道。
func ChatProviders() []BaseProvider {
	out := make([]BaseProvider, 0, 8)
	for _, p := range All() {
		if _, ok := p.(ChatCapability); ok {
			out = append(out, p)
		}
	}
	return out
}

// AsChat 探测渠道是否支持对话。
func AsChat(p BaseProvider) (ChatCapability, bool) {
	c, ok := p.(ChatCapability)
	return c, ok
}

// AsCheckIn 探测渠道是否支持签到。
func AsCheckIn(p BaseProvider) (CheckInCapability, bool) {
	c, ok := p.(CheckInCapability)
	return c, ok
}

// AsQuota 探测渠道是否支持配额查询。
func AsQuota(p BaseProvider) (QuotaCapability, bool) {
	c, ok := p.(QuotaCapability)
	return c, ok
}

// AsRefresh 探测渠道是否支持凭证刷新。
func AsRefresh(p BaseProvider) (TokenRefreshCapability, bool) {
	c, ok := p.(TokenRefreshCapability)
	return c, ok
}

// AsLocalImport 探测渠道是否支持本机凭证导入。
func AsLocalImport(p BaseProvider) (LocalImportCapability, bool) {
	c, ok := p.(LocalImportCapability)
	return c, ok
}

// AsLocalInject 探测渠道是否支持把账号写回本机 IDE（一键切号）。
func AsLocalInject(p BaseProvider) (LocalInjectCapability, bool) {
	c, ok := p.(LocalInjectCapability)
	return c, ok
}
