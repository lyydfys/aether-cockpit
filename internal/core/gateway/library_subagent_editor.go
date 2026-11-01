package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/library"
)

// 子代理编辑器（插件 subagents/save）：aether 的模型是
// {name, description, prompt, skills, mcp, agents}，而库里存的是
// AgentEntry{Content 为 prompt}。这里做形状适配，并在保存后把文件
// 下发到选中的 Agent（目前只有 Claude Code 有子代理文件约定）。
// skills/mcp 绑定随条目保存、回显；生成的 md 只含 name/description/tools/model
// 与 prompt 正文（Claude 的约定里没有按子代理绑定技能/服务器的字段）。

type subagentSaveBody struct {
	Old      string `json:"old"`
	Subagent struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Prompt      string   `json:"prompt"`
		Model       string   `json:"model"`
		Tools       string   `json:"tools"`
		Skills      []string `json:"skills"`
		MCP         []string `json:"mcp"`
		Agents      []string `json:"agents"`
	} `json:"subagent"`
}

// handleSubagentSave POST /api/library/subagents/save
func (g *Gateway) handleSubagentSave(w http.ResponseWriter, r *http.Request) {
	var body subagentSaveBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	sub := body.Subagent
	name := strings.TrimSpace(sub.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "名称不能为空"})
		return
	}
	if strings.TrimSpace(sub.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "提示词不能为空"})
		return
	}
	desc := strings.Join(strings.Fields(sub.Description), " ")
	if desc == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "描述不能为空"})
		return
	}

	old := strings.TrimSpace(body.Old)
	var prev library.AgentEntry
	for _, a := range g.library.List().Agents {
		if (old != "" && a.Name == old) || a.Name == name {
			prev = a
			break
		}
	}
	entry := library.AgentEntry{
		Name: name, Description: desc, Content: sub.Prompt,
		Model:  strings.TrimSpace(sub.Model),
		Tools:  strings.TrimSpace(sub.Tools),
		Skills: dedupeStrings(sub.Skills), MCP: dedupeStrings(sub.MCP),
	}
	if prev.ID != "" {
		entry.ID = prev.ID
		entry.CreatedAt = prev.CreatedAt
		if entry.Model == "" {
			entry.Model = prev.Model
		}
		if entry.Tools == "" {
			entry.Tools = prev.Tools
		}
	}
	saved, err := g.library.SaveAgent(entry)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	entry = saved

	// 重命名：先撤掉旧名字在各 Agent 里的文件。
	if prev.ID != "" && prev.Name != name {
		for _, id := range g.library.AppliedAgentAgents(prev.Name) {
			_ = g.library.RemoveAppliedAgent(prev.Name, id)
		}
	}

	want := dedupeStrings(sub.Agents)
	problems := []map[string]any{}
	for _, id := range want {
		if id != "claude_code" {
			problems = append(problems, map[string]any{"agent": id, "error": "该 Agent 不支持子代理文件"})
			continue
		}
		if _, err := g.library.ApplyAgent(entry.ID, "claude"); err != nil {
			problems = append(problems, map[string]any{"agent": id, "error": err.Error()})
		}
	}
	for _, id := range g.library.AppliedAgentAgents(name) {
		if !hasAgent(want, id) {
			_ = g.library.RemoveAppliedAgent(name, id)
		}
	}

	res := map[string]any{"changed": g.library.AppliedAgentAgents(name)}
	if len(problems) > 0 {
		res["problems"] = problems
	}
	g.writeLibraryViewExtra(w, map[string]any{"result": res})
}
