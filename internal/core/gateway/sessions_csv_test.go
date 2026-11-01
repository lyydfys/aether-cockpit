package gateway

import (
	"encoding/csv"
	"strings"
	"testing"
)

func TestSessionsUsageCSV(t *testing.T) {
	views := []sessionUsageView{
		{
			Agent: "claude-code", ID: "s1", Name: "Claude Code", Title: `a,b"c`, Cwd: `C:\work`,
			Start: 1700000000000, Last: 1700000060000,
			Input: 10, Output: 20, CacheRead: 3, CacheWrite: 4, Cost: 0.123456,
			Active: 60, Messages: 2, ToolCalls: 1,
			Models: []sessionModelView{{Model: "claude-sonnet"}, {Model: "gpt-5"}},
		},
	}
	text, err := sessionsUsageCSV(views)
	if err != nil {
		t.Fatal(err)
	}
	r := csv.NewReader(strings.NewReader(text))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("CSV 不合法: %v\n%s", err, text)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0][0] != "agent" || rows[0][len(rows[0])-1] != "models" {
		t.Fatalf("header = %v", rows[0])
	}
	if rows[1][3] != `a,b"c` {
		t.Fatalf("title 未正确转义: %q", rows[1][3])
	}
	if rows[1][8] != "20" || rows[1][11] != "0.123456" || rows[1][15] != "claude-sonnet;gpt-5" {
		t.Fatalf("row = %v", rows[1])
	}
}
