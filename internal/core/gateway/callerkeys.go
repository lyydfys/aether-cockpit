package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/simplecrypt"
)

// 调用方网关密钥：给"不是这台电脑上的 Agent"用的身份。
//
// 本机回环永远受信任（与 magpie 一致）；一旦网关被隧道/局域网暴露，
// 远程请求必须带主 API Key 或下列任一启用的网关密钥。
// 带密钥的请求按密钥计量（用量页的 callerKey、逐请求账本），
// 并可设置窗口内的 token / 费用上限，超限直接 429。
//
// 密钥明文只在本机加密保存（simplecrypt），列表接口只给掩码；
// 仅在新建/轮换/复制时返回一次明文。

// callerKeyLimit 是窗口内的用量上限。Tokens/Cost/RPM 为 0 表示不限。
// RPM 是"每分钟请求数"（滑窗 60 秒），与 token/费用窗口独立生效。
type callerKeyLimit struct {
	Period string  `json:"period,omitempty"` // days | hours
	Days   int     `json:"days,omitempty"`
	Hours  int     `json:"hours,omitempty"`
	Tokens int64   `json:"tokens,omitempty"`
	Cost   float64 `json:"cost,omitempty"`
	RPM    int     `json:"rpm,omitempty"`
}

// callerKey 是一条网关密钥。
type callerKey struct {
	ID        string          `json:"id"`
	Name      string          `json:"name,omitempty"`
	SecretEnc string          `json:"secret_enc"`
	CreatedAt int64           `json:"created_at"`
	Off       bool            `json:"off,omitempty"`
	Limit     *callerKeyLimit `json:"limit,omitempty"`
	Models    []string        `json:"models,omitempty"`
	// ModelsExact 为 true 时 Models 是完整模型名；默认按"包含或后缀"匹配。
}

// callerKeyInfo 是请求上下文里的调用方身份。
type callerKeyInfo struct {
	ID   string
	Name string
}

type callerKeyCtxKey struct{}

func withCallerKey(ctx context.Context, k *callerKeyInfo) context.Context {
	return context.WithValue(ctx, callerKeyCtxKey{}, k)
}

// callerKeyFrom 返回请求上下文里的调用方身份（null = 未带密钥）。
func callerKeyFrom(ctx context.Context) *callerKeyInfo {
	if v, ok := ctx.Value(callerKeyCtxKey{}).(*callerKeyInfo); ok {
		return v
	}
	return nil
}

type callerKeyStore struct {
	mu    sync.Mutex
	path  string
	keys  []callerKey
	bySum map[string]int // sha256(secret) -> index
	// rpm 是每个密钥最近一分钟的请求时间（滑窗限流），只在内存。
	rpm map[string][]time.Time
}

type callerKeysFile struct {
	Version int         `json:"version"`
	SavedAt int64       `json:"saved_at"`
	Keys    []callerKey `json:"keys"`
}

func newCallerKeyStore(path string) *callerKeyStore {
	s := &callerKeyStore{path: path, bySum: map[string]int{}, rpm: map[string][]time.Time{}}
	s.load()
	return s
}

func (s *callerKeyStore) load() {
	if s == nil || s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var f callerKeysFile
	if json.Unmarshal(b, &f) != nil {
		return
	}
	s.keys = f.Keys
	s.reindex()
}

func (s *callerKeyStore) reindex() {
	s.bySum = make(map[string]int, len(s.keys))
	for i, k := range s.keys {
		if secret := decryptCallerSecret(k.SecretEnc); secret != "" {
			s.bySum[hashCallerSecret(secret)] = i
		}
	}
}

func (s *callerKeyStore) saveLocked() error {
	if s == nil || s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(callerKeysFile{Version: 1, SavedAt: time.Now().Unix(), Keys: s.keys}, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func hashCallerSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func encryptCallerSecret(secret string) (string, error) {
	enc, err := simplecrypt.Encrypt([]byte(secret))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

func decryptCallerSecret(enc string) string {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return ""
	}
	plain, err := simplecrypt.Decrypt(raw)
	if err != nil {
		return ""
	}
	return string(plain)
}

// newSecret 生成 aek_ + 48 hex 字符。
func newSecret() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "aek_" + hex.EncodeToString([]byte(time.Now().String()))[:48]
	}
	return "aek_" + hex.EncodeToString(b)
}

func maskCallerSecret(secret string) string {
	if len(secret) <= 12 {
		return strings.Repeat("•", len(secret))
	}
	return secret[:8] + "…" + secret[len(secret)-4:]
}

// match 按明文密钥找启用的条目。
func (s *callerKeyStore) match(secret string) (*callerKeyInfo, bool) {
	if s == nil || secret == "" {
		return nil, false
	}
	sum := hashCallerSecret(secret)
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.bySum[sum]
	if !ok || i >= len(s.keys) || s.keys[i].Off {
		return nil, false
	}
	k := s.keys[i]
	return &callerKeyInfo{ID: k.ID, Name: k.Name}, true
}

// hasEnabled 报告是否存在启用的密钥。
func (s *callerKeyStore) hasEnabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.keys {
		if !k.Off {
			return true
		}
	}
	return false
}

func (s *callerKeyStore) get(id string) *callerKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.keys {
		if s.keys[i].ID == id {
			cp := s.keys[i]
			return &cp
		}
	}
	return nil
}

// add 新建密钥，返回明文（只此一次）。
func (s *callerKeyStore) add(name string) (callerKey, string) {
	secret := newSecret()
	enc, _ := encryptCallerSecret(secret)
	k := callerKey{
		ID: "ck_" + shortHash(secret), Name: strings.TrimSpace(name),
		SecretEnc: enc, CreatedAt: time.Now().Unix(),
	}
	s.mu.Lock()
	s.keys = append(s.keys, k)
	s.reindex()
	_ = s.saveLocked()
	s.mu.Unlock()
	return k, secret
}

// update 修改指定 ID 的条目；fn 返回 false 时不动。
func (s *callerKeyStore) update(id string, fn func(*callerKey) bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.keys {
		if s.keys[i].ID == id {
			if fn(&s.keys[i]) {
				_ = s.saveLocked()
			}
			return true
		}
	}
	return false
}

func (s *callerKeyStore) remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.keys {
		if s.keys[i].ID == id {
			s.keys = append(s.keys[:i], s.keys[i+1:]...)
			s.reindex()
			_ = s.saveLocked()
			return true
		}
	}
	return false
}

// rotate 换一个新密钥，保留名称/限额。
func (s *callerKeyStore) rotate(id string) (string, bool) {
	secret := newSecret()
	enc, _ := encryptCallerSecret(secret)
	ok := false
	s.mu.Lock()
	for i := range s.keys {
		if s.keys[i].ID == id {
			s.keys[i].SecretEnc = enc
			ok = true
			break
		}
	}
	if ok {
		s.reindex()
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	return secret, ok
}

// reveal 返回明文（本地管理界面用）。
func (s *callerKeyStore) reveal(id string) (string, bool) {
	k := s.get(id)
	if k == nil {
		return "", false
	}
	secret := decryptCallerSecret(k.SecretEnc)
	return secret, secret != ""
}

// usageFor 汇总某密钥在窗口内的用量（从逐请求账本读）。
func (g *Gateway) callerKeyUsage(id string, limit *callerKeyLimit) (tokens int64, cost float64) {
	if g.ledger == nil || id == "" {
		return 0, 0
	}
	since := time.Now().AddDate(0, 0, -1)
	if limit != nil {
		switch {
		case limit.Period == "hours" && limit.Hours > 0:
			since = time.Now().Add(-time.Duration(limit.Hours) * time.Hour)
		case limit.Period == "days" && limit.Days > 0:
			since = time.Now().AddDate(0, 0, -limit.Days)
		}
	}
	from := since.Format("2006-01-02")
	to := time.Now().Format("2006-01-02")
	for _, r := range g.ledger.records(from, to) {
		if r.CallerKey != id || r.T < since.UnixMilli() {
			continue
		}
		tokens += r.In + r.Out + r.CacheRead + r.CacheWrite
		cost += r.Cost
	}
	return tokens, cost
}

// allowRPM 记一次请求并判断是否超过每分钟上限。
// 滑窗 60 秒；rpm<=0 表示不限。放行的请求才计数。
func (s *callerKeyStore) allowRPM(id string, rpm int, now time.Time) bool {
	if rpm <= 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rpm == nil {
		s.rpm = map[string][]time.Time{}
	}
	cutoff := now.Add(-time.Minute)
	ts := s.rpm[id]
	keep := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	ts = keep
	if len(ts) >= rpm {
		s.rpm[id] = ts
		return false
	}
	s.rpm[id] = append(ts, now)
	return true
}

// resetRPM 清掉某密钥的滑窗（重置限额/删除密钥时调用）。
func (s *callerKeyStore) resetRPM(id string) {
	s.mu.Lock()
	delete(s.rpm, id)
	s.mu.Unlock()
}

// overLimit 报告该密钥是否已超出窗口上限。
func (g *Gateway) callerKeyOverLimit(id string, limit *callerKeyLimit) (bool, string) {
	if limit == nil {
		return false, ""
	}
	tokens, cost := g.callerKeyUsage(id, limit)
	if limit.Tokens > 0 && tokens >= limit.Tokens {
		return true, "该网关密钥的用量已到上限"
	}
	if limit.Cost > 0 && cost >= limit.Cost {
		return true, "该网关密钥的费用已到上限"
	}
	return false, ""
}

// servesModel 报告密钥是否允许该模型。
func (k *callerKey) servesModel(model string) bool {
	if len(k.Models) == 0 || model == "" {
		return true
	}
	for _, m := range k.Models {
		if m == model {
			return true
		}
		// 名单里写的是裸模型名时跨渠道匹配；带渠道前缀的名字要求全名一致。
		if !strings.Contains(m, "/") && strings.HasSuffix(model, "/"+m) {
			return true
		}
		if !strings.Contains(model, "/") && strings.HasSuffix(m, "/"+model) {
			return true
		}
	}
	return false
}
