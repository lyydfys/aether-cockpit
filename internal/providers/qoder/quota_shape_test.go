package qoder

import (
	"encoding/json"
	"testing"
)

// v2（camelCase）响应必须能解出来。
// 历史 bug：结构体只写了 snake_case 的 json tag，解 camelCase 时静默得到零值，
// 于是 v2 明明有 1800/1400，却判定"没有可读额度"直接返回。
func TestQoderQuotaStatusV2CamelCase(t *testing.T) {
	// 实测样本（qoder-cn，2026-11-07）
	raw := `{
      "userId":"019f8363-b521-71b4-9300-a29acc4b0b7d",
      "userType":"personal_standard",
      "usageType":"credits",
      "totalUsagePercentage":0.78,
      "isQuotaExceeded":false,
      "expiresAt":253402214400000,
      "userQuota":{"total":0.0,"used":0.0,"remaining":0.0,"percentage":0.0,"unit":"credits"},
      "addOnQuota":{"total":1800.0,"used":1400.0,"remaining":400.0,"percentage":0.78,"unit":"credits"}
    }`
	out := mustParse(t, raw)
	user, addOn, ok := out.pickBuckets()
	if !ok {
		t.Fatal("v2 字段应能解出额度")
	}
	if user.Total != 0 || addOn.Total != 1800 || addOn.Used != 1400 || addOn.Remaining != 400 {
		t.Fatalf("v2 解出来的桶不对：user=%+v addOn=%+v", user, addOn)
	}
	if addOn.Percent != 78 {
		t.Fatalf("percentage 0.78 应折算成 78，得到 %v", addOn.Percent)
	}
	if got := out.expiresAt(); got != 253402214400 {
		t.Fatalf("expiresAt 毫秒应折算成秒，得到 %d", got)
	}
	if pct := out.totalUsagePercent(); pct != 78 {
		t.Fatalf("totalUsagePercentage 0.78 应折算成 78，得到 %v", pct)
	}

	q := qoderQuotaStatus(out)
	if q == nil {
		t.Fatal("应产出 QuotaStatus")
	}
	if q.Total != 1800 || q.Used != 1400 || q.Remaining != 400 {
		t.Fatalf("总量/已用/剩余不对：%+v", q)
	}
	if len(q.Windows) == 0 {
		t.Fatal("应至少有一个额度窗口")
	}
	if q.Balance != 400 {
		t.Fatalf("余额应为 400，得到 %v", q.Balance)
	}
}

// v1（snake_case）_response 同样要能解（老账号兜底）。
func TestQoderQuotaStatusV1SnakeCase(t *testing.T) {
	raw := `{
      "user_id":"01a171e8-dc26-725a-8461-f76204952d99",
      "plan_tier":"pro_trial","plan_tier_name":"Pro Trial",
      "expires_at":1794122069676,
      "total_usage_percentage":36,
      "user_quota":{"used":300,"remaining":0,"total":300,"percentage":100,"unit":"credits"},
      "add_on_quota":{"used":854,"remaining":46,"total":900,"percentage":94.9,"unit":"credits"}
    }`
	out := mustParse(t, raw)
	user, addOn, ok := out.pickBuckets()
	if !ok {
		t.Fatal("v1 字段应能解出额度")
	}
	if user.Total != 300 || user.Used != 300 {
		t.Fatalf("v1 user_quota 不对：%+v", user)
	}
	if addOn.Total != 900 || addOn.Remaining != 46 {
		t.Fatalf("v1 add_on_quota 不对：%+v", addOn)
	}
	if got := out.planName(); got != "Pro Trial" {
		t.Fatalf("plan 名 = %q", got)
	}
	q := qoderQuotaStatus(out)
	if q == nil || q.Total != 1200 || q.Remaining != 46 {
		t.Fatalf("汇总不对：%+v", q)
	}
}

// 两边都没数时返回 nil（让上层展示"未返回额度"，而不是显示 0 卡）。
func TestQoderQuotaStatusEmpty(t *testing.T) {
	out := mustParse(t, `{"userId":"x"}`)
	if _, _, ok := out.pickBuckets(); ok {
		t.Fatal("空响应不该判定有额度")
	}
	if q := qoderQuotaStatus(out); q != nil {
		t.Fatalf("空响应应返回 nil，得到 %+v", q)
	}
}

// 只有总百分比、没有桶时，也要给出可展示的用量。
func TestQoderQuotaStatusPercentOnly(t *testing.T) {
	out := mustParse(t, `{"totalUsagePercentage":0.42,"expiresAt":1794122069676}`)
	q := qoderQuotaStatus(out)
	if q == nil {
		t.Fatal("只有百分比时也应产出视图")
	}
	if len(q.Windows) != 1 || q.Windows[0].Used < 41.9 || q.Windows[0].Used > 42.1 {
		t.Fatalf("百分比窗口不对：%+v", q.Windows)
	}
}

// 字符串数字、整数、小数混着来也要能解。
func TestQoderFlexFloat(t *testing.T) {
	out := mustParse(t, `{"addOnQuota":{"total":"1,800","used":1400,"remaining":"400.0","unit":"credits"}}`)
	_, addOn, ok := out.pickBuckets()
	if !ok {
		t.Fatal("字符串数字应能解析")
	}
	if addOn.Total != 1800 || addOn.Used != 1400 || addOn.Remaining != 400 {
		t.Fatalf("字符串数字解析不对：%+v", addOn)
	}
}

func mustParse(t *testing.T, raw string) qoderUsageResponse {
	t.Helper()
	var out qoderUsageResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	return out
}
