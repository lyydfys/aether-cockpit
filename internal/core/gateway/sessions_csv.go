package gateway

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// sessionsUsageCSV 把会话用量视图导出为 CSV（含模型列表）。
func sessionsUsageCSV(views []sessionUsageView) (string, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{
		"agent", "id", "name", "title", "cwd", "started_at", "last_at",
		"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens",
		"cost_usd", "active_seconds", "messages", "tool_calls", "models",
	})
	for _, v := range views {
		models := make([]string, 0, len(v.Models))
		for _, m := range v.Models {
			models = append(models, m.Model)
		}
		_ = w.Write([]string{
			v.Agent, v.ID, v.Name, v.Title, v.Cwd,
			time.UnixMilli(v.Start).Format(time.RFC3339),
			time.UnixMilli(v.Last).Format(time.RFC3339),
			strconv.FormatInt(v.Input, 10),
			strconv.FormatInt(v.Output, 10),
			strconv.FormatInt(v.CacheRead, 10),
			strconv.FormatInt(v.CacheWrite, 10),
			fmt.Sprintf("%.6f", v.Cost),
			strconv.FormatInt(v.Active, 10),
			strconv.Itoa(v.Messages),
			strconv.Itoa(v.ToolCalls),
			strings.Join(models, ";"),
		})
	}
	w.Flush()
	return b.String(), w.Error()
}

// handleSessionsUsageCSV GET /api/sessions/usage.csv?agent=&days=&limit=
func (g *Gateway) handleSessionsUsageCSV(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	agent := strings.TrimSpace(q.Get("agent"))
	days := 0
	if v := q.Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	limit := 5000
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20000 {
			limit = n
		}
	}
	var cutoff int64
	if days > 0 {
		cutoff = time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	}
	views := g.allSessionViews()
	out := make([]sessionUsageView, 0, len(views))
	for _, v := range views {
		if agent != "" && v.Agent != agent {
			continue
		}
		if cutoff > 0 && v.Last < cutoff {
			continue
		}
		out = append(out, v)
		if len(out) >= limit {
			break
		}
	}
	text, err := sessionsUsageCSV(out)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="aether-sessions-%s.csv"`, time.Now().Format("20060102-150405")))
	_, _ = w.Write([]byte(text))
}
