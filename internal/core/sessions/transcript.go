package sessions

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// TranscriptPart 是一段可展示的会话内容。
type TranscriptPart struct {
	Role  string `json:"role"`
	Kind  string `json:"kind,omitempty"` // thinking | tool
	Name  string `json:"name,omitempty"`
	Model string `json:"model,omitempty"`
	Text  string `json:"text"`
}

const (
	transcriptMaxParts = 200
	transcriptMaxBytes = 4 << 20 // 只读文件末尾 4MB：几十 MB 的会话不能整读
)

// Transcript 读取某个会话的对话内容（只读 Agent 自己的文件）。
//
// 返回 ok=false 表示本机找不到这个会话文件；cut=true 表示只展示了末尾部分。
func Transcript(agent, id string) (parts []TranscriptPart, cut bool, ok bool) {
	var target *Session
	for _, s := range Scan() {
		if s.Agent == agent && s.ID == id {
			cp := s
			target = &cp
			break
		}
	}
	if target == nil || target.Path == "" {
		return nil, false, false
	}
	f, err := os.Open(target.Path)
	if err != nil {
		return nil, false, false
	}
	defer f.Close()

	if st, err := f.Stat(); err == nil && st.Size() > transcriptMaxBytes {
		_, _ = f.Seek(st.Size()-transcriptMaxBytes, io.SeekStart)
		cut = true
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4<<20)

	if agent == "gemini-cli" {
		parts = transcriptGemini(sc)
	} else {
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || !strings.HasPrefix(line, "{") {
				continue
			}
			parts = append(parts, parseLine(agent, line)...)
		}
	}
	if len(parts) > transcriptMaxParts {
		parts = parts[len(parts)-transcriptMaxParts:]
		cut = true
	}
	return parts, cut, true
}

// transcriptGemini 兼容 Gemini CLI 的两种落盘方式：逐条 record 和
// $set.messages 全量快照。后者以最后一份快照为准，避免整段对话重复。
func transcriptGemini(sc *bufio.Scanner) []TranscriptPart {
	var direct, snapshot []TranscriptPart
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if set, ok := m["$set"].(map[string]any); ok {
			if msgs, ok := set["messages"].([]any); ok {
				snapshot = geminiMessages(msgs)
			}
			continue
		}
		direct = append(direct, geminiLine(m)...)
	}
	if len(snapshot) > 0 {
		return snapshot
	}
	return direct
}

func geminiLine(m map[string]any) []TranscriptPart {
	typ, _ := m["type"].(string)
	switch typ {
	case "user", "gemini", "model":
		role := "user"
		if typ != "user" {
			role = "assistant"
		}
		text := stripGeminiContext(geminiContentText(m["content"]))
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []TranscriptPart{{Role: role, Text: text}}
	case "error":
		text, _ := m["content"].(string)
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []TranscriptPart{{Role: "error", Kind: "error", Text: text}}
	}
	return nil
}

func geminiMessages(msgs []any) []TranscriptPart {
	out := make([]TranscriptPart, 0, len(msgs))
	for _, raw := range msgs {
		m, _ := raw.(map[string]any)
		if m == nil {
			continue
		}
		out = append(out, geminiLine(m)...)
	}
	return out
}

func stripGeminiContext(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "</session_context>"); i >= 0 {
		s = strings.TrimSpace(s[i+len("</session_context>"):])
	}
	return s
}

func parseLine(agent, line string) []TranscriptPart {
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return nil
	}
	switch agent {
	case "claude-code":
		return parseClaude(m)
	case "codex":
		return parseCodex(m)
	}
	return nil
}

func parseClaude(m map[string]any) []TranscriptPart {
	typ, _ := m["type"].(string)
	if typ != "user" && typ != "assistant" {
		return nil
	}
	msg, _ := m["message"].(map[string]any)
	if msg == nil {
		return nil
	}
	role, _ := msg["role"].(string)
	if role == "" {
		role = typ
	}
	model, _ := msg["model"].(string)
	var out []TranscriptPart
	switch content := msg["content"].(type) {
	case string:
		if strings.TrimSpace(content) != "" {
			out = append(out, TranscriptPart{Role: role, Model: model, Text: content})
		}
	case []any:
		for _, raw := range content {
			block, _ := raw.(map[string]any)
			if block == nil {
				continue
			}
			bt, _ := block["type"].(string)
			switch bt {
			case "text":
				text, _ := block["text"].(string)
				if strings.TrimSpace(text) != "" {
					out = append(out, TranscriptPart{Role: role, Model: model, Text: text})
				}
			case "thinking":
				text, _ := block["thinking"].(string)
				if strings.TrimSpace(text) != "" {
					out = append(out, TranscriptPart{Role: role, Kind: "thinking", Model: model, Text: text})
				}
			case "tool_use":
				name, _ := block["name"].(string)
				args, _ := json.MarshalIndent(block["input"], "", "  ")
				out = append(out, TranscriptPart{Role: role, Kind: "tool", Name: name, Model: model, Text: string(args)})
			}
		}
	}
	return out
}

func parseCodex(m map[string]any) []TranscriptPart {
	typ, _ := m["type"].(string)
	payload, _ := m["payload"].(map[string]any)
	if payload == nil {
		return nil
	}
	switch typ {
	case "response_item":
		pt, _ := payload["type"].(string)
		if pt != "message" {
			return nil
		}
		role, _ := payload["role"].(string)
		var out []TranscriptPart
		if blocks, ok := payload["content"].([]any); ok {
			for _, raw := range blocks {
				block, _ := raw.(map[string]any)
				if block == nil {
					continue
				}
				text, _ := block["text"].(string)
				if strings.TrimSpace(text) != "" {
					out = append(out, TranscriptPart{Role: role, Text: text})
				}
			}
		}
		return out
	case "event_msg":
		pt, _ := payload["type"].(string)
		if pt != "agent_message" && pt != "user_message" {
			return nil
		}
		text, _ := payload["message"].(string)
		if strings.TrimSpace(text) == "" {
			return nil
		}
		role := "assistant"
		if pt == "user_message" {
			role = "user"
		}
		return []TranscriptPart{{Role: role, Text: text}}
	}
	return nil
}

// SessionFileExists 报告某会话是否有本机文件（没有时页面不给 resume 按钮）。
func SessionFileExists(agent, id string) bool {
	for _, s := range Scan() {
		if s.Agent == agent && s.ID == id {
			return s.Path != ""
		}
	}
	return false
}
