package clients

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"aether-cockpit/internal/core/jsoncutil"
)

// 本文件是"保注释写盘"的共享入口。
//
// 背景：12 个客户端的 JSON 写盘长期走「Unmarshal 成 map → MarshalIndent 吐回」，
// 代价是键序被重排、缩进被规范化，含注释的文件更是直接拒绝改写。而
// jsoncutil.SpliceMember 早就能"只替换顶层成员、其余原样"，只是之前只被
// Library 用。这里把它接进客户端的写盘路径。
//
// 设计约束（为何不一刀切）：
//   - SpliceMember 只能替换**顶层**成员，嵌套结构仍会被重新编码。对顶层就是
//     整块配置的客户端（Claude Code 的 env、opencode 的 provider）已经足够；
//     对需要在深层改单个键的，保真收益小于风险，继续走整写。
//   - 任何一步失败都返回 ok=false，由调用方回退到原来的整写路径。
//     保真是优化，不是正确性前提——不能因为解析不了注释就让用户接不进。

// spliceMembers 把若干顶层成员写进 JSONC 文档，保留其余内容与注释。
//
// 返回值 ok=false 表示"这份文档不适合走保真路径"（空文件、不是 JSONC 对象、
// 某个成员改写后不合法等），调用方应回退整写。
func spliceMembers(raw []byte, members map[string]any) ([]byte, bool) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, false
	}
	// 先确认原文是可解析的 JSONC 对象：不是对象（例如顶层是数组）就别碰。
	normalized, err := jsoncutil.Parse(raw)
	if err != nil {
		return nil, false
	}
	var probe map[string]any
	if err := json.Unmarshal(normalized, &probe); err != nil {
		return nil, false
	}

	// 固定顺序，让同一份输入总是得到同一份输出（便于 diff 与测试）。
	keys := make([]string, 0, len(members))
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := raw
	for _, k := range keys {
		next, err := jsoncutil.SpliceMember(out, k, members[k])
		if err != nil {
			return nil, false
		}
		out = next
	}
	return out, true
}

// topLevelObject 取出原文某个顶层成员的 map 副本（JSONC 感知）。
// 取不到时返回空 map：调用方按"从零构建"处理。
func topLevelObject(raw []byte, key string) map[string]any {
	normalized, err := jsoncutil.Parse(raw)
	if err != nil {
		return map[string]any{}
	}
	var root map[string]any
	if err := json.Unmarshal(normalized, &root); err != nil {
		return map[string]any{}
	}
	obj, _ := root[key].(map[string]any)
	if obj == nil {
		return map[string]any{}
	}
	// 复制一份，避免调用方改动影响 root。
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		out[k] = v
	}
	return out
}

// parseJSONCObject 解析一份可能带注释的 JSON 对象。
//
// 各 applier 原来一律用 json.Unmarshal，于是"文件里有注释"就变成硬失败。
// 统一走这里之后：注释被剥掉再解析，写回时再由 finishJSON 保留。
func parseJSONCObject(raw []byte, label string) (map[string]any, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return root, nil
	}
	normalized, err := jsoncutil.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("现有 %s 不是合法 JSON/JSONC，拒绝改写: %w", label, err)
	}
	if err := json.Unmarshal(normalized, &root); err != nil {
		return nil, fmt.Errorf("现有 %s 不是 JSON 对象，拒绝改写: %w", label, err)
	}
	return root, nil
}

// finishJSON 收尾：只把"与原文不同"的顶层成员写回去，其余文本原样保留。
//
// 这是各 JSON applier 的统一出口：
//  1. 解析原文（JSONC 感知）拿到旧值；
//  2. 与调用方构造的新 root 逐顶层键比对；
//  3. 只 splice 变化的键——注释、缩进、键序、用户其它配置全部保住；
//  4. splice 不可用（原文不是对象、嵌套结构异常）时退回整文件重写。
//
// 为什么逐键比对而不是让 applier 报"改了哪些键"：applier 有十来个，
// 逐个维护"改了哪些键"迟早写漏；这里用 reflect.DeepEqual 做一次兜底，
// 加了新逻辑也不会忘了登记。
func finishJSON(raw []byte, root map[string]any) ([]byte, error) {
	old := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if normalized, err := jsoncutil.Parse(raw); err == nil {
			_ = json.Unmarshal(normalized, &old)
		}
	}
	changed := make(map[string]any, 4)
	for k, v := range root {
		if !reflect.DeepEqual(old[k], v) {
			changed[k] = v
		}
	}
	// 删除也必须体现出来：applier 里 delete(root, ...) 表达的意图是
	// "把这个键从文件里去掉"，只 splice 新值会让旧键留在原地。
	var removed []string
	for k := range old {
		if _, ok := root[k]; !ok {
			removed = append(removed, k)
		}
	}
	sort.Strings(removed)

	if len(changed) > 0 || len(removed) > 0 {
		out := raw
		ok := true
		if len(changed) > 0 {
			out, ok = spliceMembers(out, changed)
		}
		if ok {
			for _, k := range removed {
				if next, err := jsoncutil.RemoveMember(out, k); err == nil {
					out = next
				} else {
					ok = false
					break
				}
			}
		}
		if ok {
			return out, nil
		}
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
