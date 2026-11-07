package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"aether-cockpit/internal/providers/workbuddy"
)

// 四档的身份接口契约：字段名、取值、available 顺序、CLI/Stainless 版本的空值语义。
func TestWorkBuddyIdentityEndpoint(t *testing.T) {
	t.Cleanup(func() { _ = workbuddy.SetIdentityPreset("cli") })
	reset := func(name string) {
		if err := workbuddy.SetIdentityPreset(name); err != nil {
			t.Fatalf("SetIdentityPreset(%q): %v", name, err)
		}
	}

	cases := []struct {
		preset       string
		ua           string
		cliVersion   string
		stainless    bool
		stainlessVer string
		paceMs       int64
	}{
		{
			preset:     "cli",
			ua:         "WorkBuddy/5.9.3 WorkBuddy/5.9.3 CLI/2.165.0",
			cliVersion: "2.165.0", stainless: true, stainlessVer: "6.25.0", paceMs: 350,
		},
		{
			preset:     "desktop",
			ua:         "WorkBuddy/5.9.3",
			cliVersion: "", stainless: false, stainlessVer: "", paceMs: 350,
		},
		{
			preset:     "quiet",
			ua:         "WorkBuddy/5.9.3",
			cliVersion: "", stainless: false, stainlessVer: "", paceMs: 900,
		},
		{
			preset: "browser",
			ua: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
				"(KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
			cliVersion: "", stainless: false, stainlessVer: "", paceMs: 350,
		},
	}

	for _, c := range cases {
		t.Run(c.preset, func(t *testing.T) {
			reset(c.preset)
			g := &Gateway{}
			rec := httptest.NewRecorder()
			g.handleWorkBuddyIdentity(rec, httptest.NewRequest(http.MethodGet, "/api/providers/workbuddy/identity", nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
			}
			var got struct {
				Identity       string   `json:"identity"`
				UA             string   `json:"ua"`
				Product        string   `json:"product"`
				IDeVersion     string   `json:"ide_version"`
				CLIVersion     string   `json:"cli_version"`
				Stainless      bool     `json:"stainless"`
				StainlessVer   string   `json:"stainless_version"`
				PaceMs         int64    `json:"pace_ms"`
				MachineHeaders bool     `json:"machine_headers"`
				Available      []string `json:"available"`
				Saved          *string  `json:"saved"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v (%s)", err, rec.Body.String())
			}

			if got.Identity != c.preset {
				t.Fatalf("identity = %q, want %q", got.Identity, c.preset)
			}
			if got.UA != c.ua {
				t.Fatalf("ua = %q, want %q", got.UA, c.ua)
			}
			if got.Product != "SaaS" || got.IDeVersion != "5.9.3" {
				t.Fatalf("product/ide_version = %q/%q", got.Product, got.IDeVersion)
			}
			if got.CLIVersion != c.cliVersion {
				t.Fatalf("cli_version = %q, want %q", got.CLIVersion, c.cliVersion)
			}
			if got.Stainless != c.stainless {
				t.Fatalf("stainless = %v, want %v", got.Stainless, c.stainless)
			}
			if got.StainlessVer != c.stainlessVer {
				t.Fatalf("stainless_version = %q, want %q", got.StainlessVer, c.stainlessVer)
			}
			if got.PaceMs != c.paceMs {
				t.Fatalf("pace_ms = %d, want %d", got.PaceMs, c.paceMs)
			}
			if got.MachineHeaders {
				t.Fatal("machine_headers 默认应为 false")
			}
			if want := []string{"cli", "desktop", "quiet", "browser"}; !reflect.DeepEqual(got.Available, want) {
				t.Fatalf("available = %v, want %v", got.Available, want)
			}
			if got.Saved != nil {
				t.Fatalf("未保存配置时不应回 saved，got %q", *got.Saved)
			}
		})
	}
}

// identity 必须是"运行中生效"值；saved 只反映 settings.json 的保存值，二者可不同。
func TestWorkBuddyIdentitySavedVsEffective(t *testing.T) {
	t.Cleanup(func() { _ = workbuddy.SetIdentityPreset("cli") })
	if err := workbuddy.SetIdentityPreset("cli"); err != nil {
		t.Fatalf("SetIdentityPreset(cli): %v", err)
	}

	g := &Gateway{}
	g.settings.WorkBuddyIdentity = "quiet"
	rec := httptest.NewRecorder()
	g.handleWorkBuddyIdentity(rec, httptest.NewRequest(http.MethodGet, "/api/providers/workbuddy/identity", nil))

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["identity"] != "cli" {
		t.Fatalf("identity 应为生效值 cli，got %v", got["identity"])
	}
	if got["saved"] != "quiet" {
		t.Fatalf("saved 应为保存值 quiet，got %v", got["saved"])
	}

	// 保存值为空：saved 字段省略（不是空串）。
	g2 := &Gateway{}
	rec2 := httptest.NewRecorder()
	g2.handleWorkBuddyIdentity(rec2, httptest.NewRequest(http.MethodGet, "/api/providers/workbuddy/identity", nil))
	var got2 map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &got2); err != nil {
		t.Fatalf("decode2: %v", err)
	}
	if _, ok := got2["saved"]; ok {
		t.Fatalf("空保存值不应出现 saved 字段: %v", got2)
	}
}
