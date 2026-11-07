package gateway

import (
	"net/http"
	"strings"

	"aether-cockpit/internal/providers/workbuddy"
)

// normalizeWorkBuddyIdentity 是设置保存时的白名单校验入口：委托给 provider
// 包的唯一可选项来源（NormalizeIdentityPreset），避免 gateway 里再抄一份档位表。
func normalizeWorkBuddyIdentity(name string) (string, bool) {
	return workbuddy.NormalizeIdentityPreset(name)
}

// handleWorkBuddyIdentity 暴露 WorkBuddy 渠道**运行中进程真正生效**的出网身份。
//
//	GET /api/providers/workbuddy/identity
//
// 手机端设置页据此显示 UA / 档位 / 间隔，不再镜像一份常量：
//   - identity 是 EffectiveIdentity()（进程实际在用），不是 settings.json 的保存值；
//   - 若配置里保存了 workbuddy_identity 且与生效值可能不同，额外回 saved 供客户端对比；
//     保存值为空时省略该字段；
//   - 所有字段直接取自 workbuddy.IdentityProfile，不复制第二份常量。
func (g *Gateway) handleWorkBuddyIdentity(w http.ResponseWriter, r *http.Request) {
	prof := workbuddy.EffectiveIdentity()

	out := map[string]any{
		"identity":          prof.Name,
		"ua":                prof.UA(),
		"product":           prof.Product,
		"ide_version":       prof.ClientVersion,
		"cli_version":       "",
		"stainless":         prof.Stainless(),
		"stainless_version": "",
		"pace_ms":           prof.PaceGap.Milliseconds(),
		"machine_headers":   prof.DeviceHeaders,
		"available":         workbuddy.AvailableIdentityPresets(),
	}
	// 非 CLI 档没有 CLI 段，也没有 Stainless 族：两个版本字段保持空串。
	if prof.Stainless() {
		out["cli_version"] = prof.CLIVersion
		out["stainless_version"] = prof.StainlessVersion
	}

	g.settingsMu.Lock()
	saved := strings.TrimSpace(g.settings.WorkBuddyIdentity)
	g.settingsMu.Unlock()
	if saved != "" {
		out["saved"] = saved
	}

	writeJSON(w, http.StatusOK, out)
}
