package gateway

import (
	"encoding/json"
	"strings"
	"testing"
)

// traces 里必须带 token 与费用：否则接 Langfuse/Tempo 只能看到"调了一次"。
func TestOTLPSpanCarriesUsageAndCost(t *testing.T) {
	span := buildSpan(RequestLog{
		Time: 1700000000, Protocol: "openai", Model: "gpt-x", ProviderID: "qoder",
		AccountID: "acc1", Status: "ok", DurationMS: 1200, FirstByteMS: 300,
		InputTokens: 100, OutputTokens: 50, CacheRead: 20, CacheWrite: 5,
		ReasoningTokens: 8, Cost: 0.0123, Priced: true, PricingModel: "gpt-x",
	})
	got := map[string]string{}
	for _, a := range span.Attributes {
		if a.Value.StringValue != "" {
			got[a.Key] = a.Value.StringValue
		}
		if a.Value.IntValue != "" {
			got[a.Key] = a.Value.IntValue
		}
		if a.Value.DoubleValue != nil {
			got[a.Key] = "double"
		}
	}
	for _, key := range []string{
		"gen_ai.usage.input_tokens", "gen_ai.usage.output_tokens",
		"gen_ai.usage.cache_read_tokens", "gen_ai.usage.cache_write_tokens",
		"gen_ai.usage.reasoning_tokens", "gen_ai.usage.total_tokens",
		"cost_usd", "gen_ai.request.model", "pricing_model",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("span 缺少属性 %q（现有：%v）", key, keysOf(got))
		}
	}
	if got["gen_ai.usage.total_tokens"] != "175" {
		t.Errorf("total_tokens = %s, want 175", got["gen_ai.usage.total_tokens"])
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// 指标导出：token 按类型分点，成本单列，且空批次不发。
func TestOTLPMetricsAggregatesByModel(t *testing.T) {
	req := buildExportMetricsRequest([]RequestLog{
		{Time: 1700000000, Model: "m1", ProviderID: "p1", InputTokens: 10, OutputTokens: 5, Cost: 0.01},
		{Time: 1700000000, Model: "m1", ProviderID: "p1", InputTokens: 20, OutputTokens: 7, Cost: 0.02},
	})
	if len(req.ResourceMetrics) != 1 {
		t.Fatalf("应有一个 resourceMetrics：%+v", req.ResourceMetrics)
	}
	metrics := req.ResourceMetrics[0].ScopeMetrics[0].Metrics
	var tokenMetric, costMetric *otlpMetric
	for i := range metrics {
		switch metrics[i].Name {
		case "gen_ai.client.token.usage":
			tokenMetric = &metrics[i]
		case "aether.request.cost":
			costMetric = &metrics[i]
		}
	}
	if tokenMetric == nil || tokenMetric.Sum == nil {
		t.Fatal("缺少 token 指标")
	}
	// 同模型的两次请求应聚合成一个点（input = 10+20）。
	var inputSum int64
	for _, p := range tokenMetric.Sum.DataPoints {
		for _, a := range p.Attributes {
			if a.Key == "gen_ai.token.type" && a.Value.StringValue == "input" {
				var n int64
				for _, c := range p.AsInt {
					n = n*10 + int64(c-'0')
				}
				inputSum += n
			}
		}
	}
	if inputSum != 30 {
		t.Errorf("input token 聚合 = %d, want 30", inputSum)
	}
	if costMetric == nil || len(costMetric.Sum.DataPoints) != 1 {
		t.Fatalf("成本应聚合成 1 个点：%+v", costMetric)
	}
	if dp := costMetric.Sum.DataPoints[0]; dp.AsDouble == nil || *dp.AsDouble < 0.0299 || *dp.AsDouble > 0.0301 {
		t.Errorf("成本聚合不对：%+v", dp.AsDouble)
	}

	// 空批次不发（避免给后端刷空请求）。
	if empty := buildExportMetricsRequest(nil); len(empty.ResourceMetrics) != 0 {
		t.Fatal("空批次不该产出指标")
	}
}

// metrics URL 由 traces URL 派生，不能丢掉 endpoint 前缀。
func TestOTLPMetricsURL(t *testing.T) {
	got, err := otlpMetricsURL("http://127.0.0.1:4318")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "/v1/metrics") {
		t.Fatalf("metrics URL = %q", got)
	}
	if !strings.Contains(got, "127.0.0.1:4318") {
		t.Fatalf("丢了 endpoint 前缀：%q", got)
	}
}

// 序列化后必须是合法 JSON（OTLP/JSON 后端直接解析）。
func TestOTLPMetricsSerializes(t *testing.T) {
	req := buildExportMetricsRequest([]RequestLog{{Time: 1700000000, Model: "m", ProviderID: "p", InputTokens: 1, Cost: 0.1}})
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "resourceMetrics") {
		t.Fatalf("序列化结果异常：%s", raw)
	}
}
