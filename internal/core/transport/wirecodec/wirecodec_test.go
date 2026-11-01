package wirecodec

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestAWSEventStreamRoundTrip 验证自己编码的二进制帧能被自己解出来。
// 由于编码与解码用的是同一套格式定义，这个测试能挡住字段偏移写错的问题。
func TestAWSEventStreamRoundTrip(t *testing.T) {
	payload := []byte(`{"content":"你好，世界"}`)
	headers := map[string]string{
		":event-type":  "assistantResponseEvent",
		":message-type": "event",
	}
	raw := EncodeAWSMessage(headers, payload)

	dec := NewAWSEventStreamDecoder(bytes.NewReader(raw))
	msg, err := dec.Next()
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if msg.EventType() != "assistantResponseEvent" {
		t.Errorf("event-type = %q, want assistantResponseEvent", msg.EventType())
	}
	if msg.MessageType() != "event" {
		t.Errorf("message-type = %q, want event", msg.MessageType())
	}
	if !bytes.Equal(msg.Payload, payload) {
		t.Errorf("payload = %q, want %q", msg.Payload, payload)
	}
	if !dec.LastCRCOK {
		t.Error("CRC 校验应为通过")
	}
}

// TestAWSEventStreamMultiFrame 验证连续多帧能依次读出并正确结束。
func TestAWSEventStreamMultiFrame(t *testing.T) {
	var buf bytes.Buffer
	for _, s := range []string{"a", "b", "c"} {
		buf.Write(EncodeAWSMessage(map[string]string{":event-type": "delta"}, []byte(s)))
	}

	dec := NewAWSEventStreamDecoder(&buf)
	var got []string
	for {
		msg, err := dec.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("解码失败: %v", err)
		}
		got = append(got, string(msg.Payload))
	}
	if strings.Join(got, "") != "abc" {
		t.Errorf("got %v, want [a b c]", got)
	}
}

// TestAWSEventStreamBadCRC 验证 CRC 被篡改时会报错而不是静默返回错数据。
func TestAWSEventStreamBadCRC(t *testing.T) {
	raw := EncodeAWSMessage(map[string]string{":event-type": "x"}, []byte("hello"))
	// 篡改 payload 区域（跳过 12 字节 prelude，改写第 12 字节）。
	raw[12] ^= 0xFF

	dec := NewAWSEventStreamDecoder(bytes.NewReader(raw))
	if _, err := dec.Next(); err == nil {
		t.Fatal("CRC 被破坏时应当报错")
	}
}

// TestAWSEventStreamTruncated 验证截断的流返回错误而非 panic。
func TestAWSEventStreamTruncated(t *testing.T) {
	raw := EncodeAWSMessage(map[string]string{":event-type": "x"}, []byte("hello"))
	dec := NewAWSEventStreamDecoder(bytes.NewReader(raw[:len(raw)-3]))
	if _, err := dec.Next(); err == nil {
		t.Fatal("被截断的流应当报错")
	}
}

// TestSSEDecoderBasic 验证标准 SSE 解析。
func TestSSEDecoderBasic(t *testing.T) {
	input := "event: foo\ndata: {\"a\":1}\n\ndata: plain\n\n: comment\n\ndata: [DONE]\n\n"
	dec := NewSSEDecoder(strings.NewReader(input))

	want := []Frame{
		{Event: "foo", Data: []byte(`{"a":1}`)},
		{Data: []byte("plain")},
		{Data: []byte("[DONE]")},
	}
	for i, w := range want {
		got, err := dec.Next()
		if err != nil {
			t.Fatalf("第 %d 帧解码失败: %v", i, err)
		}
		if got.Event != w.Event || !bytes.Equal(got.Data, w.Data) {
			t.Errorf("第 %d 帧 = %+v, want %+v", i, got, w)
		}
	}
	if _, err := dec.Next(); err != io.EOF {
		t.Errorf("流结束应返回 io.EOF，实际 %v", err)
	}
}

// TestSSEDecoderMultiLineData 验证多行 data 会用换行拼接。
func TestSSEDecoderMultiLineData(t *testing.T) {
	input := "data: line1\ndata: line2\n\n"
	dec := NewSSEDecoder(strings.NewReader(input))
	got, err := dec.Next()
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != "line1\nline2" {
		t.Errorf("got %q", got.Data)
	}
}

// TestSSEDecoderLongLine 验证超长行不会被截断（bufio.Scanner 的经典坑）。
func TestSSEDecoderLongLine(t *testing.T) {
	long := strings.Repeat("x", 200_000)
	dec := NewSSEDecoder(strings.NewReader("data: " + long + "\n\n"))
	got, err := dec.Next()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != len(long) {
		t.Errorf("长度 %d, want %d", len(got.Data), len(long))
	}
}

// TestNDJSONDecoder 验证逐行 JSON。
func TestNDJSONDecoder(t *testing.T) {
	dec := NewNDJSONDecoder(strings.NewReader("\n{\"a\":1}\n\n{\"b\":2}\n"))
	for _, want := range []string{`{"a":1}`, `{"b":2}`} {
		got, err := dec.Next()
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Data) != want {
			t.Errorf("got %q, want %q", got.Data, want)
		}
	}
}
