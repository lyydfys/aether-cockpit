package sessions

import (
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// Hit 是一段命中上下文（一句话的左右各若干字）。
type Hit struct {
	ID     string `json:"id"`
	Before string `json:"before"`
	Match  string `json:"match"`
	After  string `json:"after"`
}

const (
	searchTailBytes  = 1 << 20 // 每个会话只查末尾 1MB：整体几十 MB 不能全读
	searchMaxHits    = 100
	searchContextLen = 48
	// searchMaxSessions 限制一次搜索读取的会话数，超出则报告 cut。
	searchMaxSessions = 80
)

// Search 在某个 Agent 的会话文件里找关键词，每个会话最多一条命中。
func Search(agent, needle string) (hits []Hit, searched, total int, cut bool) {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return nil, 0, 0, false
	}
	lower := strings.ToLower(needle)

	var mine []Session
	for _, s := range Scan() {
		if s.Agent == agent && s.Path != "" {
			mine = append(mine, s)
		}
	}
	total = len(mine)
	if len(mine) > searchMaxSessions {
		mine = mine[:searchMaxSessions]
		cut = true
	}
	searched = len(mine)

	for _, s := range mine {
		if text, ok := readTail(s.Path, searchTailBytes); ok {
			if h, found := hitIn(s.ID, text, lower); found {
				hits = append(hits, h)
				if len(hits) >= searchMaxHits {
					return hits, searched, total, true
				}
			}
		}
	}
	return hits, searched, total, cut
}

func readTail(path string, n int64) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > n {
		_, _ = f.Seek(st.Size()-n, io.SeekStart)
	}
	b, err := io.ReadAll(io.LimitReader(f, n))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// hitIn 找关键词并把左右各截 searchContextLen 个字符。
func hitIn(id, text, lowerNeedle string) (Hit, bool) {
	idx := strings.Index(strings.ToLower(text), lowerNeedle)
	if idx < 0 {
		return Hit{}, false
	}
	// 命中位置对齐到 rune 边界，避免截断 UTF-8
	for idx > 0 && !utf8.RuneStart(text[idx]) {
		idx++
	}
	start := idx - searchContextLen
	for start > 0 && !utf8.RuneStart(text[start]) {
		start++
	}
	if start < 0 {
		start = 0
	}
	end := idx + len(lowerNeedle) + searchContextLen
	if end > len(text) {
		end = len(text)
	}
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	return Hit{
		ID:     id,
		Before: clean(text[start:idx]),
		Match:  text[idx : idx+len(lowerNeedle)],
		After:  clean(text[idx+len(lowerNeedle) : end]),
	}, true
}

// clean 把 JSONL 的转义与换行压成一行，避免命中行难以阅读。
func clean(s string) string {
	r := strings.NewReplacer("\\n", " ", "\\t", " ", "\n", " ", "\t", " ", "\\\"", "\"", "\\u0026", "&", "\\u003e", ">", "\\u003c", "<")
	return strings.TrimSpace(r.Replace(s))
}

// Markdown 把会话渲染成 Markdown 导出文件。
func Markdown(agent, id string) (string, bool) {
	parts, _, ok := Transcript(agent, id)
	if !ok {
		return "", false
	}
	var b strings.Builder
	b.WriteString("# " + agent + " · " + id + "\n\n")
	for _, p := range parts {
		label := "User"
		if p.Role == "assistant" {
			label = "Assistant"
		}
		if p.Kind == "thinking" {
			label = "Thinking"
		} else if p.Kind == "tool" {
			label = "Tool" + func() string {
				if p.Name != "" {
					return " · " + p.Name
				}
				return ""
			}()
		}
		if p.Model != "" {
			label += " (" + p.Model + ")"
		}
		b.WriteString("## " + label + "\n\n" + p.Text + "\n\n")
	}
	return b.String(), true
}
