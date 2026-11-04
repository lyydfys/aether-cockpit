// Package ir 定义统一中间表示层（Unified IR v3）。
//
// 设计约束：本包只依赖标准库，且不引用任何 provider 实现。
// 所有下游协议（OpenAI Chat / OpenAI Responses / Anthropic Messages）都在
// 入站层被归一化成这里的结构，所有上游渠道插件也只认这里的结构。
package ir

import (
	"encoding/json"
	"strings"
)

// ItemType 是 UnifiedItem 的类型判别式。
type ItemType string

const (
	// ItemTypeMessage 普通对话消息（user / assistant），可携带多模态附件。
	ItemTypeMessage ItemType = "message"
	// ItemTypeReasoning 思考过程。对齐 Responses 的 reasoning item 与 Claude 的 thinking block。
	ItemTypeReasoning ItemType = "reasoning"
	// ItemTypeFunctionCall 模型发起的一次工具调用。
	ItemTypeFunctionCall ItemType = "function_call"
	// ItemTypeFunctionCallOutput 客户端回传的工具执行结果。
	ItemTypeFunctionCallOutput ItemType = "function_call_output"
	// ItemTypeCompaction 是 Codex 压缩后回传的**封装载荷**。
	//
	// 它是上游签发的密文（encrypted_content），只能用原样送回上游；
	// 我们自己既解不开也不该替换。丢弃它会让长会话在压缩后直接被上游
	// 以 invalid_encrypted_content 拒绝——这正是"上下文一压缩就续不上"的根因。
	ItemTypeCompaction ItemType = "compaction"
)

// MediaAttachment 多模态附件。URL 与 DataB64 二选一。
type MediaAttachment struct {
	Kind     string `json:"kind"` // "image" | "file"
	MIME     string `json:"mime,omitempty"`
	URL      string `json:"url,omitempty"`
	DataB64  string `json:"data_b64,omitempty"`
	FileName string `json:"file_name,omitempty"`
}

// UnifiedItem 是 IR 的最小上下文单位。
//
// 相比粗糙的 Message，它额外承载两类"不可重建"的元数据：
//   - ThoughtSignature：Gemini 2.5+/Claude 思考块的加密签名，丢失会被上游拒绝（400）。
//   - EncryptedContent：OpenAI o 系列 / GPT-5 的加密推理状态。
//
// 这两个字段是跨协议桥接时唯一无法从明文重建的信息，必须原样透传。
type UnifiedItem struct {
	ID   string   `json:"id,omitempty"`
	Type ItemType `json:"type"`
	Role string   `json:"role,omitempty"`
	Text string   `json:"text,omitempty"`
	// RefID 指向历史中的某个 item（Responses 的 item_reference）。
	// 解析阶段只记录引用，真正的展开由 StateSuture 中间件从状态存储中完成。
	RefID            string            `json:"ref_id,omitempty"`
	ThoughtSignature string            `json:"thought_signature,omitempty"`
	EncryptedContent string            `json:"encrypted_content,omitempty"`
	CallID           string            `json:"call_id,omitempty"`
	Name             string            `json:"name,omitempty"`
	Arguments        string            `json:"arguments,omitempty"`
	Output           string            `json:"output,omitempty"`
	Attachments      []MediaAttachment `json:"attachments,omitempty"`
	// CacheControl 是 Anthropic prompt caching 的断点标记（原样 JSON，
	// 如 {"type":"ephemeral"}）。只对 Anthropic 协议上游生效，其它渠道忽略。
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

// IsUserTurn 判断该 item 是否由用户产生（用于会话指纹与粘性路由）。
func (it UnifiedItem) IsUserTurn() bool {
	return it.Type == ItemTypeMessage && it.Role == "user"
}

// IsBlank 判断 item 是否没有实际内容，可在裁剪历史时丢弃。
func (it UnifiedItem) IsBlank() bool {
	if it.Type == ItemTypeFunctionCall || it.Type == ItemTypeFunctionCallOutput {
		return false
	}
	return strings.TrimSpace(it.Text) == "" && len(it.Attachments) == 0
}

// SimpleMessage 是退化为传统 [{role, content}] 形态后的消息。
// 供那些只支持纯文本对话的轻量渠道使用。
type SimpleMessage struct {
	Role       string
	Content    string
	ToolCalls  []SimpleToolCall
	ToolCallID string
}

// SimpleToolCall 退化形态下的工具调用描述。
type SimpleToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Clone 返回 item 的浅拷贝（Attachments 切片会复制一份，避免跨请求共享底层数组）。
func (it UnifiedItem) Clone() UnifiedItem {
	cp := it
	if len(it.Attachments) > 0 {
		cp.Attachments = make([]MediaAttachment, len(it.Attachments))
		copy(cp.Attachments, it.Attachments)
	}
	return cp
}
