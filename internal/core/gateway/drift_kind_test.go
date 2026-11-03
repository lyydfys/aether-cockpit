package gateway

import (
	"testing"

	"aether-cockpit/internal/core/clients"
)

// 漂移要分类，而不是一律报"unwired"：页面按类型给不同修复动作。
func TestClassifyDrift(t *testing.T) {
	cases := []struct {
		name string
		in   []clients.PreviewChange
		want string
	}{
		{"文件被删", []clients.PreviewChange{{Path: "a.json", Removed: true}}, "removed"},
		{"文件被新建", []clients.PreviewChange{{Path: "a.json", Created: true}}, "created"},
		{"地址被改回官方", []clients.PreviewChange{{Path: "a.json", Lines: []clients.DiffLine{
			{Op: "-", Text: `"ANTHROPIC_BASE_URL": "http://127.0.0.1:7860"`},
			{Op: "+", Text: `"ANTHROPIC_BASE_URL": "https://api.anthropic.com"`},
		}}}, "unwired"},
		{"只改了模型", []clients.PreviewChange{{Path: "a.json", Lines: []clients.DiffLine{
			{Op: "-", Text: `"model": "aether/claude"`},
			{Op: "+", Text: `"model": "gpt-4o"`},
		}}}, "model"},
		{"其它键被改", []clients.PreviewChange{{Path: "a.json", Lines: []clients.DiffLine{
			{Op: "-", Text: `"theme": "dark"`},
			{Op: "+", Text: `"theme": "light"`},
		}}}, "edited"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyDrift(tc.in); got != tc.want {
				t.Fatalf("classifyDrift = %q, want %q", got, tc.want)
			}
		})
	}
}

// 多处变化时取最严重的一处（文件被删 > 地址被改 > 新建 > 其它）。
func TestClassifyDriftTakesWorst(t *testing.T) {
	in := []clients.PreviewChange{
		{Path: "a.json", Lines: []clients.DiffLine{{Op: "-", Text: `"theme": "dark"`}, {Op: "+", Text: `"theme": "light"`}}},
		{Path: "b.json", Removed: true},
	}
	if got := classifyDrift(in); got != "removed" {
		t.Fatalf("应取最严重的 removed，得到 %q", got)
	}
}
