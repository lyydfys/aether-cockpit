// Package scheduler 是 DAG 签到与保活调度引擎。
//
// 设计取舍：不做通用 DAG 调度器。实际签到链的依赖深度从未超过 3 层
// （刷新 Token → 签到 → 领奖励箱），用"有序任务数组 + DependsOn"表达即可，
// 上一套完整 DAG 引擎的复杂度换不来收益。
//
// 真正需要认真做的是三件反风控相关的事：
//  1. 同一渠道的多个账号绝不并发打卡，而是串行 + 随机抖动；
//  2. 定时触发要带随机偏移，避免所有用户在同一秒打同一个接口；
//  3. 系统唤醒后要能补签（笔记本合盖是常态，错过定时是必然）。
package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule 是一个解析后的 cron 表达式（5 字段：分 时 日 月 周）。
//
// 支持：*  |  */n  |  a,b,c  |  a-b  |  a-b/n
// 不支持：@yearly 等宏、秒级字段、L/W/# 扩展。签到场景用不到，加了反而增加心智负担。
type Schedule struct {
	expr   string
	minute [60]bool
	hour   [24]bool
	dom    [32]bool // 1-31
	month  [13]bool // 1-12
	dow    [7]bool  // 0-6，0=周日
	// domStar / dowStar 用于实现标准 cron 语义：
	// 当"日"和"周"都不是 * 时，两者是 OR 关系；否则是 AND。
	domStar bool
	dowStar bool
}

// ParseCron 解析标准 5 字段 cron。
func ParseCron(expr string) (*Schedule, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron 表达式需要 5 个字段，实际 %d 个: %q", len(fields), expr)
	}

	s := &Schedule{expr: expr}

	if err := parseField(fields[0], 0, 59, func(v int) { s.minute[v] = true }); err != nil {
		return nil, fmt.Errorf("分钟字段: %w", err)
	}
	if err := parseField(fields[1], 0, 23, func(v int) { s.hour[v] = true }); err != nil {
		return nil, fmt.Errorf("小时字段: %w", err)
	}
	if err := parseField(fields[2], 1, 31, func(v int) { s.dom[v] = true }); err != nil {
		return nil, fmt.Errorf("日期字段: %w", err)
	}
	if err := parseField(fields[3], 1, 12, func(v int) { s.month[v] = true }); err != nil {
		return nil, fmt.Errorf("月份字段: %w", err)
	}
	if err := parseField(fields[4], 0, 6, func(v int) { s.dow[v] = true }); err != nil {
		return nil, fmt.Errorf("星期字段: %w", err)
	}

	s.domStar = fields[2] == "*"
	s.dowStar = fields[4] == "*"
	return s, nil
}

// parseField 解析单个字段。
func parseField(field string, min, max int, set func(int)) error {
	if field == "" {
		return fmt.Errorf("空字段")
	}

	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("空列表项")
		}

		step := 1
		if idx := strings.Index(part, "/"); idx >= 0 {
			stepStr := part[idx+1:]
			n, err := strconv.Atoi(stepStr)
			if err != nil || n <= 0 {
				return fmt.Errorf("非法的步长 %q", stepStr)
			}
			step = n
			part = part[:idx]
		}

		var lo, hi int
		switch {
		case part == "*":
			lo, hi = min, max
		case strings.Contains(part, "-"):
			bits := strings.SplitN(part, "-", 2)
			a, err1 := strconv.Atoi(bits[0])
			b, err2 := strconv.Atoi(bits[1])
			if err1 != nil || err2 != nil {
				return fmt.Errorf("非法的区间 %q", part)
			}
			lo, hi = a, b
		default:
			v, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Errorf("非法的数值 %q", part)
			}
			lo, hi = v, v
			// 单值 + 步长（如 "5/10"）按 cron 惯例从该值起跳到上界。
			if step > 1 {
				hi = max
			}
		}

		if lo < min || hi > max || lo > hi {
			return fmt.Errorf("取值越界 [%d,%d]: %d-%d", min, max, lo, hi)
		}
		for v := lo; v <= hi; v += step {
			set(v)
		}
	}
	return nil
}

// String 返回原始表达式。
func (s *Schedule) String() string {
	if s == nil {
		return ""
	}
	return s.expr
}

// Next 返回严格晚于 after 的下一个触发时刻。
//
// 用"逐级跳转"而非逐分钟暴搜：字段不匹配时直接跳到下一个候选边界，
// 最坏情况也只是几百次迭代。
func (s *Schedule) Next(after time.Time) time.Time {
	if s == nil {
		return time.Time{}
	}
	t := after.Truncate(time.Minute).Add(time.Minute)
	// 上限 5 年：能覆盖 2 月 29 日这类极端表达式，同时防死循环。
	limit := after.AddDate(5, 0, 0)

	for t.Before(limit) {
		if !s.month[int(t.Month())] {
			// 跳到下个月 1 日 0 点
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).AddDate(0, 1, 0)
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).AddDate(0, 0, 1)
			continue
		}
		if !s.hour[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location()).Add(1 * time.Hour)
			continue
		}
		if !s.minute[t.Minute()] {
			t = t.Add(1 * time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// dayMatches 按标准 cron 语义判断日期是否匹配。
//
// 当"日"和"周"都被限制时，满足任一即可（OR）；否则两者都要满足（AND）。
func (s *Schedule) dayMatches(t time.Time) bool {
	domOK := s.dom[t.Day()]
	dowOK := s.dow[int(t.Weekday())]

	if s.domStar && s.dowStar {
		return true
	}
	if s.domStar {
		return dowOK
	}
	if s.dowStar {
		return domOK
	}
	return domOK || dowOK
}

// Jitter 在基准时刻上叠加一个确定性的随机偏移，返回偏移后的时刻。
//
// 为什么要确定性：如果纯随机，每次重启后同一任务的偏移都会变，
// 用户会感觉"签到时间飘忽不定"。用 seed（账号+任务）派生偏移则稳定可复现，
// 同时不同账号之间的偏移又是分散的。
func Jitter(base time.Time, maxOffset time.Duration, seed string) time.Time {
	if maxOffset <= 0 {
		return base
	}
	h := fnv32(seed)
	// 取模得到 0..maxOffset 秒
	sec := int64(h>>8) % (int64(maxOffset.Seconds()) + 1)
	return base.Add(time.Duration(sec) * time.Second)
}

// fnv32 是一个廉价的字符串散列，用于派生抖动偏移。
func fnv32(s string) uint32 {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	h := uint32(offset32)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime32
	}
	return h
}
