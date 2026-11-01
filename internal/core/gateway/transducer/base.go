// Package transducer 把 Provider 吐出的极简事件流，升维成各下游协议要求的
// 严格流式状态机。
//
// 这是"新增渠道零协议负担"的另一半：渠道只管发文本/思考/工具三种增量，
// 至于 OpenAI Responses 那套 item → content_part → delta → done 的生命周期树，
// 或者 Anthropic 的 content_block 索引管理，全部在这里自动补全。
package transducer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// sseWriter 是三个 transducer 共用的 SSE 输出器。
type sseWriter struct {
	w   http.ResponseWriter
	f   http.Flusher
	err error
}

// newSSEWriter 设置标准 SSE 响应头并返回写出器。
func newSSEWriter(w http.ResponseWriter) (*sseWriter, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("transducer: ResponseWriter 不支持 Flush，无法流式输出")
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// 关闭 Nginx 等反代的缓冲，否则流会被攒起来一次性下发。
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &sseWriter{w: w, f: f}, nil
}

// writeEvent 写一条具名事件：event: name\ndata: {...}\n\n
func (s *sseWriter) writeEvent(name string, payload any) {
	if s.err != nil {
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		s.err = err
		return
	}
	if name != "" {
		if _, err = fmt.Fprintf(s.w, "event: %s\n", name); err != nil {
			s.err = err
			return
		}
	}
	if _, err = fmt.Fprintf(s.w, "data: %s\n\n", b); err != nil {
		s.err = err
		return
	}
	s.f.Flush()
}

// writeRaw 写一条无事件名的纯 data 帧（OpenAI Chat 格式）。
func (s *sseWriter) writeRaw(data string) {
	if s.err != nil {
		return
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", data); err != nil {
		s.err = err
		return
	}
	s.f.Flush()
}

// writeComment 写一条 SSE 注释（心跳）。
func (s *sseWriter) writeComment(text string) {
	if s.err != nil {
		return
	}
	fmt.Fprintf(s.w, ": %s\n\n", text)
	s.f.Flush()
}

// Err 返回首次写失败的错误。
func (s *sseWriter) Err() error { return s.err }

// seqCounter 生成 Responses 协议要求的单调递增 sequence_number。
type seqCounter struct{ n int }

func (c *seqCounter) next() int {
	c.n++
	return c.n
}

// newID 生成带前缀的短 ID。
func newID(prefix string, seed int64) string {
	return prefix + "_" + strconv.FormatInt(seed, 36)
}

// jsonQuote 把字符串编码成 JSON 字面量（含双引号）。
func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
