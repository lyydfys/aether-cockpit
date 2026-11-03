package gateway

import "net/http"

// PlatformCapabilities 声明宿主平台实际可用的桌面集成能力。
//
// Config.Platform 为 nil 表示桌面/无头默认：全部能力开启，保持既有行为。
// 移动运行时传入零值，从源头关掉会触碰 PC 进程、WSL 或系统电源的能力，
// 而不是只依赖前端隐藏按钮。
type PlatformCapabilities struct {
	// WSL 允许枚举/预热 WSL 发行版与会话。
	WSL bool `json:"wsl"`
	// WindowsAutoImport 允许扫描本机客户端配置并后台导入凭据。
	WindowsAutoImport bool `json:"windows_auto_import"`
	// LocalSessions 允许预热扫描本机 Agent 会话目录。
	LocalSessions bool `json:"local_sessions"`
	// KeepAwake 允许阻止系统睡眠。
	KeepAwake bool `json:"keep_awake"`
	// CLIProcessDiscovery 允许探测本机进程/命令行 Agent 版本。
	CLIProcessDiscovery bool `json:"cli_process_discovery"`
}

func resolvePlatformCapabilities(p *PlatformCapabilities) PlatformCapabilities {
	if p == nil {
		return PlatformCapabilities{
			WSL:                 true,
			WindowsAutoImport:   true,
			LocalSessions:       true,
			KeepAwake:           true,
			CLIProcessDiscovery: true,
		}
	}
	return *p
}

// writePlatformUnsupported 明确 501，而不是返回空结果让调用方误以为
// “本机确实没有可导入的内容”。
func writePlatformUnsupported(w http.ResponseWriter, feature string) {
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error": feature + " 仅支持 Windows 桌面端，当前平台无法执行该操作。",
		"code":  "platform_unsupported",
	})
}
