package gateway

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 用量页「请求」标签 / 托盘统计 / 热力图 / CSV 的端点。

// ledgerRange 把 period（today/7d/30d/all/from..to）与 day 折成日期范围。
func ledgerRange(r *http.Request) (from, to string) {
	today := time.Now()
	days := 0
	switch p := r.URL.Query().Get("period"); p {
	case "today":
		days = 1
	case "7d":
		days = 7
	case "30d":
		days = 30
	case "all", "":
		days = ledgerMaxDays
	default:
		if a, b, ok := strings.Cut(p, ".."); ok {
			from, to = a, b
		} else {
			days = ledgerMaxDays
		}
	}
	if days > 0 {
		from = today.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
		to = today.Format("2006-01-02")
	}
	if d := r.URL.Query().Get("day"); d != "" {
		from, to = d, d
	}
	return from, to
}

func ledgerFilterOf(r *http.Request) ledgerFilter {
	q := r.URL.Query()
	f := ledgerFilter{
		Day:       q.Get("day"),
		Agent:     q.Get("agent"),
		Provider:  q.Get("provider"),
		Account:   q.Get("account"),
		Model:     q.Get("model"),
		Q:         q.Get("q"),
		Purpose:   q.Get("purpose"),
		Failed:    q.Get("failed") == "1",
		Retried:   q.Get("retried") == "1",
		Via:       q.Get("via"),
		CallerKey: q.Get("callerKey"),
		Bucket:    q.Get("bucket"),
	}
	if v := q.Get("route"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			f.RouteID = id
			f.HasRouteFilter = true
		}
	}
	return f
}

func (g *Gateway) emptyLedger() ledgerView {
	return ledgerView{
		Rows: []ledgerRecord{}, Agents: []map[string]any{}, Providers: []map[string]any{},
		Accounts: []map[string]any{}, Purposes: []string{}, CallerKeys: []any{},
		Series: []ledgerSeriesPoint{}, By: map[string][]ledgerRank{}, Computers: []any{},
		Bucket: "day",
	}
}

// handleLedger GET /api/usage/requests
func (g *Gateway) handleLedger(w http.ResponseWriter, r *http.Request) {
	if g.ledger == nil {
		writeJSON(w, http.StatusOK, g.emptyLedger())
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	from, to := ledgerRange(r)
	view := g.ledger.query(ledgerFilterOf(r), offset, limit, from, to, g.ledgerPrice)
	writeJSON(w, http.StatusOK, view)
}

// handleLedgerHeatmap GET /api/usage/heatmap
func (g *Gateway) handleLedgerHeatmap(w http.ResponseWriter, r *http.Request) {
	if g.ledger == nil {
		writeJSON(w, http.StatusOK, map[string]any{"from": "", "to": "", "days": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, g.ledger.heatmap(ledgerFilterOf(r), g.ledgerPrice))
}

// handleLedgerCSV GET /api/usage/requests.csv
func (g *Gateway) handleLedgerCSV(w http.ResponseWriter, r *http.Request) {
	if g.ledger == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	from, to := ledgerRange(r)
	data := g.ledger.csv(ledgerFilterOf(r), from, to, g.ledgerPrice)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="aether-requests-`+time.Now().Format("20060102-150405")+`.csv"`)
	_, _ = w.Write(data)
}

// handleLedgerExport POST /api/usage/requests/export —— 桌面版落到 Downloads。
func (g *Gateway) handleLedgerExport(w http.ResponseWriter, r *http.Request) {
	if g.ledger == nil {
		writeJSON(w, http.StatusOK, map[string]any{"path": ""})
		return
	}
	from, to := ledgerRange(r)
	data := g.ledger.csv(ledgerFilterOf(r), from, to, g.ledgerPrice)
	dir := ""
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, "Downloads")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			dir = candidate
		}
	}
	if dir == "" {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("aether-requests-%s.csv", time.Now().Format("20060102-150405")))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "写入失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "bytes": len(data)})
}

// handleLedgerContent GET /api/usage/requests/content
//
// 网关不保存请求/回复正文（隐私优先）；这里如实说明无法从会话文件找到。
func (g *Gateway) handleLedgerContent(w http.ResponseWriter, r *http.Request) {
	why := "missing"
	if r.URL.Query().Get("session") == "" {
		why = "session"
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": false, "why": why})
}
