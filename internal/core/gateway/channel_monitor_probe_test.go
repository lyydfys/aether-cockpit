package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 探测链路：mock 渠道离线可用，探测后 /api/channels/status 应有成功样本；
// 冷却中的账号则记录为失败并带原因。
func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func TestProbeAccountsAndStatus(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		MaxRecords:       10,
	})
	okEnv := provider.NewAccountEnv("probe-ok", "mock-native")
	g.Pool().Add(okEnv)
	cool := provider.NewAccountEnv("probe-cool", "mock-native")
	g.Pool().Add(cool)
	cool.MarkCoolingDown(time.Minute, "429")
	g.models.Rebuild()

	// 走真实入口：POST /api/channels/check 异步探测，随后轮询 status。
	rec := httptest.NewRecorder()
	g.handleChannelCheck(rec, httptest.NewRequest(http.MethodPost, "/api/channels/check", jsonBody(map[string]any{"provider": "mock-native"})))
	if rec.Code != http.StatusOK {
		t.Fatalf("check status = %d", rec.Code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec = httptest.NewRecorder()
		g.handleChannelStatus(rec, httptest.NewRequest(http.MethodGet, "/api/channels/status", nil))
		var probe struct {
			Entries []channelProbe `json:"entries"`
			Reading bool           `json:"reading"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &probe)
		if !probe.Reading && len(probe.Entries) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("探测未在 5s 内完成: %+v", probe)
		}
		time.Sleep(50 * time.Millisecond)
	}
	rec = httptest.NewRecorder()
	g.handleChannelStatus(rec, httptest.NewRequest(http.MethodGet, "/api/channels/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Entries   []channelProbe `json:"entries"`
		Reading   bool           `json:"reading"`
		CheckedAt int64          `json:"checkedAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析: %v", err)
	}
	var okEntry, coolEntry *channelProbe
	for i := range body.Entries {
		switch body.Entries[i].AccountID {
		case "probe-ok":
			okEntry = &body.Entries[i]
		case "probe-cool":
			coolEntry = &body.Entries[i]
		}
	}
	if okEntry == nil {
		t.Fatalf("缺少成功账号的探测结果: %+v", body.Entries)
	}
	if !okEntry.OK || okEntry.Ms < 0 || okEntry.Model == "" || len(okEntry.History) == 0 {
		t.Fatalf("成功账号探测结果异常: %+v", okEntry)
	}
	if coolEntry == nil || coolEntry.OK || coolEntry.Error == "" {
		t.Fatalf("冷却账号应记录失败与原因: %+v", coolEntry)
	}
	if body.Reading || body.CheckedAt == 0 {
		t.Fatalf("reading/checkedAt 异常: %+v", body)
	}
}

func TestOrderProbeModelsByCost(t *testing.T) {
	models := []ir.ModelMeta{
		{ID: "p/gpt-5-pro", UpstreamModel: "gpt-5-pro"},
		{ID: "p/deepseek-v4-pro", UpstreamModel: "deepseek-v4-pro"},
		{ID: "p/deepseek-v4-flash", UpstreamModel: "deepseek-v4-flash"},
		{ID: "p/gemini-3.5-flash", UpstreamModel: "gemini-3.5-flash"},
		{ID: "p/claude-sonnet", UpstreamModel: "claude-sonnet"},
		{ID: "p/mystery-model", UpstreamModel: "mystery-model"},
	}
	ids := func(in []ir.ModelMeta) []string {
		out := make([]string, len(in))
		for i, m := range in {
			out[i] = m.UpstreamModel
		}
		return out
	}
	equal := func(got []ir.ModelMeta, want ...string) {
		t.Helper()
		calc := ids(got)
		if len(calc) != len(want) {
			t.Fatalf("order = %v, want %v", calc, want)
		}
		for i := range want {
			if calc[i] != want[i] {
				t.Fatalf("order = %v, want %v", calc, want)
			}
		}
	}

	// 没有任何倍率/价格时按模型名档位：free/deepseek → flash → 标准 → 未知 → pro。
	equal(orderProbeModels(models, nil, nil),
		"deepseek-v4-pro", "deepseek-v4-flash", "gemini-3.5-flash", "claude-sonnet", "mystery-model", "gpt-5-pro")

	// 有积分倍率时倍率优先；未配置按 ×1。
	equal(orderProbeModels(models, map[string]float64{"deepseek-v4-pro": 3, "gemini-3.5-flash": 0.5}, nil),
		"gemini-3.5-flash", "deepseek-v4-flash", "claude-sonnet", "mystery-model", "gpt-5-pro", "deepseek-v4-pro")

	// 只有价格表时按 input+output 单价排序；无价格的模型排在后面。
	priceOf := func(id string) (ModelPrice, bool) {
		switch id {
		case "gpt-5-pro":
			return ModelPrice{InputPerM: 10, OutputPerM: 40}, true
		case "deepseek-v4-pro":
			return ModelPrice{InputPerM: 0.1, OutputPerM: 0.2}, true
		case "gemini-3.5-flash":
			return ModelPrice{InputPerM: 0.3, OutputPerM: 1.2}, true
		default:
			return ModelPrice{}, false
		}
	}
	equal(orderProbeModels(models, nil, priceOf),
		"deepseek-v4-pro", "gemini-3.5-flash", "gpt-5-pro", "deepseek-v4-flash", "claude-sonnet", "mystery-model")

	// 防止 "gemini" 里的 "mini" 被当成廉价档位。
	if modelCostTier("gemini-3.1-pro") != 4 || modelCostTier("gpt-4o-mini") != 1 {
		t.Fatalf("cost tier: gemini-pro=%d, gpt-mini=%d", modelCostTier("gemini-3.1-pro"), modelCostTier("gpt-4o-mini"))
	}
}

// 最便宜的模型失败后，探测应升级到下一个候选。
type failFirstChat struct{ calls []string }

func (f *failFirstChat) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	f.calls = append(f.calls, req.UpstreamModel)
	if len(f.calls) == 1 {
		return nil, errors.New("最便宜的模型不可用")
	}
	ch := make(chan ir.UnifiedStreamEvent, 1)
	ch <- ir.UnifiedStreamEvent{Type: ir.EventDone}
	close(ch)
	return ch, nil
}

func TestProbeFirstOKEscalates(t *testing.T) {
	fake := &failFirstChat{}
	models := []ir.ModelMeta{
		{ID: "p/cheap", UpstreamModel: "cheap"},
		{ID: "p/next", UpstreamModel: "next"},
	}
	model, ok, _, _ := probeFirstOK(context.Background(), fake, provider.NewAccountEnv("probe-esc", "mock-native"), models, 3)
	if !ok || model.UpstreamModel != "next" {
		t.Fatalf("探测应升级到 next: ok=%v model=%+v calls=%v", ok, model, fake.calls)
	}
	if len(fake.calls) != 2 || fake.calls[0] != "cheap" || fake.calls[1] != "next" {
		t.Fatalf("calls = %v, want [cheap next]", fake.calls)
	}
}

// 设置接口：分钟数夹取（0=关闭、<5 抬到 5、>1440 压到 1440）。
func TestChannelMonitorSettingRoundTrip(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	set := func(minutes int) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/settings/channel-monitor", jsonBody(map[string]any{"minutes": minutes}))
		g.handleChannelMonitorSetting(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("save %d: %d %s", minutes, rec.Code, rec.Body.String())
		}
		var out struct {
			Minutes int `json:"minutes"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Minutes
	}
	if got := set(0); got != 0 {
		t.Fatalf("0 = %d", got)
	}
	if got := set(2); got != 5 {
		t.Fatalf("2 → %d, want 5", got)
	}
	if got := set(30); got != 30 {
		t.Fatalf("30 → %d", got)
	}
	if got := set(9999); got != 1440 {
		t.Fatalf("9999 → %d, want 1440", got)
	}
}

func TestStatusFromText(t *testing.T) {
	cases := map[string]int{
		"429 Too Many Requests": 429,
		"upstream status 502":   502,
		"账号当前不可用（冷却/停用/失效）":     0,
		"":                  0,
		"request id 123456": 0,
	}
	for msg, want := range cases {
		if got := statusFromText(msg); got != want {
			t.Errorf("statusFromText(%q) = %d, want %d", msg, got, want)
		}
	}
}
