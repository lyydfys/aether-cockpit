package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aether-cockpit/internal/core/credential"
	"aether-cockpit/internal/core/simplecrypt"
	"aether-cockpit/internal/core/totp"
)

// 本机 2FA（TOTP）验证码管理：对标 local-vault 的 2FA 管理页。
//
// seed 的落盘方式取决于运行形态：
//   - 桌面：没有 `CredentialKey`，沿用 simplecrypt（既有 2fa.json 继续可读）。
//   - 移动：`CredentialKey` 由 Android Keystore 派生，seed 走 envelope v2
//     （AES-256-GCM，AAD 绑定条目 ID）。simplecrypt 的 key 来自 hostname/home，
//     在 Android 上要么退化成「所有设备同一个 key」，要么换装后再也解不开，
//     两者都不满足架构文档要求的「TOTP seed 走 Keystore 包裹的 cipher」。
//
// 列表接口一律不回传明文 seed，验证码只在本机计算。

type twoFAEntry struct {
	ID         string `json:"id"`
	Issuer     string `json:"issuer,omitempty"`
	Account    string `json:"account,omitempty"`
	Note       string `json:"note,omitempty"`
	Digits     int    `json:"digits"`
	Period     int    `json:"period"`
	Algorithm  string `json:"algorithm,omitempty"`
	SecretEnc  string `json:"secret_enc"`
	SecretTail string `json:"secret_tail,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

type twoFAFile struct {
	Entries []twoFAEntry `json:"entries"`
}

func (g *Gateway) twoFAPath() string { return filepath.Join(g.configDir(), "2fa.json") }

// twoFASecretAAD 把 seed 密文绑定到条目 ID：同一份密文换到别的条目就解不开，
// 防止「拷一条密文到另一条」这种搬运。
func twoFASecretAAD(id string) string { return credential.AAD("2fa", id) }

// sealTwoFASecret 决定 seed 怎么落盘。
//
// 有 CredentialKey（移动运行时，Keystore 派生）就走 envelope v2；没有就保持
// simplecrypt，保证桌面既有 2fa.json 不被写坏。**不猜、不降级**：给了 key 却写
// simplecrypt，等于把移动端的安全前提悄悄丢掉。
func (g *Gateway) sealTwoFASecret(id, secret string) ([]byte, error) {
	if len(g.cfg.CredentialKey) > 0 {
		return credential.Seal(g.cfg.CredentialKey, []byte(secret), twoFASecretAAD(id))
	}
	return simplecrypt.Encrypt([]byte(secret))
}

// openTwoFASecret 对称地解封。带 v2 magic 但当前运行模式没有 key 时如实报错，
// 不能假装成功；老 simplecrypt 条目继续可读（桌面与老数据兼容）。
func (g *Gateway) openTwoFASecret(id string, raw []byte) ([]byte, error) {
	if credential.IsV2(raw) {
		if len(g.cfg.CredentialKey) == 0 {
			return nil, errors.New("该条目由设备凭据加密，当前运行模式没有可用密钥")
		}
		return credential.Open(g.cfg.CredentialKey, raw, twoFASecretAAD(id))
	}
	return simplecrypt.Decrypt(raw)
}

// load2FA 读取条目；调用方持有 twofaMu。
func (g *Gateway) load2FA() ([]twoFAEntry, error) {
	raw, err := os.ReadFile(g.twoFAPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f twoFAFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	return f.Entries, nil
}

// save2FA 原子落盘；调用方持有 twofaMu。
func (g *Gateway) save2FA(entries []twoFAEntry) error {
	if err := os.MkdirAll(filepath.Dir(g.twoFAPath()), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(twoFAFile{Entries: entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp := g.twoFAPath() + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, g.twoFAPath())
}

func twoFAView(e twoFAEntry) map[string]any {
	algorithm := strings.ToUpper(strings.TrimSpace(e.Algorithm))
	if algorithm == "" {
		algorithm = "SHA1"
	}
	return map[string]any{
		"id":          e.ID,
		"issuer":      e.Issuer,
		"account":     e.Account,
		"note":        e.Note,
		"digits":      totp.Digits(e.Digits),
		"period":      totp.Period(e.Period),
		"algorithm":   algorithm,
		"created_at":  e.CreatedAt,
		"secret_tail": e.SecretTail,
	}
}

// handle2FAList GET /api/2fa
func (g *Gateway) handle2FAList(w http.ResponseWriter, r *http.Request) {
	g.twofaMu.Lock()
	defer g.twofaMu.Unlock()
	entries, err := g.load2FA()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "读取 2FA 库失败: " + err.Error()})
		return
	}
	views := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		views = append(views, twoFAView(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": views})
}

// handle2FAAdd POST /api/2fa
//
//	{"issuer":"GitHub","account":"dev","secret":"BASE32","digits":6,"period":30,"algorithm":"SHA1"}
func (g *Gateway) handle2FAAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Issuer    string `json:"issuer"`
		Account   string `json:"account"`
		Note      string `json:"note"`
		Secret    string `json:"secret"`
		Digits    int    `json:"digits"`
		Period    int    `json:"period"`
		Algorithm string `json:"algorithm"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	secret := totp.Normalize(body.Secret)
	if secret == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "密钥不能为空"})
		return
	}
	digits := totp.Digits(body.Digits)
	period := totp.Period(body.Period)
	algorithm := strings.ToUpper(strings.TrimSpace(body.Algorithm))
	if algorithm == "" {
		algorithm = "SHA1"
	}
	if _, _, err := totp.Code(secret, time.Now(), digits, period, algorithm); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	// 条目 ID 要在加封之前生成：移动端的 AAD 绑定条目 ID，先封后取 ID 就绑不上。
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	id := "2fa_" + hex.EncodeToString(buf)

	enc, err := g.sealTwoFASecret(id, secret)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "加密密钥失败: " + err.Error()})
		return
	}
	tail := secret
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}

	entry := twoFAEntry{
		ID:         id,
		Issuer:     strings.TrimSpace(body.Issuer),
		Account:    strings.TrimSpace(body.Account),
		Note:       strings.TrimSpace(body.Note),
		Digits:     digits,
		Period:     period,
		Algorithm:  algorithm,
		SecretEnc:  base64.StdEncoding.EncodeToString(enc),
		SecretTail: tail,
		CreatedAt:  time.Now().Unix(),
	}

	g.twofaMu.Lock()
	entries, err := g.load2FA()
	if err == nil {
		entries = append(entries, entry)
		err = g.save2FA(entries)
	}
	g.twofaMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存 2FA 条目失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": twoFAView(entry)})
}

// handle2FADelete DELETE /api/2fa/{id}
func (g *Gateway) handle2FADelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g.twofaMu.Lock()
	defer g.twofaMu.Unlock()
	entries, err := g.load2FA()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "读取 2FA 库失败: " + err.Error()})
		return
	}
	out := entries[:0]
	found := false
	for _, e := range entries {
		if e.ID == id {
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "条目不存在: " + id})
		return
	}
	if err := g.save2FA(out); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// handle2FACodes GET /api/2fa/codes —— 本机计算当前验证码。
func (g *Gateway) handle2FACodes(w http.ResponseWriter, r *http.Request) {
	g.twofaMu.Lock()
	entries, err := g.load2FA()
	g.twofaMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "读取 2FA 库失败: " + err.Error()})
		return
	}
	views := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		v := twoFAView(e)
		raw, err := base64.StdEncoding.DecodeString(e.SecretEnc)
		if err != nil {
			v["error"] = "密钥存储损坏"
			views = append(views, v)
			continue
		}
		plain, err := g.openTwoFASecret(e.ID, raw)
		if err != nil {
			v["error"] = "密钥无法解密：" + err.Error()
			views = append(views, v)
			continue
		}
		code, remaining, err := totp.Code(string(plain), time.Now(), e.Digits, e.Period, e.Algorithm)
		if err != nil {
			v["error"] = err.Error()
			views = append(views, v)
			continue
		}
		v["code"] = code
		v["remaining"] = remaining
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": views})
}
