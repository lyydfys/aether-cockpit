//go:build windows

package launcher

import (
	"aether-cockpit/internal/core/executil"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// desktopCache 控制探测频率：reg query 与目录扫描每次启动页都跑一遍太浪费。
var (
	desktopMu    sync.Mutex
	desktopAt    time.Time
	desktopCache []DesktopApp
)

const desktopCacheTTL = 2 * time.Minute

// DesktopApps 返回可一键启动的桌面客户端。只探测不改写任何配置：
// 启动后使用用户正常在用的目录与登录状态。
func DesktopApps() []DesktopApp {
	desktopMu.Lock()
	defer desktopMu.Unlock()
	if time.Since(desktopAt) < desktopCacheTTL {
		return append([]DesktopApp(nil), desktopCache...)
	}
	desktopAt = time.Now()
	desktopCache = detectDesktopApps()
	return append([]DesktopApp(nil), desktopCache...)
}

func detectDesktopApps() []DesktopApp {
	out := make([]DesktopApp, 0, 2)

	// Claude 桌面端：传统安装包注册 claude:// 协议；Microsoft Store/MSIX
	// 安装的包把协议写在包清单里，不落 HKCU，必须用 AUMID 激活。
	if claudeExe := claudeDesktopExe(); hasClaudeProtocol() {
		out = append(out, DesktopApp{
			ID: "claude_desktop", Name: "Claude 桌面端", Available: true,
			Target: "claude://", Executable: claudeExe, Note: "使用当前用户目录与登录态",
		})
	} else if target, exe := claudeMSIXTarget(); target != "" {
		out = append(out, DesktopApp{
			ID: "claude_desktop", Name: "Claude 桌面端", Available: true,
			Target: target, Executable: exe, Note: "Microsoft Store 安装；使用当前用户目录与登录态",
		})
	} else {
		out = append(out, DesktopApp{
			ID: "claude_desktop", Name: "Claude 桌面端", Available: false,
			Reason: "未检测到 claude:// 协议或 Claude 桌面端安装（含 Microsoft Store 版）",
		})
	}

	// Codex 桌面端：MSIX 包，用 AppUserModelID 经 shell:AppsFolder 激活。
	if target, exe := codexDesktopTarget(); target != "" {
		out = append(out, DesktopApp{
			ID: "codex_desktop", Name: "Codex 桌面端", Available: true,
			Target: target, Executable: exe, Note: "使用当前用户目录与登录态",
		})
	} else {
		out = append(out, DesktopApp{
			ID: "codex_desktop", Name: "Codex 桌面端", Available: false,
			Reason: "未检测到 OpenAI.Codex 桌面端安装",
		})
	}
	return out
}

// claudeDesktopExe 从 claude:// 的注册命令里取主程序路径。
func claudeDesktopExe() string {
	cmd := exec.Command("reg", "query", `HKCU\Software\Classes\claude\shell\open\command`)
	executil.Hide(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	text := string(out)
	i := strings.IndexByte(text, '"')
	if i < 0 {
		return ""
	}
	if j := strings.IndexByte(text[i+1:], '"'); j >= 0 {
		p := text[i+1 : i+1+j]
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func hasClaudeProtocol() bool {
	for _, key := range []string{
		`HKCU\Software\Classes\claude\shell\open\command`,
		`HKCR\claude\shell\open\command`,
		`HKLM\Software\Classes\claude\shell\open\command`,
	} {
		cmd := exec.Command("reg", "query", key)
		executil.Hide(cmd)
		out, err := cmd.CombinedOutput()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			return true
		}
	}
	return false
}

// claudeMSIXTarget 读取 Microsoft Store 版 Claude 的 AUMID 与安装目录。
// 包清单里的 Application Id 可能是 Claude，也可能随版本变化，所以从
// AppxManifest 里按可执行文件名查，而不是硬编码 "!Claude"。
func claudeMSIXTarget() (target, exe string) {
	const script = `$p = Get-AppxPackage Claude | Select-Object -First 1
if (-not $p) { exit 1 }
$m = Get-AppxPackageManifest $p
$a = $m.Package.Applications.Application | Where-Object { $_.Executable -like '*Claude.exe' } | Select-Object -First 1
if (-not $a) { $a = $m.Package.Applications.Application | Select-Object -First 1 }
if (-not $a) { exit 1 }
Write-Output ($p.PackageFamilyName + '!' + $a.Id)
Write-Output $p.InstallLocation`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	executil.Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return "", ""
	}
	family := strings.TrimSpace(lines[0])
	install := strings.TrimSpace(strings.Join(lines[1:], " "))
	return claudeMSIXTargetFrom(family, install)
}

// claudeMSIXTargetFrom 是 claudeMSIXTarget 的纯解析部分，便于单测。
func claudeMSIXTargetFrom(family, install string) (target, exe string) {
	family = strings.TrimSpace(family)
	install = strings.TrimSpace(install)
	if family == "" || !strings.Contains(family, "!") {
		return "", ""
	}
	target = `shell:AppsFolder\` + family
	for _, rel := range []string{
		filepath.Join("app", "Claude.exe"),
		"Claude.exe",
	} {
		p := filepath.Join(install, rel)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return target, p
		}
	}
	return target, ""
}

func codexDesktopTarget() (target, exe string) {
	// MSIX：%LOCALAPPDATA%\Microsoft\WindowsApps\OpenAI.Codex_<publisherId>
	// 是包的执行别名目录；AUMID = <PackageFamilyName>!<Application Id>。
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		matches, _ := filepath.Glob(filepath.Join(local, "Microsoft", "WindowsApps", "OpenAI.Codex_*"))
		for _, dir := range matches {
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				family := filepath.Base(dir)
				return "shell:AppsFolder\\" + family + "!App", codexDesktopExe()
			}
		}
	}
	// 传统安装包的兜底路径。
	for _, p := range []string{
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "OpenAI", "Codex", "Codex.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Codex", "Codex.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "OpenAI", "Codex", "Codex.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Codex", "Codex.exe"),
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, p
		}
	}
	return "", ""
}

// codexDesktopExe 取 MSIX 安装目录里的主程序。WindowsApps 不能直接枚举，
// 交给 Get-AppxPackage 拿 InstallLocation（结果缓存 2 分钟）。
func codexDesktopExe() string {
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "(Get-AppxPackage OpenAI.Codex).InstallLocation")
	executil.Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	loc := strings.TrimSpace(string(out))
	if loc == "" {
		return ""
	}
	p := filepath.Join(loc, "app", "ChatGPT.exe")
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return ""
}

// LaunchDesktopApp 用正常用户目录打开桌面客户端。
func LaunchDesktopApp(id string) error {
	for _, app := range DesktopApps() {
		if app.ID != id {
			continue
		}
		if !app.Available {
			if app.Reason != "" {
				return fmt.Errorf("%s 不可用：%s", app.Name, app.Reason)
			}
			return fmt.Errorf("%s 不可用", app.Name)
		}
		switch id {
		case "claude_desktop":
			if strings.HasPrefix(app.Target, "shell:") {
				return startGUI("explorer.exe", app.Target)
			}
			return startGUI("rundll32.exe", "url.dll,FileProtocolHandler", app.Target)
		case "codex_desktop":
			if strings.HasPrefix(app.Target, "shell:") {
				return startGUI("explorer.exe", app.Target)
			}
			return startGUI(app.Target)
		}
		return fmt.Errorf("未知桌面应用: %s", id)
	}
	return fmt.Errorf("未知桌面应用: %s", id)
}

// startGUI 启动 GUI 程序并立即脱离；不分配新控制台，也不把它记进多开实例表
// （桌面端通常只允许一个实例，停止应由用户自己关窗口）。
func startGUI(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}
