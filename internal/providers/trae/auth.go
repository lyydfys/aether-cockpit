package trae

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Trae 公开客户端参数。这些值与官方客户端一致，属于公开信息。
const (
	cnChatHost = "https://trae-api-cn.mchost.guru"
	sgChatHost = "https://coresg-normal.trae.ai"
	usChatHost = "https://coreva-normal.trae.ai"

	chatPath = "/api/agent/v3/llm_utils_chat"

	appID          = "6eefa01c-1036-4c7e-9ca5-d891f63bfcd8"
	appVersion     = "default"
	appVersionCode = "20260401"
	ideVersionCN   = "3.3.99"
	ideVersionSG   = "3.5.51"

	deviceBrand = "82RF"
	deviceCPU   = "Intel"
)

// baseURL 返回账号级上游地址。
// 账号显式填 base_url 时优先使用（测试 / 自建代理）；国际渠道可通过 region=US 切到 coreva。
func (p *Provider) baseURL(env *provider.AccountEnv) string {
	if v := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/"); v != "" {
		return v
	}
	if p.region == "CN" {
		return cnChatHost
	}
	if strings.EqualFold(strings.TrimSpace(env.GetCredential("region")), "US") {
		return usChatHost
	}
	return sgChatHost
}

// ideVersion 返回该区域官方客户端版本。
func (p *Provider) ideVersion() string {
	if p.region == "CN" {
		return ideVersionCN
	}
	return ideVersionSG
}

// accessToken 读取账号 token，兼容 token 与 access_token 两种键名。
func accessToken(env *provider.AccountEnv) string {
	if v := strings.TrimSpace(env.GetCredential("token")); v != "" {
		return v
	}
	return strings.TrimSpace(env.GetCredential("access_token"))
}

func missingTokenError(providerID string) error {
	return ir.NewAPIError(ir.ErrCodeUpstreamAuth,
		"Trae 账号缺少 token；请重新导入本机登录态或手动粘贴登录 JWT").
		WithProvider(providerID)
}

// hashDeviceID 复刻官方客户端的 32 位哈希：machineId -> 19 位十进制设备号。
// JS 里 hash 用 int32 溢出语义，这里按 uint32 位运算保持一致。
func hashDeviceID(machineID string) string {
	if machineID == "" {
		return ""
	}
	var hash int32
	for _, r := range machineID {
		hash = (hash << 5) - hash + int32(r)
	}
	n := hash
	if n < 0 {
		n = -n
	}
	return fmt.Sprintf("%019d", uint64(n))
}

// randomHex 生成 n 字节随机数的十六进制串。
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 在全平台都可用；极端失败时退化为时间哈希，保证请求头非空。
		sum := sha256.Sum256([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
		return hex.EncodeToString(sum[:n])
	}
	return hex.EncodeToString(buf)
}

// randomUUID 生成 RFC 4122 v4 UUID。
func randomUUID() string {
	h := randomHex(16)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// parseExpiry 兼容 RFC3339 与 Unix 秒/毫秒字符串。
func parseExpiry(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
		if n > 1e12 {
			n /= 1000
		}
		return time.Unix(n, 0)
	}
	return time.Time{}
}

// expiryFromAny 处理字符串/数字两种 expiredAt 形态。
func expiryFromAny(v any) time.Time {
	switch x := v.(type) {
	case string:
		return parseExpiry(x)
	case float64:
		n := int64(x)
		if n > 1e12 {
			n /= 1000
		}
		return time.Unix(n, 0)
	case json.Number:
		if n, err := x.Int64(); err == nil && n > 0 {
			if n > 1e12 {
				n /= 1000
			}
			return time.Unix(n, 0)
		}
	}
	return time.Time{}
}

// rawString 读取 JSON 字符串原文，不裁剪空白（流式文本增量必须逐字保留）。
func rawString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// anyString 把 JSON 中的字符串/数字统一成字符串。
func anyString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// anyInt 把 JSON 数字（float64/json.Number/字符串）转成 int。
func anyInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return n
		}
	}
	return 0
}

// anyCode 把错误码统一成整数。
func anyCode(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return n
		}
	}
	return 0
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
