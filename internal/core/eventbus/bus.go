// Package eventbus 是内核内部的发布订阅总线。
//
// 存在的意义：把"发生了什么"与"谁关心"解耦。
// 之前请求日志、账号状态变化、签到结果都是各写各的，Wails 前端也没法实时拿到。
// 有了总线之后：
//   - 请求日志订阅 request.* 事件；
//   - Wails 前端订阅全部事件做实时推送；
//   - 将来的告警、统计都只需要新增订阅者，不需要改发布方。
package eventbus

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 事件类型常量。用 "域.动作" 的命名，便于按前缀订阅。
const (
	// TopicRequest 请求生命周期。
	TopicRequest = "request"
	// TopicAccount 账号状态变化。
	TopicAccount = "account"
	// TopicCheckin 签到结果。
	TopicCheckin = "checkin"
	// TopicProvider 渠道注册/变更。
	TopicProvider = "provider"
	// TopicSpec 渠道参数热更新。
	TopicSpec = "spec"
	// TopicSystem 系统级事件（启动/关闭）。
	TopicSystem = "system"
)

// Event 是总线上的一个事件。
type Event struct {
	// Seq 全局单调递增序号，供前端去重与断线重连后补拉。
	Seq int64 `json:"seq"`
	// Type 完整类型，形如 "request.completed"。
	Type string `json:"type"`
	// At Unix 毫秒。
	At int64 `json:"at"`
	// Payload 任意业务载荷。
	Payload any `json:"payload,omitempty"`
}

// Topic 返回事件所属的域（Type 的前半段）。
func (e Event) Topic() string {
	if i := strings.IndexByte(e.Type, '.'); i > 0 {
		return e.Type[:i]
	}
	return e.Type
}

// subscriber 是一个订阅者。
type subscriber struct {
	// prefix 为空表示订阅全部。
	prefix string
	ch     chan Event
	// dropped 统计因消费不及时被丢弃的事件数。
	dropped int64
	closed  int32
}

// Bus 是并发安全的事件总线。
//
// 语义是"尽力投递"：订阅者消费不过来时丢弃最旧的，而不是阻塞发布方。
// 理由：一个慢消费者绝不能拖垮整个网关的请求处理。
type Bus struct {
	mu   sync.RWMutex
	subs []*subscriber

	seq atomic.Int64

	// history 是环形历史，供前端初次连接时补数据。
	hmu     sync.RWMutex
	history []Event
	hcap    int
	hidx    int
	hcount  int
}

// New 创建总线。
func New(historyCapacity int) *Bus {
	if historyCapacity <= 0 {
		historyCapacity = 200
	}
	return &Bus{
		history: make([]Event, historyCapacity),
		hcap:    historyCapacity,
	}
}

// Publish 发布一个事件。
func (b *Bus) Publish(eventType string, payload any) {
	ev := Event{
		Seq:     b.seq.Add(1),
		Type:    eventType,
		At:      time.Now().UnixMilli(),
		Payload: payload,
	}

	b.hmu.Lock()
	b.history[b.hidx] = ev
	b.hidx = (b.hidx + 1) % b.hcap
	if b.hcount < b.hcap {
		b.hcount++
	}
	b.hmu.Unlock()

	b.mu.RLock()
	subs := make([]*subscriber, len(b.subs))
	copy(subs, b.subs)
	b.mu.RUnlock()

	for _, s := range subs {
		if atomic.LoadInt32(&s.closed) == 1 {
			continue
		}
		if s.prefix != "" && !matches(s.prefix, ev.Type) {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			// 消费者跟不上：丢弃最旧的一条再塞进去，保证总有最新数据。
			select {
			case <-s.ch:
				atomic.AddInt64(&s.dropped, 1)
			default:
			}
			select {
			case s.ch <- ev:
			default:
			}
		}
	}
}

// Subscribe 订阅以 prefix 开头的事件；prefix 为空表示订阅全部。
//
// 返回的事件通道带缓冲，消费方应尽快读取。取消订阅时调用返回的函数。
func (b *Bus) Subscribe(prefix string, buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 64
	}
	s := &subscriber{
		prefix: prefix,
		ch:     make(chan Event, buffer),
	}

	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			atomic.StoreInt32(&s.closed, 1)
			b.mu.Lock()
			for i, x := range b.subs {
				if x == s {
					b.subs = append(b.subs[:i], b.subs[i+1:]...)
					break
				}
			}
			b.mu.Unlock()
			close(s.ch)
		})
	}
	return s.ch, cancel
}

// matches 判断事件类型是否匹配订阅前缀（按点分段）。
//
// "request" 匹配 "request.completed"，但不匹配 "requestx.foo"。
func matches(prefix, eventType string) bool {
	if prefix == eventType {
		return true
	}
	return strings.HasPrefix(eventType, prefix+".")
}

// Recent 返回最近 n 条事件（按时间正序）。
func (b *Bus) Recent(n int) []Event {
	b.hmu.RLock()
	defer b.hmu.RUnlock()

	if n <= 0 || n > b.hcount {
		n = b.hcount
	}
	out := make([]Event, 0, n)
	start := (b.hidx - n + b.hcap) % b.hcap
	for i := 0; i < n; i++ {
		out = append(out, b.history[(start+i)%b.hcap])
	}
	return out
}

// Stats 返回总线运行状态。
type Stats struct {
	Seq        int64 `json:"seq"`
	Subscribers int  `json:"subscribers"`
	History    int   `json:"history"`
}

// Stats 返回统计。
func (b *Bus) Stats() Stats {
	b.mu.RLock()
	n := len(b.subs)
	b.mu.RUnlock()

	b.hmu.RLock()
	h := b.hcount
	b.hmu.RUnlock()

	return Stats{Seq: b.seq.Load(), Subscribers: n, History: h}
}

// --- 全局默认总线 ---
//
// 与 spec 包同样的取舍：总线是全局基础设施，插件与内核各处都可能发布，
// 一路透传参数会让签名膨胀得不可读。

var (
	defaultMu sync.RWMutex
	defaultB  = New(200)
)

// Default 返回全局总线。
func Default() *Bus {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultB
}

// SetDefault 替换全局总线（测试用）。
func SetDefault(b *Bus) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if b == nil {
		b = New(200)
	}
	defaultB = b
}

// Emit 向全局总线发布事件。
func Emit(eventType string, payload any) {
	Default().Publish(eventType, payload)
}
