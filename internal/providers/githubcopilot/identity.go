package githubcopilot

// Copilot Chat 扩展的客户端身份常量与按请求变化的能力头。
//
// 取值来源：Copilot Chat 扩展真实抓包（0.35.0 / VS Code 1.107），与社区实现
// （opencode-copilot-auth、openclaw 的 copilot 头模块）一致。
//
// 版本号必须成组更新：UA、Editor-Version、Editor-Plugin-Version 三者互相印证，
// 混搭（例如 UA 0.35 搭配 Editor 1.99）本身就是"伪造客户端"的信号。

import (
	"strings"

	"aether-cockpit/internal/core/ir"
)

const (
	copilotChatVersion   = "0.35.0"
	copilotEditorVersion = "vscode/1.107.0"
	copilotGitHubAPIVers = "2025-04-01"
)

// copilotInitiator 判断这次请求是"用户发起"还是"Agent（工具续轮）发起"。
//
// 官方扩展的判定：最后一条消息是 tool 或 assistant → agent，否则 user。
// 写死任何一边都会与请求体自相矛盾：用户提问却声称 agent，或工具续轮却声称 user。
func copilotInitiator(req *ir.UnifiedChatRequest) string {
	if req == nil || len(req.Items) == 0 {
		return "user"
	}
	last := req.Items[len(req.Items)-1]
	if last.Type == ir.ItemTypeFunctionCallOutput || last.Type == ir.ItemTypeFunctionCall {
		return "agent"
	}
	switch strings.ToLower(strings.TrimSpace(last.Role)) {
	case "tool", "assistant", "model":
		return "agent"
	}
	return "user"
}

// copilotHasImages 判断请求里是否带图片，带图要补 Copilot-Vision-Request。
func copilotHasImages(req *ir.UnifiedChatRequest) bool {
	if req == nil {
		return false
	}
	for _, item := range req.Items {
		for _, att := range item.Attachments {
			if strings.EqualFold(strings.TrimSpace(att.Kind), "image") {
				return true
			}
		}
	}
	return false
}
