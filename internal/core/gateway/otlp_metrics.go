package gateway

import (
	"encoding/json"
	"log"
	"strconv"
	"time"
)

// OTLP 指标导出：把同一批请求聚合成计数器一起发出去。
//
// 为什么需要：只发 traces 时，后端能看到"某次请求花了多少"，但画不出
// "今天的 token 曲线 / 每个渠道的成本趋势"——那需要 metrics。字段本来就
// 都在 RequestLog 里，缺的只是把它编码成 OTLP metrics。

// otlpMetricsURL 把 endpoint 规整成 <endpoint>/v1/metrics。
func otlpMetricsURL(endpoint string) (string, error) {
	base, err := otlpTracesURL(endpoint)
	if err != nil {
		return "", err
	}
	// otlpTracesURL 已经拼好 /v1/traces，这里换掉最后一段。
	const suffix = "v1/traces"
	if len(base) >= len(suffix) && base[len(base)-len(suffix):] == suffix {
		return base[:len(base)-len(suffix)] + "v1/metrics", nil
	}
	return base, nil
}

type otlpSumPoint struct {
	Attributes        []otlpKeyValue `json:"attributes,omitempty"`
	StartTimeUnixNano string         `json:"startTimeUnixNano"`
	TimeUnixNano      string         `json:"timeUnixNano"`
	AsInt             string         `json:"asInt,omitempty"`
	AsDouble          *float64       `json:"asDouble,omitempty"`
}

type otlpSum struct {
	DataPoints             []otlpSumPoint `json:"dataPoints"`
	AggregationTemporality int            `json:"aggregationTemporality"` // 2 = CUMULATIVE
	IsMonotonic            bool           `json:"isMonotonic"`
}

type otlpMetric struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Unit        string   `json:"unit,omitempty"`
	Sum         *otlpSum `json:"sum,omitempty"`
}

type otlpScopeMetrics struct {
	Scope   *otlpScope   `json:"scope,omitempty"`
	Metrics []otlpMetric `json:"metrics"`
}

type otlpResourceMetrics struct {
	Resource     *otlpResource      `json:"resource"`
	ScopeMetrics []otlpScopeMetrics `json:"scopeMetrics"`
}

type otlpExportMetricsRequest struct {
	ResourceMetrics []otlpResourceMetrics `json:"resourceMetrics"`
}

// buildExportMetricsRequest 把一批记录聚合成 4 个计数器：
// 输入/输出/缓存 token（按 gen_ai.token.type 分点）与成本（美元）。
//
// 聚合维度只保留 model + provider：再细下去点数会爆炸，而这些足够画趋势。
func buildExportMetricsRequest(batch []RequestLog) otlpExportMetricsRequest {
	type key struct{ model, provider string }
	type agg struct {
		input, output, cacheRead, cacheWrite, total int64
		cost                                        float64
	}
	sums := map[key]*agg{}
	order := make([]key, 0, len(batch))
	for _, e := range batch {
		k := key{model: e.Model, provider: e.ProviderID}
		a := sums[k]
		if a == nil {
			a = &agg{}
			sums[k] = a
			order = append(order, k)
		}
		a.input += e.InputTokens
		a.output += e.OutputTokens
		a.cacheRead += e.CacheRead
		a.cacheWrite += e.CacheWrite
		a.cost += e.Cost
	}
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	start := now
	if len(batch) > 0 {
		start = strconv.FormatInt(batch[0].Time*int64(time.Second), 10)
	}

	tokenPoints := make([]otlpSumPoint, 0, len(order)*4)
	costPoints := make([]otlpSumPoint, 0, len(order))
	for _, k := range order {
		a := sums[k]
		base := []otlpKeyValue{}
		if k.model != "" {
			base = append(base, otlpStrAttr("gen_ai.request.model", k.model))
		}
		if k.provider != "" {
			base = append(base, otlpStrAttr("provider_id", k.provider))
		}
		add := func(kind string, n int64) {
			if n <= 0 {
				return
			}
			attrs := append(append([]otlpKeyValue{}, base...),
				otlpStrAttr("gen_ai.token.type", kind))
			tokenPoints = append(tokenPoints, otlpSumPoint{
				Attributes: attrs, StartTimeUnixNano: start, TimeUnixNano: now,
				AsInt: strconv.FormatInt(n, 10),
			})
		}
		add("input", a.input)
		add("output", a.output)
		add("cache_read", a.cacheRead)
		add("cache_write", a.cacheWrite)
		if a.cost > 0 {
			c := a.cost
			costPoints = append(costPoints, otlpSumPoint{
				Attributes: base, StartTimeUnixNano: start, TimeUnixNano: now,
				AsDouble: &c,
			})
		}
	}

	metrics := make([]otlpMetric, 0, 2)
	if len(tokenPoints) > 0 {
		metrics = append(metrics, otlpMetric{
			Name: "gen_ai.client.token.usage", Description: "Token usage by type",
			Unit: "{token}",
			Sum:  &otlpSum{DataPoints: tokenPoints, AggregationTemporality: 2, IsMonotonic: true},
		})
	}
	if len(costPoints) > 0 {
		metrics = append(metrics, otlpMetric{
			Name: "aether.request.cost", Description: "Estimated request cost",
			Unit: "usd",
			Sum:  &otlpSum{DataPoints: costPoints, AggregationTemporality: 2, IsMonotonic: true},
		})
	}
	if len(metrics) == 0 {
		return otlpExportMetricsRequest{}
	}
	return otlpExportMetricsRequest{
		ResourceMetrics: []otlpResourceMetrics{{
			Resource: &otlpResource{
				Attributes: []otlpKeyValue{otlpStrAttr("service.name", otlpServiceName)},
			},
			ScopeMetrics: []otlpScopeMetrics{{
				Scope:   &otlpScope{Name: otlpScopeName},
				Metrics: metrics,
			}},
		}},
	}
}

// exportMetrics 发送一批指标；失败只记日志（观测数据可以丢）。
func (gen *otlpGen) exportMetrics(batch []RequestLog) {
	req := buildExportMetricsRequest(batch)
	if len(req.ResourceMetrics) == 0 {
		return
	}
	u, err := otlpMetricsURL(gen.cfg.Endpoint)
	if err != nil {
		return
	}
	body, err := json.Marshal(req)
	if err != nil {
		return
	}
	if code, _, _, perr := gen.post(u, body); perr != nil {
		log.Printf("[WARN] OTLP 指标导出失败: %v", perr)
	} else if code < 200 || code > 299 {
		log.Printf("[WARN] OTLP 指标导出被拒: HTTP %d", code)
	}
}
