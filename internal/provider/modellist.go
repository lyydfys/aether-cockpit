package provider

import (
	"encoding/json"
	"errors"
	"strings"
)

// ParseModelListIDs 解析常见的上游模型列表响应：
//
//	{"data":[{"id":"gpt-5"},{"id":"glm-5"}]}   OpenAI 兼容
//	{"models":[{"name":"llama3"}]}             Ollama / vLLM 一类
//	["m1",{"id":"m2"}]                         裸数组
//
// 返回去重后的模型 ID（保留上游顺序）。
func ParseModelListIDs(raw []byte) ([]string, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	out := make([]string, 0, 32)
	seen := make(map[string]bool, 32)
	add := func(v any) {
		id := ""
		switch x := v.(type) {
		case string:
			id = strings.TrimSpace(x)
		case map[string]any:
			for _, k := range []string{"id", "model", "name"} {
				if s, ok := x[k].(string); ok && strings.TrimSpace(s) != "" {
					id = strings.TrimSpace(s)
					break
				}
			}
		}
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	switch v := root.(type) {
	case []any:
		for _, x := range v {
			add(x)
		}
	case map[string]any:
		found := false
		for _, key := range []string{"data", "models"} {
			if list, ok := v[key].([]any); ok {
				found = true
				for _, x := range list {
					add(x)
				}
			}
		}
		if !found {
			return nil, errors.New("响应里没有 data/models 数组")
		}
	default:
		return nil, errors.New("无法识别的模型列表格式")
	}
	if len(out) == 0 {
		return nil, errors.New("模型列表为空")
	}
	const limit = 1000
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
