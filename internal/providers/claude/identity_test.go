package claude

import (
	"encoding/json"
	"strings"
	"testing"
)

// 身份三件套必须稳定：同一账号+机器+会话永远得到同一组 device/account/session。
// 不稳定等于"每次请求都换一台设备"，比缺字段更容易被关联。
func TestBuildClaudeIdentityStable(t *testing.T) {
	a := buildClaudeIdentity("acct-1", "machine-1", "sess-1")
	b := buildClaudeIdentity("acct-1", "machine-1", "sess-1")
	if a != b {
		t.Fatalf("同一输入应得到同一身份：%+v vs %+v", a, b)
	}
	if len(a.DeviceID) != 64 {
		t.Fatalf("device_id 应为 64 位十六进制，得到 %q（len=%d）", a.DeviceID, len(a.DeviceID))
	}
	if strings.Count(a.SessionUUID, "-") != 4 || len(a.SessionUUID) != 36 {
		t.Fatalf("session_id 应为 UUID 形态，得到 %q", a.SessionUUID)
	}
	if strings.Count(a.AccountUUID, "-") != 4 {
		t.Fatalf("account_uuid 应为 UUID 形态，得到 %q", a.AccountUUID)
	}

	// 换机器 → device 与 session 变，账号不变。
	c := buildClaudeIdentity("acct-1", "machine-2", "sess-1")
	if c.DeviceID == a.DeviceID {
		t.Fatal("换机器后 device_id 必须变化")
	}
	if c.AccountUUID != a.AccountUUID {
		t.Fatal("换机器不应改变 account_uuid")
	}
	// 换会话 → 只有 session 变。
	d := buildClaudeIdentity("acct-1", "machine-1", "sess-2")
	if d.SessionUUID == a.SessionUUID || d.DeviceID != a.DeviceID {
		t.Fatalf("换会话只应改变 session：%+v", d)
	}
}

// metadata.user_id 必须是 **JSON 字符串**（上游按字符串解析），不是对象。
func TestApplyClaudeIdentityWritesJSONString(t *testing.T) {
	body := map[string]any{}
	id := buildClaudeIdentity("acct-9", "m-9", "s-9")
	applyClaudeIdentity(body, id)

	meta, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata 缺失或类型错误：%#v", body["metadata"])
	}
	raw, ok := meta["user_id"].(string)
	if !ok {
		t.Fatalf("metadata.user_id 必须是字符串，实际 %T", meta["user_id"])
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("user_id 不是合法 JSON：%v（%q）", err, raw)
	}
	if parsed["device_id"] != id.DeviceID || parsed["session_id"] != id.SessionUUID || parsed["account_uuid"] != id.AccountUUID {
		t.Fatalf("user_id 内容不对：%+v", parsed)
	}
}

// 已有 metadata 时只补 user_id，不覆盖其它字段。
func TestApplyClaudeIdentityKeepsExistingMetadata(t *testing.T) {
	body := map[string]any{"metadata": map[string]any{"other": "keep"}}
	applyClaudeIdentity(body, buildClaudeIdentity("a", "m", "s"))
	meta := body["metadata"].(map[string]any)
	if meta["other"] != "keep" {
		t.Fatalf("不应覆盖已有 metadata：%#v", meta)
	}
	if _, ok := meta["user_id"]; !ok {
		t.Fatal("应写入 user_id")
	}
}

// 请求 ID 每请求唯一：复用同一个 ID 会被当成重放。
func TestNewRequestUUIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id := newRequestUUID()
		if len(id) != 36 || strings.Count(id, "-") != 4 {
			t.Fatalf("UUID 形态不对：%q", id)
		}
		if seen[id] {
			t.Fatalf("出现重复请求 ID：%q", id)
		}
		seen[id] = true
	}
}
