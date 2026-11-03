package main

import (
	"strings"
	"testing"
)

// 子命令分派：认得的命令要拦住（返回 true），不认得的要落回启动流程。
func TestRunCLIDispatch(t *testing.T) {
	cases := map[string]bool{
		"status":    true,
		"providers": true,
		"usage":     true,
		"quota":     true,
		"alerts":    true,
		"sessions":  true,
		"backup":    true,
	}
	for name := range cases {
		// 这些命令会真的去打 HTTP；这里只验证"是否被识别为子命令"，
		// 因此传入一个必然失败的地址，并接受它的错误路径。
		if !isSubcommand(name) {
			t.Errorf("%s 应被识别为子命令", name)
		}
	}
	for _, name := range []string{"serve", "--addr", "-config", "unknown"} {
		if isSubcommand(name) {
			t.Errorf("%s 不该被识别为子命令", name)
		}
	}
	if isSubcommand("") {
		t.Error("空参数不该是子命令")
	}
}

// --addr / --key 支持 `--x v` 与 `--x=v` 两种写法，且能取环境变量兜底。
func TestParseCLIArgs(t *testing.T) {
	c := parseCLIArgs([]string{"--addr", "192.168.1.9:8080", "--key", "k1", "--days", "7"})
	if c.addr != "http://192.168.1.9:8080" {
		t.Errorf("addr = %q", c.addr)
	}
	if c.key != "k1" {
		t.Errorf("key = %q", c.key)
	}
	if len(c.rest) != 2 || c.rest[0] != "--days" {
		t.Errorf("其余参数 = %v", c.rest)
	}

	c2 := parseCLIArgs([]string{"--addr=http://example.com:1234/", "--key=abc"})
	if c2.addr != "http://example.com:1234" {
		t.Errorf("等号写法 addr = %q", c2.addr)
	}
	if c2.key != "abc" {
		t.Errorf("等号写法 key = %q", c2.key)
	}

	// 默认值。
	c3 := parseCLIArgs(nil)
	if !strings.HasPrefix(c3.addr, "http://127.0.0.1") {
		t.Errorf("默认 addr = %q", c3.addr)
	}
}
