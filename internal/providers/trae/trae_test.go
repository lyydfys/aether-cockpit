package trae

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// --- tc 加密回环 ---

func TestTcDecryptRoundTrip(t *testing.T) {
	payload := []byte(`{"token":"test-token","userId":"u-1","account":{"username":"alice"}}`)
	headers := map[string][]byte{
		tcEncTypeAES:  {'t', 'c', 0x05, 0x10, 0x00, 0x00},
		tcEncTypeAESP: {0x12, 0x39, 0x20, 0x20, 0x02, 0x03},
	}
	for name, header := range headers {
		t.Run(name, func(t *testing.T) {
			blob := encryptTcForTest(t, payload, header)
			if name == tcEncTypeAES && !isTcEncrypted(blob) {
				t.Fatal("加密结果缺少 tc 头")
			}
			got, err := decryptTcStorageValue(base64.StdEncoding.EncodeToString(blob))
			if err != nil {
				t.Fatalf("解密失败: %v", err)
			}
			if got != string(payload) {
				t.Fatalf("明文 = %q, want %q", got, payload)
			}
		})
	}
}

func TestTcDecryptRejectsCorrupted(t *testing.T) {
	blob := encryptTcForTest(t, []byte(`{"token":"test-token"}`), []byte{'t', 'c', 0x05, 0x10, 0x00, 0x00})
	blob[len(blob)-1] ^= 0xFF
	if _, err := decryptTcBuffer(blob); err == nil {
		t.Fatal("损坏密文应校验失败")
	}
}

// encryptTcForTest 按参考实现生成 tc 密文：头 | 32 随机字节 | AES-128-CBC(摘要+明文)。
func encryptTcForTest(t *testing.T, payload, header []byte) []byte {
	t.Helper()
	randomBytes := make([]byte, tcRandomLen)
	if _, err := rand.Read(randomBytes); err != nil {
		t.Fatal(err)
	}
	key, iv := deriveTcKeyAndIV(randomBytes, detectTcEncType(header))
	sum := sha512.Sum512(payload)
	plain := make([]byte, 0, tcHashSize+len(payload))
	plain = append(plain, sum[:]...)
	plain = append(plain, payload...)
	plain = pkcs7Pad(plain)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, plain)

	out := make([]byte, 0, len(header)+len(randomBytes)+len(ciphertext))
	out = append(out, header...)
	out = append(out, randomBytes...)
	out = append(out, ciphertext...)
	return out
}

func pkcs7Pad(data []byte) []byte {
	pad := aes.BlockSize - len(data)%aes.BlockSize
	return append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)
}

// --- 本机导入 ---

func TestLocalImportPlaintext(t *testing.T) {
	root := isolateLocalEnv(t)
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	auth := map[string]any{
		"token":        "test-token",
		"refreshToken": "test-refresh",
		"expiredAt":    expiry.Format(time.RFC3339),
		"userId":       "user-cn-1",
		"host":         cnChatHost,
		"userRegion":   "CN",
		"account":      map[string]any{"username": "alice"},
	}
	rawAuth, _ := json.Marshal(auth)
	storage := map[string]any{
		authStorageKey:                              string(rawAuth),
		"telemetry.machineId":                       "machine-abc",
		"telemetry.devDeviceId":                     "dev-device-1",
		"telemetry.sqmId":                           "{SQM-1}",
		"iCubeAuthInfo://icube-dc:1234567890123456": "ignored",
	}
	writeStorage(t, filepath.Join(root, "Trae CN"), storage)

	p := newProvider("trae-cn", "CN")
	drafts, err := p.DiscoverLocalAccounts(context.Background())
	if err != nil {
		t.Fatalf("DiscoverLocalAccounts 失败: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("drafts = %d, want 1", len(drafts))
	}
	d := drafts[0]
	want := map[string]string{
		"token":         "test-token",
		"refresh_token": "test-refresh",
		"user_id":       "user-cn-1",
		"machine_id":    "machine-abc",
		"dev_device_id": "dev-device-1",
		"sqm_id":        "{SQM-1}",
		"device_id":     "1234567890123456",
		"region":        "CN",
	}
	for key, value := range want {
		if got := d.Credentials[key]; got != value {
			t.Errorf("credentials[%s] = %q, want %q", key, got, value)
		}
	}
	if d.Credentials["expires_at"] == "" || d.ExpiresAt != expiry.Unix() {
		t.Fatalf("expires_at = %q / %d, want %s", d.Credentials["expires_at"], d.ExpiresAt, expiry)
	}
	if !strings.Contains(d.Label, "alice") || !strings.Contains(d.Label, "CN") {
		t.Fatalf("label = %q", d.Label)
	}
	if !strings.HasPrefix(d.Source, "local_import:") {
		t.Fatalf("source = %q", d.Source)
	}
}

func TestLocalImportEncrypted(t *testing.T) {
	root := isolateLocalEnv(t)

	auth := map[string]any{
		"token":        "test-token-enc",
		"refreshToken": "test-refresh-enc",
		"expiredAt":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		"userId":       987654321,
		"host":         sgChatHost,
		"userRegion":   map[string]any{"region": "US"},
		"account":      map[string]any{"username": "bob"},
	}
	rawAuth, _ := json.Marshal(auth)
	blob := base64.StdEncoding.EncodeToString(
		encryptTcForTest(t, rawAuth, []byte{'t', 'c', 0x05, 0x10, 0x00, 0x00}),
	)
	storage := map[string]any{
		authStorageKey:          blob,
		"telemetry.machineId":   "machine-sg",
		"telemetry.devDeviceId": "dev-device-sg",
		"telemetry.sqmId":       "{SQM-SG}",
	}
	writeStorage(t, filepath.Join(root, "Trae"), storage)

	p := newProvider("trae-intl", "SG")
	drafts, err := p.DiscoverLocalAccounts(context.Background())
	if err != nil {
		t.Fatalf("DiscoverLocalAccounts 失败: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("drafts = %d, want 1", len(drafts))
	}
	d := drafts[0]
	if got := d.Credentials["token"]; got != "test-token-enc" {
		t.Fatalf("token = %q", got)
	}
	if got := d.Credentials["user_id"]; got != "987654321" {
		t.Fatalf("user_id = %q", got)
	}
	if got := d.Credentials["region"]; got != "US" {
		t.Fatalf("region = %q, want US", got)
	}
	if got := d.Credentials["device_id"]; got != hashDeviceID("machine-sg") {
		t.Fatalf("device_id = %q, want 哈希兜底", got)
	}
	if !strings.Contains(d.Label, "bob") || !strings.Contains(d.Label, "US") {
		t.Fatalf("label = %q", d.Label)
	}
}

func isolateLocalEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("HOME", root)
	return root
}

func writeStorage(t *testing.T, productDir string, storage map[string]any) {
	t.Helper()
	dir := filepath.Join(productDir, "User", "globalStorage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(storage)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "storage.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// --- ChatStream ---

func TestChatStreamRequestAndSSE(t *testing.T) {
	var gotPath, gotAuth, gotCloudToken, gotDeviceID, gotMachineID, gotUID, gotUA string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCloudToken = r.Header.Get("X-Cloudide-Token")
		gotDeviceID = r.Header.Get("x-device-id")
		gotMachineID = r.Header.Get("x-machine-id")
		gotUID = r.Header.Get("x-uid")
		gotUA = r.Header.Get("User-Agent")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("Content-Type", "text/event-stream")
		frames := []struct{ event, data string }{
			{"output", `{"content":"Hel"}`},
			{"output", `{"reasoning":"think"}`},
			{"queue_begin", `{}`},
			{"output", `{"content":"lo"}`},
			{"token_usage", `{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}`},
			{"done", `{"finish_reason":"stop"}`},
		}
		for _, f := range frames {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", f.event, f.data)
		}
	}))
	defer srv.Close()

	env := testEnv(srv.URL)
	p := newProvider("trae-cn", "CN")
	req := &ir.UnifiedChatRequest{
		Instructions:  "you are helpful",
		Model:         "trae-cn/glm-5.2",
		UpstreamModel: "glm-5.2",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}

	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatalf("ChatStream 失败: %v", err)
	}

	var text, reasoning strings.Builder
	var usage *ir.Usage
	finish := ""
	for ev := range ch {
		switch ev.Type {
		case ir.EventTextDelta:
			text.WriteString(ev.TextDelta)
		case ir.EventReasoningDelta:
			reasoning.WriteString(ev.TextDelta)
		case ir.EventUsage:
			usage = ev.Usage
		case ir.EventDone:
			finish = ev.FinishReason
		case ir.EventError:
			t.Fatalf("意外错误事件: %v", ev.Err)
		}
	}

	if gotPath != chatPath {
		t.Fatalf("path = %q, want %q", gotPath, chatPath)
	}
	if gotAuth != "Cloud-IDE-JWT test-token" || gotCloudToken != "test-token" {
		t.Fatalf("auth = %q / %q", gotAuth, gotCloudToken)
	}
	if gotDeviceID != "dev-1" || gotMachineID != "mach-1" || gotUID != "user-1" {
		t.Fatalf("device headers = %q %q %q", gotDeviceID, gotMachineID, gotUID)
	}
	// 上游按 UA 风控：Go 默认 UA 会导致流内 4011。
	if !strings.HasPrefix(gotUA, "Trae/") {
		t.Fatalf("User-Agent = %q, want Trae/<version>", gotUA)
	}
	if gotBody["function"] != "chat_v3" {
		t.Fatalf("function = %v", gotBody["function"])
	}
	if gotBody["model"] != "glm-5.2" {
		t.Fatalf("model = %v", gotBody["model"])
	}
	if _, ok := gotBody["config_name"]; ok {
		t.Fatalf("不应再发送 config_name: %+v", gotBody)
	}
	if gotBody["stream"] != true {
		t.Fatalf("stream = %v", gotBody["stream"])
	}

	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) < 2 {
		t.Fatalf("messages = %+v", msgs)
	}
	userMsg, _ := msgs[1].(map[string]any)
	if userMsg["role"] != "user" {
		t.Fatalf("role = %v", userMsg["role"])
	}
	parts, _ := userMsg["content"].([]any)
	if len(parts) != 1 {
		t.Fatalf("parts = %+v", parts)
	}
	part, _ := parts[0].(map[string]any)
	if part["type"] != "text" || part["text"] != "hi" {
		t.Fatalf("part = %+v", part)
	}

	if text.String() != "Hello" {
		t.Fatalf("text = %q, want Hello", text.String())
	}
	if reasoning.String() != "think" {
		t.Fatalf("reasoning = %q", reasoning.String())
	}
	if usage == nil || usage.InputTokens != 3 || usage.OutputTokens != 4 || usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v", usage)
	}
	if finish != "stop" {
		t.Fatalf("finish = %q", finish)
	}
}

func TestChatStreamAutoMapsToDefaultModel(t *testing.T) {
	cases := []struct {
		providerID string
		region     string
		wantModel  string
	}{
		{"trae-cn", "CN", "glm-5.1"},
		{"trae-intl", "SG", "gpt-5.6"},
	}
	for _, tc := range cases {
		t.Run(tc.providerID, func(t *testing.T) {
			var gotBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &gotBody)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: done\ndata: {\"finish_reason\":\"stop\"}\n\n")
			}))
			defer srv.Close()

			p := newProvider(tc.providerID, tc.region)
			req := &ir.UnifiedChatRequest{
				Model: tc.providerID + "/auto",
				Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
			}
			ch, err := p.ChatStream(context.Background(), testEnv(srv.URL), req)
			if err != nil {
				t.Fatalf("ChatStream 失败: %v", err)
			}
			for range ch {
			}
			if gotBody["function"] != "chat_v3" {
				t.Fatalf("function = %v", gotBody["function"])
			}
			if gotBody["model"] != tc.wantModel {
				t.Fatalf("model = %v, want %s", gotBody["model"], tc.wantModel)
			}
			if _, ok := gotBody["config_name"]; ok {
				t.Fatalf("auto 映射不应带 config_name: %+v", gotBody)
			}
		})
	}
}

func TestToolHistoryFlattening(t *testing.T) {
	msgs := buildMessages(&ir.UnifiedChatRequest{
		Instructions: "sys",
		Tools: []ir.UnifiedToolSpec{{
			Name:        "get_weather",
			Description: "查询天气",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "天气"},
			{Type: ir.ItemTypeFunctionCall, CallID: "call-1", Name: "get_weather", Arguments: `{"city":"SZ"}`},
			{Type: ir.ItemTypeFunctionCallOutput, CallID: "call-1", Output: "晴"},
		},
	})
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4: %+v", len(msgs), msgs)
	}
	system, _ := msgs[0].(map[string]any)
	if system["role"] != "system" || !strings.Contains(messageText(system), "get_weather") {
		t.Fatalf("system = %+v", system)
	}
	assistant, _ := msgs[2].(map[string]any)
	if assistant["role"] != "assistant" || !strings.Contains(messageText(assistant), `<toolcall>`) {
		t.Fatalf("assistant = %+v", assistant)
	}
	toolResult, _ := msgs[3].(map[string]any)
	if toolResult["role"] != "user" || !strings.Contains(messageText(toolResult), `<tool_result for="call-1">`) {
		t.Fatalf("tool result = %+v", toolResult)
	}
}

func messageText(msg map[string]any) string {
	parts, _ := msg["content"].([]any)
	var b strings.Builder
	for _, raw := range parts {
		if part, ok := raw.(map[string]any); ok {
			b.WriteString(rawString(part["text"]))
		}
	}
	return b.String()
}

// --- 错误映射 ---

func TestSSEErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: error\ndata: {\"code\":4011,\"message\":\"rate limited\"}\n\n")
	}))
	defer srv.Close()

	p := newProvider("trae-cn", "CN")
	ch, err := p.ChatStream(context.Background(), testEnv(srv.URL), simpleRequest())
	if err != nil {
		t.Fatalf("ChatStream 失败: %v", err)
	}
	var apiErr *ir.APIError
	for ev := range ch {
		if ev.Type == ir.EventError {
			if !errors.As(ev.Err, &apiErr) {
				t.Fatalf("错误类型 = %T", ev.Err)
			}
		}
	}
	if apiErr == nil {
		t.Fatal("缺少 error 事件")
	}
	if apiErr.Code != ir.ErrCodeUpstreamRateLimit {
		t.Fatalf("code = %q", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "rate limited") || !strings.Contains(apiErr.Message, "4011") {
		t.Fatalf("message = %q", apiErr.Message)
	}
}

func TestHTTPErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"code":1001,"message":"token expired"}`)
	}))
	defer srv.Close()

	p := newProvider("trae-cn", "CN")
	_, err := p.ChatStream(context.Background(), testEnv(srv.URL), simpleRequest())
	if err == nil {
		t.Fatal("401 应返回错误")
	}
	var apiErr *ir.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("错误类型 = %T", err)
	}
	if apiErr.Code != ir.ErrCodeUpstreamAuth {
		t.Fatalf("code = %q", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "token expired") {
		t.Fatalf("message = %q", apiErr.Message)
	}
}

func testEnv(baseURL string) *provider.AccountEnv {
	env := provider.NewAccountEnv("acc-1", "trae-cn")
	env.Credentials = map[string]string{
		"token":      "test-token",
		"user_id":    "user-1",
		"machine_id": "mach-1",
		"device_id":  "dev-1",
		"region":     "CN",
		"base_url":   baseURL,
	}
	return env
}

func simpleRequest() *ir.UnifiedChatRequest {
	return &ir.UnifiedChatRequest{
		Model:         "trae-cn/glm-4.7",
		UpstreamModel: "glm-4.7",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
}

// --- Manifest ---

func TestManifestsAndEndpoints(t *testing.T) {
	for _, id := range []string{"trae-cn", "trae-intl"} {
		raw, ok := provider.Get(id)
		if !ok {
			t.Fatalf("%s 未注册", id)
		}
		m := raw.Manifest()
		if len(m.Models) == 0 {
			t.Fatalf("%s 模型清单为空", id)
		}
		if !m.Capabilities.Chat || !m.Capabilities.LocalImport {
			t.Fatalf("%s 能力声明不完整: %+v", id, m.Capabilities)
		}
		if !m.HasMiddleware("stateful_session") {
			t.Fatalf("%s 缺少 stateful_session", id)
		}
		hasAuto := false
		for _, model := range m.Models {
			if model.ID == id+"/auto" {
				hasAuto = true
			}
			if !strings.HasPrefix(model.ID, id+"/") {
				t.Fatalf("%s 模型 ID 前缀错误: %q", id, model.ID)
			}
		}
		if !hasAuto {
			t.Fatalf("%s 缺少 auto 模型", id)
		}
	}

	cn, _ := provider.Get("trae-cn")
	cnProvider := cn.(*Provider)
	env := provider.NewAccountEnv("acc-cn", "trae-cn")
	if got := cnProvider.baseURL(env); got != cnChatHost {
		t.Fatalf("CN base URL = %q, want %q", got, cnChatHost)
	}

	intl, _ := provider.Get("trae-intl")
	intlProvider := intl.(*Provider)
	env = provider.NewAccountEnv("acc-sg", "trae-intl")
	if got := intlProvider.baseURL(env); got != sgChatHost {
		t.Fatalf("SG base URL = %q, want %q", got, sgChatHost)
	}
	env.SetCredential("region", "US")
	if got := intlProvider.baseURL(env); got != usChatHost {
		t.Fatalf("US base URL = %q, want %q", got, usChatHost)
	}
}
