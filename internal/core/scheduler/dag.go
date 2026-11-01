package scheduler

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Node 是 DAG 中的一个任务节点。
type Node struct {
	// ID 节点唯一标识（等于 TaskDefinition.TaskID）。
	ID string
	// Title 用于展示。
	Title string
	// DependsOn 前置节点。
	DependsOn []string
	// Optional 表示失败不阻塞后续（"猫猫旅行"这类随时下线的玩法）。
	Optional bool
	// Timeout 单节点超时。
	Timeout time.Duration
	// NotBefore 当日最早可执行时刻（如 "10:00"）。空表示不限制。
	//
	// 为什么需要它：cron 是按单个任务配的，但 DAG 是整图执行。
	// 9:00 触发 DAG 时，10:00 才该执行的任务不应该跟着跑。
	NotBefore string
	// Run 节点实际逻辑。
	Run func(ctx context.Context) NodeResult
}

// NodeResult 是单个节点的执行结果。
type NodeResult struct {
	NodeID     string  `json:"node_id"`
	Title      string  `json:"title"`
	Success    bool    `json:"success"`
	Skipped    bool    `json:"skipped"`
	SkipWhy    string  `json:"skip_why,omitempty"`
	Message    string  `json:"message,omitempty"`
	Points     int     `json:"points,omitempty"`
	Outcome    string  `json:"outcome,omitempty"`
	Credit     float64 `json:"credit,omitempty"`
	Streak     int     `json:"streak,omitempty"`
	Day        string  `json:"day,omitempty"`
	StartedAt  int64   `json:"started_at"`
	DurationMS int64   `json:"duration_ms"`
}

// DAG 是任务依赖图。
type DAG struct {
	nodes map[string]*Node
	// insertion 保留定义顺序，让同层节点的执行顺序稳定可复现。
	insertion []string
}

// NewDAG 创建空图。
func NewDAG() *DAG {
	return &DAG{nodes: make(map[string]*Node, 8)}
}

// Add 添加节点。重复 ID 会报错。
func (d *DAG) Add(n *Node) error {
	if n == nil || n.ID == "" {
		return fmt.Errorf("dag: 节点 ID 不能为空")
	}
	if _, dup := d.nodes[n.ID]; dup {
		return fmt.Errorf("dag: 节点 %q 重复", n.ID)
	}
	if n.Timeout <= 0 {
		n.Timeout = 60 * time.Second
	}
	d.nodes[n.ID] = n
	d.insertion = append(d.insertion, n.ID)
	return nil
}

// Validate 校验依赖存在性与无环。
func (d *DAG) Validate() error {
	for _, n := range d.nodes {
		for _, dep := range n.DependsOn {
			if _, ok := d.nodes[dep]; !ok {
				return fmt.Errorf("dag: 节点 %q 依赖了不存在的节点 %q", n.ID, dep)
			}
			if dep == n.ID {
				return fmt.Errorf("dag: 节点 %q 依赖了自己", n.ID)
			}
		}
	}
	_, err := d.Layers()
	return err
}

// Layers 返回拓扑分层结果：同一层内的节点互不依赖，可以并发。
func (d *DAG) Layers() ([][]string, error) {
	done := make(map[string]bool, len(d.nodes))
	remaining := len(d.nodes)

	var layers [][]string
	for remaining > 0 {
		var layer []string
		// 按定义顺序遍历，保证同层顺序稳定可复现。
		for _, id := range d.insertion {
			if done[id] {
				continue
			}
			ready := true
			for _, dep := range d.nodes[id].DependsOn {
				if !done[dep] {
					ready = false
					break
				}
			}
			if ready {
				layer = append(layer, id)
			}
		}
		if len(layer) == 0 {
			return nil, fmt.Errorf("dag: 检测到循环依赖（剩余 %d 个节点无法排序）", remaining)
		}
		for _, id := range layer {
			done[id] = true
		}
		remaining -= len(layer)
		layers = append(layers, layer)
	}
	return layers, nil
}

// TopoOrder 返回一个平坦的拓扑序（同层按定义顺序）。
func (d *DAG) TopoOrder() ([]string, error) {
	layers, err := d.Layers()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(d.nodes))
	for _, layer := range layers {
		out = append(out, layer...)
	}
	return out, nil
}

// Bind 返回一个结构相同的副本，其中每个节点的 Run 由 factory 按 nodeID 生成。
//
// 为什么需要这样做：DAG 的结构（依赖关系）是渠道级的、可复用的；
// 但 Run 必须绑定到具体的账号环境上。同一个渠道有 5 个账号时，
// 它们共享同一张图的结构，却各自拥有独立的执行闭包。
func (d *DAG) Bind(factory func(nodeID string) func(context.Context) NodeResult) *DAG {
	cp := NewDAG()
	for _, id := range d.insertion {
		orig := d.nodes[id]
		n := *orig
		n.Run = factory(id)
		_ = cp.Add(&n)
	}
	return cp
}

// NodeIDs 返回按定义顺序的全部节点 ID。
func (d *DAG) NodeIDs() []string {
	out := make([]string, len(d.insertion))
	copy(out, d.insertion)
	return out
}

// Size 返回节点数。
func (d *DAG) Size() int { return len(d.nodes) }

// RunOptions 控制执行行为。
type RunOptions struct {
	// Concurrency 同层并发度。签到场景应设为 1（串行），因为同时打多个任务
	// 是最典型的批量操作特征。
	Concurrency int
	// Gap 节点之间的间隔，用于反风控抖动。
	Gap time.Duration
	// Timeline 各节点的当日最早时刻（来自 cron 配置），用于 NotBefore 判断。
	// key 为节点 ID。
	Timeline map[string]string
	// Now 便于测试注入固定时间。
	Now func() time.Time
	// OnResult 每个节点完成时回调（可为 nil）。
	OnResult func(NodeResult)
}

// Report 是一次 DAG 执行的汇总。
type Report struct {
	Results    []NodeResult `json:"results"`
	Success    bool         `json:"success"`
	DurationMS int64        `json:"duration_ms"`
	Skipped    int          `json:"skipped"`
	Failed     int          `json:"failed"`
}

// Run 执行整张图。
//
// 失败传播规则：
//   - 节点失败且不是 Optional -> 依赖它的后继全部标记 Skipped；
//   - 节点 Optional 失败 -> 后继照常执行（但会把失败计入报告）。
func (d *DAG) Run(ctx context.Context, opts RunOptions) *Report {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	start := opts.Now()
	rep := &Report{}

	layers, err := d.Layers()
	if err != nil {
		rep.Results = append(rep.Results, NodeResult{
			NodeID:  "_dag",
			Title:   "DAG 校验失败",
			Success: false,
			Message: err.Error(),
		})
		rep.Failed = 1
		return rep
	}

	// results 记录每个节点是否成功，供后继判断。
	results := make(map[string]NodeResult, len(d.nodes))

	for _, layer := range layers {
		if ctx.Err() != nil {
			break
		}

		sem := make(chan struct{}, opts.Concurrency)
		var wg sync.WaitGroup
		var mu sync.Mutex

		for _, id := range layer {
			id := id
			n := d.nodes[id]

			// 前置检查：依赖是否都成功
			if why, blocked := d.blockedBy(n, results); blocked {
				r := NodeResult{
					NodeID:  id,
					Title:   n.Title,
					Skipped: true,
					SkipWhy: why,
				}
				mu.Lock()
				results[id] = r
				rep.Results = append(rep.Results, r)
				rep.Skipped++
				mu.Unlock()
				if opts.OnResult != nil {
					opts.OnResult(r)
				}
				continue
			}

			// NotBefore 检查：未到该任务当日的时间点，跳过等下次触发。
			if nb := nodeNotBefore(n, opts.Timeline); nb != "" {
				if !reachedTime(opts.Now(), nb) {
					r := NodeResult{
						NodeID:  id,
						Title:   n.Title,
						Skipped: true,
						SkipWhy: "未到计划时间 " + nb,
					}
					mu.Lock()
					results[id] = r
					rep.Results = append(rep.Results, r)
					rep.Skipped++
					mu.Unlock()
					if opts.OnResult != nil {
						opts.OnResult(r)
					}
					continue
				}
			}

			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()

				r := d.runNode(ctx, n)
				mu.Lock()
				results[id] = r
				rep.Results = append(rep.Results, r)
				if !r.Success && !r.Skipped {
					rep.Failed++
				}
				if r.Skipped {
					rep.Skipped++
				}
				mu.Unlock()
				if opts.OnResult != nil {
					opts.OnResult(r)
				}
			}()
		}
		wg.Wait()

		// 层间抖动：串行模式下这一步是主要的反风控手段。
		if opts.Gap > 0 && ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-time.After(opts.Gap):
			}
		}
	}

	// 汇总：全部节点都成功或跳过，才算整体成功。
	rep.Success = rep.Failed == 0
	// 按定义顺序输出，便于阅读。
	sort.SliceStable(rep.Results, func(i, j int) bool {
		return d.indexOf(rep.Results[i].NodeID) < d.indexOf(rep.Results[j].NodeID)
	})
	rep.DurationMS = opts.Now().Sub(start).Milliseconds()
	return rep
}

func (d *DAG) indexOf(id string) int {
	for i, v := range d.insertion {
		if v == id {
			return i
		}
	}
	return len(d.insertion)
}

// runNode 执行单个节点，带超时与 panic 保护。
func (d *DAG) runNode(ctx context.Context, n *Node) NodeResult {
	r := NodeResult{
		NodeID:    n.ID,
		Title:     n.Title,
		StartedAt: time.Now().Unix(),
	}
	if n.Run == nil {
		r.Success = true
		r.Skipped = true
		r.SkipWhy = "未实现"
		return r
	}

	nctx, cancel := context.WithTimeout(ctx, n.Timeout)
	defer cancel()

	start := time.Now()
	// panic 保护：一个任务实现里的空指针不应该让整个调度器崩掉。
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				r.Success = false
				r.Message = fmt.Sprintf("任务 panic: %v", rec)
			}
		}()
		r = mergeResult(r, n.Run(nctx))
	}()
	r.DurationMS = time.Since(start).Milliseconds()
	return r
}

func mergeResult(base, got NodeResult) NodeResult {
	// Run 返回的结果优先，但保留已填充的元信息。
	if got.NodeID == "" {
		got.NodeID = base.NodeID
	}
	if got.Title == "" {
		got.Title = base.Title
	}
	if got.StartedAt == 0 {
		got.StartedAt = base.StartedAt
	}
	return got
}

// blockedBy 判断某节点的依赖是否有失败/跳过。
//
// Optional 的前置失败时允许后继继续 —— 这正是"可选任务"的语义：
// "猫猫旅行"下线了，不该连带把后面的"刷新额度"也卡死。
func (d *DAG) blockedBy(n *Node, results map[string]NodeResult) (string, bool) {
	for _, dep := range n.DependsOn {
		r, ok := results[dep]
		if !ok {
			return "前置任务 " + dep + " 未执行", true
		}
		depOptional := false
		if dn := d.nodes[dep]; dn != nil {
			depOptional = dn.Optional
		}
		if r.Skipped && !depOptional {
			return "前置任务 " + dep + " 被跳过", true
		}
		if !r.Success && !r.Skipped && !depOptional {
			return "前置任务 " + dep + " 失败", true
		}
	}
	return "", false
}

// nodeNotBefore 计算节点的最早执行时刻。
//
// 优先级：Node.NotBefore > Timeline[nodeID] > 空。
func nodeNotBefore(n *Node, timeline map[string]string) string {
	if n.NotBefore != "" {
		return n.NotBefore
	}
	if timeline != nil {
		return timeline[n.ID]
	}
	return ""
}

// reachedTime 判断当前时间是否已过 "HH:MM"。
func reachedTime(now time.Time, hhmm string) bool {
	var h, m int
	if _, err := fmt.Sscanf(hhmm, "%d:%d", &h, &m); err != nil {
		return true // 解析不了就不限制，避免永远不执行
	}
	cur := now.Hour()*60 + now.Minute()
	return cur >= h*60+m
}
