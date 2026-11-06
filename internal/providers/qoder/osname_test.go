package qoder

import (
	"strings"
	"testing"
)

// 同一台机器在不同接口上报的系统必须一致：
// `Cosy-MachineOS`（auth/machineOS）用的是 `x86_64_windows` 这种带架构前缀的形态，
// 请求体 os_type 与 checkin 头用的是裸平台名。两者必须能对上，否则就是
// "同一账号在不同接口声称不同系统"——最容易被风控关联的破绽。
func TestQoderOSConsistency(t *testing.T) {
	osName := qoderOSName()
	if osName != "windows" && osName != "darwin" && osName != "linux" {
		t.Fatalf("qoderOSName() = %q，取值不在预期集合内", osName)
	}
	machine := machineOS()
	if !strings.HasSuffix(machine, "_"+osName) {
		t.Fatalf("machineOS()=%q 与 qoderOSName()=%q 不一致", machine, osName)
	}
}
