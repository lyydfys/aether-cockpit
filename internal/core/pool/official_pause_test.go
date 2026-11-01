package pool

import (
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func TestSelectSkipsOfficialPausedAccount(t *testing.T) {
	m := New()
	a := provider.NewAccountEnv("a", "mock-native")
	b := provider.NewAccountEnv("b", "mock-native")
	m.Add(a)
	m.Add(b)

	env, err := m.Select("mock-native", SelectOptions{})
	if err != nil || env == nil {
		t.Fatalf("基线选择失败: %v", err)
	}

	a.SetOfficialPause("授权被拒")
	b.SetOfficialPause("授权被拒")
	if _, err := m.Select("mock-native", SelectOptions{}); err == nil {
		t.Fatal("全部暂停时应无可用账号")
	}

	b.ResumeOfficial()
	env, err = m.Select("mock-native", SelectOptions{})
	if err != nil || env == nil || env.AccountID != "b" {
		t.Fatalf("恢复后应只选 b: env=%v err=%v", env, err)
	}
}

func TestOfficialPausePersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")

	src := New()
	src.SetStorePath(path, nil)
	env := provider.NewAccountEnv("acc-pause", "mock-native")
	env.SetOfficialPause("官方授权被拒：401")
	src.Add(env)
	if err := src.SaveAccounts(); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	dst := New()
	if err := dst.SetStorePath(path, nil); err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	got, ok := dst.Get("acc-pause")
	if !ok {
		t.Fatal("账号未恢复")
	}
	paused, reason, at := got.OfficialPaused()
	if !paused || reason == "" || at.IsZero() {
		t.Fatalf("暂停状态未持久化: paused=%v reason=%q at=%v", paused, reason, at)
	}
	if time.Since(at) > time.Minute {
		t.Fatalf("暂停时间异常: %v", at)
	}
}
