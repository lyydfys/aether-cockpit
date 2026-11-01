package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func makeNode(id string, deps []string, run func(ctx context.Context) NodeResult) *Node {
	return &Node{ID: id, Title: id, DependsOn: deps, Run: run}
}

func okNode(id string, deps []string) *Node {
	return makeNode(id, deps, func(ctx context.Context) NodeResult {
		return NodeResult{NodeID: id, Success: true}
	})
}

func TestDAGLayers(t *testing.T) {
	d := NewDAG()
	_ = d.Add(okNode("refresh", nil))
	_ = d.Add(okNode("checkin", []string{"refresh"}))
	_ = d.Add(okNode("bonus", []string{"checkin"}))
	_ = d.Add(okNode("quota", []string{"bonus"}))

	layers, err := d.Layers()
	if err != nil {
		t.Fatalf("分层失败: %v", err)
	}
	if len(layers) != 4 {
		t.Fatalf("层数 = %d, want 4（严格线性链）: %v", len(layers), layers)
	}
	if layers[0][0] != "refresh" || layers[3][0] != "quota" {
		t.Errorf("顺序错误: %v", layers)
	}
}

func TestDAGParallelLayer(t *testing.T) {
	d := NewDAG()
	_ = d.Add(okNode("root", nil))
	_ = d.Add(okNode("a", []string{"root"}))
	_ = d.Add(okNode("b", []string{"root"}))
	_ = d.Add(okNode("c", []string{"a", "b"}))

	layers, err := d.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 3 {
		t.Fatalf("层数 = %d, want 3: %v", len(layers), layers)
	}
	if len(layers[1]) != 2 {
		t.Errorf("中间层应有 2 个并行节点，实际 %v", layers[1])
	}
}

func TestDAGCycleDetection(t *testing.T) {
	d := NewDAG()
	_ = d.Add(okNode("a", []string{"c"}))
	_ = d.Add(okNode("b", []string{"a"}))
	_ = d.Add(okNode("c", []string{"b"}))

	if _, err := d.Layers(); err == nil {
		t.Fatal("循环依赖应报错")
	}
}

func TestDAGMissingDependency(t *testing.T) {
	d := NewDAG()
	_ = d.Add(okNode("a", []string{"不存在"}))
	if err := d.Validate(); err == nil {
		t.Fatal("依赖不存在应报错")
	}
}

func TestDAGSkippedAfterFailure(t *testing.T) {
	var bonusRan atomic.Bool
	d := NewDAG()
	_ = d.Add(makeNode("checkin", nil, func(ctx context.Context) NodeResult {
		return NodeResult{NodeID: "checkin", Success: false, Message: "429"}
	}))
	_ = d.Add(makeNode("bonus", []string{"checkin"}, func(ctx context.Context) NodeResult {
		bonusRan.Store(true)
		return NodeResult{NodeID: "bonus", Success: true}
	}))

	rep := d.Run(context.Background(), RunOptions{Concurrency: 1})
	if rep.Success {
		t.Error("有失败节点时整体不应成功")
	}
	if bonusRan.Load() {
		t.Error("前置失败时后继不应执行")
	}
	// 后继应标记为 Skipped
	var skipped bool
	for _, r := range rep.Results {
		if r.NodeID == "bonus" && r.Skipped {
			skipped = true
		}
	}
	if !skipped {
		t.Errorf("后继应标记 Skipped: %+v", rep.Results)
	}
}

// TestDAGOptionalDoesNotBlock 是可选任务的核心语义：
// 可选任务失败不该连累后面的任务。
func TestDAGOptionalDoesNotBlock(t *testing.T) {
	var quotaRan atomic.Bool
	d := NewDAG()
	bonus := makeNode("bonus", nil, func(ctx context.Context) NodeResult {
		return NodeResult{NodeID: "bonus", Success: false, Message: "玩法已下线"}
	})
	bonus.Optional = true
	_ = d.Add(bonus)
	_ = d.Add(makeNode("quota", []string{"bonus"}, func(ctx context.Context) NodeResult {
		quotaRan.Store(true)
		return NodeResult{NodeID: "quota", Success: true}
	}))

	rep := d.Run(context.Background(), RunOptions{Concurrency: 1})
	if !quotaRan.Load() {
		t.Error("Optional 前置失败不应阻塞后继")
	}
	if rep.Failed != 1 {
		t.Errorf("失败数 = %d, want 1", rep.Failed)
	}
}

func TestDAGNotBefore(t *testing.T) {
	var ran atomic.Bool
	d := NewDAG()
	n := makeNode("late", nil, func(ctx context.Context) NodeResult {
		ran.Store(true)
		return NodeResult{NodeID: "late", Success: true}
	})
	n.NotBefore = "23:59"
	_ = d.Add(n)

	// 注入一个 10:00 的"当前时间"
	now := func() time.Time {
		return time.Date(2026, 10, 26, 10, 0, 0, 0, time.Local)
	}
	rep := d.Run(context.Background(), RunOptions{Now: now})
	if ran.Load() {
		t.Error("未到 NotBefore 不应执行")
	}
	if rep.Skipped != 1 {
		t.Errorf("跳过数 = %d, want 1", rep.Skipped)
	}
}

func TestDAGConcurrency(t *testing.T) {
	var concurrent, maxConcurrent int32
	work := func(id string) *Node {
		return makeNode(id, nil, func(ctx context.Context) NodeResult {
			cur := atomic.AddInt32(&concurrent, 1)
			for {
				old := atomic.LoadInt32(&maxConcurrent)
				if cur <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&concurrent, -1)
			return NodeResult{NodeID: id, Success: true}
		})
	}

	d := NewDAG()
	for _, id := range []string{"a", "b", "c", "d"} {
		_ = d.Add(work(id))
	}

	d.Run(context.Background(), RunOptions{Concurrency: 2})
	if got := atomic.LoadInt32(&maxConcurrent); got > 2 {
		t.Errorf("并发度超过限制: %d > 2", got)
	}
}

func TestDAGPanicRecovery(t *testing.T) {
	d := NewDAG()
	_ = d.Add(makeNode("boom", nil, func(ctx context.Context) NodeResult {
		var m map[string]string
		m["x"] = "y" // 故意 panic
		return NodeResult{Success: true}
	}))

	rep := d.Run(context.Background(), RunOptions{Concurrency: 1})
	if rep.Success {
		t.Error("panic 应被捕获并判定失败")
	}
	if rep.Results[0].Message == "" {
		t.Error("应记录 panic 信息")
	}
}

func TestDAGNodeTimeout(t *testing.T) {
	d := NewDAG()
	n := makeNode("slow", nil, func(ctx context.Context) NodeResult {
		select {
		case <-time.After(2 * time.Second):
			return NodeResult{Success: true}
		case <-ctx.Done():
			return NodeResult{Success: false, Message: "超时"}
		}
	})
	n.Timeout = 30 * time.Millisecond
	_ = d.Add(n)

	start := time.Now()
	rep := d.Run(context.Background(), RunOptions{Concurrency: 1})
	if time.Since(start) > time.Second {
		t.Error("超时未生效")
	}
	if rep.Success {
		t.Error("超时应判定失败")
	}
}

func TestDAGOrderStable(t *testing.T) {
	// 同层节点必须按定义顺序输出，保证结果可复现。
	d := NewDAG()
	for _, id := range []string{"c", "a", "b"} {
		_ = d.Add(okNode(id, nil))
	}
	layers, _ := d.Layers()
	want := []string{"c", "a", "b"}
	for i, id := range layers[0] {
		if id != want[i] {
			t.Errorf("同层顺序 = %v, want %v", layers[0], want)
			break
		}
	}
}

func TestCheckinGate(t *testing.T) {
	s := New(Options{StatePath: ""}, nil, nil)
	if !s.checkinAllowed("mock-native") {
		t.Fatal("未设置开关时应放行")
	}
	s.SetCheckinOn(func(id string) bool { return id != "mock-native" })
	if s.checkinAllowed("mock-native") {
		t.Fatal("开关返回 false 时不应放行")
	}
	if !s.checkinAllowed("other") {
		t.Fatal("其他渠道应按开关返回值")
	}
	s.SetCheckinOn(nil)
	if !s.checkinAllowed("mock-native") {
		t.Fatal("清除开关后应放行")
	}
}
