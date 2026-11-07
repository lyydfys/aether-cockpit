package gateway

// 用户自定义渠道。
//
// 内置渠道是编译期常量（internal/core/channels），好处是开箱即用，坏处是
// 用户手上一堆"OpenAI 兼容"的中转/自建端点时就只能等发版。这里补一条
// **运行时注册**的路：保存到设置 → 立刻注册成 provider → 出现在渠道页，
// 与内置渠道共用同一套实现（同一个 openaicompat 实例、同一套中间件与
// 账号池）。
//
// 刻意不做的三件事：
//   - 不支持脚本/钩子：自定义渠道是"配置"，不是插件。任意代码执行会让
//     "单二进制、零外挂进程"的定位失效，也会把安全边界从"填错地址"扩大到
//     "执行任意代码"。
//   - 不保存 API Key：密钥仍然放在账号里（加密存储、可单独删除）。渠道只是
//     "地址 + 模型名"，删渠道不会顺带泄露或遗失密钥。
//   - 不自动探测模型清单：探测是显式动作（probe），因为很多中转对
//     /models 要鉴权，静默探测会刷失败记录。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/openaicompat"
)

// CustomChannel 是用户自定义渠道的持久化形态。
type CustomChannel struct {
	// ID 是用户可见的短标识（渠道 id = "custom-" + ID）。
	ID string `json:"id"`
	// Name 是展示名。
	Name string `json:"name"`
	// BaseURL 是上游地址（要带版本路径，如 https://api.example.com/v1）。
	BaseURL string `json:"base_url"`
	// Protocol 决定走哪个上游端点：openai（默认）/ anthropic / responses。
	Protocol string `json:"protocol,omitempty"`
	// Models 是模型名列表（不含渠道前缀）。留空表示"任意模型名都透传"。
	Models []string `json:"models,omitempty"`
	// Headers 是附加请求头（可选）。值支持 {{api_key}} 占位符。
	Headers map[string]string `json:"headers,omitempty"`
	// CreatedAt 仅用于排序展示。
	CreatedAt int64 `json:"created_at,omitempty"`
}

// customChannelProviderID 是内部 provider id。
//
// 加前缀是为了和内置渠道名空间分开：用户可能把自定义渠道叫 "deepseek"，
// 直接占用内置 ID 会把预设顶掉。
func customChannelProviderID(id string) string { return "custom-" + id }

// slugifyCustomID 把用户输入变成合法 id（小写字母/数字/连字符）。
func slugifyCustomID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

// validCustomBaseURL 只接受 http(s) 绝对地址。
//
// 拒绝其它 scheme（file://、gopher:// 之类）：这个地址会被网关拿去发请求，
// 放开 scheme 等于给了一个任意协议客户端。
func validCustomBaseURL(raw string) error {
	u := strings.TrimSpace(raw)
	if u == "" {
		return fmt.Errorf("Base URL 不能为空")
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return fmt.Errorf("Base URL 必须以 http:// 或 https:// 开头")
	}
	if strings.ContainsAny(u, " \t\r\n") {
		return fmt.Errorf("Base URL 不能包含空白字符")
	}
	return nil
}

var customHeaderKey = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// normalizeCustomChannel 校验并补全字段。
func normalizeCustomChannel(in CustomChannel) (CustomChannel, error) {
	if err := validCustomBaseURL(in.BaseURL); err != nil {
		return in, err
	}
	out := in
	out.Name = strings.TrimSpace(out.Name)
	if out.Name == "" {
		out.Name = "自定义渠道"
	}
	switch strings.ToLower(strings.TrimSpace(out.Protocol)) {
	case "", "openai", "chat":
		out.Protocol = "openai"
	case "anthropic", "messages":
		out.Protocol = "anthropic"
	case "responses":
		out.Protocol = "responses"
	default:
		return in, fmt.Errorf("协议只能是 openai / anthropic / responses")
	}
	out.BaseURL = strings.TrimRight(strings.TrimSpace(out.BaseURL), "/")

	models := make([]string, 0, len(out.Models))
	seen := map[string]bool{}
	for _, m := range out.Models {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		models = append(models, m)
	}
	out.Models = models

	if len(out.Headers) > 0 {
		clean := make(map[string]string, len(out.Headers))
		for k, v := range out.Headers {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			if !customHeaderKey.MatchString(k) {
				return in, fmt.Errorf("请求头名 %q 不合法（只允许字母/数字/连字符）", k)
			}
			// 禁止用自定义头覆盖鉴权与主机，避免"填个头就把 key 寄到别处"。
			switch strings.ToLower(k) {
			case "authorization", "host", "content-length", "x-api-key", "api-key":
				return in, fmt.Errorf("请求头 %q 由网关管理，不能在自定义渠道里覆盖", k)
			}
			clean[k] = v
		}
		out.Headers = clean
	}
	if out.ID == "" {
		out.ID = slugifyCustomID(out.Name)
	}
	out.ID = slugifyCustomID(out.ID)
	if out.ID == "" {
		return in, fmt.Errorf("渠道标识不能为空（可用字母/数字）")
	}
	return out, nil
}

// manifestForCustom 把自定义渠道转成 provider 配置。
func manifestForCustom(c CustomChannel) openaicompat.Config {
	pid := customChannelProviderID(c.ID)
	models := make([]ir.ModelMeta, 0, len(c.Models))
	for _, m := range c.Models {
		models = append(models, ir.ModelMeta{
			ID:            pid + "/" + m,
			UpstreamModel: m,
			DisplayName:   m,
		})
	}
	cfg := openaicompat.Config{
		ID:          pid,
		DisplayName: c.Name,
		Family:      "custom",
		RegionTag:   "",
		BrandColor:  "#5B6470",
		Description: "用户自定义渠道（" + c.BaseURL + "）",
		BaseURL:     c.BaseURL,
		Models:      models,
		// 自定义渠道表单里的附加请求头（如中转站要求的 X-Api-Name / X-Tenant），
		// 之前只保存不生效；这里随 provider 配置一起下发（值支持 {{api_key}}）。
		Headers: c.Headers,
		// 自定义渠道没有内置的额度读取器；不声明 Quota 能力，
		// 页面就不会把它显示成"额度读取失败"。
		NoQuota: true,
	}
	switch c.Protocol {
	case "anthropic":
		cfg.AllowAnthropicUpstream = true
	case "responses":
		cfg.AllowResponsesUpstream = true
	}
	return cfg
}

// syncCustomChannels 把设置里的自定义渠道注册进 provider 注册表。
//
// 先注册后反注册的顺序很重要：改名（id 变了）时如果先删旧的，中间会出现
// 一小段"两个都没有"的空窗，正在跑的请求会选不到渠道。
func (g *Gateway) syncCustomChannels() {
	g.settingsMu.Lock()
	list := make([]CustomChannel, len(g.settings.CustomChannels))
	copy(list, g.settings.CustomChannels)
	g.settingsMu.Unlock()

	want := make(map[string]bool, len(list))
	for _, c := range list {
		if strings.TrimSpace(c.BaseURL) == "" || c.ID == "" {
			continue
		}
		pid := customChannelProviderID(c.ID)
		want[pid] = true
		openaicompat.RegisterDynamic(manifestForCustom(c))
	}
	// 清理：设置里已经删掉的、且过去是我们注册的渠道。
	for _, pid := range g.customProviderIDs() {
		if !want[pid] {
			openaicompat.UnregisterDynamic(pid)
		}
	}
	g.setCustomProviderIDs(customIDKeys(want))

	// 渠道刚刚（重新）注册进注册表：此前因为"渠道未注册"而只能留在 preserved 里的
	// 账号，现在应该立刻回到池里，而不是等到下次重启——否则用户重新添加渠道后
	// 仍会看到"账号清零"，得重启 App 才恢复。
	if promoted, err := g.pool.PromoteUnloaded(g.tr); err != nil {
		log.Printf("[WARN] 提升未就绪账号失败: %v", err)
	} else if promoted > 0 {
		log.Printf("自定义渠道变更后把 %d 个未就绪账号提升回池", promoted)
	}
}

// customProviderIDs 记录"当前由自定义渠道注册的 provider id"。
//
// 不能靠 provider 列表的 ID 前缀反推吗？可以，但那样会把用户从 spec 注册的
// 同名渠道也一起删掉。单独记一份最稳。放内存即可：启动时会重新注册一遍。
func (g *Gateway) customProviderIDs() []string {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	return append([]string(nil), g.customIDs...)
}

func (g *Gateway) setCustomProviderIDs(ids []string) {
	g.settingsMu.Lock()
	g.customIDs = ids
	g.settingsMu.Unlock()
}

func customIDKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// upsertCustomChannelFromDraft 把「自定义供应商」草稿落成一条真正的
// CustomChannel（创建或按 slug 更新），并立即注册 provider。
//
// 返回 (渠道, true, nil) 表示可走自定义渠道；ok=false 表示草稿里没有
// Chat/Responses/Anthropic 端点（仅 Gemini/Decide），调用方保持旧路径。
// 幂等判据是渠道 ID（slug）是否已存在：重复保存同一供应商只更新不新建。
func (g *Gateway) upsertCustomChannelFromDraft(d *providerDraft) (CustomChannel, bool, error) {
	base, protocol := "", ""
	switch {
	case trimEndpoint(d.Chat) != "":
		base, protocol = trimEndpoint(d.Chat), "openai"
	case trimEndpoint(d.Responses) != "":
		base, protocol = trimEndpoint(d.Responses), "responses"
	case trimEndpoint(d.Anthropic) != "":
		base, protocol = trimEndpoint(d.Anthropic), "anthropic"
	}

	slug := slugifyCustomID(strings.TrimPrefix(strings.TrimSpace(d.ID), "custom-"))
	if slug == "" {
		slug = slugifyCustomID(d.Name)
	}
	if slug == "" && base != "" {
		// 前端没给 id/name 时用地址派生，保证同一供应商重复保存仍是同一个 slug。
		slug = slugifyCustomID("relay-" + shortHash(base))
	}
	if slug == "" {
		return CustomChannel{}, false, nil
	}

	g.settingsMu.Lock()
	idx := -1
	var ch CustomChannel
	for i := range g.settings.CustomChannels {
		if g.settings.CustomChannels[i].ID == slug {
			idx = i
			ch = g.settings.CustomChannels[i]
			break
		}
	}
	if idx < 0 {
		ch = CustomChannel{ID: slug, CreatedAt: time.Now().Unix()}
	}
	if name := strings.TrimSpace(d.Name); name != "" {
		ch.Name = name
	}
	if ch.Name == "" {
		ch.Name = slug
	}
	// nil = 本次没提交该字段（保留旧值）；空数组 = 明确清空。
	if d.Models != nil {
		ch.Models = cleanProviderModels(d.Models)
	}
	if d.Headers != nil {
		ch.Headers = cloneStringMap(d.Headers)
	}
	if base != "" {
		ch.BaseURL, ch.Protocol = base, protocol
	}
	if strings.TrimSpace(ch.BaseURL) == "" {
		g.settingsMu.Unlock()
		return CustomChannel{}, false, nil
	}
	norm, err := normalizeCustomChannel(ch)
	if err != nil {
		g.settingsMu.Unlock()
		return CustomChannel{}, false, err
	}
	if idx >= 0 {
		g.settings.CustomChannels[idx] = norm
	} else {
		g.settings.CustomChannels = append(g.settings.CustomChannels, norm)
	}
	g.settingsMu.Unlock()

	if err := g.saveSettings(); err != nil {
		return CustomChannel{}, false, err
	}
	g.syncCustomChannels()
	if g.models != nil {
		// 新渠道的 manifest 立即进入路由表，不用等下一次模型刷新。
		g.models.Rebuild()
	}
	return norm, true, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// handleCustomChannels GET /api/custom-channels
func (g *Gateway) handleCustomChannels(w http.ResponseWriter, r *http.Request) {
	g.settingsMu.Lock()
	list := make([]CustomChannel, len(g.settings.CustomChannels))
	copy(list, g.settings.CustomChannels)
	g.settingsMu.Unlock()
	for i := range list {
		list[i].ID = strings.TrimPrefix(list[i].ID, "custom-")
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": list})
}

// handleCustomChannelSave POST /api/custom-channels
//
// 有 ID 且已存在则视为更新，否则新建。
func (g *Gateway) handleCustomChannelSave(w http.ResponseWriter, r *http.Request) {
	var body CustomChannel
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	ch, err := normalizeCustomChannel(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	g.settingsMu.Lock()
	replaced := false
	for i := range g.settings.CustomChannels {
		if g.settings.CustomChannels[i].ID == ch.ID {
			ch.CreatedAt = g.settings.CustomChannels[i].CreatedAt
			g.settings.CustomChannels[i] = ch
			replaced = true
			break
		}
	}
	if !replaced {
		ch.CreatedAt = time.Now().Unix()
		g.settings.CustomChannels = append(g.settings.CustomChannels, ch)
	}
	g.settingsMu.Unlock()

	if err := g.saveSettings(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	g.syncCustomChannels()
	ch.ID = strings.TrimPrefix(ch.ID, "custom-")
	writeJSON(w, http.StatusOK, map[string]any{"channel": ch})
}

// handleCustomChannelDelete DELETE /api/custom-channels/{id}
func (g *Gateway) handleCustomChannelDelete(w http.ResponseWriter, r *http.Request) {
	id := slugifyCustomID(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少渠道 ID"})
		return
	}
	g.settingsMu.Lock()
	kept := g.settings.CustomChannels[:0]
	found := false
	for _, c := range g.settings.CustomChannels {
		if c.ID == id {
			found = true
			continue
		}
		kept = append(kept, c)
	}
	g.settings.CustomChannels = kept
	g.settingsMu.Unlock()
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个自定义渠道"})
		return
	}
	// 账号不自动删：用户可能只是想把渠道定义清掉、稍后用同名恢复。
	// 这里只下线 provider，账号留在池里（页面会显示成"渠道不存在"）。
	if err := g.saveSettings(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	g.syncCustomChannels()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCustomChannelProbe POST /api/custom-channels/probe
//
// 显式探测上游 /models，帮助用户填模型清单。不保存任何东西。
func (g *Gateway) handleCustomChannelProbe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	base := strings.TrimRight(strings.TrimSpace(body.BaseURL), "/")
	if err := validCustomBaseURL(base); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	endpoint := base + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "构造请求失败: " + err.Error()})
		return
	}
	if k := strings.TrimSpace(body.APIKey); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	req.Header.Set("Accept", "application/json")
	client, cerr := g.tr.NewClient(provider.NetPolicySystem, "", "")
	if cerr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "构造 HTTP 客户端失败: " + cerr.Error()})
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "连不上上游: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "status": resp.StatusCode,
			"error": fmt.Sprintf("上游返回 HTTP %d（请检查地址与 Key）", resp.StatusCode),
		})
		return
	}
	// 两种常见形状：OpenAI 的 {data:[{id}]} 与裸数组。
	var doc struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	models := make([]string, 0, 16)
	if err := json.Unmarshal(raw, &doc); err == nil && len(doc.Data) > 0 {
		for _, m := range doc.Data {
			if m.ID != "" {
				models = append(models, m.ID)
			}
		}
	} else {
		var list []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &list); err == nil {
			for _, m := range list {
				id := m.ID
				if id == "" {
					id = m.Name
				}
				if id != "" {
					models = append(models, id)
				}
			}
		}
	}
	if len(models) > 200 {
		models = models[:200]
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "models": models})
}
