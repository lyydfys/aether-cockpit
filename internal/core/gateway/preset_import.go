package gateway

// 酒馆预设（SillyTavern 风格）导入：**纯解析**，不写任何状态。
//
// 两种格式都吃：
//   - Chat Completion 预设：temperature / top_p / frequency_penalty / presence_penalty /
//     openai_max_tokens / prompts[] / prompt_order[] …（用户提供的三份实测文件都是这种）
//   - Text Generation 预设：temp / top_p / top_k / rep_pen / max_length / min_p / typical_p …
//
// 实测（三份真实导出，47–51 个顶层键，330KB–1.6MB）：人设/破甲内容都在 `prompts[]`
// 的 `content` 里，顺序与启用状态在 `prompt_order[].order[]`。所以 system_prompt 不是
// 顶层字段，而是要**按 prompt_order 拼装**。字段名依据真实文件；解析器对未知字段、
// 缺字段、数值写成字符串、null 都容忍。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// 真实预设文件 330KB–1.6MB（prompts 里是整段人设文本），输入上限放到 8MB。
	presetImportMaxBytes = 8 << 20
	// 落库上限：`handlePresetSave` 对 system_prompt 的 32KB 限制（request_presets.go:423）。
	presetSystemPromptLimit = 32 << 10
	// 拼装 system_prompt 时块与块之间的分隔。
	presetSegmentSeparator = "\n\n"
)

// 丢弃分组：前端分组展示，用户才知道不是我们偷懒。
const (
	presetDropGroupGateway  = "gateway"  // 语义冲突：网关自管
	presetDropGroupUpstream = "upstream" // 上游不通用：发了会 400 或被忽略
	presetDropGroupClient   = "client"   // 客户端行为：不是请求参数
	presetDropGroupUnknown  = "unknown"  // 未识别
)

// presetImportField 是一条成功映射（目标键 + 来源字段）。
type presetImportField struct {
	Key    string `json:"key"`
	Source string `json:"source"`
}

// presetImportDrop 是一条被丢弃的字段（key + 分组 + 原因）。
type presetImportDrop struct {
	Key    string `json:"key"`
	Group  string `json:"group"`
	Reason string `json:"reason"`
}

// presetImportPreview 是 `POST /api/presets/import/preview` 的响应形态。
type presetImportPreview struct {
	Format       string         `json:"format"`
	Name         string         `json:"name"`
	Body         map[string]any `json:"body"`
	SystemPrompt string         `json:"system_prompt"`
	// 提示词拼装的可观测信息：客户端用它显示"将写入 N 段 / 共 X 字"并做 32KB 拦截。
	PromptIncluded bool                `json:"prompt_included"`
	PromptSegments int                 `json:"prompt_segments"`
	PromptChars    int                 `json:"prompt_chars"`
	PromptBytes    int                 `json:"prompt_bytes"`
	PromptLimit    int                 `json:"prompt_limit"`
	PromptOverflow bool                `json:"prompt_overflow"`
	Mapped         []presetImportField `json:"mapped"`
	Dropped        []presetImportDrop  `json:"dropped"`
}

// 网关自管字段：写进预设会被 `validatePresetBody` 直接拒绝（provider_headers.go:455-459）。
var presetGatewayOwnedKeys = map[string]bool{
	"model": true, "openai_model": true, "stream": true, "messages": true,
	"chat_completion_source": true, "tools": true, "input": true,
	"contents": true, "system": true, "instructions": true,
}

// 上游不通用：发了会 400，或被上游静默忽略（等于假支持）。
var presetUpstreamUnsupportedReasons = map[string]string{
	"top_a":                "非 OpenAI 标准参数，严格上游会 400",
	"min_p":                "非 OpenAI 标准参数，严格上游会 400",
	"repetition_penalty":   "非 OpenAI 标准参数（重复惩罚），OpenAI 用 frequency_penalty",
	"rep_pen":              "非 OpenAI 标准参数（重复惩罚），OpenAI 用 frequency_penalty",
	"rep_pen_range":        "非 OpenAI 标准参数，严格上游会 400",
	"rep_pen_slope":        "非 OpenAI 标准参数，严格上游会 400",
	"typical_p":            "非 OpenAI 标准参数，严格上游会 400",
	"tfs_p":                "非 OpenAI 标准参数，严格上游会 400",
	"max_context_unlocked": "上下文上限是客户端预算，不是请求参数",
	"openai_max_context":   "上下文上限由客户端/模型决定，不是请求参数",
}

// 客户端行为：确实是某客户端的行为开关，但不属于上游请求体。
var presetClientBehaviorReasons = map[string]string{
	"stream_openai":              "流式开关由网关按请求头决定，不是请求参数",
	"use_sysprompt":              "客户端系统提示词开关；导入时已用于拼装 system_prompt",
	"names_behavior":             "名字注入是客户端提示词拼装行为",
	"send_if_empty":              "酒馆聊天补全行为，不是请求参数",
	"impersonation_prompt":       "酒馆聊天补全行为，不是请求参数",
	"new_chat_prompt":            "酒馆聊天补全行为，不是请求参数",
	"new_group_chat_prompt":      "酒馆聊天补全行为，不是请求参数",
	"new_example_chat_prompt":    "酒馆聊天补全行为，不是请求参数",
	"continue_nudge_prompt":      "酒馆聊天补全行为，不是请求参数",
	"group_nudge_prompt":         "酒馆聊天补全行为，不是请求参数",
	"assistant_prefill":          "客户端预填行为，不是请求参数",
	"assistant_impersonation":    "客户端扮演行为，不是请求参数",
	"continue_prefill":           "客户端续写编排，不是请求参数",
	"continue_postfix":           "客户端续写编排，不是请求参数",
	"squash_system_messages":     "客户端合并 system 消息的行为",
	"bias_preset_selected":       "酒馆预设选择器状态，不是参数",
	"wi_format":                  "提示词模板占位符，由客户端渲染",
	"scenario_format":            "提示词模板占位符，由客户端渲染",
	"personality_format":         "提示词模板占位符，由客户端渲染",
	"wrap_in_quotes":             "客户端提示词引号包装",
	"wrap_in_system_prompt":      "客户端模板开关",
	"media_inlining":             "客户端多模态内联策略，不是请求参数",
	"image_inlining":             "客户端多模态内联策略，不是请求参数",
	"video_inlining":             "客户端多模态内联策略，不是请求参数",
	"inline_image_quality":       "客户端多模态内联策略，不是请求参数",
	"request_images":             "客户端多模态请求开关，不是请求参数",
	"request_image_aspect_ratio": "客户端多模态请求开关，不是请求参数",
	"request_image_resolution":   "客户端多模态请求开关，不是请求参数",
	"function_calling":           "工具调用编排由网关/客户端决定，不是请求参数",
	"tool_call_recurse_limit":    "客户端工具递归上限，不是请求参数",
	"tool_reasoning_mode":        "客户端推理展示开关，不是请求参数",
	"strip_old_tool_calls":       "客户端历史整理行为，不是请求参数",
	"show_thoughts":              "客户端推理展示开关，不是请求参数",
	"reasoning_effort":           "客户端推理强度开关，不是请求参数",
	"claude_fast_mode":           "客户端/渠道能力开关，不是请求参数",
	"extensions":                 "酒馆扩展数据，与本网关无关",
	"sampler_order":              "采样器编排是客户端行为，网关不参与",
	"dynatemp_low":               "动态温度是客户端行为，网关不参与",
	"dynatemp_high":              "动态温度是客户端行为，网关不参与",
	"dynatemp_exponent":          "动态温度是客户端行为，网关不参与",
	"max_context":                "上下文长度由客户端/模型决定，不是请求参数",
	"max_context_unused":         "上下文裁剪是客户端行为，网关不参与",
	"api_url_scale":              "客户端请求缩放开关，与网关无关",
	"reverse_proxy":              "客户端代理配置，网关有自己的代理设置",
	"proxy_password":             "客户端代理配置，网关有自己的代理设置",
}

// 数值键的取值顺序：alias 归一后取第一个非空。用切片保证预览顺序稳定。
//
// 口径（用户实测的三份 Gemini 定向预设）：top_k / top_p / temperature 在 Gemini API
// 里是标准参数，所以保留进 body；max_tokens 取 openai_max_tokens 第一非空。
type presetNumberRule struct {
	Target  string
	Sources []string
}

var presetNumberRules = []presetNumberRule{
	{Target: "temperature", Sources: []string{"temperature", "temp"}},
	{Target: "top_p", Sources: []string{"top_p"}},
	{Target: "top_k", Sources: []string{"top_k"}},
	{Target: "frequency_penalty", Sources: []string{"frequency_penalty", "freq_pen"}},
	{Target: "presence_penalty", Sources: []string{"presence_penalty", "pres_pen"}},
	{Target: "max_tokens", Sources: []string{"openai_max_tokens", "max_tokens", "max_length"}},
}

// 允许透传的键（OpenAI 兼容上游普遍接受；实测文件里 seed / n 常见）。
var presetPassThroughKeys = []string{"seed", "n", "logit_bias"}

// presetPromptSegment 是拼进 system_prompt 的一段。
type presetPromptSegment struct {
	Identifier string
	Content    string
	System     bool
}

// parseSillyTavernPreset 解析酒馆预设；format 支持 auto（默认）/ chat / text。
//
// includeSystemPrompt=false 时只映射采样参数，不拼装提示词（预览里给用户选的开关）。
// 省略该参数时按 true 处理（方便测试与既有调用）。
// 返回的 Body 可直接作为 `POST /api/presets/save` 的 `body`；
// SystemPrompt 要写进预设的 `system_prompt` 字段（**不放进 body**）。
func parseSillyTavernPreset(raw, format string, includeSystemPrompt ...bool) (presetImportPreview, error) {
	include := true
	if len(includeSystemPrompt) > 0 {
		include = includeSystemPrompt[0]
	}
	out := presetImportPreview{
		Body:        map[string]any{},
		PromptLimit: presetSystemPromptLimit,
		Mapped:      []presetImportField{},
		Dropped:     []presetImportDrop{},
	}

	if strings.TrimSpace(raw) == "" {
		return out, fmt.Errorf("导入内容为空：请选择酒馆导出的预设 JSON 文件")
	}
	if len(raw) > presetImportMaxBytes {
		return out, fmt.Errorf("预设文件过大（%d 字节，上限 %d）", len(raw), presetImportMaxBytes)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return out, fmt.Errorf("不是合法的 JSON 对象：%w", err)
	}
	if root == nil {
		return out, fmt.Errorf("JSON 顶层必须是对象（{...}），不能是数组或 null")
	}

	detected := strings.ToLower(strings.TrimSpace(format))
	if detected != "chat" && detected != "text" {
		detected = detectPresetFormat(root)
	}
	out.Format = detected

	if name, ok := root["name"].(string); ok && strings.TrimSpace(name) != "" {
		out.Name = strings.TrimSpace(name)
	}

	used := map[string]bool{"name": true}

	// 1) 数值参数（含别名归一）
	for _, rule := range presetNumberRules {
		for _, key := range rule.Sources {
			value, present := root[key]
			if !present || value == nil {
				continue
			}
			number, ok := presetNumber(value)
			if !ok {
				out.Dropped = append(out.Dropped, presetImportDrop{
					Key:    key,
					Group:  presetDropGroupUnknown,
					Reason: "值不是数字，无法映射到 " + rule.Target,
				})
				used[key] = true
				break
			}
			out.Body[rule.Target] = number
			out.Mapped = append(out.Mapped, presetImportField{Key: rule.Target, Source: key})
			used[key] = true
			break
		}
		// 没被采用的别名也属于"已识别"，不能落进"未识别的字段"丢弃列表。
		for _, key := range rule.Sources {
			if _, present := root[key]; present {
				used[key] = true
			}
		}
	}

	// 2) stop：字符串或数组都归一成字符串数组
	if rawStop, present := root["stop"]; present && rawStop != nil {
		if stops, ok := presetStopList(rawStop); ok {
			out.Body["stop"] = stops
			out.Mapped = append(out.Mapped, presetImportField{Key: "stop", Source: "stop"})
		} else {
			out.Dropped = append(out.Dropped, presetImportDrop{
				Key:    "stop",
				Group:  presetDropGroupUnknown,
				Reason: "stop 既不是字符串也不是字符串数组",
			})
		}
		used["stop"] = true
	}

	// 3) 透传（seed / n / logit_bias）
	for _, key := range presetPassThroughKeys {
		value, present := root[key]
		if !present || value == nil {
			continue
		}
		switch key {
		case "logit_bias":
			if obj, ok := value.(map[string]any); ok {
				out.Body[key] = obj
				out.Mapped = append(out.Mapped, presetImportField{Key: key, Source: key})
			} else {
				out.Dropped = append(out.Dropped, presetImportDrop{
					Key: key, Group: presetDropGroupUnknown, Reason: "logit_bias 必须是对象",
				})
			}
		default:
			if number, ok := presetInt(value); ok {
				out.Body[key] = number
				out.Mapped = append(out.Mapped, presetImportField{Key: key, Source: key})
			} else {
				out.Dropped = append(out.Dropped, presetImportDrop{
					Key: key, Group: presetDropGroupUnknown, Reason: "值不是整数",
				})
			}
		}
		used[key] = true
	}

	// 4) prompts + prompt_order → system_prompt（这是酒馆人设/破甲的实际载体）
	segments := collectPresetPromptSegments(root)
	joined := make([]string, 0, len(segments))
	for _, segment := range segments {
		joined = append(joined, segment.Content)
	}
	assembled := strings.Join(joined, presetSegmentSeparator)
	out.PromptSegments = len(selectedSegmentContents(segments))
	out.PromptChars = utf8.RuneCountInString(assembled)
	out.PromptBytes = len(assembled)
	out.PromptOverflow = out.PromptBytes > presetSystemPromptLimit
	out.PromptIncluded = include && assembled != ""
	if out.PromptIncluded {
		out.SystemPrompt = assembled
	}
	used["prompts"] = true
	used["prompt_order"] = true

	// 5) 其余字段分类丢弃（按 key 排序保证预览稳定）
	remaining := make([]string, 0, len(root))
	for key := range root {
		if used[key] {
			continue
		}
		remaining = append(remaining, key)
	}
	sortStrings(remaining)
	for _, key := range remaining {
		lower := strings.ToLower(strings.TrimSpace(key))
		if presetGatewayOwnedKeys[lower] {
			out.Dropped = append(out.Dropped, presetImportDrop{
				Key:    key,
				Group:  presetDropGroupGateway,
				Reason: "网关自管字段（模型/消息/流/工具由网关填充），预设写它会被拒绝",
			})
			continue
		}
		if reason, ok := presetUpstreamUnsupportedReasons[lower]; ok {
			out.Dropped = append(out.Dropped, presetImportDrop{
				Key: key, Group: presetDropGroupUpstream, Reason: reason,
			})
			continue
		}
		if reason, ok := presetClientBehaviorReasons[lower]; ok {
			out.Dropped = append(out.Dropped, presetImportDrop{
				Key: key, Group: presetDropGroupClient, Reason: reason,
			})
			continue
		}
		out.Dropped = append(out.Dropped, presetImportDrop{
			Key:    key,
			Group:  presetDropGroupUnknown,
			Reason: "未识别的字段，导入器不会写入",
		})
	}

	if err := validatePresetBody(out.Body); err != nil {
		return out, err
	}
	return out, nil
}

// collectPresetPromptSegments 按 prompt_order 拼装提示词段落。
//
// 规则（对应用户三份实测文件）：
//  1. 顺序以 `prompt_order[].order` 为准（优先 character_id=100000/100001，否则取第一条）；
//  2. 跳过 `enabled=false`、`marker=true`、content 为空白的项；
//  3. `system_prompt:true` 的段落优先（两组内部各自保持原顺序）；
//  4. `use_sysprompt=true` 且存在 `main` 段时，把 main 提到最前（去重）。
func collectPresetPromptSegments(root map[string]any) []presetPromptSegment {
	byID := map[string]map[string]any{}
	if prompts, ok := root["prompts"].([]any); ok {
		for _, item := range prompts {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id, _ := obj["identifier"].(string)
			if id == "" {
				continue
			}
			if _, exists := byID[id]; !exists {
				byID[id] = obj
			}
		}
	}

	var selected []presetPromptSegment
	for _, item := range presetOrderList(root) {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if enabled, present := entry["enabled"].(bool); present && !enabled {
			continue
		}
		id, _ := entry["identifier"].(string)
		obj := byID[id]
		if obj == nil {
			continue
		}
		if marker, _ := obj["marker"].(bool); marker {
			continue
		}
		content, _ := obj["content"].(string)
		if strings.TrimSpace(content) == "" {
			continue
		}
		isSystem, _ := obj["system_prompt"].(bool)
		selected = append(selected, presetPromptSegment{
			Identifier: id,
			Content:    strings.TrimSpace(content),
			System:     isSystem,
		})
	}

	// system_prompt:true 优先，组内保持原顺序
	systemFirst := make([]presetPromptSegment, 0, len(selected))
	rest := make([]presetPromptSegment, 0, len(selected))
	for _, segment := range selected {
		if segment.System {
			systemFirst = append(systemFirst, segment)
		} else {
			rest = append(rest, segment)
		}
	}
	ordered := append(systemFirst, rest...)

	// use_sysprompt 且存在 main：main 提到最前
	if use, _ := root["use_sysprompt"].(bool); use {
		if main := byID["main"]; main != nil {
			if content, ok := main["content"].(string); ok && strings.TrimSpace(content) != "" {
				head := presetPromptSegment{Identifier: "main", Content: strings.TrimSpace(content), System: true}
				deduped := make([]presetPromptSegment, 0, len(ordered)+1)
				deduped = append(deduped, head)
				for _, segment := range ordered {
					if segment.Identifier == "main" {
						continue
					}
					deduped = append(deduped, segment)
				}
				return deduped
			}
		}
	}
	return ordered
}

// selectedSegmentContents 返回**实际会拼进去**的段落内容（main 去重后的真实结果）。
func selectedSegmentContents(segments []presetPromptSegment) []string {
	contents := make([]string, 0, len(segments))
	for _, segment := range segments {
		if strings.TrimSpace(segment.Content) == "" {
			continue
		}
		contents = append(contents, segment.Content)
	}
	return contents
}

// presetOrderList 取 prompt_order 里的 order 列表。
func presetOrderList(root map[string]any) []any {
	orders, ok := root["prompt_order"].([]any)
	if !ok {
		return nil
	}
	var fallback []any
	for _, item := range orders {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		list, _ := obj["order"].([]any)
		if fallback == nil {
			fallback = list
		}
		if id, ok := presetInt(obj["character_id"]); ok && (id == 100000 || id == 100001) {
			return list
		}
	}
	return fallback
}

// detectPresetFormat 用"只有某一种格式才有的字段"判型。
//
// 先看 chat 标记：Gemini 定向的聊天预设同样带 top_k/max_length 这类 text 标记，
// 若先判 text 会把它们误判成 Text Generation（真实文件回归过）。
func detectPresetFormat(root map[string]any) string {
	chatMarkers := []string{
		"temperature", "openai_max_tokens", "chat_completion_source",
		"stream_openai", "frequency_penalty", "presence_penalty", "prompts", "prompt_order",
	}
	for _, key := range chatMarkers {
		if _, ok := root[key]; ok {
			return "chat"
		}
	}
	textMarkers := []string{"temp", "max_length", "top_k", "rep_pen", "sampler_order", "min_p", "typical_p"}
	for _, key := range textMarkers {
		if _, ok := root[key]; ok {
			return "text"
		}
	}
	return "unknown"
}

// presetNumber 接受数字、json.Number 与"数字字符串"。
func presetNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

func presetInt(value any) (int64, bool) {
	switch v := value.(type) {
	case float64:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// presetStopList 把 stop 归一成 []string；空字符串/空数组视为"没给"。
//
// 字符串 stop **不 trim 内容**：酒馆里 "\nUser:" 这种换行前缀有语义，
// 只拿 TrimSpace 判空。
func presetStopList(value any) ([]string, bool) {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, false
		}
		return []string{v}, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	}
	return nil, false
}

// sortStrings 是最小插入排序：只用于把预览里的剩余字段排稳（字段数很少）。
func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		value := list[i]
		j := i - 1
		for j >= 0 && list[j] > value {
			list[j+1] = list[j]
			j--
		}
		list[j+1] = value
	}
}

// handlePresetImportPreview POST /api/presets/import/preview —— 只解析，不写任何状态。
//
// 入参：`{format?: "auto"|"chat"|"text", json: "<预设文件原文>", include_system_prompt?: true}`
// 出参见 presetImportPreview；写入仍走既有 `POST /api/presets/save`。
func (g *Gateway) handlePresetImportPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Format              string `json:"format"`
		JSON                string `json:"json"`
		IncludeSystemPrompt *bool  `json:"include_system_prompt"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	include := true
	if in.IncludeSystemPrompt != nil {
		include = *in.IncludeSystemPrompt
	}
	preview, err := parseSillyTavernPreset(in.JSON, in.Format, include)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, preview)
}
