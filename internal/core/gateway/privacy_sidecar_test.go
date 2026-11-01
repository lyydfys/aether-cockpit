package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
)

// 用测试二进制自身充当边车（Go 标准的 helper-process 模式）：
// 子进程被同一个测试函数接管，读写行协议；父进程走真实的 spawn 路径。
func TestSidecarMaskRoundTrip(t *testing.T) {
	if os.Getenv("AETHER_FAKE_SIDECAR") == "1" {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
		for sc.Scan() {
			var req struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
				continue
			}
			out := strings.ReplaceAll(req.Text, "张三", "[PERSON_1]")
			resp := map[string]any{
				"text":    out,
				"mapping": map[string]string{"[PERSON_1]": "张三"},
			}
			b, _ := json.Marshal(resp)
			fmt.Println(string(b))
		}
		os.Exit(0)
	}

	t.Setenv("AETHER_FAKE_SIDECAR", "1")
	c, err := newSidecarClient(SidecarConfig{Command: os.Args[0], Args: []string{"-test.run=TestSidecarMaskRoundTrip"}})
	if err != nil {
		// fork 自身在内存吃紧时会 ENOMEM（"paging file is too small"），
		// 环境上限按 NOT-RUN 处理，不报成缺陷。
		if resourceStarved(err.Error()) {
			t.Skipf("系统资源不足，边车子进程起不来：%v", err)
		}
		t.Fatalf("启动边车失败: %v", err)
	}
	defer c.Close()

	out, mapping, err := c.Mask(context.Background(), "我是张三，邮箱 a@b.com")
	if err != nil {
		t.Fatalf("Mask: %v", err)
	}
	if !strings.Contains(out, "[PERSON_1]") || strings.Contains(out, "张三") {
		t.Fatalf("边车未替换实体: %q", out)
	}
	if mapping["[PERSON_1]"] != "张三" {
		t.Fatalf("映射未返回: %v", mapping)
	}

	// 第二次调用复用同一进程，仍要能正常往返。
	if out2, _, err := c.Mask(context.Background(), "张三又来了"); err != nil || !strings.Contains(out2, "[PERSON_1]") {
		t.Fatalf("第二次调用失败: %q err=%v", out2, err)
	}
}

// 边车命令不存在时必须报错——引擎会因此 fail-closed，不能静默降级。
func TestSidecarStartFailure(t *testing.T) {
	if _, err := newSidecarClient(SidecarConfig{Command: "definitely-not-a-real-binary-xyz"}); err == nil {
		t.Fatal("不存在的命令应返回错误")
	}
}

// 配置了边车时，遮蔽走边车并在回复里还原。
func TestPrivacyEngineWithSidecar(t *testing.T) {
	t.Setenv("AETHER_FAKE_SIDECAR", "1")
	e, err := newPrivacyEngine(PrivacyMaskConfig{
		Enabled: true,
		Rules:   []string{"emails"},
		Sidecar: &SidecarConfig{Command: os.Args[0], Args: []string{"-test.run=TestSidecarMaskRoundTrip"}},
	})
	if err != nil {
		t.Fatalf("newPrivacyEngine: %v", err)
	}
	defer e.sidecar.Close()

	req := &ir.UnifiedChatRequest{
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "张三的邮箱 a@b.com"}},
	}
	u, err := e.maskRequest(req)
	if err != nil {
		t.Fatalf("maskRequest: %v", err)
	}
	text := req.Items[0].Text
	if strings.Contains(text, "张三") || strings.Contains(text, "a@b.com") {
		t.Fatalf("未遮蔽: %q", text)
	}
	if u == nil {
		t.Fatal("应返回还原器")
	}
	// 占位符出现在回复里时还原成人名。
	out := u.push(text) + u.flush()
	if !strings.Contains(out, "张三") {
		t.Fatalf("未还原: %q", out)
	}
}
