package gateway

import (
	"testing"

	"aether-cockpit/internal/core/ir"
)

// live 目录优先，但静态清单里 live 没提到的模型必须保留：
// workbuddy-cn 的 /models 没有 deepseek-v4.1-flash，可上游明明接受它。
func TestMergeCatalogModels(t *testing.T) {
	live := []ir.ModelMeta{
		{ID: "a", DisplayName: "A-live"},
		{ID: "b", DisplayName: "B-live"},
	}
	static := []ir.ModelMeta{
		{ID: "b", DisplayName: "B-static"},
		{ID: "c", DisplayName: "C-static"},
	}
	got := mergeCatalogModels(live, static)
	if len(got) != 3 {
		t.Fatalf("合并后 %d 条，want 3: %+v", len(got), got)
	}
	byID := map[string]string{}
	for _, m := range got {
		byID[m.ID] = m.DisplayName
	}
	if byID["b"] != "B-live" {
		t.Errorf("重复项应保留 live 的元数据，实际 %q", byID["b"])
	}
	if byID["c"] != "C-static" {
		t.Errorf("live 缺失的项应补上静态清单，实际 %q", byID["c"])
	}
}

// 空 live 时等价于静态清单，不能丢模型。
func TestMergeCatalogModelsEmptyLive(t *testing.T) {
	static := []ir.ModelMeta{{ID: "x"}, {ID: "y"}}
	if got := mergeCatalogModels(nil, static); len(got) != 2 {
		t.Fatalf("空 live 应保留全部静态项，实际 %+v", got)
	}
}
