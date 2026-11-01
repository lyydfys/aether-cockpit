package spec

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSpecFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDirSingleSpec(t *testing.T) {
	dir := t.TempDir()
	writeSpecFile(t, dir, "demo.json", `{
		"provider_id": "demo",
		"spec_version": "2026.10.26.1",
		"base_url": "https://api.demo.com",
		"client_version": "2.3.1",
		"headers_template": {"X-Version": "{{.Fingerprint.ClientVersion}}"},
		"endpoints": {"chat": "/v2/chat"}
	}`)

	s := NewStore()
	if err := s.LoadDir(dir); err != nil {
		t.Fatalf("加载失败: %v", err)
	}

	sp := s.Get("demo")
	if sp == nil {
		t.Fatal("未加载到 spec")
	}
	if sp.BaseURL != "https://api.demo.com" || sp.ClientVersion != "2.3.1" {
		t.Errorf("字段解析错误: %+v", sp)
	}
	if sp.Endpoint("chat", "/v1/chat") != "/v2/chat" {
		t.Errorf("端点覆盖失败: %q", sp.Endpoint("chat", "/v1/chat"))
	}
	if sp.Endpoint("checkin", "/api/sign") != "/api/sign" {
		t.Error("缺失端点应返回 fallback")
	}
}

func TestLoadDirAggregateFormat(t *testing.T) {
	dir := t.TempDir()
	writeSpecFile(t, dir, "all.json", `{
		"version": 1,
		"specs": [
			{"provider_id": "a", "spec_version": "1", "base_url": "https://a.com"},
			{"provider_id": "b", "spec_version": "1", "base_url": "https://b.com"}
		]
	}`)

	s := NewStore()
	if err := s.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if len(s.All()) != 2 {
		t.Fatalf("条数 = %d, want 2", len(s.All()))
	}
}

func TestReloadRemovesDeletedSpec(t *testing.T) {
	dir := t.TempDir()
	writeSpecFile(t, dir, "a.json", `{"provider_id":"a","spec_version":"1"}`)
	writeSpecFile(t, dir, "b.json", `{"provider_id":"b","spec_version":"1"}`)

	s := NewStore()
	if err := s.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "a.json")); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if s.Get("a") != nil {
		t.Fatal("删除本地文件后 reload 应移除对应 spec")
	}
	if s.Get("b") == nil {
		t.Fatal("未删除的 spec 不应被误删")
	}
}

func TestLoadDirMissingIsOK(t *testing.T) {
	s := NewStore()
	if err := s.LoadDir(filepath.Join(t.TempDir(), "not-exist")); err != nil {
		t.Errorf("目录不存在应视为正常: %v", err)
	}
}

func TestGetReturnsCopy(t *testing.T) {
	s := NewStore()
	writeSpecFile(t, t.TempDir(), "x.json", `{"provider_id":"x","spec_version":"1","base_url":"https://origin"}`)
	_ = s.LoadDir(filepath.Dir(filepath.Join(t.TempDir(), "x.json")))

	// 直接 put 一条
	s.put(&Spec{ProviderID: "x", SpecVersion: "1", BaseURL: "https://origin"}, "test")

	a := s.Get("x")
	a.BaseURL = "https://mutated"
	b := s.Get("x")
	if b.BaseURL != "https://origin" {
		t.Errorf("Get 应返回副本，实际被改写为 %q", b.BaseURL)
	}
}

// TestHotSwap 是核心用例：改 JSON 后不重启，新参数立刻生效。
func TestHotSwap(t *testing.T) {
	dir := t.TempDir()
	writeSpecFile(t, dir, "wb.json", `{"provider_id":"wb","spec_version":"v1","base_url":"https://old.example.com","client_version":"1.0.0"}`)

	s := NewStore()
	if err := s.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("wb").BaseURL; got != "https://old.example.com" {
		t.Fatalf("初始 base_url = %q", got)
	}

	// 模拟上游升级：改文件内容
	writeSpecFile(t, dir, "wb.json", `{"provider_id":"wb","spec_version":"v2","base_url":"https://new.example.com","client_version":"1.5.0"}`)
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}

	sp := s.Get("wb")
	if sp.BaseURL != "https://new.example.com" {
		t.Errorf("热更新未生效，base_url = %q", sp.BaseURL)
	}
	if sp.ClientVersion != "1.5.0" {
		t.Errorf("client_version = %q", sp.ClientVersion)
	}
}

func TestPutSkipsSameVersion(t *testing.T) {
	s := NewStore()
	sp := &Spec{ProviderID: "x", SpecVersion: "1", BaseURL: "https://a"}
	if !s.put(sp, "local:a") {
		t.Fatal("首次写入应返回 true")
	}
	if s.put(sp, "local:a") {
		t.Error("同版本同来源应跳过")
	}
	if !s.put(&Spec{ProviderID: "x", SpecVersion: "2"}, "local:a") {
		t.Error("版本变化应更新")
	}
}

func TestRemoteRejectsHTTP(t *testing.T) {
	s := NewStore()
	if err := s.SetRemote("http://evil.example.com/spec.json"); err == nil {
		t.Error("非 HTTPS 应被拒绝")
	}
	if err := s.SetRemote("http://127.0.0.1:8080/spec.json"); err != nil {
		t.Errorf("localhost 应放行: %v", err)
	}
	if err := s.SetRemote("https://cdn.example.com/spec.json"); err != nil {
		t.Errorf("HTTPS 应放行: %v", err)
	}
}

func TestFetchRemote(t *testing.T) {
	body := `{"version":1,"specs":[{"provider_id":"r","spec_version":"9","base_url":"https://remote.example.com"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	s := NewStore()
	if err := s.SetRemote(srv.URL); err != nil {
		t.Fatal(err)
	}
	n, err := s.Fetch(context.Background())
	if err != nil {
		t.Fatalf("拉取失败: %v", err)
	}
	if n != 1 {
		t.Errorf("生效条数 = %d", n)
	}
	sp := s.Get("r")
	if sp == nil || sp.BaseURL != "https://remote.example.com" {
		t.Fatalf("远程 spec 未生效: %+v", sp)
	}
	if sp.Source == "" {
		t.Error("应记录来源")
	}
	if s.Status().FetchCount != 1 {
		t.Errorf("FetchCount = %d", s.Status().FetchCount)
	}
}

func TestFetchRemoteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := NewStore()
	_ = s.SetRemote(srv.URL)
	if _, err := s.Fetch(context.Background()); err == nil {
		t.Fatal("500 应报错")
	}
	if s.Status().LastError == "" {
		t.Error("应记录错误")
	}
}

func TestSignatureHMAC(t *testing.T) {
	rule := &SignatureRule{
		Algorithm:       "hmac_sha256",
		SaltPlain:       "secret",
		PayloadTemplate: "{{.Method}}\n{{.Path}}\n{{.Timestamp}}\n{{.DeviceID}}",
	}

	ts := time.Unix(1700000000, 0)
	sig1, tsStr, err := rule.Sign(SignInput{
		Method: "post", Path: "/v1/chat", DeviceID: "dev-1", Timestamp: ts,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tsStr != "1700000000" {
		t.Errorf("时间戳 = %q", tsStr)
	}
	if len(sig1) != 64 {
		t.Errorf("HMAC-SHA256 应为 64 位 hex，实际 %d", len(sig1))
	}

	// 同输入必须同签名（确定性）
	sig2, _, _ := rule.Sign(SignInput{Method: "post", Path: "/v1/chat", DeviceID: "dev-1", Timestamp: ts})
	if sig1 != sig2 {
		t.Error("同输入应产生相同签名")
	}

	// 换设备必须换签名
	sig3, _, _ := rule.Sign(SignInput{Method: "post", Path: "/v1/chat", DeviceID: "dev-2", Timestamp: ts})
	if sig1 == sig3 {
		t.Error("不同设备应产生不同签名")
	}
}

func TestSignatureHexSalt(t *testing.T) {
	rule := &SignatureRule{
		Algorithm: "hmac_sha256",
		SaltHex:   hex.EncodeToString([]byte("secret")),
		// 与 TestSignatureHMAC 的 SaltPlain 等价，签名应当一致
		PayloadTemplate: "{{.Method}}\n{{.Path}}\n{{.Timestamp}}\n{{.DeviceID}}",
	}
	plain := &SignatureRule{
		Algorithm:       "hmac_sha256",
		SaltPlain:       "secret",
		PayloadTemplate: "{{.Method}}\n{{.Path}}\n{{.Timestamp}}\n{{.DeviceID}}",
	}

	in := SignInput{Method: "GET", Path: "/x", DeviceID: "d", Timestamp: time.Unix(1700000000, 0)}
	a, _, _ := rule.Sign(in)
	b, _, _ := plain.Sign(in)
	if a != b {
		t.Errorf("hex 盐与明文字盐应等价: %s vs %s", a, b)
	}
}

func TestSignatureNone(t *testing.T) {
	var rule *SignatureRule
	sig, _, err := rule.Sign(SignInput{})
	if err != nil || sig != "" {
		t.Errorf("nil rule 应返回空签名: %q %v", sig, err)
	}
	r := &SignatureRule{Algorithm: "none"}
	sig, _, _ = r.Sign(SignInput{})
	if sig != "" {
		t.Error("algorithm=none 应返回空签名")
	}
}

func TestSignatureTimestampMS(t *testing.T) {
	rule := &SignatureRule{Algorithm: "md5", SaltPlain: "k", TimestampUnit: "ms"}
	_, tsStr, err := rule.Sign(SignInput{Timestamp: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if tsStr != "1700000000000" {
		t.Errorf("毫秒时间戳 = %q", tsStr)
	}
}

func TestMapModel(t *testing.T) {
	sp := &Spec{Models: map[string]string{"claude-4-sonnet": "claude-sonnet-4-20250514"}}
	if got := sp.MapModel("claude-4-sonnet"); got != "claude-sonnet-4-20250514" {
		t.Errorf("映射失败: %q", got)
	}
	if got := sp.MapModel("unknown"); got != "unknown" {
		t.Errorf("未映射时应原样返回: %q", got)
	}
	var nilSpec *Spec
	if got := nilSpec.MapModel("x"); got != "x" {
		t.Error("nil spec 应原样返回")
	}
}

func TestGlobalDefault(t *testing.T) {
	old := Default()
	defer SetDefault(old)

	s := NewStore()
	s.put(&Spec{ProviderID: "g", SpecVersion: "1", BaseURL: "https://g"}, "test")
	SetDefault(s)

	if Load("g") == nil {
		t.Fatal("全局 Load 失败")
	}
	if Load("nope") != nil {
		t.Error("不存在时应返回 nil")
	}
}
