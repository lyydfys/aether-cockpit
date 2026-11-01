package scheduler

import (
	"sort"
	"time"

	"aether-cockpit/internal/provider"
)

// CheckinSummary 是一键签到的结果快照。
type CheckinSummary struct {
	GeneratedAt int64         `json:"generated_at"`
	Since       int64         `json:"since"`
	Counts      SummaryCounts `json:"counts"`
	Items       []SummaryItem `json:"items"`
}

type SummaryCounts struct {
	// 账号口径
	NewSuccess  int `json:"new_success"`
	Already     int `json:"already"`
	Failed      int `json:"failed"`
	Unavailable int `json:"unavailable"`
	NotRun      int `json:"not_run"`
	// 任务/渠道口径
	NoAccount int `json:"no_account"`
	Pending   int `json:"pending"`
}

type SummaryFailure struct {
	AccountID string `json:"account_id"`
	Label     string `json:"label"`
	Message   string `json:"message"`
	At        int64  `json:"at,omitempty"`
}

type SummaryItem struct {
	ProviderID   string           `json:"provider_id"`
	ProviderName string           `json:"provider_name"`
	TaskID       string           `json:"task_id"`
	Title        string           `json:"title"`
	Status       string           `json:"status"` // success | already | failed | no_account | pending | unavailable | not_run
	Accounts     int              `json:"accounts"`
	NewSuccess   int              `json:"new_success"`
	Already      int              `json:"already"`
	Failed       int              `json:"failed"`
	Unavailable  int              `json:"unavailable"`
	Failures     []SummaryFailure `json:"failures,omitempty"`
}

// CheckinSummary 汇总今天每个签到任务的结果。
// since 是本次一键签到的开始时间（Unix 秒）；早于它的成功算“当天已签到”，
// 晚于它的成功算“本次成功”。since<=0 时按今天 0 点处理。
func (s *Scheduler) CheckinSummary(since int64) CheckinSummary {
	now := time.Now()
	if since <= 0 {
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	}

	s.mu.Lock()
	jobs := make([]*Job, len(s.jobs))
	copy(jobs, s.jobs)
	s.mu.Unlock()

	s.qmu.Lock()
	pending := make(map[string]int, len(s.queues))
	for k, q := range s.queues {
		pending[k] = q.Pending()
	}
	s.qmu.Unlock()

	out := CheckinSummary{GeneratedAt: now.Unix(), Since: since, Items: make([]SummaryItem, 0, len(jobs))}
	for _, j := range jobs {
		item := SummaryItem{
			ProviderID:   j.ProviderID,
			TaskID:       j.TaskID,
			Title:        j.Title,
			ProviderName: j.ProviderID,
		}
		if p, ok := provider.Get(j.ProviderID); ok {
			if name := p.Manifest().DisplayName; name != "" {
				item.ProviderName = name
			}
		}
		envs := s.pool.ListByProvider(j.ProviderID)
		item.Accounts = len(envs)
		if len(envs) == 0 {
			item.Status = "no_account"
			out.Counts.NoAccount++
			out.Items = append(out.Items, item)
			continue
		}

		isPending := pending[j.ProviderID] > 0
		for _, env := range envs {
			if _, running := s.running.Load(runningKey(j.ProviderID, env.AccountID)); running {
				isPending = true
			}
			results := s.st.TodayResults(env.AccountID)
			res, has := results[j.TaskID]
			if !env.Available() {
				item.Unavailable++
				continue
			}
			switch {
			case has && res.Success:
				if res.At >= since {
					item.NewSuccess++
				} else {
					item.Already++
				}
			case has && !res.Success:
				item.Failed++
				msg := res.Message
				if msg == "" {
					msg = "上游未返回失败原因"
				}
				item.Failures = append(item.Failures, SummaryFailure{
					AccountID: env.AccountID, Label: env.Label, Message: msg, At: res.At,
				})
			default:
				if !isPending {
					out.Counts.NotRun++
				}
			}
		}

		if isPending {
			item.Status = "pending"
			out.Counts.Pending++
		} else if item.Failed > 0 {
			item.Status = "failed"
			out.Counts.Failed += item.Failed
		} else if item.NewSuccess > 0 {
			item.Status = "success"
			out.Counts.NewSuccess += item.NewSuccess
		} else if item.Already > 0 {
			item.Status = "already"
			out.Counts.Already += item.Already
		} else if item.Unavailable > 0 {
			item.Status = "unavailable"
			out.Counts.Unavailable += item.Unavailable
		} else {
			item.Status = "not_run"
		}
		// 混合账号：即使主状态是 success/failed，也把已签与不可用计数汇总。
		if item.Status != "already" {
			out.Counts.Already += item.Already
		}
		if item.Status != "unavailable" {
			out.Counts.Unavailable += item.Unavailable
		}
		if item.Status != "success" {
			out.Counts.NewSuccess += item.NewSuccess
		}
		if item.Status != "failed" {
			out.Counts.Failed += item.Failed
		}
		out.Items = append(out.Items, item)
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].ProviderID < out.Items[j].ProviderID })
	return out
}
