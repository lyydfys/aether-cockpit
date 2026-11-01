package sessions

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

// ModelUsage 是一个会话里某个模型的 token 用量。
type ModelUsage struct {
	Model      string
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Requests   int
}

// DayUsage 是一个会话某一天的用量（按本地日期）。
type DayUsage struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Requests   int
	Models     map[string]*ModelUsage
}

// Usage 是一个会话的真实用量（从 Agent 自己的 JSONL 统计）。
//
// Input 统一为“不含缓存”的输入：Claude 的 usage 本来就是这种口径；
// Codex 的 input_tokens 包含 cached_input_tokens，解析时减掉，保证
// 汇总页的输入、缓存、输出可以直接相加。
type Usage struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Messages   int
	ToolCalls  int
	Requests   int
	ActiveSec  int64
	Start      int64
	End        int64
	Models     map[string]*ModelUsage
	Days       map[string]*DayUsage
}

// ActiveGap 是"在工作"的最大间隔：相邻消息超过它就算暂停。
const activeGapSec = 300

// usageFileCap 限制单文件读取量，避免超大会话拖垮页面。
const usageFileCap = 32 << 20

// Analyze 统计一个会话的 token / 消息 / 工具调用 / 活跃时长。
//
// Claude：assistant 消息的 usage 逐条累加（同 message.id 去重）；
// Codex：token_count 的 total_token_usage 是累计值，用相邻两条的差值
// 归到当前模型与当前日期，这样按模型 / 按天都能拆开。
func Analyze(s Session) Usage {
	u := Usage{Models: map[string]*ModelUsage{}, Days: map[string]*DayUsage{}}
	if s.Path == "" {
		u.Start, u.End = s.Last, s.Last
		return u
	}
	f, err := os.Open(s.Path)
	if err != nil {
		u.Start, u.End = s.Last, s.Last
		return u
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	var stamps []int64
	seen := map[string]bool{}
	read := 0
	addStamp := func(ts int64) {
		if ts > 0 {
			stamps = append(stamps, ts)
		}
	}
	// Codex 的 token_count 是累计值；记录上一条累计与当时模型，按差值拆分。
	var codexModel string
	var codexPrevIn, codexPrevCached, codexPrevOut, codexPrevWrite int64

	for sc.Scan() {
		read += len(sc.Bytes())
		if read > usageFileCap {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		switch s.Agent {
		case "claude-code":
			var m struct {
				Type      string `json:"type"`
				Timestamp string `json:"timestamp"`
				UUID      string `json:"uuid"`
				Message   struct {
					ID      string          `json:"id"`
					Model   string          `json:"model"`
					Content json.RawMessage `json:"content"`
					Usage   struct {
						Input         int64 `json:"input_tokens"`
						Output        int64 `json:"output_tokens"`
						CacheRead     int64 `json:"cache_read_input_tokens"`
						CacheCreation int64 `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			ts := parseStamp(m.Timestamp)
			switch m.Type {
			case "user":
				u.Messages++
				addStamp(ts)
			case "assistant":
				id := m.Message.ID
				if id == "" {
					id = m.UUID
				}
				if id != "" && seen[id] {
					break
				}
				if id != "" {
					seen[id] = true
				}
				in := m.Message.Usage.Input
				out := m.Message.Usage.Output
				cr := m.Message.Usage.CacheRead
				cw := m.Message.Usage.CacheCreation
				if in+out+cr+cw > 0 {
					u.add(m.Message.Model, in, out, cr, cw, 1, dayOf(ts, s.Last))
				}
				var parts []struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(m.Message.Content, &parts) == nil {
					for _, c := range parts {
						if c.Type == "tool_use" {
							u.ToolCalls++
						}
					}
				}
				addStamp(ts)
			}
		case "codex":
			var m struct {
				Type      string `json:"type"`
				Timestamp string `json:"timestamp"`
				Payload   struct {
					Type  string `json:"type"`
					Model string `json:"model"`
					Info  *struct {
						Total struct {
							Input       int64 `json:"input_tokens"`
							Output      int64 `json:"output_tokens"`
							CachedInput int64 `json:"cached_input_tokens"`
							CacheWrite  int64 `json:"cache_write_input_tokens"`
						} `json:"total_token_usage"`
					} `json:"info"`
				} `json:"payload"`
			}
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			ts := parseStamp(m.Timestamp)
			switch m.Type {
			case "session_meta", "turn_context":
				if m.Payload.Model != "" {
					codexModel = m.Payload.Model
				}
				addStamp(ts)
			case "event_msg":
				switch m.Payload.Type {
				case "token_count":
					if m.Payload.Info != nil {
						t := m.Payload.Info.Total
						// input_tokens 包含缓存；统一成“不含缓存”的增量。
						nonCached := t.Input - t.CachedInput
						if nonCached < 0 {
							nonCached = 0
						}
						dIn := nonCached - codexPrevIn
						dCached := t.CachedInput - codexPrevCached
						dOut := t.Output - codexPrevOut
						dWrite := t.CacheWrite - codexPrevWrite
						if dIn < 0 {
							dIn = nonCached
						}
						if dCached < 0 {
							dCached = t.CachedInput
						}
						if dOut < 0 {
							dOut = t.Output
						}
						if dWrite < 0 {
							dWrite = t.CacheWrite
						}
						if dIn+dCached+dOut+dWrite > 0 {
							model := codexModel
							if model == "" {
								model = "codex"
							}
							u.add(model, dIn, dOut, dCached, dWrite, 1, dayOf(ts, s.Last))
						}
						codexPrevIn, codexPrevCached = nonCached, t.CachedInput
						codexPrevOut, codexPrevWrite = t.Output, t.CacheWrite
					}
					addStamp(ts)
				case "user_message", "agent_message":
					u.Messages++
					addStamp(ts)
				case "function_call":
					u.ToolCalls++
				}
			}
		}
	}
	if len(stamps) > 0 {
		sort.Slice(stamps, func(i, j int) bool { return stamps[i] < stamps[j] })
		u.Start = stamps[0]
		u.End = stamps[len(stamps)-1]
		for i := 1; i < len(stamps); i++ {
			if gap := stamps[i] - stamps[i-1]; gap > 0 && gap <= activeGapSec {
				u.ActiveSec += gap
			}
		}
	} else {
		u.Start, u.End = s.Last, s.Last
	}
	return u
}

// add 把一次增量累加到总量、模型和当天。
func (u *Usage) add(model string, in, out, cr, cw int64, requests int, day string) {
	if model == "" {
		model = "unknown"
	}
	u.Input += in
	u.Output += out
	u.CacheRead += cr
	u.CacheWrite += cw
	u.Requests += requests

	mu := u.Models[model]
	if mu == nil {
		mu = &ModelUsage{Model: model}
		u.Models[model] = mu
	}
	mu.Input += in
	mu.Output += out
	mu.CacheRead += cr
	mu.CacheWrite += cw
	mu.Requests += requests

	du := u.Days[day]
	if du == nil {
		du = &DayUsage{Models: map[string]*ModelUsage{}}
		u.Days[day] = du
	}
	du.Input += in
	du.Output += out
	du.CacheRead += cr
	du.CacheWrite += cw
	du.Requests += requests
	dmu := du.Models[model]
	if dmu == nil {
		dmu = &ModelUsage{Model: model}
		du.Models[model] = dmu
	}
	dmu.Input += in
	dmu.Output += out
	dmu.CacheRead += cr
	dmu.CacheWrite += cw
	dmu.Requests += requests
}

func dayOf(ts, fallback int64) string {
	if ts == 0 {
		ts = fallback
	}
	if ts == 0 {
		ts = time.Now().Unix()
	}
	return time.Unix(ts, 0).Format("2006-01-02")
}

func parseStamp(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.Unix()
	}
	return 0
}
