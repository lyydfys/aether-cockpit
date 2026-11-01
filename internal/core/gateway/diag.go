package gateway

import (
	"fmt"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/core/transport"
	"aether-cockpit/internal/provider"
)

// handleSpecs 返回全部渠道参数与热更新状态。
func (g *Gateway) handleSpecs(w http.ResponseWriter, r *http.Request) {
	st := spec.Default()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": st.Status(),
		"specs":  st.All(),
	})
}

// handleSpecReload 重新扫描本地 spec 目录。
func (g *Gateway) handleSpecReload(w http.ResponseWriter, r *http.Request) {
	st := spec.Default()
	if err := st.Reload(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	// spec 可能改了 models / middlewares / OAuth，路由表需要立即重建，
	// 否则"重载成功但模型列表没变"会让人误以为热更新没生效。
	g.models.Rebuild()
	eventbus.Emit(eventbus.TopicSpec+".reloaded", st.Status())
	writeJSON(w, http.StatusOK, map[string]any{
		"reloaded": true,
		"status":   st.Status(),
	})
}

// --- 自检 ---

// DiagnosticItem 是一条诊断结论。
type DiagnosticItem struct {
	// Level: ok | warn | error
	Level      string `json:"level"`
	Title      string `json:"title"`
	Detail     string `json:"detail,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// DiagnosticReport 是整体自检结果。
type DiagnosticReport struct {
	Items   []DiagnosticItem `json:"items"`
	OK      int              `json:"ok"`
	Warns   int              `json:"warns"`
	Errors  int              `json:"errors"`
	Summary string           `json:"summary"`
}

// handleDiagnose 做一次端到端自检，回答"为什么我的客户端连不上"。
//
// 设计原则：每条 error/warn 都必须给出**可执行的下一步**，
// 而不是只说"有问题"。自检的价值在于缩短排查路径，不在于罗列状态。
func (g *Gateway) handleDiagnose(w http.ResponseWriter, r *http.Request) {
	rep := &DiagnosticReport{}
	add := func(level, title, detail, suggestion string) {
		rep.Items = append(rep.Items, DiagnosticItem{
			Level: level, Title: title, Detail: detail, Suggestion: suggestion,
		})
		switch level {
		case "ok":
			rep.OK++
		case "warn":
			rep.Warns++
		default:
			rep.Errors++
		}
	}

	// 1. 监听地址与鉴权
	addr := g.cfg.Addr
	if isPublicListen(addr) {
		if g.cfg.APIKey == "" {
			add("error", "监听所有网卡但未设置 API Key",
				"当前监听 "+addr+"，同网段任何人都能使用你的账号额度",
				"改回 127.0.0.1:7860，或设置 api_key")
		} else {
			add("warn", "监听所有网卡",
				"当前监听 "+addr+"，已设置 API Key 但仍建议自用只监听本机",
				"如非必要，把 addr 改回 127.0.0.1:7860")
		}
	} else {
		add("ok", "监听地址安全", addr+"（仅本机可访问）", "")
	}

	// 2. 渠道注册
	manifests := provider.AllManifests()
	if len(manifests) == 0 {
		add("error", "没有任何已注册渠道",
			"cmd/cockpit/main.go 里没有导入任何 provider 包",
			"在入口加一行匿名导入，如 _ \"aether-cockpit/internal/providers/openaicompat\"")
	} else {
		names := make([]string, 0, len(manifests))
		for _, m := range manifests {
			names = append(names, m.ID)
		}
		add("ok", fmt.Sprintf("已注册 %d 个渠道", len(manifests)), strings.Join(names, ", "), "")
	}

	// 3. 账号与可用性
	totalReady := 0
	var unconfigured []string
	for _, m := range manifests {
		envs := g.pool.ListByProvider(m.ID)
		if len(envs) == 0 {
			// 逆向渠道骨架在未填 spec 前天然没有模型与账号；
			// 逐个报 warn 会淹没真正需要处理的问题，这里合并成一条。
			if len(m.Models) == 0 && strings.Contains(m.Description, "骨架") {
				unconfigured = append(unconfigured, m.ID)
				continue
			}
			add("warn", "渠道 "+m.ID+" 没有账号",
				"该渠道的模型会路由失败",
				"在管理界面给 "+m.ID+" 添加一个账号")
			continue
		}
		ready := 0
		for _, e := range envs {
			if e.Available() {
				ready++
			}
		}
		totalReady += ready
		if ready == 0 {
			add("error", "渠道 "+m.ID+" 的账号全部不可用",
				fmt.Sprintf("%d 个账号均处于冷却/耗尽/失效状态", len(envs)),
				"检查账号状态详情，必要时重新登录")
		} else {
			add("ok", fmt.Sprintf("渠道 %s 有 %d/%d 个可用账号", m.ID, ready, len(envs)), "", "")
		}
	}

	if len(unconfigured) > 0 {
		add("warn", fmt.Sprintf("%d 个逆向渠道骨架尚未配置", len(unconfigured)),
			strings.Join(unconfigured, ", "),
			"按 docs/ADD_PROVIDER.md 抓包后填写对应 configs/specs/<id>.json，再点「重载参数」")
	}

	if totalReady == 0 && len(manifests) > 0 {
		add("error", "全局没有可用账号",
			"任何请求都会返回 no_account_available",
			"至少给一个渠道添加账号")
	}

	// 4. 模型路由连通性
	// 逐个检查 Manifest 里登记的模型能否被 Resolve 回自己。
	// 这能挡住"模型注册了但路由表没建起来"这类装配错误。
	broken := 0
	for _, m := range manifests {
		for _, mm := range m.Models {
			fullID := mm.ID
			if !strings.Contains(fullID, "/") {
				fullID = m.ID + "/" + mm.ID
			}
			bindings := g.models.Resolve(fullID)
			found := false
			for _, b := range bindings {
				if b.ProviderID == m.ID {
					found = true
					break
				}
			}
			if !found {
				broken++
			}
		}
	}
	if broken > 0 {
		add("error", fmt.Sprintf("%d 个模型无法路由回自己的渠道", broken),
			"模型路由器索引未正确构建",
			"重启网关；若仍失败，检查该渠道的 Manifest.Models 是否为空")
	} else {
		add("ok", "模型路由表自洽", fmt.Sprintf("%d 个模型全部可解析", len(g.models.KnownModels())), "")
	}

	// 5. 虚拟模型组
	groups := g.models.VirtualGroups()
	if len(groups) == 0 {
		add("warn", "未配置虚拟模型组",
			"客户端必须写死具体渠道前缀，额度耗尽时无法自动切换",
			"在 cockpit.json 里配置 model_groups，如 {\"auto/free\": [\"ch-a\", \"ch-b\"]}")
	} else {
		for name, chain := range groups {
			if len(chain) < 2 {
				add("warn", "虚拟组 "+name+" 只有一个渠道",
					"没有 fallback 意义", "至少配置两个渠道")
				continue
			}
			resolved := g.models.Resolve(name)
			if len(resolved) == 0 {
				add("error", "虚拟组 "+name+" 无法解析",
					"组内渠道可能都未注册", "检查 model_groups 里的渠道 ID")
			} else {
				add("ok", "虚拟组 "+name+" 正常", fmt.Sprintf("%d 级 fallback", len(resolved)), "")
			}
		}
	}

	// 6. TUN 模式
	if tun := transport.DetectTUN(); tun.Detected {
		add("warn", "检测到 TUN 虚拟网卡",
			strings.Join(tun.Interfaces, ", "),
			"国内渠道的强制直连会失效，请为网关配置直连规则")
	} else {
		add("ok", "网络环境正常", "未检测到 TUN 劫持", "")
	}

	// 7. 渠道参数
	if st := spec.Default().Status(); st.Count > 0 {
		detail := fmt.Sprintf("%d 个渠道参数已加载", st.Count)
		if st.RemoteURL != "" {
			detail += "，远程热更新已启用"
		}
		if st.LastError != "" {
			add("warn", "渠道参数加载正常但远程拉取失败", st.LastError, "检查 spec_remote_url 是否可访问")
		} else {
			add("ok", "渠道参数", detail, "")
		}
	} else {
		add("ok", "未配置渠道参数（使用内置默认值）", "", "")
	}

	// 8. 会话状态
	add("ok", "会话状态", fmt.Sprintf("%d 条快照", g.store.Count()), "")

	// 汇总
	switch {
	case rep.Errors > 0:
		rep.Summary = fmt.Sprintf("发现 %d 个问题、%d 个提醒，请按建议逐条处理", rep.Errors, rep.Warns)
	case rep.Warns > 0:
		rep.Summary = fmt.Sprintf("网关可用，有 %d 个提醒", rep.Warns)
	default:
		rep.Summary = "一切正常，可以直接接入客户端"
	}

	writeJSON(w, http.StatusOK, rep)
}

// isPublicListen 判断监听地址是否对外暴露。
func isPublicListen(addr string) bool {
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "", "0.0.0.0", "::", "*":
		return true
	case "127.0.0.1", "localhost", "::1":
		return false
	default:
		return true
	}
}
