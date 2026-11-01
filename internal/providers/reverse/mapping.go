package reverse

import (
	"encoding/json"
	"strconv"
	"strings"

	"aether-cockpit/internal/core/spec"
)

// ChatConfig 是"协议机制"里可由 spec 描述的部分。
//
// 逆向渠道的差异大多集中在三处：鉴权头、请求字段名、响应字段路径。
// 把这三处抽出来之后，同一套引擎可以覆盖绝大多数 JSON / SSE 变体；
// 真正需要写代码的只剩下非标准的签名算法与二进制分帧（后者已有 wirecodec）。
type ChatConfig struct {
	Auth     AuthMapping     `json:"auth"`
	Request  RequestMapping  `json:"request"`
	Response ResponseMapping `json:"response"`
}

// AuthMapping 描述凭证放在哪个头、用什么前缀。
type AuthMapping struct {
	// Header 默认 Authorization。
	Header string `json:"header,omitempty"`
	// Prefix 默认 "Bearer "；NoPrefix 为 true 时原样写入。
	Prefix string `json:"prefix,omitempty"`
	// NoPrefix 适用于 X-Api-Key 这类直接放 token 的头。
	NoPrefix bool `json:"no_prefix,omitempty"`
	// Credential 指定读取哪个凭证字段；留空按 access_token/token/api_key/cookie 顺序探测。
	Credential string `json:"credential,omitempty"`
	// Cookie 为 true 时把凭证写进 Cookie 头。
	Cookie bool `json:"cookie,omitempty"`
	// None 为 true 表示上游不需要鉴权。
	None bool `json:"none,omitempty"`
}

// RequestMapping 描述请求体字段名。
type RequestMapping struct {
	ModelField    string `json:"model_field,omitempty"`
	MessagesField string `json:"messages_field,omitempty"`
	StreamField   string `json:"stream_field,omitempty"`
	StreamValue   any    `json:"stream_value,omitempty"`
	// SystemField 非空且 SystemInMessages=false 时，Instructions 放到顶层该字段。
	SystemField string `json:"system_field,omitempty"`
	// SystemInMessages 用指针是为了区分"未配置"与"显式 false"。
	SystemInMessages *bool  `json:"system_in_messages,omitempty"`
	ToolsField       string `json:"tools_field,omitempty"`
	// ExtraFields 会合并进请求体，用于上游要求的固定字段（版本号、平台标识等）。
	ExtraFields map[string]any `json:"extra_fields,omitempty"`
}

// ResponseMapping 描述上游流式响应的字段路径。
//
// 路径用点号分隔，数组下标用数字，例如 choices.0.delta.content。
type ResponseMapping struct {
	// Format: sse（默认）| ndjson | json | aws_eventstream
	Format string `json:"format,omitempty"`

	TextPath       string   `json:"text_path,omitempty"`
	ReasoningPaths []string `json:"reasoning_paths,omitempty"`

	ToolCallsPath string `json:"tool_calls_path,omitempty"`
	ToolIDPath    string `json:"tool_id_path,omitempty"`
	ToolNamePath  string `json:"tool_name_path,omitempty"`
	ToolArgsPath  string `json:"tool_args_path,omitempty"`
	ToolIndexPath string `json:"tool_index_path,omitempty"`

	FinishPath string `json:"finish_path,omitempty"`

	UsagePath          string `json:"usage_path,omitempty"`
	UsageInputPath     string `json:"usage_input_path,omitempty"`
	UsageOutputPath    string `json:"usage_output_path,omitempty"`
	UsageTotalPath     string `json:"usage_total_path,omitempty"`
	UsageCachedPath    string `json:"usage_cached_path,omitempty"`
	UsageReasoningPath string `json:"usage_reasoning_path,omitempty"`

	SessionIDPath string `json:"session_id_path,omitempty"`

	ErrorPath        string `json:"error_path,omitempty"`
	ErrorMessagePath string `json:"error_message_path,omitempty"`

	// DoneMarkers 是表示流结束的数据值，默认 ["[DONE]"]。
	DoneMarkers []string `json:"done_markers,omitempty"`
}

// chatConfig 从 spec.Extra 里解析映射，并补齐默认值。
func chatConfig(sp *spec.Spec) ChatConfig {
	var cfg ChatConfig
	if sp != nil && len(sp.Extra) > 0 {
		var extra struct {
			Chat ChatConfig `json:"chat"`
		}
		_ = json.Unmarshal(sp.Extra, &extra)
		cfg = extra.Chat
	}

	// 默认值对齐 OpenAI Chat，这是逆向渠道里最常见的形态。
	if cfg.Auth.Header == "" {
		cfg.Auth.Header = "Authorization"
	}
	if cfg.Request.ModelField == "" {
		cfg.Request.ModelField = "model"
	}
	if cfg.Request.MessagesField == "" {
		cfg.Request.MessagesField = "messages"
	}
	if cfg.Request.StreamField == "" {
		cfg.Request.StreamField = "stream"
	}
	if cfg.Request.StreamValue == nil {
		cfg.Request.StreamValue = true
	}
	if cfg.Request.ToolsField == "" {
		cfg.Request.ToolsField = "tools"
	}
	if cfg.Request.SystemInMessages == nil {
		v := true
		cfg.Request.SystemInMessages = &v
	}

	if cfg.Response.TextPath == "" {
		cfg.Response.TextPath = "choices.0.delta.content"
	}
	if len(cfg.Response.ReasoningPaths) == 0 {
		cfg.Response.ReasoningPaths = []string{
			"choices.0.delta.reasoning_content",
			"choices.0.delta.reasoning",
		}
	}
	if cfg.Response.ToolCallsPath == "" {
		cfg.Response.ToolCallsPath = "choices.0.delta.tool_calls"
	}
	if cfg.Response.ToolIDPath == "" {
		cfg.Response.ToolIDPath = "id"
	}
	if cfg.Response.ToolNamePath == "" {
		cfg.Response.ToolNamePath = "function.name"
	}
	if cfg.Response.ToolArgsPath == "" {
		cfg.Response.ToolArgsPath = "function.arguments"
	}
	if cfg.Response.ToolIndexPath == "" {
		cfg.Response.ToolIndexPath = "index"
	}
	if cfg.Response.FinishPath == "" {
		cfg.Response.FinishPath = "choices.0.finish_reason"
	}
	if cfg.Response.UsagePath == "" {
		cfg.Response.UsagePath = "usage"
	}
	if cfg.Response.UsageInputPath == "" {
		cfg.Response.UsageInputPath = "prompt_tokens"
	}
	if cfg.Response.UsageOutputPath == "" {
		cfg.Response.UsageOutputPath = "completion_tokens"
	}
	if cfg.Response.UsageTotalPath == "" {
		cfg.Response.UsageTotalPath = "total_tokens"
	}
	if len(cfg.Response.DoneMarkers) == 0 {
		cfg.Response.DoneMarkers = []string{"[DONE]"}
	}
	return cfg
}

// --- JSON 路径读取 ---

// lookup 按 "a.b.0.c" 形式读取嵌套值。
func lookup(root any, path string) any {
	if root == nil || path == "" {
		return nil
	}
	cur := root
	for _, seg := range strings.Split(path, ".") {
		if seg == "" {
			continue
		}
		switch v := cur.(type) {
		case map[string]any:
			cur = v[seg]
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(v) {
				return nil
			}
			cur = v[idx]
		default:
			return nil
		}
		if cur == nil {
			return nil
		}
	}
	return cur
}

func lookupString(root any, path string) string {
	switch v := lookup(root, path).(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "true"
		}
		return "false"
	}
	return ""
}

func lookupInt(root any, path string) int {
	switch v := lookup(root, path).(type) {
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func lookupSlice(root any, path string) []any {
	if v, ok := lookup(root, path).([]any); ok {
		return v
	}
	return nil
}

func lookupMap(root any, path string) map[string]any {
	if v, ok := lookup(root, path).(map[string]any); ok {
		return v
	}
	return nil
}

// firstString 返回第一个非空值，减少调用点的判断噪音。
func firstString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
