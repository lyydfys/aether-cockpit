package providers_test

// 反代渠道的"品牌泄漏"硬锁。
//
// 起因：`codex_cli_rs/0.16.0 (Aether Cockpit)`、`AetherCockpit/quota` 这类
// 写法把自家品牌写进了要发给上游的 User-Agent。对反代渠道来说这是最糟的一类
// 指纹——真实客户端的 UA 里不可能出现第三方产品名，等于自报身份。
//
// 规则：internal/providers 下所有发往上游的 User-Agent 字面量，不得包含本产品
// 名称。自有端点（relaybilling 是本地计费中继，不出网）不在此列。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 允许出现品牌的地方：自有端点，不发给第三方。
var brandingAllowlist = map[string]bool{
	"relaybilling": true,
}

// 视为"品牌泄漏"的片段。
var brandMarkers = []string{"Aether Cockpit", "AetherCockpit", "aether-cockpit/", "aether-cockpit"}

func TestNoBrandInUpstreamUserAgent(t *testing.T) {
	root := "."
	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(info.Name())
			if brandingAllowlist[base] {
				return filepath.SkipDir
			}
			if base == "testdata" || (strings.HasPrefix(base, ".") && base != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, `Header.Set("User-Agent"`) {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, marker := range brandMarkers {
				// "aether-cockpit/"+sp.ClientVersion 这类可配置默认值也一并禁止：
				// 反代场景下它同样是品牌泄漏；官方兼容渠道应显式配置真实 UA。
				if strings.Contains(line, marker) {
					violations = append(violations, filepath.ToSlash(path)+":"+itoa(i+1)+"  "+strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历失败：%v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("发往上游的 User-Agent 里出现了本产品品牌（真实客户端不会这么写，等于自报身份）：\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// 头里不要放"可能是空串"的凭证值。
//
// `Header.Set("X-Uid", strings.TrimSpace(env.GetCredential("user_id")))` 在凭证缺失时
// 会发出一个**空值头**。官方客户端要么带真实值、要么根本不会有这个头；空值头
// 是一个很容易被规则命中的异常特征（也污染缓存键）。
//
// 正确写法：先取值、判空，非空再 Set。
func TestNoEmptyCredentialHeader(t *testing.T) {
	root := "."
	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(info.Name())
			if base == "testdata" || (strings.HasPrefix(base, ".") && base != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if !strings.Contains(line, `.Set("`) || !strings.Contains(line, "TrimSpace(env.GetCredential") {
				continue
			}
			violations = append(violations, filepath.ToSlash(path)+":"+itoa(i+1)+"  "+trimmed)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历失败：%v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("这些头可能被写成空值（官方客户端不会发空值头），请先判空：\n  %s",
			strings.Join(violations, "\n  "))
	}
}
