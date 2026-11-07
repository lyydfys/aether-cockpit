package workbuddy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

var (
	traceparentRe = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)
	hex32Re       = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hex16Re       = regexp.MustCompile(`^[0-9a-f]{16}$`)
	uuidV4Re      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// 三个预设必须与参考实现 Fingerprint.<clinit> 的 OFFICIAL/DESKTOP/QUIET 一致。
func TestIdentityPresets(t *testing.T) {
	cases := []struct {
		prof   IdentityProfile
		ua     string
		mode   string
		stain  bool
		pace   time.Duration
		cliVer string
	}{
		{
			prof: identityCLI, mode: "cli", stain: true, pace: 350 * time.Millisecond, cliVer: "2.165.0",
			ua: "WorkBuddy/5.9.3 WorkBuddy/5.9.3 CLI/2.165.0",
		},
		{prof: identityDesktop, ua: "WorkBuddy/5.9.3", mode: "client", stain: false, pace: 350 * time.Millisecond},
		{prof: identityQuiet, ua: "WorkBuddy/5.9.3", mode: "client", stain: false, pace: 900 * time.Millisecond},
		{prof: identityBrowser, ua: browserUA, mode: "browser", stain: false, pace: 350 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.prof.Name, func(t *testing.T) {
			if got := c.prof.UA(); got != c.ua {
				t.Fatalf("UA = %q, want %q", got, c.ua)
			}
			if c.prof.UAMode != c.mode || c.prof.Stainless() != c.stain || c.prof.PaceGap != c.pace {
				t.Fatalf("mode/stainless/pace = %s/%v/%v", c.prof.UAMode, c.prof.Stainless(), c.prof.PaceGap)
			}
			if c.prof.ClientVersion != "5.9.3" || c.prof.StainlessVersion != "6.25.0" ||
				c.prof.OS != "Windows" || c.prof.Arch != "x64" ||
				c.prof.Runtime != "node" || c.prof.RuntimeVersion != "v22.14.0" ||
				c.prof.Product != "SaaS" || !c.prof.PrivateData || c.prof.DeviceHeaders ||
				c.prof.MachineSeed != "wb2a" {
				t.Fatalf("preset 字段不对: %+v", c.prof)
			}
			if c.stain {
				if c.prof.CLIVersion != c.cliVer || !strings.HasSuffix(c.prof.UA(), " CLI/"+c.cliVer) {
					t.Fatalf("CLI 档版本不对: %+v", c.prof)
				}
			} else if strings.Contains(c.prof.UA(), "CLI/") {
				// 参考实现的 DESKTOP/QUIET 仍保留 cliVersion 字段，只是不写进 UA、
				// 不发 Stainless；跨档差异只看 UA 与头，不看字段是否存在。
				t.Fatalf("非 CLI 档 UA 不应有 CLI 段: %+v", c.prof)
			}
		})
	}
}

func TestResolveIdentityPreset(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"", "cli", false}, // 参考实现默认 = OFFICIAL（双段）
		{"official", "cli", false},
		{"cli", "cli", false},
		{"CLI", "cli", false},
		{"desktop", "desktop", false},
		{" quiet ", "quiet", false},
		{"browser", "browser", false},
		{"bogus", "cli", true},
	}
	for _, c := range cases {
		got, err := resolveIdentityPreset(c.in)
		if (err != nil) != c.err {
			t.Fatalf("resolve(%q) err=%v want err=%v", c.in, err, c.err)
		}
		if got.Name != c.want {
			t.Fatalf("resolve(%q) = %q, want %q", c.in, got.Name, c.want)
		}
	}
}

func TestCurrentIdentitySourceOrder(t *testing.T) {
	resetIdentityForTest()
	t.Setenv(identityEnvVar, "")
	if got := currentIdentity().Name; got != "cli" {
		t.Fatalf("默认档 = %q, want cli", got)
	}
	t.Setenv(identityEnvVar, "quiet")
	if got := currentIdentity().Name; got != "quiet" {
		t.Fatalf("环境变量档 = %q, want quiet", got)
	}
	if err := SetIdentityPreset("desktop"); err != nil {
		t.Fatalf("SetIdentityPreset(desktop): %v", err)
	}
	if got := currentIdentity().Name; got != "desktop" {
		t.Fatalf("宿主设置应优先于环境变量，got %q", got)
	}
	if err := SetIdentityPreset(""); err != nil {
		t.Fatalf("SetIdentityPreset(\"\"): %v", err)
	}
	if got := IdentityPresetName(); got != "cli" {
		t.Fatalf("重置后应回到默认 cli，got %q", got)
	}
	if err := SetIdentityPreset("nope"); err == nil {
		t.Fatal("未知档位应返回错误")
	}
	resetIdentityForTest()
	t.Setenv(identityEnvVar, "")
}

// 对话头：逐项对齐参考实现 Upstream.chatHeaders（基础头 + conversation +
// stainless + channelVerify）。
func TestChatHeadersAlignReference(t *testing.T) {
	for _, prof := range []IdentityProfile{identityCLI, identityDesktop} {
		for _, region := range []profile{cnProfile, intlProfile} {
			t.Run(prof.Name+"/"+region.id, func(t *testing.T) {
				resetIdentityForTest()
				p := newProvider(region)
				env := provider.NewAccountEnv("acc-"+region.id+"-"+prof.Name, region.id)
				env.Credentials = map[string]string{
					"token": "tk", "user_id": "uid-1",
					"enterprise_id": "ent-1", "device_token": "dt-1",
					"device_headers": "false",
				}
				req := httptest.NewRequest(http.MethodPost, "https://example.com/v2/chat/completions", nil)

				p.applyHeaders(req, env, prof, "tk", "uid-1", &ir.UnifiedChatRequest{}, 0)
				h := req.Header

				if got := h.Get("User-Agent"); got != prof.UA() {
					t.Fatalf("User-Agent = %q, want %q", got, prof.UA())
				}
				if h.Get("X-IDE-Version") != prof.ClientVersion {
					t.Fatalf("X-IDE-Version = %q", h.Get("X-IDE-Version"))
				}
				for k, v := range map[string]string{
					"X-Product":           "SaaS",
					"X-IDE-Type":          "WorkBuddy",
					"X-IDE-Name":          "WorkBuddy",
					"X-Private-Data":      "true",
					"X-CodeBuddy-Request": "1",
					"X-Requested-With":    "XMLHttpRequest",
					"Accept":              "application/json",
					"Content-Type":        "application/json",
					"X-Enterprise-Id":     "ent-1",
					"X-Tenant-Id":         "ent-1",
					"X-Device-Token":      "dt-1",
				} {
					if got := h.Get(k); got != v {
						t.Fatalf("%s = %q, want %q", k, got, v)
					}
				}
				// 参考实现不发这两个头（REA 检索确认 X-Agent-Type 只出现在
				// importHeaders 的识别表里，不在任何请求构造中）。
				for _, k := range []string{"X-Agent-Type", "X-Device-Id"} {
					if v := h.Get(k); v != "" {
						t.Fatalf("%s 不应发送，got %q", k, v)
					}
				}

				// 会话 ID：账号级稳定 UUID v4；message/request ID：32 位 uuidV7。
				conv, msg, reqID := h.Get("X-Conversation-ID"), h.Get("X-Conversation-Message-ID"), h.Get("X-Conversation-Request-ID")
				if !uuidV4Re.MatchString(conv) {
					t.Fatalf("X-Conversation-ID 不是 UUID v4: %q", conv)
				}
				if !hex32Re.MatchString(msg) || !hex32Re.MatchString(reqID) {
					t.Fatalf("message/request id 应为 32 位 hex: %q %q", msg, reqID)
				}
				if h.Get("X-Request-ID") != msg {
					t.Fatalf("X-Request-ID 应等于 message id: %q vs %q", h.Get("X-Request-ID"), msg)
				}
				if reqID == msg {
					t.Fatal("X-Conversation-Request-ID 应是独立的 uuidV7")
				}
				if h.Get("X-Agent-Intent") != "craft" || h.Get("X-Agent-Purpose") != "conversation" {
					t.Fatalf("agent 头不对: %q %q", h.Get("X-Agent-Intent"), h.Get("X-Agent-Purpose"))
				}

				// 通道校验头：W3C trace + b3 顺序 <trace>-<span>-1-<parent> + baggage。
				tp := h.Get("traceparent")
				if !traceparentRe.MatchString(tp) {
					t.Fatalf("traceparent 格式不对: %q", tp)
				}
				parts := strings.Split(tp, "-")
				span, parent := h.Get("X-B3-SpanId"), h.Get("X-B3-ParentSpanId")
				if parts[1] != h.Get("X-B3-TraceId") || parts[2] != span {
					t.Fatalf("B3 与 traceparent 不同源: %q %q %q", parts[1], parts[2], tp)
				}
				if !hex16Re.MatchString(span) || !hex16Re.MatchString(parent) || h.Get("X-B3-Sampled") != "1" {
					t.Fatalf("B3 span/parent/sampled 不对: %q %q %q", span, parent, h.Get("X-B3-Sampled"))
				}
				if want := parts[1] + "-" + parts[2] + "-1-" + parent; h.Get("b3") != want {
					t.Fatalf("b3 = %q, want %q", h.Get("b3"), want)
				}
				if want := "codebuddy.session_id=" + conv + ",codebuddy.conversation_request_id=" + reqID; h.Get("baggage") != want {
					t.Fatalf("baggage = %q, want %q", h.Get("baggage"), want)
				}
				// 小写 x-codebuddy-request 必须是独立的一条（Go 的 Set 会改写规范
				// 键名，所以这里直接查原始 map）。
				if v := h["x-codebuddy-request"]; len(v) != 1 || v[0] != "1" {
					t.Fatalf("缺少小写 x-codebuddy-request: %v", h)
				}

				// Stainless 族：仅 CLI 档，值逐字对齐；Retry-Count 恒 0。
				stainless := map[string]string{
					"X-Stainless-Lang":            "js",
					"X-Stainless-Package-Version": "6.25.0",
					"X-Stainless-OS":              "Windows",
					"X-Stainless-Arch":            "x64",
					"X-Stainless-Runtime":         "node",
					"X-Stainless-Runtime-Version": "v22.14.0",
					"X-Stainless-Retry-Count":     "0",
				}
				for k, v := range stainless {
					if prof.Stainless() {
						if got := h.Get(k); got != v {
							t.Fatalf("%s = %q, want %q", k, got, v)
						}
					} else if got := h.Get(k); got != "" {
						t.Fatalf("%s 档不应发 %s，got %q", prof.Name, k, got)
					}
				}

				// 设备标识默认不发。
				if h.Get("X-Machine-ID") != "" || h.Get("X-Session-ID") != "" {
					t.Fatal("默认不应发送 X-Machine-ID / X-Session-ID")
				}
			})
		}
	}
}

// 会话 ID 账号级稳定；不同账号不同；reset 后重新生成。
func TestConversationIDStable(t *testing.T) {
	resetIdentityForTest()
	a := conversationIDFor("acc-a")
	if !uuidV4Re.MatchString(a) {
		t.Fatalf("会话 ID 不是 UUID v4: %q", a)
	}
	if b := conversationIDFor("acc-a"); b != a {
		t.Fatalf("同一账号会话 ID 应稳定: %q vs %q", a, b)
	}
	if c := conversationIDFor("acc-b"); c == a || !uuidV4Re.MatchString(c) {
		t.Fatalf("不同账号应有不同会话 ID: %q vs %q", a, c)
	}
	resetIdentityForTest()
	if d := conversationIDFor("acc-a"); d == a {
		t.Fatal("reset 后应重新生成会话 ID")
	}
}

// uuidV7：12 位毫秒时间 + 20 位随机，共 32 位 hex、不带连字符。
func TestUUIDV7(t *testing.T) {
	v := uuidV7()
	if !hex32Re.MatchString(v) {
		t.Fatalf("uuidV7 应为 32 位 hex: %q", v)
	}
	if len(v) != 32 {
		t.Fatalf("uuidV7 长度 = %d", len(v))
	}
	// 前 12 位应接近当前毫秒时间戳（允许 10 分钟误差）。
	var ms int64
	for _, ch := range v[:12] {
		ms = ms*16 + int64(strings.IndexRune("0123456789abcdef", ch))
	}
	now := time.Now().UnixMilli()
	if ms < now-600_000 || ms > now+600_000 {
		t.Fatalf("uuidV7 时间前缀 %d 与当前 %d 偏差过大", ms, now)
	}
}

// derive/deriveUuid 逐字复刻参考实现；machineHeaders 默认关、显式开才发。
func TestDeriveMachineHeaders(t *testing.T) {
	if got := deriveHex("wb2a", "machine", "uid-1"); got[:8] != "e1e2b867" || len(got) != 64 {
		t.Fatalf("derive machine = %q", got)
	}
	if got := deriveUUID("wb2a", "session", "uid-1"); got != "4a4cd596-e45a-41b8-8377-c00532773913" {
		t.Fatalf("derive session = %q", got)
	}

	creds := map[string]string{"user_id": "uid-1"}
	get := func(k string) string { return creds[k] }
	if got := machineHeadersFor(identityCLI, get); got != nil {
		t.Fatalf("默认不应产生设备头: %v", got)
	}
	creds["device_headers"] = "true"
	got := machineHeadersFor(identityCLI, get)
	if got["X-Machine-ID"] != "e1e2b867" || got["X-Session-ID"] != "4a4cd596-e45a-41b8-8377-c00532773913" {
		t.Fatalf("设备头 = %v", got)
	}
	// 没 uid 时即使打开也不发。
	delete(creds, "user_id")
	if got := machineHeadersFor(identityCLI, get); got != nil {
		t.Fatalf("无 uid 不应产生设备头: %v", got)
	}
}

// available 顺序稳定，且返回副本（调用方修改不影响内部列表）。
func TestAvailableIdentityPresets(t *testing.T) {
	want := []string{"cli", "desktop", "quiet", "browser"}
	got := AvailableIdentityPresets()
	if len(got) != len(want) {
		t.Fatalf("available = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("available[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	got[0] = "mutated"
	if AvailableIdentityPresets()[0] != "cli" {
		t.Fatal("AvailableIdentityPresets 应返回副本")
	}
}

// EffectiveIdentity 反映运行中 setter 的生效值，完整 profile 可直接供 HTTP 层取用。
func TestEffectiveIdentity(t *testing.T) {
	t.Cleanup(func() { _ = SetIdentityPreset("") })
	if err := SetIdentityPreset("quiet"); err != nil {
		t.Fatalf("SetIdentityPreset: %v", err)
	}
	prof := EffectiveIdentity()
	if prof.Name != "quiet" || prof.UA() != "WorkBuddy/5.9.3" || prof.Stainless() ||
		prof.PaceGap != 900*time.Millisecond {
		t.Fatalf("EffectiveIdentity = %+v", prof)
	}
}

func TestRandomHex(t *testing.T) {
	for _, n := range []int{16, 32} {
		v := randomHex(n)
		if len(v) != n {
			t.Fatalf("randomHex(%d) 长度 = %d", n, len(v))
		}
		if !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(v) {
			t.Fatalf("randomHex(%d) = %q", n, v)
		}
	}
	if randomHex(0) != "" {
		t.Fatal("randomHex(0) 应为空")
	}
}

// 计费/积分接口：UA 恒单段桌面形态（参考 billingHeaders 不使用 uaMode），
// 带 Accept-Language / Origin / Referer，不带 Stainless。
func TestBillingHeadersAlignReference(t *testing.T) {
	resetIdentityForTest()
	var h http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h = r.Header.Clone()
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("acc-bill", "workbuddy-cn")
	env.Credentials = map[string]string{"token": "tk", "user_id": "uid-1", "base_url": srv.URL, "enterprise_id": "ent-1"}
	var dst map[string]any
	_ = newProvider(cnProfile).meterCallJSON(context.Background(), env, "tk", "uid-1", "/v2/billing/meter/x", map[string]any{}, &dst)

	if got := h.Get("User-Agent"); got != "WorkBuddy/5.9.3" {
		t.Fatalf("计费 UA 应恒为单段桌面形态，got %q", got)
	}
	for k, v := range map[string]string{
		"Accept-Language":     "zh-CN",
		"Origin":              "https://copilot.tencent.com",
		"Referer":             "https://copilot.tencent.com/",
		"X-Requested-With":    "XMLHttpRequest",
		"X-CodeBuddy-Request": "1",
		"X-Enterprise-Id":     "ent-1",
		"X-Tenant-Id":         "ent-1",
	} {
		if got := h.Get(k); got != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
	if h.Get("X-Stainless-Lang") != "" {
		t.Fatal("计费链路不应发 Stainless")
	}
}

// 登录链路：UA 按档位、Accept-Language/Origin/X-Domain 按区域。
func TestLoginHeadersAlignReference(t *testing.T) {
	resetIdentityForTest()
	var h http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h = r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := httpJSON(http.MethodPost, srv.URL+"/v2/plugin/auth/state", map[string]any{}, false, false); err != nil {
		t.Fatalf("cn httpJSON: %v", err)
	}
	if got := h.Get("User-Agent"); got != identityCLI.UA() {
		t.Fatalf("登录 UA 应按默认档位 %q，got %q", identityCLI.UA(), got)
	}
	for k, v := range map[string]string{
		"Accept-Language":     "zh-CN",
		"Origin":              "https://copilot.tencent.com",
		"X-Domain":            "www.codebuddy.cn",
		"X-No-Authorization":  "true",
		"X-CodeBuddy-Request": "1",
	} {
		if got := h.Get(k); got != v {
			t.Fatalf("cn %s = %q, want %q", k, got, v)
		}
	}

	if _, err := httpJSON(http.MethodPost, srv.URL+"/v2/plugin/auth/state", map[string]any{}, false, true); err != nil {
		t.Fatalf("intl httpJSON: %v", err)
	}
	for k, v := range map[string]string{
		"Accept-Language": "en-US",
		"Origin":          "https://www.workbuddy.ai",
		"X-Domain":        "www.workbuddy.ai",
	} {
		if got := h.Get(k); got != v {
			t.Fatalf("intl %s = %q, want %q", k, got, v)
		}
	}
}

func TestPaceWaitAt(t *testing.T) {
	const gap = 350 * time.Millisecond
	next := map[string]time.Time{}
	now := time.Unix(1000, 0)

	if got := paceWaitAt(next, "", now, gap); got != 0 {
		t.Fatalf("空账号不应节流，got %v", got)
	}
	if got := paceWaitAt(next, "acc", now, gap); got != 0 {
		t.Fatalf("首次请求不应等待，got %v", got)
	}
	if got := paceWaitAt(next, "acc", now, gap); got != gap {
		t.Fatalf("第二次请求应等待 %v，got %v", gap, got)
	}
	if got := paceWaitAt(next, "acc", now, gap); got != 2*gap {
		t.Fatalf("第三次请求应等待 %v，got %v", 2*gap, got)
	}
	if got := paceWaitAt(next, "acc", now.Add(10*gap), gap); got != 0 {
		t.Fatalf("越过时间片后不应等待，got %v", got)
	}
	if got := paceWaitAt(next, "other", now, gap); got != 0 {
		t.Fatalf("不同账号不应共享节流，got %v", got)
	}

	qnext := map[string]time.Time{}
	paceWaitAt(qnext, "q", now, identityQuiet.PaceGap)
	if got := paceWaitAt(qnext, "q", now, identityQuiet.PaceGap); got != 900*time.Millisecond {
		t.Fatalf("quiet 档间隔 = %v, want 900ms", got)
	}
}
