package mobile

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/apppaths"
)

// 调用方密钥（caller key）持久化的 Go 侧合同：
//
//  1. 首次启动生成一把 43 字符 base64url 的 key，并落盘（0600，在数据目录里）；
//  2. 之后每次启动读到**同一把**——客户端配置好的 key 重启后仍然能打通 /v1；
//  3. 文件内容为空/损坏/非法时重新生成并覆盖，启动不崩；
//  4. 生成结果始终满足既有格式校验（43 字符、base64url、32 字节随机数）；
//  5. 落盘失败 fail-closed（报错而不是悄悄退回"每次启动换 key"）；
//  6. key 不进日志。

// fixtureCallerKey 是一把合法的 43 字符 base64url key：32 个 0 字节的编码
// （base64url 无填充恰好 43 个 'A'）。用它当"已知已落盘"的种子。
const fixtureCallerKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// callerKeyFilePath 只用来在 Start 之前种文件：路径来自 apppaths，确保测试
// 断言的就是运行时真正读写的那条路径。
func callerKeyFilePath(t *testing.T, cfg RuntimeConfig) string {
	t.Helper()
	paths, err := apppaths.NewPaths(cfg.DataDir, cfg.CacheDir, cfg.TempDir)
	if err != nil {
		t.Fatalf("apppaths.NewPaths: %v", err)
	}
	return paths.CallerKeyPath
}

// keyGet 用 caller key 打一个 GET，返回状态码与响应体。
func keyGet(t *testing.T, addr, path, key string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// assertOwnerOnly 在真正支持 Unix 权限位的平台上断言 0600。
// Windows 上 os.Chmod 只切只读位，断不出 0600，跳过。
func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("%s perm = %#o, want 0600", path, perm)
	}
}

// 首次启动：文件原本不存在 → 生成、落盘、内容与下发给宿主的值一致。
func TestCallerKeyGeneratedAndPersistedOnFirstStart(t *testing.T) {
	cfg := testConfig(t)
	file := callerKeyFilePath(t, cfg)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("调用方密钥文件 %s 不应该预先存在: %v", file, err)
	}

	rt := startForTest(t, cfg)
	status := rt.Status()
	key := status.CallerKey
	if len(key) != 43 {
		t.Fatalf("CallerKey = %q（%d 字符），want 43", key, len(key))
	}
	if !validCallerKey(key) {
		t.Fatalf("CallerKey = %q 不满足 base64url 32 字节格式", key)
	}
	if key == status.AdminSession {
		t.Fatal("caller key 不能与管理会话 key 相同")
	}
	if got := rt.Paths().CallerKeyPath; got != file {
		t.Fatalf("Paths().CallerKeyPath = %q, want %q", got, file)
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("首次启动必须把 key 落盘到 %s: %v", file, err)
	}
	if onDisk := strings.TrimSpace(string(raw)); onDisk != key {
		t.Fatalf("落盘内容 = %q，与下发给宿主的 %q 不一致", onDisk, key)
	}
	assertOwnerOnly(t, file)

	// key 不得进日志（落盘/恢复的日志行都不含值）。
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	logData, err := os.ReadFile(rt.Paths().LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(logData), key) {
		t.Fatalf("日志泄漏了调用方密钥: %s", logData)
	}
}

// 核心断言：二次启动读到**同一把** key，并且客户端手里那把 key 在重启后
// 仍然能打通 /v1（这才是用户感知到的"不再莫名 401"）。
func TestCallerKeySurvivesRestart(t *testing.T) {
	cfg := testConfig(t)
	// 让管理会话 key 走运行时生成路径，才能断言它仍然每次启动轮换
	// （testConfig 默认注入固定值，注入值当然每次都一样）。
	cfg.AdminSessionKey = ""

	first := startForTest(t, cfg)
	firstStatus := first.Status()
	key1 := firstStatus.CallerKey
	if code, body := keyGet(t, firstStatus.Addr, "/v1/models", key1); code != http.StatusOK {
		t.Fatalf("首次启动用 caller key 访问 /v1/models = %d: %s", code, body)
	}
	if err := first.Stop(context.Background()); err != nil {
		t.Fatalf("第一次 Stop: %v", err)
	}
	// Stop 会清空内存态；落盘的那把不受影响。
	if after := first.Status(); after.CallerKey != "" {
		t.Fatalf("Stop 后内存里不该还留着 caller key: %q", after.CallerKey)
	}

	second := startForTest(t, cfg)
	secondStatus := second.Status()
	key2 := secondStatus.CallerKey
	if key1 != key2 {
		t.Fatalf("重启后 caller key 变了：%q → %q（客户端配置会突然 401）", key1, key2)
	}
	if !validCallerKey(key2) {
		t.Fatalf("重启后读到的 key = %q 格式不合法", key2)
	}

	// 客户端配置里那把（旧进程下发的）key，重启后仍然被网关接受。
	if code, body := keyGet(t, secondStatus.Addr, "/v1/models", key1); code != http.StatusOK {
		t.Fatalf("重启后用同一把 caller key 访问 /v1/models = %d: %s", code, body)
	}
	// 持久化不改鉴权边界：caller key 仍然进不了管理面。
	if code, _ := keyGet(t, secondStatus.Addr, "/api/providers", key1); code != http.StatusUnauthorized {
		t.Fatalf("caller key 不得解锁管理面，状态 = %d", code)
	}
	// 管理会话 key 仍然每次启动轮换（本次需求只持久化 caller key）。
	if secondStatus.AdminSession == firstStatus.AdminSession {
		t.Fatal("管理会话 key 仍应每次启动轮换")
	}

	// 第三次启动同样是同一把：不是"只有第二次凑巧一致"。
	if err := second.Stop(context.Background()); err != nil {
		t.Fatalf("第二次 Stop: %v", err)
	}
	third := startForTest(t, cfg)
	if got := third.Status().CallerKey; got != key1 {
		t.Fatalf("第三次启动 caller key = %q, want %q", got, key1)
	}
}

// 已有合法文件时优先复用它，不重新生成（模拟"升级前手工/上一版本落过盘"）。
func TestCallerKeyUsesExistingFile(t *testing.T) {
	cfg := testConfig(t)
	file := callerKeyFilePath(t, cfg)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 带尾换行，验证读取侧容忍编辑器友好的结尾空白。
	if err := os.WriteFile(file, []byte(fixtureCallerKey+"\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	rt := startForTest(t, cfg)
	status := rt.Status()
	if status.CallerKey != fixtureCallerKey {
		t.Fatalf("CallerKey = %q，应复用已落盘的 %q", status.CallerKey, fixtureCallerKey)
	}
	if code, body := keyGet(t, status.Addr, "/v1/models", fixtureCallerKey); code != http.StatusOK {
		t.Fatalf("用落盘的 key 访问 /v1/models = %d: %s", code, body)
	}
	// 复用路径不该改写文件内容。
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if onDisk := strings.TrimSpace(string(raw)); onDisk != fixtureCallerKey {
		t.Fatalf("复用后文件被改写成 %q", onDisk)
	}
	assertOwnerOnly(t, file)
}

// 文件为空/损坏/非法时：重新生成、覆盖、不崩，且新 key 仍然合法。
func TestCallerKeyRegeneratedWhenFileCorrupt(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"whitespace-only", "   \n\t\r\n"},
		{"not-base64", "definitely-not-a-caller-key"},
		{"too-short", strings.Repeat("A", 42)},
		{"too-long", strings.Repeat("A", 43) + "="},
		{"invalid-char", strings.Repeat("A", 42) + "!"},
		{"inner-newline", strings.Repeat("A", 21) + "\n" + strings.Repeat("A", 21)},
		{"non-canonical-tail-bits", strings.Repeat("A", 42) + "B"},
		{"json-ish", `{"callerKey":"` + fixtureCallerKey + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			file := callerKeyFilePath(t, cfg)
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(file, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("seed broken file: %v", err)
			}

			rt := startForTest(t, cfg)
			key := rt.Status().CallerKey
			if !validCallerKey(key) {
				t.Fatalf("损坏内容 %q 之后生成的 key = %q 不合法", tc.content, key)
			}
			if key == strings.TrimSpace(tc.content) {
				t.Fatalf("损坏内容 %q 被当成合法 key 复用了", tc.content)
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("重新生成后应覆盖落盘: %v", err)
			}
			if onDisk := strings.TrimSpace(string(raw)); onDisk != key {
				t.Fatalf("覆盖后文件内容 = %q，want %q", onDisk, key)
			}
			assertOwnerOnly(t, file)

			// 重新生成的 key 必须真的能用（不是写了一个不生效的值）。
			if code, body := keyGet(t, rt.Status().Addr, "/v1/models", key); code != http.StatusOK {
				t.Fatalf("重新生成的 key 访问 /v1/models = %d: %s", code, body)
			}
		})
	}
}

// 生成器本身的格式合同：43 字符 base64url（无填充），解码后 32 字节，且每次都不同。
func TestGenerateCallerKeyFormat(t *testing.T) {
	// 常量本身就是对比特位/编码长度的合同：32 字节 → ceil(32/3)*4-1 = 43。
	if callerKeyEncodedLen != 43 || callerKeyBytes != 32 {
		t.Fatalf("caller key 常量 = %d 字节 / %d 字符, want 32 / 43", callerKeyBytes, callerKeyEncodedLen)
	}
	seen := make(map[string]bool, 50)
	for i := 0; i < 50; i++ {
		key, err := generateCallerKey()
		if err != nil {
			t.Fatalf("generateCallerKey: %v", err)
		}
		if len(key) != 43 {
			t.Fatalf("key %q 长度 = %d, want 43", key, len(key))
		}
		if !validCallerKey(key) {
			t.Fatalf("生成的 key %q 未通过 validCallerKey", key)
		}
		raw, err := base64.RawURLEncoding.DecodeString(key)
		if err != nil || len(raw) != callerKeyBytes {
			t.Fatalf("key %q 解码 = %d 字节, err = %v, want 32", key, len(raw), err)
		}
		for _, r := range key {
			if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", r) {
				t.Fatalf("key %q 含非 base64url 字符 %q", key, r)
			}
		}
		if seen[key] {
			t.Fatalf("生成了重复的 key %q", key)
		}
		seen[key] = true
	}
}

// validCallerKey 的严格性：长度、字符集、以及"解码再编码等于原文"。
func TestValidCallerKey(t *testing.T) {
	if !validCallerKey(fixtureCallerKey) {
		t.Fatalf("validCallerKey(%q) = false, want true", fixtureCallerKey)
	}
	generated, err := generateCallerKey()
	if err != nil {
		t.Fatalf("generateCallerKey: %v", err)
	}
	if !validCallerKey(generated) {
		t.Fatalf("validCallerKey(%q) = false, want true", generated)
	}

	bad := []string{
		"",
		"   ",
		strings.Repeat("A", 42),
		strings.Repeat("A", 44),
		strings.Repeat("A", 43) + "=",
		strings.Repeat("A", 42) + "!",
		strings.Repeat("A", 42) + "+", // 标准 base64 而不是 url 字母表
		strings.Repeat("A", 42) + "/",
		strings.Repeat("A", 21) + "\n" + strings.Repeat("A", 21),
		strings.Repeat("A", 42) + "B", // 末位多余比特非零：解码能过但不是规范编码
		`{"callerKey":"` + fixtureCallerKey + `"}`,
	}
	for _, key := range bad {
		if validCallerKey(key) {
			t.Fatalf("validCallerKey(%q) = true, want false", key)
		}
	}
}

// 落盘失败要 fail-closed：报错码可诊断，而不是悄悄用内存态 key 继续跑。
// 构造方式：把 caller-key 这个路径先占成一个目录，rename 到该路径必然失败。
func TestCallerKeyPersistFailureFailsClosed(t *testing.T) {
	cfg := testConfig(t)
	file := callerKeyFilePath(t, cfg)
	if err := os.MkdirAll(file, 0o700); err != nil {
		t.Fatalf("mkdir blocker: %v", err)
	}

	_, err := Start(context.Background(), cfg)
	var se *StartError
	if !errors.As(err, &se) || se.Code != "caller_key_persist_failed" {
		t.Fatalf("err = %v, want caller_key_persist_failed", err)
	}
	if _, statErr := os.Stat(file + ".tmp"); !os.IsNotExist(statErr) {
		t.Fatalf("失败后不该留下 %s.tmp: %v", file, statErr)
	}
}
