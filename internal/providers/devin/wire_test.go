package devin

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func appendTestBytes(dst []byte, num protowire.Number, b []byte) []byte {
	dst = protowire.AppendTag(dst, num, protowire.BytesType)
	return protowire.AppendBytes(dst, b)
}

func appendTestVarint(dst []byte, num protowire.Number, v uint64) []byte {
	dst = protowire.AppendTag(dst, num, protowire.VarintType)
	return protowire.AppendVarint(dst, v)
}

func TestConnectEnvelopeFraming(t *testing.T) {
	payload := []byte("hello devin connect-rpc")
	envelope := WrapConnectEnvelope(payload)
	if len(envelope) != 5+len(payload) {
		t.Fatalf("envelope len=%d", len(envelope))
	}
	if envelope[0] != ConnectFlagData {
		t.Fatalf("flag=0x%02x", envelope[0])
	}
	flag, got, err := ReadConnectFrame(bytes.NewReader(envelope))
	if err != nil {
		t.Fatalf("ReadConnectFrame: %v", err)
	}
	if flag != ConnectFlagData || !bytes.Equal(got, payload) {
		t.Fatalf("roundtrip mismatch: flag=%d payload=%q", flag, got)
	}
}

func TestGenerateDevinDeviceFingerprint(t *testing.T) {
	fp1 := GenerateDevinDeviceFingerprint("seed-1")
	if len(fp1) != devinFingerprintHexLen {
		t.Fatalf("len=%d", len(fp1))
	}
	for _, c := range fp1 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("invalid hex char %c", c)
		}
	}
	if fp2 := GenerateDevinDeviceFingerprint("seed-1"); fp1 != fp2 {
		t.Fatal("同 seed 指纹应一致")
	}
	if fp3 := GenerateDevinDeviceFingerprint("seed-2"); fp3 == fp1 {
		t.Fatal("不同 seed 指纹不应一致")
	}
	if fp4 := GenerateDevinDeviceFingerprint(""); len(fp4) != devinFingerprintHexLen || fp4 == fp1 {
		t.Fatal("空 seed 应随机且长度正确")
	}
}

func TestGenerateDevinSentryTrace(t *testing.T) {
	tr := GenerateDevinSentryTrace()
	parts := strings.Split(tr, "-")
	if len(parts) != 3 || len(parts[0]) != 32 || len(parts[1]) != 16 || parts[2] != "1" {
		t.Fatalf("sentry trace 形态不符: %q", tr)
	}
	if tr == GenerateDevinSentryTrace() {
		t.Fatal("两次应不同")
	}
}

func TestBuildDevinGetChatMessageRequest(t *testing.T) {
	prompts := []devinPrompt{
		{MessageID: "msg-1", Source: 1, Content: "hello", Images: []devinImage{{Base64Data: "iVBORw0KGgo", MimeType: "image/png"}}},
		{MessageID: "msg-2", Source: 2, Content: "hi there", Thinking: "thinking step", Signature: []byte("sealed.v1.test")},
		{MessageID: "msg-3", Source: 4, Content: `{"result":"ok"}`, ToolCallID: "call-1"},
	}
	tools := []devinTool{{Name: "get_weather", Description: "lookup weather", Parameters: []byte(`{"type":"object"}`)}}
	temp := 0.7
	req := buildDevinGetChatMessageRequest("token-123", "device-seed-1", "swe-2-high",
		"you are a helpful assistant", prompts, tools, &temp, 4000, "session-x", "cascade-x")

	if len(req) == 0 {
		t.Fatal("编码结果为空")
	}
	for _, want := range []string{
		"you are a helpful assistant", "hello", "hi there", "thinking step",
		`{"result":"ok"}`, "get_weather", "lookup weather", "swe-2-high",
		"session-x", "cascade-x", "iVBORw0KGgo", "image/png",
	} {
		if !bytes.Contains(req, []byte(want)) {
			t.Fatalf("编码结果缺少 %q", want)
		}
	}
}

func TestBuildRequestTurnIndexIncrements(t *testing.T) {
	first := buildDevinGetChatMessageRequest("tok", "seed", "swe-2-high", "sys",
		[]devinPrompt{{Source: 1, Content: "hello"}}, nil, nil, 100, "sess-turn", "sess-turn")
	second := buildDevinGetChatMessageRequest("tok", "seed", "swe-2-high", "sys",
		[]devinPrompt{{Source: 1, Content: "hello"}}, nil, nil, 100, "sess-turn", "sess-turn")

	if turn := field15Turn(t, first); turn != 0 {
		t.Fatalf("首轮 turn=%d", turn)
	}
	if turn := field15Turn(t, second); turn != 1 {
		t.Fatalf("第二轮 turn=%d", turn)
	}
}

// field15Turn 读出 Field 15 子消息里的 Field 2（turn index），缺省 0。
func field15Turn(t *testing.T, req []byte) uint64 {
	t.Helper()
	pos := 0
	for pos < len(req) {
		num, typ, n := protowire.ConsumeTag(req[pos:])
		if n <= 0 {
			break
		}
		pos += n
		switch typ {
		case protowire.BytesType:
			val, bn := protowire.ConsumeBytes(req[pos:])
			if bn <= 0 {
				t.Fatalf("bytes err")
			}
			pos += bn
			if num == 15 {
				ipos := 0
				var turn uint64
				for ipos < len(val) {
					in, it, inn := protowire.ConsumeTag(val[ipos:])
					if inn <= 0 {
						break
					}
					ipos += inn
					if it == protowire.VarintType {
						v, vn := protowire.ConsumeVarint(val[ipos:])
						if vn <= 0 {
							break
						}
						ipos += vn
						if in == 2 {
							turn = v
						}
					} else {
						skip := protowire.ConsumeFieldValue(in, it, val[ipos:])
						if skip <= 0 {
							break
						}
						ipos += skip
					}
				}
				return turn
			}
		default:
			skip := protowire.ConsumeFieldValue(num, typ, req[pos:])
			if skip <= 0 {
				t.Fatalf("skip err")
			}
			pos += skip
		}
	}
	return 0
}

func TestParseDevinFrame(t *testing.T) {
	var payload []byte
	payload = appendTestBytes(payload, 1, []byte("bot-uuid-123"))
	payload = appendTestBytes(payload, 3, []byte("Hello world"))
	payload = appendTestBytes(payload, 9, []byte("Let me think..."))
	payload = appendTestBytes(payload, 10, []byte("CAQS-signature-bytes"))
	payload = appendTestBytes(payload, 21, []byte("anthropic"))
	payload = appendTestVarint(payload, 5, 10)
	var tc []byte
	tc = appendTestBytes(tc, 1, []byte("call-1"))
	tc = appendTestBytes(tc, 2, []byte("get_weather"))
	tc = appendTestBytes(tc, 3, []byte(`{"city":"SH"}`))
	payload = appendTestBytes(payload, 6, tc)

	res, err := ParseDevinFrame(payload)
	if err != nil {
		t.Fatalf("ParseDevinFrame: %v", err)
	}
	if res.OutputID != "bot-uuid-123" || res.ContentText != "Hello world" ||
		res.ThinkingText != "Let me think..." || string(res.DeltaSignature) != "CAQS-signature-bytes" ||
		res.DeltaSignatureType != "anthropic" || res.StopReason != 10 {
		t.Fatalf("字段解析不符: %+v", res)
	}
	if len(res.ToolCallDeltas) != 1 || res.ToolCallDeltas[0].ID != "call-1" ||
		res.ToolCallDeltas[0].Name != "get_weather" || res.ToolCallDeltas[0].Arguments != `{"city":"SH"}` {
		t.Fatalf("工具增量不符: %+v", res.ToolCallDeltas)
	}
}

func TestParseDevinUsageAndDimensionGroups(t *testing.T) {
	var usage []byte
	usage = appendTestVarint(usage, 2, 11)
	usage = appendTestVarint(usage, 3, 7)
	usage = appendTestVarint(usage, 5, 3)
	usage = appendTestBytes(usage, 9, []byte("swe-2-high"))
	u := parseDevinUsageField(usage)
	if u.PromptTokens != 11 || u.CompletionTokens != 7 || u.CachedTokens != 3 || u.ModelName != "swe-2-high" {
		t.Fatalf("用量解析不符: %+v", u)
	}

	var metric []byte
	metric = appendTestBytes(metric, 5, []byte("input_tokens"))
	var detail []byte
	detail = protowire.AppendTag(detail, 2, protowire.Fixed32Type)
	detail = protowire.AppendFixed32(detail, math.Float32bits(120))
	metric = appendTestBytes(metric, 4, detail)
	var group []byte
	group = appendTestBytes(group, 1, []byte("Token Usage"))
	group = appendTestBytes(group, 2, metric)
	pt, _, _, found := ParseDevinResponseDimensionGroups(group)
	if !found || pt != 120 {
		t.Fatalf("dimension group 解析不符: found=%v pt=%d", found, pt)
	}
}

func TestParseDevinTrailerError(t *testing.T) {
	cases := []struct {
		body string
		want int
	}{
		{`{"error":{"code":"failed_precondition","message":"User monthly ACU quota exhausted"}}`, 429},
		{`{"error":{"code":"unauthenticated","message":"Invalid or expired session token"}}`, 401},
		{`{"error":{"code":"invalid_argument","message":"an internal error occurred"}}`, 502},
		{`{"error":{"code":"resource_exhausted","message":"rate limited"}}`, 429},
		{`{}`, 0},
	}
	for _, tc := range cases {
		code, err := ParseDevinTrailerError([]byte(tc.body))
		if tc.want == 0 {
			if err != nil {
				t.Fatalf("%s: 期望无错误", tc.body)
			}
			continue
		}
		if err == nil || code != tc.want {
			t.Fatalf("%s: code=%d err=%v want %d", tc.body, code, err, tc.want)
		}
	}
}

func TestUTF8SplitBuffer(t *testing.T) {
	b := &UTF8SplitBuffer{}
	text := "你好世界"
	raw := []byte(text)
	var got strings.Builder
	for i := 0; i < len(raw); i += 2 {
		end := i + 2
		if end > len(raw) {
			end = len(raw)
		}
		got.WriteString(b.Feed(raw[i:end]))
	}
	if got.String() != text {
		t.Fatalf("UTF-8 跨帧拼接失败: %q", got.String())
	}
}
