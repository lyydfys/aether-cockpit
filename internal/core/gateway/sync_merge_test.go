package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// 三路合并的四种基本分支。
func TestMergeThreeWayBasic(t *testing.T) {
	base := map[string]any{"a": "1", "b": "2", "c": "3"}

	// 只有远端改 a；只有本机改 b；c 两边都没动。
	local := map[string]any{"a": "1", "b": "2-local", "c": "3"}
	remote := map[string]any{"a": "1-remote", "b": "2", "c": "3"}
	res := mergeThreeWay(base, local, remote)
	got := res.Value.(map[string]any)
	if got["a"] != "1-remote" {
		t.Errorf("只有远端改过，应取远端：%v", got["a"])
	}
	if got["b"] != "2-local" {
		t.Errorf("只有本机改过，应取本机：%v", got["b"])
	}
	if got["c"] != "3" {
		t.Errorf("都没改，应保持：%v", got["c"])
	}
	if len(res.Conflicts) != 0 {
		t.Errorf("不该有冲突：%+v", res.Conflicts)
	}
}

// 两边都改同一个字段 → 保留本机 + 记冲突（这是"不丢本机正在编辑的值"）。
func TestMergeThreeWayConflictKeepsLocal(t *testing.T) {
	base := map[string]any{"port": float64(7860)}
	local := map[string]any{"port": float64(8080)}
	remote := map[string]any{"port": float64(9999)}
	res := mergeThreeWay(base, local, remote)
	got := res.Value.(map[string]any)
	if got["port"] != float64(8080) {
		t.Fatalf("冲突应保留本机值，得到 %v", got["port"])
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Path != "port" {
		t.Fatalf("应记录一条 port 冲突：%+v", res.Conflicts)
	}
	if res.Conflicts[0].Remote != float64(9999) {
		t.Fatalf("冲突档案里要留下远端值：%+v", res.Conflicts[0])
	}
}

// 嵌套对象逐键合并，而不是整段覆盖。
func TestMergeThreeWayNestedObject(t *testing.T) {
	base := map[string]any{"ui": map[string]any{"theme": "dark", "lang": "zh"}}
	local := map[string]any{"ui": map[string]any{"theme": "light", "lang": "zh"}}
	remote := map[string]any{"ui": map[string]any{"theme": "dark", "lang": "en"}}
	res := mergeThreeWay(base, local, remote)
	ui := res.Value.(map[string]any)["ui"].(map[string]any)
	if ui["theme"] != "light" {
		t.Errorf("本机改的 theme 应保留：%v", ui["theme"])
	}
	if ui["lang"] != "en" {
		t.Errorf("远端改的 lang 应取远端：%v", ui["lang"])
	}
	if len(res.Conflicts) != 0 {
		t.Errorf("不同字段各自被改，不算冲突：%+v", res.Conflicts)
	}
}

// 数组按身份键并集：本机新增 + 远端新增都要保留。
func TestMergeThreeWaySliceUnion(t *testing.T) {
	base := map[string]any{"items": []any{
		map[string]any{"id": "a", "v": "1"},
	}}
	local := map[string]any{"items": []any{
		map[string]any{"id": "a", "v": "1"},
		map[string]any{"id": "b", "v": "local-new"},
	}}
	remote := map[string]any{"items": []any{
		map[string]any{"id": "a", "v": "1"},
		map[string]any{"id": "c", "v": "remote-new"},
	}}
	res := mergeThreeWay(base, local, remote)
	items := res.Value.(map[string]any)["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("应合并成 3 项（a/b/c），得到 %d：%+v", len(items), items)
	}
	ids := map[string]bool{}
	for _, it := range items {
		ids[it.(map[string]any)["id"].(string)] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !ids[want] {
			t.Errorf("缺少 %s：%+v", want, items)
		}
	}
}

// 第一次同步（没有底稿）也不能整包覆盖：逐键合并，本机优先。
func TestMergeBundlesWithoutBase(t *testing.T) {
	local := []byte(`{"settings":{"port":8080,"theme":"light"}}`)
	remote := []byte(`{"settings":{"port":7860,"theme":"dark","lang":"en"}}`)
	merged, _, err := mergeBundles(nil, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]map[string]any
	if json.Unmarshal(merged, &out) != nil {
		t.Fatal("合并结果不是合法 JSON")
	}
	s := out["settings"]
	if s["port"] != float64(8080) {
		t.Errorf("没有底稿时保留本机值：%v", s["port"])
	}
	if s["lang"] != "en" {
		t.Errorf("远端独有的键要带过来：%v", s["lang"])
	}
}

// part 摘要用于跳过"远端已经一样"的无谓上传。
func TestBundlePartDigestIgnoresExportedAt(t *testing.T) {
	a := []byte(`{"version":1,"exported_at":100,"settings":{"port":1}}`)
	b := []byte(`{"version":1,"exported_at":999,"settings":{"port":1}}`)
	if !samePartDigest(bundlePartDigest(a), bundlePartDigest(b)) {
		t.Fatal("exported_at 不同不该被当成内容变化")
	}
	c := []byte(`{"version":1,"exported_at":100,"settings":{"port":2}}`)
	if samePartDigest(bundlePartDigest(a), bundlePartDigest(c)) {
		t.Fatal("内容变了必须能看出来")
	}
}

// 跨进程锁：活着的持有者会挡住第二次获取；释放后可以再拿。
func TestSyncLockExcludesAndReleases(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: "-"})

	release, err := g.acquireSyncLock()
	if err != nil {
		t.Fatalf("首次加锁应成功：%v", err)
	}
	// 自己持有期间再取：同一 PID 视为活着，应被挡住。
	if _, err := g.acquireSyncLock(); err == nil {
		t.Fatal("锁被占用时应拒绝")
	}
	release()
	if _, err := g.acquireSyncLock(); err != nil {
		t.Fatalf("释放后应能重新加锁：%v", err)
	}
}

// 过期锁自动失效（进程崩溃留下的锁不该永久挡住同步）。
func TestSyncLockExpires(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: "-"})
	lock := g.syncStatePath() + ".lock"
	// 造一个"很久以前、PID 不存在"的锁文件。
	if err := os.WriteFile(lock, []byte("999999\n"+strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.acquireSyncLock(); err != nil {
		t.Fatalf("过期锁应自动失效：%v", err)
	}
}

// 冲突留档要真的落盘（用户能从文件里找回远端值）。
func TestArchiveSyncConflictsWritesFile(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: "-"})
	path := g.archiveSyncConflicts([]mergeConflict{{Path: "port", Local: 8080, Remote: 9999}})
	if path == "" {
		t.Fatal("应写出冲突档案")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Conflicts []mergeConflict `json:"conflicts"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Conflicts) != 1 {
		t.Fatalf("档案内容不对：%s", raw)
	}
	if doc.Conflicts[0].Path != "port" {
		t.Fatalf("冲突路径不对：%+v", doc.Conflicts)
	}
}

// 状态文件读写往返。
func TestSyncStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: "-"})
	st := &syncState{
		Base: json.RawMessage(`{"a":1}`), BaseAt: 123,
		Parts: map[string]string{"settings": "abc"}, ETag: `"v1"`,
	}
	g.saveSyncState(st)
	got := g.loadSyncState()
	// Base 会随状态文件一起被缩进重排，按语义比对而不是逐字节。
	var base map[string]any
	if json.Unmarshal(got.Base, &base) != nil || base["a"] != float64(1) {
		t.Fatalf("Base 丢失或损坏：%s", got.Base)
	}
	if got.ETag != `"v1"` || got.Parts["settings"] != "abc" || got.BaseAt != 123 {
		t.Fatalf("往返丢字段：%+v", got)
	}
}
