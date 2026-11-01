package sessions

import "testing"

// 同一个 (路径, mtime, 大小) 只读一次文件头——这是会话列表从 5 秒降到毫秒级
// 的关键。改了大小必须重读，否则新标题会被旧缓存挡住。
func TestPeekCacheReadsOncePerVersion(t *testing.T) {
	peekMu.Lock()
	peekCache = map[peekKey][2]string{}
	peekMu.Unlock()

	calls := 0
	read := func() (string, string) {
		calls++
		return "标题", "/w"
	}

	t1, c1 := peekCached("p", 100, 10, read)
	if t1 != "标题" || c1 != "/w" || calls != 1 {
		t.Fatalf("首次应真读一次：%q %q calls=%d", t1, c1, calls)
	}
	if t2, c2 := peekCached("p", 100, 10, read); t2 != "标题" || c2 != "/w" || calls != 1 {
		t.Fatalf("同版本应命中缓存：%q %q calls=%d", t2, c2, calls)
	}
	peekCached("p", 100, 11, read)
	if calls != 2 {
		t.Fatalf("大小变了必须重读：calls=%d", calls)
	}
	peekCached("p", 101, 11, read)
	if calls != 3 {
		t.Fatalf("mtime 变了必须重读：calls=%d", calls)
	}
}
