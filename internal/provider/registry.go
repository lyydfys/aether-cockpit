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

// RegisterOrReplace 注册渠道，同 ID 已存在时就地替换。
//
// 只给"运行期才出现的渠道"用（目前是 spec 声明的 reverse 渠道）：
// spec 是热更新的，同一份配置会被反复应用，用 Register 会 panic。
// 编译期自注册的渠道仍应走 Register——那里的重复是真装配错误，必须炸。
func RegisterOrReplace(p BaseProvider) {
	if p == nil {
		return
	}
	m := p.Manifest()
	if m.ID == "" {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := providers[m.ID]; exists {
		providers[m.ID] = p
		return
	}
	providers[m.ID] = p
	registerOrder = append(registerOrder, m.ID)
}

// Unregister 下线一个运行期注册的渠道；返回是否真的删掉了。
//
// 用于 spec 被删除/改 ID 的场景。编译期自注册的渠道被删掉后不会再回来，
// 所以调用方必须只对自己注册过的 ID 调它。
func Unregister(id string) bool {
	if id == "" {
		return false
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := providers[id]; !ok {
		return false
	}
	delete(providers, id)
	for i, v := range registerOrder {
		if v == id {
			registerOrder = append(registerOrder[:i], registerOrder[i+1:]...)
			break
		}
	}
	return true
}

// specChannelReconciler 由"能读 spec 的渠道包"在 init() 里注入。
//
// 为什么要这一层间接：按项目约定，核心包（gateway）不 import 任何具体渠道包
// （见本文件顶部注释）。但 spec 热更新后需要有人把"声明式渠道"同步进注册表，
// 这件事只有渠道包自己知道怎么做。于是核心只调用这个钩子，具体实现由
// reverse 包注入——既保持依赖方向，又不需要在 gateway 里写渠道名。
var (
	reconcilerMu            sync.RWMutex
	specChannelReconciler   func()
	builtinsSnapshotApplied bool
)

// SetSpecChannelReconciler 由渠道包在 init() 中注入 spec 渠道同步逻辑。
func SetSpecChannelReconciler(fn func()) {
	reconcilerMu.Lock()
	specChannelReconciler = fn
	reconcilerMu.Unlock()
}

// ReconcileSpecChannels 让"spec 声明的渠道"与当前 spec 集合一致。
// 没有注入实现时是空操作（例如只跑了部分渠道包的测试）。
func ReconcileSpecChannels() {
	reconcilerMu.RLock()
	fn := specChannelReconciler
	reconcilerMu.RUnlock()
	if fn != nil {
		fn()
	}
}

// SnapshotRegistry 让渠道包在加载 spec 之前记录"当前这些都是内置渠道"。
// 同样走钩子注入，理由同上。
func SnapshotRegistry() {
	reconcilerMu.RLock()
	fn := registrySnapshotFn
	reconcilerMu.RUnlock()
	if fn != nil {
		fn()
	}
	reconcilerMu.Lock()
	builtinsSnapshotApplied = true
	reconcilerMu.Unlock()
}

var registrySnapshotFn func()

// SetRegistrySnapshot 由渠道包注入"给内置渠道拍快照"的实现。
func SetRegistrySnapshot(fn func()) {
	reconcilerMu.Lock()
	if builtinsSnapshotApplied {
		// 已经拍过快照的进程不再接受新的拍快照实现，避免把 spec 渠道误当内置。
		reconcilerMu.Unlock()
		return
	}
	registrySnapshotFn = fn
	reconcilerMu.Unlock()
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
