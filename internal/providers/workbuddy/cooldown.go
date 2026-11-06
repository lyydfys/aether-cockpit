package workbuddy

// 错误 → 冷却时长映射（纯函数 + 表驱动）。
//
// 规则来自对官方客户端风控/限流行为的离线比对（证据见 docs/ANTI_BAN.md）：
//
//	11140, 11142                          → 30 分钟（异常行为/风控）
//	14015, HTTP 401                       → 30 分钟（登录态过期）
//	14001,14002,14012,14013,14014,14018   → 60 分钟（空响应/无效请求）
//	14003,10105,6005,6006,6007,6008       → 1440 分钟（长周期额度上限）
//	quota_request_limit                   → 10 分钟（请求频控窗口）
//
// 冷却只作用于**当前账号**（由 ChatStream 调 env.MarkCoolingDown），到期自动
// 恢复；它不是绕过校验，只是把"同一个号连续撞墙"变成"换号/等窗口"。

import (
	"net/http"
	"strings"
	"time"
)

func wbCooldown(status int, raw string) (time.Duration, string) {
	code, message := parseUpstreamError(raw)
	lower := strings.ToLower(raw + " " + message)

	switch {
	case code == "11140" || code == "11142":
		return 30 * time.Minute, "WorkBuddy 风控/异常行为（" + code + "），账号冷却 30 分钟"
	case code == "14015":
		return 30 * time.Minute, "WorkBuddy 登录态过期（14015），账号冷却 30 分钟"
	case status == http.StatusUnauthorized:
		return 30 * time.Minute, "WorkBuddy 鉴权失败（401），账号冷却 30 分钟"
	case isWBEmptyCode(code):
		return 60 * time.Minute, "WorkBuddy 上游空响应/无效请求（" + code + "），账号冷却 60 分钟"
	case isWBMeterLimitCode(code):
		return 1440 * time.Minute, "WorkBuddy 长周期额度上限（" + code + "），账号冷却 24 小时"
	case strings.Contains(lower, "quota_request_limit"):
		return 10 * time.Minute, "WorkBuddy 请求频控（quota_request_limit），账号冷却 10 分钟"
	}
	return 0, ""
}

// isWBEmptyCode 是"上游没返回有效内容/请求被判无效"的一组业务码。
func isWBEmptyCode(code string) bool {
	switch code {
	case "14001", "14002", "14012", "14013", "14014", "14018":
		return true
	}
	return false
}

// isWBMeterLimitCode 是长周期额度/用量上限的一组业务码。
func isWBMeterLimitCode(code string) bool {
	switch code {
	case "14003", "10105", "6005", "6006", "6007", "6008":
		return true
	}
	return false
}
