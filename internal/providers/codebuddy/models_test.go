package codebuddy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

// CodeBuddy 的模型目录在 /v3/config，不是 OpenAI 的 /models（站点上 /models 是 404）。
func TestListModelInfosV3Config(t *testing.T) {
	var gotPath, gotAuth, gotUID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotUID = r.Header.Get("X-User-Id")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"models":[
			{"id":"glm-5.2","name":"GLM 5.2","maxInputTokens":200000,"supportsToolCall":true,"supportsReasoning":true},
			{"id":"deepseek-v4-pro","name":"DeepSeek V4 Pro","maxInputTokens":1000000,"supportsToolCall":true}
		]}}`))
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("cb-models-test", "codebuddy-intl")
	env.Credentials = map[string]string{"token": "tk", "uid": "u1", "base_url": srv.URL}
	env.HTTPClient = srv.Client()

	p := &Provider{profile: intlProfile}
	infos, err := p.ListModelInfos(context.Background(), env)
	if err != nil {
		t.Fatalf("ListModelInfos: %v", err)
	}
	if gotPath != "/v3/config" {
		t.Fatalf("路径 = %q, want /v3/config", gotPath)
	}
	if gotAuth != "Bearer tk" || gotUID != "u1" {
		t.Fatalf("鉴权头 = %q uid=%q", gotAuth, gotUID)
	}
	if len(infos) != 2 || infos[0].ID != "glm-5.2" || infos[0].Context != 200000 ||
		!infos[0].Capabilities.ToolCall || !infos[0].Capabilities.Reasoning ||
		infos[1].ID != "deepseek-v4-pro" {
		t.Fatalf("模型目录 = %+v", infos)
	}
}

// 账号 base_url 带 /v2 时也要落到站点根的 /v3/config。
func TestListModelInfosStripsV2(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"code":0,"data":{"models":[{"id":"m1","supportsToolCall":true}]}}`))
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("cb-models-v2", "codebuddy-cn")
	env.Credentials = map[string]string{"token": "tk", "base_url": srv.URL + "/v2"}
	env.HTTPClient = srv.Client()

	if _, err := (&Provider{profile: cnProfile}).ListModelInfos(context.Background(), env); err != nil {
		t.Fatalf("ListModelInfos: %v", err)
	}
	if gotPath != "/v3/config" {
		t.Fatalf("路径 = %q, want /v3/config", gotPath)
	}
}

func TestSupportsModelListNeedsToken(t *testing.T) {
	p := &Provider{profile: cnProfile}
	empty := provider.NewAccountEnv("cb-no-token", "codebuddy-cn")
	if p.SupportsModelList(empty) {
		t.Fatal("无 token 时不应声明可拉取模型目录")
	}
	withToken := provider.NewAccountEnv("cb-token", "codebuddy-cn")
	withToken.Credentials = map[string]string{"access_token": "tk"}
	if !p.SupportsModelList(withToken) {
		t.Fatal("有 token 时应支持 /v3/config")
	}
}
