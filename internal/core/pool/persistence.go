package pool

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aether-cockpit/internal/core/credential"
	"aether-cockpit/internal/core/simplecrypt"
	"aether-cockpit/internal/core/transport"
	"aether-cockpit/internal/provider"
)

// persistedAccount 是账号的落盘形态。凭证整体加密后以 base64 存储。
type persistedAccount struct {
	AccountID      string                     `json:"account_id"`
	ProviderID     string                     `json:"provider_id"`
	Label          string                     `json:"label,omitempty"`
	Tags           []string                   `json:"tags,omitempty"`
	Note           string                     `json:"note,omitempty"`
	NetworkPolicy  provider.NetworkPolicy     `json:"network_policy"`
	Fingerprint    provider.DeviceFingerprint `json:"fingerprint"`
	CredentialsEnc string                     `json:"credentials_enc,omitempty"`
	TLSProfile     string                     `json:"tls_profile,omitempty"`
	ProxyOverride  string                     `json:"proxy_override,omitempty"`
	Disabled       bool                       `json:"disabled,omitempty"`
	Priority       int                        `json:"priority,omitempty"`
	Weight         int                        `json:"weight,omitempty"`
	Concurrency    *int                       `json:"concurrency,omitempty"`
	AllowedModels  []string                   `json:"allowed_models,omitempty"`
	ModelMapping   map[string]string          `json:"model_mapping,omitempty"`
	// 官方通道暂停状态（仅对官方订阅账号有意义）。
	OfficialPaused      bool   `json:"official_paused,omitempty"`
	OfficialPauseReason string `json:"official_pause_reason,omitempty"`
	OfficialPausedAt    int64  `json:"official_paused_at,omitempty"`
	CreatedAt           int64  `json:"created_at"`
	// WindowCaps 是"用到窗口多少就按住"的配额闸（见 provider/account_caps.go）。
	WindowCaps provider.WindowCaps `json:"window_caps,omitempty"`
	// ModelRests 是未过期的模型级冷却（见 provider/rest.go）。
	// 只存到期时间与原因，不存失败计数——重启后从 1 档重新退避，
	// 避免"重启一次就永久高退避"。
	ModelRests []provider.ModelRest `json:"model_rests,omitempty"`
}

type accountsFile struct {
	Version  int                `json:"version"`
	SavedAt  int64              `json:"saved_at"`
	Accounts []persistedAccount `json:"accounts"`
}

// maxPreservedAccounts 是 preserved（"渠道暂时未注册、原样留在磁盘上"的记录）
// 的内存上限。
//
// preserved 只活在内存里，不会另外落盘；它的规模上限就是 accounts.json 里的账号数。
// 正常账号文件远小于这个数，所以 500 是防病态输入（被塞了几万条垃圾记录）的护栏。
// 超过上限时**不丢弃、也不回写**：直接置 blocked 让 SaveAccounts 拒写，
// 宁可这一轮不保存，也不能把用户账号删掉。
const maxPreservedAccounts = 500

// persistence 承担账号的落盘与恢复。
type persistence struct {
	mu       sync.Mutex
	path     string
	key      []byte
	migrated bool
	// blocked 在账号文件加载失败后置位，禁止空池回写覆盖原文件。
	blocked bool
	// preserved 保存"本次加载时渠道尚未注册、因此没进内存池"的账号**原始落盘记录**。
	//
	// 为什么必须留着：加载发生在 Gateway.New 里（gateway.go:581），此时自定义渠道/
	// spec 渠道可能还没注册；旧代码在这里 `continue` 丢掉该账号，而 dirty 随后被清零，
	// 于是下一次任何账号变更触发的 SaveAccounts 都会把内存池**当作全集**写回磁盘——
	// 用户的自定义渠道账号就被永久删除了（表现为"杀后台后接入的渠道全没了"）。
	// 现在这些记录原样并回，磁盘内容只增不减。
	preserved []persistedAccount
	// preservedOverflow 表示"渠道未注册的账号多到超出 maxPreservedAccounts"。
	// 这种情况下保留集不完整，回写会丢账号，因此禁止写盘。
	// 单独一个字段而不是复用 blocked：SetStorePath 加载成功后会无条件把 blocked
	// 清零，用它会被清掉。
	preservedOverflow bool
}

// SetCredentialKey enables envelope v2 for account credentials. Without a key
// (the desktop default) the existing simplecrypt format is preserved.
func (m *Manager) SetCredentialKey(key []byte) {
	if len(key) == 0 {
		return
	}
	m.persist.mu.Lock()
	m.persist.key = append([]byte(nil), key...)
	m.persist.mu.Unlock()
}

// SetStorePath 设置账号持久化路径，并立即尝试加载。
func (m *Manager) SetStorePath(path string, tr *transport.Factory) error {
	m.persist.mu.Lock()
	m.persist.path = path
	m.persist.mu.Unlock()

	if path == "" {
		return nil
	}
	if err := m.loadAccounts(tr); err != nil {
		m.persist.mu.Lock()
		m.persist.blocked = true
		m.persist.mu.Unlock()
		return err
	}
	m.persist.mu.Lock()
	m.persist.blocked = false
	m.persist.mu.Unlock()
	// 加载过程会走 Add()，把 dirty 置位；这里复位，避免启动即触发一次无意义的回写。
	atomic.StoreInt32(&m.dirty, 0)
	// 亲和表也要恢复：不然一重启所有会话都重新选号，上游 prompt cache 全部作废。
	// 失败不影响启动（亲和只是优化项）。
	_ = m.LoadSticky()
	m.persist.mu.Lock()
	migrated := m.persist.migrated
	m.persist.migrated = false
	m.persist.mu.Unlock()
	if migrated {
		return m.SaveAccounts()
	}
	return nil
}

// RebuildClients 用新的 transport 工厂重新铸造全部账号的 HTTP 客户端。
//
// 先把所有客户端建好再统一替换：任何一个账号失败都保持旧客户端不变，
// 避免"一半账号走新代理、一半走旧代理"的中间态。
func (m *Manager) RebuildClients(tr *transport.Factory) (int, error) {
	if tr == nil {
		return 0, fmt.Errorf("pool: transport 工厂为空")
	}
	envs := m.All()
	type pending struct {
		env    *provider.AccountEnv
		client *http.Client
	}
	built := make([]pending, 0, len(envs))
	for _, env := range envs {
		client, err := tr.NewClient(env.NetworkPolicy, env.Proxy(), env.TLSProfile)
		if err != nil {
			return 0, fmt.Errorf("账号 %s 重建连接失败: %w", env.AccountID, err)
		}
		built = append(built, pending{env: env, client: client})
	}
	for _, b := range built {
		b.env.SetHTTPClient(b.client)
	}
	return len(built), nil
}

// Flush 在有变更时落盘。
func (m *Manager) Flush() error {
	if atomic.LoadInt32(&m.dirty) == 0 {
		return nil
	}
	if err := m.SaveAccounts(); err != nil {
		return err
	}
	atomic.StoreInt32(&m.dirty, 0)
	return nil
}

// StartAutoSave 启动周期性落盘，返回停止函数（停止时会做最后一次 Flush 并等它写完）。
func (m *Manager) StartAutoSave(interval time.Duration) func() {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				_ = m.Flush()
				return
			case <-t.C:
				_ = m.Flush()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(stop) })
		<-done
	}
}

// SaveAccounts 把所有账号写入磁盘。
func (m *Manager) SaveAccounts() error {
	m.persist.mu.Lock()
	path := m.persist.path
	blocked := m.persist.blocked
	overflow := m.persist.preservedOverflow
	m.persist.mu.Unlock()
	if path == "" {
		return nil
	}
	if blocked {
		return fmt.Errorf("账号文件加载失败后保持只读，拒绝覆盖: %s", path)
	}
	if overflow {
		// 保留集放不下，回写必然丢账号：宁可这次不保存。
		return fmt.Errorf("渠道未注册的账号超出保留上限 %d，拒绝回写以免丢失: %s",
			maxPreservedAccounts, path)
	}

	envs := m.All()
	out := accountsFile{Version: 1, SavedAt: time.Now().Unix(), Accounts: make([]persistedAccount, 0, len(envs))}
	m.persist.mu.Lock()
	key := append([]byte(nil), m.persist.key...)
	// 加载时因渠道未注册而没能进池的账号要原样写回，否则本函数会把它们抹掉。
	preserved := append([]persistedAccount(nil), m.persist.preserved...)
	m.persist.mu.Unlock()

	for _, env := range envs {
		paused, pauseReason, pauseAt := env.OfficialPaused()
		// 只导出凭证字典，不含 HTTPClient 等运行时对象。
		creds, err := json.Marshal(env.Credentials)
		if err != nil {
			continue
		}
		var enc []byte
		if len(key) > 0 {
			enc, err = credential.Seal(key, creds, credential.AAD(env.ProviderID, env.AccountID))
		} else {
			enc, err = simplecrypt.Encrypt(creds)
		}
		if err != nil {
			continue
		}
		out.Accounts = append(out.Accounts, persistedAccount{
			AccountID:           env.AccountID,
			ProviderID:          env.ProviderID,
			Label:               env.Label,
			Tags:                env.TagsList(),
			Note:                env.Note(),
			NetworkPolicy:       env.NetworkPolicy,
			Fingerprint:         env.Fingerprint,
			CredentialsEnc:      base64.StdEncoding.EncodeToString(enc),
			TLSProfile:          env.TLSProfile,
			Disabled:            env.Disabled(),
			Priority:            env.Priority(),
			Weight:              env.Weight(),
			Concurrency:         concurrencyPtr(env),
			AllowedModels:       env.AllowedModels(),
			ModelMapping:        env.ModelMapping(),
			OfficialPaused:      paused,
			OfficialPauseReason: pauseReason,
			OfficialPausedAt:    pauseAtUnix(pauseAt),
			CreatedAt:           accountCreatedAt(env),
			WindowCaps:          env.WindowCaps(),
			ModelRests:          env.ModelRests(),
		})
	}

	// 并回"渠道未注册"的账号记录（按 account_id 去重，绝不覆盖内存池里的最新状态）。
	if len(preserved) > 0 {
		live := make(map[string]bool, len(out.Accounts))
		for _, pa := range out.Accounts {
			live[pa.AccountID] = true
		}
		for _, pa := range preserved {
			if live[pa.AccountID] {
				continue
			}
			live[pa.AccountID] = true
			out.Accounts = append(out.Accounts, pa)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadAccounts 从磁盘恢复账号。
func (m *Manager) loadAccounts(tr *transport.Factory) error {
	m.persist.mu.Lock()
	path := m.persist.path
	m.persist.mu.Unlock()

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var f accountsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("账号文件解析失败: %w", err)
	}
	m.persist.mu.Lock()
	key := append([]byte(nil), m.persist.key...)
	// 重新加载就重建保留集，避免同一进程内多次 SetStorePath 造成重复累加。
	m.persist.preserved = nil
	m.persist.preservedOverflow = false
	m.persist.mu.Unlock()
	migrated := false
	// skipped 收集"渠道未注册"的原始记录，加载结束后交给 persistence.preserved 保管。
	skipped := make([]persistedAccount, 0, 4)
	// overflow 是超出 maxPreservedAccounts 的条数：一旦非零就说明保留集放不下，
	// 此时必须禁止回写（见下面的 blocked 置位），否则那些账号会被抹掉。
	overflow := 0

	for _, pa := range f.Accounts {
		if pa.ProviderID == "" || pa.AccountID == "" {
			continue
		}
		// 渠道可能已下线（插件被移除），或还没注册（自定义渠道 / spec 渠道可能在本函数
		// 所在流程之后才 reconcile）。此时**不能丢**：先留在 preserved 里原样写回，
		// 等渠道重新可用时由 PromoteUnloaded 提升回池。
		if _, ok := provider.Get(pa.ProviderID); !ok {
			if len(skipped) < maxPreservedAccounts {
				skipped = append(skipped, pa)
			} else {
				overflow++
			}
			continue
		}
		env, didMigrate, err := restoreAccount(pa, key, tr)
		if err != nil {
			return err
		}
		if didMigrate {
			migrated = true
		}
		m.Add(env)
	}
	if len(skipped) > 0 {
		// 只记数量与渠道 id，不记凭证。
		ids := make(map[string]bool, len(skipped))
		for _, pa := range skipped {
			ids[pa.ProviderID] = true
		}
		missing := make([]string, 0, len(ids))
		for id := range ids {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		log.Printf("[WARN] 账号文件里有 %d 个账号的渠道当前未注册（%s）：已原样保留，不会回写删除",
			len(skipped), strings.Join(missing, ", "))
	}
	m.persist.mu.Lock()
	if migrated {
		m.persist.migrated = true
	}
	m.persist.preserved = skipped
	m.persist.preservedOverflow = overflow > 0
	m.persist.mu.Unlock()
	if overflow > 0 {
		log.Printf("[ERROR] 有 %d 个账号的渠道未注册且超出保留上限 %d：已禁止回写账号文件，"+
			"请先恢复对应渠道再保存", overflow, maxPreservedAccounts)
	}
	return nil
}

// restoreAccount 把一条落盘记录还原成内存里的账号对象（**不**入池，由调用方决定）。
// 第二个返回值表示"这次用旧格式解出来了、需要按 envelope v2 重新落盘"。
func restoreAccount(pa persistedAccount, key []byte, tr *transport.Factory) (*provider.AccountEnv, bool, error) {
	manifest, ok := provider.Get(pa.ProviderID)
	if !ok {
		return nil, false, fmt.Errorf("渠道 %q 未注册", pa.ProviderID)
	}
	migrated := false
	env := provider.NewAccountEnv(pa.AccountID, pa.ProviderID)
	env.Label = pa.Label
	env.SetTags(pa.Tags)
	env.SetNote(pa.Note)
	if pa.CreatedAt > 0 {
		env.SetCreatedAt(time.Unix(pa.CreatedAt, 0))
	}
	env.Fingerprint = pa.Fingerprint
	env.NetworkPolicy = pa.NetworkPolicy
	env.TLSProfile = pa.TLSProfile
	env.SetDisabled(pa.Disabled)
	env.SetPriority(pa.Priority)
	env.SetWeight(pa.Weight)
	env.SetConcurrencyOverride(pa.Concurrency)
	env.SetAllowedModels(pa.AllowedModels)
	env.SetModelMapping(pa.ModelMapping)
	env.SetWindowCaps(pa.WindowCaps)
	env.RestoreModelRests(pa.ModelRests)
	if pa.OfficialPaused {
		env.SetOfficialPause(pa.OfficialPauseReason)
		if pa.OfficialPausedAt > 0 {
			env.SetOfficialPausedAt(time.Unix(pa.OfficialPausedAt, 0))
		}
	}
	if env.NetworkPolicy == "" {
		env.NetworkPolicy = manifest.Manifest().NetworkPolicy
	}

	if pa.CredentialsEnc != "" {
		raw, err := base64.StdEncoding.DecodeString(pa.CredentialsEnc)
		if err == nil {
			if credential.IsV2(raw) {
				if len(key) == 0 {
					return nil, false, fmt.Errorf("账号 %s 使用 envelope v2，但未提供凭据密钥", pa.AccountID)
				}
				plain, err := credential.Open(key, raw, credential.AAD(pa.ProviderID, pa.AccountID))
				if err != nil {
					return nil, false, fmt.Errorf("账号 %s 凭据解密失败: %w", pa.AccountID, err)
				}
				_ = json.Unmarshal(plain, &env.Credentials)
			} else if plain, err := simplecrypt.Decrypt(raw); err == nil {
				if len(key) > 0 {
					migrated = true
				}
				_ = json.Unmarshal(plain, &env.Credentials)
			}
		}
	}
	if env.Credentials == nil {
		env.Credentials = make(map[string]string, 4)
	}

	if tr != nil {
		if client, err := tr.NewClient(env.NetworkPolicy, env.Proxy(), env.TLSProfile); err == nil {
			if pa.ProxyOverride != "" {
				env.SetProxyOverride(pa.ProxyOverride)
			}
			env.SetHTTPClient(client)
		}
	}
	return env, migrated, nil
}

// UnloadedAccount 是"磁盘上有、但对应渠道当前未注册，所以进不了内存池"的账号摘要。
//
// 它存在的意义：账号文件是本机唯一可信来源，渠道未注册不等于账号不存在。
// 这些账号必须以「渠道未就绪」的形态对用户可见，并且在渠道恢复注册后自动回到池里
// （见 PromoteUnloaded），而不是无声无息地消失。
type UnloadedAccount struct {
	AccountID  string `json:"account_id"`
	ProviderID string `json:"provider_id"`
	Label      string `json:"label,omitempty"`
	CreatedAt  int64  `json:"created_at,omitempty"`
}

// UnloadedAccounts 返回当前进不了池的账号（按 provider_id、account_id 排序，输出稳定）。
func (m *Manager) UnloadedAccounts() []UnloadedAccount {
	m.persist.mu.Lock()
	preserved := append([]persistedAccount(nil), m.persist.preserved...)
	m.persist.mu.Unlock()
	out := make([]UnloadedAccount, 0, len(preserved))
	for _, pa := range preserved {
		out = append(out, UnloadedAccount{
			AccountID:  pa.AccountID,
			ProviderID: pa.ProviderID,
			Label:      pa.Label,
			CreatedAt:  pa.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		return out[i].AccountID < out[j].AccountID
	})
	return out
}

// PromoteUnloaded 把"渠道现在已注册"的保留账号重新加载进内存池，返回进池的数量。
//
// 为什么需要它：账号在启动时若渠道还没注册，只能先躺在 preserved 里；
// 而渠道是**可以后来才上线**的（用户重新添加自定义渠道、spec 热更新 reconcile）。
// 没有这一步，这些账号会一直卡在 preserved——概览永远数不到它们，
// 用户看到的就是"接入的渠道清零了"。
//
// 失败安全：只有能完整还原（渠道已注册 + 凭据解得出）的才进池并从 preserved 移除；
// 任何一条失败都原样留在 preserved 里，绝不会被删除。
func (m *Manager) PromoteUnloaded(tr *transport.Factory) (int, error) {
	m.promoteMu.Lock()
	defer m.promoteMu.Unlock()

	m.persist.mu.Lock()
	path := m.persist.path
	key := append([]byte(nil), m.persist.key...)
	preserved := append([]persistedAccount(nil), m.persist.preserved...)
	blocked := m.persist.blocked
	overflow := m.persist.preservedOverflow
	m.persist.mu.Unlock()
	if path == "" || len(preserved) == 0 || blocked || overflow {
		return 0, nil
	}

	remaining := make([]persistedAccount, 0, len(preserved))
	promoted := 0
	migrated := false
	for _, pa := range preserved {
		if _, ok := provider.Get(pa.ProviderID); !ok {
			remaining = append(remaining, pa)
			continue
		}
		env, didMigrate, err := restoreAccount(pa, key, tr)
		if err != nil {
			// 还原不了（例如密钥不可用）就留着，交给上层报错，绝不删。
			remaining = append(remaining, pa)
			continue
		}
		if didMigrate {
			migrated = true
		}
		m.Add(env)
		promoted++
	}

	m.persist.mu.Lock()
	m.persist.preserved = remaining
	if migrated {
		m.persist.migrated = true
	}
	m.persist.mu.Unlock()

	if promoted == 0 {
		return 0, nil
	}
	log.Printf("已把 %d 个此前「渠道未注册」的账号重新加载进池（剩余 %d 个仍未就绪）",
		promoted, len(remaining))
	if migrated {
		// 旧格式迁到 envelope v2，需要回写一次。
		return promoted, m.SaveAccounts()
	}
	return promoted, nil
}

// accountCreatedAt 返回稳定的创建时间：首次落盘时记住，之后不再改写。
func accountCreatedAt(env *provider.AccountEnv) int64 {
	if t := env.CreatedAtUnix(); t > 0 {
		return t
	}
	now := time.Now()
	env.SetCreatedAt(now)
	return now.Unix()
}

func pauseAtUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// concurrencyPtr 返回账号级并发覆盖的可落盘形态（nil=跟随全局）。
func concurrencyPtr(env *provider.AccountEnv) *int {
	if v, ok := env.ConcurrencyOverride(); ok {
		return &v
	}
	return nil
}
