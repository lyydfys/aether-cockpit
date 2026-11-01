package gateway

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"aether-cockpit/internal/provider"
)

// 账号"休息"（被网关跳过）与手动解除。
//
// 429 / 额度耗尽会把账号推进冷却或耗尽态，路由在 resetAt 之前跳过它。
// 前端把这段状态显示在密钥行下（Resting · why · back at），并给一个
// "Try now"：用户判断上游已经恢复时，不必等到点，下一次请求就再问它一次。

// restOf 把账号的休息状态摊成前端要的形状；不在休息返回 nil。
func restOf(e *provider.AccountEnv) *aetherKeyRest {
	st, _, resetAt := e.StateDetail()
	if resetAt.IsZero() || !resetAt.After(time.Now()) {
		return nil
	}
	var why string
	switch st {
	case provider.StateCoolingDown:
		why = "rate"
	case provider.StateDepleted:
		why = "quota"
	default:
		return nil
	}
	return &aetherKeyRest{Key: e.AccountID, Why: why, Until: resetAt.UnixMilli()}
}

// handleGatewayUnrest POST /api/gateway/unrest {key}
//
// 解除某个账号的冷却/耗尽，让它从下一次请求起重新参与路由；
// 再被上游拒绝就照常重新休息。停用（off）的账号仍然不参与路由。
func (g *Gateway) handleGatewayUnrest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
	if body.Key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 key"})
		return
	}
	env, ok := g.pool.Get(body.Key)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "账号不存在: " + body.Key})
		return
	}
	env.MarkReady()
	log.Printf("[INFO] 账号 %s 已手动解除休息，下一次请求会重新尝试", env.Label)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": body.Key})
}
