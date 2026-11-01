package gateway

import "testing"

// 关掉 WSL 接入后：不生成注入目标，/api/wsl 明确说"已关闭"而不是"不可用"。
func TestWSLDisabledSkipsTargetsAndView(t *testing.T) {
	g, _, _ := agentApplyTestGateway(t)
	g.settings.WSLDisabled = true

	if got := g.wslTargets(); len(got) != 0 {
		t.Fatalf("关闭后不应有 WSL 目标：%+v", got)
	}
	v := g.wslView()
	if v["disabled"] != true || v["available"] != false {
		t.Fatalf("关闭后视图不对：%+v", v)
	}
	if s, _ := v["reason"].(string); s == "" {
		t.Fatal("关闭时应给出原因")
	}
}
