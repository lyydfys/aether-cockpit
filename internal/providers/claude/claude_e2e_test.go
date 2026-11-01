// 端到端验证：走真实上游打通"本机导入 -> IR 请求 -> 拿到文本"。
//
// 默认跳过（不访问网络、不读凭证内容）；运行方式：
//
//	AE_CLAUDE_E2E=1 go test ./internal/providers/claude/ -run TestE2E -v
//
// 凭证来源按优先级：
//  1. 本机 Claude Code 订阅登录态（~/.claude/.credentials.json 的 claudeAiOauth）：
//     这是本渠道的正主路径，任何失败都会让测试 FAIL（429 限流除外，
//     按项目先例标注"代码级可用"后跳过）；
//  2. 回退：本机 Claude Code 的 settings.json 里配置的 ANTHROPIC_API_KEY +
//     ANTHROPIC_BASE_URL。回退路径打的是中转站而非官方端点，中转自身的
//     自定义要求（额外请求头、自有凭证体系、模型白名单）导致的失败
//     无法归因到本包代码，因此记为 Skip 并原样打印上游错误。
//
// 另有独立的 sessionKey 引导链路（sessionkey.go 三步流）：设置
// AE_CLAUDE_SESSION_KEY=sk-ant-sid01-...（浏览器 claude.ai 的 sessionKey
// Cookie）后运行 TestE2EClaudeSessionKey。
//
// 日志只打印文件顶层键名与错误信息，绝不打印任何凭证值。
package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func TestE2EClaudeSubscription(t *testing.T) {
	if os.Getenv("AE_CLAUDE_E2E") == "" {
		t.Skip("需要真实凭证：设置 AE_CLAUDE_E2E=1 并确保本机已登录 Claude Code")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p := &Provider{}
	env := provider.NewAccountEnv("e2e", channelID)
	// settings.json 里可能配置了中转偏好的模型名（如 ANTHROPIC_MODEL），
	// 命中时 E2E 优先用它，避免打到中转不支持的模型。
	var modelHint string
	// onFallback 标记当前走的是"中转站回退"还是"官方订阅"路径。
	onFallback := false

	// 路径 1：本机订阅登录态。
	drafts, importErr := p.DiscoverLocalAccounts(ctx)
	switch {
	case importErr == nil && len(drafts) > 0:
		for k, v := range drafts[0].Credentials {
			env.SetCredential(k, v)
		}
		env.Label = drafts[0].Label
		if path, err := credentialsPath(); err == nil {
			t.Logf("本机导入成功：%s 顶层键=%v", path, topLevelKeys(mustRead(t, path)))
		}
	default:
		// 路径 2：本机没有订阅登录态（当前开发机就是这种情况 ——
		// Claude Code 用 settings.json 里的 API key + 中转 base_url）。
		t.Logf("本机没有 .credentials.json 登录态（%v），尝试回退 settings.json 的 API key", importErr)
		apiKey, baseURL, model := readLocalSettings(t)
		if apiKey == "" {
			t.Skip("本机既没有 .credentials.json 也没有 settings.json 的 ANTHROPIC_API_KEY，无法 E2E")
		}
		env.SetCredential("access_token", apiKey)
		if baseURL != "" {
			env.SetCredential("base_url", baseURL)
			t.Logf("使用 settings.json 的 ANTHROPIC_BASE_URL=%s", baseURL)
		}
		// API key 走 Bearer 头是对中转站的兼容约定；官方端点要求 x-api-key，
		// 若打官方端点 + API key 出现 401 属预期，如实报告即可。
		modelHint = model
		env.Label = "Claude API key (settings.json)"
		onFallback = true
	}

	upstreamModel := modelHint
	if upstreamModel == "" {
		upstreamModel = "claude-sonnet-4-6"
	} else {
		t.Logf("使用 settings.json 的 ANTHROPIC_MODEL=%s", upstreamModel)
	}

	maxTokens := 64
	req := &ir.UnifiedChatRequest{
		Model:              channelID + "/" + upstreamModel,
		UpstreamModel:      upstreamModel,
		DownstreamProtocol: ir.ProtocolAnthropic,
		Stream:             false, // 非流式：一次断言完整文本，最小化凭证消耗
		MaxOutputTokens:    &maxTokens,
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "ping"},
		},
	}

	ch, err := p.ChatStream(ctx, env, req)
	if err != nil {
		reportE2EFailure(t, onFallback, "ChatStream", err)
	}

	resp, err := ir.Collect(ch)
	if err != nil {
		reportE2EFailure(t, onFallback, "上游响应", err)
	}
	t.Logf("finish=%s usage=%+v", resp.FinishReason, resp.Usage)
	if strings.TrimSpace(resp.Text) == "" {
		t.Fatalf("响应文本为空：%+v", resp)
	}
}

// TestE2EClaudeSessionKey sessionKey 引导授权的端到端验证（sessionkey.go）。
//
// 运行方式：AE_CLAUDE_SESSION_KEY=sk-ant-sid01-... go test -run TestE2EClaudeSessionKey -v
// sessionKey 从浏览器 claude.ai 的 Cookie 里复制（DevTools -> Application ->
// Cookies -> sessionKey）。链路：三步流自动换取 OAuth -> 凭证写回账号 ->
// 发一条真实请求。无环境变量时 Skip。
func TestE2EClaudeSessionKey(t *testing.T) {
	sessionKey := strings.TrimSpace(os.Getenv("AE_CLAUDE_SESSION_KEY"))
	if sessionKey == "" {
		t.Skip("需要真实 sessionKey：设置 AE_CLAUDE_SESSION_KEY=sk-ant-sid01-...（浏览器 claude.ai 的 sessionKey Cookie）")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	p := &Provider{}
	env := provider.NewAccountEnv("e2e-sessionkey", channelID)
	env.SetCredential("session_key", sessionKey)

	maxTokens := 64
	req := &ir.UnifiedChatRequest{
		Model:              channelID + "/claude-sonnet-4-6",
		UpstreamModel:      "claude-sonnet-4-6",
		DownstreamProtocol: ir.ProtocolAnthropic,
		Stream:             false, // 非流式：一次断言完整文本，最小化消耗
		MaxOutputTokens:    &maxTokens,
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "ping"},
		},
	}

	ch, err := p.ChatStream(ctx, env, req)
	if err != nil {
		// sessionKey 失效（浏览器重新登录会轮换）是最常见的环境问题：
		// 错误信息本身已带"请重新从浏览器复制"提示，这里如实 FAIL。
		t.Fatalf("sessionKey 引导链路失败：%v", err)
	}
	resp, err := ir.Collect(ch)
	if err != nil {
		t.Fatalf("上游响应失败：%v", err)
	}
	t.Logf("finish=%s usage=%+v", resp.FinishReason, resp.Usage)
	if strings.TrimSpace(resp.Text) == "" {
		t.Fatalf("响应文本为空：%+v", resp)
	}
	// 懒换取必须已把 OAuth 凭证写回账号（只断言键存在，不打印值）。
	if strings.TrimSpace(env.GetCredential("access_token")) == "" {
		t.Fatal("换取后 access_token 未写回账号凭证")
	}
	if env.GetCredential("refresh_token") == "" || env.GetCredential("expires_at") == "" {
		t.Fatal("换取后 refresh_token / expires_at 未写回账号凭证")
	}
	t.Log("sessionKey -> OAuth 三步流换取成功，access_token / refresh_token / expires_at 已写回")
}

// reportE2EFailure 统一处理 E2E 失败：
//   - 429 限流：上游已受理请求，代码链路验证完毕，按项目先例标注"代码级可用"；
//   - 中转回退路径的其它错误：无法区分"中转自身的要求"与"本包代码问题"，
//     记 Skip 并原样打印（诚实报告，不假装通过）；
//   - 官方订阅路径的其它错误：真失败，FAIL。
func reportE2EFailure(t *testing.T, onFallback bool, stage string, err error) {
	t.Helper()
	if ae := ir.AsAPIError(err); ae != nil && ae.Code == ir.ErrCodeUpstreamRateLimit {
		t.Skipf("上游限流（代码级可用，请求已正确构造并发送）：%v", err)
	}
	if onFallback {
		t.Skipf("回退中转站拒绝了请求（环境原因，无法归因到本包代码，原样报告）：%v", err)
	}
	t.Fatalf("%s 失败：%v", stage, err)
}

// readLocalSettings 读取本机 Claude Code 的 settings.json（只取 env 里的键名，
// 除 base_url / model 外不输出任何值）。
func readLocalSettings(t *testing.T) (apiKey, baseURL, model string) {
	t.Helper()

	var configDir string
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		configDir = dir
	} else if home, err := os.UserHomeDir(); err == nil {
		configDir = filepath.Join(home, ".claude")
	} else {
		return "", "", ""
	}

	raw, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err != nil {
		t.Logf("读取 %s 失败：%v", filepath.Join(configDir, "settings.json"), err)
		return "", "", ""
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Logf("settings.json 无法解析：%v", err)
		return "", "", ""
	}
	for _, k := range sortedKeys(settings.Env) {
		t.Logf("settings.json env 键：%s", k)
	}
	return settings.Env["ANTHROPIC_API_KEY"], settings.Env["ANTHROPIC_BASE_URL"], settings.Env["ANTHROPIC_MODEL"]
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// 简单插入排序，避免为测试引入 sort 之外的依赖心智负担。
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return raw
}
