package reverse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"aether-cockpit/internal/provider"
)

// 官方桶探针：17 条 stop 触发官方校验 400 才算命中；200（被夹）不算。
func TestProbeOfficialSessionClassifies(t *testing.T) {
	var hits int32
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		stops, _ := body["stop"].([]any)
		if len(stops) != 17 {
			t.Errorf("探针必须带 17 条 stop，实际 %d", len(stops))
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Stop string array too long: 17"}}`))
	}))
	defer official.Close()

	cell := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-x","choices":[]}`))
	}))
	defer cell.Close()

	p := New(findProfile(t, "opencode-go"))
	client := &http.Client{}
	if !p.probeOfficialSessionForTest(client, official.URL, "m", map[string]string{}, "s1") {
		t.Fatal("官方 400 应判定为命中")
	}
	if p.probeOfficialSessionForTest(client, cell.URL, "m", map[string]string{}, "s1") {
		t.Fatal("200（被夹）不该判定为官方")
	}
	if hits != 1 {
		t.Fatalf("探针请求次数 = %d", hits)
	}
}

// 选号：第一个候选落在中转桶时，应换 ID 重试并最终锁定官方那个。
func TestOfficialSessionRoulette(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := r.Header.Get("x-opencode-session")
		seen = append(seen, sess)
		// 模拟"大多数桶是官方"：只要不是原始 base 会话就命中官方。
		if sess != "base-session" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`Stop string array too long: 17`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-cell"}`))
	}))
	defer srv.Close()

	p := New(findProfile(t, "opencode-go"))
	// 该渠道默认关闭选号（实测路由会漂移），测试里显式打开以覆盖逻辑本身。
	p.profile.OfficialSessionProbe = true
	env := provider.NewAccountEnv("acc", "opencode-go")
	_ = env

	got := p.officialSession(&http.Client{}, srv.URL, "m", func(string) map[string]string {
		return map[string]string{}
	}, "base-session")
	if got == "base-session" || got == "" {
		t.Fatalf("应换一个会话 ID 命中官方桶，得到 %q（尝试过 %v）", got, seen)
	}
	// 第二次应命中缓存，不再打探针。
	before := len(seen)
	again := p.officialSession(&http.Client{}, srv.URL, "m", func(string) map[string]string {
		return map[string]string{}
	}, "base-session")
	if again != got {
		t.Fatalf("缓存应返回同一个会话，得到 %q", again)
	}
	if len(seen) != before {
		t.Fatalf("命中缓存后不该再发探针，新增 %d 次", len(seen)-before)
	}
}

// 全部候选都没命中时，退回原始会话（不影响可用性）。
func TestOfficialSessionAllMissFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-cell"}`))
	}))
	defer srv.Close()
	p := New(findProfile(t, "opencode-go"))
	got := p.officialSession(&http.Client{}, srv.URL, "m", func(string) map[string]string {
		return map[string]string{}
	}, "never-official")
	if got != "never-official" {
		t.Fatalf("应退回原始会话，得到 %q", got)
	}
}

// 探针的 stop 必须正好 17 条（多一条才必然越过官方上限 16）。
func TestProbeStops(t *testing.T) {
	if n := len(probeStops()); n != 17 {
		t.Fatalf("stop 条数 = %d，want 17", n)
	}
}
