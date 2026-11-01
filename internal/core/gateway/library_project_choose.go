package gateway

import (
	"aether-cockpit/internal/core/executil"
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// 项目目录选择（插件 projects/choose）：桌面版弹系统文件夹选择框，
// 返回 {dir}；取消返回空串，浏览器版不会调用这里（手输路径）。

// handleProjectChoose POST /api/library/projects/choose
func (g *Gateway) handleProjectChoose(w http.ResponseWriter, r *http.Request) {
	pick := g.pickFolder
	if pick == nil {
		pick = pickFolderDialog
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	dir, err := pick(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "无法打开目录选择器: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dir": strings.TrimSpace(dir)})
}

// pickFolderDialog 用 PowerShell 的 FolderBrowserDialog 选目录。
// 只在 Windows 桌面版可用；其它系统明确报错，让页面回到手输路径。
func pickFolderDialog(ctx context.Context) (string, error) {
	if runtime.GOOS != "windows" {
		return "", fmt.Errorf("当前系统请在输入框里直接填写路径")
	}
	script := `Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = 'Add a project folder'
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write($d.SelectedPath) }`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA", "-WindowStyle", "Hidden", "-Command", script)
	executil.Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
