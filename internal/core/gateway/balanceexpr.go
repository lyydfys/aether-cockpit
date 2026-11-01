package gateway

import (
	"encoding/json"
	"strconv"
	"strings"
)

// balanceExpr 计算 magpie 的「 Balance field 」表达式子集。
//
// 支持编辑器示例里承诺的写法：
//
//	data.balance            $data.quota / 500000       (1 - credits.used / 70) %
//	$remaining              5h: a.used / a.cap %; $credits.left
//	a.*.used                a[status=active].used      sum(a.*.used) / max() / min() / count()
//
// 第一段有值的语句决定余额；`$` 前缀与 `%` 后缀用作展示装饰。
// 求值失败返回 ok=false，调用方据此如实报错，而不是把 0 当余额。
type balanceValue struct {
	amount float64
	unit   string // "USD" / "%" / ""
	text   string // 第一段的展示文本
	all    string // 所有段的展示文本（"5h 12% · $40"）
	parts  []balanceSegment
}

// balanceSegment 是多段余额中的一段，供用量卡逐行展示。
type balanceSegment struct {
	label string
	text  string
}

func evalBalanceExpr(expr string, root any, defaultPath string) (balanceValue, bool) {
	src := strings.TrimSpace(expr)
	if src == "" {
		src = defaultPath
	}
	if strings.TrimSpace(src) == "" {
		return balanceValue{}, false
	}
	parts := strings.Split(src, ";")
	var first *balanceValue
	var texts []string
	var segs []balanceSegment
	for _, raw := range parts {
		stmt := strings.TrimSpace(raw)
		if stmt == "" {
			continue
		}
		label := ""
		// 标签（5h: / $credits.left）不参与求值，只用于多段展示。
		if i := strings.IndexByte(stmt, ':'); i >= 0 {
			label = strings.TrimSpace(stmt[:i])
			stmt = strings.TrimSpace(stmt[i+1:])
		}
		unit := ""
		if strings.HasPrefix(stmt, "$") {
			unit = "USD"
			stmt = strings.TrimSpace(stmt[1:])
		}
		if strings.HasSuffix(stmt, "%") {
			stmt = strings.TrimSpace(strings.TrimSuffix(stmt, "%"))
			if unit == "" {
				unit = "%"
			}
		}
		v, ok := parseBalanceArith(stmt, root)
		if !ok {
			continue
		}
		out := balanceValue{amount: v, unit: unit}
		num := trimFloat(v, 2)
		switch unit {
		case "USD":
			out.text = "$" + num
		case "%":
			out.text = num + "%"
		default:
			out.text = num
		}
		if out.text == "" {
			out.text = "0"
		}
		label = strings.TrimSpace(label)
		texts = append(texts, strings.TrimSpace(label+" "+out.text))
		segs = append(segs, balanceSegment{label: label, text: out.text})
		if first == nil {
			cp := out
			first = &cp
		}
	}
	if first == nil {
		return balanceValue{}, false
	}
	first.all = strings.Join(texts, " · ")
	first.parts = segs
	return *first, true
}

type balanceParser struct {
	src string
	pos int
	obj any
}

func parseBalanceArith(src string, root any) (float64, bool) {
	p := &balanceParser{src: src, obj: root}
	v, ok := p.parseSum()
	if !ok {
		return 0, false
	}
	p.skipSpace()
	if p.pos != len(p.src) {
		return 0, false
	}
	return v, true
}

func (p *balanceParser) skipSpace() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t' || p.src[p.pos] == '\n') {
		p.pos++
	}
}

func (p *balanceParser) peek() byte {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *balanceParser) parseSum() (float64, bool) {
	v, ok := p.parseProduct()
	if !ok {
		return 0, false
	}
	for {
		switch p.peek() {
		case '+':
			p.pos++
			r, ok := p.parseProduct()
			if !ok {
				return 0, false
			}
			v += r
		case '-':
			p.pos++
			r, ok := p.parseProduct()
			if !ok {
				return 0, false
			}
			v -= r
		default:
			return v, true
		}
	}
}

func (p *balanceParser) parseProduct() (float64, bool) {
	v, ok := p.parseUnary()
	if !ok {
		return 0, false
	}
	for {
		switch p.peek() {
		case '*':
			p.pos++
			r, ok := p.parseUnary()
			if !ok {
				return 0, false
			}
			v *= r
		case '/':
			p.pos++
			r, ok := p.parseUnary()
			if !ok || r == 0 {
				return 0, false
			}
			v /= r
		default:
			return v, true
		}
	}
}

func (p *balanceParser) parseUnary() (float64, bool) {
	if p.peek() == '-' {
		p.pos++
		v, ok := p.parseUnary()
		return -v, ok
	}
	return p.parseAtom()
}

func (p *balanceParser) parseAtom() (float64, bool) {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return 0, false
	}
	if p.src[p.pos] == '(' {
		p.pos++
		v, ok := p.parseSum()
		if !ok {
			return 0, false
		}
		p.skipSpace()
		if p.pos >= len(p.src) || p.src[p.pos] != ')' {
			return 0, false
		}
		p.pos++
		return v, true
	}
	if isDigit(p.src[p.pos]) || p.src[p.pos] == '.' {
		start := p.pos
		for p.pos < len(p.src) && (isDigit(p.src[p.pos]) || p.src[p.pos] == '.') {
			p.pos++
		}
		f, err := strconv.ParseFloat(p.src[start:p.pos], 64)
		return f, err == nil
	}
	// $ 前缀在语句层已处理；这里允许出现在路径前（如 "$a.b"）。
	if p.src[p.pos] == '$' {
		p.pos++
	}
	ident := p.parseIdent()
	if ident == "" {
		return 0, false
	}
	if p.peek() == '(' {
		return p.callFunc(ident)
	}
	val, ok := p.resolvePath(ident)
	if !ok {
		return 0, false
	}
	nums, ok := balanceNumbers(val)
	if !ok || len(nums) == 0 {
		return 0, false
	}
	return nums[0], true
}

func (p *balanceParser) parseIdent() string {
	p.skipSpace()
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '_' || c == '-' || isAlpha(c) || isDigit(c) {
			p.pos++
			continue
		}
		break
	}
	return p.src[start:p.pos]
}

func (p *balanceParser) callFunc(name string) (float64, bool) {
	p.pos++ // '('
	val, ok := p.evalListArg()
	if !ok {
		return 0, false
	}
	p.skipSpace()
	if p.pos >= len(p.src) || p.src[p.pos] != ')' {
		return 0, false
	}
	p.pos++
	nums, okList := balanceNumbers(val)
	switch name {
	case "sum":
		if !okList {
			return 0, false
		}
		var sum float64
		for _, n := range nums {
			sum += n
		}
		return sum, true
	case "max":
		if !okList || len(nums) == 0 {
			return 0, false
		}
		m := nums[0]
		for _, n := range nums[1:] {
			if n > m {
				m = n
			}
		}
		return m, true
	case "min":
		if !okList || len(nums) == 0 {
			return 0, false
		}
		m := nums[0]
		for _, n := range nums[1:] {
			if n < m {
				m = n
			}
		}
		return m, true
	case "count":
		return float64(balanceCount(val)), true
	}
	return 0, false
}

// evalListArg 读取函数实参；普通表达式返回标量，路径返回列表。
func (p *balanceParser) evalListArg() (any, bool) {
	start := p.pos
	ident := p.parseIdent()
	if ident == "" {
		v, ok := p.parseSum()
		return v, ok
	}
	if p.peek() == '(' {
		v, ok := p.callFunc(ident)
		return v, ok
	}
	val, ok := p.resolvePath(ident)
	if ok {
		return val, true
	}
	p.pos = start
	v, ok := p.parseSum()
	return v, ok
}

func (p *balanceParser) resolvePath(ident string) (any, bool) {
	cur := pathLookup(p.obj, ident)
	if cur == nil {
		return nil, false
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.src) {
			return cur, true
		}
		switch p.src[p.pos] {
		case '.':
			p.pos++
			if p.pos < len(p.src) && p.src[p.pos] == '*' {
				p.pos++
				cur = pathExpand(cur, "")
				continue
			}
			key := p.parseIdent()
			if key == "" {
				return nil, false
			}
			cur = pathExpand(cur, key)
			if cur == nil {
				return nil, false
			}
		case '[':
			p.pos++
			cur = p.pathIndex(cur)
			if cur == nil {
				return nil, false
			}
		default:
			return cur, true
		}
	}
}

func (p *balanceParser) pathIndex(cur any) any {
	p.skipSpace()
	// [status=active]：按字段值过滤数组。
	start := p.pos
	key := p.parseIdent()
	if key != "" {
		p.skipSpace()
		if p.pos < len(p.src) && p.src[p.pos] == '=' {
			p.pos++
			p.skipSpace()
			want := p.parseIdent()
			if want == "" {
				startNum := p.pos
				for p.pos < len(p.src) && (isDigit(p.src[p.pos]) || p.src[p.pos] == '.' || p.src[p.pos] == '-') {
					p.pos++
				}
				want = p.src[startNum:p.pos]
			}
			if p.pos < len(p.src) && p.src[p.pos] == ']' {
				p.pos++
			}
			list, ok := cur.([]any)
			if !ok {
				return nil
			}
			out := make([]any, 0, len(list))
			for _, it := range list {
				m, ok := it.(map[string]any)
				if !ok {
					continue
				}
				if raw, ok := m[key]; ok && strings.EqualFold(toText(raw), want) {
					out = append(out, it)
				}
			}
			return out
		}
	}
	// [n]：数组下标。
	p.pos = start
	numStart := p.pos
	for p.pos < len(p.src) && (isDigit(p.src[p.pos]) || p.src[p.pos] == '-') {
		p.pos++
	}
	if numStart == p.pos {
		return nil
	}
	idx, err := strconv.Atoi(p.src[numStart:p.pos])
	if err != nil {
		return nil
	}
	if p.pos < len(p.src) && p.src[p.pos] == ']' {
		p.pos++
	}
	list, ok := cur.([]any)
	if !ok || idx < 0 || idx >= len(list) {
		return nil
	}
	return list[idx]
}

func pathLookup(obj any, key string) any {
	switch v := obj.(type) {
	case map[string]any:
		if x, ok := v[key]; ok {
			return x
		}
	}
	return nil
}

// pathExpand 取值：对数组做映射（跳过缺失项），对象直接取字段。
func pathExpand(cur any, key string) any {
	switch v := cur.(type) {
	case []any:
		out := make([]any, 0, len(v))
		for _, it := range v {
			if key == "" {
				out = append(out, it)
				continue
			}
			if m, ok := it.(map[string]any); ok {
				if x, ok := m[key]; ok {
					out = append(out, x)
				}
			}
		}
		return out
	case map[string]any:
		if key == "" {
			return v
		}
		if x, ok := v[key]; ok {
			return x
		}
	}
	return nil
}

func balanceNumbers(v any) ([]float64, bool) {
	switch x := v.(type) {
	case float64:
		return []float64{x}, true
	case json.Number:
		f, err := x.Float64()
		return []float64{f}, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return nil, false
		}
		return []float64{f}, true
	case []any:
		out := make([]float64, 0, len(x))
		for _, it := range x {
			nums, ok := balanceNumbers(it)
			if !ok {
				continue
			}
			out = append(out, nums...)
		}
		return out, true
	}
	return nil, false
}

func balanceCount(v any) int {
	if list, ok := v.([]any); ok {
		return len(list)
	}
	return 0
}

func toText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return ""
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
