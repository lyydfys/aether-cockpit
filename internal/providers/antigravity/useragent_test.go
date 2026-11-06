package antigravity

import (
	"runtime"
	"strings"
	"testing"
)

// UA 必须反映本机平台：写死 darwin/arm64 会造成"登录在 Windows、调用自称 macOS"
// 的设备身份矛盾（风控最容易抓的不一致）。
func TestAntigravityUserAgentMatchesHost(t *testing.T) {
	ua := antigravityUserAgent()
	if !strings.HasPrefix(ua, "antigravity/hub/2.24.0 (aidev_client; os_type=") {
		t.Fatalf("UA 前缀不对：%q", ua)
	}
	if !strings.Contains(ua, "cl=1000335682") {
		t.Fatalf("UA 缺少 client build id：%q", ua)
	}
	wantOS := map[string]string{"windows": "windows", "darwin": "darwin", "linux": "linux"}[runtime.GOOS]
	if wantOS == "" {
		wantOS = strings.ToLower(runtime.GOOS)
	}
	if !strings.Contains(ua, "os_type="+wantOS+";") {
		t.Fatalf("UA 的 os_type 与运行平台不符：%q（want %s）", ua, wantOS)
	}
	wantArch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
	if wantArch == "" {
		wantArch = runtime.GOARCH
	}
	if !strings.Contains(ua, "arch="+wantArch+";") {
		t.Fatalf("UA 的 arch 与运行平台不符：%q（want %s）", ua, wantArch)
	}
}
