//go:build windows

package transport

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// 代理失效后的自动直连。
//
// 背景：很多用户把系统代理（Clash 等）关掉后，注册表和环境变量里仍留着
// 指向 127.0.0.1:7890 的配置。此时所有账号共用同一个已失效的出口，
// 故障转移在账号之间切来切去毫无意义——每次都是同一条死路，用户看到的是
// "线路切换失败 / Connection error"。
//
// 这里的策略：一旦检测到连接类失败且当前存在代理配置，就把代理判定为
// "疑似失效"，在接下来的一段时间内直接绕过它走直连；到期后重新尝试代理。
// 这样用户关掉代理软件后能自愈，而不必先来改设置。

// proxySuspectTTL 是"判定代理失效后绕开它"的时长。
//
// 取 5 分钟：足够覆盖用户关掉代理后继续干活的这段时间，又不会因为
// 一次偶发网络抖动就长期禁止代理（到期会自动重新尝试）。
const proxySuspectTTL = 5 * time.Minute

var (
	// proxyBrokenUntil 是代理被判定失效的截止时刻（UnixNano，0=正常）。
	proxyBrokenUntil atomic.Int64
	proxyBrokenOnce  sync.Once
)

// MarkProxySuspect 把当前代理标记为"疑似失效"，在 ttl 内改走直连。
func MarkProxySuspect(reason string) {
	until := time.Now().Add(proxySuspectTTL).UnixNano()
	if prev := proxyBrokenUntil.Load(); prev > time.Now().UnixNano() {
		return // 已在失效窗口内，不重复打日志
	}
	proxyBrokenUntil.Store(until)
	proxyBrokenOnce.Do(func() {})

	// 只在状态从"正常"翻转到"失效"时打一次日志，避免刷屏。
	fmt.Printf("[WARN] 代理连接失败（%s），接下来 %s 内自动改走直连\n",
		reason, proxySuspectTTL)
}

// proxySuspectedBroken 判断当前是否处于"绕开代理"窗口内。
func proxySuspectedBroken() bool {
	until := proxyBrokenUntil.Load()
	return until > 0 && time.Now().UnixNano() < until
}

// ClearProxySuspect 清除失效标记（代理恢复或用户改了设置时调用）。
func ClearProxySuspect() {
	proxyBrokenUntil.Store(0)
}

// IsConnFailure 判断错误是否属于"连接没建立起来"这一类。
//
// 只认建连阶段的错误：连接被拒、主机不可达、DNS 失败、连上后被立刻关闭。
// 不把 4xx/5xx 或超时算进来——那些多半是上游自身问题，不该据此怪代理。
func IsConnFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, pat := range []string{
		"connection refused",
		"actively refused",
		"积极拒绝",
		"proxyconnect",
		"no such host",
		"network is unreachable",
		"connection reset by peer",
		"eof",
	} {
		if strings.Contains(msg, pat) {
			return true
		}
	}
	return false
}

// hasProxyConfigured 判断当前是否真的存在代理配置（系统代理 / 环境变量 / 全局）。
//
// 没有代理时不该触发"代理失效"逻辑——那只说明是目标站点本身连不上。
func hasProxyConfigured(f *Factory) bool {
	if f != nil {
		if u, err := f.globalProxyURL(); err == nil && u != nil {
			return true
		}
	}
	if u, _ := windowsSystemProxy(); u != nil {
		return true
	}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy"} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}

// NoteConnFailure 在请求因建连失败时调用：若确有代理配置，则标记代理失效。
//
// 返回 true 表示本次调用新触发了"改走直连"，调用方可以据此立刻重试。
func NoteConnFailure(f *Factory, err error) bool {
	if !IsConnFailure(err) {
		return false
	}
	if !hasProxyConfigured(f) {
		return false
	}
	if proxySuspectedBroken() {
		return false
	}
	MarkProxySuspect(err.Error())
	return true
}

// directProxyFunc 让请求绕过一切代理（系统代理与环境变量）。
func directProxyFunc(*http.Request) (*url.URL, error) { return nil, nil }

// 确保 log 包被使用（诊断信息按需启用）。
var _ = log.Printf
