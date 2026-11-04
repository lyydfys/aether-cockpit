package workbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 本周期额度优先：Cycle* 与 Capacity* 同时存在时必须用 Cycle*。
//
// 真实场景：账号买了 10000 credits（Capacity），本周期只发 1000（Cycle），
// 已用 900。用 Capacity 算会显示"还剩 9100"，用户却马上会被限流。
func TestWorkBuddyQuotaPrefersCycleCapacity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/get-user-resource") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Data":{"Response":{"Data":{"Accounts":[{
			"CapacitySize":10000,"CapacityUsed":9500,"CapacityRemain":500,
			"CycleCapacitySize":1000,"CycleCapacityUsed":900,"CycleCapacityRemain":100,
			"CycleEndTime":"2026-11-10T00:00:00Z","ProductName":"Pro"
		}]}}}}`))
	}))
	defer srv.Close()

	q, err := newProvider(cnProfile).QueryQuota(context.Background(), wbQuotaEnv(srv))
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q == nil || len(q.Windows) != 1 {
		t.Fatalf("q = %+v", q)
	}
	w := q.Windows[0]
	if w.Limit != 1000 || w.Amount != 900 {
		t.Fatalf("应使用本周期额度 1000/900，得到 %+v", w)
	}
	// 剩余用 total-used 反算，而不是信上游的 Remain 字段。
	if q.Balance != 100 {
		t.Fatalf("剩余应为 100，得到 %v", q.Balance)
	}
	// RFC3339 时间必须解出来（过去会被当成数字解析成 0）。
	if w.ResetsAt != 1794268800 {
		t.Fatalf("重置时间应解析为 1794268800，得到 %d", w.ResetsAt)
	}
}

// 额度用光是合法读数：total>0、used==total 时剩余是 0，不能当成"没读到"
// 而回落到别的接口把账号显示成有额度。
func TestWorkBuddyQuotaExhaustedIsValid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/get-user-resource") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Data":{"Response":{"Data":{"Accounts":[{
			"CycleCapacitySize":500,"CycleCapacityUsed":500,"CycleCapacityRemain":0,
			"CycleEndTime":1796083200000
		}]}}}}`))
	}))
	defer srv.Close()

	q, err := newProvider(cnProfile).QueryQuota(context.Background(), wbQuotaEnv(srv))
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Balance != 0 {
		t.Fatalf("用光时剩余应为 0，得到 %v", q.Balance)
	}
	if len(q.Windows) != 1 || q.Windows[0].Used != 100 {
		t.Fatalf("用光时应有一张 100%% 的卡，得到 %+v", q.Windows)
	}
	// 毫秒时间戳要归一成秒。
	if q.Windows[0].ResetsAt != 1796083200 {
		t.Fatalf("毫秒时间戳应归一到秒，得到 %d", q.Windows[0].ResetsAt)
	}
}

// 包字段既可能是 number 也可能是 string 形态。
func TestWorkBuddyQuotaStringNumbers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/get-user-resource") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Data": map[string]any{"Response": map[string]any{"Data": map[string]any{"Accounts": []any{
				map[string]any{"CycleCapacitySize": "2000", "CycleCapacityUsed": "500"},
			}}}},
		})
	}))
	defer srv.Close()

	q, err := newProvider(cnProfile).QueryQuota(context.Background(), wbQuotaEnv(srv))
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Balance != 1500 {
		t.Fatalf("字符串数字应能解析，剩余应为 1500，得到 %v", q.Balance)
	}
}
