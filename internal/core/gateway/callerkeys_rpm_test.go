package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCallerKeyRPMSlidingWindow(t *testing.T) {
	s := newCallerKeyStore("")
	now := time.Now()
	if !s.allowRPM("k", 2, now) || !s.allowRPM("k", 2, now.Add(10*time.Second)) {
		t.Fatal("前两次应放行")
	}
	if s.allowRPM("k", 2, now.Add(20*time.Second)) {
		t.Fatal("第三次应被限流")
	}
	// 超过 60 秒后旧记录滑出窗口。
	if !s.allowRPM("k", 2, now.Add(61*time.Second)) {
		t.Fatal("滑窗应放行")
	}
	// rpm<=0 不限。
	for i := 0; i < 10; i++ {
		if !s.allowRPM("free", 0, now) {
			t.Fatal("rpm=0 不应限流")
		}
	}
	// 重置后计数清零。
	s.resetRPM("k")
	if !s.allowRPM("k", 2, now.Add(2*time.Minute)) || !s.allowRPM("k", 2, now.Add(2*time.Minute)) {
		t.Fatal("重置后应恢复计数")
	}
}

func TestAuthEnforcesCallerKeyRPM(t *testing.T) {
	g := newCallerKeyGateway(t)
	key, secret := g.callerKeys.add("limited")
	if !g.callerKeys.update(key.ID, func(k *callerKey) bool {
		k.Limit = &callerKeyLimit{RPM: 1}
		return true
	}) {
		t.Fatal("设置限额失败")
	}
	ok := false
	h := g.auth(func(w http.ResponseWriter, r *http.Request) {
		ok = true
		w.WriteHeader(http.StatusOK)
	})

	call := func() int {
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		req.RemoteAddr = "10.0.0.9:3333"
		req.Header.Set("Authorization", "Bearer "+secret)
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code
	}
	if code := call(); code != http.StatusOK || !ok {
		t.Fatalf("第一次应放行: %d", code)
	}
	if code := call(); code != http.StatusTooManyRequests {
		t.Fatalf("第二次应 429，得到 %d", code)
	}
}
