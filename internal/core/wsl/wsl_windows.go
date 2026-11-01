//go:build windows

package wsl

import (
	"aether-cockpit/internal/core/executil"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 检测结果缓存：wsl.exe 每次要起进程，会话扫描与客户端刷新都会问。
var (
	mu      sync.Mutex
	cache   []Home
	at      time.Time
	ttl     time.Duration
	problem string
	// detecting 表示有一次后台检测在跑（TryHomes 的单飞标志）。
	detecting atomic.Bool
)

const (
	detectTTL = 10 * time.Minute
	// 残缺结果（有发行版没响应）只缓存 1 分钟：既不把"空"撑满 10 分钟，
	// 也不会让每次调用都重付一次冷启动超时。
	partialTTL = time.Minute
	// 冷启动的发行版第一次响应可能要十几秒。超时太短会被当成"这个发行版
	// 没装客户端"，而空结果一缓存就是 10 分钟——冷机上整个 WSL 面会长时间
	// 显示不可用。所以给足冷启动时间，并允许重试一次。
	homeQueryTimeout = 20 * time.Second
)

// agentDirs 是"这台发行版里装了 AI 客户端"的证据目录。会话侧要的是
// 已经有会话的目录；注入侧只要客户端存在（哪怕还没开始用过）就值得接线，
// 所以这里认的是各客户端自己的配置目录。
var agentDirs = [][]string{
	{".claude"},
	{".codex"},
	{".gemini"},
	{".config", "opencode"},
	{".config", "mimocode"},
}

// Homes 返回本机全部 WSL 家目录。AETHER_WSL_HOMES 覆盖时不走缓存、
// 也不要求目录里真有客户端（测试与手工挂载用）。
func Homes() []Home {
	if v := strings.TrimSpace(os.Getenv("AETHER_WSL_HOMES")); v != "" {
		out := make([]Home, 0, 2)
		for _, p := range strings.Split(v, string(os.PathListSeparator)) {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, Home{Distro: distroFromPath(p, filepath.Base(p)), Path: p})
			}
		}
		return out
	}
	mu.Lock()
	if !at.IsZero() && time.Since(at) < ttl {
		out := append([]Home(nil), cache...)
		mu.Unlock()
		return out
	}
	mu.Unlock()

	// detect() 可能要几十秒（wsl.exe 冷启动 + UNC stat）：**不持锁**跑，
	// 否则并发的 TryHomes/LastProblem 会被一起卡住。
	homes, prob, complete := detect()

	mu.Lock()
	cache, problem = homes, prob
	ttl = detectTTL
	if !complete {
		ttl = partialTTL
	}
	at = time.Now()
	out := append([]Home(nil), cache...)
	mu.Unlock()
	return out
}

// TryHomes 只返回"已经检测好"的家目录；没有新鲜结果时启动一次后台检测并
// 立即返回 ready=false。界面首屏据此先渲染，不必等 wsl.exe 与 UNC stat。
func TryHomes() ([]Home, bool) {
	if v := strings.TrimSpace(os.Getenv("AETHER_WSL_HOMES")); v != "" {
		return Homes(), true
	}
	mu.Lock()
	fresh := !at.IsZero() && time.Since(at) < ttl
	out := append([]Home(nil), cache...)
	mu.Unlock()
	if fresh {
		return out, true
	}
	if detecting.CompareAndSwap(false, true) {
		go func() {
			defer detecting.Store(false)
			Homes()
		}()
	}
	return out, false
}

// LastProblem 说明最近一次检测为何没拿到家目录；空字符串表示没有异常。
func LastProblem() string {
	mu.Lock()
	defer mu.Unlock()
	return problem
}

func invalidateDetection() {
	mu.Lock()
	cache, at = nil, time.Time{}
	mu.Unlock()
}

// detect 列出发行版、取各自的 $HOME，筛出装了客户端的那些。
// 第三个返回值表示结果是否完整：有发行版查询失败时为 false（残缺，不该缓存）。
func detect() ([]Home, string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wsl.exe", "-l", "-q")
	executil.Hide(cmd)
	raw, err := cmd.Output()
	if err != nil {
		return nil, "wsl.exe -l -q 失败：" + err.Error(), false
	}
	out := make([]Home, 0, 2)
	var failures []string
	for _, distro := range wslDistros(raw) {
		home, err := distroHome(distro)
		if err != nil {
			failures = append(failures, distro+"："+err.Error())
			continue
		}
		for _, base := range []string{"//wsl.localhost/", "//wsl$/"} {
			root := base + distro + home
			if !hasAgentDirs(root) {
				continue
			}
			if st, err := os.Stat(root); err == nil && st.IsDir() {
				out = append(out, Home{Distro: distro, Path: root})
				break
			}
		}
	}
	if len(failures) > 0 {
		return out, "部分发行版未能响应（冷启动或未运行）：" + strings.Join(failures, "；"), false
	}
	if len(out) == 0 {
		return nil, "没有发行版装有 Agent 客户端配置（.claude / .codex / .gemini / .config/opencode）", true
	}
	return out, "", true
}

// wslDistros 解析 `wsl.exe -l -q` 的输出：UTF-16LE、跳过 docker-* 与空行。
func wslDistros(raw []byte) []string {
	out := make([]string, 0, 2)
	for _, line := range strings.Split(strings.ReplaceAll(decodeUTF16(raw), "\r", ""), "\n") {
		d := strings.TrimSpace(line)
		if d == "" || strings.HasPrefix(strings.ToLower(d), "docker") {
			continue
		}
		out = append(out, d)
	}
	return out
}

// distroHome 取发行版里的 $HOME。冷启动第一次可能超时，重试一次即可命中
// （第一次调用已经把发行版拉起来）。
func distroHome(distro string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), homeQueryTimeout)
		cmd := exec.CommandContext(ctx, "wsl.exe", "-d", distro, "--", "sh", "-lc", `printf %s "$HOME"`)
		executil.Hide(cmd)
		raw, err := cmd.Output()
		cancel()
		if err == nil {
			home := strings.TrimRight(strings.TrimSpace(string(raw)), "/")
			if !strings.HasPrefix(home, "/") {
				return "", fmt.Errorf("$HOME 不是绝对路径：%q", home)
			}
			return home, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func hasAgentDirs(home string) bool {
	seen := 0
	for _, rel := range agentDirs {
		if st, err := os.Stat(filepath.Join(append([]string{home}, rel...)...)); err == nil && st.IsDir() {
			seen++
		}
	}
	return seen > 0
}

// HostIP 返回 WSL 虚拟网卡在 Windows 侧的 IPv4 地址（即 WSL 里的默认网关）。
//
// 这是 WSL 访问 Windows 的唯一稳定入口：WSL2 的 NAT 里 127.0.0.1 指的是
// WSL 自己，不是宿主（除非开了镜像网络）。本机实测：绑在这个地址上的
// 监听，WSL 能连、局域网连不上——正好是"只给 WSL 用"的边界。
func HostIP() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		if !strings.Contains(strings.ToLower(ifc.Name), "wsl") {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("未找到 WSL 虚拟网卡（vEthernet (WSL)）：请确认 WSL2 已安装并至少启动过一次")
}

// EnsureRunning 启动（或确认已启动）某个发行版。
//
// 必要性：发行版没在跑时，\\wsl.localhost 下的读写会报"找不到路径"，
// 与"文件确实不存在"在 os.Stat 层面无法区分——配置合并会因此把用户
// 已有的 settings.json 当成不存在，写出一个"-created"备份并丢掉原字段。
// 写配置前先把它拉起来，这个歧义就消失了。
func EnsureRunning(distro string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wsl.exe", "-d", distro, "--", "true")
	executil.Hide(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("无法启动 WSL 发行版 %s：%v", distro, err)
	}
	return nil
}

func decodeUTF16(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	runes := make([]rune, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		r := rune(b[i]) | rune(b[i+1])<<8
		if r != 0 {
			runes = append(runes, r)
		}
	}
	return strings.TrimPrefix(string(runes), "\ufeff")
}
