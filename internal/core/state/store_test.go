package state

import (
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

func TestPutGetResponse(t *testing.T) {
	s := New(Options{MaxRecords: 10, TTL: time.Hour})
	rec := &ResponseRecord{
		ResponseID: "resp_1",
		ProviderID: "p",
		AccountID:  "a",
		AccumulatedItems: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
		},
	}
	s.PutResponse(rec)

	got, ok := s.GetResponse("resp_1")
	if !ok {
		t.Fatal("未找到刚写入的记录")
	}
	if len(got.AccumulatedItems) != 1 || got.AccumulatedItems[0].Text != "hi" {
		t.Errorf("内容不匹配: %+v", got)
	}
	if _, ok := s.GetResponse("nope"); ok {
		t.Error("不存在的 ID 不应命中")
	}
}

func TestResponseTTL(t *testing.T) {
	s := New(Options{MaxRecords: 10, TTL: time.Hour})
	// 直接写入一个已过期的记录
	s.PutResponse(&ResponseRecord{
		ResponseID: "expired",
		ExpiresAt:  time.Now().Add(-time.Minute).Unix(),
	})
	if _, ok := s.GetResponse("expired"); ok {
		t.Error("过期记录不应命中")
	}
}

func TestLRUEviction(t *testing.T) {
	s := New(Options{MaxRecords: 3, TTL: time.Hour})
	for _, id := range []string{"a", "b", "c", "d"} {
		s.PutResponse(&ResponseRecord{ResponseID: id, AccumulatedItems: []ir.UnifiedItem{}})
	}
	if s.Count() > 3 {
		t.Errorf("超过上限未淘汰: %d", s.Count())
	}
	if _, ok := s.GetResponse("a"); ok {
		t.Error("最旧的记录应被淘汰")
	}
	if _, ok := s.GetResponse("d"); !ok {
		t.Error("最新记录不应被淘汰")
	}
}

func TestSignatures(t *testing.T) {
	s := New(Options{MaxRecords: 10})
	s.PutSignature("call_1", "sig_abc")
	got, ok := s.GetSignature("call_1")
	if !ok || got != "sig_abc" {
		t.Errorf("签名读取失败: %q %v", got, ok)
	}
	if _, ok := s.GetSignature(""); ok {
		t.Error("空 callID 不应命中")
	}
}

func TestSessionBinding(t *testing.T) {
	s := New(Options{MaxRecords: 10})
	s.PutSession("key1", "provider", "acc", "sess_9")

	p, a, sess, ok := s.GetSession("key1")
	if !ok || p != "provider" || a != "acc" || sess != "sess_9" {
		t.Errorf("会话绑定错误: %q %q %q %v", p, a, sess, ok)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	s1 := New(Options{MaxRecords: 100, SnapshotPath: path})
	s1.PutResponse(&ResponseRecord{
		ResponseID:       "resp_x",
		ProviderID:       "p",
		AccountID:        "a",
		UpstreamSessionID: "sess_1",
		AccumulatedItems: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "记住我"}},
	})
	s1.PutSignature("c1", "sig1")
	if err := s1.Save(); err != nil {
		t.Fatalf("落盘失败: %v", err)
	}

	s2 := New(Options{MaxRecords: 100, SnapshotPath: path})
	rec, ok := s2.GetResponse("resp_x")
	if !ok {
		t.Fatal("重启后未恢复记录")
	}
	if len(rec.AccumulatedItems) != 1 || rec.AccumulatedItems[0].Text != "记住我" {
		t.Errorf("上下文未恢复: %+v", rec.AccumulatedItems)
	}
	if sess, ok := s2.GetSignature("c1"); !ok || sess != "sig1" {
		t.Errorf("签名未恢复: %q", sess)
	}
}

func TestDeleteResponse(t *testing.T) {
	s := New(Options{MaxRecords: 10})
	s.PutResponse(&ResponseRecord{ResponseID: "r", AccumulatedItems: []ir.UnifiedItem{}})
	if !s.DeleteResponse("r") {
		t.Error("删除应返回 true")
	}
	if s.DeleteResponse("r") {
		t.Error("重复删除应返回 false")
	}
}
