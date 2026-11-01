package gateway

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/tools"
)

func TestAudioProxyAndSpeech(t *testing.T) {
	var speechModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-audio" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/audio/transcriptions":
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				t.Errorf("ct = %q", r.Header.Get("Content-Type"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"text":"你好"}`))
		case "/audio/speech":
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(r.Body)
			speechModel = buf.String()
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("ID3fakeaudio"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	g := New(Config{Addr: "127.0.0.1:0", UsagePath: "-", AccountStorePath: filepath.Join(t.TempDir(), "accounts.json")})
	g.settings.Tools = &ToolsConfig{Audio: &tools.AudioConfig{BaseURL: srv.URL, APIKey: "sk-audio", Model: "whisper-1"}}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	fw, _ := mw.CreateFormFile("file", "a.wav")
	_, _ = fw.Write([]byte("RIFF"))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	g.handleAudioProxy("/audio/transcriptions")(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "你好") || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("transcription status=%d ct=%q body=%s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hi","voice":"alloy"}`))
	rec = httptest.NewRecorder()
	g.handleAudioSpeech(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "audio/mpeg" || rec.Body.String() != "ID3fakeaudio" {
		t.Fatalf("speech status=%d ct=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if !strings.Contains(speechModel, "whisper-1") {
		t.Fatalf("speech model 未注入: %s", speechModel)
	}
}
