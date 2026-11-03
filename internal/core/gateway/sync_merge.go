package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// 云同步的三路合并。
//
// 背景：原来的推送是"整包覆盖"——两台机器同时改设置，后推的把先推的整份
// 盖掉，配置静默消失。这是数据丢失级问题，比任何功能缺失都严重。
//
// 这里实现的是标准的 diff3 思路（对齐 aether 的 sync 三段合并）：
//
//	base   = 上次成功同步时的内容（本机留底）
//	local  = 本机当前内容
//	remote = 远端当前内容
//
// 对每一个路径：
//   - local == remote            → 双方一致，取任意一边
//   - local == base              → 只有远端改过，取远端
//   - remote == base             → 只有本机改过，取本机
//   - 两边都改了且不同           → 冲突：保留本机值，把远端值记进冲突档案
//
// 对象逐键递归；数组按"身份键"（id/name/account_id/key）做并集；标量冲突
// 一律保留本机——用户正在操作的这台机器的意图优先，远端值进档案可回溯。

// mergeConflict 是一条被舍弃的远端值（写进冲突档案，便于人工回溯）。
type mergeConflict struct {
	Path   string `json:"path"`
	Local  any    `json:"local"`
	Remote any    `json:"remote"`
}

// mergeResult 是三路合并的结果。
type mergeResult struct {
	Value     any             `json:"value"`
	Conflicts []mergeConflict `json:"conflicts,omitempty"`
	// Changed 表示合并结果与本机不同（需要写回本机 / 上传）。
	Changed bool `json:"changed"`
}

// identityKeys 是数组元素的"身份键"，用于按身份做并集而不是整段替换。
var identityKeys = []string{"id", "name", "account_id", "key", "provider_id", "model"}

// mergeThreeWay 合并 base/local/remote 三份 JSON 值。
func mergeThreeWay(base, local, remote any) mergeResult {
	res := mergeResult{}
	res.Value, res.Conflicts = mergeNode("", base, local, remote)
	res.Changed = !reflect.DeepEqual(res.Value, local)
	return res
}

// mergeNode 是递归合并的核心。
func mergeNode(path string, base, local, remote any) (any, []mergeConflict) {
	// 双方一致：不需要判断谁改过。
	if reflect.DeepEqual(local, remote) {
		return local, nil
	}
	// 只有远端改过。
	if reflect.DeepEqual(local, base) {
		return remote, nil
	}
	// 只有本机改过。
	if reflect.DeepEqual(remote, base) {
		return local, nil
	}

	// 两边都改了：对象逐键递归。
	lm, lok := local.(map[string]any)
	rm, rok := remote.(map[string]any)
	if lok && rok {
		out := make(map[string]any, len(lm)+len(rm))
		bm, _ := base.(map[string]any)
		var conflicts []mergeConflict
		keys := map[string]bool{}
		for k := range lm {
			keys[k] = true
		}
		for k := range rm {
			keys[k] = true
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			var bv any
			if bm != nil {
				bv = bm[k]
			}
			v, cs := mergeNode(joinPath(path, k), bv, lm[k], rm[k])
			if v != nil || hasKey(lm, k) || hasKey(rm, k) {
				out[k] = v
			}
			conflicts = append(conflicts, cs...)
		}
		return out, conflicts
	}

	// 两边都改了数组：按身份键并集；没有身份键就整段取本机并记冲突。
	if la, ok := local.([]any); ok {
		if ra, ok2 := remote.([]any); ok2 {
			if merged, ok3 := mergeSlice(la, ra); ok3 {
				return merged, nil
			}
			return local, []mergeConflict{{Path: path, Local: local, Remote: remote}}
		}
	}

	// 标量或类型不同：保留本机，远端值进档案。
	return local, []mergeConflict{{Path: path, Local: local, Remote: remote}}
}

// mergeSlice 按身份键把两份数组合并成并集。
//
// 合并规则：以本机顺序为主；本机有、远端也有的同身份项按 mergeNode 合并；
// 只有远端有的项追加在后面（这样"另一台机器新增的条目"不会丢）。
// 任一侧缺少身份键时返回 ok=false，让调用方退回整段取本机。
func mergeSlice(local, remote []any) ([]any, bool) {
	key := sliceIdentity(local)
	if key == "" || sliceIdentity(remote) != key {
		return nil, false
	}
	out := make([]any, 0, len(local)+len(remote))
	seen := make(map[string]bool, len(local))
	for _, item := range local {
		id := sliceItemID(item, key)
		if id == "" {
			return nil, false
		}
		if r := findSliceItem(remote, key, id); r != nil {
			merged, _ := mergeNode("", item, item, r)
			out = append(out, merged)
		} else {
			out = append(out, item)
		}
		seen[id] = true
	}
	for _, item := range remote {
		id := sliceItemID(item, key)
		if id == "" {
			return nil, false
		}
		if !seen[id] {
			out = append(out, item)
		}
	}
	return out, true
}

// sliceIdentity 返回这组元素都能用来识别身份的键（没有则空串）。
func sliceIdentity(list []any) string {
	if len(list) == 0 {
		return ""
	}
	for _, candidate := range identityKeys {
		ok := true
		for _, item := range list {
			if sliceItemID(item, candidate) == "" {
				ok = false
				break
			}
		}
		if ok {
			return candidate
		}
	}
	return ""
}

func sliceItemID(item any, key string) string {
	m, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return ""
}

func findSliceItem(list []any, key, id string) any {
	for _, item := range list {
		if sliceItemID(item, key) == id {
			return item
		}
	}
	return nil
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func hasKey(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

// mergeBundles 对两份备份包做三路合并，返回合并后的包与冲突清单。
//
// base 为空（第一次同步）时退化成"两边都改了就以本机为准"，仍然不会
// 整包覆盖——第一次同步就没有整包覆盖的窗口。
func mergeBundles(baseRaw, localRaw, remoteRaw []byte) ([]byte, []mergeConflict, error) {
	var base, local, remote any
	if len(baseRaw) > 0 {
		if err := json.Unmarshal(baseRaw, &base); err != nil {
			base = nil // 底稿损坏：当作没有底稿，仍然走逐键合并
		}
	}
	if err := json.Unmarshal(localRaw, &local); err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(remoteRaw, &remote); err != nil {
		return nil, nil, err
	}
	res := mergeThreeWay(base, local, remote)
	out, err := json.Marshal(res.Value)
	if err != nil {
		return nil, nil, err
	}
	return out, res.Conflicts, nil
}

// bundleParts 把备份包拆成 part 清单，用于 part 级摘要与增量判断。
//
// 为什么按 part：整包比对永远不相等（exported_at 每次都变），根本没机会
// 命中"远端没变"的快路径；按 part 存摘要才能只传变了的那些。
var bundleParts = []string{"settings", "specs", "accounts_meta", "library", "icons"}

// bundlePartDigest 返回每个 part 的内容摘要（不含 exported_at）。
func bundlePartDigest(raw []byte) map[string]string {
	out := make(map[string]string, len(bundleParts))
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return out
	}
	for _, part := range bundleParts {
		v, ok := root[part]
		if !ok {
			continue
		}
		sum := sha256.Sum256(v)
		out[part] = hex.EncodeToString(sum[:8])
	}
	return out
}

// samePartDigest 判断两份摘要是否完全一致（用于跳过无意义的整包上传）。
func samePartDigest(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// describeConflicts 把冲突清单整理成可读文本（写进同步结果给页面看）。
func describeConflicts(list []mergeConflict) string {
	if len(list) == 0 {
		return ""
	}
	paths := make([]string, 0, len(list))
	for _, c := range list {
		if c.Path == "" {
			paths = append(paths, "(根)")
			continue
		}
		paths = append(paths, c.Path)
	}
	sort.Strings(paths)
	if len(paths) > 6 {
		paths = append(paths[:6], "…")
	}
	return "保留本机值的冲突项：" + strings.Join(paths, "、")
}
