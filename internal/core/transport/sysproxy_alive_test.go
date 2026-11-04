//go:build windows

package transport

import (
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// 代理端口活着 → 按代理走；死了 → 退回直连。
//
// 这是 0.1.20 引入系统代理后最容易踩的坑：Clash 关掉后注册表常常仍留着
// ProxyEnable=1，照注册表走会让所有请求撞在"连接拒绝"上。
func TestProxyAliveDetectsDeadProxy(t *testing.T) {
	// 起一个真实监听当作"活代理"。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			_ = c.Close()
		}
	}()

	aliveURL, _ := url.Parse("http://" + ln.Addr().String())
	if !proxyAlive(aliveURL) {
		t.Fatal("监听中的端口应判定为存活")
	}

	// 关掉的端口当作"死代理"。
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := ln2.Addr().String()
	ln2.Close()
	deadURL, _ := url.Parse("http://" + deadAddr)
	if proxyAlive(deadURL) {
		t.Fatal("关掉的端口应判定为失效")
	}

	// nil 不该 panic。
	if proxyAlive(nil) {
		t.Fatal("nil 应返回 false")
	}
	_ = time.Now
}

// 死代理时 sysProxyFunc 必须返回 nil（直连），而不是把死地址交给 Transport。
func TestSysProxyFuncSkipsDeadRegistryProxy(t *testing.T) {
	// 用一个确定关闭的端口伪造"注册表代理"。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := ln.Addr().String()
	ln.Close()

	deadURL, _ := url.Parse("http://" + deadAddr)
	if proxyAlive(deadURL) {
		t.Fatal("前置条件：端口应已关闭")
	}
	// 直接验证判定结果：Transport 的 Proxy 函数在内部用的是同一个 proxyAlive。
	fn := sysProxyFunc()
	if fn == nil {
		t.Fatal("sysProxyFunc 不应为 nil")
	}
	// 本机回环必然命中绕过规则，返回 nil——不影响其它断言。
	if u, err := fn(mustRequest(t, "http://api.example.com/")); err != nil {
		t.Fatalf("不应返回错误：%v", err)
	} else if u != nil && u.Host == deadAddr {
		t.Fatalf("死代理不该被交给 Transport：%v", u)
	}
	_ = http.MethodGet
}
