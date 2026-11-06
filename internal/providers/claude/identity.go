package claude

// 订阅流量的"身份三件套"。
//
// 真实 Claude Code CLI 每次请求都带一组互相自洽的身份字段（来源：对官方
// CLI 的抓包与公开逆向资料）：
//
//	X-Claude-Code-Session-Id: {会话 UUID}
//	x-client-request-id:      {请求 UUID}
//	metadata.user_id:         {"device_id":"<64hex>","account_uuid":"<uuid>","session_id":"<uuid>"}
//
// 少这些字段本身不比"字段写错"更安全：接第三方网关时，上游能看到的是
// "一个声称是 CLI 的客户端，却没有任何设备/会话标识"，这比设备 ID 不一致
// 更容易被归到异常样本。这里按账号派生**稳定**的设备/账号标识，按会话派生
// 稳定的 session，只有 request id 每次随机——与真实客户端的行为一致。
//
// 只用于 OAuth 订阅模式；API Key 模式不注入（官方 API 不要求这些字段，
// 多塞反而像伪造）。

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// uuidFromHex 把任意字符串的哈希压成 UUID 文本形态。
//
// 真实客户端用 randomUUID()，形如 8-4-4-4-12。我们不需要真随机：同一账号
// 必须得到同一个值，否则每次请求都换设备 = 典型的"账号共享"信号。
func uuidFromHex(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	h := hex.EncodeToString(sum[:16])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// hex64From 返回 64 位十六进制（Device ID 的形态：randomBytes(32).toString("hex")）。
func hex64From(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// claudeIdentity 是一组自洽的身份标识。
type claudeIdentity struct {
	DeviceID    string
	AccountUUID string
	SessionUUID string
}

// buildClaudeIdentity 按账号 + 机器 + 会话派生身份。
//
// 三个字段的来源刻意不同：
//   - device_id 绑定"账号 + 本机"，换机会变（与真实客户端一致）；
//   - account_uuid 只绑定账号；
//   - session_uuid 只绑定会话（跨轮稳定，换会话就变）。
func buildClaudeIdentity(accountID, machineID, sessionKey string) claudeIdentity {
	base := accountID + "|" + machineID
	return claudeIdentity{
		DeviceID:    hex64From("device|" + base),
		AccountUUID: uuidFromHex("account|" + accountID),
		SessionUUID: uuidFromHex("session|" + base + "|" + sessionKey),
	}
}

// applyClaudeIdentity 把身份写进请求体 metadata，并返回会话 UUID 供请求头使用。
//
// metadata.user_id 在真实客户端里是**JSON 字符串**（不是对象）：上游按字符串
// 解析。写错形态可能直接 400，所以这里显式 Marshal 再放进字符串。
func applyClaudeIdentity(body map[string]any, id claudeIdentity) {
	if body == nil {
		return
	}
	payload := map[string]string{
		"device_id":    id.DeviceID,
		"account_uuid": id.AccountUUID,
		"session_id":   id.SessionUUID,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	meta, _ := body["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["user_id"] = string(raw)
	body["metadata"] = meta
}

// newRequestUUID 生成一个真随机的 UUID v4（每请求一个，与真实客户端一致）。
// 请求 ID 必须每请求不同：复用同一个 ID 会被上游当成重放/重复请求。
func newRequestUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
