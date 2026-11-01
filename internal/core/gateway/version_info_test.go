package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPickBuildTime(t *testing.T) {
	if v, s := pickBuildTime("2026-10-29T01:40:00Z", "2026-10-28T15:44:45Z"); v != "2026-10-29T01:40:00Z" || s != "ldflags" {
		t.Fatalf("注入值应优先：%q %q", v, s)
	}
	if v, s := pickBuildTime("", "2026-10-28T15:44:45Z"); v != "2026-10-28T15:44:45Z" || s != "vcs-commit" {
		t.Fatalf("没有注入时应回退并标注来源：%q %q", v, s)
	}
	if v, s := pickBuildTime("", ""); v != "" || s != "unknown" {
		t.Fatalf("都没有时应是 unknown：%q %q", v, s)
	}
}

// /version 的形状被运维脚本消费：字段改名或缺失都算回归。build_time 必须
// 带来源，否则分不清它是构建时间还是提交时间。
func TestVersionHandlerReportsBuildTimeSource(t *testing.T) {
	g := &Gateway{startedAt: time.Now()}
	rec := httptest.NewRecorder()
	g.handleVersion(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "go", "revision", "build_time", "build_time_source", "uptime_sec"} {
		if _, ok := out[k]; !ok {
			t.Fatalf("/version 缺字段 %q：%v", k, out)
		}
	}
	if out["build_time_source"] == "unknown" && out["build_time"] != "" {
		t.Fatalf("来源与值矛盾：%v", out)
	}
}
