package rtstats

import "io"

// sniffLimit 是首次读取时用于提取 id 的最大字节数。
//
// 12KB 足够覆盖 SSE 的第一帧与普通 JSON 响应的 id 字段（一般在最前面），
// 又不会为了统计把大响应缓冲进内存。
const sniffLimit = 12 << 10

// Sniff 包一层 ReadCloser：第一次 Read 时从头部提取 `"id":"…"` 记一次统计，
// 之后原样透传，不改变调用方看到的字节。
//
// 放在这一层而不是解析器里，是因为流式/非流式/json/ndjson 四种形态都要统计，
// 而它们的解析器各写各的；在 HTTP body 层统一嗅探最省事，也不会让统计逻辑
// 掺进协议转换。
func Sniff(body io.ReadCloser, providerID string) io.ReadCloser {
	if body == nil {
		return body
	}
	return &sniffReader{inner: body, providerID: providerID}
}

type sniffReader struct {
	inner      io.ReadCloser
	providerID string
	done       bool
	// pending 是第一次读取时多读出来的字节（已读过、还没交给调用方）。
	pending []byte
}

func (r *sniffReader) Read(p []byte) (int, error) {
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	if r.done {
		return r.inner.Read(p)
	}
	r.done = true
	// 先读一块用于嗅探；读到的内容不能丢，剩下的留给下次 Read。
	buf := make([]byte, sniffLimit)
	n, err := r.inner.Read(buf)
	if n > 0 {
		recordFromPrefix(r.providerID, buf[:n])
		r.pending = append(r.pending, buf[:n]...)
	}
	if n > 0 {
		copied := copy(p, r.pending)
		r.pending = r.pending[copied:]
		return copied, nil
	}
	return 0, err
}

func (r *sniffReader) Close() error { return r.inner.Close() }
