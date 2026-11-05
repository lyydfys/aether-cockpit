package rtstats

import (
	"io"
	"strings"
	"testing"
)

func TestClassifyID(t *testing.T) {
	cases := map[string]Family{
		"chatcmpl-abc123":  FamilyOfficial,
		"msg_01H8XYZ":      FamilyOfficial,
		"resp_abc":         FamilyOfficial,
		"0217931234567890": FamilyOfficial,
		"gw-7f3a2b":        FamilyRelay,
		"GW-UPPER":         FamilyRelay,
		"req-cell-42":      FamilyRelay,
		"":                 FamilyUnknown,
		"weird-id-123":     FamilyUnknown,
		"chatcmpl":         FamilyUnknown, // 没有分隔符，不算官方前缀
		"  02179  ":        FamilyOfficial,
	}
	for id, want := range cases {
		if got := classifyID(id); got != want {
			t.Errorf("classifyID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestExtractID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`{"id":"chatcmpl-1","object":"chat.completion"}`, "chatcmpl-1"},
		{"data: {\"id\":\"chatcmpl-2\",\"choices\":[]}", "chatcmpl-2"},
		{`{"object":"x","choices":[{"id":"inner","delta":{}}]}`, "inner"},
		{`{"id":""}`, ""},
		{`{"id":123}`, ""},
		{`not json at all`, ""},
		{``, ""},
		// 响应头里有 request_id 之类字段，不应该被当成 id。
		{`{"request_id":"req-1","id":"chatcmpl-3"}`, "chatcmpl-3"},
	}
	for _, c := range cases {
		if got := extractID(c.in); got != c.want {
			t.Errorf("extractID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRecordCounts(t *testing.T) {
	s := &Store{data: map[string]*Counters{}}
	s.record("opencode-go", "chatcmpl-a")
	s.record("opencode-go", "chatcmpl-b")
	s.record("opencode-go", "gw-1")
	s.record("opencode-go", "weird")
	if c := s.data["opencode-go"]; c.Total != 4 || c.Official != 2 || c.Relay != 1 || c.Unknown != 1 {
		t.Fatalf("计数不对：%+v", c)
	}
	if share := s.data["opencode-go"].Share(); share != 0.5 {
		t.Fatalf("官方占比 = %v，want 0.5", share)
	}
	// 不同渠道互不影响。
	s.record("other", "gw-2")
	if c := s.data["other"]; c.Relay != 1 || c.Total != 1 {
		t.Fatalf("其它渠道计数不对：%+v", c)
	}
}

// Sniff 必须原样透传字节：统计不能改变上游响应内容。
func TestSniffPassesThrough(t *testing.T) {
	payload := `{"id":"chatcmpl-xyz","choices":[]}` + strings.Repeat("x", 5000)
	s := &Store{data: map[string]*Counters{}}
	old := global
	global = s
	defer func() { global = old }()

	body := Sniff(io.NopCloser(strings.NewReader(payload)), "p1")
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if string(got) != payload {
		t.Fatalf("内容被改写：%d vs %d 字节", len(got), len(payload))
	}
	if c := s.data["p1"]; c == nil || c.Official != 1 {
		t.Fatalf("应记录一次官方响应：%+v", s.data["p1"])
	}
}

// 空体/无 id 时不能 panic，也不能凭空计数。
func TestSniffNoID(t *testing.T) {
	s := &Store{data: map[string]*Counters{}}
	old := global
	global = s
	defer func() { global = old }()
	body := Sniff(io.NopCloser(strings.NewReader("data: keepalive\n\n")), "p2")
	_, _ = io.ReadAll(body)
	if len(s.data) != 0 {
		t.Fatalf("无 id 不该计数：%+v", s.data)
	}
}
