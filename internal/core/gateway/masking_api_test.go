package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const maskTestKey = "sk-abcdefghijklmnopqrstuvwxyz123456"

func newMaskTestGateway(t *testing.T) *Gateway {
	t.Helper()
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	eng := g.redactEngine()
	eng.SetEnabled(true)
	key := eng.NewRequestKey()
	eng.MaskWithMeta("deploy with "+maskTestKey, "workbuddy-cn", "m1", key, "", "system")
	eng.MaskWithMeta("mail ops@example.com too", "workbuddy-cn", "m1", key, "", "user")
	return g
}

func serveJSON(t *testing.T, h http.HandlerFunc, method, target string, body string) map[string]any {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s = %d %s", method, target, rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s %s 返回非 JSON: %v (%s)", method, target, err, rec.Body.String())
	}
	return out
}

func TestMaskingSummaryContract(t *testing.T) {
	g := newMaskTestGateway(t)
	sum := serveJSON(t, g.handleMaskingSummary, "GET", "/api/masking/summary?days=7", "")

	if days, ok := sum["days"].([]any); !ok || len(days) != 7 {
		t.Fatalf("days = %#v", sum["days"])
	}
	if sum["requests"].(float64) != 1 {
		t.Fatalf("requests = %v", sum["requests"])
	}
	if sum["n"].(float64) < 2 || sum["distinct"].(float64) != 2 {
		t.Fatalf("n/distinct = %v/%v", sum["n"], sum["distinct"])
	}
	vals, _ := sum["values"].([]any)
	if len(vals) != 2 {
		t.Fatalf("values = %#v", vals)
	}
	first := vals[0].(map[string]any)
	for _, k := range []string{"p", "kind", "by", "head", "len", "n", "requests", "last"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("value 缺字段 %s: %#v", k, first)
		}
	}
	if sum["by"].(map[string]any)["rule"].(float64) < 2 {
		t.Fatalf("by.rule = %#v", sum["by"])
	}
	if sum["enabled"] != true {
		t.Fatal("enabled 应为 true")
	}
}

func TestMaskingRecordsAndDetail(t *testing.T) {
	g := newMaskTestGateway(t)
	recs := serveJSON(t, g.handleMaskingRecords, "GET", "/api/masking/records?limit=40", "")
	list, _ := recs["records"].([]any)
	if len(list) != 1 {
		t.Fatalf("records = %#v", recs)
	}
	row := list[0].(map[string]any)
	key, _ := row["key"].(string)
	if key == "" || strings.ContainsAny(key, "abcdef") {
		t.Fatalf("key 必须是十进制字符串（前端 BigInt）: %q", key)
	}
	if row["n"].(float64) != 2 || row["provider"] != "workbuddy-cn" {
		t.Fatalf("row = %#v", row)
	}
	vals, _ := row["values"].([]any)
	if len(vals) != 2 {
		t.Fatalf("values = %#v", vals)
	}

	one := serveJSON(t, g.handleMaskingRecord, "GET", "/api/masking/record?key="+key, "")
	if one["key"] != key {
		t.Fatalf("detail = %#v", one)
	}

	// 值只按需返回，且只在内存里。
	ps := make([]string, 0, len(vals))
	for _, v := range vals {
		ps = append(ps, v.(map[string]any)["p"].(string))
	}
	raw, _ := json.Marshal(map[string]any{"ps": ps})
	got := serveJSON(t, g.handleMaskingValues, "POST", "/api/masking/values", string(raw))
	values := got["values"].(map[string]any)
	if len(values) != 2 {
		t.Fatalf("values reply = %#v", got)
	}
	found := false
	for _, v := range values {
		if v == maskTestKey {
			found = true
		}
	}
	if !found {
		t.Fatalf("应返回原值: %#v", values)
	}

	single := serveJSON(t, g.handleMaskingValue, "GET", "/api/masking/value?p="+ps[0], "")
	if single["value"] == nil || single["value"] == "" {
		t.Fatalf("value = %#v", single)
	}

	sent := serveJSON(t, g.handleMaskingSent, "GET", "/api/masking/sent?key="+key, "")
	body, _ := sent["body"].(string)
	if body == "" || strings.Contains(body, maskTestKey) {
		t.Fatalf("sent body 应是已脱敏文本: %q", body)
	}
	if !strings.Contains(body, "{{API_KEY_") || !strings.Contains(body, "messages") {
		t.Fatalf("sent body = %q", body)
	}
}

func TestMaskingFilters(t *testing.T) {
	g := newMaskTestGateway(t)
	none := serveJSON(t, g.handleMaskingRecords, "GET", "/api/masking/records?kind=PHONE", "")
	if len(none["records"].([]any)) != 0 {
		t.Fatalf("kind=PHONE 不应命中: %#v", none)
	}
	hit := serveJSON(t, g.handleMaskingRecords, "GET", "/api/masking/records?kind=API_KEY,EMAIL", "")
	if len(hit["records"].([]any)) != 1 {
		t.Fatalf("kind=API_KEY,EMAIL 应命中: %#v", hit)
	}
	model := serveJSON(t, g.handleMaskingRecords, "GET", "/api/masking/records?by=model", "")
	if len(model["records"].([]any)) != 0 {
		t.Fatalf("by=model 当前不应命中: %#v", model)
	}
}

func TestMaskingPreviewAndActions(t *testing.T) {
	g := newMaskTestGateway(t)
	pv := serveJSON(t, g.handleMaskingPreview, "POST", "/api/masking/preview", `{"text":"phone 13812345678 and sk-abcdefghijklmnopqrstuvwxyz123456"}`)
	if pv["off"] != false {
		t.Fatal("开启时 off 应为 false")
	}
	hits, _ := pv["hits"].([]any)
	if len(hits) != 2 {
		t.Fatalf("hits = %#v", hits)
	}

	// 把手机号改成拦截：预览应报告 block，实际请求也会被拦。
	_ = serveJSON(t, g.handleRedactSettings, "POST", "/api/settings/redact-actions", `{"actions":{"PHONE":"block"}}`)
	res, _ := g.redactEngine().Preview("13812345678")
	if !res.Blocked {
		t.Fatal("PHONE 覆盖为 block 后应拦截")
	}
}

func TestMaskingBackCounts(t *testing.T) {
	g := newMaskTestGateway(t)
	rec := g.redactEngine().Records()[0]
	g.redactEngine().NoteBack(rec.Key, "reply "+rec.Hits[0].Placeholder+" and "+rec.Hits[0].Placeholder)
	one := serveJSON(t, g.handleMaskingRecord, "GET", "/api/masking/record?key="+rec.Key, "")
	back, _ := one["back"].(map[string]any)
	if back[rec.Hits[0].Placeholder].(float64) != 2 {
		t.Fatalf("back = %#v", one["back"])
	}
}

func TestMaskingClearAndRestartBoundary(t *testing.T) {
	g := newMaskTestGateway(t)
	_ = serveJSON(t, g.handleMaskingClear, "POST", "/api/masking/clear", "{}")
	sum := serveJSON(t, g.handleMaskingSummary, "GET", "/api/masking/summary", "")
	if sum["requests"].(float64) != 0 || sum["n"].(float64) != 0 {
		t.Fatalf("clear 后 summary = %#v", sum)
	}
}
