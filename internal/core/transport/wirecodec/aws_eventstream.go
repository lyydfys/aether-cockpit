package wirecodec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
)

// AWS EventStream 二进制帧格式（Kiro 上游使用）：
//
//	+---------------------------------------------------+
//	| Total Length (4B, big-endian)                     |
//	| Headers Length (4B, big-endian)                   |
//	| Prelude CRC32 (4B, big-endian)  <- 前 8 字节的 CRC |
//	| Headers (Headers Length 字节)                      |
//	| Payload (Total - 12 - HeadersLen - 4 字节)         |
//	| Message CRC32 (4B, big-endian)  <- 前面全部的 CRC   |
//	+---------------------------------------------------+
//
// Header 项格式：[NameLen 1B][Name][ValueType 1B][Value]
// 其中字符串类 Value = [Len 2B][Bytes]，整数类定长。
const (
	awsPreludeLen   = 12 // total(4) + headersLen(4) + preludeCRC(4)
	awsMessageCRCLen = 4
	awsMaxMessage   = 16 << 20 // 单条消息上限 16MB
)

// AWS Header 值类型。
const (
	awsTypeBoolTrue  = 0
	awsTypeBoolFalse = 1
	awsTypeByte      = 2
	awsTypeShort     = 3
	awsTypeInteger   = 4
	awsTypeLong      = 5
	awsTypeByteArray = 6
	awsTypeString    = 7
	awsTypeTimestamp = 8
	awsTypeUUID      = 9
)

// AWSEventStreamDecoder 解析 AWS 二进制 EventStream（Kiro / Bedrock 系）。
//
// 说明：这里手写而非引入 aws-sdk-go-v2，是为了让整个网关保持零第三方依赖
// （部署即单个 exe）。协议本身只有 120 行，且 format 已冻结多年。
type AWSEventStreamDecoder struct {
	r   io.Reader
	// StrictCRC 为 true 时 CRC 校验失败直接报错；为 false 时仅记录在 LastCRCOK。
	StrictCRC bool
	// LastCRCOK 记录最近一帧的 CRC 是否匹配，便于排查上游实现不规范的情况。
	LastCRCOK bool
}

// NewAWSEventStreamDecoder 创建解码器。默认严格校验 CRC。
func NewAWSEventStreamDecoder(r io.Reader) *AWSEventStreamDecoder {
	return &AWSEventStreamDecoder{r: r, StrictCRC: true}
}

// AWSMessage 是解出的一条完整消息。
type AWSMessage struct {
	// Headers 键值对，Key 去掉前导冒号（":event-type" -> "event-type"）。
	Headers map[string]string
	// Payload 业务载荷（通常是 JSON）。
	Payload []byte
}

// EventType 返回 ":event-type" 头的值，Kiro 用它区分 delta / 工具调用 / 结束。
func (m *AWSMessage) EventType() string {
	if m == nil {
		return ""
	}
	return m.Headers["event-type"]
}

// MessageType 返回 ":message-type" 头（event / exception / error）。
func (m *AWSMessage) MessageType() string {
	if m == nil {
		return ""
	}
	return m.Headers["message-type"]
}

// ExceptionType 返回 ":exception-type" 头。
func (m *AWSMessage) ExceptionType() string {
	if m == nil {
		return ""
	}
	return m.Headers["exception-type"]
}

// Next 读取下一条消息。流结束返回 io.EOF。
func (d *AWSEventStreamDecoder) Next() (*AWSMessage, error) {
	prelude := make([]byte, awsPreludeLen)
	if _, err := io.ReadFull(d.r, prelude); err != nil {
		return nil, err
	}

	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])
	preludeCRC := binary.BigEndian.Uint32(prelude[8:12])

	if totalLen == 0 {
		return nil, io.EOF
	}
	if totalLen > awsMaxMessage {
		return nil, fmt.Errorf("wirecodec: AWS 消息长度非法 %d", totalLen)
	}
	if headersLen > totalLen {
		return nil, fmt.Errorf("wirecodec: AWS headersLen %d 大于总长 %d", headersLen, totalLen)
	}

	// Prelude CRC 是判断"后续长度字段是否可信"的唯一依据，必须校验。
	if got := crc32.ChecksumIEEE(prelude[0:8]); got != preludeCRC {
		return nil, fmt.Errorf("wirecodec: AWS prelude CRC 不匹配 (want %08x got %08x)", preludeCRC, got)
	}

	bodyLen := int(totalLen) - awsPreludeLen - awsMessageCRCLen
	if bodyLen < 0 {
		return nil, fmt.Errorf("wirecodec: AWS 消息长度越界 total=%d", totalLen)
	}

	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(d.r, body); err != nil {
		return nil, err
	}

	crcBuf := make([]byte, awsMessageCRCLen)
	if _, err := io.ReadFull(d.r, crcBuf); err != nil {
		return nil, err
	}
	msgCRC := binary.BigEndian.Uint32(crcBuf)

	// 校验整条消息的 CRC：覆盖 prelude + headers + payload。
	h := crc32.NewIEEE()
	h.Write(prelude)
	h.Write(body)
	if got := h.Sum32(); got != msgCRC {
		d.LastCRCOK = false
		if d.StrictCRC {
			return nil, fmt.Errorf("wirecodec: AWS message CRC 不匹配 (want %08x got %08x)", msgCRC, got)
		}
	} else {
		d.LastCRCOK = true
	}

	if int(headersLen) > len(body) {
		return nil, fmt.Errorf("wirecodec: AWS headers 越界")
	}
	headers, err := parseAWSHeaders(body[:headersLen])
	if err != nil {
		return nil, err
	}

	return &AWSMessage{
		Headers: headers,
		Payload: body[headersLen:],
	}, nil
}

// parseAWSHeaders 解析 header 区块。
func parseAWSHeaders(b []byte) (map[string]string, error) {
	out := make(map[string]string, 4)
	i := 0
	for i < len(b) {
		nameLen := int(b[i])
		i++
		if i+nameLen > len(b) {
			return nil, errors.New("wirecodec: AWS header 名称越界")
		}
		name := string(b[i : i+nameLen])
		i += nameLen
		// AWS 的系统头都以 ":" 开头（:event-type / :message-type），
		// 统一去掉前导冒号，让调用方按普通键取值。
		name = strings.TrimPrefix(name, ":")

		if i >= len(b) {
			return nil, errors.New("wirecodec: AWS header 缺少类型字节")
		}
		vt := b[i]
		i++

		switch vt {
		case awsTypeBoolTrue:
			out[name] = "true"
		case awsTypeBoolFalse:
			out[name] = "false"
		case awsTypeByte:
			if i+1 > len(b) {
				return nil, errors.New("wirecodec: AWS byte 越界")
			}
			out[name] = fmt.Sprintf("%d", int8(b[i]))
			i++
		case awsTypeShort:
			if i+2 > len(b) {
				return nil, errors.New("wirecodec: AWS short 越界")
			}
			out[name] = fmt.Sprintf("%d", int16(binary.BigEndian.Uint16(b[i:i+2])))
			i += 2
		case awsTypeInteger:
			if i+4 > len(b) {
				return nil, errors.New("wirecodec: AWS integer 越界")
			}
			out[name] = fmt.Sprintf("%d", int32(binary.BigEndian.Uint32(b[i:i+4])))
			i += 4
		case awsTypeLong, awsTypeTimestamp:
			if i+8 > len(b) {
				return nil, errors.New("wirecodec: AWS long 越界")
			}
			out[name] = fmt.Sprintf("%d", int64(binary.BigEndian.Uint64(b[i:i+8])))
			i += 8
		case awsTypeByteArray, awsTypeString:
			if i+2 > len(b) {
				return nil, errors.New("wirecodec: AWS 字符串长度越界")
			}
			l := int(binary.BigEndian.Uint16(b[i : i+2]))
			i += 2
			if i+l > len(b) {
				return nil, errors.New("wirecodec: AWS 字符串内容越界")
			}
			out[name] = string(b[i : i+l])
			i += l
		case awsTypeUUID:
			if i+16 > len(b) {
				return nil, errors.New("wirecodec: AWS uuid 越界")
			}
			out[name] = fmt.Sprintf("%x", b[i:i+16])
			i += 16
		default:
			return nil, fmt.Errorf("wirecodec: 未知 AWS header 类型 0x%02x", vt)
		}
	}
	return out, nil
}

// EncodeAWSMessage 把消息编码回二进制帧。
// 用于 Kiro 站点的单元测试与"需要构造上游请求"的少数场景（如双向流）。
func EncodeAWSMessage(headers map[string]string, payload []byte) []byte {
	hb := encodeAWSHeaders(headers)
	totalLen := awsPreludeLen + len(hb) + len(payload) + awsMessageCRCLen

	out := make([]byte, 0, totalLen)
	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(prelude[4:8], uint32(len(hb)))
	out = append(out, prelude...)
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(prelude))
	out = append(out, hb...)
	out = append(out, payload...)
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
	return out
}

func encodeAWSHeaders(headers map[string]string) []byte {
	var out []byte
	for k, v := range headers {
		if len(k) > 255 {
			continue
		}
		out = append(out, byte(len(k)))
		out = append(out, k...)
		out = append(out, awsTypeString)
		out = binary.BigEndian.AppendUint16(out, uint16(len(v)))
		out = append(out, v...)
	}
	return out
}
