package workbuddy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestWorkBuddyListModelInfos(t *testing.T) {
	var gotPath, gotAuth, gotUser, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		gotUser, gotUA = r.Header.Get("X-User-Id"), r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"code":0,"data":{
		  "models":[
		    {"id":"default-model","name":"Auto","maxInputTokens":176000,"supportsImages":true,"supportsToolCall":true,"supportsReasoning":true,"credits":""},
		    {"id":"glm-5.3","name":"GLM-5.3","maxInputTokens":180000,"supportsImages":true,"supportsToolCall":true,"supportsReasoning":true,"credits":"x0.69"},
		    {"id":"hy3","name":"Hy3","maxInputTokens":192000,"supportsToolCall":true,"supportsReasoning":true,"credits":"x0.00"},
		    {"id":"kimi-k3","name":"Kimi K3","maxInputTokens":180000,"supportsToolCall":true,"credits":"x1.62"},
		    {"id":"hy-image-3.5","name":"HY Image","credits":null}
		  ],
		  "agents":[{"name":"cli","models":["default-model","glm-5.3","hy3","kimi-k3"]}],
		  "modelPromotions":[
		    {"id":"glm-half","enabled":true,"kind":"discount","modelIds":["glm-5.3"],"discount":{"factor":0.5}},
		    {"id":"kimi-expired","enabled":true,"kind":"discount","modelIds":["kimi-k3"],
		     "discount":{"factor":0.1},"schedule":{"validUntil":"2020-01-01T00:00:00+08:00"}}
		  ]
		}}`)
	}))
	defer srv.Close()

	p := newProvider(intlProfile)
	env := provider.NewAccountEnv("acc", "workbuddy-intl")
	env.Credentials = map[string]string{"token": "tk", "user_id": "uid", "base_url": srv.URL + "/v2"}
	env.HTTPClient = srv.Client()
	if !p.SupportsModelList(env) {
		t.Fatal("WorkBuddy 应支持模型目录")
	}
	infos, err := p.ListModelInfos(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v3/config" || gotAuth != "Bearer tk" || gotUser != "uid" || gotUA[:9] != "WorkBuddy" {
		t.Fatalf("path=%q auth=%q user=%q ua=%q", gotPath, gotAuth, gotUser, gotUA)
	}
	byID := map[string]provider.ModelInfo{}
	for _, info := range infos {
		byID[info.ID] = info
	}
	if len(infos) != 4 {
		t.Fatalf("infos = %+v", infos)
	}
	if _, ok := byID["hy-image-3.5"]; ok {
		t.Fatal("非 cli agent 的模型不应出现")
	}
	if auto := byID["default-model"]; auto.Rate != 0 || auto.Free {
		t.Fatalf("未标 credits 的模型不应当成免费: %+v", auto)
	}
	glm := byID["glm-5.3"]
	if glm.Rate != 0.345 || glm.Context != 180000 || !glm.Capabilities.Vision || glm.Name != "GLM-5.3" {
		t.Fatalf("glm = %+v", glm)
	}
	if hy := byID["hy3"]; !hy.Free || hy.Rate != 0 {
		t.Fatalf("hy3 应为免费: %+v", hy)
	}
	if kimi := byID["kimi-k3"]; kimi.Rate != 1.62 {
		t.Fatalf("过期促销不应生效: %+v", kimi)
	}
}

func TestParseCredits(t *testing.T) {
	cases := map[string]struct {
		rate  float64
		known bool
	}{
		"x0.34 credits": {0.34, true},
		"x0.00":         {0, true},
		"x3.31 credits": {3.31, true},
		"":              {0, false},
	}
	for in, want := range cases {
		rate, known := parseCredits(in)
		if rate != want.rate || known != want.known {
			t.Errorf("parseCredits(%q) = %v,%v want %v,%v", in, rate, known, want.rate, want.known)
		}
	}
}

func TestModelNotAllowedIsRetryable(t *testing.T) {
	body := `{"code":11158,"msg":"model [grok-4.7] is not allowed for user type [personal_free_user]"}`
	ae := mapUpstreamError(400, body, nil)
	if ae.Code != "bad_request" || !ae.Retryable {
		t.Fatalf("模型不开放应可换渠道: %+v", ae)
	}
}
