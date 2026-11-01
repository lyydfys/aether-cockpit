package library

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCentralBackupMirror(t *testing.T) {
	s, home := newTestStore(t)
	target := filepath.Join(home, "x.json")
	if err := os.WriteFile(target, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.backupFile(target); err != nil {
		t.Fatal(err)
	}
	dir := s.BackupDir()
	if dir == "" {
		t.Fatal("BackupDir 为空")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("集中备份条目 = %v err=%v", entries, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if string(data) != "v1" {
		t.Fatalf("备份内容 = %q", data)
	}

	// 同一来源超过 5 份时只留最新 5 份。
	hash := fmt.Sprintf("%08x", fnv32a(filepath.Clean(target)))
	prefix := "x.json-" + hash + backupSuffix
	for i := 0; i < 7; i++ {
		name := filepath.Join(dir, prefix+fmt.Sprintf("2020010%d-000000", i))
		if err := os.WriteFile(name, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.backupFile(target); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, prefix+"*"))
	if len(matches) != maxBackups {
		t.Fatalf("保留份数 = %d，期望 %d", len(matches), maxBackups)
	}
}

func TestBackupDirCreates(t *testing.T) {
	s, _ := newTestStore(t)
	dir := s.BackupDir()
	if dir == "" {
		t.Fatal("BackupDir 为空")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("目录未创建: %v", err)
	}
}
