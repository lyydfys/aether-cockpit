package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func newWakeGateway(t *testing.T) (*Gateway, *httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	g := New(Config{
		MaxAttempts:      2,
		FirstByteTimeout: 5 * time.Second,
		StateTTL:         time.Minute,
		MaxRecords:       100,
		SettingsPath:     filepath.Join(dir, "settings.json"),
	})
	env := provider.NewAccountEnv("mock-acct", "mock-native")
	env.Label = "mock 账号"
	env.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	g.Pool().Add(env)
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return g, srv, dir
}

func wakeDo(t *testing.T, srv *httptest.Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req, err := http.NewRequest(method, srv.URL+path, &buf)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func numOf(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

func wakeTaskFrom(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	task, ok := out["task"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 task: %v", out)
	}
	return task
}

func TestWakeTaskLifecycle(t *testing.T) {
	_, srv, dir := newWakeGateway(t)

	code, out := wakeDo(t, srv, http.MethodPost, "/api/wake", map[string]any{
		"account_id": "mock-acct", "every_minutes": 60,
	})
	if code != 200 {
		t.Fatalf("创建失败: %d %v", code, out)
	}
	task := wakeTaskFrom(t, out)
	id, _ := task["id"].(string)
	if id == "" || task["provider_id"] != "mock-native" || task["enabled"] != true {
		t.Fatalf("任务字段不符: %v", task)
	}
	created := int64(numOf(task, "created_at"))
	if nr := int64(numOf(task, "next_run")); nr < created+3600-2 || nr > created+3600+2 {
		t.Fatalf("首轮 next_run 应为创建时间+间隔: created=%d next=%d", created, nr)
	}

	// 同一账号再提交视为更新，不新增
	code, out = wakeDo(t, srv, http.MethodPost, "/api/wake", map[string]any{
		"account_id": "mock-acct", "every_minutes": 30, "enabled": false,
	})
	if code != 200 {
		t.Fatalf("更新失败: %d %v", code, out)
	}
	task = wakeTaskFrom(t, out)
	if task["id"] != id || numOf(task, "every_minutes") != 30 || task["enabled"] != false {
		t.Fatalf("更新结果不符: %v", task)
	}

	_, out = wakeDo(t, srv, http.MethodGet, "/api/wake", nil)
	if list, _ := out["tasks"].([]any); len(list) != 1 {
		t.Fatalf("应只有一条任务: %v", out)
	}

	// 落盘
	b, err := os.ReadFile(filepath.Join(dir, "wake.json"))
	if err != nil || !bytes.Contains(b, []byte("mock-acct")) {
		t.Fatalf("wake.json 未落盘: err=%v body=%s", err, b)
	}

	// 重新加载同一设置目录，任务仍在
	g2 := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), MaxRecords: 10, StateTTL: time.Minute, FirstByteTimeout: time.Second})
	g2.wakeMu.Lock()
	loaded := len(g2.wakeTasks)
	g2.wakeMu.Unlock()
	if loaded != 1 {
		t.Fatalf("重载后任务数 %d", loaded)
	}

	code, out = wakeDo(t, srv, http.MethodDelete, "/api/wake/"+id, nil)
	if code != 200 {
		t.Fatalf("删除失败: %d %v", code, out)
	}
	_, out = wakeDo(t, srv, http.MethodGet, "/api/wake", nil)
	if list, _ := out["tasks"].([]any); len(list) != 0 {
		t.Fatalf("删除后仍有任务: %v", out)
	}
}

func TestWakeRunAccountOneShot(t *testing.T) {
	_, srv, _ := newWakeGateway(t)

	code, out := wakeDo(t, srv, http.MethodPost, "/api/wake/run", map[string]any{"account_id": "mock-acct"})
	if code != 200 {
		t.Fatalf("一次性唤醒失败: %d %v", code, out)
	}
	if out["ok"] != true || out["model"] != "echo-pro" {
		t.Fatalf("唤醒结果不符: %v", out)
	}

	code, out = wakeDo(t, srv, http.MethodPost, "/api/wake/run", map[string]any{"account_id": "nope"})
	if code != 200 || out["ok"] != false {
		t.Fatalf("未知账号应返回 ok=false: %d %v", code, out)
	}
	if msg, _ := out["result"].(string); msg == "" {
		t.Fatalf("未知账号缺少原因: %v", out)
	}
}

func TestWakeRunTaskRecordsResult(t *testing.T) {
	_, srv, _ := newWakeGateway(t)
	code, out := wakeDo(t, srv, http.MethodPost, "/api/wake", map[string]any{"account_id": "mock-acct", "every_minutes": 60})
	if code != 200 {
		t.Fatalf("创建失败: %v", out)
	}
	id := wakeTaskFrom(t, out)["id"].(string)

	code, out = wakeDo(t, srv, http.MethodPost, "/api/wake/"+id+"/run", nil)
	if code != http.StatusAccepted {
		t.Fatalf("手动唤醒应 202: %d %v", code, out)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, list := wakeDo(t, srv, http.MethodGet, "/api/wake", nil)
		tasks, _ := list["tasks"].([]any)
		if len(tasks) == 1 {
			task, _ := tasks[0].(map[string]any)
			if numOf(task, "last_run") > 0 {
				if task["last_ok"] != true {
					t.Fatalf("执行失败: %v", task)
				}
				if task["last_model"] != "echo-pro" {
					t.Fatalf("last_model=%v", task["last_model"])
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("等待唤醒执行超时")
}

func TestWakeValidation(t *testing.T) {
	_, srv, _ := newWakeGateway(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"过短间隔", map[string]any{"account_id": "mock-acct", "every_minutes": 5}},
		{"过长间隔", map[string]any{"account_id": "mock-acct", "every_minutes": 10081}},
		{"缺账号", map[string]any{"every_minutes": 60}},
		{"未知账号", map[string]any{"account_id": "ghost", "every_minutes": 60}},
	}
	for _, tc := range cases {
		if code, _ := wakeDo(t, srv, http.MethodPost, "/api/wake", tc.body); code != http.StatusBadRequest {
			t.Fatalf("%s: code=%d", tc.name, code)
		}
	}
	if code, _ := wakeDo(t, srv, http.MethodPost, "/api/wake/ghost-id", map[string]any{}); code != http.StatusNotFound {
		t.Fatalf("未知 ID 更新应 404: %d", code)
	}
}

func TestWakeDueRules(t *testing.T) {
	g := &Gateway{}
	now := time.Now()
	g.wakeTasks = []*wakeTask{
		{ID: "due", Enabled: true, CreatedAt: now.Add(-2 * time.Hour).Unix(), EveryMinutes: 60},
		{ID: "fresh", Enabled: true, CreatedAt: now.Unix(), EveryMinutes: 60},
		{ID: "off", Enabled: false, CreatedAt: now.Add(-2 * time.Hour).Unix(), EveryMinutes: 60},
		{ID: "recent", Enabled: true, CreatedAt: now.Add(-3 * time.Hour).Unix(), LastRun: now.Add(-30 * time.Minute).Unix(), EveryMinutes: 60},
	}
	g.wakeRunning = map[string]bool{"due": false}
	due := g.dueWakeTasks(now)
	if len(due) != 1 || due[0].ID != "due" {
		t.Fatalf("到期集合不符: %+v", due)
	}
	// 正在运行的任务不重复入队
	g.wakeRunning["due"] = true
	if due := g.dueWakeTasks(now); len(due) != 0 {
		t.Fatalf("运行中的任务不应再次入队: %+v", due)
	}
}
