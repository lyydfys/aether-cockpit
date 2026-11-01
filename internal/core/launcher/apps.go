package launcher

// DesktopApp 是本机已安装的桌面客户端，用于「一键启动」。
// 桌面端没有隔离环境变量，启动时一律使用当前 Windows 用户目录与登录态。
type DesktopApp struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	// Target 是启动目标：协议（claude://）、shell:AppsFolder\AUMID 或 exe 路径。
	Target string `json:"target,omitempty"`
	// Executable 是桌面端主程序路径（可空）。多开隔离实例需要直接带参数启动。
	Executable string `json:"executable,omitempty"`
	// Note 说明启动方式；Reason 在不可用时给出原因。
	Note   string `json:"note,omitempty"`
	Reason string `json:"reason,omitempty"`
}
