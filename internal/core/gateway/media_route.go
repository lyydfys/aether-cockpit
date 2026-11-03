package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/core/tools"
	"aether-cockpit/internal/provider"
)

// 媒体端点（images / videos / audio / realtime）的统一寻址层。
//
// 背景：这四个端点原来只认设置页里的一条 base_url。端点数量比同类产品多，
// 但深度只有"一条地址、一把钥匙"：换不了号、失败不会转移，也无法复用
// 渠道页里已经配好的账号与地址。
//
// 现在的顺序是"先渠道、后设置页"：
//  1. 请求里显式带了模型名（?model= / body.model / multipart 的 model 字段）时，
//     按模型名解析到渠道绑定；
//  2. 命中渠道 → 用该渠道账号池选出的账号（凭证、可用性、冷却）与渠道的上游地址；
//  3. 没命中（或渠道压根没声明地址）→ 退回设置页的单一配置。
//
// 为什么必须先查渠道再回退设置：模型名是客户端表达"我要用哪个上游"的唯一手段。
// 用户既然把某个媒体模型挂到了渠道上，就应当复用那里的账号轮换、冷却与凭证；
// 而绝大多数存量用户没做这件事，设置页那条路径必须原样可跑，所以回退是
// "这类配置不存在"的正常分支，不是错误分支。
//
// 为什么渠道解析只认精确名 / 渠道前缀 / 虚拟组 / 重定向，而不是直接用路由表的
// 全部结果：ModelRouter 的最后一层是"模型家族兜底"，它会把 "gpt-image-1" 这种
// 带家族关键词的名字兜到同族的对话模型（如 gpt-5）上。对对话请求这是好事，
// 对生图/生视频就是把请求打到错误的模型与端点上——宁可回退设置页。
type mediaTarget struct {
	// BaseURL 上游根地址（已去掉尾部 /）。为空表示没有渠道地址。
	BaseURL string
	// APIKey 上游凭证（渠道账号的 api_key / access_token / token）。
	APIKey string
	// Model 渠道解析出的上游模型名。为空表示沿用请求体或设置页的模型名。
	Model string
	// Env 命中的账号；设置页回退路径下为 nil。
	Env *provider.AccountEnv
	// ProviderID 命中的渠道；设置页回退路径下为空。
	ProviderID string
}

// fromChannel 报告本次目标是不是由渠道解析出来的。
func (t mediaTarget) fromChannel() bool { return t.ProviderID != "" }

// 四个 applyTo* 只做字段覆盖：端点、请求/响应形状完全由各自的 api 文件决定，
// 这里不参与任何编解码，避免"路由改对了、协议改坏了"。
func (t mediaTarget) applyToImage(cfg tools.ImageConfig) tools.ImageConfig {
	if t.BaseURL != "" {
		cfg.BaseURL = t.BaseURL
	}
	if t.APIKey != "" {
		cfg.APIKey = t.APIKey
	}
	if t.Model != "" {
		cfg.Model = t.Model
	}
	return cfg
}

func (t mediaTarget) applyToVideo(cfg tools.VideoConfig) tools.VideoConfig {
	if t.BaseURL != "" {
		cfg.BaseURL = t.BaseURL
	}
	if t.APIKey != "" {
		cfg.APIKey = t.APIKey
	}
	if t.Model != "" {
		cfg.Model = t.Model
	}
	return cfg
}

func (t mediaTarget) applyToAudio(cfg tools.AudioConfig) tools.AudioConfig {
	if t.BaseURL != "" {
		cfg.BaseURL = t.BaseURL
	}
	if t.APIKey != "" {
		cfg.APIKey = t.APIKey
	}
	if t.Model != "" {
		cfg.Model = t.Model
	}
	return cfg
}

func (t mediaTarget) applyToRealtime(cfg tools.RealtimeConfig) tools.RealtimeConfig {
	if t.BaseURL != "" {
		cfg.BaseURL = t.BaseURL
	}
	if t.APIKey != "" {
		cfg.APIKey = t.APIKey
	}
	if t.Model != "" {
		cfg.Model = t.Model
	}
	return cfg
}

// mediaTargetFor 是四个端点共用的入口：从请求里取模型提示并解析目标。
// GET 类端点没有 body，传 nil 即可（仍会读 ?model=）。
func (g *Gateway) mediaTargetFor(r *http.Request, body []byte) (mediaTarget, *ir.APIError) {
	return g.resolveMediaTarget(mediaModelHint(r, body))
}

// resolveMediaTarget 给一次媒体请求找目标。
//
// 返回的 *ir.APIError 只在"模型已经明确指向某个渠道、但该渠道此刻没有可用账号"
// 时非空：此时绝不能悄悄回退设置页——那会用别人的钥匙去打另一条上游，
// 用户看到的"成功"其实是打错了地方。
func (g *Gateway) resolveMediaTarget(model string) (mediaTarget, *ir.APIError) {
	bindings := g.mediaBindings(model)
	if len(bindings) == 0 {
		return mediaTarget{}, nil
	}
	if g.pool == nil {
		return mediaTarget{}, nil
	}

	exclude := make(map[string]bool, 2)
	var lastErr *ir.APIError
	// channelKnown 表示候选里至少有一个渠道真的声明了上游地址。
	// 它把两种情况分开：渠道配了地址但没号（要报错）vs 渠道根本不是媒体的
	// 上游、只是模型名撞上了（要回退设置页）。
	channelKnown := false

	for _, b := range bindings {
		if _, ok := provider.Get(b.ProviderID); !ok {
			continue
		}
		// 渠道级地址来自 spec（热更新的 base_url）。
		specBase := strings.TrimRight(strings.TrimSpace(g.providerBaseURL(b.ProviderID)), "/")
		if specBase != "" {
			channelKnown = true
		}

		// 账号选择沿用对话链路的那套语义（Routing / Stays），只是不带会话粘性：
		// 媒体请求没有可复用的会话指纹，硬套上去只会把生图请求粘到无关账号。
		// 刻意也不占用 RPM 令牌桶与并发槽位：媒体请求是一次性转发、没有长流，
		// 占着槽位不放只会把对话请求挤掉。
		route := g.providerRoute(b.ProviderID)
		env, selErr := g.pool.Select(b.ProviderID, pool.SelectOptions{
			Exclude:  exclude,
			Model:    b.UpstreamModel,
			Mode:     g.selectMode(b.ProviderID, route.Routing),
			Affinity: route.Affinity,
		})
		if selErr != nil {
			lastErr = selErr
			continue
		}
		exclude[env.AccountID] = true

		base := specBase
		// 账号自带 base_url（openaicompat / reverse 这类中转渠道的常态）优先：
		// 同一个渠道下不同账号可能落在不同域名上。
		if v := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/"); v != "" {
			base = v
			channelKnown = true
		}
		if base == "" {
			// 渠道没有地址：它接不了媒体请求。继续看下一个候选，最后回退设置页。
			// 每个渠道只提示一次，避免高频媒体请求把日志刷满。
			if _, warned := mediaNoAddrWarned.LoadOrStore(b.ProviderID, struct{}{}); !warned {
				log.Printf("[MEDIA] 渠道 %s 未声明上游 base_url，媒体请求跳过该渠道", b.ProviderID)
			}
			continue
		}
		return mediaTarget{
			BaseURL:    base,
			APIKey:     mediaAPIKey(env),
			Model:      b.UpstreamModel,
			Env:        env,
			ProviderID: b.ProviderID,
		}, nil
	}

	if channelKnown && lastErr != nil {
		return mediaTarget{}, lastErr
	}
	return mediaTarget{}, nil
}

// mediaBindings 把模型名解析成"允许用于媒体"的候选绑定。
//
// 只接受四类命中（都能追溯到用户的显式意图）：渠道前缀、虚拟聚合组、
// 模型重定向、以及上游名与请求名一致（归一化后）的精确/裸名绑定。
// 家族兜底出来的绑定被丢弃，理由见本文件头部的注释。
func (g *Gateway) mediaBindings(model string) []modelBinding {
	model = strings.TrimSpace(model)
	if model == "" || g.models == nil {
		return nil
	}
	all := g.models.Resolve(model)
	if len(all) == 0 {
		return nil
	}

	// 1. 显式渠道前缀 pid/model：路由器的透传分支就是用户点名的渠道。
	if i := strings.Index(model, "/"); i > 0 {
		if _, ok := provider.Get(model[:i]); ok {
			return all
		}
	}
	// 2. 虚拟聚合组 / 模型重定向：都是用户在设置里写出来的映射。
	if g.models.HasGroup(model) || g.isRedirectName(model) {
		return all
	}
	// 3. 精确名 / 裸名（允许大小写、连字符、点号的写法差异）。
	want := normalizeModelKey(model)
	if want == "" {
		return nil
	}
	out := make([]modelBinding, 0, len(all))
	for _, b := range all {
		if normalizeModelKey(b.UpstreamModel) == want || normalizeModelKey(b.DisplayName) == want {
			out = append(out, b)
		}
	}
	return out
}

// isRedirectName 报告这个名字是否是用户配置的模型重定向虚拟名。
func (g *Gateway) isRedirectName(model string) bool {
	key := normalizeModelKey(model)
	if key == "" {
		return false
	}
	for alias := range g.modelRedirects() {
		if normalizeModelKey(alias) == key {
			return true
		}
	}
	return false
}

// mediaNoAddrWarned 保证"渠道没有媒体上游地址"的提示每个渠道只打一次。
var mediaNoAddrWarned sync.Map // providerID -> struct{}

// mediaAPIKey 取账号的上游凭证。键名按现有渠道的既有约定依次尝试：
// api_key（多数中转）→ access_token（OAuth 类）→ token（网页端协议）。
func mediaAPIKey(env *provider.AccountEnv) string {
	if env == nil {
		return ""
	}
	for _, k := range []string{"api_key", "access_token", "token"} {
		if v := strings.TrimSpace(env.GetCredential(k)); v != "" {
			return v
		}
	}
	return ""
}

// mediaModelHint 取"客户端显式指定的模型名"。
//
// 顺序：?model= → JSON body 的 model → multipart 表单的 model 字段。
// 刻意不拿设置页的缺省模型当提示：设置页常见值是 "gpt-image-1" 这类带家族
// 关键词、却不一定登记在任何渠道里的名字，用它去解析会让存量用户的生图
// 请求被改道到对话渠道（见 mediaBindings 的说明）。设置页模型只在
// "回退设置页"分支里当缺省值使用。
func mediaModelHint(r *http.Request, body []byte) string {
	if r != nil {
		if v := strings.TrimSpace(r.URL.Query().Get("model")); v != "" {
			return v
		}
	}
	if len(body) == 0 {
		return ""
	}
	// JSON 是绝大多数媒体的形状（生图/生视频/语音合成）。非 JSON 会很快失败。
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err == nil {
		if v := strings.TrimSpace(probe.Model); v != "" {
			return v
		}
	}
	if r == nil {
		return ""
	}
	// multipart（语音转写、图像编辑）：只找表单字段 model，其余部分由
	// multipart.Reader 自己丢弃，不会把文件内容读进内存。
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return ""
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for i := 0; i < 64; i++ {
		part, err := mr.NextPart()
		if err != nil {
			return ""
		}
		if part.FormName() == "model" {
			buf, _ := io.ReadAll(io.LimitReader(part, 512))
			_ = part.Close()
			return strings.TrimSpace(string(buf))
		}
		_ = part.Close()
	}
	return ""
}

// rewriteJSONModel 把请求体里的 model 强制改成渠道的上游名。
// 渠道路径必须覆盖：客户端传的可能是虚拟名/别名，上游只认自己的模型名。
// body 不是 JSON 对象时原样返回（multipart 的 model 字段无法安全重写）。
func rewriteJSONModel(body []byte, model string) []byte {
	model = strings.TrimSpace(model)
	if model == "" || len(body) == 0 {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	if cur, _ := payload["model"].(string); cur == model {
		return body
	}
	payload["model"] = model
	out, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return out
}

// applyModelToBody 决定请求体里的模型名要不要改写：
//   - 渠道路径：客户端传的可能是虚拟名，必须换成渠道的上游名（覆盖）；
//   - 设置页路径：设置页模型只是缺省值，客户端显式给了就尊重（仅补缺）。
func applyModelToBody(body []byte, t mediaTarget, settingsModel string) []byte {
	if t.Model != "" {
		return rewriteJSONModel(body, t.Model)
	}
	if settingsModel = strings.TrimSpace(settingsModel); settingsModel != "" {
		if patched, ok := injectJSONModel(body, settingsModel); ok {
			return patched
		}
	}
	return body
}

// noteMediaResult 按账号池语义记录一次媒体转发的结果。
//
// 只惩罚"这个账号/这条渠道真的坏了"的状态：
//   - 429 → 冷却（并由 penalize 记录限流窗口）；
//   - 401 → 凭证失效（403 不算：多数是模型/功能没给这个 Key 开，不是号坏了）；
//   - 5xx / 网络错误 → 计入连续失败。
//
// 其余 4xx（参数错、模型不支持该端点）是请求本身的问题，惩罚账号会把
// 可用号打坏——媒体端点的 4xx 恰恰最常见。设置页回退路径没有账号，直接跳过。
func (g *Gateway) noteMediaResult(t mediaTarget, status int, err error) {
	if t.Env == nil {
		return
	}
	if err != nil {
		g.penalize(t.Env, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "媒体上游请求失败").
			WithCause(err).WithProvider(t.ProviderID))
		return
	}
	switch {
	case status >= 200 && status < 300:
		t.Env.MarkSuccess()
	case status == http.StatusTooManyRequests:
		g.penalize(t.Env, ir.NewAPIError(ir.ErrCodeUpstreamRateLimit, "媒体上游限流").
			WithStatus(status).WithProvider(t.ProviderID))
	case status == http.StatusUnauthorized:
		g.penalize(t.Env, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "媒体上游鉴权失败").
			WithStatus(status).WithProvider(t.ProviderID))
	case status >= 500:
		g.penalize(t.Env, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "媒体上游错误").
			WithStatus(status).WithProvider(t.ProviderID))
	}
}

// mediaTargetError 把"命中渠道但选不到账号"翻译成能指导操作的一句话。
func mediaTargetError(err *ir.APIError) string {
	msg := "媒体模型命中渠道，但该渠道当前没有可用账号"
	if err != nil {
		if m := strings.TrimSpace(err.Message); m != "" {
			msg += "：" + m
		}
	}
	return msg + "。请检查该渠道账号是否全部冷却/停用，或在请求里改用设置页中的媒体模型名。"
}

// --- 视频任务 ↔ 渠道的短期对应表 ---

// 视频的轮询端点（GET /v1/videos/{id}、GET /v1/videos/{id}/content）本身不带
// 模型名：创建走渠道、轮询却打设置页地址的话，永远拿不到那个任务。所以创建
// 成功时把"上游任务 id → 本次目标"记下来，轮询时优先查这张表。
//
// 刻意做成包级缓存、不挂在 Gateway 上：它只是进程内的短期缓存（2 小时后失效），
// 重启后由请求里的 ?model= 兜底，不值得为它改 Gateway 结构体。
const (
	videoRouteTTL     = 2 * time.Hour
	videoRouteMaxKeys = 512
)

type videoRouteEntry struct {
	target  mediaTarget
	expires time.Time
}

var videoRouteCache = struct {
	mu    sync.Mutex
	items map[string]videoRouteEntry
	order []string
}{
	items: make(map[string]videoRouteEntry, 16),
}

// rememberVideoRoute 记住某个上游视频任务 id 是用什么目标创建的。
// 只记渠道路径：设置页路径本来就是轮询的默认回退，记了也没用。
func rememberVideoRoute(id string, t mediaTarget) {
	id = strings.TrimSpace(id)
	if id == "" || t.ProviderID == "" {
		return
	}
	videoRouteCache.mu.Lock()
	defer videoRouteCache.mu.Unlock()
	if _, ok := videoRouteCache.items[id]; !ok {
		videoRouteCache.order = append(videoRouteCache.order, id)
	}
	videoRouteCache.items[id] = videoRouteEntry{target: t, expires: time.Now().Add(videoRouteTTL)}
	// FIFO 淘汰，避免长期运行后无限增长。
	for len(videoRouteCache.order) > videoRouteMaxKeys {
		oldest := videoRouteCache.order[0]
		videoRouteCache.order = videoRouteCache.order[1:]
		delete(videoRouteCache.items, oldest)
	}
}

// videoRouteFor 查某个上游视频任务 id 创建时用的目标（已过期视为未命中）。
func videoRouteFor(id string) (mediaTarget, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return mediaTarget{}, false
	}
	videoRouteCache.mu.Lock()
	defer videoRouteCache.mu.Unlock()
	e, ok := videoRouteCache.items[id]
	if !ok {
		return mediaTarget{}, false
	}
	if time.Now().After(e.expires) {
		delete(videoRouteCache.items, id)
		return mediaTarget{}, false
	}
	return e.target, true
}

// rememberVideoTaskFromResponse 从上游创建响应里取任务 id 并记住路由。
// 两种上游形状都要认：xAI 的 request_id 与 OpenAI Sora 的 id。
func rememberVideoTaskFromResponse(out []byte, t mediaTarget) {
	if t.ProviderID == "" || len(out) == 0 || len(out) > 1<<20 {
		return
	}
	var probe struct {
		RequestID string `json:"request_id"`
		ID        string `json:"id"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return
	}
	rememberVideoRoute(probe.RequestID, t)
	rememberVideoRoute(probe.ID, t)
}
