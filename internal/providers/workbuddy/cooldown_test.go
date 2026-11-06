package workbuddy

import (
	"net/http"
	"testing"
	"time"
)

// 冷却映射必须覆盖参考实现的每一组错误码（表驱动）。
func TestWBCooldownTable(t *testing.T) {
	cases := []struct {
		name   string
		status int
		raw    string
		want   time.Duration
	}{
		{"forbidden-11140", http.StatusForbidden, `{"code":11140,"message":"abnormal behavior"}`, 30 * time.Minute},
		{"forbidden-11142", http.StatusForbidden, `{"code":11142,"message":"abnormal behavior"}`, 30 * time.Minute},
		{"expired-14015", http.StatusUnauthorized, `{"code":14015,"message":"session expired"}`, 30 * time.Minute},
		{"http-401", http.StatusUnauthorized, `{"message":"unauthorized"}`, 30 * time.Minute},
		{"empty-14001", http.StatusBadRequest, `{"code":14001}`, 60 * time.Minute},
		{"empty-14002", http.StatusBadRequest, `{"code":14002}`, 60 * time.Minute},
		{"empty-14012", http.StatusBadRequest, `{"code":14012}`, 60 * time.Minute},
		{"empty-14013", http.StatusBadRequest, `{"code":14013}`, 60 * time.Minute},
		{"empty-14014", http.StatusBadRequest, `{"code":14014}`, 60 * time.Minute},
		{"empty-14018", http.StatusBadRequest, `{"code":14018}`, 60 * time.Minute},
		{"meter-14003", http.StatusOK, `{"code":14003}`, 1440 * time.Minute},
		{"meter-10105", http.StatusOK, `{"code":10105}`, 1440 * time.Minute},
		{"meter-6005", http.StatusOK, `{"code":6005}`, 1440 * time.Minute},
		{"meter-6006", http.StatusOK, `{"code":6006}`, 1440 * time.Minute},
		{"meter-6007", http.StatusOK, `{"code":6007}`, 1440 * time.Minute},
		{"meter-6008", http.StatusOK, `{"code":6008}`, 1440 * time.Minute},
		{"quota-request-limit", http.StatusTooManyRequests, `{"code":14029,"message":"quota_request_limit reached"}`, 10 * time.Minute},
		{"unrelated", http.StatusBadRequest, `{"code":10085}`, 0},
		{"ok", http.StatusOK, `{"ok":true}`, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := wbCooldown(c.status, c.raw)
			if got != c.want {
				t.Fatalf("wbCooldown(%d, %q) = %v (%s), want %v", c.status, c.raw, got, reason, c.want)
			}
			if got > 0 && reason == "" {
				t.Fatal("触发冷却时必须给出原因")
			}
		})
	}
}

// 风控码与额度码不能再落到 bad_request（否则换号逻辑不会触发）。
func TestRiskAndMeterCodesNotBadRequest(t *testing.T) {
	for _, code := range []string{"11140", "11142", "14003", "10105", "6005", "6006", "6007", "6008"} {
		if workbuddyBadRequestCodes[code] {
			t.Fatalf("%s 不应被当成 bad_request", code)
		}
	}
	if workbuddyBadRequestCodes["10085"] != true || workbuddyBadRequestCodes["11128"] != true {
		t.Fatal("10085/11128 仍应是 bad_request")
	}
}

func TestMapUpstreamErrorCooldownWiring(t *testing.T) {
	ae := mapUpstreamError(http.StatusForbidden, `{"code":11142,"message":"abnormal"}`, http.Header{})
	if ae == nil || ae.RetryAfter != 30*time.Minute || !ae.Retryable {
		t.Fatalf("11142 应映射为可重试 + 30min: %+v", ae)
	}
	ae = mapUpstreamError(http.StatusOK, `{"code":6005,"message":"limit"}`, http.Header{})
	if ae == nil || ae.RetryAfter != 1440*time.Minute {
		t.Fatalf("6005 应映射为 24h 冷却: %+v", ae)
	}
	ae = mapUpstreamError(http.StatusUnauthorized, `{"code":14015}`, http.Header{})
	if ae == nil || ae.RetryAfter != 30*time.Minute {
		t.Fatalf("14015 应带 30min RetryAfter: %+v", ae)
	}
}
