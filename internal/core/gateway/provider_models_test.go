package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func newModelsGateway(t *testing.T) (*Gateway, *httptest.Server) {
	t.Helper()
	registerFakeOfficial()
	g := New(Config{
		MaxAttempts:      1,
		FirstByteTimeout: time.Second,
		StateTTL:         time.Minute,
		MaxRecords:       10,
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
	})
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return g, srv
}

func listModels(t *testing.T, srv *httptest.Server) map[string]bool {
	t.Helper()
	resp, err := http.Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, m := range body.Data {
		out[m.ID] = true
	}
	return out
}

// 未接入任何账号的渠道，模型不出现在 /v1/models。
func TestModelsRequireConnectedProvider(t *testing.T) {
	g, srv := newModelsGateway(t)
	if got := listModels(t, srv); got["mock-native/echo-pro"] {
		t.Fatalf("没有账号时不应暴露模型: %v", got)
	}
	env := provider.NewAccountEnv("acc-models", "mock-native")
	env.HTTPClient = &http.Client{Timeout: time.Second}
	g.Pool().Add(env)
	if got := listModels(t, srv); !got["mock-native/echo-pro"] {
		t.Fatalf("接入账号后应暴露模型: %v", got)
	}
}

// 渠道级模型选择：清空 = 不暴露；勾选 = 只暴露勾选的；倍率出现在渠道视图。
func TestProviderModelSelectionAndRate(t *testing.T) {
	g, srv := newModelsGateway(t)
	env := provider.NewAccountEnv("acc-models", "mock-native")
	env.HTTPClient = &http.Client{Timeout: time.Second}
	g.Pool().Add(env)

	if err := g.saveProviderModelSelection("mock-native", []string{}, nil); err != nil {
		t.Fatalf("save empty: %v", err)
	}
	if got := listModels(t, srv); got["mock-native/echo-pro"] {
		t.Fatalf("清空选择后仍暴露模型: %v", got)
	}

	if err := g.saveProviderModelSelection("mock-native", []string{"mock-native/echo-pro"}, map[string]float64{"echo-pro": 2.5}); err != nil {
		t.Fatalf("save selection: %v", err)
	}
	if got := listModels(t, srv); !got["mock-native/echo-pro"] {
		t.Fatalf("勾选后应暴露模型: %v", got)
	}

	// 渠道视图回显 on 与 credit_rate
	resp, err := http.Get(srv.URL + "/api/providers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var view struct {
		Providers []struct {
			ID     string `json:"id"`
			Models []struct {
				ID         string  `json:"id"`
				On         bool    `json:"on"`
				CreditRate float64 `json:"credit_rate"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range view.Providers {
		if p.ID != "mock-native" {
			continue
		}
		for _, m := range p.Models {
			if m.ID == "echo-pro" {
				found = true
				if !m.On || m.CreditRate != 2.5 {
					t.Fatalf("渠道视图模型字段不符: %+v", m)
				}
			}
		}
	}
	if !found {
		t.Fatal("渠道视图缺少 echo-pro")
	}
}

// 账号白名单进一步收窄可用模型。
func TestAccountWhitelistNarrowsModels(t *testing.T) {
	g, srv := newModelsGateway(t)
	env := provider.NewAccountEnv("acc-models", "mock-native")
	env.HTTPClient = &http.Client{Timeout: time.Second}
	env.SetAllowedModels([]string{"other-model"})
	g.Pool().Add(env)
	if got := listModels(t, srv); got["mock-native/echo-pro"] {
		t.Fatalf("账号白名单不含该模型时不应暴露: %v", got)
	}
	env.SetAllowedModels([]string{"echo-pro"})
	if got := listModels(t, srv); !got["mock-native/echo-pro"] {
		t.Fatalf("账号白名单含该模型时应暴露: %v", got)
	}
}
