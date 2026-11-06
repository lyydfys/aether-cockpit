package providers_test

// 跨接口 UA 一致性硬锁。
//
// 同一个渠道的对话、额度、签到、刷新在**真实客户端**里是同一个人格：UA 只由
// 客户端版本决定，不会因为调的是哪个接口而变。反代实现里如果不同文件各写各的
// 字面量，很容易出现"对话是 A 客户端、额度是 B 客户端"——同一账号两套指纹，
// 是最容易被关联的特征之一。
//
// 规则：一个渠道目录里发给上游的 UA 字面量原则上只应有一种。
// 例外要在这里显式登记原因，避免"静默分叉"。

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// uaMultiAllowed 登记"确实应该有多种 UA"的渠道。
var uaMultiAllowed = map[string]string{
	// 对话（MiniMaxAgent）与签到（MiniMaxCode）是两个不同上游产品/endpoint。
	"minimax": "对话与签到分属不同上游产品",
}

var uaLiteralRe = regexp.MustCompile(`Set\("User-Agent",\s*"([^"]+)"`)

// 返回 渠道目录 -> 去重后的 UA 字面量。
func collectUA(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	dirs, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取 provider 目录失败：%v", err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		entries, readErr := os.ReadDir(d.Name())
		if readErr != nil {
			continue
		}
		seen := map[string]bool{}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			raw, readErr := os.ReadFile(filepath.Join(d.Name(), name))
			if readErr != nil {
				continue
			}
			for _, m := range uaLiteralRe.FindAllStringSubmatch(string(raw), -1) {
				seen[m[1]] = true
			}
		}
		if len(seen) == 0 {
			continue
		}
		vals := make([]string, 0, len(seen))
		for v := range seen {
			vals = append(vals, v)
		}
		sort.Strings(vals)
		out[d.Name()] = vals
	}
	return out
}

func TestSingleUserAgentPerChannel(t *testing.T) {
	for dir, vals := range collectUA(t) {
		if len(vals) <= 1 {
			continue
		}
		if reason, ok := uaMultiAllowed[dir]; ok {
			_ = reason
			continue
		}
		t.Errorf("渠道 %s 存在 %d 种 UA 字面量（%s）——真实客户端不会按接口换客户端；"+
			"请统一为同一常量，或在本文件 uaMultiAllowed 登记原因",
			dir, len(vals), strings.Join(vals, " | "))
	}
}

// 能转发对话的渠道必须有明确的 User-Agent。
//
// 不设 UA 的请求会带上 Go 默认的 `Go-http-client/1.1`——这是最经典的一眼假。
// 唯一合理的例外是"真实客户端本身就不发 UA"（devin-cli），必须在下面登记。
var uaAbsentAllowed = map[string]string{
	"devin": "原生 devin-cli 不发送 User-Agent（抓包结论），实现里显式抑制默认值",
	// 协议兼容层：由 spec/调用方决定身份，不属于某个具体客户端。
	"openaicompat": "通用 OpenAI 兼容层，UA 由 spec 的 client_version 决定",
	"geminicompat": "通用 Gemini 兼容层，UA 由 spec 决定",
	"reverse":      "反向引擎本体，各渠道传入 Profile/Headers",
	"mock":         "本地假上游，不访问真实服务",
}

func TestChatChannelSetsUserAgent(t *testing.T) {
	dirs, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取 provider 目录失败：%v", err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		if _, ok := uaAbsentAllowed[d.Name()]; ok {
			continue
		}
		entries, readErr := os.ReadDir(d.Name())
		if readErr != nil {
			continue
		}
		hasChat := false
		hasUA := false
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			raw, readErr := os.ReadFile(filepath.Join(d.Name(), name))
			if readErr != nil {
				continue
			}
			text := string(raw)
			if strings.Contains(text, ") ChatStream(") {
				hasChat = true
			}
			if strings.Contains(text, `Set("User-Agent"`) || strings.Contains(text, `"User-Agent":`) {
				hasUA = true
			}
		}
		if hasChat && !hasUA {
			t.Errorf("渠道 %s 实现了 ChatStream 但从不设置 User-Agent；"+
				"上游会看到 Go-http-client。若真实客户端确实不发 UA，请在 uaAbsentAllowed 登记原因", d.Name())
		}
	}
}
