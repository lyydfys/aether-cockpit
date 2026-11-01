package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 资源库每次渲染都要知道每个服务器被哪些 Agent 持有。直接重扫四个全局
// 配置文件在 .claude.json 很大时会明显变慢；按路径+mtime+size 缓存，
// 任何一次写入都会让签名变化并触发重扫。
//
// WSL 目标（wsl-<distro>--claude_code 这类）也一起扫：它们的 agent 名带
// 目标前缀，这样库页面的芯片对 WSL 客户端同样显示"已下发"。

type srvAgentsCache struct {
	mu  sync.Mutex
	sig string
	m   map[string][]string
}

func globalMCPConfigFiles(home string) []string {
	return []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".gemini", "settings.json"),
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".config", "opencode", "opencode.json"),
	}
}

// mcpScanHome 是一个要扫描的家目录；prefix 非空表示 WSL 目标（如 "wsl-ubuntu-22-04--"）。
type mcpScanHome struct {
	prefix string
	home   string
}

func (g *Gateway) mcpScanHomes() []mcpScanHome {
	homes := make([]mcpScanHome, 0, 3)
	if home := g.library.Home(); home != "" {
		homes = append(homes, mcpScanHome{home: home})
	}
	for _, tgt := range g.wslTargets() {
		homes = append(homes, mcpScanHome{prefix: tgt.ID + "--", home: tgt.Home})
	}
	return homes
}

func (g *Gateway) homeServerAgents() map[string][]string {
	homes := g.mcpScanHomes()
	if len(homes) == 0 {
		return nil
	}
	var sig strings.Builder
	for _, h := range homes {
		for _, p := range globalMCPConfigFiles(h.home) {
			st, err := os.Stat(p)
			if err != nil {
				fmt.Fprintf(&sig, "%s|-;", p)
				continue
			}
			fmt.Fprintf(&sig, "%s|%d|%d;", p, st.ModTime().UnixNano(), st.Size())
		}
	}
	key := sig.String()

	g.srvAgents.mu.Lock()
	defer g.srvAgents.mu.Unlock()
	if g.srvAgents.sig == key && g.srvAgents.m != nil {
		return g.srvAgents.m
	}
	m := map[string][]string{}
	for _, h := range homes {
		for _, f := range discoverHomeServers(h.home, map[string]bool{}) {
			// 同一服务器可同时存在于多个 Agent 的配置里；按名字合并，
			// 否则后一个 Agent 会覆盖前一个，视图只显示一个来源。
			agents := make([]string, 0, len(f.Agents))
			for _, a := range f.Agents {
				agents = append(agents, h.prefix+a)
			}
			m[f.Name] = dedupeStrings(append(m[f.Name], agents...))
		}
	}
	g.srvAgents.sig, g.srvAgents.m = key, m
	return m
}
