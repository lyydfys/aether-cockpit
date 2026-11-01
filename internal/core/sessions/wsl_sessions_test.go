package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

// WSL 目录通过 UNC/注入路径参与扫描，Agent 与标题与本地一致。
func TestScanIncludesWSLHome(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-home-wsl-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"/home/wsl/proj","message":{"content":"Hello from WSL"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "wsl-session-1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AETHER_WSL_HOMES", home)
	t.Setenv("AETHER_WSL_DISTRO", "Ubuntu-22.04")

	found := false
	for _, s := range Scan() {
		if s.ID == "wsl-session-1" {
			found = true
			if s.Agent != "claude-code" || s.Title != "Hello from WSL" || s.Cwd != "/home/wsl/proj" {
				t.Fatalf("wsl session = %+v", s)
			}
			if s.WSL != "Ubuntu-22.04" {
				t.Fatalf("wsl distro = %q", s.WSL)
			}
		}
	}
	if !found {
		t.Fatal("WSL 注入目录的会话未被扫描")
	}
}

// 仅输出本机 WSL 检测结果，供活体取证；没有 WSL 时为空。
func TestWSLDetectionInfo(t *testing.T) {
	t.Setenv("AETHER_WSL_HOMES", "")
	t.Logf("detected WSL roots: %v", wslRoots())
}
