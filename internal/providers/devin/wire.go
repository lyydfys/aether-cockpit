package devin

// Devin CLI 的 Connect-RPC 线协议：请求编码、帧封装与响应解码。
// 协议依据 代理服务进程 internal/runtime/executor/helps/devin_wire.go（逐段移植，
// 去掉敏感词混淆与日志辅助，只保留收发所需）。

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	// ConnectFlagData 未压缩的数据帧。
	ConnectFlagData byte = 0x00
	// ConnectFlagCompressed gzip 压缩的数据帧。
	ConnectFlagCompressed byte = 0x01
	// ConnectFlagEndStream 终止帧（trailer）。
	ConnectFlagEndStream byte = 0x02

	// devinDefaultBaseURL 上游 Codeium/Devin 推理后端。
	devinDefaultBaseURL = "https://server.codeium.com"
	// devinChatPath Connect-RPC 聊天端点。
	devinChatPath = "/exa.api_server_pb.ApiServerService/GetChatMessage"

	devinDefaultClientName    = "chisel"
	devinDefaultClientVersion = "3000.10.21"
	// devinFingerprintHexLen 元数据 #31 设备指纹的固定长度。
	devinFingerprintHexLen = 732

	// devinDefaultMaxTokens 缺省补全上限。
	devinDefaultMaxTokens = 128000

	devinMaxConnectFrameSize      = 16 * 1024 * 1024
	devinMaxDecompressedFrameSize = 64 * 1024 * 1024
)

// devinTool 是 GetChatMessageRequest 里的工具声明。
type devinTool struct {
	Name        string
	Description string
	Parameters  []byte
}

// devinToolCall 是历史里 assistant 发起的工具调用。
type devinToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// devinToolCallDelta 是流式响应 Field 6 的工具调用增量。
type devinToolCallDelta struct {
	ID             string
	Name           string
	Arguments      string
	InvalidJSONStr string
	InvalidJSONErr string
}

// devinImage 是用户消息里的图片附件（Prompt #10）。
type devinImage struct {
	Base64Data string
	MimeType   string
}

// devinPrompt 是请求历史中的一轮（repeated Field 3）。
// Source：1=用户 2=助手 4=工具结果。
type devinPrompt struct {
	MessageID  string
	Source     int
	Content    string
	Images     []devinImage
	ToolCalls  []devinToolCall
	ToolCallID string
	Thinking   string
	Signature  []byte
}

// devinUsage 是响应 Field 7 的用量。
type devinUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
	CacheWriteTokens int64
	StatusCode       uint64
	RequestID        string
	ModelName        string
}

// devinFrameResult 是单帧解码结果。
type devinFrameResult struct {
	OutputID                string
	ContentText             string
	StopReason              uint64 // 2/4=stop，10=tool_calls
	ToolCallDeltas          []devinToolCallDelta
	ThinkingText            string
	DeltaSignature          []byte
	DeltaSignatureType      string
	MessageID               string
	Usage                   *devinUsage
	ResponseDimensionGroups [][]byte
}

// GenerateDevinDeviceFingerprint 生成 732 字符的十六进制设备指纹：
// seed 为空时每次随机（对齐原生 devin-cli），否则确定性派生。
func GenerateDevinDeviceFingerprint(seed string) string {
	if seed == "" {
		var b [devinFingerprintHexLen / 2]byte
		if _, err := rand.Read(b[:]); err == nil {
			return hex.EncodeToString(b[:])
		}
		seed = uuid.New().String()
	}
	var sb strings.Builder
	counter := 0
	for sb.Len() < devinFingerprintHexLen {
		h := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", seed, counter)))
		sb.WriteString(hex.EncodeToString(h[:]))
		counter++
	}
	return sb.String()[:devinFingerprintHexLen]
}

// GenerateDevinSentryTrace 生成 "<32hex>-<16hex>-1" 的 Sentry 追踪头。
func GenerateDevinSentryTrace() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		u1 := strings.ReplaceAll(uuid.New().String(), "-", "")
		u2 := strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
		return u1 + "-" + u2 + "-1"
	}
	return hex.EncodeToString(b[:16]) + "-" + hex.EncodeToString(b[16:]) + "-1"
}

// 会话轮次计数器：Field 15.2 是进程内按会话单调递增的请求序号，首轮 0 不上线。
// 表按参考实现设上限，超限整体清空（只影响一次计数，不影响正确性）。
var (
	devinSessionTurns   sync.Map // string -> *atomic.Uint64
	devinSessionTurnCap = 4096
	devinSessionTurnN   atomic.Int64
)

func nextDevinSessionTurnIndex(sessionID string) int {
	cleanID := strings.TrimSpace(sessionID)
	if cleanID == "" {
		return 0
	}
	if _, loaded := devinSessionTurns.Load(cleanID); !loaded {
		if devinSessionTurnN.Add(1) > int64(devinSessionTurnCap) {
			devinSessionTurns.Range(func(k, _ any) bool {
				devinSessionTurns.Delete(k)
				return true
			})
			devinSessionTurnN.Store(0)
		}
	}
	v, _ := devinSessionTurns.LoadOrStore(cleanID, &atomic.Uint64{})
	return int(v.(*atomic.Uint64).Add(1) - 1)
}

// WrapConnectEnvelope 把负载包成 [flag][4 字节大端长度][payload]。
func WrapConnectEnvelope(protoBytes []byte) []byte {
	return wrapConnectEnvelopeWithFlag(ConnectFlagData, protoBytes)
}

func wrapConnectEnvelopeWithFlag(flag byte, protoBytes []byte) []byte {
	header := make([]byte, 5, 5+len(protoBytes))
	header[0] = flag
	binary.BigEndian.PutUint32(header[1:5], uint32(len(protoBytes)))
	return append(header, protoBytes...)
}

// ReadConnectFrame 从流里读一帧；压缩帧自动解压。
func ReadConnectFrame(r io.Reader) (flag byte, payload []byte, err error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	flag = header[0]
	if flag != ConnectFlagData && flag != ConnectFlagCompressed && flag != ConnectFlagEndStream && flag != (ConnectFlagCompressed|ConnectFlagEndStream) {
		return flag, nil, fmt.Errorf("invalid connect frame flag: 0x%02x", flag)
	}
	length := binary.BigEndian.Uint32(header[1:5])
	if length > devinMaxConnectFrameSize {
		return flag, nil, fmt.Errorf("connect frame length %d exceeds maximum limit (%d)", length, devinMaxConnectFrameSize)
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if flag&ConnectFlagCompressed != 0 {
		gz, errGz := gzip.NewReader(bytes.NewReader(payload))
		if errGz != nil {
			return flag, nil, fmt.Errorf("decompress gzip connect frame: %w", errGz)
		}
		defer func() { _ = gz.Close() }()
		initCap := int(length) * 4
		if initCap > 4*1024*1024 {
			initCap = 4 * 1024 * 1024
		} else if initCap < 4096 {
			initCap = 4096
		}
		decompBuf := bytes.NewBuffer(make([]byte, 0, initCap))
		if _, errRead := decompBuf.ReadFrom(io.LimitReader(gz, devinMaxDecompressedFrameSize+1)); errRead != nil {
			return flag, nil, fmt.Errorf("read decompressed connect frame: %w", errRead)
		}
		if decompBuf.Len() > devinMaxDecompressedFrameSize {
			return flag, nil, fmt.Errorf("decompressed frame size exceeds maximum limit (%d)", devinMaxDecompressedFrameSize)
		}
		payload = decompBuf.Bytes()
	}
	return flag, payload, nil
}

// buildDevinClientMetadataBytes 构造 Field 1（ClientMetadata）。
func buildDevinClientMetadataBytes(sessionToken, deviceSeed, osName string) []byte {
	if osName == "" {
		osName = runtime.GOOS
	}
	fingerprint := GenerateDevinDeviceFingerprint(deviceSeed)

	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, devinDefaultClientName)
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendString(b, devinDefaultClientVersion)
	b = protowire.AppendTag(b, 3, protowire.BytesType)
	b = protowire.AppendString(b, sessionToken)
	b = protowire.AppendTag(b, 4, protowire.BytesType)
	b = protowire.AppendString(b, "en")
	b = protowire.AppendTag(b, 5, protowire.BytesType)
	b = protowire.AppendString(b, osName)
	b = protowire.AppendTag(b, 7, protowire.BytesType)
	b = protowire.AppendString(b, devinDefaultClientVersion)
	b = protowire.AppendTag(b, 12, protowire.BytesType)
	b = protowire.AppendString(b, devinDefaultClientName)
	b = protowire.AppendTag(b, 31, protowire.BytesType)
	b = protowire.AppendString(b, fingerprint)
	return b
}

// buildDevinGetChatMessageRequest 编码完整的 GetChatMessageRequest。
func buildDevinGetChatMessageRequest(
	sessionToken string,
	deviceSeed string,
	chatModelUID string,
	systemPrompt string,
	prompts []devinPrompt,
	tools []devinTool,
	temperature *float64,
	maxTokens int,
	sessionID string,
	cascadeID string,
) []byte {
	if maxTokens <= 0 {
		maxTokens = devinDefaultMaxTokens
	}
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	if cascadeID == "" {
		cascadeID = sessionID
	}

	var req []byte
	req = protowire.AppendTag(req, 1, protowire.BytesType)
	req = protowire.AppendBytes(req, buildDevinClientMetadataBytes(sessionToken, deviceSeed, runtime.GOOS))

	if systemPrompt != "" {
		req = protowire.AppendTag(req, 2, protowire.BytesType)
		req = protowire.AppendString(req, systemPrompt)
	}

	for _, p := range prompts {
		var pb []byte
		msgID := p.MessageID
		if msgID == "" {
			msgID = uuid.New().String()
		}
		pb = protowire.AppendTag(pb, 1, protowire.BytesType)
		pb = protowire.AppendString(pb, msgID)

		source := p.Source
		if source <= 0 {
			source = 1
		}
		pb = protowire.AppendTag(pb, 2, protowire.VarintType)
		pb = protowire.AppendVarint(pb, uint64(source))

		pb = protowire.AppendTag(pb, 3, protowire.BytesType)
		pb = protowire.AppendString(pb, p.Content)

		for _, tc := range p.ToolCalls {
			var tcb []byte
			if tc.ID != "" {
				tcb = protowire.AppendTag(tcb, 1, protowire.BytesType)
				tcb = protowire.AppendString(tcb, tc.ID)
			}
			if tc.Name != "" {
				tcb = protowire.AppendTag(tcb, 2, protowire.BytesType)
				tcb = protowire.AppendString(tcb, tc.Name)
			}
			if tc.Arguments != "" {
				tcb = protowire.AppendTag(tcb, 3, protowire.BytesType)
				tcb = protowire.AppendString(tcb, tc.Arguments)
			}
			pb = protowire.AppendTag(pb, 6, protowire.BytesType)
			pb = protowire.AppendBytes(pb, tcb)
		}

		if p.ToolCallID != "" {
			pb = protowire.AppendTag(pb, 7, protowire.BytesType)
			pb = protowire.AppendString(pb, p.ToolCallID)
		}

		for _, img := range p.Images {
			data := strings.TrimSpace(img.Base64Data)
			if data == "" {
				continue
			}
			var ib []byte
			ib = protowire.AppendTag(ib, 1, protowire.BytesType)
			ib = protowire.AppendString(ib, data)
			mime := strings.TrimSpace(img.MimeType)
			if mime == "" {
				mime = "image/png"
			}
			ib = protowire.AppendTag(ib, 2, protowire.BytesType)
			ib = protowire.AppendString(ib, mime)
			pb = protowire.AppendTag(pb, 10, protowire.BytesType)
			pb = protowire.AppendBytes(pb, ib)
		}

		if p.Thinking != "" {
			pb = protowire.AppendTag(pb, 11, protowire.BytesType)
			pb = protowire.AppendString(pb, p.Thinking)
		}
		if len(p.Signature) > 0 {
			pb = protowire.AppendTag(pb, 12, protowire.BytesType)
			pb = protowire.AppendBytes(pb, p.Signature)
		}

		req = protowire.AppendTag(req, 3, protowire.BytesType)
		req = protowire.AppendBytes(req, pb)
	}

	// Field 7 固定 5
	req = protowire.AppendTag(req, 7, protowire.VarintType)
	req = protowire.AppendVarint(req, 5)

	// Field 8 采样与补全配置
	var f8 []byte
	f8 = protowire.AppendTag(f8, 1, protowire.VarintType)
	f8 = protowire.AppendVarint(f8, 1)
	f8 = protowire.AppendTag(f8, 2, protowire.VarintType)
	f8 = protowire.AppendVarint(f8, uint64(maxTokens))
	f8 = protowire.AppendTag(f8, 3, protowire.VarintType)
	f8 = protowire.AppendVarint(f8, 400)
	tempVal := 1.0
	if temperature != nil {
		tempVal = *temperature
	}
	f8 = protowire.AppendTag(f8, 5, protowire.Fixed64Type)
	f8 = protowire.AppendFixed64(f8, math.Float64bits(tempVal))
	f8 = protowire.AppendTag(f8, 7, protowire.VarintType)
	f8 = protowire.AppendVarint(f8, 40)
	f8 = protowire.AppendTag(f8, 8, protowire.Fixed64Type)
	f8 = protowire.AppendFixed64(f8, math.Float64bits(float64(float32(0.95))))
	req = protowire.AppendTag(req, 8, protowire.BytesType)
	req = protowire.AppendBytes(req, f8)

	// Field 10 工具
	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		var tb []byte
		tb = protowire.AppendTag(tb, 1, protowire.BytesType)
		tb = protowire.AppendString(tb, tool.Name)
		if tool.Description != "" {
			tb = protowire.AppendTag(tb, 2, protowire.BytesType)
			tb = protowire.AppendString(tb, tool.Description)
		}
		if len(tool.Parameters) > 0 {
			tb = protowire.AppendTag(tb, 3, protowire.BytesType)
			tb = protowire.AppendBytes(tb, tool.Parameters)
		}
		req = protowire.AppendTag(req, 10, protowire.BytesType)
		req = protowire.AppendBytes(req, tb)
	}

	// Field 15 会话元数据
	turnIndex := nextDevinSessionTurnIndex(sessionID)
	var f15 []byte
	f15 = protowire.AppendTag(f15, 1, protowire.BytesType)
	f15 = protowire.AppendString(f15, sessionID)
	if turnIndex > 0 {
		f15 = protowire.AppendTag(f15, 2, protowire.VarintType)
		f15 = protowire.AppendVarint(f15, uint64(turnIndex))
	}
	f15 = protowire.AppendTag(f15, 3, protowire.VarintType)
	f15 = protowire.AppendVarint(f15, 4)
	if len(prompts) > 0 && prompts[len(prompts)-1].Source == 1 {
		if turnIndex == 0 || len(prompts) < 2 || prompts[len(prompts)-2].Source != 1 {
			f15 = protowire.AppendTag(f15, 4, protowire.VarintType)
			f15 = protowire.AppendVarint(f15, 14)
		}
	}
	req = protowire.AppendTag(req, 15, protowire.BytesType)
	req = protowire.AppendBytes(req, f15)

	// Field 16 cascade id（prompt cache key）
	req = protowire.AppendTag(req, 16, protowire.BytesType)
	req = protowire.AppendString(req, cascadeID)

	// Field 20 固定 1
	req = protowire.AppendTag(req, 20, protowire.VarintType)
	req = protowire.AppendVarint(req, 1)

	// Field 21 模型 UID
	req = protowire.AppendTag(req, 21, protowire.BytesType)
	req = protowire.AppendString(req, chatModelUID)

	return req
}

// ParseDevinFrame 从单帧负载解析增量、工具调用、思考、签名与用量。
func ParseDevinFrame(payload []byte) (devinFrameResult, error) {
	var res devinFrameResult
	var textParts, thinkingParts []string

	pos := 0
	for pos < len(payload) {
		num, typ, n := protowire.ConsumeTag(payload[pos:])
		if n <= 0 {
			return res, fmt.Errorf("consume tag error at offset %d: %w", pos, protowire.ParseError(n))
		}
		pos += n

		switch typ {
		case protowire.VarintType:
			v, vn := protowire.ConsumeVarint(payload[pos:])
			if vn <= 0 {
				return res, fmt.Errorf("consume varint error at offset %d: %w", pos, protowire.ParseError(vn))
			}
			pos += vn
			if num == 5 {
				res.StopReason = v
			}
		case protowire.Fixed64Type:
			_, fn := protowire.ConsumeFixed64(payload[pos:])
			if fn <= 0 {
				return res, fmt.Errorf("consume fixed64 error at offset %d: %w", pos, protowire.ParseError(fn))
			}
			pos += fn
		case protowire.Fixed32Type:
			_, fn := protowire.ConsumeFixed32(payload[pos:])
			if fn <= 0 {
				return res, fmt.Errorf("consume fixed32 error at offset %d: %w", pos, protowire.ParseError(fn))
			}
			pos += fn
		case protowire.BytesType:
			val, bn := protowire.ConsumeBytes(payload[pos:])
			if bn <= 0 {
				return res, fmt.Errorf("consume bytes error at offset %d: %w", pos, protowire.ParseError(bn))
			}
			pos += bn
			switch num {
			case 1:
				res.OutputID = string(val)
			case 3:
				textParts = append(textParts, string(val))
			case 6:
				if tc, err := parseDevinToolCallDelta(val); err == nil {
					res.ToolCallDeltas = append(res.ToolCallDeltas, tc)
				}
			case 7:
				res.Usage = parseDevinUsageField(val)
			case 9:
				thinkingParts = append(thinkingParts, string(val))
			case 10:
				res.DeltaSignature = append(res.DeltaSignature, val...)
			case 17:
				res.MessageID = string(val)
			case 21:
				res.DeltaSignatureType = string(val)
			case 28:
				res.ResponseDimensionGroups = append(res.ResponseDimensionGroups, val)
			}
		default:
			return res, fmt.Errorf("unsupported wire type %d at offset %d", typ, pos)
		}
	}
	if len(textParts) > 0 {
		res.ContentText = strings.Join(textParts, "")
	}
	if len(thinkingParts) > 0 {
		res.ThinkingText = strings.Join(thinkingParts, "")
	}
	return res, nil
}

func parseDevinToolCallDelta(data []byte) (devinToolCallDelta, error) {
	var tc devinToolCallDelta
	pos := 0
	for pos < len(data) {
		num, typ, n := protowire.ConsumeTag(data[pos:])
		if n <= 0 {
			return tc, protowire.ParseError(n)
		}
		pos += n
		switch typ {
		case protowire.VarintType:
			v, vn := protowire.ConsumeVarint(data[pos:])
			if vn <= 0 {
				return tc, protowire.ParseError(vn)
			}
			pos += vn
			_ = v
		case protowire.BytesType:
			val, bn := protowire.ConsumeBytes(data[pos:])
			if bn <= 0 {
				return tc, protowire.ParseError(bn)
			}
			pos += bn
			switch num {
			case 1:
				tc.ID = string(val)
			case 2:
				tc.Name = string(val)
			case 3:
				tc.Arguments = string(val)
			case 4:
				tc.InvalidJSONStr = string(val)
			case 5:
				tc.InvalidJSONErr = string(val)
			}
		default:
			nSkip := protowire.ConsumeFieldValue(num, typ, data[pos:])
			if nSkip <= 0 {
				return tc, protowire.ParseError(nSkip)
			}
			pos += nSkip
		}
	}
	return tc, nil
}

func parseDevinHeaderField(data []byte) (string, string) {
	var key, val string
	pos := 0
	for pos < len(data) {
		num, typ, n := protowire.ConsumeTag(data[pos:])
		if n <= 0 {
			break
		}
		pos += n
		switch typ {
		case protowire.BytesType:
			b, bn := protowire.ConsumeBytes(data[pos:])
			if bn <= 0 {
				return key, val
			}
			pos += bn
			switch num {
			case 1:
				key = string(b)
			case 2:
				val = string(b)
			}
		default:
			nSkip := protowire.ConsumeFieldValue(num, typ, data[pos:])
			if nSkip <= 0 {
				return key, val
			}
			pos += nSkip
		}
	}
	return key, val
}

func parseDevinUsageField(data []byte) *devinUsage {
	u := &devinUsage{}
	pos := 0
	for pos < len(data) {
		num, typ, n := protowire.ConsumeTag(data[pos:])
		if n <= 0 {
			break
		}
		pos += n
		switch typ {
		case protowire.VarintType:
			v, vn := protowire.ConsumeVarint(data[pos:])
			if vn <= 0 {
				return u
			}
			pos += vn
			switch num {
			case 2:
				u.PromptTokens += int64(v)
			case 3:
				u.CompletionTokens = int64(v)
			case 4:
				u.CacheWriteTokens += int64(v)
			case 5:
				u.CachedTokens = int64(v)
			case 6:
				u.StatusCode = v
			}
		case protowire.BytesType:
			val, bn := protowire.ConsumeBytes(data[pos:])
			if bn <= 0 {
				return u
			}
			pos += bn
			if num == 9 {
				u.ModelName = string(val)
			}
		case protowire.Fixed64Type:
			_, fn := protowire.ConsumeFixed64(data[pos:])
			if fn <= 0 {
				return u
			}
			pos += fn
		case protowire.Fixed32Type:
			_, fn := protowire.ConsumeFixed32(data[pos:])
			if fn <= 0 {
				return u
			}
			pos += fn
		default:
			nSkip := protowire.ConsumeFieldValue(num, typ, data[pos:])
			if nSkip <= 0 {
				return u
			}
			pos += nSkip
		}
	}
	return u
}

// ParseDevinResponseDimensionGroups 解析 Field 28 的 Token Usage 指标组。
func ParseDevinResponseDimensionGroups(groups ...[]byte) (promptTokens, completionTokens, cachedTokens int64, found bool) {
	for _, gBytes := range groups {
		if len(gBytes) == 0 {
			continue
		}
		if num, typ, n := protowire.ConsumeTag(gBytes); n > 0 && num == 28 && typ == protowire.BytesType {
			if inner, bn := protowire.ConsumeBytes(gBytes[n:]); bn > 0 {
				gBytes = inner
			}
		}
		gPos := 0
		var title string
		type metricItem struct {
			key string
			val float32
		}
		var metrics []metricItem
		for gPos < len(gBytes) {
			gNum, gTyp, gn := protowire.ConsumeTag(gBytes[gPos:])
			if gn <= 0 {
				break
			}
			gPos += gn
			if gTyp != protowire.BytesType {
				gSkip := protowire.ConsumeFieldValue(gNum, gTyp, gBytes[gPos:])
				if gSkip <= 0 {
					break
				}
				gPos += gSkip
				continue
			}
			gb, gbn := protowire.ConsumeBytes(gBytes[gPos:])
			if gbn <= 0 {
				break
			}
			gPos += gbn
			if gNum == 1 {
				title = string(gb)
			} else if gNum == 2 {
				mPos := 0
				var mKey string
				var mVal float32
				for mPos < len(gb) {
					mNum, mTyp, mn := protowire.ConsumeTag(gb[mPos:])
					if mn <= 0 {
						break
					}
					mPos += mn
					if mTyp != protowire.BytesType {
						mSkip := protowire.ConsumeFieldValue(mNum, mTyp, gb[mPos:])
						if mSkip <= 0 {
							break
						}
						mPos += mSkip
						continue
					}
					mb, mbn := protowire.ConsumeBytes(gb[mPos:])
					if mbn <= 0 {
						break
					}
					mPos += mbn
					if mNum == 5 {
						mKey = string(mb)
					} else if mNum == 4 {
						dPos := 0
						for dPos < len(mb) {
							dNum, dTyp, dn := protowire.ConsumeTag(mb[dPos:])
							if dn <= 0 {
								break
							}
							dPos += dn
							if dTyp == protowire.Fixed32Type {
								dv, dfn := protowire.ConsumeFixed32(mb[dPos:])
								if dfn <= 0 {
									break
								}
								dPos += dfn
								if dNum == 2 {
									mVal = math.Float32frombits(dv)
								}
							} else {
								dSkip := protowire.ConsumeFieldValue(dNum, dTyp, mb[dPos:])
								if dSkip <= 0 {
									break
								}
								dPos += dSkip
							}
						}
					}
				}
				if mKey != "" {
					metrics = append(metrics, metricItem{key: mKey, val: mVal})
				}
			}
		}
		if strings.EqualFold(title, "Token Usage") {
			for _, m := range metrics {
				switch m.key {
				case "input_tokens":
					promptTokens = int64(m.val)
					found = true
				case "output_tokens":
					completionTokens = int64(m.val)
					found = true
				case "cached_input_tokens":
					cachedTokens = int64(m.val)
					found = true
				}
			}
			if found {
				return promptTokens, completionTokens, cachedTokens, true
			}
		}
	}
	return promptTokens, completionTokens, cachedTokens, found
}

// ParseDevinTrailerError 解析 Connect 终止帧里的错误（JSON），映射成 HTTP 语义码。
func ParseDevinTrailerError(payload []byte) (statusCode int, err error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("{}")) {
		return 0, nil
	}
	var trailer struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if errUnmarshal := json.Unmarshal(trimmed, &trailer); errUnmarshal != nil || trailer.Error == nil {
		return 0, nil
	}
	codeStr := strings.ToLower(trailer.Error.Code)
	msgLower := strings.ToLower(trailer.Error.Message)

	httpCode := http.StatusBadGateway
	switch codeStr {
	case "invalid_argument":
		if strings.Contains(msgLower, "internal error") {
			httpCode = http.StatusBadGateway
		} else {
			httpCode = http.StatusBadRequest
		}
	case "internal":
		httpCode = http.StatusBadGateway
	case "unauthenticated":
		httpCode = http.StatusUnauthorized
	case "permission_denied":
		if strings.Contains(msgLower, "high demand") {
			httpCode = http.StatusTooManyRequests
		} else {
			httpCode = http.StatusForbidden
		}
	case "resource_exhausted":
		httpCode = http.StatusTooManyRequests
	case "unavailable":
		httpCode = http.StatusServiceUnavailable
	case "canceled":
		httpCode = 499
	case "deadline_exceeded":
		httpCode = http.StatusGatewayTimeout
	case "failed_precondition":
		if strings.Contains(msgLower, "quota") ||
			strings.Contains(msgLower, "credit") ||
			strings.Contains(msgLower, "acu") ||
			strings.Contains(msgLower, "exhausted") ||
			strings.Contains(msgLower, "limit") {
			httpCode = http.StatusTooManyRequests
		} else {
			httpCode = http.StatusBadRequest
		}
	}
	return httpCode, fmt.Errorf("devin upstream error (%s): %s", trailer.Error.Code, trailer.Error.Message)
}

// UTF8SplitBuffer 跨帧缓冲不完整的 UTF-8 序列。
type UTF8SplitBuffer struct {
	remainder []byte
}

// Feed 拼接残余字节并返回完整的 UTF-8 字符串。
func (b *UTF8SplitBuffer) Feed(chunk []byte) string {
	combined := append(b.remainder, chunk...)
	b.remainder = nil
	if len(combined) == 0 {
		return ""
	}
	validUntil := 0
	for validUntil < len(combined) {
		r, size := utf8.DecodeRune(combined[validUntil:])
		if r == utf8.RuneError && size == 1 {
			trailingLen := len(combined) - validUntil
			if trailingLen < utf8.UTFMax && !utf8.FullRune(combined[validUntil:]) {
				break
			}
			validUntil++
			continue
		}
		validUntil += size
	}
	validBytes := combined[:validUntil]
	b.remainder = append(b.remainder, combined[validUntil:]...)
	return string(validBytes)
}

// devinSessionUUID 由会话种子派生稳定 UUID（同一段对话的多轮共用一个 session/cascade id）。
func devinSessionUUID(seed string) string {
	sum := sha256.Sum256([]byte("devin-session:" + seed))
	var u uuid.UUID
	copy(u[:], sum[:16])
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return u.String()
}
