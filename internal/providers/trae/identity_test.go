package trae

import (
	"net/http"
	"runtime"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

// 机器标识必须非空且稳定：空字符串头比"没有这个头"更显眼。
// 导入账号可能只有 token，没有 machine_id —— 这时要用网关派生的稳定指纹兜底。
func TestDeviceIdentityHeadersNonEmpty(t *testing.T) {
	env := provider.NewAccountEnv("acc-trae", "trae-cn")
	env.Credentials = map[string]string{"token": "tok", "user_id": "u-1"}
	env.Fingerprint = provider.DeviceFingerprint{MachineID: "fp-machine-abc"}

	h := http.Header{}
	p := &Provider{}
	p.applyHeaders(h, env, "tok")

	machine := h.Get("x-machine-id")
	device := h.Get("x-device-id")
	if strings.TrimSpace(machine) == "" {
		t.Fatal("x-machine-id 不能为空")
	}
	if strings.TrimSpace(device) == "" {
		t.Fatal("x-device-id 不能为空")
	}
	if machine != "fp-machine-abc" {
		t.Fatalf("缺 machine_id 时应退回指纹：%q", machine)
	}

	// 同一账号两次必须一致（稳定），否则每次请求都像换了一台设备。
	h2 := http.Header{}
	p.applyHeaders(h2, env, "tok")
	if h2.Get("x-machine-id") != machine || h2.Get("x-device-id") != device {
		t.Fatal("设备标识必须稳定，不能每次变化")
	}

	// 显式配置优先。
	env2 := provider.NewAccountEnv("acc-trae2", "trae-cn")
	env2.Credentials = map[string]string{"token": "tok", "machine_id": "real-machine", "device_id": "real-device"}
	env2.Fingerprint = provider.DeviceFingerprint{MachineID: "fp-should-not-win"}
	h3 := http.Header{}
	p.applyHeaders(h3, env2, "tok")
	if h3.Get("x-machine-id") != "real-machine" || h3.Get("x-device-id") != "real-device" {
		t.Fatalf("显式凭证应优先：machine=%q device=%q", h3.Get("x-machine-id"), h3.Get("x-device-id"))
	}
}

// 平台字段必须反映本机：写死 Windows 会让 macOS/Linux 上的网关"自称 Windows 10"，
// 与登录时上报的真实系统冲突。
func TestPlatformMatchesHost(t *testing.T) {
	want := map[string]string{"windows": "windows", "darwin": "darwin", "linux": "linux"}[runtime.GOOS]
	if want == "" {
		want = runtime.GOOS
	}
	if got := traeDeviceType(); got != want {
		t.Fatalf("traeDeviceType() = %q，want %q", got, want)
	}
	if strings.TrimSpace(traeOSVersion()) == "" {
		t.Fatal("traeOSVersion() 不能为空")
	}
	if runtime.GOOS == "windows" && traeOSVersion() != "Windows 10" {
		t.Fatalf("Windows 上应保持抓包原值 Windows 10，得到 %q", traeOSVersion())
	}
}
