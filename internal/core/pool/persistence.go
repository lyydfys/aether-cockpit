package pool

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

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
}

type accountsFile struct {
	Version  int                `json:"version"`
	SavedAt  int64              `json:"saved_at"`
	Accounts []persistedAccount `json:"accounts"`
}

// persistence 承担账号的落盘与恢复。
type persistence struct {
	mu   sync.Mutex
	path string
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
		return err
	}
	// 加载过程会走 Add()，把 dirty 置位；这里复位，避免启动即触发一次无意义的回写。
	atomic.StoreInt32(&m.dirty, 0)
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

// StartAutoSave 启动周期性落盘，返回停止函数（停止时会做最后一次 Flush）。
func (m *Manager) StartAutoSave(interval time.Duration) func() {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	stop := make(chan struct{})
	go func() {
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
	return func() { once.Do(func() { close(stop) }) }
}

// SaveAccounts 把所有账号写入磁盘。
func (m *Manager) SaveAccounts() error {
	m.persist.mu.Lock()
	path := m.persist.path
	m.persist.mu.Unlock()
	if path == "" {
		return nil
	}

	envs := m.All()
	out := accountsFile{Version: 1, SavedAt: time.Now().Unix(), Accounts: make([]persistedAccount, 0, len(envs))}

	for _, env := range envs {
		paused, pauseReason, pauseAt := env.OfficialPaused()
		// 只导出凭证字典，不含 HTTPClient 等运行时对象。
		creds, err := json.Marshal(env.Credentials)
		if err != nil {
			continue
		}
		enc, err := simplecrypt.Encrypt(creds)
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
		})
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

	for _, pa := range f.Accounts {
		if pa.ProviderID == "" || pa.AccountID == "" {
			continue
		}
		// 渠道可能已下线（插件被移除），此时跳过而不是报错。
		manifest, ok := provider.Get(pa.ProviderID)
		if !ok {
			continue
		}

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
				if plain, err := simplecrypt.Decrypt(raw); err == nil {
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
		m.Add(env)
	}
	return nil
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
