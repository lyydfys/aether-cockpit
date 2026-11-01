package gateway

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// 请求身份对照：把下游 Codex 请求的身份头与官方 CLI 的形状逐项比对。
//
// 官方 Codex CLI 的请求有三处可辨认的身份：
//   - User-Agent: codex_cli_rs/<version>
//   - originator: codex_cli_rs
//   - session_id: <uuid>
//
// 中转/第三方客户端往往会缺其中一项或多项。这里只做"观测与对照"，
// 不改写客户端的请求——改写会被上游当成伪造，风险高于收益。

const identityRingSize = 20

// codexIdentity 是一条观测到的下游身份。
type codexIdentity struct {
	Time         int64    `json:"time"`
	UserAgent    string   `json:"user_agent,omitempty"`
	Originator   string   `json:"originator,omitempty"`
	SessionID    string   `json:"session_id,omitempty"`
	AccountID    string   `json:"chatgpt_account_id,omitempty"`
	Beta         string   `json:"openai_beta,omitempty"`
	Stream       bool     `json:"stream"`
	OfficialLike bool     `json:"official_like"`
	Mismatches   []string `json:"mismatches,omitempty"`
}

type identityRing struct {
	mu    sync.Mutex
	items []codexIdentity
}

func (r *identityRing) add(it codexIdentity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, it)
	if len(r.items) > identityRingSize {
		r.items = r.items[len(r.items)-identityRingSize:]
	}
}

func (r *identityRing) snapshot(limit int) []codexIdentity {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 || limit > len(r.items) {
		limit = len(r.items)
	}
	out := make([]codexIdentity, limit)
	copy(out, r.items[len(r.items)-limit:])
	// 最近的在前
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// classifyCodexIdentity 比对身份头与官方 CLI 模板。
func classifyCodexIdentity(ua, originator, sessionID string) (bool, []string) {
	var mismatches []string
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ua)), "codex_cli_rs") {
		mismatches = append(mismatches, "User-Agent 不是 codex_cli_rs/<版本>")
	}
	if strings.TrimSpace(originator) != "codex_cli_rs" {
		mismatches = append(mismatches, "originator 不是 codex_cli_rs")
	}
	if strings.TrimSpace(sessionID) == "" {
		mismatches = append(mismatches, "缺少 session_id")
	}
	return len(mismatches) == 0, mismatches
}

// recordCodexIdentity 记录一次下游身份（只读请求头，不消费请求体）。
func (g *Gateway) recordCodexIdentity(r *http.Request) {
	if g.identities == nil {
		return
	}
	ua := strings.TrimSpace(r.Header.Get("User-Agent"))
	originator := strings.TrimSpace(r.Header.Get("originator"))
	if originator == "" {
		originator = strings.TrimSpace(r.Header.Get("X-Originator"))
	}
	sessionID := strings.TrimSpace(r.Header.Get("session_id"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(r.Header.Get("X-Session-Id"))
	}
	official, mismatches := classifyCodexIdentity(ua, originator, sessionID)
	g.identities.add(codexIdentity{
		Time:         time.Now().Unix(),
		UserAgent:    ua,
		Originator:   originator,
		SessionID:    sessionID,
		AccountID:    strings.TrimSpace(r.Header.Get("chatgpt-account-id")),
		Beta:         strings.TrimSpace(r.Header.Get("OpenAI-Beta")),
		OfficialLike: official,
		Mismatches:   mismatches,
	})
}

// handleCodexIdentity GET /api/providers/codex/identity
func (g *Gateway) handleCodexIdentity(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := parsePositiveInt(v); err == nil && n > 0 && n <= identityRingSize {
			limit = n
		}
	}
	items := []codexIdentity{}
	if g.identities != nil {
		items = g.identities.snapshot(limit)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expected": map[string]any{
			"user_agent": "codex_cli_rs/<version>",
			"originator": "codex_cli_rs",
			"session_id": "UUID",
		},
		"items": items,
		"note":  "仅观测下游请求头，不改写客户端身份。",
	})
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errNotNumber
		}
		n = n*10 + int(c-'0')
		if n > 1<<20 {
			return 0, errNotNumber
		}
	}
	if n == 0 {
		return 0, errNotNumber
	}
	return n, nil
}

var errNotNumber = &identityParseError{}

type identityParseError struct{}

func (*identityParseError) Error() string { return "not a positive integer" }
