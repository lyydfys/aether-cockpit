package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/library"
)

// handleSkillsRemoveGone POST /api/library/skills/remove-gone {names}
//
// 只移除“来源已确认没有”的技能：仓库来源会再确认一次（期间又出现就保留），
// 内容先进集中备份，再删掉已下发的目录与库条目——不静默丢弃用户的本地副本。
func (g *Gateway) handleSkillsRemoveGone(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Names []string `json:"names"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	changed := []string{}
	unremoved := []map[string]any{}
	for _, raw := range body.Names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		var sk *library.SkillEntry
		for _, s := range g.library.List().Skills {
			if s.Name == name {
				cp := s
				sk = &cp
				break
			}
		}
		if sk == nil {
			unremoved = append(unremoved, map[string]any{"what": "skill:" + name, "error": "条目不存在"})
			continue
		}
		gone := false
		if c := g.skillCheckOf(name); c != nil {
			st, _ := c["status"].(string)
			gone = st == "gone"
		}
		repoInstalled := (sk.Kind == "github" || sk.Kind == "gitlab" || sk.Kind == "git") &&
			sk.Repo != "" && sk.SkillPath != ""
		if repoInstalled {
			remote, err := fetchRepoRaw(ctx, sk.Kind, sk.Repo, sk.SkillPath)
			switch {
			case err == nil:
				if remote != sk.Content {
					g.setSkillCheck(name, "update", "")
				} else {
					g.setSkillCheck(name, "current", "")
				}
				unremoved = append(unremoved, map[string]any{
					"what": "skill:" + name, "error": "来源上又有这个技能了，未删除",
				})
				continue
			case errors.Is(err, errSkillGone):
				gone = true
			default:
				unremoved = append(unremoved, map[string]any{
					"what": "skill:" + name, "error": "无法确认来源状态，未删除: " + err.Error(),
				})
				continue
			}
		}
		if !gone {
			unremoved = append(unremoved, map[string]any{
				"what": "skill:" + name, "error": "来源没有标记为已移除，未删除",
			})
			continue
		}
		_ = g.library.BackupContent(sk.Name, sk.Content)
		for _, target := range []string{"claude", "agents"} {
			_ = g.library.RemoveAppliedSkill(sk.Name, target)
		}
		g.library.DeleteSkill(sk.ID)
		g.clearSkillCheck(name)
		changed = append(changed, name)
	}
	out := map[string]any{"changed": changed}
	if len(unremoved) > 0 {
		out["unremoved"] = unremoved
	}
	g.writeLibraryViewExtra(w, map[string]any{"result": out})
}
