package gateway

import (
	"encoding/json"
	"testing"
)

func balanceTestRoot(t *testing.T) any {
	t.Helper()
	var root any
	if err := json.Unmarshal([]byte(`{
		"data": {"balance": 12.5, "quota": 2500000, "total_available": 750000},
		"remaining": "42.25",
		"credits": {"used": 30, "left": 40},
		"items": [
			{"status": "active", "used": 10, "cap": 100},
			{"status": "active", "used": 30, "cap": 100},
			{"status": "expired", "used": 99, "cap": 100}
		]
	}`), &root); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return root
}

func TestBalanceExpr(t *testing.T) {
	root := balanceTestRoot(t)
	cases := []struct {
		expr string
		want float64
		unit string
	}{
		{"data.balance", 12.5, ""},
		{"$data.quota / 500000", 5, "USD"},
		{"data.total_available / 500000", 1.5, ""},
		{"$remaining", 42.25, "USD"},
		{"(1 - credits.used / 70)", 0.5714, ""},
		{"(1 - credits.used / 70) %", 0.5714, "%"},
		{"sum(items.*.used) / 3", 46.3333, ""},
		{"sum(items[status=active].used)", 40, ""},
		{"max(items.*.used)", 99, ""},
		{"min(items.*.used)", 10, ""},
		{"count(items[status=active].used)", 2, ""},
		{"5h: data.balance %; $credits.left", 12.5, "%"},
		{"data.missing", 0, ""},
	}
	for _, c := range cases {
		got, ok := evalBalanceExpr(c.expr, root, "")
		if c.expr == "data.missing" {
			if ok {
				t.Fatalf("%s 不应求值成功", c.expr)
			}
			continue
		}
		if !ok {
			t.Fatalf("%s 求值失败", c.expr)
		}
		if diff := got.amount - c.want; diff > 0.001 || diff < -0.001 {
			t.Fatalf("%s = %v, want %v", c.expr, got.amount, c.want)
		}
		if got.unit != c.unit {
			t.Fatalf("%s unit = %q, want %q", c.expr, got.unit, c.unit)
		}
	}
}

func TestBalanceExprDisplayText(t *testing.T) {
	root := balanceTestRoot(t)
	if v, ok := evalBalanceExpr("$data.balance", root, ""); !ok || v.text != "$12.5" {
		t.Fatalf("USD text = %+v", v)
	}
	if v, ok := evalBalanceExpr("data.balance %", root, ""); !ok || v.text != "12.5%" {
		t.Fatalf("percent text = %+v", v)
	}
	if v, ok := evalBalanceExpr("", root, "data.balance"); !ok || v.text != "12.5" {
		t.Fatalf("default path = %+v", v)
	}
}

func TestBalanceExprMultipleSegments(t *testing.T) {
	root := balanceTestRoot(t)
	v, ok := evalBalanceExpr("5h: data.balance %; $credits.left", root, "")
	if !ok {
		t.Fatal("多段求值失败")
	}
	if v.text != "12.5%" || v.all != "5h 12.5% · $40" {
		t.Fatalf("多段展示 = %+v", v)
	}
	if v.amount != 12.5 || v.unit != "%" {
		t.Fatalf("第一段应决定余额: %+v", v)
	}
	if len(v.parts) != 2 || v.parts[0].label != "5h" || v.parts[0].text != "12.5%" ||
		v.parts[1].label != "" || v.parts[1].text != "$40" {
		t.Fatalf("parts = %+v", v.parts)
	}
}

func TestTrimFloatKeepsIntegerDigits(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{{40, "40"}, {500000, "500000"}, {12.5, "12.5"}, {0, "0"}, {-3.25, "-3.25"}, {12.499, "12.49"}}
	for _, c := range cases {
		if got := trimFloat(c.in, 2); got != c.want {
			t.Fatalf("trimFloat(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
