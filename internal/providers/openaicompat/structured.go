package openaicompat

import (
	"encoding/json"

	"aether-cockpit/internal/core/ir"
)

// 把 IR 的工具选择与结构化输出约束展开成各上游的线缆形态。

func openAIToolChoice(tc *ir.ToolChoice) any {
	if tc == nil {
		return nil
	}
	if tc.Mode == "function" {
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tc.Name},
		}
	}
	return tc.Mode
}

func openAIResponseFormat(rf *ir.ResponseFormat) any {
	if rf == nil {
		return nil
	}
	switch rf.Type {
	case "json_schema":
		item := map[string]any{"type": "json_schema"}
		spec := map[string]any{}
		if rf.Name != "" {
			spec["name"] = rf.Name
		}
		if len(rf.Schema) > 0 {
			var schema any
			if json.Unmarshal(rf.Schema, &schema) == nil {
				spec["schema"] = schema
			}
		}
		if rf.Strict != nil {
			spec["strict"] = *rf.Strict
		}
		item["json_schema"] = spec
		return item
	case "json_object", "text":
		return map[string]any{"type": rf.Type}
	}
	return nil
}

// responsesToolChoice Responses API 的 function 选择是扁平结构。
func responsesToolChoice(tc *ir.ToolChoice) any {
	if tc == nil {
		return nil
	}
	if tc.Mode == "function" {
		return map[string]any{"type": "function", "name": tc.Name}
	}
	return tc.Mode
}

// responsesTextFormat Responses API 把结构化输出放在 text.format。
func responsesTextFormat(rf *ir.ResponseFormat) any {
	if rf == nil {
		return nil
	}
	switch rf.Type {
	case "json_schema":
		format := map[string]any{"type": "json_schema"}
		if rf.Name != "" {
			format["name"] = rf.Name
		}
		if len(rf.Schema) > 0 {
			var schema any
			if json.Unmarshal(rf.Schema, &schema) == nil {
				format["schema"] = schema
			}
		}
		if rf.Strict != nil {
			format["strict"] = *rf.Strict
		}
		return format
	case "json_object", "text":
		return map[string]any{"type": rf.Type}
	}
	return nil
}
