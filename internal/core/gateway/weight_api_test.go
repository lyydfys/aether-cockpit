package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestKeysWeightAction(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		UsagePath:        "-",
	}
	g := New(cfg)
	env := provider.NewAccountEnv("w-acc", "mock-native")
	env.Label = "weighted"
	g.pool.Add(env)
	g.saveAccountsNow()

	srv := httptest.NewServer(g)
	defer srv.Close()

	post := func(body string) int {
		resp, err := http.Post(srv.URL+"/api/keys/weight", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(`{"id":"mock-native","ref":"w-acc","weight":9}`); code != http.StatusOK {
		t.Fatalf("设置权重 = %d", code)
	}
	if env.Weight() != 9 {
		t.Fatalf("env.Weight = %d", env.Weight())
	}

	// providers 回显真实权重（前端据此显示权重选择器）。
	resp, err := http.Get(srv.URL + "/api/providers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var envelope struct {
		Providers []struct {
			ID      string `json:"id"`
			KeyList []struct {
				ID     string `json:"id"`
				Weight int    `json:"weight"`
			} `json:"keyList"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range envelope.Providers {
		if p.ID != "mock-native" {
			continue
		}
		for _, k := range p.KeyList {
			if k.ID == "w-acc" {
				found = true
				if k.Weight != 9 {
					t.Fatalf("keyList weight = %d", k.Weight)
				}
			}
		}
	}
	if !found {
		t.Fatal("providers 未列出该账号")
	}

	// 立刻落盘，重启后仍在。
	raw, err := os.ReadFile(filepath.Join(dir, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"weight": 9`) {
		t.Fatalf("权重未落盘: %s", raw)
	}
	g2 := New(cfg)
	env2, ok := g2.pool.Get("w-acc")
	if !ok || env2.Weight() != 9 {
		t.Fatalf("重启后权重丢失: %v %d", ok, env2.Weight())
	}

	// 越界收敛到 1..1000。
	if code := post(`{"id":"mock-native","ref":"w-acc","weight":5000}`); code != http.StatusOK {
		t.Fatalf("越界权重 = %d", code)
	}
	if env.Weight() != 1000 {
		t.Fatalf("越界未收敛: %d", env.Weight())
	}
	// 未知账号 404。
	if code := post(`{"id":"mock-native","ref":"nope","weight":3}`); code != http.StatusNotFound {
		t.Fatalf("未知账号 = %d", code)
	}
}
