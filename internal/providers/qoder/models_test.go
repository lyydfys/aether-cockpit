package qoder

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func TestListModelInfosFromCatalog(t *testing.T) {
	var gotPath, gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotAccept = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"chat":[
		  {"key":"auto","display_name":"Auto","enable":true,"is_vl":true,"is_reasoning":true,"price_factor":0.5,"max_input_tokens":200000},
		  {"key":"dfmodel","display_name":"DeepSeek-Flash","enable":true,"is_vl":true,"is_reasoning":false,"price_factor":0.1,"max_input_tokens":180000,
		   "thinking_config":{"enabled":{"efforts":{"high":{},"max":{"is_default":true}}}}},
		  {"key":"qfmodel","display_name":"Qwen3.8 Flash","enable":true,"is_free":true,"price_factor":0,"max_input_tokens":180000},
		  {"key":"disabled","display_name":"Off","enable":false}
		]}`)
	}))
	defer srv.Close()

	p := newProvider(cnProfile)
	env := provider.NewAccountEnv("acc", "qoder-cn")
	env.Credentials = map[string]string{
		"token": "tk", "user_id": "uid", "machine_id": "mid",
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
		"base_url":   srv.URL,
	}
	env.HTTPClient = srv.Client()
	if !p.SupportsModelList(env) {
		t.Fatal("Qoder 应支持模型目录")
	}
	infos, err := p.ListModelInfos(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/algo/api/v2/model/list" || gotAccept != "application/json" || !strings.HasPrefix(gotAuth, "Bearer COSY.") {
		t.Fatalf("path=%q accept=%q auth_prefix=%q", gotPath, gotAccept, gotAuth[:min(16, len(gotAuth))])
	}
	if len(infos) != 3 {
		t.Fatalf("infos = %+v", infos)
	}
	byID := map[string]provider.ModelInfo{}
	for _, info := range infos {
		byID[info.ID] = info
	}
	ds, ok := byID["DeepSeek-Flash"]
	if !ok || ds.UpstreamModel != "dfmodel" || ds.Context != 180000 || !ds.Capabilities.Vision || ds.Capabilities.Reasoning {
		t.Fatalf("deepseek = %+v", ds)
	}
	if ds.Rate != 0.1 || len(ds.Efforts) != 2 || ds.Efforts[0] != "high" || ds.Efforts[1] != "max" {
		t.Fatalf("deepseek 元数据 = %+v", ds)
	}
	qf, ok := byID["Qwen3.8-Flash"]
	if !ok || qf.UpstreamModel != "qfmodel" || !qf.Free || qf.Rate != 0 {
		t.Fatalf("qwen free = %+v", qf)
	}
	if _, ok := byID["Off"]; ok {
		t.Fatal("disabled 模型不应出现")
	}
}

func TestLegacyQoderModelKey(t *testing.T) {
	cases := map[string]string{
		// V4 Flash 与 V4.1 Flash 是两个模型：旧名字不再被改写成 dfmodel。
		"DeepSeek-V4-Flash": "DeepSeek-V4-Flash", "deepseek-v4.1-pro": "dmodel",
		"GLM-5.2": "gmodel", "Kimi-K2": "kmodel", "MiniMax-M3": "mmodel",
		"Qwen3.8-Max": "qmodel_38max", "Efficient": "auto",
		"unknow-model": "unknow-model",
	}
	for in, want := range cases {
		if got := legacyModelKey(in); got != want {
			t.Errorf("legacyModelKey(%q) = %q, want %q", in, got, want)
		}
	}
}
