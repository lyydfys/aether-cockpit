package clients

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"aether-cockpit/internal/core/jsoncutil"
)

// 键级还原（key-level restore）。
//
// 背景：断开（Restore）原来是把整份备份写回。问题是用户可能在接入之后
// 又手改过同一个文件里的**别的**键——整份写回会把这些改动一起抹掉。
// 例如 opencode：我们只往里塞了 provider.aether，用户后来又加了 provider.mine、
// 改了 theme；整份回滚会把 mine 和 theme 一起还原掉。
//
// 做法：Apply 成功后记录"我方改动了哪些**路径**、原值是什么"（路径是点号的，
// 如 provider.aether / theme）。断开时只还原这些路径，其余原样保留；
// 如果用户压根没动过文件，仍走整份备份还原——逐字节回原，最严格。
//
// 粒度取舍：路径只追到"值不再是对象"的那一层。provider.aether 是一个整块，
// 我们替换它时本来也是整块替换，所以按整块还原是自洽的。

type keyState struct {
	Version int `json:"version"`
	// Clients: 客户端 ID -> 文件路径 -> 记录
	Clients map[string]map[string]*fileRecord `json:"clients"`
}

// fileRecord 是一个文件的"我方写入"记录。
type fileRecord struct {
	// Paths 是接入前的路径原值（"null" 表示原本没有该路径）。
	Paths map[string]json.RawMessage `json:"paths"`
	// Written 是上一次 Apply 写出的内容摘要。用来判断"用户之后有没有动过
	// 这个文件"：没动过就走整份备份还原（逐字节一致，最安全）；动过才走
	// 键级还原，避免把用户的改动一起抹掉。
	Written string `json:"written,omitempty"`
}

const keyStateVersion = 1

var (
	keyStateMu   sync.Mutex
	keyStatePath string
	keyStateMemo *keyState
)

// SetKeyStatePath 设置键级还原状态文件的位置（空表示禁用）。
func (m *Manager) SetKeyStatePath(path string) {
	keyStateMu.Lock()
	keyStatePath = path
	keyStateMemo = nil
	keyStateMu.Unlock()
}

func loadKeyStateLocked() *keyState {
	if keyStateMemo != nil {
		return keyStateMemo
	}
	st := &keyState{Version: keyStateVersion, Clients: map[string]map[string]*fileRecord{}}
	if keyStatePath != "" {
		if raw, err := os.ReadFile(keyStatePath); err == nil {
			var got keyState
			if json.Unmarshal(raw, &got) == nil && got.Clients != nil {
				st = &got
			}
		}
	}
	keyStateMemo = st
	return st
}

func saveKeyStateLocked() error {
	if keyStatePath == "" || keyStateMemo == nil {
		return nil
	}
	raw, err := json.MarshalIndent(keyStateMemo, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyStatePath), 0o755); err != nil {
		return err
	}
	tmp := keyStatePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, keyStatePath)
}

// recordManagedKeys 在 Apply 成功后记录"我方改了哪些路径、原值是什么"。
//
// 关键：**同一个文件只记第一次**。重复 Apply（改模型、换地址）时若覆盖记录，
// 记下的就变成"上一次应用之后"的状态——断开时会把上一次的网关配置当成
// 用户原配置还原回去，等于没断开。只有第一次记录才是真正的"接入前"。
func recordManagedKeys(id, path string, before, after []byte) {
	if len(before) == 0 {
		// 文件本不存在：整份都是我们建的，断开时删文件即可（走备份的 -created 路径）。
		return
	}
	beforeObj := topLevelAny(before)
	afterObj := topLevelAny(after)
	if beforeObj == nil || afterObj == nil {
		return
	}
	paths := map[string]json.RawMessage{}
	diffPaths("", beforeObj, afterObj, paths)
	if len(paths) == 0 {
		return
	}

	keyStateMu.Lock()
	defer keyStateMu.Unlock()
	st := loadKeyStateLocked()
	if st.Clients == nil {
		st.Clients = map[string]map[string]*fileRecord{}
	}
	byPath := st.Clients[id]
	if byPath == nil {
		byPath = map[string]*fileRecord{}
		st.Clients[id] = byPath
	}
	rec := byPath[path]
	if rec == nil {
		rec = &fileRecord{Paths: map[string]json.RawMessage{}}
		byPath[path] = rec
	}
	for k, v := range paths {
		if _, ok := rec.Paths[k]; ok {
			continue // 第一次记录优先：那才是真正的"接入前"
		}
		rec.Paths[k] = v
	}
	rec.Written = contentDigest(after)
	_ = saveKeyStateLocked()
}

// diffPaths 递归比对两份 JSON 对象，把"变了的叶子路径"记进 out。
// 值为 null 表示"原本没有这个路径"（断开时要删掉）。
func diffPaths(prefix string, before, after map[string]any, out map[string]json.RawMessage) {
	for k, av := range after {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		bv, had := before[k]
		if had && reflect.DeepEqual(bv, av) {
			continue
		}
		// 两边都是对象 → 继续下钻，尽量把粒度做细。
		am, aok := av.(map[string]any)
		bm, bok := bv.(map[string]any)
		if aok && bok {
			diffPaths(path, bm, am, out)
			continue
		}
		if had {
			raw, err := json.Marshal(bv)
			if err == nil {
				out[path] = raw
			}
		} else {
			out[path] = json.RawMessage("null")
		}
	}
}

// restoreManagedKeys 尝试只还原"我方写过的路径"，保留用户的其它改动。
//
// 返回 handled=true 表示已经处理完（调用方不要再整份写回备份）；
// 任何一种不确定的情况都返回 false，让调用方回退到整份备份。
func restoreManagedKeys(id, path string) bool {
	keyStateMu.Lock()
	st := loadKeyStateLocked()
	rec := st.Clients[id][path]
	keyStateMu.Unlock()
	if rec == nil || len(rec.Paths) == 0 {
		return false
	}

	cur, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if rec.Written != "" && contentDigest(cur) == rec.Written {
		// 用户没动过 → 交给整份备份还原，保证逐字节回到原样。
		return false
	}
	root := topLevelAny(cur)
	if root == nil {
		return false
	}

	// 按顶层祖先分组：每改一个路径，最后只需重写它所属的那个顶层成员。
	touched := map[string]bool{}
	for p, v := range rec.Paths {
		seg := strings.Split(p, ".")
		top := seg[0]
		if strings.TrimSpace(string(v)) == "null" {
			delPath(root, seg)
		} else {
			var val any
			if err := json.Unmarshal(v, &val); err != nil {
				return false
			}
			setPath(root, seg, val)
		}
		touched[top] = true
	}

	// 逐个顶层成员写回（保注释：只替换这些成员，其余文本不动）。
	members := make(map[string]any, len(touched))
	for top := range touched {
		if v, ok := root[top]; ok {
			members[top] = v
		}
	}
	// 有顶层成员需要整体删除的情况：单独处理。
	var toRemove []string
	for top := range touched {
		if _, ok := root[top]; !ok {
			toRemove = append(toRemove, top)
		}
	}
	sort.Strings(toRemove)

	out := cur
	if len(members) > 0 {
		next, ok := spliceMembers(out, members)
		if !ok {
			return false
		}
		out = next
	}
	for _, top := range toRemove {
		next, ok := removeTopLevel(out, top)
		if !ok {
			return false
		}
		out = next
	}
	if err := atomicWrite(path, out); err != nil {
		return false
	}

	// 记录用完即清：下次 Apply 会重新记录。
	keyStateMu.Lock()
	if st.Clients[id] != nil {
		delete(st.Clients[id], path)
		if len(st.Clients[id]) == 0 {
			delete(st.Clients, id)
		}
	}
	_ = saveKeyStateLocked()
	keyStateMu.Unlock()
	return true
}

// setPath 在 root 里按路径设置值，必要时创建中间对象。
func setPath(root map[string]any, seg []string, val any) {
	cur := root
	for i, s := range seg {
		if i == len(seg)-1 {
			cur[s] = val
			return
		}
		next, _ := cur[s].(map[string]any)
		if next == nil {
			next = map[string]any{}
			cur[s] = next
		}
		cur = next
	}
}

// delPath 在 root 里按路径删除值；中间不存在时什么也不做。
func delPath(root map[string]any, seg []string) {
	cur := root
	for i, s := range seg {
		if i == len(seg)-1 {
			delete(cur, s)
			return
		}
		next, _ := cur[s].(map[string]any)
		if next == nil {
			return
		}
		cur = next
	}
}

// contentDigest 是内容摘要，用于判断"用户接入后有没有改过这个文件"。
func contentDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// topLevelAny 把 JSON/JSONC 文档解析成通用对象；失败返回 nil。
func topLevelAny(raw []byte) map[string]any {
	normalized, err := jsoncutil.Parse(raw)
	if err != nil {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(normalized, &obj); err != nil {
		return nil
	}
	return obj
}

// removeTopLevel 删除一个顶层成员，保留其余内容与注释。
func removeTopLevel(raw []byte, key string) ([]byte, bool) {
	out, err := jsoncutil.RemoveMember(raw, key)
	if err != nil {
		return nil, false
	}
	return out, true
}
