//go:build windows

package workbuddy

// WorkBuddy 5.6+ 登录态加密字段解密。
//
// 官方客户端把 accessToken/refreshToken 包成 {$wbEncrypted:1, envelope:<base64>}，
// 信封是 AES-256-GCM；密钥由 WorkBuddy.exe 的官方 Electron 原生绑定在 Node 模式下
// 提供（atRestSecretKey → sha256）。这里只通过匿名管道取密钥，不落盘、不写日志。

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"
)

const wbKeyScript = `
try {
  const c = require('crypto');
  const p = JSON.parse(process._linkedBinding('electron_browser_workbuddy_storage').loggerGet());
  if (p.version !== 1 || typeof p.atRestSecretKey !== 'string') process.exit(2);
  const b = Buffer.from(p.atRestSecretKey, 'base64');
  if (b.length !== 32 || b.toString('base64') !== p.atRestSecretKey || b.every(x => x === 0)) process.exit(2);
  const key = c.createHash('sha256').update(p.atRestSecretKey, 'utf8').digest();
  process.stdout.write(key.toString('base64'));
  key.fill(0); b.fill(0);
} catch (_) { process.exit(2); }
`

var (
	wbKeyMu      sync.Mutex
	wbKeyCache   []byte
	wbKeyExpires time.Time
)

// resolveWorkBuddyExe 查找官方客户端可执行文件。
func resolveWorkBuddyExe() (string, error) {
	if v := strings.TrimSpace(os.Getenv("WORKBUDDY_EXE")); v != "" {
		return v, nil
	}
	candidates := []string{
		`D:\ziliao\xuexi\WorkBuddy\WorkBuddy.exe`,
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "WorkBuddy", "WorkBuddy.exe"),
		filepath.Join(os.Getenv("PROGRAMFILES"), "WorkBuddy", "WorkBuddy.exe"),
	}
	for _, p := range candidates {
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	// 注册表卸载项兜底（DisplayIcon 指向 exe）。
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, `Software\Microsoft\Windows\CurrentVersion\Uninstall`, registry.READ)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		for _, name := range names {
			sub, err := registry.OpenKey(k, name, registry.READ)
			if err != nil {
				continue
			}
			display, _, _ := sub.GetStringValue("DisplayName")
			if strings.Contains(display, "WorkBuddy") {
				icon, _, _ := sub.GetStringValue("DisplayIcon")
				icon = strings.TrimSuffix(strings.Trim(icon, `"`), ",0")
				sub.Close()
				if icon != "" {
					if _, err := os.Stat(icon); err == nil {
						k.Close()
						return icon, nil
					}
				}
			}
			sub.Close()
		}
		k.Close()
	}
	return "", fmt.Errorf("未找到 WorkBuddy 官方客户端（可用 WORKBUDDY_EXE 指定路径）")
}

// loadWBKey 取官方密钥（sha256(atRestSecretKey) 的 32 字节），缓存 60 秒。
func loadWBKey() ([]byte, error) {
	wbKeyMu.Lock()
	defer wbKeyMu.Unlock()
	if wbKeyCache != nil && time.Now().Before(wbKeyExpires) {
		return wbKeyCache, nil
	}
	exe, err := resolveWorkBuddyExe()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-e", wbKeyScript)
	cmd.Env = append(os.Environ(), "ELECTRON_RUN_AS_NODE=1")
	cmd.Env = removeEnv(cmd.Env, "NODE_OPTIONS")
	cmd.Stdin = nil
	cmd.Stderr = io.Discard
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("WorkBuddy 官方密钥不可用（客户端可能已更新）")
	}
	out = bytes.TrimSpace(out)
	key, err := base64.StdEncoding.DecodeString(string(out))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("WorkBuddy 官方密钥格式不受支持")
	}
	wbKeyCache = key
	wbKeyExpires = time.Now().Add(60 * time.Second)
	return key, nil
}

func removeEnv(env []string, key string) []string {
	out := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// decryptWBField 解密一个 {$wbEncrypted:1,envelope} 字段；非加密字段按字符串返回。
// 返回 (明文, 是否加密, 错误)。
func decryptWBField(raw json.RawMessage, key []byte) (string, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", false, nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return strings.TrimSpace(s), false, nil
		}
		return "", false, nil
	}
	var wrapper map[string]any
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return "", true, fmt.Errorf("加密字段不是合法 JSON")
	}
	if v, ok := wrapper["$wbEncrypted"]; !ok || v != float64(1) {
		return "", true, fmt.Errorf("不支持的加密字段格式")
	}
	envB64, _ := wrapper["envelope"].(string)
	rawEnv, err := base64.StdEncoding.DecodeString(envB64)
	if err != nil {
		return "", true, fmt.Errorf("加密信封解码失败")
	}
	var env struct {
		Suite        int    `json:"suite"`
		KeyID        string `json:"keyId"`
		KeyIDSnake   string `json:"key_id"`
		Nonce        string `json:"nonce"`
		AuthTag      string `json:"authTag"`
		AuthTagSnake string `json:"auth_tag"`
		Ciphertext   string `json:"ciphertext"`
	}
	if err := json.Unmarshal(rawEnv, &env); err != nil {
		return "", true, fmt.Errorf("加密信封解析失败")
	}
	if env.KeyID == "" {
		env.KeyID = env.KeyIDSnake
	}
	if env.AuthTag == "" {
		env.AuthTag = env.AuthTagSnake
	}
	if env.Suite != 1 {
		return "", true, fmt.Errorf("不支持的加密套件 %d", env.Suite)
	}
	if key == nil {
		return "", true, fmt.Errorf("缺少 WorkBuddy 官方密钥")
	}
	sum := sha256.Sum256(key)
	if hex.EncodeToString(sum[:])[:16] != env.KeyID {
		return "", true, fmt.Errorf("WorkBuddy 官方密钥已变化，请重试")
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil || len(nonce) != 12 {
		return "", true, fmt.Errorf("加密 nonce 非法")
	}
	tag, err := base64.StdEncoding.DecodeString(env.AuthTag)
	if err != nil || len(tag) != 16 {
		return "", true, fmt.Errorf("加密 tag 非法")
	}
	ct, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return "", true, fmt.Errorf("密文解码失败")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", true, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", true, err
	}
	aad := wbAAD(env.KeyID)
	plain, err := gcm.Open(nil, nonce, append(ct, tag...), aad)
	if err != nil {
		return "", true, fmt.Errorf("加密字段校验失败")
	}
	return string(plain), true, nil
}

func wbAAD(keyID string) []byte {
	var b bytes.Buffer
	b.WriteString("WB-AAD\x00\x01")
	for _, v := range []string{"WBEV1", "sym-v1"} {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(v)))
		b.Write(l[:])
		b.WriteString(v)
	}
	var one [4]byte
	binary.BigEndian.PutUint32(one[:], 1)
	b.Write(one[:])
	var kl [4]byte
	binary.BigEndian.PutUint32(kl[:], uint32(len(keyID)))
	b.Write(kl[:])
	b.WriteString(keyID)
	b.Write([]byte{2, 0, 0})
	return b.Bytes()
}
