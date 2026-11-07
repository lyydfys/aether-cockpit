package trae

import (
	"encoding/json"
	"testing"
)

// v2 的套餐额度在顶层 usage_summary 里：1400 总额 / 212.74 已用。
// 旧实现只认 pack 里的 basic_usage_limit（恒为 0），会误判"没有额度包"退回签到积分。
func TestEntUsageToQuotaV2Summary(t *testing.T) {
	raw := `{
      "is_credits_billing": true,
      "usage_summary": {"consumed_amount": 212.74, "total_amount": 1400, "consumption_ratio": 0.152},
      "user_entitlement_pack_list": [
        {"display_desc":"免费","entitlement_base_info":{"end_time":1796054399,"quota":{}},"usage":{}},
        {"display_desc":"签到奖励","entitlement_base_info":{"end_time":1794151378,"quota":{"credits_limit":2000}},"usage":{"credits_amount":100}}
      ]
    }`
	var out traeEntUsage
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	q := entUsageToQuota(out)
	if q == nil {
		t.Fatal("v2 应能算出额度")
	}
	if q.Total != 1400 || q.Used != 212 || q.Remaining != 1187 {
		t.Fatalf("总量/已用/剩余不对：total=%d used=%d remaining=%d", q.Total, q.Used, q.Remaining)
	}
	if q.Unit != "credits" {
		t.Fatalf("单位 = %q", q.Unit)
	}
	if len(q.Windows) != 1 {
		t.Fatalf("应有一个套餐额度窗口，得到 %+v", q.Windows)
	}
	// 重置时间取最晚的包（1796054399），不是某个签到包（1794151378）。
	if q.Until != 1796054399 {
		t.Fatalf("重置时间应为最晚到期 1796054399，得到 %d", q.Until)
	}
	if pct := q.Windows[0].Used; pct < 15.1 || pct > 15.3 {
		t.Fatalf("已用百分比应为 15.2%%，得到 %v", pct)
	}
}

// v1 的嵌套形状仍要能用（老账号）。
func TestEntUsageToQuotaV1Packs(t *testing.T) {
	raw := `{
      "code": 0,
      "data": {"user_entitlement_pack_list": [
        {"entitlement_base_info":{"end_time":"2026-12-01T00:00:00Z","quota":{"basic_usage_limit":1000}},
         "usage":{"basic_usage_amount":250}}
      ]}
    }`
	var out traeEntUsage
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	q := entUsageToQuota(out)
	if q == nil {
		t.Fatal("v1 应能算出额度")
	}
	if q.Total != 1000 || q.Used != 250 || q.Remaining != 750 {
		t.Fatalf("v1 换算不对：%+v", q)
	}
	if q.Until == 0 {
		t.Fatal("RFC3339 的 end_time 应被解析成时间戳")
	}
}

// 签到包（只有 credits_limit）不能被当成套餐额度。
func TestEntUsageToQuotaIgnoresCheckinPacks(t *testing.T) {
	raw := `{"user_entitlement_pack_list":[
      {"display_desc":"签到奖励","entitlement_base_info":{"quota":{"credits_limit":2000}},"usage":{"credits_amount":100}}
    ]}`
	var out traeEntUsage
	_ = json.Unmarshal([]byte(raw), &out)
	if q := entUsageToQuota(out); q != nil {
		t.Fatalf("只有签到包时不该产出套餐额度，得到 %+v", q)
	}
}

// 两种形状都没有额度时返回 nil。
func TestEntUsageToQuotaEmpty(t *testing.T) {
	var out traeEntUsage
	_ = json.Unmarshal([]byte(`{"code":0}`), &out)
	if q := entUsageToQuota(out); q != nil {
		t.Fatalf("空响应应返回 nil，得到 %+v", q)
	}
}

// flexUnix 要能吃秒 / 毫秒 / RFC3339 三种写法。
func TestFlexUnix(t *testing.T) {
	cases := map[string]int64{
		`1796054399`:             1796054399,
		`1796054399000`:          1796054399,
		`"1796054399"`:           1796054399,
		`"2026-12-01T00:00:00Z"`: 1796083200,
		`null`:                   0,
	}
	for raw, want := range cases {
		var v flexUnix
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatalf("%s 解析失败：%v", raw, err)
		}
		if int64(v) != want {
			t.Errorf("%s → %d，want %d", raw, int64(v), want)
		}
	}
}
