package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 没填模型的自定义渠道：后台探测把上游真实模型写回清单，
// 让 /v1/models 能看到中转站的模型（而不是只剩内置渠道）。
func TestCustomChannelModelDiscovery(t *testing.T) {
	var hits int32
	var gotAuth, gotCustomHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotAuth = r.Header.Get("Authorization")
		gotCustomHeader = r.Header.Get("X-Api-Name")
		if r.URL.Path != "/v1/models" {
			t.Errorf("探测路径 = %q, want /v1/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"relay-model-a"},{"id":"relay-model-b"}
		]}`))
	}))
	defer srv.Close()

	g := newSupplierGateway(t)
	// 建渠道时不填模型。
	body := `{"id":"relay-x","name":"Relay X","chat":"` + srv.URL + `/v1/chat/completions","key":"sk-x",
		"headers":{"X-Api-Name":"tenant-1"}}`
	if rec := saveSupplier(g, body); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	// 保存触发的是异步探测：等它落盘。
	deadline := time.Now().Add(5 * time.Second)
	var ch *CustomChannel
	for time.Now().Before(deadline) {
		ch = findSupplierChannel(supplierChannels(g), "relay-x")
		if ch != nil && len(ch.Models) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ch == nil || len(ch.Models) != 2 || ch.Models[0] != "relay-model-a" {
		t.Fatalf("模型未写回：%+v（hits=%d）", ch, atomic.LoadInt32(&hits))
	}
	if gotAuth != "Bearer sk-x" {
		t.Fatalf("探测未带账号 key：%q", gotAuth)
	}
	if gotCustomHeader != "tenant-1" {
		t.Fatalf("探测未带渠道自定义头：%q", gotCustomHeader)
	}

	// manifest 与路由表都要能看到这些模型。
	rec := httptest.NewRecorder()
	g.respondProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	var out struct {
		Providers []struct {
			ID     string `json:"id"`
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, pv := range out.Providers {
		if pv.ID != "custom-relay-x" {
			continue
		}
		for _, m := range pv.Models {
			if m.ID == "relay-model-a" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("providers 里看不到发现的模型: %s", rec.Body.String())
	}
}

// 用户手填了模型：不覆盖、不探测。
func TestCustomChannelModelDiscoverySkipsFilledModels(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"data":[{"id":"upstream-model"}]}`))
	}))
	defer srv.Close()

	g := newSupplierGateway(t)
	body := `{"id":"relay-fixed","name":"Relay Fixed","chat":"` + srv.URL + `/v1/chat/completions",
		"key":"sk-y","models":["my-model"]}`
	if rec := saveSupplier(g, body); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	time.Sleep(150 * time.Millisecond)
	ch := findSupplierChannel(supplierChannels(g), "relay-fixed")
	if ch == nil || len(ch.Models) != 1 || ch.Models[0] != "my-model" {
		t.Fatalf("手填清单被覆盖：%+v", ch)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("清单非空时不应该探测上游，实际请求 %d 次", hits)
	}
}

// 探测失败：不写空、保留原清单，且不报错给用户。
func TestCustomChannelModelDiscoveryFailureKeepsList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	g := newSupplierGateway(t)
	body := `{"id":"relay-bad","name":"Relay Bad","chat":"` + srv.URL + `/v1/chat/completions","key":"sk-z"}`
	if rec := saveSupplier(g, body); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	time.Sleep(250 * time.Millisecond)
	ch := findSupplierChannel(supplierChannels(g), "relay-bad")
	if ch == nil {
		t.Fatal("渠道应仍存在")
	}
	if len(ch.Models) != 0 {
		t.Fatalf("探测失败不应写入任何模型：%+v", ch.Models)
	}
}

// 手动刷新端点：同步返回模型并写回。
func TestCustomChannelRefreshModelsEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"}]}`))
	}))
	defer srv.Close()

	g := newSupplierGateway(t)
	// 用带模型的渠道避免后台探测抢跑；随后清空模型再手动刷新。
	body := `{"id":"relay-manual","name":"Relay Manual","chat":"` + srv.URL + `/v1/chat/completions",
		"key":"sk-m","models":["placeholder"]}`
	if rec := saveSupplier(g, body); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/custom-channels/relay-manual/refresh-models", nil)
	req.SetPathValue("id", "relay-manual")
	rec := httptest.NewRecorder()
	g.handleCustomChannelRefreshModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK     bool     `json:"ok"`
		Count  int      `json:"count"`
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.OK || out.Count != 2 || len(out.Models) != 2 {
		t.Fatalf("refresh 返回 = %+v", out)
	}
	ch := findSupplierChannel(supplierChannels(g), "relay-manual")
	if ch == nil || len(ch.Models) != 2 {
		t.Fatalf("手动刷新未写回：%+v", ch)
	}

	// 不存在的渠道 → 404。
	req2 := httptest.NewRequest(http.MethodPost, "/api/custom-channels/nope/refresh-models", nil)
	req2.SetPathValue("id", "nope")
	rec2 := httptest.NewRecorder()
	g.handleCustomChannelRefreshModels(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("未知渠道应 404，实际 %d", rec2.Code)
	}
}

// 「留空 = 任意模型透传」：探测失败后 custom-xxx/<任意模型> 仍可解析。
func TestCustomChannelEmptyModelsStillPassThrough(t *testing.T) {
	g := newSupplierGateway(t)
	body := `{"id":"relay-pass","name":"Relay Pass","chat":"https://pass.example.com/v1/chat/completions","key":"sk-p"}`
	if rec := saveSupplier(g, body); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	// 让后台探测失败并结束（域名不可解析），避免测试结束时仍有 goroutine。
	time.Sleep(600 * time.Millisecond)

	if len(g.models.Resolve("custom-relay-pass/anything-goes")) == 0 {
		t.Fatal("空清单渠道应支持任意模型透传")
	}
}

// parseCustomModelList 覆盖三种常见响应形状。
func TestParseCustomModelList(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"openai", `{"data":[{"id":"a"},{"id":"b"}]}`, []string{"a", "b"}},
		{"gemini", `{"models":[{"name":"models/gemini-pro"}]}`, []string{"gemini-pro"}},
		{"bare", `[{"id":"x"},{"name":"y"}]`, []string{"x", "y"}},
		{"dedupe", `{"data":[{"id":"a"},{"id":"a"}]}`, []string{"a"}},
		{"empty", `{}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseCustomModelList([]byte(c.raw))
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

var _ = strings.TrimSpace
