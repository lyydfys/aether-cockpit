package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 断开时只还原"我方写过的顶层键"：用户接入后手改的其它键必须保住。
//
// 这是"整份备份还原"最容易被骂的地方——用户改了主题、加了别的 provider，
// 一点断开全没了。
func TestRestoreKeepsUserEditsToOtherKeys(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	m.SetKeyStatePath(filepath.Join(home, ".aether", "clients-state.json"))

	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{"theme":"opencode","provider":{"ollama":{"npm":"x"}}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Apply("opencode", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// 用户接入后手改：换主题、加一个自己的 provider。
	cur, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(cur, &root); err != nil {
		t.Fatal(err)
	}
	root["theme"] = "user-changed"
	prov, _ := root["provider"].(map[string]any)
	prov["mine"] = map[string]any{"npm": "my-adapter"}
	edited, _ := json.MarshalIndent(root, "", "  ")
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := m.Restore("opencode", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	var got map[string]any
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("还原后不是合法 JSON: %v\n%s", err, raw)
	}
	if got["theme"] != "user-changed" {
		t.Errorf("用户改的主题被回滚了：%v", got["theme"])
	}
	gp, _ := got["provider"].(map[string]any)
	if _, ok := gp["mine"]; !ok {
		t.Error("用户后加的 provider 被回滚了")
	}
	if _, ok := gp["aether"]; ok {
		t.Errorf("我方写入的 provider.aether 未被移除：%v", gp)
	}
	if _, ok := gp["ollama"]; !ok {
		t.Error("原有 provider 丢了")
	}
}

// 用户没动过文件时，仍走整份备份，保证逐字节回到原样。
func TestRestoreUntouchedFileIsByteIdentical(t *testing.T) {
	home := t.TempDir()
	m := NewWithHome(home)
	m.SetKeyStatePath(filepath.Join(home, ".aether", "clients-state.json"))

	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "{\"theme\": \"opencode\",\n \"provider\": {\"ollama\": {\"npm\": \"x\"}}}\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply("opencode", ApplyParams{BaseURL: "http://127.0.0.1:7860", APIKey: "k"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := m.Restore("opencode", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != original {
		t.Errorf("未改动过的文件应逐字节还原：\n%q\nwant\n%q", raw, original)
	}
}
