package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"aether-cockpit/internal/core/library"
)

// errSkillGone 表示技能文件已从来源仓库移除（HTTP 404 / 克隆里没有此文件）。
var errSkillGone = errors.New("技能已从来源移除")

func (g *Gateway) skillCheckOf(name string) map[string]any {
	g.skillCheckMu.Lock()
	defer g.skillCheckMu.Unlock()
	c := g.skillChecks[name]
	if c == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range c {
		out[k] = v
	}
	return out
}

func (g *Gateway) setSkillCheck(name, status, errMsg string) {
	g.skillCheckMu.Lock()
	defer g.skillCheckMu.Unlock()
	if g.skillChecks == nil {
		g.skillChecks = map[string]map[string]any{}
	}
	c := map[string]any{"status": status}
	if errMsg != "" {
		c["error"] = errMsg
	}
	g.skillChecks[name] = c
}

func (g *Gateway) clearSkillCheck(name string) {
	g.skillCheckMu.Lock()
	defer g.skillCheckMu.Unlock()
	delete(g.skillChecks, name)
}

// checkGitHubSkills 逐个比对从仓库安装的技能与远端内容。
func (g *Gateway) checkGitHubSkills(ctx context.Context) {
	for _, s := range g.library.List().Skills {
		if (s.Kind != "github" && s.Kind != "gitlab" && s.Kind != "git") || s.Repo == "" || s.SkillPath == "" {
			continue
		}
		remote, err := fetchRepoRaw(ctx, s.Kind, s.Repo, s.SkillPath)
		if err != nil {
			if errors.Is(err, errSkillGone) {
				g.setSkillCheck(s.Name, "gone", err.Error())
			} else {
				g.setSkillCheck(s.Name, "unknown", err.Error())
			}
			continue
		}
		if remote != s.Content {
			g.setSkillCheck(s.Name, "update", "")
		} else {
			g.setSkillCheck(s.Name, "current", "")
		}
	}
}

func (g *Gateway) updateGitHubSkill(ctx context.Context, name string, replace bool) (bool, error) {
	var sk *library.SkillEntry
	for _, s := range g.library.List().Skills {
		if s.Name == name {
			cp := s
			sk = &cp
			break
		}
	}
	if sk == nil {
		return false, fmt.Errorf("库中没有这个技能: %s", name)
	}
	if (sk.Kind != "github" && sk.Kind != "gitlab" && sk.Kind != "git") || sk.Repo == "" || sk.SkillPath == "" {
		return false, fmt.Errorf("%s 不是从仓库安装的，无法更新", name)
	}
	remote, err := fetchRepoRaw(ctx, sk.Kind, sk.Repo, sk.SkillPath)
	if err != nil {
		if errors.Is(err, errSkillGone) {
			g.setSkillCheck(name, "gone", err.Error())
		} else {
			g.setSkillCheck(name, "unknown", err.Error())
		}
		return false, err
	}
	if remote == sk.Content {
		g.setSkillCheck(name, "current", "")
		return false, nil
	}
	if sk.InstalledContent != "" && sk.Content != sk.InstalledContent && !replace {
		return false, fmt.Errorf("%s 在本地改过，确认替换后再更新", name)
	}
	if err := g.library.BackupContent(sk.Name, sk.Content); err != nil {
		return false, err
	}
	updated := *sk
	updated.Content = remote
	updated.Description = skillDescription(remote)
	updated.InstalledContent = remote
	if _, err := g.library.SaveSkill(updated); err != nil {
		return false, err
	}
	g.setSkillCheck(name, "current", "")
	return true, nil
}

// handleSkillsCheck POST /api/library/skills/check
func (g *Gateway) handleSkillsCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	g.checkGitHubSkills(ctx)
	g.writeLibraryView(w)
}

// handleSkillsUpdate POST /api/library/skills/update {name, replace}
func (g *Gateway) handleSkillsUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Replace bool   `json:"replace"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少技能名"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	changed, err := g.updateGitHubSkill(ctx, name, body.Replace)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	updated := []string{}
	if changed {
		updated = append(updated, name)
	}
	g.writeLibraryViewExtra(w, map[string]any{"result": map[string]any{"updated": updated}})
}

// handleSkillsUpdateBulk POST /api/library/skills/update-all|update-some {names}
func (g *Gateway) handleSkillsUpdateBulk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Names []string `json:"names"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	names := body.Names
	if len(names) == 0 {
		for _, s := range g.library.List().Skills {
			if s.Kind == "github" && s.Repo != "" && s.SkillPath != "" {
				names = append(names, s.Name)
			}
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	updated := []string{}
	unupdated := []any{}
	gone := []string{}
	for _, name := range names {
		changed, err := g.updateGitHubSkill(ctx, name, false)
		if err != nil {
			if errors.Is(err, errSkillGone) {
				gone = append(gone, name)
				continue
			}
			unupdated = append(unupdated, map[string]any{"what": "skill:" + name, "error": err.Error()})
			continue
		}
		if changed {
			updated = append(updated, name)
		}
	}
	sort.Strings(updated)
	g.writeLibraryViewExtra(w, map[string]any{"result": map[string]any{
		"updated": updated, "unupdated": unupdated, "gone": gone,
	}})
}

// handleSkillsRegroup POST /api/library/skills/regroup（按 repo 分组由前端完成）
func (g *Gateway) handleSkillsRegroup(w http.ResponseWriter, r *http.Request) {
	g.writeLibraryView(w)
}
