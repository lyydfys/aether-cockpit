package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 逐请求账本 —— 用量页「请求」标签与托盘统计的数据源。
//
// 与按天聚合的 usageStore 互补：这里保留每个请求一行（谁、哪个模型、
// 走了哪个渠道/账号、token、成本、耗时、状态、重试次数、路由轨迹 id），
// 按天落成 JSONL，保留 30 天。请求体与回复正文 **不保存**（隐私），
// 所以「查看内容」会如实返回 not_kept。

const (
	ledgerMaxDays  = 30
	ledgerMaxDay   = 5000 // 单日内存上限，超出丢最旧（文件仍保留）
	ledgerPageMax  = 500
	defaultLedPage = 100
)

// ledgerRecord 是一行请求。JSON 字段名对齐 aether 用量页。
type ledgerRecord struct {
	ID            string  `json:"id"`
	T             int64   `json:"t"`
	Day           string  `json:"day"`
	Agent         string  `json:"agent"`
	AgentName     string  `json:"agentName,omitempty"`
	Icon          string  `json:"icon,omitempty"`
	Kind          string  `json:"kind,omitempty"`
	Session       string  `json:"session,omitempty"`
	Req           string  `json:"req,omitempty"`
	Model         string  `json:"model,omitempty"`
	ProviderID    string  `json:"providerId,omitempty"`
	ProviderName  string  `json:"providerName,omitempty"`
	Host          string  `json:"host,omitempty"`
	Access        string  `json:"access,omitempty"`
	AccountID     string  `json:"accountId,omitempty"`
	AccountName   string  `json:"accountName,omitempty"`
	In            int64   `json:"in"`
	Out           int64   `json:"out"`
	Reasoning     int64   `json:"reasoning,omitempty"`
	CacheRead     int64   `json:"cache_read,omitempty"`
	CacheWrite    int64   `json:"cache_write,omitempty"`
	Cost          float64 `json:"cost"`
	Priced        bool    `json:"priced"`
	PricingModel  string  `json:"pricing_model,omitempty"`
	TTFT          int64   `json:"ttft_ms,omitempty"`
	Ms            int64   `json:"ms,omitempty"`
	Status        int     `json:"status,omitempty"`
	ErrType       string  `json:"err_type,omitempty"`
	Err           string  `json:"err,omitempty"`
	Tries         int     `json:"tries,omitempty"`
	HandedOn      bool    `json:"handed_on,omitempty"`
	Retried       bool    `json:"retried,omitempty"`
	Stream        bool    `json:"stream,omitempty"`
	Items         int     `json:"items,omitempty"`
	PreviousID    bool    `json:"previous_id,omitempty"`
	AutoContinued bool    `json:"auto_continued,omitempty"`
	TrimmedItems  int     `json:"trimmed_items,omitempty"`
	// EstTokens 是请求发出前对输入 token 的粗估；UpstreamTruncated 表示
	// 上游实际处理的量远小于它（上游静默截断上下文）。两者配合能把"模型突然
	// 失忆、开始复读"直接归因到上游截断，而不是让人对着输出猜。
	EstTokens         int64  `json:"est_tokens,omitempty"`
	UpstreamTruncated bool   `json:"upstream_truncated,omitempty"`
	Source            string `json:"source,omitempty"`
	RouteID           int64  `json:"route_id,omitempty"`
	Purpose           string `json:"purpose,omitempty"`
	FastPath          bool   `json:"fast_path,omitempty"`
	CallerKey         string `json:"callerKey,omitempty"`
	CallerKeyName     string `json:"callerKeyName,omitempty"`
}

type ledgerStore struct {
	mu      sync.Mutex
	dir     string
	seq     int64
	days    map[string][]ledgerRecord // 内存里按天
	file    *os.File                  // 当天的追加句柄
	fileDay string
}

func newLedgerStore(dir string) *ledgerStore {
	s := &ledgerStore{dir: dir, days: make(map[string][]ledgerRecord, 8)}
	s.prune()
	s.loadDay(usageDate(time.Now()))
	return s
}

func (s *ledgerStore) path(day string) string {
	return filepath.Join(s.dir, day+".jsonl")
}

func (s *ledgerStore) prune() {
	if s.dir == "" {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -ledgerMaxDays).Format("2006-01-02")
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		if strings.TrimSuffix(name, ".jsonl") < cutoff {
			_ = os.Remove(filepath.Join(s.dir, name))
		}
	}
}

func (s *ledgerStore) loadDay(day string) {
	if s.dir == "" {
		return
	}
	f, err := os.Open(s.path(day))
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	list := make([]ledgerRecord, 0, 256)
	for sc.Scan() {
		var rec ledgerRecord
		if json.Unmarshal(sc.Bytes(), &rec) == nil {
			list = append(list, rec)
		}
	}
	if len(list) > ledgerMaxDay {
		list = list[len(list)-ledgerMaxDay:]
	}
	s.days[day] = list
}

// add 记录一个请求（调用方不持有锁）。
func (s *ledgerStore) add(rec ledgerRecord) {
	if s == nil || s.dir == "" {
		return
	}
	if rec.T == 0 {
		rec.T = time.Now().UnixMilli()
	}
	rec.Day = time.UnixMilli(rec.T).Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	rec.ID = fmt.Sprintf("%d-%d", rec.T, s.seq)
	if rec.Agent != "" && rec.AgentName == "" {
		rec.AgentName = clientAgentName(rec.Agent)
	}
	list := append(s.days[rec.Day], rec)
	if len(list) > ledgerMaxDay {
		list = list[len(list)-ledgerMaxDay:]
	}
	s.days[rec.Day] = list

	// 追加写文件：按天打开一次句柄，跨天自动切换。
	if s.file == nil || s.fileDay != rec.Day {
		if s.file != nil {
			_ = s.file.Close()
		}
		_ = os.MkdirAll(s.dir, 0o700)
		f, err := os.OpenFile(s.path(rec.Day), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			s.file = nil
			return
		}
		s.file, s.fileDay = f, rec.Day
	}
	if b, err := json.Marshal(rec); err == nil {
		_, _ = s.file.Write(append(b, '\n'))
	}
}

// Close 关闭当天文件的追加句柄（测试与优雅退出用）。
func (s *ledgerStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		err := s.file.Close()
		s.file = nil
		return err
	}
	return nil
}

// ledgerFilter 是一组筛选条件。
type ledgerFilter struct {
	From, To       string
	Day            string
	Agent          string
	Provider       string
	ProviderName   string
	Account        string
	Model          string
	Q              string
	Purpose        string
	Failed         bool
	Retried        bool
	Via            string
	CallerKey      string
	RouteID        int64
	HasRouteFilter bool
	// Bucket 只影响时间序列粒度（day/hour），不参与逐行过滤。
	Bucket string
}

func (f ledgerFilter) match(r ledgerRecord) bool {
	if f.Day != "" && r.Day != f.Day {
		return false
	}
	if f.Agent != "" && r.Agent != f.Agent {
		return false
	}
	if f.Provider != "" && r.ProviderID != f.Provider && r.ProviderName != f.Provider {
		return false
	}
	if f.Account != "" && r.AccountID != f.Account {
		return false
	}
	if f.Model != "" && r.Model != f.Model && r.Req != f.Model {
		return false
	}
	if f.Q != "" {
		q := strings.ToLower(f.Q)
		hay := strings.ToLower(r.Model + " " + r.Req + " " + r.ProviderName + " " + r.AccountName + " " + r.Session)
		if !strings.Contains(hay, q) {
			return false
		}
	}
	if f.Failed && r.Status < 400 {
		return false
	}
	if f.Retried && !r.Retried {
		return false
	}
	if f.HasRouteFilter && r.RouteID != f.RouteID {
		return false
	}
	if f.CallerKey != "" && f.CallerKey != "none" && r.CallerKey != f.CallerKey {
		return false
	}
	if f.CallerKey == "none" && r.CallerKey != "" {
		return false
	}
	return true
}

// records 取日期范围内的记录（按时间升序）。
func (s *ledgerStore) records(from, to string) []ledgerRecord {
	if s == nil || s.dir == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 补齐范围内未加载的天（内存里通常只有今天）。
	if from != "" && to != "" {
		for d := from; d <= to; {
			if _, ok := s.days[d]; !ok {
				s.loadDay(d)
			}
			t, err := time.Parse("2006-01-02", d)
			if err != nil {
				break
			}
			d = t.AddDate(0, 0, 1).Format("2006-01-02")
		}
	}
	out := make([]ledgerRecord, 0, 1024)
	for day, list := range s.days {
		if from != "" && day < from {
			continue
		}
		if to != "" && day > to {
			continue
		}
		out = append(out, list...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}

// earliestDay 返回账本目录里最早的一天（"2006-01-02"），空表示还没有账本。
// 周期起点早于它时，说明那段时间的调用没有记录，holds 不能假装完整。
func (s *ledgerStore) earliestDay() string {
	if s == nil || s.dir == "" {
		return ""
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return ""
	}
	min := ""
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".jsonl")
		if len(name) != len("2006-01-02") || name[4] != '-' || name[7] != '-' {
			continue
		}
		if min == "" || name < min {
			min = name
		}
	}
	return min
}

// LedgerTotals 是汇总数值（aether 的 totals 形状）。
type LedgerTotals struct {
	Calls        int64   `json:"calls"`
	Errors       int64   `json:"errors"`
	Retried      int64   `json:"retried"`
	Input        int64   `json:"input"`
	Output       int64   `json:"output"`
	CacheRead    int64   `json:"cache_read"`
	CacheWrite   int64   `json:"cache_write"`
	Reasoning    int64   `json:"reasoning"`
	Cost         float64 `json:"cost"`
	Unpriced     int64   `json:"unpriced"`
	Timed        int64   `json:"timed"`
	TTFTMs       int64   `json:"ttft_ms"`
	DecodeMS     int64   `json:"decode_ms"`
	DecodeOut    int64   `json:"decode_out"`
	CacheUnknown int64   `json:"cache_unknown"`
}

func addTotals(t *LedgerTotals, r ledgerRecord) {
	t.Calls++
	if r.Status >= 400 {
		t.Errors++
	}
	if r.Retried {
		t.Retried++
	}
	t.Input += r.In
	t.Output += r.Out
	t.CacheRead += r.CacheRead
	t.CacheWrite += r.CacheWrite
	t.Reasoning += r.Reasoning
	t.Cost += r.Cost
	if !r.Priced {
		t.Unpriced++
	}
	if r.TTFT > 0 {
		t.Timed++
		t.TTFTMs += r.TTFT
	}
	if r.Ms > r.TTFT && r.Out > 0 {
		t.DecodeMS += r.Ms - r.TTFT
		t.DecodeOut += r.Out
	}
}

// ledgerRank 是排行榜的一行。
type ledgerRank struct {
	ID         string  `json:"id"`
	Name       string  `json:"name,omitempty"`
	Icon       string  `json:"icon,omitempty"`
	Calls      int64   `json:"calls"`
	Errors     int64   `json:"errors"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cache_read"`
	CacheWrite int64   `json:"cache_write"`
	Reasoning  int64   `json:"reasoning"`
	Cost       float64 `json:"cost"`
	TTFTMs     int64   `json:"ttft_ms"`
	Timed      int64   `json:"timed"`
}

// ledgerSeriesPoint 是按天的时间轴点。
type ledgerSeriesPoint struct {
	// 时间轴点用 ISO 日期字符串：前端按 p.time.slice(0,10) 对应到天。
	Time       string  `json:"time"`
	Label      string  `json:"label"`
	Calls      int64   `json:"calls"`
	Errors     int64   `json:"errors"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cache_read"`
	CacheWrite int64   `json:"cache_write"`
	Reasoning  int64   `json:"reasoning"`
	Cost       float64 `json:"cost"`
}

// ledgerView 是 /api/usage/requests 的响应。
// ledgerChartSeries 是 chartBy.model 里的一条：按模型拆开的逐日序列。
// 前端用它画堆叠面积（哪个模型在吃额度），所以点数只给调用与成本，不带 token 明细。
type ledgerChartSeries struct {
	Name   string             `json:"name"`
	Points []ledgerChartPoint `json:"points"`
}

type ledgerChartPoint struct {
	T string  `json:"t"`
	C int64   `json:"c"`
	M float64 `json:"m"`
}

type ledgerView struct {
	Rows   []ledgerRecord `json:"rows"`
	Offset int            `json:"offset"`
	Limit  int            `json:"limit"`
	Total  int64          `json:"total"`
	Bucket string         `json:"bucket"`
	AvgMS  int64          `json:"avg_ms,omitempty"`
	P95MS  int64          `json:"p95_ms,omitempty"`
	LedgerTotals
	Agents     []map[string]any        `json:"agents"`
	Providers  []map[string]any        `json:"providers"`
	Accounts   []map[string]any        `json:"accounts"`
	Purposes   []string                `json:"purposes"`
	CallerKeys []any                   `json:"callerKeys"`
	Series     []ledgerSeriesPoint     `json:"series"`
	By         map[string][]ledgerRank `json:"by"`
	Through    LedgerTotals            `json:"through"`
	Direct     LedgerTotals            `json:"direct"`
	Reported   LedgerTotals            `json:"reported"`
	Computers  []any                   `json:"computers"`
	ChartBy    any                     `json:"chartBy"`
}

// query 按条件分页并汇总。
func (s *ledgerStore) query(f ledgerFilter, offset, limit int, from, to string, price func(ledgerRecord) (float64, bool)) ledgerView {
	all := s.records(from, to)
	if limit <= 0 {
		limit = defaultLedPage
	}
	if limit > ledgerPageMax {
		limit = ledgerPageMax
	}
	if offset < 0 {
		offset = 0
	}
	v := ledgerView{
		Rows: []ledgerRecord{}, Offset: offset, Limit: limit, Bucket: "day",
		Agents: []map[string]any{}, Providers: []map[string]any{}, Accounts: []map[string]any{},
		Purposes: []string{}, CallerKeys: []any{}, Series: []ledgerSeriesPoint{},
		By: map[string][]ledgerRank{}, Computers: []any{},
	}
	agentSeen := map[string]bool{}
	providerSeen := map[string]bool{}
	accountSeen := map[string]bool{}
	byAgent := map[string]*ledgerRank{}
	byModel := map[string]*ledgerRank{}
	byProvider := map[string]*ledgerRank{}
	byAccount := map[string]*ledgerRank{}
	hourly := strings.EqualFold(f.Bucket, "hour")
	if hourly {
		v.Bucket = "hour"
	}
	bucketOf := func(r ledgerRecord) (key, label string) {
		if hourly {
			t := time.UnixMilli(r.T).In(time.Local)
			return t.Format("2006-01-02T15"), t.Format("15:00")
		}
		t, _ := time.ParseInLocation("2006-01-02", r.Day, time.Local)
		return t.Format("2006-01-02"), t.Format("Jan 2")
	}
	byDay := map[string]*ledgerSeriesPoint{}
	byModelDay := map[string]map[string]*ledgerChartPoint{}
	byKey := map[string]*ledgerRank{}
	callerSeen := map[string]bool{}

	matched := make([]ledgerRecord, 0, len(all))
	for _, r := range all {
		if !f.match(r) {
			continue
		}
		matched = append(matched, r)
	}
	v.Total = int64(len(matched))
	for _, r := range matched {
		if price != nil {
			if c, ok := price(r); ok {
				r.Cost = c
				r.Priced = true
				r.PricingModel = r.Model
			}
		}
		addTotals(&v.LedgerTotals, r)
		addTotals(&v.Through, r)
		if !agentSeen[r.Agent] && r.Agent != "" {
			agentSeen[r.Agent] = true
			v.Agents = append(v.Agents, map[string]any{"id": r.Agent, "name": clientAgentName(r.Agent), "icon": "generic"})
		}
		if !providerSeen[r.ProviderID] && r.ProviderID != "" {
			providerSeen[r.ProviderID] = true
			v.Providers = append(v.Providers, map[string]any{"id": r.ProviderID, "name": r.ProviderName})
		}
		if !accountSeen[r.AccountID] && r.AccountID != "" {
			accountSeen[r.AccountID] = true
			v.Accounts = append(v.Accounts, map[string]any{"id": r.AccountID, "name": r.AccountName, "providers": []string{r.ProviderName}})
		}
		bumpRank(byAgent, r.Agent, func() *ledgerRank {
			return &ledgerRank{ID: r.Agent, Name: clientAgentName(r.Agent), Icon: "generic"}
		}, r)
		bumpRank(byModel, r.Model, func() *ledgerRank { return &ledgerRank{ID: r.Model, Name: r.Model} }, r)
		bumpRank(byProvider, r.ProviderID, func() *ledgerRank {
			return &ledgerRank{ID: r.ProviderID, Name: r.ProviderName, Icon: "generic"}
		}, r)
		bumpRank(byAccount, r.AccountID, func() *ledgerRank {
			return &ledgerRank{ID: r.AccountID, Name: r.AccountName}
		}, r)
		if r.CallerKey != "" {
			if !callerSeen[r.CallerKey] {
				callerSeen[r.CallerKey] = true
				v.CallerKeys = append(v.CallerKeys, map[string]any{"id": r.CallerKey, "name": r.CallerKeyName})
			}
			bumpRank(byKey, r.CallerKey, func() *ledgerRank {
				return &ledgerRank{ID: r.CallerKey, Name: r.CallerKeyName, Icon: "key"}
			}, r)
		}
		key, label := bucketOf(r)
		if r.Model != "" {
			md, ok := byModelDay[r.Model]
			if !ok {
				md = map[string]*ledgerChartPoint{}
				byModelDay[r.Model] = md
			}
			mp, ok := md[key]
			if !ok {
				mp = &ledgerChartPoint{T: key}
				md[key] = mp
			}
			mp.C++
			mp.M += r.Cost
		}
		pt, ok := byDay[key]
		if !ok {
			pt = &ledgerSeriesPoint{Time: key, Label: label}
			byDay[key] = pt
		}
		pt.Calls++
		if r.Status >= 400 {
			pt.Errors++
		}
		pt.Input += r.In
		pt.Output += r.Out
		pt.CacheRead += r.CacheRead
		pt.CacheWrite += r.CacheWrite
		pt.Reasoning += r.Reasoning
		pt.Cost += r.Cost
	}

	if len(matched) > 0 {
		durs := make([]int64, 0, len(matched))
		var sum int64
		for _, r := range matched {
			durs = append(durs, r.Ms)
			sum += r.Ms
		}
		v.AvgMS = sum / int64(len(durs))
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		idx := (len(durs)*95+99)/100 - 1
		if idx >= len(durs) {
			idx = len(durs) - 1
		}
		v.P95MS = durs[idx]
	}

	// 最新在前分页
	sort.Slice(matched, func(i, j int) bool { return matched[i].T > matched[j].T })
	end := offset + limit
	if offset < len(matched) {
		if end > len(matched) {
			end = len(matched)
		}
		v.Rows = matched[offset:end]
	}
	for _, pt := range byDay {
		v.Series = append(v.Series, *pt)
	}
	sort.Slice(v.Series, func(i, j int) bool { return v.Series[i].Time < v.Series[j].Time })
	v.By["agent"] = rankList(byAgent)
	v.By["model"] = rankList(byModel)
	v.By["provider"] = rankList(byProvider)
	v.By["account"] = rankList(byAccount)
	v.By["callerKey"] = rankList(byKey)
	v.ChartBy = map[string]any{"model": chartByModel(byModelDay, byModel, v.Series)}
	return v
}

// chartByModel 取调用量前 5 的模型逐日序列，其余并成「其他」。
// 天数与 v.Series 对齐，前端可以直接按日期对表。
func chartByModel(byModelDay map[string]map[string]*ledgerChartPoint, byModel map[string]*ledgerRank, days []ledgerSeriesPoint) []ledgerChartSeries {
	order := rankList(byModel)
	if len(order) == 0 {
		return []ledgerChartSeries{}
	}
	const topN = 5
	out := make([]ledgerChartSeries, 0, topN+1)
	var others *ledgerChartSeries
	for i, r := range order {
		if i < topN {
			series := ledgerChartSeries{Name: r.ID, Points: make([]ledgerChartPoint, 0, len(days))}
			for _, d := range days {
				pt := ledgerChartPoint{T: d.Time}
				if p, ok := byModelDay[r.ID][d.Time]; ok {
					pt.C = p.C
					pt.M = p.M
				}
				series.Points = append(series.Points, pt)
			}
			out = append(out, series)
			continue
		}
		// 第 5 名及以后累加进同一个「其他」，按天归位而不是往后追加
		if others == nil {
			others = &ledgerChartSeries{Name: "其他", Points: make([]ledgerChartPoint, len(days))}
			for j, d := range days {
				others.Points[j].T = d.Time
			}
		}
		for j, d := range days {
			if p, ok := byModelDay[r.ID][d.Time]; ok {
				others.Points[j].C += p.C
				others.Points[j].M += p.M
			}
		}
	}
	if others != nil {
		out = append(out, *others)
	}
	return out
}

func bumpRank(m map[string]*ledgerRank, key string, init func() *ledgerRank, r ledgerRecord) {
	if key == "" {
		return
	}
	x, ok := m[key]
	if !ok {
		x = init()
		m[key] = x
	}
	x.Calls++
	if r.Status >= 400 {
		x.Errors++
	}
	x.Input += r.In
	x.Output += r.Out
	x.CacheRead += r.CacheRead
	x.CacheWrite += r.CacheWrite
	x.Reasoning += r.Reasoning
	x.Cost += r.Cost
	if r.TTFT > 0 {
		x.Timed++
		x.TTFTMs += r.TTFT
	}
}

func rankList(m map[string]*ledgerRank) []ledgerRank {
	out := make([]ledgerRank, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Calls > out[j].Calls })
	return out
}

// -- 账本记录装配 --

// ledgerPending 在请求生命周期内收集字段，最终一次性写入账本。
type ledgerPending struct {
	rec ledgerRecord
	mu  sync.Mutex
}

func (g *Gateway) newLedgerPending(agent, requestedModel, session string, trace *traceRoute) *ledgerPending {
	if g.ledger == nil {
		return nil
	}
	p := &ledgerPending{rec: ledgerRecord{
		T: time.Now().UnixMilli(), Agent: agent, Req: requestedModel,
		Session: shortSession(session), Source: "gateway",
	}}
	if trace != nil {
		p.rec.RouteID = trace.ID
	}
	return p
}

func shortSession(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// noteTry 由 withUsageRecorder 在流结束时回填 token 与耗时。
// noteEstimate 记录请求发出前的输入 token 粗估，供事后判断上游是否截断。
func (p *ledgerPending) noteEstimate(tokens int64) {
	if p == nil || tokens <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rec.EstTokens = tokens
}

func (p *ledgerPending) noteUsage(u *ir.Usage, ttftMS, ms int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if u != nil {
		p.rec.In = int64(u.InputTokens)
		p.rec.Out = int64(u.OutputTokens)
		p.rec.Reasoning = int64(u.ReasoningTokens)
		p.rec.CacheRead = int64(u.CachedTokens)
		p.rec.CacheWrite = int64(u.CacheCreationTokens)
		// 上游静默截断：估算 36 万 token，上游只认 1381，模型其实只看到
		// 最后几条碎片。差一倍以上就标记，供请求明细一眼看出来。
		if actual := p.rec.In + p.rec.CacheRead; p.rec.EstTokens > 5000 && actual*2 < p.rec.EstTokens {
			p.rec.UpstreamTruncated = true
		}
	}
	p.rec.TTFT = ttftMS
	p.rec.Ms = ms
}

// finalize 补齐渠道/账号/价格/状态并写入。
func (p *ledgerPending) finalize(g *Gateway, entry *RequestLog, trace *traceRoute, status int, errType, errMsg string) {
	if p == nil || g.ledger == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.rec
	if r.Ms == 0 {
		r.Ms = entry.DurationMS
	}
	if r.TTFT == 0 {
		r.TTFT = entry.FirstByteMS
	}
	if r.Model == "" {
		r.Model = entry.Model
	}
	if r.Req == "" {
		r.Req = entry.Model
	}
	r.Stream = entry.Stream
	r.Items = entry.Items
	r.PreviousID = entry.PreviousID
	r.AutoContinued = entry.AutoContinued
	r.TrimmedItems = entry.TrimmedItems
	if entry.FastPath {
		r.FastPath = true
	}
	r.ProviderID = entry.ProviderID
	if p, ok := provider.Get(entry.ProviderID); ok {
		m := p.Manifest()
		r.ProviderName = m.DisplayName
		if len(m.SupportedAuths) > 0 {
			for _, a := range m.SupportedAuths {
				if a == provider.AuthOAuthPKCE || a == provider.AuthLocalImport {
					r.Access = "subscription"
				}
			}
		}
	}
	if r.Host == "" && entry.ProviderID != "" {
		r.Host = hostOf(g.providerBaseURL(entry.ProviderID))
	}
	if g.pool != nil {
		if env, ok := g.pool.Get(entry.AccountID); ok {
			r.AccountID = env.AccountID
			r.AccountName = env.Label
		}
	}
	if r.Status == 0 {
		r.Status = status
	}
	if r.ErrType == "" {
		r.ErrType = errType
	}
	if r.Err == "" {
		r.Err = errMsg
	}
	if trace != nil {
		r.RouteID = trace.ID
		s := trace.snapshot()
		r.Tries = len(s.Tries)
		if len(s.Tries) > 1 {
			r.Retried = s.Status < 400
			r.HandedOn = s.Status >= 400
		}
	}
	// 成本按当前价格表估算（失败请求也计 token，若上游报过）。
	if price, ok := g.priceForUsage(r.Model, r.In+r.CacheRead+r.CacheWrite, time.UnixMilli(r.T)); ok && r.Model != "" {
		r.Cost = costOf(price, r.In, r.Out, r.CacheRead, r.CacheWrite)
		if rate := g.providerPriceRate(r.ProviderID); rate != 1 {
			r.Cost *= rate
		}
		r.Priced = true
		r.PricingModel = r.Model
	}
	// 请求日志与账本共用一份 token/费用口径。
	entry.InputTokens = r.In
	entry.OutputTokens = r.Out
	entry.CacheRead = r.CacheRead
	entry.CacheWrite = r.CacheWrite
	entry.ReasoningTokens = r.Reasoning
	entry.Cost = r.Cost
	entry.Priced = r.Priced
	entry.PricingModel = r.PricingModel
	g.ledger.add(r)
}

// snapshot 返回轨迹的副本（避免读写竞态）。
func (t *traceRoute) snapshot() traceRoute {
	if t == nil || t.store == nil {
		return traceRoute{}
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	cp := *t
	cp.Tries = append([]traceTry(nil), t.Tries...)
	cp.Order = append([]traceSeat(nil), t.Order...)
	cp.Left = append([]traceSeat(nil), t.Left...)
	return cp
}

// heatmap 返回 53 周日历数据（只列有请求的天）。
func (s *ledgerStore) heatmap(f ledgerFilter, price func(ledgerRecord) (float64, bool)) map[string]any {
	to := time.Now()
	from := to.AddDate(0, 0, -370)
	all := s.records(from.Format("2006-01-02"), to.Format("2006-01-02"))
	type dayVal struct {
		Date   string  `json:"date"`
		Calls  int64   `json:"calls"`
		Tokens int64   `json:"tokens"`
		Cost   float64 `json:"cost"`
	}
	byDay := map[string]*dayVal{}
	for _, r := range all {
		if !f.match(r) {
			continue
		}
		if price != nil {
			if c, ok := price(r); ok {
				r.Cost = c
			}
		}
		d, ok := byDay[r.Day]
		if !ok {
			d = &dayVal{Date: r.Day}
			byDay[r.Day] = d
		}
		d.Calls++
		d.Tokens += r.In + r.Out + r.CacheRead + r.CacheWrite
		d.Cost += r.Cost
	}
	days := make([]dayVal, 0, len(byDay))
	for _, d := range byDay {
		days = append(days, *d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	return map[string]any{
		"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"), "days": days,
	}
}

// csv 把筛选后的记录导出为 CSV。
func (s *ledgerStore) csv(f ledgerFilter, from, to string, price func(ledgerRecord) (float64, bool)) []byte {
	all := s.records(from, to)
	var b strings.Builder
	b.WriteString("time,agent,model,provider,account,input,output,cache_read,cache_write,cost,ttft_ms,ms,status,error\n")
	for _, r := range all {
		if !f.match(r) {
			continue
		}
		if price != nil {
			if c, ok := price(r); ok {
				r.Cost = c
			}
		}
		line := []string{
			time.UnixMilli(r.T).Format(time.RFC3339), r.Agent, r.Model, r.ProviderName, r.AccountName,
			fmt.Sprintf("%d", r.In), fmt.Sprintf("%d", r.Out), fmt.Sprintf("%d", r.CacheRead), fmt.Sprintf("%d", r.CacheWrite),
			fmt.Sprintf("%.6f", r.Cost), fmt.Sprintf("%d", r.TTFT), fmt.Sprintf("%d", r.Ms),
			fmt.Sprintf("%d", r.Status), r.Err,
		}
		for i, v := range line {
			if strings.ContainsAny(v, ",\"\n") {
				line[i] = `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
			}
		}
		b.WriteString(strings.Join(line, ","))
		b.WriteString("\n")
	}
	return []byte(b.String())
}
