package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Result 是单次签到任务的执行结果。
type Result struct {
	Success   bool    `json:"success"`
	Points    int     `json:"points,omitempty"`
	Message   string  `json:"message,omitempty"`
	At        int64   `json:"at"`
	LatencyMS int64   `json:"latency_ms,omitempty"`
	Outcome   string  `json:"outcome,omitempty"`
	Credit    float64 `json:"credit,omitempty"`
	Streak    int     `json:"streak,omitempty"`
	Day       string  `json:"day,omitempty"`
	// Reason 记录触发来源：cron / manual / wake。
	Reason string `json:"reason,omitempty"`
}

// State 记录"今天哪些账号的哪些任务已经完成"。
//
// 落盘是必需的：否则每次重启都会重复签到，轻则浪费请求，
// 重则因为"短时间内重复打卡"被上游判定异常。
type State struct {
	mu   sync.Mutex
	path string
	// days: 日期(2026-10-26) -> accountID -> taskID -> Result
	days map[string]map[string]map[string]Result
}

// NewState 创建状态存储。path 为空表示纯内存。
func NewState(path string) *State {
	s := &State{
		path: path,
		days: make(map[string]map[string]map[string]Result, 8),
	}
	s.load()
	return s
}

// DayKey 返回用于索引的日期字符串。
func DayKey(t time.Time) string {
	return t.Format("2006-01-02")
}

// IsDone 判断某账号某任务今天是否已完成。
func (s *State) IsDone(accountID, taskID string, day string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.days[day]
	if !ok {
		return false
	}
	a, ok := d[accountID]
	if !ok {
		return false
	}
	r, ok := a[taskID]
	return ok && r.Success
}

// Record 记录一次执行结果。
func (s *State) Record(accountID, taskID string, r Result, day string) {
	if r.At == 0 {
		r.At = time.Now().Unix()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.days[day]
	if !ok {
		d = make(map[string]map[string]Result, 8)
		s.days[day] = d
	}
	a, ok := d[accountID]
	if !ok {
		a = make(map[string]Result, 4)
		d[accountID] = a
	}
	// 只有成功才覆盖已有成功记录，避免"重试失败"把已成功的状态冲掉。
	if prev, exists := a[taskID]; exists && prev.Success && !r.Success {
		return
	}
	a[taskID] = r
}

// TodayResults 返回今天某账号的全部结果（供 UI 展示）。
func (s *State) TodayResults(accountID string) map[string]Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Result, 4)
	d, ok := s.days[DayKey(time.Now())]
	if !ok {
		return out
	}
	for k, v := range d[accountID] {
		out[k] = v
	}
	return out
}

// CompletedToday 返回今天已成功的任务数。
func (s *State) CompletedToday() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	d := s.days[DayKey(time.Now())]
	for _, tasks := range d {
		for _, r := range tasks {
			if r.Success {
				n++
			}
		}
	}
	return n
}

// Reset 清除某账号某任务今日的记录（用于手动重试）。
func (s *State) Reset(accountID, taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.days[DayKey(time.Now())]
	if !ok {
		return
	}
	if a, ok := d[accountID]; ok {
		delete(a, taskID)
	}
}

// --- 落盘 ---

type stateFile struct {
	Version int                                     `json:"version"`
	SavedAt int64                                   `json:"saved_at"`
	Days    map[string]map[string]map[string]Result `json:"days"`
}

// Save 写入磁盘并清理过期日期。
func (s *State) Save() error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// 只保留最近 7 天，避免文件无限增长。
	cutoff := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	for day := range s.days {
		if day < cutoff {
			delete(s.days, day)
		}
	}

	f := stateFile{Version: 1, SavedAt: time.Now().Unix(), Days: s.days}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *State) load() {
	if s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var f stateFile
	if err := json.Unmarshal(b, &f); err != nil {
		return
	}
	if f.Days != nil {
		s.days = f.Days
	}
}
