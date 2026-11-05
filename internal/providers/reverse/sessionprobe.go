package reverse

// 官方会话选号（"抽官方桶"）。
//
// 背景（实测，2026-11-05）：OpenCode 这类混合池的路由是**按会话 ID 分桶**的，
// 不是每次请求随机：同一个 x-opencode-session 会稳定落到同一类后端。
// 用同一段文本派生的会话跑 30 次结果完全一致（要么全官方、要么全中转）；
// 换一个会话 ID（哪怕只改一个字符）就可能从"全部被夹"变成"全部官方校验"。
//
// 于是可以做一件很划算的事：新会话第一次请求前，用**一次几乎免费的小探针**
// 试这个会话 ID —— 发 17 条 stop，官方后端会 400（不产生 token），
// 非官方后端会 200（只花几个 token）。命中 official 就锁定，否则换 ID 重试。
//
// 代价：每个新会话多 1 次（最坏 N 次）极小请求；收益：把"运气"变成"选号"。

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// officialSessionMaxTries 是最多尝试几个会话 ID。
// 观测到"官方桶"占多数，通常第 1–2 次就命中；8 次留足余量又不至于太慢。
const officialSessionMaxTries = 8

// officialSessionTTL 是"这个会话属于官方桶"的记忆时长。
const officialSessionTTL = 6 * time.Hour

type sessionChoice struct {
	value string
	at    time.Time
}

var officialSessionCache = struct {
	mu   sync.Mutex
	data map[string]sessionChoice
}{data: map[string]sessionChoice{}}

// probeStops 是探针用的 17 条 stop（官方上限 16，多一条必然触发校验错误）。
func probeStops() []string {
	out := make([]string, 0, 17)
	for i := 0; i < 17; i++ {
		out = append(out, "probe_"+strconv.Itoa(i))
	}
	return out
}

// officialSession 从 base 开始，挑一个能命中原生/官方后端的会话 ID。
//
// 无法命中原生时返回原始 base（保证不影响可用性）：宁可退回普通会话，
// 也不要因为选号失败把请求拖死。
func (p *Provider) officialSession(
	client *http.Client,
	endpoint, model string,
	headers func(sess string) map[string]string,
	base string,
) string {
	base = strings.TrimSpace(base)
	if base == "" || !p.profile.OfficialSessionProbe {
		return base
	}
	cacheKey := p.profile.ID + "|" + base
	officialSessionCache.mu.Lock()
	if c, ok := officialSessionCache.data[cacheKey]; ok && time.Since(c.at) < officialSessionTTL {
		officialSessionCache.mu.Unlock()
		return c.value
	}
	officialSessionCache.mu.Unlock()

	for i := 0; i < officialSessionMaxTries; i++ {
		cand := base
		if i > 0 {
			// 必须换成**另一个独立 ID**，不能只在原 ID 后加后缀：
			// 路由是按 ID 哈希分桶的，后缀常常落在同一个桶里（实测
			// 追加 "~od1" 仍然稳定被判为中转），那样重试等于白试。
			cand = randomSessionID()
		}
		if p.probeOfficialSession(client, endpoint, model, headers(cand), cand) {
			officialSessionCache.mu.Lock()
			if len(officialSessionCache.data) > 4096 {
				now := time.Now()
				for k, v := range officialSessionCache.data {
					if now.Sub(v.at) > officialSessionTTL {
						delete(officialSessionCache.data, k)
					}
				}
			}
			officialSessionCache.data[cacheKey] = sessionChoice{value: cand, at: time.Now()}
			officialSessionCache.mu.Unlock()
			return cand
		}
	}
	// 全都没命中：记住"这个 base 挑不出来"，避免每个请求都重试 8 次。
	officialSessionCache.mu.Lock()
	officialSessionCache.data[cacheKey] = sessionChoice{value: base, at: time.Now()}
	officialSessionCache.mu.Unlock()
	return base
}

// probeOfficialSession 发一次 17-stop 小探针，判断该会话是否落在官方桶。
//
// 判定只看"是否出现官方校验错误"：400 + Stop string array too long。
// 200 说明被夹（非官方）；其它错误一律当作"未命中"，不据此做任何惩罚。
func (p *Provider) probeOfficialSession(client *http.Client, endpoint, model string, headers map[string]string, sess string) bool {
	body, err := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"stop":       probeStops(),
	})
	if err != nil {
		return false
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if p.profile.SessionHeader != "" {
		req.Header.Set(p.profile.SessionHeader, sess)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusBadRequest {
		return false
	}
	low := strings.ToLower(string(raw))
	return strings.Contains(low, "stop string array too long")
}

// randomSessionID 生成一个 32 位十六进制会话 ID（与网关注入的指纹同形状）。
func randomSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "fallback-session-id"
	}
	return hex.EncodeToString(b)
}

// probeOfficialSessionForTest 暴露给测试：不经过缓存。
func (p *Provider) probeOfficialSessionForTest(client *http.Client, endpoint, model string, headers map[string]string, sess string) bool {
	return p.probeOfficialSession(client, endpoint, model, headers, sess)
}
