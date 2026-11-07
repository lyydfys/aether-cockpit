package gateway

import (
	"net/http"
	"strings"
	"testing"
)

// 纯中文渠道名必须仍然能建出渠道：slugify 只认 ASCII，中文会被清成空串，
// 所以要回退到 `relay-<短哈希>`。历史上这里会退化成空 slug 直接返回"没建"，
// 用户看到的是"保存成功但渠道没出现"。
func TestCustomChannelChineseNameGetsStableSlug(t *testing.T) {
	g := newSupplierGateway(t)
	body := `{"name":"公司中转","chat":"https://relay.example.com/v1/chat/completions","key":"sk-x"}`
	if rec := saveSupplier(g, body); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	list := supplierChannels(g)
	if len(list) != 1 {
		t.Fatalf("渠道数 = %d，want 1", len(list))
	}
	ch := list[0]
	if ch.Name != "公司中转" {
		t.Fatalf("name = %q，want 公司中转", ch.Name)
	}
	if !strings.HasPrefix(ch.ID, "relay-") {
		t.Fatalf("中文名应当回退到 relay-<hash>，实际 id = %q", ch.ID)
	}
	if len(ch.ID) <= len("relay-") {
		t.Fatalf("id 缺少哈希部分: %q", ch.ID)
	}

	// 同一个名字重复保存必须是同一条渠道（幂等），不能每次生成新 id。
	if rec := saveSupplier(g, body); rec.Code != http.StatusOK {
		t.Fatalf("二次 save: %d %s", rec.Code, rec.Body.String())
	}
	if got := len(supplierChannels(g)); got != 1 {
		t.Fatalf("二次保存后渠道数 = %d，want 1（幂等）", got)
	}
}

// 清单为空且探测失败的自定义渠道，/v1/models 至少要能看见它（通配提示项）。
func TestEmptyCustomChannelStillVisibleInModels(t *testing.T) {
	g := newSupplierGateway(t)
	// 指向一个必然连不通的地址：探测会失败，清单保持为空。
	body := `{"id":"dead-relay","name":"Dead","chat":"http://127.0.0.1:1/v1/chat/completions","key":"sk-x"}`
	if rec := saveSupplier(g, body); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	hidden := map[string]bool{}
	avail := g.availableModelIDs()
	wild := g.customChannelWildcards(hidden, avail)
	if len(wild) != 1 {
		t.Fatalf("通配项数 = %d，want 1（渠道清单为空时应给一条兜底）", len(wild))
	}
	if id, _ := wild[0]["id"].(string); id != "custom-dead-relay/*" {
		t.Fatalf("通配 id = %q，want custom-dead-relay/*", id)
	}

	// 有模型清单的渠道不应再出现通配项。
	g.settingsMu.Lock()
	g.settings.CustomChannels = append(g.settings.CustomChannels, CustomChannel{
		ID: "filled", Name: "Filled", BaseURL: "https://x.example.com/v1", Models: []string{"m1"},
	})
	g.settingsMu.Unlock()
	if got := len(g.customChannelWildcards(hidden, avail)); got != 1 {
		t.Fatalf("加一条有模型的渠道后通配项 = %d，want 1（有清单的渠道不生成兜底）", got)
	}
}
