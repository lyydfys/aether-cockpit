package library

import (
	"os"
	"path/filepath"
	"testing"
)

func newLinkStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	home := t.TempDir()
	path := filepath.Join(dir, "library.json")
	return NewStoreWithHome(path, home), dir, home
}

func TestApplySkillLinkAndCopy(t *testing.T) {
	s, dir, home := newLinkStore(t)
	entry, err := s.SaveSkill(SkillEntry{Name: "LinkSkill", Content: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(dir, "skills", skillDirName(entry.Name))
	targetDir := filepath.Join(home, ".claude", "skills", skillDirName(entry.Name))

	if _, err := s.ApplySkillAs(entry.ID, "claude", true); err != nil {
		t.Fatalf("ApplySkillAs(link): %v", err)
	}
	if !IsDirLink(targetDir) {
		t.Fatalf("目标不是目录链接: %s", targetDir)
	}
	c1, err := os.Stat(filepath.Join(canonical, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := os.Stat(filepath.Join(targetDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(c1, t1) {
		t.Fatal("链接目录里的 SKILL.md 与 canonical 不是同一文件")
	}

	// 保存技能即同步到链接目录。
	entry.Content = "v2"
	if _, err := s.SaveSkill(entry); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(targetDir, "SKILL.md"))
	if string(raw) != "v2" {
		t.Fatalf("链接目录内容 = %q", raw)
	}

	// 切回复制：链接被替换成真实目录。
	if _, err := s.ApplySkillAs(entry.ID, "claude", false); err != nil {
		t.Fatal(err)
	}
	if IsDirLink(targetDir) {
		t.Fatal("切回复制后仍是链接")
	}
	raw, _ = os.ReadFile(filepath.Join(targetDir, "SKILL.md"))
	if string(raw) != "v2" {
		t.Fatalf("复制内容 = %q", raw)
	}

	// 再切回链接。
	if _, err := s.ApplySkillAs(entry.ID, "claude", true); err != nil {
		t.Fatal(err)
	}
	if !IsDirLink(targetDir) {
		t.Fatal("再次链接失败")
	}
}

func TestApplySkillLinkRefusesForeignDir(t *testing.T) {
	s, _, home := newLinkStore(t)
	entry, err := s.SaveSkill(SkillEntry{Name: "Foreign", Content: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".claude", "skills", skillDirName(entry.Name))
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplySkillAs(entry.ID, "claude", true); err == nil {
		t.Fatal("含其他文件的目录应拒绝改为链接")
	}
	if _, err := os.Stat(filepath.Join(target, "notes.txt")); err != nil {
		t.Fatalf("拒绝后文件被删: %v", err)
	}
}

func TestSkillHowPersistence(t *testing.T) {
	s, dir, home := newLinkStore(t)
	if s.SkillHowSetting() != SkillHowCopy {
		t.Fatalf("默认 = %q", s.SkillHowSetting())
	}
	if err := s.SetSkillHow("", SkillHowLink); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSkillHow("claude_code", SkillHowCopy); err != nil {
		t.Fatal(err)
	}
	if got := s.EffectiveSkillHow("claude"); got != SkillHowCopy {
		t.Fatalf("claude = %q", got)
	}
	if got := s.EffectiveSkillHow("agents"); got != SkillHowLink {
		t.Fatalf("agents = %q", got)
	}
	if err := s.SetSkillHow("", "weird"); err == nil {
		t.Fatal("非法方式应拒绝")
	}

	reloaded := NewStoreWithHome(filepath.Join(dir, "library.json"), home)
	if reloaded.SkillHowSetting() != SkillHowLink {
		t.Fatalf("重载默认 = %q", reloaded.SkillHowSetting())
	}
	if h, own := reloaded.AgentSkillHow("claude_code"); h != SkillHowCopy || !own {
		t.Fatalf("重载 claude_code = %q own=%v", h, own)
	}
	if got := reloaded.EffectiveSkillHow("claude"); got != SkillHowCopy {
		t.Fatalf("重载 claude = %q", got)
	}
}
