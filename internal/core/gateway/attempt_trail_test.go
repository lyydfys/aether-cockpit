package gateway

import (
	"strings"
	"testing"
)

// 失败转移轨迹要能看出"哪个账号、什么结果"。
// 只报"尝试了 3 次"没有诊断价值——用户不知道该修哪个账号。
func TestAttemptTrailShape(t *testing.T) {
	tries := []traceTry{
		{ID: "c27de30a96f1", Status: 502, Fail: "upstream"},
		{ID: "9ab3f1c0d2e4", Status: 200, Picked: true},
	}
	got := attemptTrail(tries)
	if got == "" {
		t.Fatal("应生成轨迹")
	}
	if !strings.Contains(got, "c27de30a") || !strings.Contains(got, "upstream") {
		t.Fatalf("应包含失败账号与原因：%q", got)
	}
	if !strings.Contains(got, "9ab3f1c0") || !strings.Contains(got, "ok") {
		t.Fatalf("应包含命中账号与成功标记：%q", got)
	}
	if !strings.Contains(got, "→") {
		t.Fatalf("多次尝试应以箭头串联：%q", got)
	}
}

// 账号 ID 截断到 8 位，避免轨迹过长。
func TestAttemptTrailTruncatesID(t *testing.T) {
	got := attemptTrail([]traceTry{{ID: "0123456789abcdef", Status: 200, Picked: true}})
	if strings.Contains(got, "0123456789") {
		t.Fatalf("账号 ID 应截断到 8 位：%q", got)
	}
	if !strings.Contains(got, "01234567") {
		t.Fatalf("应保留前 8 位：%q", got)
	}
}

// 最多保留最近 6 次，避免轨迹爆炸。
func TestAttemptTrailCapsLength(t *testing.T) {
	var tries []traceTry
	for i := 0; i < 12; i++ {
		tries = append(tries, traceTry{ID: "acc" + string(rune('a'+i)), Status: 502, Fail: "rate"})
	}
	got := attemptTrail(tries)
	if n := strings.Count(got, "→") + 1; n != 6 {
		t.Fatalf("轨迹应只保留最近 6 次，实际 %d 段：%q", n, got)
	}
}

// 没有尝试时不产出轨迹（不写空串占位）。
func TestAttemptTrailEmpty(t *testing.T) {
	if got := attemptTrail(nil); got != "" {
		t.Fatalf("无尝试时应返回空串，实际 %q", got)
	}
}
