package qoder

import (
	"net/http"
	"testing"

	"aether-cockpit/internal/provider"
)

// 同一账号在对话 / 额度 / 签到三个接口上的机器标识必须一致。
// 之前对话与额度固定发 machine_id，签到优先发 machine_token——两种身份并存。
func TestCosyMachineTokenPrefersCredential(t *testing.T) {
	env := provider.NewAccountEnv("acc-q", "qoder")
	env.Credentials = map[string]string{
		"user_id":       "u-1",
		"token":         "tok",
		"machine_id":    "mid-123",
		"machine_token": "mtok-456",
	}
	p := &Provider{}
	c := p.credentials(env)
	if got := firstNonEmpty(c.MachineToken, c.MachineID); got != "mtok-456" {
		t.Fatalf("应优先 machine_token，得到 %q", got)
	}

	// 没配 machine_token 时退回 machine_id（历史账号仍然可用）。
	env2 := provider.NewAccountEnv("acc-q2", "qoder")
	env2.Credentials = map[string]string{"machine_id": "mid-only"}
	c2 := p.credentials(env2)
	if got := firstNonEmpty(c2.MachineToken, c2.MachineID); got != "mid-only" {
		t.Fatalf("缺省应退回 machine_id，得到 %q", got)
	}

	// 额度接口的头也必须走同一规则。
	h := http.Header{}
	p.applyQuotaHeaders(h, c)
	if got := h.Get("Cosy-MachineToken"); got != "mtok-456" {
		t.Fatalf("额度头 Cosy-MachineToken=%q，want mtok-456", got)
	}
	if got := h.Get("Cosy-MachineId"); got != "mid-123" {
		t.Fatalf("额度头 Cosy-MachineId=%q，want mid-123", got)
	}
}
