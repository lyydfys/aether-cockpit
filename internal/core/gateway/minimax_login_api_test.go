package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/providers/minimax"
)

func TestMiniMaxDeviceLoginFlow(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/oauth2/device/code":
			_, _ = io.WriteString(w, `{"device_code":"dc","user_code":"UC-9","verification_uri":"https://example.com/v","expires_in":600,"interval":1}`)
		case r.Method == http.MethodPost && r.URL.Path == "/oauth2/token":
			polls++
			if polls == 1 {
				_, _ = io.WriteString(w, `{"status":"pending"}`)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"at1","refresh_token":"rt1","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/api/user/info":
			_, _ = io.WriteString(w, `{"data":{"userInfo":{"realUserID":"uid1","email":"minimax@example.com","name":"MM"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := minimax.AccountBaseOverride
	minimax.AccountBaseOverride = srv.URL
	defer func() { minimax.AccountBaseOverride = old }()

	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/provider/minimax/device", g.handleMiniMaxDeviceStart)
	mux.HandleFunc("GET /api/provider/minimax/device/{id}", g.handleMiniMaxDevicePoll)
	mux.HandleFunc("POST /api/provider/minimax/device/{id}/cancel", g.handleMiniMaxDeviceCancel)
	call := func(method, path string) map[string]any {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(`{"site":"minimax-code"}`)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d %s", method, path, rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	start := call("POST", "/api/provider/minimax/device")
	id, _ := start["id"].(string)
	if id == "" || start["user_code"] != "UC-9" || start["verification_uri"] != "https://example.com/v" {
		t.Fatalf("start = %#v", start)
	}
	if p := call("GET", "/api/provider/minimax/device/"+id); p["state"] != "pending" {
		t.Fatalf("pending = %#v", p)
	}
	done := call("GET", "/api/provider/minimax/device/"+id)
	if done["state"] != "done" || done["user"] != "minimax@example.com" {
		t.Fatalf("done = %#v", done)
	}
	envs := g.pool.ListByProvider("minimax-code")
	if len(envs) != 1 {
		t.Fatalf("accounts = %d", len(envs))
	}
	env := envs[0]
	if env.GetCredential("access_token") != "at1" || env.GetCredential("refresh_token") != "rt1" || env.GetCredential("user_id") != "uid1" {
		t.Fatalf("credentials = %v", env.Credentials)
	}
	if env.Label != "minimax@example.com" {
		t.Fatalf("label = %q", env.Label)
	}
}
