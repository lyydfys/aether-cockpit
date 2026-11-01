package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/library"
)

func TestProjectSkillLinkAndRemoval(t *testing.T) {
	proj := t.TempDir()
	canonical := filepath.Join(t.TempDir(), "skills", "LinkSkill")
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(proj, ".claude", "skills", "LinkSkill")

	if err := applyProjectSkillsMode(proj, "LinkSkill", "v1", []string{"claude_code"}, true, canonical); err != nil {
		t.Fatalf("link: %v", err)
	}
	if !library.IsDirLink(target) {
		t.Fatalf("目标不是链接: %s", target)
	}
	// 改 canonical，项目侧直接看到新内容。
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if string(raw) != "v2" {
		t.Fatalf("链接内容 = %q", raw)
	}

	// 切回复制：链接被替换成真实目录，canonical 保留。
	if err := applyProjectSkillsMode(proj, "LinkSkill", "v2", []string{"claude_code"}, false, ""); err != nil {
		t.Fatal(err)
	}
	if library.IsDirLink(target) {
		t.Fatal("切回复制后仍是链接")
	}
	if _, err := os.Stat(filepath.Join(canonical, "SKILL.md")); err != nil {
		t.Fatalf("canonical 被删: %v", err)
	}

	// 撤销技能：只删项目侧副本，canonical 不动。
	if err := applyProjectSkillsMode(proj, "LinkSkill", "", nil, false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("目标未删除: %v", err)
	}
	if _, err := os.Stat(filepath.Join(canonical, "SKILL.md")); err != nil {
		t.Fatalf("撤销时删到了 canonical: %v", err)
	}
}

func TestProjectSkillLinkRefusesForeignDir(t *testing.T) {
	proj := t.TempDir()
	canonical := filepath.Join(t.TempDir(), "skills", "Foreign")
	_ = os.MkdirAll(canonical, 0o755)
	_ = os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("v1"), 0o644)
	target := filepath.Join(proj, ".claude", "skills", "Foreign")
	_ = os.MkdirAll(target, 0o755)
	_ = os.WriteFile(filepath.Join(target, "notes.txt"), []byte("mine"), 0o644)

	if err := applyProjectSkillsMode(proj, "Foreign", "v1", []string{"claude_code"}, true, canonical); err == nil {
		t.Fatal("含其他文件的目录应拒绝改为链接")
	}
	if _, err := os.Stat(filepath.Join(target, "notes.txt")); err != nil {
		t.Fatalf("拒绝后文件被删: %v", err)
	}
}
