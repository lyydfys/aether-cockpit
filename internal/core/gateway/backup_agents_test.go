package gateway

import (
	"encoding/json"
	"testing"

	"aether-cockpit/internal/core/clients"
)

// 恢复接线时只动"装了、且当前没有我们接线"的客户端。
func TestRestorableAgentsGuard(t *testing.T) {
	agents := []backupAgentMeta{
		{ID: "detected-clean", Model: "auto/x"},  // 装了、没接线 → 应恢复
		{ID: "already-managed", Model: "auto/y"}, // 已经接过 → 不覆盖
		{ID: "not-installed", Model: "auto/z"},   // 没装 → 跳过
		{ID: "unknown-in-backup"},                // 本机没有这个 id → 跳过
	}
	status := map[string]clients.Status{
		"detected-clean":  {ID: "detected-clean", Detected: true},
		"already-managed": {ID: "already-managed", Detected: true, Managed: &clients.ManagedInfo{BaseURL: "http://127.0.0.1:7860"}},
		"not-installed":   {ID: "not-installed", Detected: false},
	}
	got := restorableAgents(agents, status)
	if len(got) != 1 || got[0].ID != "detected-clean" {
		t.Fatalf("只应恢复 detected-clean，得到 %+v", got)
	}
	if got[0].Model != "auto/x" {
		t.Fatalf("应带上备份里的模型，得到 %q", got[0].Model)
	}
}

// 空输入不该 panic，也不该凭空恢复东西。
func TestRestorableAgentsEmpty(t *testing.T) {
	if got := restorableAgents(nil, map[string]clients.Status{}); len(got) != 0 {
		t.Fatalf("空备份应返回空，得到 %+v", got)
	}
	if got := restorableAgents([]backupAgentMeta{{ID: "x"}}, nil); len(got) != 0 {
		t.Fatalf("没有本机状态时不该恢复，得到 %+v", got)
	}
}

// 备份里的 agents 部分只能含可安全字段：绝不能把密钥写进备份。
func TestBackupAgentsCarryNoSecrets(t *testing.T) {
	bundle := backupBundle{
		Agents: []backupAgentMeta{
			{ID: "claude_code", Name: "Claude Code", Kind: "json", BaseURL: "http://127.0.0.1:7860", Model: "auto/claude-sonnet-4"},
		},
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	s := string(raw)
	for _, bad := range []string{"sk-", "api_key", "apiKey", "token", "secret", "password"} {
		if containsFoldASCII(s, bad) {
			t.Fatalf("备份不该出现 %q：%s", bad, s)
		}
	}
	// 但接线信息必须在：换机后靠它恢复。
	for _, want := range []string{"claude_code", "http://127.0.0.1:7860", "auto/claude-sonnet-4"} {
		if !containsFoldASCII(s, want) {
			t.Fatalf("备份缺少接线信息 %q：%s", want, s)
		}
	}
}

// containsFoldASCII 做大小写无关的包含判断（备份字段名大小写不统一）。
func containsFoldASCII(s, sub string) bool {
	if sub == "" {
		return true
	}
	ls := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		ls = append(ls, c)
	}
	lsub := make([]byte, 0, len(sub))
	for i := 0; i < len(sub); i++ {
		c := sub[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		lsub = append(lsub, c)
	}
	return indexOf(ls, lsub) >= 0
}

func indexOf(hay, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
