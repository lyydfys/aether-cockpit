package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// 网关密钥的生成接口。
//
// 为什么要有它：设置页原先只允许手填密钥，用户要么自己编一个弱口令，
// 要么干脆不设（于是局域网永远打不开）。移动端一直是"服务端生成、只在
// 创建那一次回显明文"的做法（见 caller-keys 的 add-key），桌面端这里补齐。

const gatewayKeyPrefix = "sk-aether-"

// newGatewayKey 生成一个网关密钥：sk-aether- + 32 位十六进制（128 位熵）。
//
// 与概览页前端生成的口径保持一致（同为 sk-aether-<32hex>），这样无论是
// 点概览页的"生成"还是设置页的"生成"，拿到的密钥形态都一样。
func newGatewayKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 拿不到系统熵时退回时间派生：够长、唯一，但不可预测性弱，属于兜底。
		sum := []byte(time.Now().Format("20060102150405.000000000"))
		return gatewayKeyPrefix + hex.EncodeToString(sum)[:32]
	}
	return gatewayKeyPrefix + hex.EncodeToString(b)
}

// handleGatewayKeyGenerate POST /api/settings/gateway-key
//
// 生成并立即启用一个新密钥，明文只在这一次响应里返回（之后 GET /api/settings
// 只给打码值）。返回值同时带 masked，前端可以立刻更新"已设置"提示。
func (g *Gateway) handleGatewayKeyGenerate(w http.ResponseWriter, r *http.Request) {
	key := newGatewayKey()

	g.SetGatewayKey(key)
	g.settingsMu.Lock()
	g.settings.APIKey = key
	err := g.saveSettings()
	g.settingsMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		// api_key 是明文，仅此一次。
		"api_key":        key,
		"api_key_masked": maskGatewayKey(key),
		"api_key_set":    true,
	})
}

// gatewayKeyPlausible 供 POST /api/settings 校验手填密钥：太短的直接拒绝。
//
// 交互式手填常见错误是"随手敲几个字母"，那种密钥等于没有；后端提前拦掉，
// 比事后被扫端口才发现强。生成接口不受此限制（它一定足够长）。
func gatewayKeyPlausible(key string) bool {
	return len(strings.TrimSpace(key)) >= 16
}

// handleGatewayKeyReveal GET /api/settings/gateway-key/reveal
//
// 回显当前网关密钥明文，供本机用户"看一眼再复制"。
//
// 为什么需要：密钥以前只有两种状态——生成时回显一次、之后永不可见。
// 用户过两天想填到另一台设备上，就只能重新轮换一次（还连带把已在用的
// 客户端全部踢下线）。打码值又不能复制（复制到的是省略号）。
//
// 安全边界：只允许本机（127.0.0.1 / ::1）访问，且拒绝任何带转发头的请求。
// 局域网/公网访问永远拿不到明文——那等同于把门锁交出去。
func (g *Gateway) handleGatewayKeyReveal(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "网关密钥明文只允许在本机查看",
		})
		return
	}
	key := g.GatewayKey()
	if key == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "还没有设置网关密钥"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"api_key":        key,
		"api_key_masked": maskGatewayKey(key),
		"api_key_set":    true,
	})
}
