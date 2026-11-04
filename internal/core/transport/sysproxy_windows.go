//go:build windows

package transport

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Windows「Internet 选项」里的手动代理。
//
// 为什么必须单独读：Go 的 http.ProxyFromEnvironment 只认 HTTP_PROXY /
// HTTPS_PROXY 环境变量，而 Windows 用户在 Clash / 公司代理里勾的是
// 「系统代理」—— 那个开关只写注册表，不写环境变量。于是会出现：
// 浏览器能上网、我们的网关却直连超时，用户完全不知道为什么。
//
// 这里按 WinINET 的规则读 HKCU\...\Internet Settings，并与环境变量合并：
// 显式设置的软件全局代理优先（由调用方处理），其次注册表，最后环境变量。

const (
	winINetKey     = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	winProxyEnable = "ProxyEnable"
	winProxyServer = "ProxyServer"
	winProxyBypass = "ProxyOverride"
)

// sysProxyCache 缓存注册表读数：注册表读取比环境变量慢得多，而每个账号
// 铸造 Transport 时都会问一次。TTL 取 1 分钟，用户在系统设置里改完代理
// 最多一分钟生效，可以接受。
type sysProxyCache struct {
	mu      sync.Mutex
	at      time.Time
	proxy   *url.URL
	bypass  []string
	fetched bool
}

var winSysProxy sysProxyCache

// windowsSystemProxy 返回注册表里配置的系统代理（没有则 nil）。
func windowsSystemProxy() (*url.URL, []string) {
	winSysProxy.mu.Lock()
	defer winSysProxy.mu.Unlock()
	if winSysProxy.fetched && time.Since(winSysProxy.at) < time.Minute {
		return winSysProxy.proxy, winSysProxy.bypass
	}
	winSysProxy.fetched = true
	winSysProxy.at = time.Now()
	winSysProxy.proxy, winSysProxy.bypass = readWinINetProxy()
	return winSysProxy.proxy, winSysProxy.bypass
}

// readWinINetProxy 读注册表并解析成 *url.URL。
func readWinINetProxy() (*url.URL, []string) {
	// ProxyEnable=0 表示系统代理关闭。
	if v, ok := readRegistryDWORD(winINetKey, winProxyEnable); !ok || v == 0 {
		return nil, nil
	}
	raw, ok := readRegistryString(winINetKey, winProxyServer)
	if !ok {
		return nil, nil
	}
	proxy := parseWinProxyServer(raw)
	if proxy == nil {
		return nil, nil
	}
	var bypass []string
	if s, ok := readRegistryString(winINetKey, winProxyBypass); ok && strings.TrimSpace(s) != "" {
		for _, part := range strings.Split(s, ";") {
			if p := strings.TrimSpace(part); p != "" {
				bypass = append(bypass, p)
			}
		}
	}
	return proxy, bypass
}

// parseWinProxyServer 解析 ProxyServer 的两种写法：
//
//	"127.0.0.1:7890"                              → 所有协议共用
//	"http=127.0.0.1:7890;https=127.0.0.1:7891"    → 按协议分别指定
//
// 统一取 http= 那一项（Go 的 Transport 只认一个代理；按协议分流要靠
// Proxy 函数，注册表里 https= 通常和 http= 是同一个端口）。
func parseWinProxyServer(raw string) *url.URL {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	host := s
	if strings.Contains(s, "=") {
		host = ""
		for _, part := range strings.Split(s, ";") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				continue
			}
			scheme := strings.ToLower(strings.TrimSpace(kv[0]))
			val := strings.TrimSpace(kv[1])
			if val == "" {
				continue
			}
			switch scheme {
			case "http":
				host = val
			case "https":
				if host == "" {
					host = val
				}
			}
		}
		if host == "" {
			return nil
		}
	}
	// 注册表里通常是 host:port，没有 scheme。
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Host == "" {
		return nil
	}
	return u
}

// sysProxyFunc 组合"注册表系统代理 + 环境变量"，返回 Transport 用的 Proxy。
//
// 顺序：注册表优先。理由：用户勾了"系统代理"通常就是要它生效；环境变量
// 多见于脚本/容器，两者同时存在时以用户可见的那个为准。
func sysProxyFunc() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if proxy, bypass := windowsSystemProxy(); proxy != nil {
			if !shouldBypass(req.URL.Hostname(), bypass) {
				return proxy, nil
			}
			return nil, nil
		}
		return http.ProxyFromEnvironment(req)
	}
}

// shouldBypass 判断主机是否命中 ProxyOverride（如 "localhost;127.*;<local>"）。
func shouldBypass(host string, patterns []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, p := range patterns {
		pat := strings.ToLower(strings.TrimSpace(p))
		switch {
		case pat == "":
			continue
		case pat == "<local>":
			// WinINET 的 <local> 表示"不带点的主机名"（内网短名）。
			if !strings.Contains(host, ".") {
				return true
			}
		case strings.HasPrefix(pat, "*."):
			if strings.HasSuffix(host, pat[1:]) {
				return true
			}
		case strings.HasPrefix(pat, "*"):
			if strings.HasSuffix(host, pat[1:]) {
				return true
			}
		case strings.HasSuffix(pat, "*"):
			if strings.HasPrefix(host, pat[:len(pat)-1]) {
				return true
			}
		default:
			if host == pat {
				return true
			}
		}
	}
	return false
}

// readRegistryDWORD / readRegistryString 读 HKCU 下的值。
//
// 用 reg.exe 而不是 syscall：读取注册表需要 win32 API，走 syscall 会引入
// x/sys 依赖并增加维护面；reg.exe 在 Windows 上是系统自带、输出稳定，
// 每次结果缓存 1 分钟，开销可以忽略。
func readRegistryDWORD(key, name string) (uint64, bool) {
	out, ok := runRegQuery(key, name)
	if !ok {
		return 0, false
	}
	// 形如：    ProxyEnable    REG_DWORD    0x1
	fields := strings.Fields(out)
	if len(fields) < 3 {
		return 0, false
	}
	v := fields[len(fields)-1]
	// reg query 的 DWORD 输出带 0x 前缀，必须剥掉再按十六进制解析
	// （直接按十六进制读会卡在 'x' 上，导致"系统代理明明开着却读成 0"）。
	v = strings.TrimPrefix(strings.TrimPrefix(v, "0x"), "0X")
	if v == "" {
		return 0, false
	}
	var n uint64
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
			n = n<<4 + uint64(r-'0')
		case r >= 'a' && r <= 'f':
			n = n<<4 + uint64(r-'a'+10)
		case r >= 'A' && r <= 'F':
			n = n<<4 + uint64(r-'A'+10)
		default:
			return 0, false
		}
	}
	return n, true
}

func readRegistryString(key, name string) (string, bool) {
	out, ok := runRegQuery(key, name)
	if !ok {
		return "", false
	}
	// 形如：    ProxyServer    REG_SZ    127.0.0.1:7890
	idx := strings.Index(out, "REG_SZ")
	if idx < 0 {
		// 少数环境是 REG_EXPAND_SZ。
		idx = strings.Index(out, "REG_EXPAND_SZ")
		if idx < 0 {
			return "", false
		}
		idx += len("REG_EXPAND_SZ")
	} else {
		idx += len("REG_SZ")
	}
	v := strings.TrimSpace(out[idx:])
	return v, v != ""
}

// runRegQuery 执行 reg query 并返回整行输出。
func runRegQuery(key, name string) (string, bool) {
	cmd := newRegCmd(key, name)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}
