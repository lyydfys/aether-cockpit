// Package wirecodec 负责把上游各式各样的流式线缆格式，统一拆成"一个帧 = 一段业务载荷"。
//
// 这一层存在的唯一理由是：不同厂商的流式传输根本不是同一种东西。
//   - Trae / WorkBuddy / Qoder / Codex / OpenCode：标准 text/event-stream
//   - Kiro：AWS 二进制 EventStream（带 CRC32 分帧，不是文本）
//   - Antigravity / Vertex 变体：JSON 数组流或 NDJSON
//
// 渠道插件只需要 Next() 拿到载荷，不必关心底层是怎么分帧的。
package wirecodec

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// Frame 是解帧后的单条载荷。
type Frame struct {
	// Event 是具名事件的名称（SSE 的 event: 字段），其他格式为空。
	Event string
	// Data 是业务载荷原文，通常是一段 JSON。
	Data []byte
	// ID 可选。
	ID string
}

// Decoder 是所有流式解码器的统一接口。
type Decoder interface {
	// Next 返回下一帧。流结束返回 io.EOF。
	Next() (Frame, error)
}

// SSE 相关的哨兵值。
var (
	// ErrFrameTooLarge 单帧超过安全上限，防止恶意上游打爆内存。
	ErrFrameTooLarge = errors.New("wirecodec: 帧长度超过上限")
)

// maxFrameBytes 单帧上限 8MB。SSE 单帧正常在几 KB，8MB 已是极端宽松。
const maxFrameBytes = 8 << 20

// SSEDecoder 解析标准 text/event-stream。
//
// 手写而非用 scanner：bufio.Scanner 默认 64KB 单行上限且不可动态调整，
// 遇到长 base64 图片块会直接截断报错。
type SSEDecoder struct {
	r *bufio.Reader
	// LastEventID 供需要断点续传的上游使用。
	LastEventID string
}

// NewSSEDecoder 创建一个 SSE 解码器。
func NewSSEDecoder(r io.Reader) *SSEDecoder {
	return &SSEDecoder{r: bufio.NewReaderSize(r, 32<<10)}
}

// Next 读取下一个完整事件（以空行分隔）。
func (d *SSEDecoder) Next() (Frame, error) {
	var (
		frame     Frame
		dataLines []string
		hasData   bool
	)

	for {
		line, err := d.readLine()
		if err != nil {
			// 流在事件未闭合时结束：把已积累的数据吐出去，避免丢掉最后一帧。
			if len(dataLines) > 0 {
				frame.Data = []byte(strings.Join(dataLines, "\n"))
				return frame, nil
			}
			return Frame{}, err
		}

		if line == "" {
			// 空行 = 事件边界。
			if !hasData && frame.Event == "" && frame.ID == "" {
				continue // 连续空行，跳过
			}
			if hasData {
				frame.Data = []byte(strings.Join(dataLines, "\n"))
			}
			if frame.ID != "" {
				d.LastEventID = frame.ID
			}
			return frame, nil
		}

		if strings.HasPrefix(line, ":") {
			continue // 注释/心跳行
		}

		field, value, found := strings.Cut(line, ":")
		if !found {
			// 无冒号的行按字段名处理，值为空。
			field, value = line, ""
		} else {
			value = strings.TrimPrefix(value, " ")
		}

		switch field {
		case "event":
			frame.Event = value
		case "data":
			dataLines = append(dataLines, value)
			hasData = true
		case "id":
			frame.ID = value
		case "retry":
			// 重连间隔，网关不关心。
		default:
			// 未知字段忽略。
		}
	}
}

// readLine 读一行，去掉行尾 \r\n / \n，并施加长度上限。
func (d *SSEDecoder) readLine() (string, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := d.r.ReadLine()
		if err != nil {
			return "", err
		}
		buf = append(buf, chunk...)
		if len(buf) > maxFrameBytes {
			return "", ErrFrameTooLarge
		}
		if !isPrefix {
			break
		}
	}
	return string(buf), nil
}

// NDJSONDecoder 解析逐行 JSON（每行一个完整 JSON 对象）。
type NDJSONDecoder struct {
	r *bufio.Reader
}

// NewNDJSONDecoder 创建一个 NDJSON 解码器。
func NewNDJSONDecoder(r io.Reader) *NDJSONDecoder {
	return &NDJSONDecoder{r: bufio.NewReaderSize(r, 32<<10)}
}

// Next 返回下一行非空内容。
func (d *NDJSONDecoder) Next() (Frame, error) {
	for {
		var buf []byte
		for {
			chunk, isPrefix, err := d.r.ReadLine()
			if err != nil {
				return Frame{}, err
			}
			buf = append(buf, chunk...)
			if len(buf) > maxFrameBytes {
				return Frame{}, ErrFrameTooLarge
			}
			if !isPrefix {
				break
			}
		}
		trimmed := strings.TrimSpace(string(buf))
		if trimmed == "" {
			continue
		}
		return Frame{Data: []byte(trimmed)}, nil
	}
}
