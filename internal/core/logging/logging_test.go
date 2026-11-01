package logging

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupWritesAndRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "aether.log")

	oldMax := MaxSize
	MaxSize = 32
	t.Cleanup(func() { MaxSize = oldMax })

	cleanup, err := Setup(path)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	log.Print("first-line-0123456789abcdef")
	cleanup()

	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "first-line") {
		t.Fatalf("日志未写入: err=%v body=%q", err, b)
	}

	// 第二次打开时文件已超过阈值 → 滚动为 .1
	cleanup2, err := Setup(path)
	if err != nil {
		t.Fatalf("Setup2: %v", err)
	}
	log.Print("second")
	cleanup2()
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("未滚动出 .1: %v", err)
	}
	b2, _ := os.ReadFile(path)
	if !strings.Contains(string(b2), "second") || strings.Contains(string(b2), "first-line") {
		t.Fatalf("滚动后新文件内容不符: %q", b2)
	}

	// 空路径是空操作
	noop, err := Setup("")
	if err != nil {
		t.Fatal(err)
	}
	noop()
}
