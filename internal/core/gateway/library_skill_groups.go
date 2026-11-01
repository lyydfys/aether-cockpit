package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// 技能的“我的分组”（插件 skills/group / skills/ungroup）：
// 组只是技能上的一个名字，分组不移动文件，也不影响下发；按名字整体改名/解散。

// handleSkillGroup POST /api/library/skills/group {name, old, names}
//
//	old 非空且 names 为空：把 old 组整体改名为 name；
//	names 非空：把这些技能放进 name 组（从原组移出）。
func (g *Gateway) handleSkillGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string   `json:"name"`
		Old   string   `json:"old"`
		Names []string `json:"names"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	name := strings.TrimSpace(body.Name)
	old := strings.TrimSpace(body.Old)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "分组名不能为空"})
		return
	}
	names := cleanSkillNames(body.Names)
	if len(names) == 0 && old != "" {
		names = g.library.SkillsInGroup(old)
	}
	if len(names) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "没有要分组的技能"})
		return
	}
	changed := g.library.UpdateSkillGroup(names, name)
	g.writeLibraryViewExtra(w, map[string]any{"result": map[string]any{"changed": changed}})
}

// handleSkillUngroup POST /api/library/skills/ungroup {name, names}
//
// names 为空：解散整组（所有成员移出）；否则只移出列出的技能。
func (g *Gateway) handleSkillUngroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string   `json:"name"`
		Names []string `json:"names"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少分组名"})
		return
	}
	names := cleanSkillNames(body.Names)
	if len(names) == 0 {
		names = g.library.SkillsInGroup(name)
	}
	changed := g.library.UpdateSkillGroup(names, "")
	g.writeLibraryViewExtra(w, map[string]any{"result": map[string]any{"changed": changed}})
}

func cleanSkillNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return dedupeStrings(out)
}
