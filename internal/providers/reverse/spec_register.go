package reverse

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"

	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// 本文件让"声明式 spec 直接新增一个渠道"成为可能，不必改 Go 入口再发版。
//
// 定位（与 aether 的关键差别，务必保持）：
//   - 只允许声明 **reverse 形态**（OpenAI/Anthropic 兼容的 JSON/SSE），
//     也就是把 base_url、鉴权头、请求字段名、响应字段路径写成配置；
//   - **不允许**声明 shell / exec / 任意 HTTP 钩子。Aether 的产品前提是
//     "单二进制、零外挂进程、请求路径可审计"，一旦允许执行任意代码，
//     这个前提就不成立了——那正是本仓库不照抄 aether 插件宿主的原因。
//
// 幂等与下线：
//   - spec 是热更新的，同一份配置会被反复应用，所以用 RegisterOrReplace；
//   - spec 被删除或改了声明时，之前由本模块注册的 ID 要能下线（Unregister）。

// specProfile 是 spec.Extra 里 "reverse_profile" 的形状。
//
// 放在 Extra 而不是 spec 主结构里：Extra 本来就是"渠道私有字段"的口子，
// 加在这里不会影响已有 spec 文件的序列化与兼容性。
type specProfile struct {
	// Kind 必须显式写 "reverse"：故意要求显式，避免一个手误的 Extra
	// 就把任意 provider_id 变成可路由渠道。
	Kind string `json:"kind"`
	// DisplayName 缺省用 ProviderID。
	DisplayName string `json:"display_name,omitempty"`
	Family      string `json:"family,omitempty"`
	RegionTag   string `json:"region_tag,omitempty"`
	BrandColor  string `json:"brand_color,omitempty"`
	Description string `json:"description,omitempty"`
	// NetworkPolicy: ""（跟随全局）/ "direct" / "proxy"。
	NetworkPolicy string `json:"network_policy,omitempty"`
	// ModelListPath 是上游模型列表端点；空表示不刷新实时目录。
	ModelListPath string `json:"model_list_path,omitempty"`
	// NativeResponses 表示上游原生支持 Responses，可走 Fast-Path 直通。
	NativeResponses bool `json:"native_responses,omitempty"`
	// DefaultChatPath / DefaultResponsesPath 是 endpoints 未声明时的兜底路径。
	DefaultChatPath      string `json:"default_chat_path,omitempty"`
	DefaultResponsesPath string `json:"default_responses_path,omitempty"`
}

// builtinIDs 记录编译期就注册好的渠道 ID。
//
// 必须保护它们：spec 是用户可写的，若允许覆盖内置渠道，一个手误的
// provider_id 就能把 codex / claude 之类的实现整体换掉——那是安全事故，
// 不是配置错误。
var (
	specMu         sync.Mutex
	specRegistered = map[string]bool{}
	builtinIDs     map[string]bool
)

// init 把 spec 渠道同步逻辑注入核心。
//
// 为什么用钩子而不是让 gateway 直接 import 本包：项目约定核心包不 import
// 具体渠道包（见 provider/registry.go 顶部）。这里由渠道包把能力"注册上去"，
// 依赖方向保持不变。
func init() {
	provider.SetRegistrySnapshot(SnapshotBuiltins)
	provider.SetSpecChannelReconciler(func() {
		ReconcileSpecs(spec.Default().All())
	})
}

// SnapshotBuiltins 记录"此刻已注册的渠道都是内置的"。
//
// 必须在任何 spec 渠道注册之前调用（gateway 启动时、spec 加载之前）。
// 之所以不在 init() 里做：init 的跨包顺序只保证"被导入的包先初始化"，
// 而这里要的是"所有渠道包都注册完之后"。
func SnapshotBuiltins() {
	ids := provider.IDs()
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	specMu.Lock()
	builtinIDs = m
	specMu.Unlock()
}

// profileFromSpec 把一条 spec 转成 reverse Profile。
//
// 纯函数：不读全局、不注册，方便单测。返回错误表示"这条 spec 不该被当成渠道"。
func profileFromSpec(sp *spec.Spec) (Profile, error) {
	if sp == nil {
		return Profile{}, fmt.Errorf("spec 为空")
	}
	id := strings.TrimSpace(sp.ProviderID)
	if id == "" {
		return Profile{}, fmt.Errorf("spec 缺少 provider_id")
	}
	if len(sp.Extra) == 0 {
		return Profile{}, fmt.Errorf("spec %s 没有 extra，未声明 reverse_profile", id)
	}
	var extra struct {
		Reverse *specProfile `json:"reverse_profile"`
	}
	if err := json.Unmarshal(sp.Extra, &extra); err != nil {
		return Profile{}, fmt.Errorf("spec %s 的 extra 解析失败: %w", id, err)
	}
	if extra.Reverse == nil {
		return Profile{}, fmt.Errorf("spec %s 未声明 extra.reverse_profile", id)
	}
	if kind := strings.ToLower(strings.TrimSpace(extra.Reverse.Kind)); kind != "reverse" {
		return Profile{}, fmt.Errorf("spec %s 的 reverse_profile.kind=%q 不受支持（只允许 \"reverse\"）", id, extra.Reverse.Kind)
	}

	rp := extra.Reverse
	display := strings.TrimSpace(rp.DisplayName)
	if display == "" {
		display = id
	}
	p := Profile{
		ID:                   id,
		DisplayName:          display,
		Family:               rp.Family,
		RegionTag:            rp.RegionTag,
		BrandColor:           rp.BrandColor,
		Description:          rp.Description,
		NetworkPolicy:        networkPolicyOf(rp.NetworkPolicy),
		DefaultBaseURL:       strings.TrimSpace(sp.BaseURL),
		Headers:              cloneMap(sp.HeadersTemplate),
		Middlewares:          append([]string(nil), sp.Middlewares...),
		ModelListPath:        rp.ModelListPath,
		DefaultChatPath:      firstNonEmpty(sp.Endpoint("chat", ""), rp.DefaultChatPath),
		DefaultResponsesPath: firstNonEmpty(sp.Endpoint("responses", ""), rp.DefaultResponsesPath),
		NativeResponses:      rp.NativeResponses,
	}
	// 模型清单：spec.Models 的键是"对外名"（可带 `<渠道>/` 前缀），值是上游名。
	// Profile.DefaultModels 要的是"上游名"，Manifest 再把 ID 拼成 `<渠道>/<上游名>`。
	// 因此这里取每个映射的**值**去重后作为清单——用户在 spec 里写了映射，
	// 就说明这个模型该出现在 /v1/models 里。
	seen := map[string]bool{}
	for _, name := range sortedKeys(sp.Models) {
		up := strings.TrimSpace(sp.Models[name])
		if up == "" || seen[up] {
			continue
		}
		seen[up] = true
		p.DefaultModels = append(p.DefaultModels, up)
	}
	return p, nil
}

// RegisterFromSpec 按一条 spec 注册（或更新）一个渠道。幂等。
func RegisterFromSpec(sp *spec.Spec) error {
	p, err := profileFromSpec(sp)
	if err != nil {
		return err
	}
	specMu.Lock()
	if builtinIDs != nil && builtinIDs[p.ID] {
		specMu.Unlock()
		return fmt.Errorf("spec %s 与内置渠道同名，拒绝覆盖（内置渠道只能由代码定义）", p.ID)
	}
	specMu.Unlock()

	provider.RegisterOrReplace(New(p))

	specMu.Lock()
	specRegistered[p.ID] = true
	specMu.Unlock()
	return nil
}

// ReconcileSpecs 让"已注册的 spec 渠道集合"与给定 spec 列表一致：
// 新增/更新的注册，消失的下线。返回新增数与下线数。
func ReconcileSpecs(all []*spec.Spec) (added, removed int) {
	want := make(map[string]bool, len(all))
	var errs []string
	for _, sp := range all {
		if sp == nil {
			continue
		}
		if _, err := profileFromSpec(sp); err != nil {
			// 不是 reverse 声明（绝大多数 spec 只是"参数覆盖"）属于正常情况，
			// 不打印噪音；真正的语义错误在 RegisterFromSpec 里报。
			continue
		}
		if err := RegisterFromSpec(sp); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		want[sp.ProviderID] = true
		added++
	}

	specMu.Lock()
	gone := make([]string, 0, len(specRegistered))
	for id := range specRegistered {
		if !want[id] {
			gone = append(gone, id)
		}
	}
	sort.Strings(gone)
	specMu.Unlock()

	for _, id := range gone {
		provider.Unregister(id)
		specMu.Lock()
		delete(specRegistered, id)
		specMu.Unlock()
		removed++
	}

	if len(errs) > 0 {
		log.Printf("[WARN] spec 声明的渠道有 %d 条未能注册: %s", len(errs), strings.Join(errs, "；"))
	}
	return added, removed
}

// networkPolicyOf 把 spec 里的策略名转成 provider.NetworkPolicy。
// 未知值按"跟随全局"处理，不报错——spec 是用户手写的，宽容一点。
func networkPolicyOf(v string) provider.NetworkPolicy {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "direct":
		return provider.NetPolicyDirectForce
	case "proxy", "proxyfirst", "proxy_first":
		return provider.NetPolicyProxyFirst
	default:
		return provider.NetPolicySystem
	}
}

// cloneMap 复制一层 map，避免把 spec 里的对象直接交给 Profile
// （spec 热更新会整体替换，共享引用会出现"读到一半变了"）。
func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// firstNonEmpty 返回第一个非空串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// sortedKeys 返回排序后的键，保证 /v1/models 与 Manifest 的顺序稳定。
func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
