package gateway

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
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

	// 只扫**受版本控制**的文件：硬锁要守的是"仓库里有什么"，不是开发者本机的
	// 临时目录（.smoke-data/、build-verify/、构建产物、抓包脚本…）。这些目录里
	// 出现参考项目的名字是正常的（分析工具、对比材料），把它们算作违规会让锁
	// 变成"谁本地有草稿谁红"，最后大家只会学会忽略它。
	if tracked, ok := trackedFiles(repoRoot); ok {
		for _, relSlash := range tracked {
			if len(violations) >= 50 {
				break
			}
			checkOne(repoRoot, relSlash, re, attributionFiles, textExts, textNames, &violations)
		}
	} else {
		// 没有 git（例如从 tar 包解压的源码）时退回全目录扫描。
		err = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				rel = path
			}
			checkOne(repoRoot, filepath.ToSlash(rel), re, attributionFiles, textExts, textNames, &violations)
			if len(violations) >= 50 {
				return fs.SkipAll
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk repo: %v", err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("forbidden external project names detected (%d):\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// trackedFiles 返回仓库里受版本控制的文件（相对路径，斜杠分隔）。
// 第二个返回值表示 git 是否可用。
func trackedFiles(root string) ([]string, bool) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		return nil, false
	}
	parts := strings.Split(string(out), "\x00")
	files := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		files = append(files, filepath.ToSlash(p))
	}
	return files, true
}

// checkOne 检查单个文件：先看路径，再看内容（署名文件只查路径）。
func checkOne(root, relSlash string, re *regexp.Regexp, attributionFiles, textExts, textNames map[string]bool, violations *[]string) {
	if m := re.FindString(relSlash); m != "" {
		*violations = append(*violations, "PATH "+relSlash+" matched "+m)
	}
	if attributionFiles[relSlash] {
		return
	}
	name := path.Base(relSlash)
	ext := strings.ToLower(path.Ext(name))
	if !textExts[ext] && !textNames[name] {
		return
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relSlash)))
	if err != nil {
		return
	}
	for idx, line := range strings.Split(string(raw), "\n") {
		if m := re.FindString(line); m != "" {
			*violations = append(*violations, relSlash+":"+strconv.Itoa(idx+1)+" matched "+m)
			return
		}
	}
}
