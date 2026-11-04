package kiro

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func kiroQuotaEnv(srv *httptest.Server) *provider.AccountEnv {
	env := provider.NewAccountEnv("kiro-quota", "kiro")
	env.Credentials = map[string]string{
		"access_token": "tk",
		"profile_arn":  "arn:aws:codewhisperer:us-east-1:1:profile/x",
		"base_url":     srv.URL,
	}
	env.HTTPClient = srv.Client()
	return env
}

// 额度接口必须带 resourceType=AGENTIC_REQUEST：不带会拿到另一套计量，
// 额度显示成 0 或比例不对。
func TestKiroQuotaSendsResourceType(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"usageBreakdownList":[{"resourceType":"AGENTIC_REQUEST",
			"usageLimitWithPrecision":100,"currentUsageWithPrecision":40}]}`))
	}))
	defer srv.Close()

	if _, err := (&Provider{}).QueryQuota(context.Background(), kiroQuotaEnv(srv)); err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if !strings.Contains(gotQuery, "resourceType=AGENTIC_REQUEST") {
		t.Fatalf("请求缺少 resourceType 参数：%s", gotQuery)
	}
	if !strings.Contains(gotQuery, "origin=AI_EDITOR") {
		t.Fatalf("请求缺少 origin 参数：%s", gotQuery)
	}
}

// 免费试用账号：主额度为 0，额度在 freeTrialInfo 里。读不到就会显示"没额度"。
func TestKiroQuotaFreeTrial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"usageBreakdownList":[{
			"resourceType":"AGENTIC_REQUEST",
			"usageLimit":0,"currentUsage":0,
			"freeTrialInfo":{
				"usageLimitWithPrecision":200,"currentUsageWithPrecision":50,
				"freeTrialExpiry":1800000000000
			}}]}`))
	}))
	defer srv.Close()

	q, err := (&Provider{}).QueryQuota(context.Background(), kiroQuotaEnv(srv))
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	var trial *provider.QuotaWindow
	for i := range q.Windows {
		if q.Windows[i].Name == "免费试用" {
			trial = &q.Windows[i]
		}
	}
	if trial == nil {
		t.Fatalf("应识别出免费试用窗口：%+v", q.Windows)
	}
	if trial.Limit != 200 || trial.Amount != 50 || trial.Used != 25 {
		t.Fatalf("试用窗口换算错误：%+v", *trial)
	}
	if trial.ResetsAt != 1800000000 {
		t.Fatalf("毫秒过期时间应归一到秒，得到 %d", trial.ResetsAt)
	}
	if q.Balance != 150 {
		t.Fatalf("剩余应把试用额度算进去（150），得到 %v", q.Balance)
	}
}

// 新版把额度拆成 usageBreakdowns.{plan,covered,bonus}，老代码完全读不到。
func TestKiroQuotaNewBreakdownShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"usageBreakdowns":{
			"plan":{"usageLimitWithPrecision":500,"currentUsageWithPrecision":100},
			"bonus":{"usageLimitWithPrecision":50,"currentUsageWithPrecision":0}
		}}`))
	}))
	defer srv.Close()

	q, err := (&Provider{}).QueryQuota(context.Background(), kiroQuotaEnv(srv))
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if len(q.Windows) != 2 {
		t.Fatalf("应识别 plan 与 bonus 两个窗口：%+v", q.Windows)
	}
	if q.Balance != 450 {
		t.Fatalf("剩余应为 (500-100)+50 = 450，得到 %v", q.Balance)
	}
}

// 只有扁平 credits 汇总时也要能读。
func TestKiroQuotaCreditsFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"credits":{"total":300,"used":120}}`))
	}))
	defer srv.Close()

	q, err := (&Provider{}).QueryQuota(context.Background(), kiroQuotaEnv(srv))
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Balance != 180 {
		t.Fatalf("剩余应为 180，得到 %v", q.Balance)
	}
}

// 403 + BANNED 是终态：不能当成普通失败反复重试。
func TestKiroQuotaBannedIsTerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"BANNED: account suspended"}`))
	}))
	defer srv.Close()

	_, err := (&Provider{}).QueryQuota(context.Background(), kiroQuotaEnv(srv))
	if err == nil {
		t.Fatal("应报错")
	}
	if !strings.Contains(err.Error(), "封禁") {
		t.Fatalf("应判定为封禁终态，得到 %v", err)
	}
}

// 域名按区域拼：普通分区 q.<region>，GovCloud 用 q-fips 前缀。
func TestKiroQuotaHost(t *testing.T) {
	if got := kiroQuotaHost("us-east-1"); got != "https://q.us-east-1.amazonaws.com" {
		t.Fatalf("host = %s", got)
	}
	if got := kiroQuotaHost("us-gov-west-1"); got != "https://q-fips.us-gov-west-1.amazonaws.com" {
		t.Fatalf("gov host = %s", got)
	}
}
