package gateway

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestNoExternalProjectNamesHardLock enforces the repository-wide hard lock:
// no source file, comment, test, script, or documentation file may contain
// external/competitor/reference project names.
func TestNoExternalProjectNamesHardLock(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	tokens := []string{
		"dsh" + "-go",
		"dsh" + "go",
		"ccch1m" + "neyyy",
		"mag" + "pie",
		"yet" + "one",
		"sub2" + "api",
		"wei-" + "shaw",
		"cockpit" + "-tools",
		"cockpit" + "_tools",
		"cockpit" + "tools",
		"Cockpit" + " Tools",
		"cc-" + "switch",
		"cc_" + "switch",
		"cc" + "switch",
		"CC " + "Switch",
		"cli" + "proxy" + "api",
		"cli" + "proxy",
		"new-" + "api",
		"one-" + "api",
		"velo" + "era",
		"opencode-" + "agy-auth",
		`\bcle` + `wd\b`,
		"lite" + "llm",
		"prompt" + "foo",
		"agent" + "gateway",
		"one-" + "hub",
		"super-" + "api",
		"chat" + "nio",
		"cherry" + " studio",
		"next" + "chat",
		"lobe-" + "chat",
		"open-" + "webui",
		"libre" + "chat",
	}
	re := regexp.MustCompile(`(?i)(` + strings.Join(tokens, `|`) + `)`)

	skipDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		".gradle":      true,
		"build":        true,
		"bin":          true,
		"out":          true,
	}

	textExts := map[string]bool{
		".go": true, ".md": true, ".js": true, ".mjs": true, ".ts": true,
		".css": true, ".html": true, ".json": true, ".jsonc": true, ".sh": true,
		".ps1": true, ".yml": true, ".yaml": true, ".kt": true, ".kts": true,
		".xml": true, ".txt": true, ".properties": true, ".toml": true,
		".mod": true, ".sum": true, ".gradle": true, ".proto": true, ".sql": true,
		".py": true, ".rs": true, ".java": true, ".svg": true, ".env": true,
		".mk": true, ".gradle.kts": true,
	}

	// 无扩展名的文本文件也要扫（它们同样是仓库的一部分）。
	textNames := map[string]bool{
		"NOTICE": true, "LICENSE": true, "VERSION": true, "Dockerfile": true,
		"Makefile": true, "Procfile": true, ".gitignore": true,
		".gitattributes": true, ".editorconfig": true,
	}

	// 署名文件豁免：NOTICE / LICENSE / README / CHANGELOG 必须能承载上游项目的
	// 版权与许可声明（MIT、LGPL-3.0 均要求保留归属）。硬锁的目标是产品代码、
	// 脚本与配置零外部项目名，而不是抹掉法律要求的致谢。
	// 注意：这三处实现（本 Go 测试 / scripts/check-no-external-project-names.sh /
	// Android 的 NoExternalProjectNamesContractTest）必须保持同一份豁免名单。
	attributionFiles := map[string]bool{
		"NOTICE":       true,
		"NOTICE.md":    true,
		"LICENSE":      true,
		"LICENSE.md":   true,
		"CHANGELOG.md": true,
		"README.md":    true,
		"README.en.md": true,
	}

	var violations []string

	err = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			if skipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}

		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			rel = path
		}
		relSlash := filepath.ToSlash(rel)

		if m := re.FindString(relSlash); m != "" {
			violations = append(violations, "PATH "+relSlash+" matched "+m)
		}

		// 署名文件只查路径、不查内容（见上方 attributionFiles 说明）。
		if attributionFiles[relSlash] {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(name))
		if !textExts[ext] && !textNames[name] {
			return nil
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		lines := strings.Split(string(raw), "\n")
		for idx, line := range lines {
			if m := re.FindString(line); m != "" {
				violations = append(violations, relSlash+":"+strconv.Itoa(idx+1)+" matched "+m)
				if len(violations) >= 50 {
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}

	if len(violations) > 0 {
		t.Fatalf("forbidden external project names detected (%d):\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}
