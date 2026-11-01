package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"aether-cockpit/internal/core/eventbus"
)

// handleEvents 以 SSE 推送内核事件流。
//
// 这是 Wails 前端与 Web UI 做实时更新的统一入口：
// 前端连一条长连接，就能收到请求完成、账号状态变化、签到结果等全部事件，
// 不需要轮询。
func (g *Gateway) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "当前连接不支持流式输出", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	bus := eventbus.Default()

	// 先补发最近的历史，让刚连上的客户端不至于空白。
	for _, ev := range bus.Recent(50) {
		if err := writeSSEEvent(w, ev); err != nil {
			return
		}
	}
	flusher.Flush()

	ch, cancel := bus.Subscribe("", 256)
	defer cancel()

	// 心跳：某些代理会在 60 秒无数据时断开连接。
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()

		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := writeSSEEvent(w, ev); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, ev eventbus.Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return nil // 序列化失败就跳过这条，不断开连接
	}
	// 用事件类型作为 SSE 的 event 名，前端可以按名字分别监听。
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
	return err
}
