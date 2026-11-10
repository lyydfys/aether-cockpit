package gateway

import "testing"

// 局域网地址排序：真实网卡必须排在虚拟网卡前面。
//
// 事故背景：界面默认展示并复制**第一个**地址。某台机器上第一个是
// VMware 的 192.168.146.1（仅主机网卡），手机连它必然超时——用户看到的
// 是"局域网开了但连不上"，很难想到是地址选错了。
func TestLanAddrRankPrefersPhysicalAdapters(t *testing.T) {
	cases := []struct {
		iface string
		want  int
	}{
		// 真实网卡
		{"WLAN", 0},
		{"Wi-Fi", 0},
		{"Ethernet", 0},
		{"以太网", 0},
		// 虚拟网卡：名字里带 "ethernet" 的也必须判成虚拟
		{"vEthernet (WSL (Hyper-V firewall))", 2},
		{"VMware Network Adapter VMnet8", 2},
		{"VirtualBox Host-Only Network", 2},
		{"Hyper-V Virtual Ethernet Adapter", 2},
		{"Docker Desktop", 2},
		{"Tailscale", 2},
		// 其它
		{"LingJing", 1},
	}
	for _, c := range cases {
		if got := lanAddrRank(c.iface); got != c.want {
			t.Errorf("lanAddrRank(%q) = %d, want %d", c.iface, got, c.want)
		}
	}
}

// 回归：WSL 的 vEthernet 曾被误判成物理网卡排到第一。
func TestLanAddrRankWSLIsVirtual(t *testing.T) {
	if got := lanAddrRank("vEthernet (WSL)"); got != 2 {
		t.Fatalf("WSL 网卡应判为虚拟（2），得到 %d", got)
	}
	if got := lanAddrRank("vEthernet (WSL (Hyper-V firewall))"); got != 2 {
		t.Fatalf("WSL 网卡应判为虚拟（2），得到 %d", got)
	}
}

// 排序本身：虚拟网卡不能排在真实网卡之前（模拟界面取 [0] 的行为）。
func TestLanURLsRankingPutsPhysicalFirst(t *testing.T) {
	// 直接验证排序比较器的语义：rank 小的在前。
	order := []struct {
		iface string
		rank  int
	}{
		{"WLAN", lanAddrRank("WLAN")},
		{"vEthernet (WSL (Hyper-V firewall))", lanAddrRank("vEthernet (WSL (Hyper-V firewall))")},
		{"VMware Network Adapter VMnet1", lanAddrRank("VMware Network Adapter VMnet1")},
	}
	if order[0].rank >= order[1].rank || order[0].rank >= order[2].rank {
		t.Fatalf("真实网卡 rank(%d) 必须小于虚拟网卡 rank(%d, %d)",
			order[0].rank, order[1].rank, order[2].rank)
	}
}
