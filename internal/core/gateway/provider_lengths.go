package gateway

import (
	"fmt"
	"strconv"
	"strings"
)

// 渠道级"每模型长度"（渠道编辑器 高级 → Context window / Output / Compaction）。
//
//   - contexts：每模型（或 "*" 全部）的上下文窗口，覆盖渠道 manifest 里声明的值；
//     真正影响下游可读到的模型元信息（Gemini models API 的 inputTokenLimit），
//     并在渠道编辑器里以 "1m, gpt-6=128k" 的文本回显；
//   - outputs：每模型最大输出，回显到 Gemini models API 的 outputTokenLimit；
//   - compacts：压缩阈值，目前**只存储/回显**（本项目没有 agent 侧压缩配置的
//     消费点，见 GAP §250 的说明），不参与路由或请求改写。
//
// 此前这三项只在前后端 draft 里，providerDraft 没有字段，保存时被静默丢弃。

// ModelLengthCfg 是一个模型的三项长度；0 表示未设置。
type ModelLengthCfg struct {
	Context int `json:"context,omitempty"`
	Output  int `json:"output,omitempty"`
	Compact int `json:"compact,omitempty"`
}

// providerLengths 返回某渠道的每模型长度覆盖（副本）。
func (g *Gateway) providerLengths(providerID string) map[string]ModelLengthCfg {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	src := g.settings.ProviderLengths[providerID]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]ModelLengthCfg, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// setProviderLengths 保存 contexts/outputs/compacts 三张表：nil=不改，
// 空 map=清除该表。merge 时只更新提交的表，不清掉其他表。
func (g *Gateway) setProviderLengths(providerID string, contexts, outputs, compacts map[string]int) error {
	clean := func(in map[string]int) (map[string]int, error) {
		if in == nil {
			return nil, nil
		}
		out := make(map[string]int, len(in))
		for k, v := range in {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			if v < 0 || v > 10_000_000 {
				return nil, fmt.Errorf("长度 %s=%d 超出范围（1..10000000）", k, v)
			}
			if v == 0 {
				continue
			}
			out[k] = v
		}
		return out, nil
	}
	cx, err := clean(contexts)
	if err != nil {
		return err
	}
	ox, err := clean(outputs)
	if err != nil {
		return err
	}
	cpx, err := clean(compacts)
	if err != nil {
		return err
	}
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.ProviderLengths == nil {
		g.settings.ProviderLengths = map[string]map[string]ModelLengthCfg{}
	}
	cfg := g.settings.ProviderLengths[providerID]
	if cfg == nil {
		cfg = map[string]ModelLengthCfg{}
	}
	merge := func(dst func(*ModelLengthCfg) *int, src map[string]int) {
		if src == nil {
			return
		}
		// 先把该字段全部清空，再按提交的表写回（提交空表=清空）。
		for k, v := range cfg {
			if *dst(&v) != 0 {
				*dst(&v) = 0
				cfg[k] = v
			}
		}
		for k, v := range src {
			c := cfg[k]
			*dst(&c) = v
			cfg[k] = c
		}
	}
	merge(func(c *ModelLengthCfg) *int { return &c.Context }, cx)
	merge(func(c *ModelLengthCfg) *int { return &c.Output }, ox)
	merge(func(c *ModelLengthCfg) *int { return &c.Compact }, cpx)
	// 去掉三字段都为 0 的模型。
	for k, v := range cfg {
		if v.Context == 0 && v.Output == 0 && v.Compact == 0 {
			delete(cfg, k)
		}
	}
	if len(cfg) == 0 {
		delete(g.settings.ProviderLengths, providerID)
	} else {
		g.settings.ProviderLengths[providerID] = cfg
	}
	if len(g.settings.ProviderLengths) == 0 {
		g.settings.ProviderLengths = nil
	}
	return g.saveSettings()
}

// lengthFor 返回某渠道某模型（或 "*" 兜底）的长度配置。
func (g *Gateway) lengthFor(providerID, model string) ModelLengthCfg {
	lengths := g.providerLengths(providerID)
	if len(lengths) == 0 {
		return ModelLengthCfg{}
	}
	// 键既可能是裸模型名（编辑器手输 "gpt-6=128k"），也可能是完整 id
	// （"每个模型的最大窗口"一键选择写的是 provider/model）。
	for _, key := range []string{model, providerID + "/" + model} {
		if c, ok := lengths[key]; ok {
			return c
		}
	}
	return lengths["*"]
}

// providerContextMap 把 contexts/outputs/compacts 表折成**对象**回显：
// 前端 contextsText 负责格式化成 "1m, gpt-6=128k"。早先这里回的是文本，
// 前端又按对象 stringify，编辑器会显示成一串字符索引（0=1,1=m,...）。
func (g *Gateway) providerContextMap(providerID string, field string) map[string]int {
	lengths := g.providerLengths(providerID)
	if len(lengths) == 0 {
		return nil
	}
	pick := func(c ModelLengthCfg) int {
		switch field {
		case "context":
			return c.Context
		case "output":
			return c.Output
		default:
			return c.Compact
		}
	}
	out := map[string]int{}
	for k, c := range lengths {
		if v := pick(c); v > 0 {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// formatLength 与前端 contextsText 的尺寸写法一致：1m / 128k / 原样。
func formatLength(n int) string {
	switch {
	case n%1_000_000 == 0:
		return strconv.Itoa(n/1_000_000) + "m"
	case n%1_000 == 0:
		return strconv.Itoa(n/1_000) + "k"
	default:
		return strconv.Itoa(n)
	}
}
