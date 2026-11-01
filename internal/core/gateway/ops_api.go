package gateway

import (
	"net/http"
	"runtime"
	"runtime/debug"
	"time"

	"aether-cockpit/internal/provider"
)

// 运维端点：/healthz（存活）、/readyz（就绪）、/version（构建信息）。
//
// 企业部署里这三个是标准面：负载均衡/容器探针用前两个，排障与升级核对用
// /version。它们不依赖账号可用性（网关允许"先起服务再配置渠道"），
// readyz 只在"连一个模型都没有、无法服务任何请求"时判为未就绪。

// BuildVersion / BuildTime 可以在构建时用 -ldflags 注入：
//
//	-X aether-cockpit/internal/core/gateway.BuildVersion=0.1.0
//	-X aether-cockpit/internal/core/gateway.BuildTime=2026-10-29T01:40:00Z
var (
	BuildVersion = "dev"
	BuildTime    = ""
)

func buildInfo() (version, revision, buildTime, buildTimeSource, goVer string) {
	version = BuildVersion
	goVer = runtime.Version()
	vcsTime := ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.time":
				vcsTime = s.Value
			}
		}
	}
	buildTime, buildTimeSource = pickBuildTime(BuildTime, vcsTime)
	return
}

// pickBuildTime 选出报告给运维的"构建时间"：注入了就用注入值，否则退回
// VCS 提交时间并**标注来源**——把提交时间说成构建时间，排障时会答错
// "这个二进制是什么时候编的"。
func pickBuildTime(injected, vcs string) (value, source string) {
	if injected != "" {
		return injected, "ldflags"
	}
	if vcs != "" {
		return vcs, "vcs-commit"
	}
	return "", "unknown"
}

// handleHealthz 是存活探针：进程能响应就是 200，不查任何依赖。
func (g *Gateway) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// handleReadyz 是就绪探针：至少有一个**凭证可用**的账号才 ready。
// 一个刚启动、还没配置账号的网关"活着但不可用"，负载均衡不该把请求转给它。
// 账号处于冷却/耗尽不算未就绪：那只是暂时接不了（到点会自己恢复），网关
// 本身是健康的。凭证失效（invalid）则相反——在有人重新登录之前它永远接不
// 了请求，所以全部账号都 invalid 时必须判未就绪，否则流量会被打到死路上。
func (g *Gateway) handleReadyz(w http.ResponseWriter, r *http.Request) {
	models := len(g.models.KnownModels())
	// providers 与 /api/health 同义：**已注册**的渠道数。有账号在用的渠道数
	// 另给 providers_in_use——两处同名不同义会让运维面板对不上号。
	registered := len(provider.All())
	inUse := len(g.providerIDs())
	all := g.pool.All()
	accounts := len(all)
	usable := 0
	for _, env := range all {
		if env.Snapshot().State != provider.StateInvalid {
			usable++
		}
	}
	reasons := []string{}
	switch {
	case accounts == 0:
		reasons = append(reasons, "还没有任何账号：先在渠道页添加账号")
	case usable == 0:
		reasons = append(reasons, "所有账号的凭证都已失效（invalid）：重新登录任意一个即可恢复")
	}
	ready := len(reasons) == 0
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"ready":            ready,
		"models":           models,
		"providers":        registered,
		"providers_in_use": inUse,
		"accounts":         accounts,
		"usable_accounts":  usable,
		"uptime_sec":       int(time.Since(g.startedAt).Seconds()),
		"reasons":          reasons,
	})
}

// handleVersion 返回构建信息（不含任何配置或凭据）。
func (g *Gateway) handleVersion(w http.ResponseWriter, r *http.Request) {
	v, rev, bt, source, goVer := buildInfo()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":           v,
		"go":                goVer,
		"revision":          rev,
		"build_time":        bt,
		"build_time_source": source,
		"uptime_sec":        int(time.Since(g.startedAt).Seconds()),
	})
}

// providerIDs 返回已注册渠道 id 列表（供 readyz 计数）。
func (g *Gateway) providerIDs() []string {
	out := make([]string, 0, 8)
	for _, p := range g.pool.All() {
		out = append(out, p.ProviderID)
	}
	// 去重：同一渠道多个账号只算一个。
	seen := map[string]bool{}
	uniq := out[:0]
	for _, id := range out {
		if seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	return uniq
}
