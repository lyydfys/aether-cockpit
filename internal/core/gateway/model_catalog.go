package gateway

// 上游实时模型目录：从渠道自己的 /models 一类端点拉取"今天实际可用"的
// 模型，落盘缓存并合并静态元数据。静态清单永远是兜底，拉取失败不改变
// 现有行为；拉取成功则替换该渠道的模型清单（见 aether 的 catalog 分层）。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aether-cockpit/internal/core/apppaths"
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const (
	liveModelsTTL = 24 * time.Hour
	liveModelsCap = 1000
	// metaCacheName 是 models.dev api.json 的缓存文件；下划线开头，
	// 不会被当作某个渠道的实时模型列表。
	metaCacheName = "_models.dev.json"
)

// modelsDevURL 允许用环境变量指向镜像/测试服务。
func modelsDevURL() string {
	if v := strings.TrimSpace(os.Getenv("AETHER_MODELS_DEV_URL")); v != "" {
		return v
	}
	return "https://models.dev/api.json"
}

type metaEntry struct {
	Name          string
	Context       int
	ToolCall      bool
	Reasoning     bool
	Vision        bool
	CostKnown     bool
	CostIn        float64
	CostOut       float64
	CostCacheRead float64
	Efforts       []string
	// Canonical 是 models.dev 的 canonical_model_id（如 deepseek/deepseek-v4.1-flash），
	// 用来把不同渠道的别名归到同一个可路由家族。
	Canonical string
}

type modelsDevModel struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ToolCall   bool   `json:"tool_call"`
	Reasoning  bool   `json:"reasoning"`
	Status     string `json:"status"`
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
	} `json:"limit"`
	Cost *struct {
		Input     float64 `json:"input"`
		Output    float64 `json:"output"`
		CacheRead float64 `json:"cache_read"`
	} `json:"cost"`
	ReasoningOptions []struct {
		Type   string   `json:"type"`
		Values []string `json:"values"`
	} `json:"reasoning_options"`
	CanonicalModelID string `json:"canonical_model_id"`
}

type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

type liveModelFile struct {
	ProviderID string   `json:"provider_id,omitempty"`
	Fetched    int64    `json:"fetched"`
	Base       string   `json:"base,omitempty"`
	Models     []string `json:"models"`
	// Entries 是富目录（Qoder 这类对外名 != 上游 key 的渠道）。
	Entries []provider.ModelInfo `json:"entries,omitempty"`
}

type modelCatalog struct {
	g        *Gateway
	dir      string
	mu       sync.RWMutex
	live     map[string]liveModelFile
	liveInfo map[string]provider.ModelInfo
	inflight map[string]bool
	auto     atomic.Bool
	// meta 是 models.dev 的富元数据索引（归一化 ID/别名 -> 元数据）。
	metaMu      sync.RWMutex
	meta        map[string]metaEntry
	metaFetched int64
}

func newModelCatalog(dir string, g *Gateway) *modelCatalog {
	if strings.TrimSpace(dir) == "" {
		dir = apppaths.Data("models")
	}
	c := &modelCatalog{g: g, dir: dir, live: map[string]liveModelFile{}, liveInfo: map[string]provider.ModelInfo{}, inflight: map[string]bool{}, meta: map[string]metaEntry{}}
	c.load()
	c.loadMetadata()
	return c
}

func (c *modelCatalog) load() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), "_") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(c.dir, e.Name()))
		if err != nil {
			continue
		}
		var f liveModelFile
		if json.Unmarshal(raw, &f) != nil || len(f.Models) == 0 {
			continue
		}
		pid := strings.TrimSuffix(e.Name(), ".json")
		if f.ProviderID != "" {
			pid = f.ProviderID
		}
		c.live[pid] = f
	}
	c.rebuildLiveInfo()
}

// rebuildLiveInfo 建立"归一化 ID/上游 key -> 富条目"索引，供元数据查询使用。
// 调用方需持有 c.mu 或处于初始化阶段。
func (c *modelCatalog) rebuildLiveInfo() {
	idx := make(map[string]provider.ModelInfo, len(c.live)*4)
	for pid, f := range c.live {
		for _, e := range f.Entries {
			for _, k := range []string{e.ID, e.UpstreamModel} {
				nk := normalizeModelKey(k)
				if nk == "" {
					continue
				}
				key := pid + "|" + nk
				if _, ok := idx[key]; !ok {
					idx[key] = e
				}
			}
		}
	}
	c.liveInfo = idx
}

// liveKey 把富目录条目按渠道隔离，避免不同渠道同名模型（auto 等）互相串味。
func liveKey(pid, id string) string { return pid + "|" + normalizeModelKey(id) }

func (c *modelCatalog) save(pid string, f liveModelFile) error {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(c.dir, pid+".json.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(c.dir, pid+".json"))
}

// models 返回渠道当前的模型清单：有实时目录时以它为准（元数据从静态
// 清单按归一化 ID 回填），否则退回静态清单。
func (c *modelCatalog) models(pid string) []ir.ModelMeta {
	p, ok := provider.Get(pid)
	if !ok {
		return nil
	}
	static := p.Manifest().Models
	index := staticModelIndex(static)
	c.mu.RLock()
	live, has := c.live[pid]
	c.mu.RUnlock()
	if !has || (len(live.Models) == 0 && len(live.Entries) == 0) {
		out := make([]ir.ModelMeta, len(static))
		for i, m := range static {
			out[i] = c.enrichStatic(m)
		}
		return out
	}
	if len(live.Entries) > 0 {
		return c.modelsFromEntries(pid, live.Entries, static)
	}
	out := make([]ir.ModelMeta, 0, len(live.Models))
	seen := make(map[string]bool, len(live.Models))
	for _, raw := range live.Models {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		meta := ir.ModelMeta{
			ID:            pid + "/" + id,
			UpstreamModel: id,
			DisplayName:   id,
			Capabilities:  ir.ModelCapabilities{Streaming: true, ToolCall: true},
		}
		known, ok := index[normalizeModelKey(id)]
		if ok {
			if known.DisplayName != "" {
				meta.DisplayName = known.DisplayName
			}
			meta.ContextWindow = known.ContextWindow
			meta.Capabilities = known.Capabilities
		}
		if entry, found := c.lookupMeta(id); found {
			if meta.ContextWindow == 0 {
				meta.ContextWindow = entry.Context
			}
			if meta.DisplayName == "" || meta.DisplayName == id {
				if entry.Name != "" {
					meta.DisplayName = entry.Name
				}
			}
			if !ok {
				// 静态目录没有的实时模型：能力位以 models.dev 为准。
				meta.Capabilities.ToolCall = entry.ToolCall
				meta.Capabilities.Reasoning = entry.Reasoning
				meta.Capabilities.Vision = entry.Vision
			}
		}
		out = append(out, meta)
	}
	return out
}

// enrichStatic 给没有实时目录的静态模型补上 models.dev 的显示名与上下文。
func (c *modelCatalog) enrichStatic(m ir.ModelMeta) ir.ModelMeta {
	if m.DisplayName != "" && m.ContextWindow > 0 {
		return m
	}
	id := m.UpstreamModel
	if id == "" {
		id = m.ID
	}
	if e, ok := c.lookupMeta(id); ok {
		if m.DisplayName == "" {
			m.DisplayName = e.Name
		}
		if m.ContextWindow == 0 {
			m.ContextWindow = e.Context
		}
	}
	return m
}

// modelsFromEntries 用富目录构建模型清单：对外 ID（显示名）与上游 key 分离。
func (c *modelCatalog) modelsFromEntries(pid string, entries []provider.ModelInfo, static []ir.ModelMeta) []ir.ModelMeta {
	index := staticModelIndex(static)
	out := make([]ir.ModelMeta, 0, len(entries))
	for _, e := range entries {
		id := strings.TrimSpace(e.ID)
		if id == "" {
			id = strings.TrimSpace(e.UpstreamModel)
		}
		if id == "" {
			continue
		}
		upstream := strings.TrimSpace(e.UpstreamModel)
		if upstream == "" {
			upstream = id
		}
		name := strings.TrimSpace(e.Name)
		if name == "" {
			name = id
		}
		meta := ir.ModelMeta{
			ID:            pid + "/" + id,
			UpstreamModel: upstream,
			DisplayName:   name,
			ContextWindow: e.Context,
			Capabilities:  e.Capabilities,
		}
		known, staticFound := index[normalizeModelKey(id)]
		if !staticFound {
			known, staticFound = index[normalizeModelKey(upstream)]
		}
		if staticFound {
			if meta.ContextWindow == 0 {
				meta.ContextWindow = known.ContextWindow
			}
			if meta.Capabilities == (ir.ModelCapabilities{}) {
				meta.Capabilities = known.Capabilities
			}
		}
		md, metaFound := c.lookupMeta(id)
		if !metaFound {
			md, metaFound = c.lookupMeta(upstream)
		}
		if metaFound {
			if meta.ContextWindow == 0 {
				meta.ContextWindow = md.Context
			}
			if meta.DisplayName == "" || meta.DisplayName == id {
				if md.Name != "" {
					meta.DisplayName = md.Name
				}
			}
		}
		out = append(out, meta)
	}
	return out
}

func staticModelIndex(models []ir.ModelMeta) map[string]ir.ModelMeta {
	idx := make(map[string]ir.ModelMeta, len(models)*2)
	for _, m := range models {
		for _, key := range []string{m.UpstreamModel, m.ID} {
			if k := normalizeModelKey(key); k != "" {
				if _, dup := idx[k]; !dup {
					idx[k] = m
				}
			}
		}
	}
	return idx
}

// normalizeModelKey 把大小写、连字符、点号和渠道前缀抹平，用于把上游
// 的 "DeepSeek-V4.1-Flash" 与静态目录里的 "deepseek-v4.1-flash" 对上。
func normalizeModelKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (c *modelCatalog) supportsOn(pid string, env *provider.AccountEnv) bool {
	p, ok := provider.Get(pid)
	if !ok {
		return false
	}
	if _, ok := p.(provider.ModelLister); !ok {
		return false
	}
	if s, ok := p.(provider.ModelListSupport); ok {
		return s.SupportsModelList(env)
	}
	return true
}

func (c *modelCatalog) pickEnv(pid string) *provider.AccountEnv {
	var fallback *provider.AccountEnv
	for _, env := range c.g.pool.ListByProvider(pid) {
		if env == nil || env.Disabled() {
			continue
		}
		if env.Available() {
			return env
		}
		if fallback == nil {
			fallback = env
		}
	}
	return fallback
}

// supports 表示该渠道现在是否能执行"从上游刷新"（有账号且端点可用）。
func (c *modelCatalog) supports(pid string) bool {
	env := c.pickEnv(pid)
	if env == nil {
		return false
	}
	return c.supportsOn(pid, env)
}

func (c *modelCatalog) source(pid string) (string, int64) {
	c.mu.RLock()
	live, ok := c.live[pid]
	c.mu.RUnlock()
	if ok && len(live.Models) > 0 {
		return "live", live.Fetched
	}
	return "static", 0
}

func (c *modelCatalog) refresh(ctx context.Context, pid string) (int, error) {
	p, ok := provider.Get(pid)
	if !ok {
		return 0, errors.New("渠道未注册: " + pid)
	}
	lister, ok := p.(provider.ModelLister)
	if !ok {
		return 0, errors.New("渠道 " + pid + " 不支持从上游获取模型")
	}
	env := c.pickEnv(pid)
	if env == nil {
		return 0, errors.New("渠道 " + pid + " 没有可用账号")
	}
	if !c.supportsOn(pid, env) {
		return 0, errors.New("渠道 " + pid + " 没有可用的模型列表端点")
	}
	c.mu.Lock()
	if c.inflight[pid] {
		c.mu.Unlock()
		return 0, errors.New("模型目录正在刷新，请稍后")
	}
	c.inflight[pid] = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.inflight, pid)
		c.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var (
		ids     []string
		entries []provider.ModelInfo
		err     error
	)
	if rich, ok := p.(provider.RichModelLister); ok {
		entries, err = rich.ListModelInfos(ctx, env)
		if err != nil {
			return 0, err
		}
		cleanEntries := make([]provider.ModelInfo, 0, len(entries))
		seen := make(map[string]bool, len(entries))
		for _, e := range entries {
			id := strings.TrimSpace(e.ID)
			if id == "" {
				id = strings.TrimSpace(e.UpstreamModel)
			}
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			e.ID = id
			if strings.TrimSpace(e.UpstreamModel) == "" {
				e.UpstreamModel = id
			}
			cleanEntries = append(cleanEntries, e)
			ids = append(ids, id)
		}
		entries = cleanEntries
	} else {
		ids, err = lister.ListModels(ctx, env)
		if err != nil {
			return 0, err
		}
		clean := make([]string, 0, len(ids))
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			clean = append(clean, id)
		}
		ids = clean
	}
	if len(ids) == 0 {
		return 0, errors.New("上游返回了空模型列表")
	}
	if len(ids) > liveModelsCap {
		ids = ids[:liveModelsCap]
	}
	if len(entries) > liveModelsCap {
		entries = entries[:liveModelsCap]
	}
	f := liveModelFile{
		ProviderID: pid,
		Fetched:    time.Now().Unix(),
		Base:       c.g.providerBaseURL(pid),
		Models:     ids,
		Entries:    entries,
	}
	if err := c.save(pid, f); err != nil {
		return 0, err
	}
	c.mu.Lock()
	c.live[pid] = f
	c.rebuildLiveInfo()
	c.mu.Unlock()
	if c.g.models != nil {
		c.g.models.Rebuild()
	}
	log.Printf("[CATALOG] %s 从上游刷新模型目录：%d 个", pid, len(ids))
	return len(ids), nil
}

func (c *modelCatalog) maybeRefresh(pid string) {
	if !c.auto.Load() {
		return
	}
	c.mu.RLock()
	live, ok := c.live[pid]
	c.mu.RUnlock()
	if ok && time.Since(time.Unix(live.Fetched, 0)) < liveModelsTTL {
		return
	}
	if _, err := c.refresh(context.Background(), pid); err != nil {
		log.Printf("[CATALOG] %s 自动刷新失败：%v", pid, err)
	}
}

// Start 在网关启动后调用：对支持模型列表且已有账号的渠道异步刷新一次。
func (c *modelCatalog) Start() {
	c.auto.Store(true)
	go c.ensureMetadata()
	for _, p := range provider.All() {
		pid := p.Manifest().ID
		if !c.supports(pid) {
			continue
		}
		go c.maybeRefresh(pid)
	}
}

// catalogModels 是网关注入给路由表 / 管理接口的模型来源。
func (g *Gateway) catalogModels(pid string) []ir.ModelMeta {
	if g.catalog == nil {
		if p, ok := provider.Get(pid); ok {
			return p.Manifest().Models
		}
		return nil
	}
	return g.catalog.models(pid)
}

// StartModelCatalog 由进程入口在网关构造完成后调用；测试不调用，
// 避免单测产生真实网络请求。
func (g *Gateway) StartModelCatalog() {
	if g.catalog != nil {
		g.catalog.Start()
	}
}

// handleRefreshProviderModels POST /api/providers/{id}/models/refresh
// 手动从上游拉一次模型目录；失败保留原有缓存（静态或上次实时）。
func (g *Gateway) handleRefreshProviderModels(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少渠道 id"})
		return
	}
	if g.catalog == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "模型目录未初始化"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	n, err := g.catalog.refresh(ctx, id)
	if err != nil {
		status := http.StatusBadGateway
		for _, hint := range []string{"不支持", "没有可用", "正在刷新"} {
			if strings.Contains(err.Error(), hint) {
				status = http.StatusBadRequest
				break
			}
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	source, fetched := g.catalog.source(id)
	writeJSON(w, http.StatusOK, map[string]any{
		"provider_id": id,
		"models":      n,
		"source":      source,
		"fetched":     fetched,
	})
}

// --- models.dev 富元数据（第二层） ---

func (c *modelCatalog) loadMetadata() {
	path := filepath.Join(c.dir, metaCacheName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	idx, err := parseModelsDev(raw)
	if err != nil || len(idx) == 0 {
		return
	}
	fetched := int64(0)
	if st, err := os.Stat(path); err == nil {
		fetched = st.ModTime().Unix()
	}
	c.metaMu.Lock()
	c.meta = idx
	c.metaFetched = fetched
	c.metaMu.Unlock()
}

func (c *modelCatalog) saveMetadata(raw []byte) error {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(c.dir, metaCacheName+".tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(c.dir, metaCacheName))
}

func (c *modelCatalog) refreshMetadata(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevURL(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AetherCockpit/model-catalog")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("models.dev 返回 " + resp.Status)
	}
	idx, err := parseModelsDev(raw)
	if err != nil {
		return err
	}
	if err := c.saveMetadata(raw); err != nil {
		return err
	}
	c.metaMu.Lock()
	c.meta = idx
	c.metaFetched = time.Now().Unix()
	c.metaMu.Unlock()
	if c.g != nil && c.g.models != nil {
		c.g.models.Rebuild()
	}
	log.Printf("[CATALOG] models.dev 元数据已更新：%d 个别名", len(idx))
	return nil
}

func (c *modelCatalog) ensureMetadata() {
	c.metaMu.RLock()
	fresh := len(c.meta) > 0 && time.Since(time.Unix(c.metaFetched, 0)) < liveModelsTTL
	c.metaMu.RUnlock()
	if fresh {
		return
	}
	if err := c.refreshMetadata(context.Background()); err != nil {
		log.Printf("[CATALOG] models.dev 元数据刷新失败：%v", err)
	}
}

func (c *modelCatalog) lookupMeta(id string) (metaEntry, bool) {
	key := normalizeModelKey(id)
	if key == "" || genericModelKey(key) {
		return metaEntry{}, false
	}
	c.metaMu.RLock()
	defer c.metaMu.RUnlock()
	e, ok := c.meta[key]
	return e, ok
}

// genericModelKey 是渠道自己的路由器/档位名，不是某个具体模型；
// 用 models.dev 的同名条目补它会张冠李戴（例如 Qoder 的 auto 被补成 2M）。
func genericModelKey(k string) bool {
	switch k {
	case "auto", "default", "chat", "fast", "slow", "balanced", "efficient",
		"performance", "ultimate", "pro", "flash", "lite", "mini":
		return true
	}
	return false
}

// parseModelsDev 把 api.json 摊平成"归一化 ID / 别名 / 显示名 -> 元数据"。
// 非 deprecated 优先；同键冲突取先出现的条目。
func parseModelsDev(raw []byte) (map[string]metaEntry, error) {
	var root map[string]modelsDevProvider
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	primary := map[string]metaEntry{}
	deprecated := map[string]metaEntry{}
	providerIDs := make([]string, 0, len(root))
	for id := range root {
		providerIDs = append(providerIDs, id)
	}
	sort.Strings(providerIDs)
	for _, providerID := range providerIDs {
		p := root[providerID]
		keys := make([]string, 0, len(p.Models))
		for key := range p.Models {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			m := p.Models[key]
			e := metaEntry{
				Name:      m.Name,
				Context:   m.Limit.Context,
				ToolCall:  m.ToolCall,
				Reasoning: m.Reasoning,
				Vision:    metaHasImage(m.Modalities.Input),
				Canonical: strings.TrimSpace(m.CanonicalModelID),
			}
			if m.Cost != nil {
				e.CostKnown = true
				e.CostIn = m.Cost.Input
				e.CostOut = m.Cost.Output
				e.CostCacheRead = m.Cost.CacheRead
			}
			for _, opt := range m.ReasoningOptions {
				if opt.Type == "effort" && len(opt.Values) > 0 {
					e.Efforts = append([]string(nil), opt.Values...)
				}
			}
			target := primary
			if strings.EqualFold(m.Status, "deprecated") {
				target = deprecated
			}
			aliases := []string{key, m.ID, m.CanonicalModelID, m.Name, providerID + "/" + key, providerID + "/" + m.ID}
			for _, k := range aliases {
				nk := normalizeModelKey(k)
				if nk == "" {
					continue
				}
				if _, exists := target[nk]; !exists {
					target[nk] = e
				}
			}
		}
	}
	for k, v := range deprecated {
		if _, ok := primary[k]; !ok {
			primary[k] = v
		}
	}
	if len(primary) == 0 {
		return nil, errors.New("models.dev 目录为空")
	}
	return primary, nil
}

func metaHasImage(mods []string) bool {
	for _, m := range mods {
		if strings.Contains(strings.ToLower(m), "image") {
			return true
		}
	}
	return false
}

// modelCatalogInfo 是模型列表要展示的富信息（实时目录 + models.dev）。
type modelCatalogInfo struct {
	Context       int
	CostKnown     bool
	CostIn        float64
	CostOut       float64
	CostCacheRead float64
	Efforts       []string
	Rate          float64
	Free          bool
}

func (c *modelCatalog) info(modelID string) modelCatalogInfo {
	if pid, mid := splitProviderModel(modelID); pid != "" {
		return c.infoFor(pid, mid)
	}
	// 裸模型名（虚拟组）没有渠道上下文，只用 models.dev 的公共元数据。
	out := modelCatalogInfo{}
	if e, found := c.lookupMeta(modelID); found {
		out.Context = e.Context
		out.Efforts = append([]string(nil), e.Efforts...)
		out.CostKnown = e.CostKnown
		out.CostIn = e.CostIn
		out.CostOut = e.CostOut
		out.CostCacheRead = e.CostCacheRead
	}
	return out
}

// infoFor 读取指定渠道的富条目 + models.dev 公共元数据。
func (c *modelCatalog) infoFor(pid, modelID string) modelCatalogInfo {
	out := modelCatalogInfo{}
	c.mu.RLock()
	live, ok := c.liveInfo[liveKey(pid, modelID)]
	c.mu.RUnlock()
	if ok {
		out.Context = live.Context
		out.Efforts = append([]string(nil), live.Efforts...)
		out.Rate = live.Rate
		out.Free = live.Free
	}
	if e, found := c.lookupMeta(modelID); found {
		if out.Context == 0 {
			out.Context = e.Context
		}
		if len(out.Efforts) == 0 {
			out.Efforts = append([]string(nil), e.Efforts...)
		}
		out.CostKnown = e.CostKnown
		out.CostIn = e.CostIn
		out.CostOut = e.CostOut
		out.CostCacheRead = e.CostCacheRead
	}
	return out
}

// aliasesFor 返回模型的归一化别名（canonical_model_id 与显示名），
// 供路由层把 "DeepSeek-V4.1-Flash" 这类写法映射到各渠道的实际模型。
func (c *modelCatalog) aliasesFor(pid, modelID string) []string {
	keys := make([]string, 0, 3)
	seen := map[string]bool{}
	add := func(s string) {
		k := normalizeModelKey(s)
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		keys = append(keys, k)
	}
	if e, ok := c.lookupMeta(modelID); ok {
		add(e.Canonical)
		add(e.Name)
	}
	c.mu.RLock()
	live, ok := c.liveInfo[liveKey(pid, modelID)]
	c.mu.RUnlock()
	if ok {
		add(live.Name)
	}
	return keys
}

// splitProviderModel 拆出已注册渠道前缀；裸模型名返回空 pid。
func splitProviderModel(model string) (string, string) {
	if i := strings.IndexByte(model, '/'); i > 0 {
		if _, ok := provider.Get(model[:i]); ok {
			return model[:i], model[i+1:]
		}
	}
	return "", model
}

// catalogInfoFor 供管理接口按渠道取元数据。
func (g *Gateway) catalogInfoFor(pid, modelID string) modelCatalogInfo {
	if g.catalog == nil {
		return modelCatalogInfo{}
	}
	return g.catalog.infoFor(pid, modelID)
}

// catalogInfo 供 /api/providers 的模型视图使用；目录未初始化时返回空。
func (g *Gateway) catalogInfo(modelID string) modelCatalogInfo {
	if g.catalog == nil {
		return modelCatalogInfo{}
	}
	return g.catalog.info(modelID)
}

// priceFromCatalog 用 models.dev 的价格兜底（美元 / 百万 tokens）。
// 本地价目表与用户覆盖优先；这里只补没收录的模型。
func (g *Gateway) priceFromCatalog(model string) (ModelPrice, bool) {
	if g.catalog == nil {
		return ModelPrice{}, false
	}
	info := g.catalog.info(model)
	if info.Free {
		return ModelPrice{}, true
	}
	if !info.CostKnown {
		return ModelPrice{}, false
	}
	return ModelPrice{
		InputPerM:      info.CostIn,
		OutputPerM:     info.CostOut,
		CachedReadPerM: info.CostCacheRead,
	}, true
}

// --- reasoning effort 归一化（用实时目录里的档位约束下游请求） ---

var effortRank = map[string]int{"low": 1, "medium": 2, "high": 3, "xhigh": 4, "max": 5}

func normalizeEffortRequest(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	switch s {
	case "", "none", "off", "disabled":
		return ""
	case "minimal":
		return "low"
	case "extra-high", "extra high", "x-high":
		return "xhigh"
	}
	return s
}

// nearestEffort 在模型支持的档位里找最接近的；等距时取更低档（更省）。
func nearestEffort(want string, supported []string) string {
	wr, ok := effortRank[want]
	if !ok {
		wr = effortRank["medium"]
	}
	best, bestDist := "", 1<<30
	for _, s := range supported {
		sr, ok := effortRank[strings.ToLower(strings.TrimSpace(s))]
		if !ok {
			continue
		}
		d := sr - wr
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			best, bestDist = strings.ToLower(strings.TrimSpace(s)), d
		}
	}
	return best
}

// clampReasoningEffort 把客户端的 effort 收窄到模型支持的档位：
//   - none/off 直接去掉；
//   - minimal 归一到 low；
//   - 模型有档位目录时，不支持的值映射到最近档；
//   - 没有元数据时原样（只做别名归一）。
func (g *Gateway) clampReasoningEffort(model, effort string) string {
	want := normalizeEffortRequest(effort)
	if want == "" {
		return ""
	}
	info := g.catalogInfo(model)
	if len(info.Efforts) == 0 {
		return want
	}
	for _, s := range info.Efforts {
		if strings.EqualFold(strings.TrimSpace(s), want) {
			return want
		}
	}
	if n := nearestEffort(want, info.Efforts); n != "" {
		return n
	}
	return want
}
