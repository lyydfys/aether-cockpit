package gateway

// 自定义渠道的模型自动发现。
//
// 背景：manifestForCustom 的模型只来自用户手填的 c.Models。用户建渠道时没填
// 模型（很常见——中转站的模型名事先并不知道），manifest 里 Models 为空，
// model_route 就不会为该 provider 产生任何 binding，`/v1/models` 于是只
// 剩内置渠道的模型（外加 auto/ 跨渠道别名）——客户端"读到的模型不对劲"。
//
// 这里补一条后台探测：用渠道自己的 BaseURL + 账号 Key（并带上渠道自定义头）
// 打 GET {base}/models，成功后把模型写回 settings.CustomChannels[i].Models，
// 再重建路由表，客户端即可看到中转站的真实模型。
//
// 刻意保持的边界：
//   - 只探测"Models 为空"的渠道：用户手填的清单是权威，不覆盖。
//   - 失败不写空：保留原值（可能是用户填的），只记日志。
//   - 完全异步：保存按钮不能因为上游慢而卡住。
//   - 不破坏"留空 = 任意模型透传"：openaicompat 的已注册前缀透传规则仍在，
//     探测失败时 custom-xxx/some-model 照样能直接走通。

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

const (
	// customModelProbeTimeout 单次探测上限：中转站常见 3–8s 才回 /models。
	customModelProbeTimeout = 15 * time.Second
	// customModelProbeMaxBody 响应体上限，防止异常端点把内存吃满。
	customModelProbeMaxBody = 4 << 20
	// customModelProbeMaxModels 落盘的模型数上限（与 probe 接口一致）。
	customModelProbeMaxModels = 200
)

// startCustomChannelModelProbe 对"模型清单为空"的自定义渠道发起后台探测。
// 不阻塞调用方；同一渠道并发调用只会真正探测一次。
func (g *Gateway) startCustomChannelModelProbe(onlyIDs ...string) {
	g.settingsMu.Lock()
	list := make([]CustomChannel, len(g.settings.CustomChannels))
	copy(list, g.settings.CustomChannels)
	g.settingsMu.Unlock()

	filter := map[string]bool{}
	for _, id := range onlyIDs {
		if id = strings.TrimSpace(id); id != "" {
			filter[id] = true
		}
	}
	for _, c := range list {
		if strings.TrimSpace(c.BaseURL) == "" || c.ID == "" {
			continue
		}
		if len(filter) > 0 && !filter[c.ID] {
			continue
		}
		// 用户填过模型就尊重它；只有空清单才自动发现。
		if len(c.Models) > 0 {
			continue
		}
		if !g.beginCustomModelProbe(c.ID) {
			continue
		}
		go g.probeCustomChannelModels(c)
	}
}

// beginCustomModelProbe 标记"该渠道正在探测"，重复调用返回 false。
func (g *Gateway) beginCustomModelProbe(id string) bool {
	g.customModelProbe.mu.Lock()
	defer g.customModelProbe.mu.Unlock()
	if g.customModelProbe.inflight == nil {
		g.customModelProbe.inflight = map[string]bool{}
	}
	if g.customModelProbe.inflight[id] {
		return false
	}
	g.customModelProbe.inflight[id] = true
	return true
}

func (g *Gateway) endCustomModelProbe(id string) {
	g.customModelProbe.mu.Lock()
	delete(g.customModelProbe.inflight, id)
	g.customModelProbe.mu.Unlock()
}

// probeCustomChannelModels 执行一次探测并把结果写回设置。
func (g *Gateway) probeCustomChannelModels(c CustomChannel) {
	defer g.endCustomModelProbe(c.ID)

	models, err := g.fetchCustomChannelModels(c)
	if err != nil {
		// 失败不写空：用户手填的清单可能仍有用，探测失败不代表清单无效。
		log.Printf("[CUSTOM] 渠道 %s 模型自动发现失败（保留原清单）：%v", c.ID, err)
		return
	}
	if len(models) == 0 {
		log.Printf("[CUSTOM] 渠道 %s 上游没有返回模型；保留空清单（仍支持 custom-xxx/<任意模型> 透传）", c.ID)
		return
	}

	g.settingsMu.Lock()
	applied := false
	for i := range g.settings.CustomChannels {
		if g.settings.CustomChannels[i].ID != c.ID {
			continue
		}
		// 探测期间用户可能刚手填/改过模型：以用户为准。
		if len(g.settings.CustomChannels[i].Models) > 0 {
			break
		}
		g.settings.CustomChannels[i].Models = models
		applied = true
		break
	}
	if applied {
		_ = g.saveSettings()
	}
	g.settingsMu.Unlock()

	if !applied {
		return
	}
	g.syncCustomChannels()
	if g.models != nil {
		g.models.Rebuild()
	}
	log.Printf("[CUSTOM] 渠道 %s 从上游发现 %d 个模型", c.ID, len(models))
}

// fetchCustomChannelModels 真正请求上游 {base}/models。
//
// 鉴权顺序：渠道名下第一个账号的 api_key → access_token；渠道自定义头
// （如中转站要求的 X-Api-Name）全部带上。没有账号也先试一次匿名请求——
// 部分自建端点的 /models 不鉴权。
func (g *Gateway) fetchCustomChannelModels(c CustomChannel) ([]string, error) {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	endpoint := base + "/models"

	ctx, cancel := context.WithTimeout(context.Background(), customModelProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if key := g.customChannelAPIKey(c.ID); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range c.Headers {
		if strings.TrimSpace(k) == "" || req.Header.Get(k) != "" {
			continue
		}
		req.Header.Set(k, strings.ReplaceAll(v, "{{api_key}}", g.customChannelAPIKey(c.ID)))
	}

	client, cerr := g.tr.NewClient(provider.NetPolicySystem, "", "")
	if cerr != nil {
		return nil, cerr
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, customModelProbeMaxBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &customModelProbeError{Status: resp.StatusCode}
	}
	return parseCustomModelList(raw), nil
}

type customModelProbeError struct{ Status int }

func (e *customModelProbeError) Error() string {
	return "上游 /models 返回 HTTP " + strconv.Itoa(e.Status)
}

// customChannelAPIKey 取渠道名下第一个账号的密钥（优先 api_key，其次 access_token）。
func (g *Gateway) customChannelAPIKey(channelID string) string {
	if g.pool == nil {
		return ""
	}
	pid := customChannelProviderID(channelID)
	for _, env := range g.pool.ListByProvider(pid) {
		if env == nil || env.Disabled() {
			continue
		}
		if v := strings.TrimSpace(env.GetCredential("api_key")); v != "" {
			return v
		}
		if v := strings.TrimSpace(env.GetCredential("access_token")); v != "" {
			return v
		}
	}
	return ""
}

// parseCustomModelList 解析常见的两种模型清单形状：
// OpenAI 的 {data:[{id}]} 与裸数组 [{id|name}]，另外兼容 Gemini 风格的 {models:[{name}]}。
func parseCustomModelList(raw []byte) []string {
	var doc struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"models"`
	}
	out := make([]string, 0, 32)
	seen := map[string]bool{}
	add := func(id string) {
		id = strings.TrimPrefix(strings.TrimSpace(id), "models/")
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	if err := json.Unmarshal(raw, &doc); err == nil {
		for _, d := range doc.Data {
			add(d.ID)
		}
		for _, m := range doc.Models {
			if m.ID != "" {
				add(m.ID)
			} else {
				add(m.Name)
			}
		}
	}
	if len(out) == 0 {
		var list []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &list); err == nil {
			for _, m := range list {
				if m.ID != "" {
					add(m.ID)
				} else {
					add(m.Name)
				}
			}
		}
	}
	if len(out) > customModelProbeMaxModels {
		out = out[:customModelProbeMaxModels]
	}
	return out
}

// RefreshCustomChannelModels 是同步版入口：探测单个渠道并在客户端等待时返回结果。
// 供手动触发（HTTP 端点/测试）使用；失败返回错误，不写设置。
func (g *Gateway) RefreshCustomChannelModels(channelID string) ([]string, error) {
	g.settingsMu.Lock()
	var target CustomChannel
	found := false
	for _, c := range g.settings.CustomChannels {
		if c.ID == channelID {
			target, found = c, true
			break
		}
	}
	g.settingsMu.Unlock()
	if !found {
		return nil, errCustomChannelNotFound
	}
	models, err := g.fetchCustomChannelModels(target)
	if err != nil || len(models) == 0 {
		return models, err
	}
	g.settingsMu.Lock()
	for i := range g.settings.CustomChannels {
		if g.settings.CustomChannels[i].ID == channelID {
			g.settings.CustomChannels[i].Models = models
			break
		}
	}
	_ = g.saveSettings()
	g.settingsMu.Unlock()
	g.syncCustomChannels()
	if g.models != nil {
		g.models.Rebuild()
	}
	return models, nil
}

type customChannelNotFoundError struct{}

func (customChannelNotFoundError) Error() string { return "没有这个自定义渠道" }

var errCustomChannelNotFound = customChannelNotFoundError{}
