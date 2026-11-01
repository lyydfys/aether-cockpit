package gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRelayStream 验证直通转发：内容完整、逐跳头被过滤、长度头被剔除。
func TestRelayStream(t *testing.T) {
	body := "event: response.created\ndata: {\"x_vendor_meta\":\"kept\"}\n\n"
	resp := &http.Response{
		StatusCode: 200,
		Header: http.Header{
			"Content-Type":      []string{"text/event-stream"},
			"X-Upstream-Marker": []string{"native"},
			// 逐跳头必须被丢弃
			"Connection":        []string{"keep-alive"},
			"Transfer-Encoding": []string{"chunked"},
			// 流式长度未知，必须剔除
			"Content-Length": []string{"999"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}

	rec := httptest.NewRecorder()
	if err := relayStream(rec, resp); err != nil {
		t.Fatalf("转发失败: %v", err)
	}

	if got := rec.Body.String(); got != body {
		t.Errorf("内容被改写:\n got=%q\nwant=%q", got, body)
	}
	if rec.Header().Get("X-Upstream-Marker") != "native" {
		t.Error("上游自定义头丢失")
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Error("Content-Length 应被剔除")
	}
	if rec.Header().Get("Connection") != "" {
		t.Error("Connection 是逐跳头，应被剔除")
	}
	if rec.Header().Get("Transfer-Encoding") != "" {
		t.Error("Transfer-Encoding 是逐跳头，应被剔除")
	}
	if rec.Code != 200 {
		t.Errorf("状态码 = %d", rec.Code)
	}
}

// TestRelayStreamDefaultsContentType 上游没给 Content-Type 时兜底成 SSE。
func TestRelayStreamDefaultsContentType(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("data: x\n\n")),
	}
	rec := httptest.NewRecorder()
	if err := relayStream(rec, resp); err != nil {
		t.Fatal(err)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q", ct)
	}
}

// TestRelayStreamUpstreamError 上游流中途出错要能返回错误而不是静默丢数据。
func TestRelayStreamUpstreamError(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(&errReader{data: []byte("data: partial\n\n")}),
	}
	rec := httptest.NewRecorder()
	err := relayStream(rec, resp)
	if err == nil {
		t.Fatal("上游中断应返回错误")
	}
	if !strings.Contains(rec.Body.String(), "partial") {
		t.Error("中断前的数据应当已经写出")
	}
}

// errReader 先返回一段数据，再返回错误。
type errReader struct {
	data []byte
	done bool
}

func (e *errReader) Read(p []byte) (int, error) {
	if e.done {
		return 0, errors.New("模拟上游断开")
	}
	e.done = true
	n := copy(p, e.data)
	return n, nil
}

// TestHopByHopHeaders 确认常见的逐跳头都在过滤表里。
func TestHopByHopHeaders(t *testing.T) {
	for _, h := range []string{"connection", "keep-alive", "transfer-encoding", "upgrade", "content-length"} {
		if !hopByHopHeaders[h] {
			t.Errorf("%q 应在过滤表里", h)
		}
	}
	if hopByHopHeaders["content-type"] {
		t.Error("content-type 不应被过滤")
	}
}
