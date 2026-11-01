package gateway

import (
	"fmt"
	"log"
	"strings"
	"sync"
)

// ProviderRouteCfg 是渠道编辑器里"请求怎么在账号之间分"的三项设置：
//
//   - Routing：账号选择方式（order / rotate / smart / weight / usage / pace / fast；
//     free 没有可用的成本元数据，按 smart 处理并记一条 WARN）；
//   - Sink：429 后仍有额度的账号排到队尾（否则保持原位，与 magpie 的 Sink 同义）；
//   - Affinity：会话粘性档位（auto / session / turn / off / strict）。
//
// 这些值之前只存在于前端草稿里，后端 providerDraft 没有对应字段，保存时被静默
// 丢掉——界面看起来能选、实际永远按默认跑。本文件让它们真正落盘并被 dispatch 使用。
type ProviderRouteCfg struct {
	Routing  string `json:"routing,omitempty"`
	Sink     bool   `json:"sink,omitempty"`
	Affinity string `json:"affinity,omitempty"`
}

var knownRouting = map[string]bool{
	"": true, "order": true, "rotate": true, "smart": true, "weight": true,
	"usage": true, "pace": true, "fast": true, "free": true, "manual": true,
}

var knownAffinity = map[string]bool{
	"": true, "auto": true, "session": true, "turn": true, "off": true, "strict": true,
}

func normalizeRouting(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if !knownRouting[v] {
		return "", fmt.Errorf("未知的 Routing: %q（支持 order/rotate/smart/weight/usage/pace/fast）", v)
	}
	return v, nil
}

// setProviderRoute 保存/合并某渠道的路由设置；三项都清空时删除该条目。
func (g *Gateway) setProviderRoute(id string, routing *string, sink *bool, affinity *string) error {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.ProviderRouting == nil {
		g.settings.ProviderRouting = map[string]ProviderRouteCfg{}
	}
	cfg := g.settings.ProviderRouting[id]
	if routing != nil {
		v, err := normalizeRouting(*routing)
		if err != nil {
			return err
		}
		cfg.Routing = v
	}
	if affinity != nil {
		v := strings.ToLower(strings.TrimSpace(*affinity))
		if !knownAffinity[v] {
			return fmt.Errorf("未知的 Stays: %q（支持 Auto/Session/Within a turn/Off/Strict）", *affinity)
		}
		cfg.Affinity = v
	}
	if sink != nil {
		cfg.Sink = *sink
	}
	if cfg.Routing == "" && !cfg.Sink && cfg.Affinity == "" {
		delete(g.settings.ProviderRouting, id)
	} else {
		g.settings.ProviderRouting[id] = cfg
	}
	return g.saveSettings()
}

// providerRoute 返回渠道的路由设置（未设置为零值）。
func (g *Gateway) providerRoute(id string) ProviderRouteCfg {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	return g.settings.ProviderRouting[id]
}

// selectMode 把 Routing 折成 pool 的选择模式；free 没有成本元数据，按 smart。
// 警告每个渠道只打一次：selectMode 在每次尝试时都会被调用，逐次打日志会刷屏。
var freeWarned sync.Map // providerID -> struct{}

func (g *Gateway) selectMode(id, routing string) string {
	if routing == "free" {
		if _, warned := freeWarned.LoadOrStore(id, struct{}{}); !warned {
			log.Printf("[WARN] 渠道 %s 的 Routing=free 没有可用的成本元数据，按 smart 处理", id)
		}
		return "smart"
	}
	return routing
}
