package inbound

import (
	"encoding/json"
	"strings"

	"aether-cockpit/internal/core/ir"
)

// 下游协议对 tool_choice / response_format 的表达各不相同，
// 这里统一归一到 IR，交给各上游按自己的协议再展开。

func decodeToolChoice(raw json.RawMessage) *ir.ToolChoice {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		mode := strings.ToLower(strings.TrimSpace(s))
		if mode == "" {
			return nil
		}
		return &ir.ToolChoice{Mode: mode}
	}
	var obj struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	typ := strings.ToLower(strings.TrimSpace(obj.Type))
	name := obj.Function.Name
	if name == "" {
		name = obj.Name
	}
	if typ == "function" && name != "" {
		return &ir.ToolChoice{Mode: "function", Name: name}
	}
	if typ != "" {
		return &ir.ToolChoice{Mode: typ}
	}
	return nil
}

// decodeResponseFormat 同时兼容两种线缆形态：
//   - Chat Completions: {"type":"json_schema","json_schema":{"name","schema","strict"}}
//   - Responses:        {"type":"json_schema","name","schema","strict"}
func decodeResponseFormat(raw json.RawMessage) *ir.ResponseFormat {
	if len(raw) == 0 {
		return nil
	}
	var obj struct {
		Type       string          `json:"type"`
		Name       string          `json:"name"`
		Schema     json.RawMessage `json:"schema"`
		Strict     *bool           `json:"strict"`
		JSONSchema struct {
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"schema"`
			Strict *bool           `json:"strict"`
		} `json:"json_schema"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	typ := strings.ToLower(strings.TrimSpace(obj.Type))
	if typ == "" {
		return nil
	}
	out := &ir.ResponseFormat{Type: typ}
	if obj.JSONSchema.Name != "" || len(obj.JSONSchema.Schema) > 0 || obj.JSONSchema.Strict != nil {
		out.Name = obj.JSONSchema.Name
		out.Schema = obj.JSONSchema.Schema
		out.Strict = obj.JSONSchema.Strict
	} else {
		out.Name = obj.Name
		out.Schema = obj.Schema
		out.Strict = obj.Strict
	}
	return out
}
