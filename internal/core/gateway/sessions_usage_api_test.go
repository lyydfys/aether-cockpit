package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/sessions"
)

func TestSessionViewCostAndResume(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	body := `{"type":"user","timestamp":"2026-10-28T01:00:00Z","message":{"role":"user","content":"hi"}}
{"type":"assistant","timestamp":"2026-10-28T01:00:10Z","uuid":"a1","message":{"id":"m1","model":"claude-sonnet-4-6","content":[{"type":"text"}],"usage":{"input_tokens":1000000,"output_tokens":100000,"cache_read_input_tokens":500000,"cache_creation_input_tokens":0}}}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{}
	prices := map[string]ModelPrice{"claude-sonnet-4-6": {InputPerM: 3, OutputPerM: 15, CachedReadPerM: 0.3}}
	v := g.sessionViewOf(sessions.Session{ID: "sid", Agent: "claude-code", Path: path, Last: 1790000000}, prices, true)
	if !v.Priced || v.Unpriced {
		t.Errorf("priced=%v unpriced=%v", v.Priced, v.Unpriced)
	}
	// 1M*3 + 0.1M*15 + 0.5M*0.3 = 3 + 1.5 + 0.15 = 4.65
	if v.Cost < 4.649 || v.Cost > 4.651 {
		t.Errorf("cost = %v", v.Cost)
	}
	if v.Input != 1000000 || v.Output != 100000 || v.CacheRead != 500000 {
		t.Errorf("usage = %+v", v)
	}
	if v.Resume != "claude --resume sid" || v.Path != path {
		t.Errorf("resume=%q path=%q", v.Resume, v.Path)
	}
	if v.Key != "claude-code:sid" {
		t.Errorf("key = %q", v.Key)
	}
}

func TestShapeOfBuckets(t *testing.T) {
	list := []sessionUsageView{
		{Messages: 0, Active: 0, ToolCalls: 0},
		{Messages: 7, Active: 90, ToolCalls: 3},
		{Messages: 200, Active: 3600, ToolCalls: 40},
	}
	shape := shapeOf(list)
	for _, k := range []string{"messages", "minutes", "autonomy"} {
		b, ok := shape[k].(map[string]any)
		if !ok {
			t.Fatalf("%s missing", k)
		}
		if b["total"] != 3 {
			t.Errorf("%s total = %v", k, b["total"])
		}
	}
}
