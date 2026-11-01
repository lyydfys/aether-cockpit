package clients

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PreviewChange 是一次 Apply/Restore 会带来的文件变化（不落盘）。
type PreviewChange struct {
	Path    string     `json:"path"`
	Created bool       `json:"created,omitempty"`
	Removed bool       `json:"removed,omitempty"`
	Lines   []DiffLine `json:"lines"`
}

// DiffLine 是一行变化。op 为 "+" 新增或 "-" 删除。
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
	Was  string `json:"was,omitempty"`
}

// PreviewApply 返回把网关写进客户端配置会产生的 diff，不修改任何文件。
func (m *Manager) PreviewApply(id string, p ApplyParams) ([]PreviewChange, error) {
	d, ok := m.def(id)
	if !ok {
		return nil, fmt.Errorf("未知客户端: %s", id)
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return nil, fmt.Errorf("BaseURL 不能为空")
	}
	p.BaseURL = strings.TrimRight(p.BaseURL, "/")

	path := m.pathOf(d)
	var old []byte
	created := true
	if fileExists(path) {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		old, created = trimBOM(b), false
	}
	var catalogChange *PreviewChange
	if d.kind == KindTOML && len(p.ModelOptions) > 0 {
		existing := tomlTopValue(old, "model_catalog_json")
		if existing == "" || existing == codexCatalogName {
			p.CodexCatalog = codexCatalogName
			catPath := filepath.Join(filepath.Dir(path), codexCatalogName)
			var oldCat []byte
			catCreated := true
			if b, err := os.ReadFile(catPath); err == nil {
				oldCat, catCreated = b, false
			}
			newCat := codexCatalog(p.ModelOptions)
			catalogChange = &PreviewChange{Path: catPath, Created: catCreated, Lines: diffLines(string(oldCat), string(newCat))}
		}
	}
	out, err := m.renderFor(d, old, p)
	if err != nil {
		return nil, err
	}
	changes := []PreviewChange{{Path: path, Created: created, Lines: diffLines(string(old), string(out))}}
	if catalogChange != nil {
		changes = append(changes, *catalogChange)
	}
	return changes, nil
}

// PreviewRestore 返回断开会把配置还原成上一份备份的 diff，不修改任何文件。
func (m *Manager) PreviewRestore(id string) ([]PreviewChange, error) {
	d, ok := m.def(id)
	if !ok {
		return nil, fmt.Errorf("未知客户端: %s", id)
	}
	path := m.pathOf(d)
	backups := m.listBackups(path)
	if len(backups) == 0 {
		return nil, fmt.Errorf("没有可用的备份")
	}
	createdMarker := strings.HasSuffix(filepath.Base(backups[0].File), "-created")
	var restored []byte
	var err error
	if !createdMarker {
		restored, err = os.ReadFile(backups[0].File)
		if err != nil {
			return nil, err
		}
	}
	var cur []byte
	if fileExists(path) {
		cur, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
	}
	removed := createdMarker || len(strings.TrimSpace(string(restored))) == 0
	changes := []PreviewChange{{Path: path, Removed: removed, Lines: diffLines(string(cur), string(restored))}}
	if d.kind == KindTOML {
		catPath := filepath.Join(filepath.Dir(path), codexCatalogName)
		if b, err := os.ReadFile(catPath); err == nil {
			changes = append(changes, PreviewChange{Path: catPath, Removed: true, Lines: diffLines(string(b), "")})
		}
	}
	return changes, nil
}

// diffLines 是逐行 diff（LCS）。文件很大时退化为按位置比较，仍只返回变化行。
func diffLines(oldText, newText string) []DiffLine {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	out := make([]DiffLine, 0, 16)
	if len(oldLines)*len(newLines) > 1_000_000 {
		n := len(oldLines)
		if len(newLines) > n {
			n = len(newLines)
		}
		for i := 0; i < n; i++ {
			switch {
			case i >= len(newLines):
				out = append(out, DiffLine{Op: "-", Text: oldLines[i]})
			case i >= len(oldLines):
				out = append(out, DiffLine{Op: "+", Text: newLines[i]})
			case oldLines[i] != newLines[i]:
				out = append(out, DiffLine{Op: "-", Text: oldLines[i]}, DiffLine{Op: "+", Text: newLines[i]})
			}
		}
		return out
	}
	// LCS 表：lcs[i][j] = old[i:] 与 new[j:] 的最长公共子序列长度。
	lcs := make([][]int, len(oldLines)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		switch {
		case oldLines[i] == newLines[j]:
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{Op: "-", Text: oldLines[i]})
			i++
		default:
			out = append(out, DiffLine{Op: "+", Text: newLines[j]})
			j++
		}
	}
	for ; i < len(oldLines); i++ {
		out = append(out, DiffLine{Op: "-", Text: oldLines[i]})
	}
	for ; j < len(newLines); j++ {
		out = append(out, DiffLine{Op: "+", Text: newLines[j]})
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}
