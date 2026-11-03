// Package jsoncutil 解析 JSONC，并在改写时只替换指定顶层成员，
// 保留文件里的注释与其他成员（成员内部的注释会随该成员重写）。
package jsoncutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/jsonc"
)

// Parse 把 JSONC 转成标准 JSON；解析失败返回错误。
func Parse(raw []byte) ([]byte, error) {
	out := jsonc.ToJSON(raw)
	if !json.Valid(out) {
		return nil, errors.New("JSONC 解析失败")
	}
	return out, nil
}

// SpliceMember 把顶层成员 key 的值替换为 value；不存在时插入。
// 除该成员自身外，原始文本（含注释）保持不变。
func SpliceMember(raw []byte, key string, value any) ([]byte, error) {
	bom := []byte{}
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		bom, raw = raw[:3], raw[3:]
	}
	encoded, err := json.MarshalIndent(value, "  ", "  ")
	if err != nil {
		return nil, err
	}
	replacement := quote(key) + ": " + string(encoded)

	start, end, insertAt, found, closeBrace, err := findMember(raw, key)
	if err != nil {
		return nil, err
	}
	var out []byte
	if found {
		out = append(append(append([]byte{}, raw[:start]...), replacement...), raw[end:]...)
	} else {
		if closeBrace < 0 {
			return nil, errors.New("JSONC 顶层不是对象，拒绝改写")
		}
		if insertAt > 0 {
			out = append(append(append(append([]byte{}, raw[:insertAt]...), []byte(",\n  ")...), replacement...), raw[insertAt:]...)
		} else {
			brace := skipSpace(raw, 0)
			at := skipSpace(raw, brace+1)
			var b bytes.Buffer
			b.Write(raw[:at])
			b.WriteString("\n  ")
			b.WriteString(replacement)
			b.WriteString("\n")
			b.Write(raw[at:])
			out = b.Bytes()
		}
	}
	normalized, err := Parse(out)
	if err != nil {
		return nil, fmt.Errorf("改写后不是合法 JSONC: %w", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(normalized, &probe); err != nil {
		return nil, err
	}
	return append(bom, out...), nil
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// RemoveMember 删除顶层成员 key；其余文本（含注释、缩进、键序）保持不变。
// 不存在时原样返回。删完必须仍是合法 JSONC，否则报错并不返回半成品。
//
// 删除比替换麻烦的地方在于逗号：删中间成员要连带吞掉它后面的逗号，
// 删最后一个成员要吞掉它前面的逗号，否则会留下 `{"a":1,}` 这种非法形态。
func RemoveMember(raw []byte, key string) ([]byte, error) {
	bom := []byte{}
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		bom, raw = raw[:3], raw[3:]
	}
	start, end, _, found, _, err := findMember(raw, key)
	if err != nil {
		return nil, err
	}
	if !found {
		return append(bom, raw...), nil
	}

	// 优先吞掉后面的逗号与空白（中间的成员）。
	cutEnd := end
	i := skipSpace(raw, cutEnd)
	if i < len(raw) && raw[i] == ',' {
		i++
		// 连带吃掉逗号后的空格与换行，但不吃注释（注释可能属于下一个成员）。
		for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == '\r' || raw[i] == '\n') {
			i++
		}
		out := append(append([]byte{}, raw[:start]...), raw[i:]...)
		if !json.Valid(mustJSON(out)) {
			return nil, errors.New("JSONC 删除成员后不是合法 JSON")
		}
		return append(bom, out...), nil
	}

	// 没有后随逗号：说明是最后一个成员，改吞前面的逗号。
	cutStart := start
	j := cutStart - 1
	for j >= 0 && (raw[j] == ' ' || raw[j] == '\t' || raw[j] == '\r' || raw[j] == '\n') {
		j--
	}
	if j >= 0 && raw[j] == ',' {
		// 同时把上一行尾的换行与缩进一起删掉，避免留下空行。
		k := j
		for k > 0 && (raw[k-1] == ' ' || raw[k-1] == '\t' || raw[k-1] == '\r' || raw[k-1] == '\n') {
			k--
		}
		cutStart = k
	}
	out := append(append([]byte{}, raw[:cutStart]...), raw[end:]...)
	if !json.Valid(mustJSON(out)) {
		return nil, errors.New("JSONC 删除成员后不是合法 JSON")
	}
	return append(bom, out...), nil
}

// mustJSON 把 JSONC 文本转成标准 JSON，供 json.Valid 校验。
// 转换失败返回一段非法内容，让上层校验自然失败。
func mustJSON(raw []byte) []byte {
	out := jsonc.ToJSON(raw)
	if out == nil {
		return []byte("{")
	}
	return out
}

// findMember 返回顶层 key 的成员区间 [start,end)、最后一个成员的结束位置
// （insertAt，用于无该键时插入）与顶层对象的右花括号位置。
func findMember(raw []byte, key string) (start, end, insertAt int, found bool, closeBrace int, err error) {
	i := skipSpace(raw, 0)
	if i >= len(raw) || raw[i] != '{' {
		return 0, 0, 0, false, -1, errors.New("JSONC 顶层不是对象")
	}
	i++
	insertAt = 0
	for {
		i = skipSpace(raw, i)
		if i >= len(raw) {
			return 0, 0, insertAt, false, -1, errors.New("JSONC 对象未闭合")
		}
		if raw[i] == '}' {
			return 0, 0, insertAt, false, i, nil
		}
		if raw[i] != '"' {
			return 0, 0, insertAt, false, -1, fmt.Errorf("JSONC 键位置异常: %q", raw[i])
		}
		keyStart := i
		keyEnd, err := scanString(raw, i)
		if err != nil {
			return 0, 0, insertAt, false, -1, err
		}
		var k string
		if err := json.Unmarshal(raw[keyStart:keyEnd], &k); err != nil {
			return 0, 0, insertAt, false, -1, err
		}
		i = skipSpace(raw, keyEnd)
		if i >= len(raw) || raw[i] != ':' {
			return 0, 0, insertAt, false, -1, errors.New("JSONC 缺少冒号")
		}
		i = skipSpace(raw, i+1)
		valueEnd, err := scanValue(raw, i)
		if err != nil {
			return 0, 0, insertAt, false, -1, err
		}
		insertAt = valueEnd
		if k == key {
			return keyStart, valueEnd, valueEnd, true, -1, nil
		}
		i = skipSpace(raw, valueEnd)
		if i < len(raw) && raw[i] == ',' {
			i++
			continue
		}
		if i < len(raw) && raw[i] == '}' {
			return 0, 0, insertAt, false, i, nil
		}
		return 0, 0, insertAt, false, -1, errors.New("JSONC 成员分隔异常")
	}
}

func skipSpace(raw []byte, i int) int {
	for i < len(raw) {
		switch {
		case raw[i] == ' ' || raw[i] == '\t' || raw[i] == '\r' || raw[i] == '\n':
			i++
		case raw[i] == '/' && i+1 < len(raw) && raw[i+1] == '/':
			i += 2
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
		case raw[i] == '/' && i+1 < len(raw) && raw[i+1] == '*':
			i += 2
			for i+1 < len(raw) && !(raw[i] == '*' && raw[i+1] == '/') {
				i++
			}
			if i+1 < len(raw) {
				i += 2
			}
		default:
			return i
		}
	}
	return i
}

func scanString(raw []byte, i int) (int, error) {
	if i >= len(raw) || raw[i] != '"' {
		return 0, errors.New("JSONC 字符串位置异常")
	}
	i++
	for i < len(raw) {
		switch raw[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1, nil
		default:
			i++
		}
	}
	return 0, errors.New("JSONC 字符串未闭合")
}

func scanValue(raw []byte, i int) (int, error) {
	if i >= len(raw) {
		return 0, errors.New("JSONC 缺少值")
	}
	switch raw[i] {
	case '"':
		return scanString(raw, i)
	case '{', '[':
		depth := 0
		for i < len(raw) {
			switch {
			case raw[i] == '"':
				j, err := scanString(raw, i)
				if err != nil {
					return 0, err
				}
				i = j
			case raw[i] == '/' && i+1 < len(raw) && (raw[i+1] == '/' || raw[i+1] == '*'):
				i = skipSpace(raw, i)
			case raw[i] == '{' || raw[i] == '[':
				depth++
				i++
			case raw[i] == '}' || raw[i] == ']':
				depth--
				i++
				if depth == 0 {
					return i, nil
				}
			default:
				i++
			}
		}
		return 0, errors.New("JSONC 值未闭合")
	default:
		for i < len(raw) {
			c := raw[i]
			if c == ',' || c == '}' || c == ']' || c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				break
			}
			if c == '/' && i+1 < len(raw) && (raw[i+1] == '/' || raw[i+1] == '*') {
				break
			}
			i++
		}
		if strings.TrimSpace(string(raw[:i])) == "" {
			return 0, errors.New("JSONC 值无效")
		}
		return i, nil
	}
}
