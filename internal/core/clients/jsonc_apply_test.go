package clients

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 带注释的 Qwen / Droid 配置不再拒写，且注释与用户其它键必须保住。
func TestJSONCAppliersPreserveComments(t *testing.T) {
	cases := []struct {
		id      string
		relPath string
		seed    string
		must    []string
	}{
		{
			id:      "qwen_code",
			relPath: filepath.Join(".qwen", "settings.json"),
			seed:    "// qwen 说明\n{\n  \"theme\": \"dark\",\n  \"env\": {\"KEEP\": \"me\"}\n}\n",
			must:    []string{"// qwen 说明", `"theme"`, `"KEEP"`},
		},
		{
			id:      "droid",
			relPath: filepath.Join(".factory", "settings.json"),
			seed:    "// droid 说明\n{\n  \"other\": 1,\n  \"customModels\": []\n}\n",
			must:    []string{"// droid 说明", `"other"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			home := t.TempDir()
			m := NewWithHome(home)
			path := filepath.Join(home, tc.relPath)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.seed), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Apply(tc.id, ApplyParams{
				BaseURL: "http://127.0.0.1:7860", APIKey: "k", Model: "mock/echo",
			}); err != nil {
				t.Fatalf("带注释的配置应能改写：%v", err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			out := string(raw)
			for _, want := range tc.must {
				if !strings.Contains(out, want) {
					t.Errorf("期望保留 %q，实际：\n%s", want, out)
				}
			}
			if !strings.Contains(out, "127.0.0.1:7860") {
				t.Errorf("没有写入网关地址：\n%s", out)
			}
		})
	}
}
