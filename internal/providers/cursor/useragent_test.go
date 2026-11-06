package cursor

import (
	"runtime"
	"strings"
	"testing"
)

// UA 必须反映本机平台：写死 Windows 会让 macOS/Linux 上的网关"自称 Windows"。
func TestCursorUserAgentMatchesHost(t *testing.T) {
	ua := cursorUserAgent()
	if !strings.HasPrefix(ua, "Mozilla/5.0 (") || !strings.Contains(ua, "Chrome/") {
		t.Fatalf("UA 形态不对：%q", ua)
	}
	var want string
	switch runtime.GOOS {
	case "windows":
		want = "Windows NT 10.0"
	case "darwin":
		want = "Macintosh"
	default:
		want = "X11; Linux"
	}
	if !strings.Contains(ua, want) {
		t.Fatalf("UA 平台标识与运行平台不符：%q（want 含 %q）", ua, want)
	}
}
