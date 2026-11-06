package workbuddy

import (
	"testing"
	"time"
)

// 11140 / 403 是风控信号，不是鉴权失败：识别出来才能"停下来"。
func TestRiskBlocked(t *testing.T) {
	cases := []struct {
		status int
		raw    string
		want   bool
	}{
		{403, `{"code":403,"message":"forbidden"}`, true},
		{400, `{"code":11140,"message":"abnormal behavior"}`, true},
		{400, `{"code": 11140, "message":"abnormal"}`, true},
		{400, `{"code":11142,"message":"abnormal behavior"}`, true},
		{400, `{"code": 11142, "message":"abnormal"}`, true},
		{400, `{"code":11128,"message":"channel check"}`, false}, // 通道校验，不是风控
		{401, `{"code":401}`, false},                             // 鉴权失效
		{200, `{"ok":true}`, false},
	}
	for _, c := range cases {
		if got := riskBlocked(c.status, c.raw); got != c.want {
			t.Errorf("riskBlocked(%d, %q) = %v, want %v", c.status, c.raw, got, c.want)
		}
	}
}

// 冷静期内应拒绝；过期后自动恢复，不需要人工介入。
func TestRiskPauseWindow(t *testing.T) {
	id := "wb-test-pause"
	if until := pausedUntil(id); !until.IsZero() {
		t.Fatal("初始不应处于冷静期")
	}
	pauseProvider(id, 50*time.Millisecond)
	if until := pausedUntil(id); until.IsZero() {
		t.Fatal("暂停后应返回截止时间")
	}
	time.Sleep(80 * time.Millisecond)
	if until := pausedUntil(id); !until.IsZero() {
		t.Fatalf("过期后应自动恢复，得到 %v", until)
	}
}

// 空渠道 id / 非正时长不应建立暂停（避免误伤全部渠道）。
func TestRiskPauseIgnoresInvalid(t *testing.T) {
	pauseProvider("", time.Minute)
	if until := pausedUntil(""); !until.IsZero() {
		t.Fatal("空 id 不应被暂停")
	}
	pauseProvider("wb-zero", 0)
	if until := pausedUntil("wb-zero"); !until.IsZero() {
		t.Fatal("0 时长不应被暂停")
	}
}
