package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// extraBody 落盘、回显为 JSON 文本、并真正合并进上游 JSON 请求体。
func TestProviderExtraBodySaveEchoAndMerge(t *testing.T) {
	var got map[string]any
	var rawBody, lastCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		rawBody = string(b)
		got = nil
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	g := headersGateway(t)
	if rec := saveHeaders(t, g, `{"id":"mock-native","extraBody":{"prompt_cache_key":"k1","metadata":{"a":1}}}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	view := providerViewOf(t, g, "mock-native")
	eb, _ := view["extraBody"].(map[string]any)
	if eb["prompt_cache_key"] != "k1" {
		t.Fatalf("extraBody 应以对象回显: %v", view["extraBody"])
	}

	env := g.pool.ListByProvider("mock-native")[0]
	env.SetHTTPClient(srv.Client())
	g.applyProviderTransport(env)

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}],"metadata":{"b":2}}`
	resp, err := env.HTTPClient.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if got["prompt_cache_key"] != "k1" {
		t.Fatalf("上游没收到 extraBody 字段: %+v（ct=%s）", got, lastCT)
	}
	md, _ := got["metadata"].(map[string]any)
	if md["a"] != float64(1) || md["b"] != float64(2) {
		t.Fatalf("嵌套对象应深度合并: %+v", md)
	}
	if got["model"] != "m" || got["messages"] == nil {
		t.Fatalf("原有字段不应被破坏: %+v", got)
	}

	// 非 JSON 请求体不动。
	resp, err = env.HTTPClient.Post(srv.URL, "application/x-www-form-urlencoded", strings.NewReader("a=1"))
	if err != nil {
		t.Fatalf("POST form: %v", err)
	}
	resp.Body.Close()
	if rawBody != "a=1" || lastCT != "application/x-www-form-urlencoded" {
		t.Fatalf("非 JSON 请求体应原样发送，got ct=%q body=%q", lastCT, rawBody)
	}
}

// 非对象 / 保留字段 / 超大 payload 必须 400；空对象清除配置。
func TestProviderExtraBodyRejects(t *testing.T) {
	g := headersGateway(t)
	cases := []string{
		`{"id":"mock-native","extraBody":[1,2]}`,
		`{"id":"mock-native","extraBody":"not-json"}`,
		`{"id":"mock-native","extraBody":{"messages":[1]}}`,
		`{"id":"mock-native","extraBody":{"model":"x"}}`,
	}
	for _, body := range cases {
		rec := saveHeaders(t, g, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应 400，got %d：%s", body, rec.Code, rec.Body.String())
		}
	}

	// 合法保存后，空对象清除。
	if rec := saveHeaders(t, g, `{"id":"mock-native","extraBody":{"prompt_cache_key":"k"}}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d", rec.Code)
	}
	if rec := saveHeaders(t, g, `{"id":"mock-native","extraBody":{}}`); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d", rec.Code)
	}
	if v := providerViewOf(t, g, "mock-native")["extraBody"]; v != nil {
		t.Fatalf("清空后不应回显: %v", v)
	}
}
