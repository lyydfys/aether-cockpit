package openaicompat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func TestAzureChatStream(t *testing.T) {
	var gotPath, gotQuery, gotAPIKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("api-version")
		gotAPIKey = r.Header.Get("api-key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"azure-ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := &Provider{cfg: azureConfig}
	env := provider.NewAccountEnv("acc", "azure")
	env.Credentials = map[string]string{"base_url": srv.URL, "api_key": "az-key"}
	env.SetHTTPClient(srv.Client())
	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model:              "azure/dep1",
		DownstreamProtocol: ir.ProtocolOpenAIChat,
		Items:              []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
		Stream:             true,
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text strings.Builder
	for ev := range ch {
		if ev.Type == ir.EventError {
			t.Fatalf("stream error: %v", ev.Err)
		}
		if ev.Type == ir.EventTextDelta {
			text.WriteString(ev.TextDelta)
		}
	}
	if gotPath != "/openai/deployments/dep1/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "2024-10-21" {
		t.Errorf("api-version = %q", gotQuery)
	}
	if gotAPIKey != "az-key" || gotAuth != "" {
		t.Errorf("api-key=%q authorization=%q", gotAPIKey, gotAuth)
	}
	if text.String() != "azure-ok" {
		t.Errorf("text = %q", text.String())
	}
}

func TestAzureManifestNeedsEndpoint(t *testing.T) {
	m := (&Provider{cfg: azureConfig}).Manifest()
	if m.ID != "azure" || m.Family != "azure" {
		t.Fatalf("manifest = %q/%q", m.ID, m.Family)
	}
	if !m.AuthFormSchema[0].Required {
		t.Errorf("azure must require a resource endpoint")
	}
	fields := map[string]provider.FormFieldSchema{}
	for _, f := range m.AuthFormSchema {
		fields[f.Key] = f
	}
	if fields["api_key"].Required {
		t.Error("azure api_key must be optional when Entra is configured")
	}
	for _, k := range []string{"azure_tenant_id", "azure_client_id", "azure_client_secret", "azure_scope", "azure_token_url"} {
		if _, ok := fields[k]; !ok {
			t.Errorf("missing Entra field %s", k)
		}
	}
}

func TestAzureEntraClientCredentials(t *testing.T) {
	var tokenCalls int
	var gotForm string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		_ = r.ParseForm()
		gotForm = r.PostForm.Encode()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"entra-token-1","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer tokenSrv.Close()

	var gotAuth, gotAPIKey string
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"entra-ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer apiSrv.Close()

	p := &Provider{cfg: azureConfig}
	env := provider.NewAccountEnv("acc-entra", "azure")
	env.Credentials = map[string]string{
		"base_url":            apiSrv.URL,
		"access_token":        "stale-token",
		"expires_at":          "2020-01-01T00:00:00Z",
		"azure_tenant_id":     "tenant-1",
		"azure_client_id":     "client-1",
		"azure_client_secret": "secret-1",
		"azure_token_url":     tokenSrv.URL,
	}
	env.SetHTTPClient(apiSrv.Client())

	req := &ir.UnifiedChatRequest{
		Model: "azure/dep1", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	for i := 0; i < 2; i++ {
		ch, err := p.ChatStream(context.Background(), env, req)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		var text strings.Builder
		for ev := range ch {
			if ev.Type == ir.EventError {
				t.Fatalf("stream error: %v", ev.Err)
			}
			if ev.Type == ir.EventTextDelta {
				text.WriteString(ev.TextDelta)
			}
		}
		if text.String() != "entra-ok" {
			t.Fatalf("call %d text = %q", i, text.String())
		}
	}
	if tokenCalls != 1 {
		t.Fatalf("token endpoint calls = %d, want cached single call", tokenCalls)
	}
	if !strings.Contains(gotForm, "grant_type=client_credentials") ||
		!strings.Contains(gotForm, "client_id=client-1") ||
		!strings.Contains(gotForm, "scope=https%3A%2F%2Fcognitiveservices.azure.com%2F.default") {
		t.Fatalf("token form = %q", gotForm)
	}
	if gotAuth != "Bearer entra-token-1" || gotAPIKey != "" {
		t.Fatalf("authorization=%q api-key=%q", gotAuth, gotAPIKey)
	}
	if env.GetCredential("access_token") != "entra-token-1" || env.GetCredential("expires_at") == "" {
		t.Fatalf("cached credentials = %v", env.Credentials)
	}
}

func TestAzureEntraMissingCredentials(t *testing.T) {
	p := &Provider{cfg: azureConfig}
	env := provider.NewAccountEnv("acc-entra-empty", "azure")
	env.Credentials = map[string]string{"base_url": "https://example.openai.azure.com"}
	env.SetHTTPClient(http.DefaultClient)
	_, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "azure/dep1", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "Azure 需要 api-key") {
		t.Fatalf("error = %v", err)
	}
}
