package gateway

// 真实预设结构（用户提供的三份 Chat Completion 导出，47–51 个顶层键）的解析测试。
//
// 这些样例按实测键名逐条构造（不含真实文件里的私家人设正文），重点验证：
//   - 六个采样参数进 body（top_k 在 Gemini 是标准参数，必须保留）；
//   - prompts[] + prompt_order[] 拼装 system_prompt（顺序/启用/marker/use_sysprompt）；
//   - 三类丢弃分组（上游不通用 / 客户端行为 / 未识别）与 32KB 上限标记；
//   - 大文件（>64KB，实测 330KB–1.6MB）不再被误拒。

import (
	"strings"
	"testing"
)

// realPresetSample 用真实键名构造一份 Chat Completion 预设。
func realPresetSample() string {
	return `{
		"name": "实测结构预设",
		"temperature": 0.85,
		"frequency_penalty": 0.1,
		"presence_penalty": 0.2,
		"top_p": 0.95,
		"top_k": 40,
		"top_a": 0.3,
		"min_p": 0.05,
		"repetition_penalty": 1.1,
		"max_context_unlocked": true,
		"openai_max_context": 1000000,
		"openai_max_tokens": 8192,
		"names_behavior": 2,
		"send_if_empty": "",
		"impersonation_prompt": "[Impersonation]",
		"new_chat_prompt": "",
		"new_group_chat_prompt": "",
		"new_example_chat_prompt": "",
		"continue_nudge_prompt": "[Continue]",
		"group_nudge_prompt": "",
		"bias_preset_selected": "Default",
		"wi_format": "{0}",
		"scenario_format": "{{scenario}}",
		"personality_format": "{{personality}}",
		"media_inlining": false,
		"inline_image_quality": "auto",
		"continue_prefill": false,
		"continue_postfix": " ",
		"stream_openai": true,
		"use_sysprompt": true,
		"squash_system_messages": false,
		"assistant_prefill": "",
		"assistant_impersonation": "",
		"function_calling": false,
		"tool_call_recurse_limit": 5,
		"tool_reasoning_mode": "auto",
		"show_thoughts": true,
		"reasoning_effort": "medium",
		"claude_fast_mode": false,
		"strip_old_tool_calls": false,
		"extensions": {"note": "ext"},
		"verbosity": "high",
		"prompts": [
			{"identifier": "main", "name": "Main Prompt", "content": "主提示：你是角色扮演助手。\n保持中文，别用英文。", "system_prompt": true, "marker": false},
			{"identifier": "sys-late", "name": "后置系统", "content": "系统补充：\n禁止输出「测试」二字", "system_prompt": true, "marker": false},
			{"identifier": "user-note", "name": "用户备注", "content": "普通段落：\"引号\"与换行\n第二行", "system_prompt": false, "marker": false},
			{"identifier": "chatHistory", "name": "Chat History", "content": "", "system_prompt": false, "marker": true},
			{"identifier": "disabled-one", "name": "被停用", "content": "不该出现", "system_prompt": false, "marker": false},
			{"identifier": "empty-one", "name": "空内容", "content": "   ", "system_prompt": false, "marker": false}
		],
		"prompt_order": [
			{"character_id": 100001, "order": [
				{"identifier": "disabled-one", "enabled": false},
				{"identifier": "chatHistory", "enabled": true},
				{"identifier": "user-note", "enabled": true},
				{"identifier": "main", "enabled": true},
				{"identifier": "sys-late", "enabled": true},
				{"identifier": "empty-one", "enabled": true}
			]}
		]
	}`
}

func TestParseRealStructureChatPreset(t *testing.T) {
	preview, err := parseSillyTavernPreset(realPresetSample(), "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if preview.Format != "chat" {
		t.Fatalf("格式应为 chat，实际 %q", preview.Format)
	}
	if preview.Name != "实测结构预设" {
		t.Fatalf("名称 = %q", preview.Name)
	}

	// 1) 六个采样参数进 body（top_k 必须保留：Gemini 标准参数）
	wantBody := map[string]float64{
		"temperature":       0.85,
		"top_p":             0.95,
		"top_k":             40,
		"frequency_penalty": 0.1,
		"presence_penalty":  0.2,
		"max_tokens":        8192,
	}
	for key, want := range wantBody {
		if got := presetBodyNumber(t, preview, key); got != want {
			t.Fatalf("body[%s] = %v，期望 %v", key, got, want)
		}
	}
	if len(preview.Body) != len(wantBody) {
		t.Fatalf("body 应只有六个采样参数，实际 %v", preview.Body)
	}

	// 2) 丢弃分组
	upstream := []string{"top_a", "min_p", "repetition_penalty", "max_context_unlocked", "openai_max_context"}
	for _, key := range upstream {
		drop, ok := findPresetDrop(preview, key)
		if !ok || drop.Group != presetDropGroupUpstream {
			t.Fatalf("%s 应归入上游不通用，实际 %+v", key, drop)
		}
	}
	client := []string{
		"stream_openai", "use_sysprompt", "names_behavior", "send_if_empty",
		"impersonation_prompt", "new_chat_prompt", "continue_nudge_prompt",
		"bias_preset_selected", "wi_format", "scenario_format", "personality_format",
		"media_inlining", "inline_image_quality", "continue_prefill", "continue_postfix",
		"assistant_prefill", "assistant_impersonation", "squash_system_messages",
		"function_calling", "tool_call_recurse_limit", "tool_reasoning_mode",
		"show_thoughts", "reasoning_effort", "claude_fast_mode", "strip_old_tool_calls",
		"extensions",
	}
	for _, key := range client {
		drop, ok := findPresetDrop(preview, key)
		if !ok || drop.Group != presetDropGroupClient {
			t.Fatalf("%s 应归入客户端行为，实际 %+v", key, drop)
		}
	}
	if drop, ok := findPresetDrop(preview, "verbosity"); !ok || drop.Group != presetDropGroupUnknown {
		t.Fatalf("未列入口径的键应归入 unknown，实际 %+v", drop)
	}

	// 3) prompts 拼装：use_sysprompt=true → main 最前；system_prompt:true 次之；其余按原顺序
	if !preview.PromptIncluded {
		t.Fatal("默认应导入提示词")
	}
	text := preview.SystemPrompt
	head := text
	if len(head) > 40 {
		head = head[:40]
	}
	if !strings.HasPrefix(text, "主提示：你是角色扮演助手。") {
		t.Fatalf("main 应在最前，实际开头：%q", head)
	}
	// marker / disabled / 空内容都不能出现
	for _, banned := range []string{"不该出现", "Chat History"} {
		if strings.Contains(text, banned) {
			t.Fatalf("提示词里不应出现 %q", banned)
		}
	}
	// system_prompt:true 的 sys-late 要排在普通段落之前
	if strings.Index(text, "系统补充") > strings.Index(text, "普通段落") {
		t.Fatalf("system_prompt:true 段落应优先：%q", text)
	}
	// 块之间用空行分隔，多行/中文/引号原样保留
	if !strings.Contains(text, "禁止输出「测试」二字") || !strings.Contains(text, `"引号"`) {
		t.Fatalf("多行与中文引号应原样保留：%q", text)
	}
	if !strings.Contains(text, "\n\n") {
		t.Fatalf("段落之间应用空行分隔：%q", text)
	}
	// 3 段（main + sys-late + user-note），marker/disabled/empty 被跳过
	if preview.PromptSegments != 3 {
		t.Fatalf("应拼 3 段，实际 %d（%q）", preview.PromptSegments, text)
	}
	if preview.PromptChars <= 0 || preview.PromptBytes <= 0 {
		t.Fatalf("字符统计不对：chars=%d bytes=%d", preview.PromptChars, preview.PromptBytes)
	}
	if preview.PromptOverflow {
		t.Fatal("这份样例不该溢出 32KB")
	}
}

func TestParseRealStructureWithoutPromptImport(t *testing.T) {
	preview, err := parseSillyTavernPreset(realPresetSample(), "chat", false)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if preview.PromptIncluded || preview.SystemPrompt != "" {
		t.Fatalf("关掉提示词导入后不应产出 system_prompt: %+v", preview)
	}
	// 采样参数照常
	if got := presetBodyNumber(t, preview, "top_k"); got != 40 {
		t.Fatalf("只导入参数时 top_k 仍应在 body，实际 %v", got)
	}
	// 统计信息仍在，前端才能显示"可导入 N 段 / X 字"
	if preview.PromptSegments != 3 || preview.PromptChars <= 0 {
		t.Fatalf("段数/字数应仍然可观测：segments=%d chars=%d", preview.PromptSegments, preview.PromptChars)
	}
}

func TestParseRealStructurePromptOverflow(t *testing.T) {
	// 造一个超过 32KB 的 prompts：6 段 × 8KB
	block := strings.Repeat("长", 4096+1024)
	var prompts strings.Builder
	var order strings.Builder
	for i := 0; i < 6; i++ {
		id := "seg-" + string(rune('a'+i))
		if i > 0 {
			prompts.WriteString(",")
			order.WriteString(",")
		}
		prompts.WriteString(`{"identifier":"` + id + `","content":"` + block + `","marker":false}`)
		order.WriteString(`{"identifier":"` + id + `","enabled":true}`)
	}
	raw := `{"temperature":1.0,"prompts":[` + prompts.String() + `],"prompt_order":[{"character_id":100000,"order":[` + order.String() + `]}]}`
	preview, err := parseSillyTavernPreset(raw, "")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !preview.PromptOverflow {
		t.Fatalf("超过 32KB 应标记溢出：bytes=%d limit=%d", preview.PromptBytes, preview.PromptLimit)
	}
	if preview.PromptLimit != presetSystemPromptLimit {
		t.Fatalf("limit = %d", preview.PromptLimit)
	}
	if preview.PromptBytes <= preview.PromptLimit {
		t.Fatalf("样例应真的超限：bytes=%d", preview.PromptBytes)
	}
}

// 真实文件 330KB–1.6MB：旧的 64KB 上限会把它们全部拒掉，这里回归一次。
func TestParseLargeRealStructureAccepted(t *testing.T) {
	padding := strings.Repeat("人设正文", 40_000) // ≈ 480KB
	raw := `{"name":"大文件","temperature":1.0,"prompts":[{"identifier":"main","content":"` +
		padding + `","system_prompt":true}],"prompt_order":[{"character_id":100000,"order":[{"identifier":"main","enabled":true}]}]}`
	if len(raw) < 300_000 {
		t.Fatalf("样例应大于 300KB，实际 %d", len(raw))
	}
	preview, err := parseSillyTavernPreset(raw, "")
	if err != nil {
		t.Fatalf("大文件不应被拒: %v", err)
	}
	if !preview.PromptIncluded || preview.PromptBytes < 300_000 {
		t.Fatalf("大文件提示词应被解析：included=%v bytes=%d", preview.PromptIncluded, preview.PromptBytes)
	}
	if !preview.PromptOverflow {
		t.Fatal("480KB 提示词必然超过 32KB 落库上限，应标记溢出")
	}
}
