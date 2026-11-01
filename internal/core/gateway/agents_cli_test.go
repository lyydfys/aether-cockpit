package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseAgentVersion(t *testing.T) {
	cases := map[string]string{
		"2.1.42 (Claude Code)":              "2.1.42",
		"codex-cli 0.48.0":                  "0.48.0",
		"v1.2":                              "1.2",
		"no version here":                   "",
		"opencode 0.6.12-beta.1":            "0.6.12-beta.1",
		"Gemini CLI v0.11.3\nnode v24.18.0": "0.11.3",
	}
	for in, want := range cases {
		if got := parseAgentVersion(in); got != want {
			t.Fatalf("parse(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDetectInstallMethod(t *testing.T) {
	cases := map[string]string{
		`C:\Users\u\AppData\Roaming\npm\claude.cmd`: "npm",
		"/usr/local/lib/node_modules/codex/bin/x":   "npm",
		"/opt/homebrew/Cellar/aider/1.0/bin/aider":  "brew",
		"/usr/lib/python3/site-packages/bin/aider":  "pip",
		`C:\tools\agent.exe`:                        "direct",
	}
	for in, want := range cases {
		if got := detectInstallMethod(in); got != want {
			t.Fatalf("method(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDetectAgentCLIWithStubRunner(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "fakeagent.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	oldRunner := agentVersionRunner
	agentVersionRunner = func(ctx context.Context, path, arg string) (string, error) {
		if path != exe {
			t.Fatalf("runner path=%q want %q", path, exe)
		}
		return "fakeagent 3.4.5\n", nil
	}
	t.Cleanup(func() { agentVersionRunner = oldRunner })

	st := detectAgentCLI(context.Background(), agentCLI{
		ID: "fakeagent", Name: "Fake Agent", Bins: []string{"fakeagent"},
		Package: "@example/fakeagent", Manager: "npm", VersionArg: "--version",
	})
	if !st.Installed || st.Version != "3.4.5" || st.Method != "direct" {
		t.Fatalf("检测结果不符: %+v", st)
	}
	if !strings.Contains(st.UpdateCmd, "npm i -g @example/fakeagent@latest") {
		t.Fatalf("升级命令不符: %q", st.UpdateCmd)
	}

	// 未安装 → 只给安装命令
	st = detectAgentCLI(context.Background(), agentCLI{
		ID: "absent", Name: "Absent", Bins: []string{"definitely-not-installed-xyz"},
		Package: "absent-pkg", Manager: "npm",
	})
	if st.Installed || st.InstallCmd == "" {
		t.Fatalf("未安装结果不符: %+v", st)
	}
}

func TestAgentsCLIAPI(t *testing.T) {
	agentCLIs = agentCLICache{}
	g := New(Config{
		MaxAttempts:      1,
		FirstByteTimeout: time.Second,
		StateTTL:         time.Minute,
		MaxRecords:       10,
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
	})
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/agents/cli")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Agents map[string]struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Installed bool   `json:"installed"`
		} `json:"agents"`
		CheckedAt int64 `json:"checked_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Agents) != len(agentCLIRegistry) || body.CheckedAt == 0 {
		t.Fatalf("巡检条目数=%d checked=%d", len(body.Agents), body.CheckedAt)
	}
	for _, a := range agentCLIRegistry {
		if body.Agents[a.ID].ID != a.ID || body.Agents[a.ID].Name == "" {
			t.Fatalf("缺少条目 %s: %+v", a.ID, body.Agents[a.ID])
		}
	}

	// 未知 id 404；已知 id 返回升级指引（不执行）
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/agents/cli/nope", strings.NewReader("{}"))
	if resp2, err := http.DefaultClient.Do(req); err != nil || resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("未知 id 应 404: %v", err)
	} else {
		resp2.Body.Close()
	}
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/agents/cli/claude", strings.NewReader("{}"))
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp3.Body).Decode(&out)
	if out["error"] == nil && out["version"] == nil {
		t.Fatalf("升级指引响应不符: %v", out)
	}
}
