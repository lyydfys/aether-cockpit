// Package spec 是"机制与策略分离"里的策略层。
//
// 逆向渠道最怕的情况：上游把客户端从 v1.4.2 升级到 v1.5.0，
// 换了签名盐值或请求头字段，旧版本立刻全面报错。
// 如果这些参数硬编码在 Go 代码里，就只能重新编译、重新分发安装包。
//
// 本包把所有"易变参数"（base_url / 客户端版本号 / 签名盐值 / 头模板 / 模型映射）
// 抽成可在运行时热替换的 JSON。签名算法这类"稳定的机制"仍留在代码里，
// 只有喂给它的参数来自 JSON。
//
// 安全约束：远程 spec 能改变 base_url，也就意味着它能决定凭证被发往哪里。
// 因此只接受 HTTPS（localhost 除外），并在状态里明确记录来源，让用户可见。
package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SignatureRule 描述一次请求签名怎么算。
//
// 算法（机制）在代码里实现，参数（策略）在这里配置。
type SignatureRule struct {
	// Algorithm 目前支持 hmac_sha256 / md5 / sha256 / none。
	Algorithm string `json:"algorithm"`
	// SaltHex 密钥，十六进制字符串。
	SaltHex string `json:"salt_hex,omitempty"`
	// SaltPlain 密钥，明文（有些上游用可读字符串）。
	SaltPlain string `json:"salt_plain,omitempty"`
	// PayloadTemplate 待签名串的模板，支持 {{.Method}} {{.Path}} {{.Timestamp}} {{.DeviceID}} {{.Body}}
	PayloadTemplate string `json:"payload_template,omitempty"`
	// HeaderName 签名写回哪个请求头。
	HeaderName string `json:"header_name,omitempty"`
	// TimestampUnit 时间戳单位：s（默认）或 ms。
	TimestampUnit string `json:"timestamp_unit,omitempty"`
	// TimestampHeader 时间戳写回哪个头（部分上游要求时间戳也上送）。
	TimestampHeader string `json:"timestamp_header,omitempty"`
}

// OAuthSpec 是渠道的 OAuth 参数覆盖。
//
// 为什么放在 spec 里：上游把 client_id / token 端点换掉是逆向渠道的常态，
// 如果这些值写死在 Manifest 里就只能重新编译。spec 支持热更新，改 JSON 即可恢复。
type OAuthSpec struct {
	AuthorizeURL    string            `json:"authorize_url,omitempty"`
	TokenURL        string            `json:"token_url,omitempty"`
	DeviceCodeURL   string            `json:"device_code_url,omitempty"`
	ClientID        string            `json:"client_id,omitempty"`
	ClientSecret    string            `json:"client_secret,omitempty"`
	Scopes          []string          `json:"scopes,omitempty"`
	RedirectPath    string            `json:"redirect_path,omitempty"`
	ExtraAuthParams map[string]string `json:"extra_auth_params,omitempty"`
	TokenField      string            `json:"token_field,omitempty"`
	RefreshField    string            `json:"refresh_field,omitempty"`
	ExpiresField    string            `json:"expires_field,omitempty"`
}

// Clone 深拷贝。
func (o *OAuthSpec) Clone() *OAuthSpec {
	if o == nil {
		return nil
	}
	cp := *o
	if len(o.Scopes) > 0 {
		cp.Scopes = append([]string(nil), o.Scopes...)
	}
	if len(o.ExtraAuthParams) > 0 {
		cp.ExtraAuthParams = make(map[string]string, len(o.ExtraAuthParams))
		for k, v := range o.ExtraAuthParams {
			cp.ExtraAuthParams[k] = v
		}
	}
	return &cp
}

// RequestOverrides 是 spec 声明的请求参数缺省值。
//
// 用指针 + nil 表达"未声明"：只有 JSON 里真的写了这个键，
// 补偿中间件才会在客户端未显式给出该参数时填入缺省值。
type RequestOverrides struct {
	MaxOutputTokens *int     `json:"max_output_tokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
}

// Spec 是某个渠道的一整套可变参数。
type Spec struct {
	ProviderID    string `json:"provider_id"`
	SpecVersion   string `json:"spec_version"`
	BaseURL       string `json:"base_url,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
	// TLSProfile 预留：将来接 uTLS 时用，如 "chrome_124"。
	TLSProfile string `json:"tls_profile,omitempty"`
	// HeadersTemplate 支持 {{.Fingerprint.DeviceID}} 等占位符。
	HeadersTemplate map[string]string `json:"headers_template,omitempty"`
	// SignatureRule 为空表示该渠道不需要签名。
	SignatureRule *SignatureRule `json:"signature_rule,omitempty"`
	// Models 网关模型名 -> 上游模型名的覆盖映射。
	Models map[string]string `json:"models,omitempty"`
	// Endpoints 各功能的路径，如 {"chat": "/v1/chat", "checkin": "/api/sign"}。
	Endpoints map[string]string `json:"endpoints,omitempty"`
	// Middlewares 覆盖渠道 Manifest 声明的补偿中间件。
	// 抓包确认上游不支持原生 tools 时，在这里加 "xml_tool_call" 即可。
	Middlewares []string `json:"middlewares,omitempty"`
	// OAuth 覆盖/补充渠道的 OAuth 参数，供 PKCE、设备码与令牌刷新使用。
	OAuth *OAuthSpec `json:"oauth,omitempty"`
	// SystemPromptInject 渠道级系统提示词注入：追加到请求 Instructions 末尾（换行连接）。
	// 用于"某个上游必须带特定系统提示才工作"的补偿，改 spec 即生效，不用重新编译。
	SystemPromptInject string `json:"system_prompt_inject,omitempty"`
	// RequestOverrides 请求参数覆盖：仅在客户端未显式给该参数时生效（不覆盖用户意图）。
	// 由 "spec_overrides" 中间件消费（internal/core/pipeline/mw_spec_overrides.go）。
	RequestOverrides *RequestOverrides `json:"request_overrides,omitempty"`
	// Extra 渠道私有的任意字段，由插件自行解释。
	Extra json.RawMessage `json:"extra,omitempty"`

	// --- 运行时元信息（不由用户填写）---
	UpdatedAt int64  `json:"updated_at,omitempty"`
	Source    string `json:"source,omitempty"`
	// Revision 是存储内单调递增的版本号，供缓存失效判断使用。
	// 不写进 JSON：它只在进程内有效，重启后从 0 重新计数。
	Revision int64 `json:"-"`
}

// Endpoint 取某个功能的路径，缺失时返回 fallback。
func (s *Spec) Endpoint(name, fallback string) string {
	if s == nil || s.Endpoints == nil {
		return fallback
	}
	if v, ok := s.Endpoints[name]; ok && v != "" {
		return v
	}
	return fallback
}

// MapModel 按 spec 的映射表转换模型名。
func (s *Spec) MapModel(name string) string {
	if s == nil || s.Models == nil {
		return name
	}
	if v, ok := s.Models[name]; ok && v != "" {
		return v
	}
	return name
}

// Clone 深拷贝，避免热更新时旧引用被就地改写。
func (s *Spec) Clone() *Spec {
	if s == nil {
		return nil
	}
	cp := *s
	if s.HeadersTemplate != nil {
		cp.HeadersTemplate = make(map[string]string, len(s.HeadersTemplate))
		for k, v := range s.HeadersTemplate {
			cp.HeadersTemplate[k] = v
		}
	}
	if s.Models != nil {
		cp.Models = make(map[string]string, len(s.Models))
		for k, v := range s.Models {
			cp.Models[k] = v
		}
	}
	if s.Endpoints != nil {
		cp.Endpoints = make(map[string]string, len(s.Endpoints))
		for k, v := range s.Endpoints {
			cp.Endpoints[k] = v
		}
	}
	if s.SignatureRule != nil {
		r := *s.SignatureRule
		cp.SignatureRule = &r
	}
	if len(s.Middlewares) > 0 {
		cp.Middlewares = append([]string(nil), s.Middlewares...)
	}
	cp.OAuth = s.OAuth.Clone()
	if s.RequestOverrides != nil {
		ro := *s.RequestOverrides
		cp.RequestOverrides = &ro
	}
	if s.Extra != nil {
		cp.Extra = append(json.RawMessage(nil), s.Extra...)
	}
	return &cp
}

// globalRev 是全局单调递增的 spec 版本号。
//
// 用全局而不是每 Store 一个：测试或热替换 Store 时，Provider 的 Manifest 缓存
// 仍可能存活，跨 Store 的版本号必须不重复，否则会读到上一份 spec 生成的旧 Manifest。
var globalRev atomic.Int64

// Store 管理全部渠道的 spec，支持本地加载与远程热更新。
type Store struct {
	mu    sync.RWMutex
	specs map[string]*Spec

	// 配置
	dir       string
	remoteURL string

	// 运行时状态
	lastFetchAt time.Time
	lastError   string
	fetchCount  int

	// onChange 在任意一条 spec 生效后调用（用于重建路由表）。
	// 回调在锁外执行，避免回调里读取 Status 造成死锁。
	onChange func()
}

// SetOnChange 注册 spec 变更回调。
func (s *Store) SetOnChange(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = fn
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{specs: make(map[string]*Spec, 8)}
}

// --- 本地加载 ---

// LoadDir 读取目录下所有 *.json 并合并进存储。
//
// 文件名不参与解析，以 JSON 里的 provider_id 为准 —— 这样可以自由组织文件。
func (s *Store) LoadDir(dir string) error {
	s.mu.Lock()
	s.dir = dir
	s.mu.Unlock()

	if dir == "" {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// 目录不存在是正常情况（还没配置任何 spec）；
			// 但如果之前加载过本地 spec，现在整目录被删了，应同步清掉内存里的残留。
			s.pruneLocalSpecs(nil)
			return nil
		}
		return err
	}

	seen := make(map[string]bool, 8)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 %s 失败: %w", path, err)
		}
		ids, err := s.applyPayload(b, "local:"+path)
		if err != nil {
			return fmt.Errorf("解析 %s 失败: %w", path, err)
		}
		for _, id := range ids {
			seen[id] = true
		}
	}

	// 本地文件被删除时，热重载应该同时移除对应 spec；
	// 否则内存里会一直保留一份"幽灵配置"。
	s.pruneLocalSpecs(seen)
	return nil
}

// pruneLocalSpecs 删除本地来源、且不在 seen 里的 spec。
// seen 为 nil 表示清空全部本地 spec（目录被删除）。
func (s *Store) pruneLocalSpecs(seen map[string]bool) {
	s.mu.Lock()
	var removed int
	for id, sp := range s.specs {
		if !strings.HasPrefix(sp.Source, "local:") {
			continue // 远程 spec 由远程更新管理，不在这里删
		}
		if seen != nil && seen[id] {
			continue
		}
		delete(s.specs, id)
		removed++
	}
	onChange := s.onChange
	s.mu.Unlock()

	if removed > 0 && onChange != nil {
		onChange()
	}
}

// Reload 重新扫描本地目录（供"手动刷新"按钮调用）。
func (s *Store) Reload() error {
	s.mu.RLock()
	dir := s.dir
	s.mu.RUnlock()
	return s.LoadDir(dir)
}

// payload 是本地文件与远程响应的共同格式。
//
// 单个文件可以直接是 Spec 对象，也可以是 {"version":1,"specs":[...]} 的聚合格式。
type payload struct {
	Version int     `json:"version"`
	Specs   []*Spec `json:"specs"`
}

// applyPayload 解析并合并一份 spec 数据，返回其中出现的 provider_id。
func (s *Store) applyPayload(b []byte, source string) ([]string, error) {
	b = trimBOM(b)

	// 先尝试聚合格式
	var p payload
	if err := json.Unmarshal(b, &p); err == nil && len(p.Specs) > 0 {
		ids := make([]string, 0, len(p.Specs))
		for _, sp := range p.Specs {
			if sp == nil {
				continue
			}
			s.put(sp, source)
			if sp.ProviderID != "" {
				ids = append(ids, sp.ProviderID)
			}
		}
		return ids, nil
	}

	// 再尝试单个 Spec
	var single Spec
	if err := json.Unmarshal(b, &single); err != nil {
		return nil, err
	}
	if single.ProviderID == "" {
		return nil, fmt.Errorf("缺少 provider_id")
	}
	s.put(&single, source)
	return []string{single.ProviderID}, nil
}

// put 写入一条 spec。返回是否真的更新了（版本变化或首次出现）。
func (s *Store) put(sp *Spec, source string) bool {
	if sp == nil || sp.ProviderID == "" {
		return false
	}

	s.mu.Lock()
	if old, ok := s.specs[sp.ProviderID]; ok {
		// 版本相同且内容来源相同 -> 跳过，避免热更新循环里反复重建对象。
		if old.SpecVersion != "" && old.SpecVersion == sp.SpecVersion && old.Source == source {
			s.mu.Unlock()
			return false
		}
	}

	cp := sp.Clone()
	cp.Source = source
	cp.UpdatedAt = time.Now().Unix()
	cp.Revision = globalRev.Add(1)
	s.specs[cp.ProviderID] = cp
	onChange := s.onChange
	s.mu.Unlock()

	if onChange != nil {
		onChange()
	}
	return true
}

// --- 查询 ---

// Get 返回某渠道 spec 的副本；不存在返回 nil。
//
// 返回副本而不是指针：热更新时整体替换，调用方手里的旧对象不会被就地改写，
// 避免出现"读到一半字段变了"的撕裂状态。
func (s *Store) Get(providerID string) *Spec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.specs[providerID].Clone()
}

// All 返回全部 spec 的副本，按 provider_id 排序。
func (s *Store) All() []*Spec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Spec, 0, len(s.specs))
	for _, sp := range s.specs {
		out = append(out, sp.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProviderID < out[j].ProviderID })
	return out
}

// Status 是给 UI 看的运行状态。
type Status struct {
	Count       int    `json:"count"`
	Dir         string `json:"dir,omitempty"`
	RemoteURL   string `json:"remote_url,omitempty"`
	LastFetchAt int64  `json:"last_fetch_at,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	FetchCount  int    `json:"fetch_count"`
}

// Status 返回运行状态。
func (s *Store) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Status{
		Count:      len(s.specs),
		Dir:        s.dir,
		RemoteURL:  s.remoteURL,
		FetchCount: s.fetchCount,
		LastError:  s.lastError,
	}
	if !s.lastFetchAt.IsZero() {
		st.LastFetchAt = s.lastFetchAt.Unix()
	}
	return st
}

func trimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

// --- 全局默认存储 ---
//
// 渠道插件通过包级 Load() 取自己的 spec，不需要把 store 一路透传。
// spec 本质是全局配置，这样最省事；测试里可用 SetDefault 替换。

var (
	defaultMu sync.RWMutex
	defaultSt = NewStore()
)

// Default 返回全局存储。
func Default() *Store {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultSt
}

// SetDefault 替换全局存储（测试用）。
func SetDefault(s *Store) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if s == nil {
		s = NewStore()
	}
	defaultSt = s
}

// Load 从全局存储取某渠道的 spec，不存在返回 nil。
//
// 约定：调用方拿到 nil 时应当退化到内置默认值，而不是报错。
// 这样"没配置 spec"和"配置了 spec"是同一套代码路径。
func Load(providerID string) *Spec {
	return Default().Get(providerID)
}
