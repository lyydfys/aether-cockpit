package spec

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// SignInput 是一次签名所需的上下文。
type SignInput struct {
	Method    string
	Path      string
	Body      []byte
	DeviceID  string
	Timestamp time.Time
}

// Sign 按规则计算签名，返回签名值与应当上送的时间戳字符串。
//
// 设计意图：算法（机制）在代码里，参数（策略）在 JSON 里。
// 上游改盐值时只需更新 JSON，改算法时才需要动代码 —— 后者极其罕见。
func (r *SignatureRule) Sign(in SignInput) (signature string, timestampValue string, err error) {
	if r == nil || r.Algorithm == "" || r.Algorithm == "none" {
		return "", "", nil
	}

	ts := in.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	var tsStr string
	if r.TimestampUnit == "ms" {
		tsStr = fmt.Sprintf("%d", ts.UnixMilli())
	} else {
		tsStr = fmt.Sprintf("%d", ts.Unix())
	}

	payload := r.expandPayload(in, tsStr)
	key := r.keyBytes()

	switch strings.ToLower(r.Algorithm) {
	case "hmac_sha256":
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(payload))
		return hex.EncodeToString(mac.Sum(nil)), tsStr, nil

	case "sha256":
		h := sha256.New()
		h.Write(key)
		h.Write([]byte(payload))
		return hex.EncodeToString(h.Sum(nil)), tsStr, nil

	case "md5":
		h := md5.New()
		h.Write(key)
		h.Write([]byte(payload))
		return hex.EncodeToString(h.Sum(nil)), tsStr, nil

	default:
		return "", "", fmt.Errorf("spec: 不支持的签名算法 %q", r.Algorithm)
	}
}

// expandPayload 用极简占位符替换构造待签名串。
func (r *SignatureRule) expandPayload(in SignInput, tsStr string) string {
	tmpl := r.PayloadTemplate
	if tmpl == "" {
		// 没有模板时的通用默认：method\npath\ntimestamp
		tmpl = "{{.Method}}\n{{.Path}}\n{{.Timestamp}}"
	}

	repl := []struct{ k, v string }{
		{"{{.Method}}", strings.ToUpper(in.Method)},
		{"{{.Path}}", in.Path},
		{"{{.Timestamp}}", tsStr},
		{"{{.DeviceID}}", in.DeviceID},
		{"{{.Body}}", string(in.Body)},
	}
	out := tmpl
	for _, p := range repl {
		out = strings.ReplaceAll(out, p.k, p.v)
	}
	return out
}

// keyBytes 返回签名密钥。
func (r *SignatureRule) keyBytes() []byte {
	if r.SaltHex != "" {
		if b, err := hex.DecodeString(strings.TrimSpace(r.SaltHex)); err == nil {
			return b
		}
		// 解不出来就按原文用，避免配置写错时静默失效。
	}
	return []byte(r.SaltPlain)
}
