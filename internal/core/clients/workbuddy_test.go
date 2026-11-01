package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyWorkBuddyModels(t *testing.T) {
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	home := t.TempDir()
	m := NewWithHome(home)
	for _, id := range []string{"workbuddy", "workbuddy-ai"} {
		if _, err := m.Apply(id, ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "gw", Model: "claude-sonnet-4-6"}); err != nil {
			t.Fatalf("%s Apply: %v", id, err)
		}
	}
	for folder, id := range map[string]string{".workbuddy": "workbuddy", ".workbuddy-ai": "workbuddy-ai"} {
		path := filepath.Join(home, folder, "models.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s 未写入: %v", id, err)
		}
		var doc struct {
			Models []struct {
				ID     string `json:"id"`
				Vendor string `json:"vendor"`
				URL    string `json:"url"`
			} `json:"models"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Models) != 1 || doc.Models[0].Vendor != "aether" || doc.Models[0].URL != "http://127.0.0.1:7860/v1/chat/completions" {
			t.Fatalf("%s models = %+v", id, doc.Models)
		}
		if st := findStatus(t, m.List(), id); st.Managed == nil || st.Managed.Model != "claude-sonnet-4-6" {
			t.Fatalf("%s managed = %+v", id, st.Managed)
		}
		// 还原：新建的文件应删除。
		if err := m.Restore(id, ""); err != nil {
			t.Fatalf("%s Restore: %v", id, err)
		}
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("%s 还原后 models.json 应被删除", id)
		}
	}
}
