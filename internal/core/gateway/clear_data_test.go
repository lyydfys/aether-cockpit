package gateway

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 中转站的 billing 接口常用 1e8 一类的巨大数表示"无限额度"，真实余额只有几块钱。
// 这种值必须判成"读不到"，否则界面上会出现「剩余 $99999999.04 / 上限 $100000000.00」。
func TestImplausibleBalanceSentinel(t *testing.T) {
	cases := []struct {
		v    float64
		want bool
	}{
		{0, false},
		{12.5, false},
		{999.99, false},
		{99999, false},        // 五位数仍可能是真实积分
		{1e6, true},           // 分界点：>= 1e6 视为哨兵
		{100000000, true},     // 典型的 hard_limit_usd 哨兵
		{99999999.04, true},
		{math.NaN(), true},
		{math.Inf(1), true},
		{math.Inf(-1), true},
	}
	for _, c := range cases {
		if got := implausibleBalanceSentinel(c.v); got != c.want {
			t.Errorf("implausibleBalanceSentinel(%v) = %v，want %v", c.v, got, c.want)
		}
	}
}

// LogBuffer.Clear 清空记录但保留 seq 递增（前端按 seq 做 key，复用旧号会串缓存）。
func TestLogBufferClearKeepsSequence(t *testing.T) {
	b := NewLogBuffer(4)
	b.Push(RequestLog{Model: "m1"})
	b.Push(RequestLog{Model: "m2"})
	if n := b.Clear(); n != 2 {
		t.Fatalf("Clear 返回被清条数 = %d，want 2", n)
	}
	if got := len(b.Recent(10)); got != 0 {
		t.Fatalf("清空后仍有 %d 条", got)
	}
	b.Push(RequestLog{Model: "m3"})
	recent := b.Recent(1)
	if len(recent) != 1 {
		t.Fatalf("清空后新记录没进来")
	}
	if recent[0].Seq != 3 {
		t.Fatalf("清空后 seq 应继续递增到 3，实际 %d（复用旧号会让前端缓存错位）", recent[0].Seq)
	}
}

// handleClearLogs 的 HTTP 契约。
func TestClearLogsEndpoint(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	g.logs.Push(RequestLog{Model: "m1"})
	g.logs.Push(RequestLog{Model: "m2"})
	rec := httptest.NewRecorder()
	g.handleClearLogs(rec, httptest.NewRequest(http.MethodDelete, "/api/logs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"cleared":2`) {
		t.Fatalf("应报告清掉 2 条，实际 %s", body)
	}
}

// usageStore.Clear 清空聚合且把空快照落盘（不是只清内存）。
func TestUsageStoreClearPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")
	s := newUsageStore(path)
	s.record(usageCell{Date: "2026-11-01", ProviderID: "p", Model: "m", Requests: 3})
	s.flush()

	if n := s.Clear(); n != 1 {
		t.Fatalf("Clear 返回天数 = %d，want 1", n)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("清空后应仍有文件（空快照），实际读不到：%v", err)
	}
	if strings.Contains(string(raw), "2026-11-01") {
		t.Fatalf("落盘内容仍含旧数据：%s", string(raw))
	}

	// 重新加载必须也是空的（清空得真落盘了）。
	s2 := newUsageStore(path)
	if days := len(s2.snapshotLocked().days); days != 0 {
		t.Fatalf("重新加载后仍有数据")
	}
}

// ledgerStore.Clear 必须删掉全部 *.jsonl 且关掉当天的追加句柄。
func TestLedgerStoreClearRemovesFiles(t *testing.T) {
	dir := t.TempDir()
	s := newLedgerStore(dir)
	s.add(ledgerRecord{T: 1700000000, Day: "2026-11-01", Model: "m1"})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	removed, err := s.Clear()
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if removed != 1 {
		t.Fatalf("删除文件数 = %d，want 1", removed)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			t.Fatalf("清空后仍残留 %s", e.Name())
		}
	}
}
// 删单条必须只删目标，且重写后的文件与内存一致、还能继续追加。
func TestLedgerDeleteByID(t *testing.T) {
	dir := t.TempDir()
	s := newLedgerStore(dir)
	// add 会用 T 重新算 Day，所以这里按 T 推出来的真实日期查（不写死）。
	base := time.Now().UnixMilli()
	day := time.UnixMilli(base).Format("2006-01-02")
	s.add(ledgerRecord{T: base, Model: "m1"})
	s.add(ledgerRecord{T: base + 1, Model: "m2"})
	s.add(ledgerRecord{T: base + 2, Model: "m3"})

	var target string
	for _, r := range s.days[day] {
		if r.Model == "m2" {
			target = r.ID
		}
	}
	if target == "" {
		t.Fatalf("没找到 m2 的记录 id（day=%s，共 %d 条）", day, len(s.days[day]))
	}

	removed, err := s.DeleteByID(target)
	if err != nil {
		t.Fatalf("DeleteByID: %v", err)
	}
	if !removed {
		t.Fatal("应报告删除成功")
	}

	// 内存里只剩两条。
	if got := len(s.days[day]); got != 2 {
		t.Fatalf("内存剩余 %d 条，want 2", got)
	}

	// 落盘内容也必须只剩两条，且不含被删的那条。
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, day+".jsonl"))
	if err != nil {
		t.Fatalf("读回账本失败: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var rec ledgerRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("重写后的行不是合法 JSONL：%s", line)
		}
		if rec.ID == target {
			t.Fatalf("被删的记录仍在文件里：%s", line)
		}
	}
	if n := strings.Count(strings.TrimSpace(string(raw)), "\n") + 1; n != 2 {
		t.Fatalf("文件里有 %d 条，want 2", n)
	}

	// 删完还能继续追加（句柄应能自动重开）。
	s2 := newLedgerStore(dir)
	s2.add(ledgerRecord{T: base + 3, Model: "m4"})
	if err := s2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw2, _ := os.ReadFile(filepath.Join(dir, day+".jsonl"))
	if !strings.Contains(string(raw2), "m4") {
		t.Fatalf("删除后追加的新记录没落盘：%s", string(raw2))
	}
}

// 删不存在的 id 返回 false 而不是报错（记录可能已被别处删掉）。
func TestLedgerDeleteMissingID(t *testing.T) {
	s := newLedgerStore(t.TempDir())
	removed, err := s.DeleteByID("nope")
	if err != nil {
		t.Fatalf("不存在 id 不该报错：%v", err)
	}
	if removed {
		t.Fatal("不存在 id 不该报告删除成功")
	}
}

// 删掉当天的最后一条：文件应被移除，内存里当天也消失。
func TestLedgerDeleteLastRecordRemovesFile(t *testing.T) {
	dir := t.TempDir()
	s := newLedgerStore(dir)
	base := time.Now().UnixMilli()
	day := time.UnixMilli(base).Format("2006-01-02")
	s.add(ledgerRecord{T: base, Model: "only"})
	var id string
	for _, r := range s.days[day] {
		id = r.ID
	}
	if _, err := s.DeleteByID(id); err != nil {
		t.Fatalf("DeleteByID: %v", err)
	}
	if _, ok := s.days[day]; ok {
		t.Fatalf("删光后内存里不该还有这一天")
	}
	_ = s.Close()
	if _, err := os.Stat(filepath.Join(dir, day+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("删光后文件应被移除，stat err = %v", err)
	}
}