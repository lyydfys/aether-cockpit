// Command winres-icon 从 build/windows/icon.ico 生成 cmd/desktop 的 Windows 资源对象，
// 让普通的 go build 产物也带 MAC 图标（Wails CLI 打包时也会生成同样的 syso）。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

// productVersion 把 VERSION（0.1.0）转成 winres 需要的四段版本号。
func productVersion() ([4]uint16, error) {
	var out [4]uint16
	b, err := os.ReadFile("VERSION")
	if err != nil {
		return out, err
	}
	raw := strings.TrimSpace(string(b))
	re := regexp.MustCompile(`(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:\.(\d+))?`)
	m := re.FindStringSubmatch(raw)
	if m == nil {
		return out, fmt.Errorf("VERSION 不是版本号: %q", raw)
	}
	for i := 0; i < 4 && i+1 < len(m); i++ {
		if m[i+1] == "" {
			continue
		}
		n, _ := strconv.Atoi(m[i+1])
		out[i] = uint16(n)
	}
	return out, nil
}

func main() {
	icoPath := filepath.Join("build", "windows", "icon.ico")
	f, err := os.Open(icoPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open icon:", err)
		os.Exit(1)
	}
	defer f.Close()
	ico, err := winres.LoadICO(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load ico:", err)
		os.Exit(1)
	}
	rs := winres.ResourceSet{}
	if err := rs.SetIcon(winres.RT_ICON, ico); err != nil {
		fmt.Fprintln(os.Stderr, "set icon:", err)
		os.Exit(1)
	}
	// 版本资源：从仓库根 VERSION 读取产品版本，写成 Windows VERSIONINFO。
	if ver, err := productVersion(); err == nil {
		vi := version.Info{FileVersion: ver, ProductVersion: ver}
		lang := uint16(version.LangDefault)
		_ = vi.Set(lang, version.CompanyName, "Aether Cockpit")
		_ = vi.Set(lang, version.ProductName, "Aether Cockpit")
		_ = vi.Set(lang, version.FileDescription, "Aether Cockpit · AI 算力聚合网关")
		_ = vi.Set(lang, version.InternalName, "AetherCockpit")
		_ = vi.Set(lang, version.OriginalFilename, "AetherCockpit.exe")
		_ = vi.Set(lang, version.FileVersion, fmt.Sprintf("%d.%d.%d.%d", ver[0], ver[1], ver[2], ver[3]))
		_ = vi.Set(lang, version.ProductVersion, fmt.Sprintf("%d.%d.%d.%d", ver[0], ver[1], ver[2], ver[3]))
		rs.SetVersionInfo(vi)
	}
	out := filepath.Join("cmd", "desktop", "rsrc_windows_amd64.syso")
	w, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create syso:", err)
		os.Exit(1)
	}
	defer w.Close()
	if err := rs.WriteObject(w, winres.ArchAMD64); err != nil {
		fmt.Fprintln(os.Stderr, "write syso:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", out)
}
