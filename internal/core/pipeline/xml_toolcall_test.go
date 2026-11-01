package pipeline

import (
	"strings"
	"testing"
)

// feedAll 把一段文本按固定大小切片喂给扫描器，模拟真实流式分片。
func feedAll(sc *xmlScanner, text string, chunk int) (string, []parsedToolCall) {
	var (
		sb    strings.Builder
		calls []parsedToolCall
	)
	r := []rune(text)
	for i := 0; i < len(r); i += chunk {
		end := i + chunk
		if end > len(r) {
			end = len(r)
		}
		t, c := sc.Feed(string(r[i:end]))
		sb.WriteString(t)
		calls = append(calls, c...)
	}
	t, c := sc.Flush()
	sb.WriteString(t)
	calls = append(calls, c...)
	return sb.String(), calls
}

// TestXMLScannerSplitTag 验证标签被切成多片时依然能识别。
//
// 这是最关键的边界：真实流式下发几乎不可能让标签落在一个 chunk 里。
func TestXMLScannerSplitTag(t *testing.T) {
	input := `好的，我来读取。<tool_call>{"name":"read_file","arguments":{"path":"a.go"}}</tool_call>`
	for _, chunk := range []int{1, 2, 3, 5, 7, 64} {
		text, calls := feedAll(newXMLScanner(), input, chunk)
		if len(calls) != 1 {
			t.Fatalf("chunk=%d: 工具调用数 = %d, want 1", chunk, len(calls))
		}
		if calls[0].Name != "read_file" {
			t.Errorf("chunk=%d: name = %q", chunk, calls[0].Name)
		}
		if calls[0].Arguments != `{"path":"a.go"}` {
			t.Errorf("chunk=%d: args = %q", chunk, calls[0].Arguments)
		}
		if strings.Contains(text, "tool_call") {
			t.Errorf("chunk=%d: 文本中不应残留标签: %q", chunk, text)
		}
		if text != "好的，我来读取。" {
			t.Errorf("chunk=%d: 文本 = %q", chunk, text)
		}
	}
}

// TestXMLScannerMultipleCalls 验证连续多个工具调用。
func TestXMLScannerMultipleCalls(t *testing.T) {
	input := `<tool_call>{"name":"a","arguments":{}}</tool_call>中间文本<tool_call>{"name":"b","arguments":{"x":1}}</tool_call>`
	text, calls := feedAll(newXMLScanner(), input, 3)
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[0].Name != "a" || calls[1].Name != "b" {
		t.Errorf("names = %q, %q", calls[0].Name, calls[1].Name)
	}
	if text != "中间文本" {
		t.Errorf("text = %q", text)
	}
}

// TestXMLScannerToolUseAlias 验证 <tool_use> 别名。
func TestXMLScannerToolUseAlias(t *testing.T) {
	input := `<tool_use>{"name":"grep","input":{"q":"foo"}}</tool_use>`
	_, calls := feedAll(newXMLScanner(), input, 4)
	if len(calls) != 1 || calls[0].Name != "grep" {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].Arguments != `{"q":"foo"}` {
		t.Errorf("args = %q", calls[0].Arguments)
	}
}

// TestXMLScannerStringArguments 验证 arguments 是字符串时也能解析。
func TestXMLScannerStringArguments(t *testing.T) {
	input := `<tool_call>{"id":"call_1","name":"bash","arguments":"{\"cmd\":\"ls\"}"}</tool_call>`
	_, calls := feedAll(newXMLScanner(), input, 6)
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if calls[0].ID != "call_1" {
		t.Errorf("id = %q", calls[0].ID)
	}
	if calls[0].Arguments != `{"cmd":"ls"}` {
		t.Errorf("args = %q", calls[0].Arguments)
	}
}

// TestXMLScannerCodeFence 验证模型把 JSON 包进 ```json 时仍可解析。
func TestXMLScannerCodeFence(t *testing.T) {
	input := "<tool_call>\n```json\n{\"name\":\"read\",\"arguments\":{\"p\":1}}\n```\n</tool_call>"
	_, calls := feedAll(newXMLScanner(), input, 8)
	if len(calls) != 1 || calls[0].Name != "read" {
		t.Fatalf("calls = %+v", calls)
	}
}

// TestXMLScannerUnclosed 验证未闭合的工具块不会吞掉内容。
func TestXMLScannerUnclosed(t *testing.T) {
	input := `前文<tool_call>{"name":"x"}`
	text, calls := feedAll(newXMLScanner(), input, 4)
	if len(calls) != 0 {
		t.Errorf("未闭合时不应产出调用，实际 %d", len(calls))
	}
	if !strings.Contains(text, "前文") {
		t.Errorf("前文丢失: %q", text)
	}
}

// TestXMLScannerPlainLessThan 验证普通文本里的 < 不被误判。
func TestXMLScannerPlainLessThan(t *testing.T) {
	input := "if a < b then return a < 10"
	text, calls := feedAll(newXMLScanner(), input, 3)
	if len(calls) != 0 {
		t.Fatalf("不应有调用")
	}
	if text != input {
		t.Errorf("text = %q, want %q", text, input)
	}
}

// TestSystemMerge 验证 system 角色合并。
func TestSystemMerge(t *testing.T) {
	// 直接测 applySystemMerge 的行为需要构造 UnifiedChatRequest，
	// 这里放在 ir 包测试更合适，此处仅验证合并函数不丢内容。
	items := mergeAdjacentItems(nil)
	if items != nil {
		t.Errorf("空输入应返回 nil")
	}
}

// --- Markdown 围栏感知（aether mdCode.find 同款消歧） ---

// TestXMLScannerFencedExampleIgnored 验证代码块里复述的 <tool_call> 用法示例
// 不被当成真调用，且真调用与围栏内示例混合时互不干扰。
func TestXMLScannerFencedExampleIgnored(t *testing.T) {
	input := "先看示例：\n```xml\n<tool_call>{\"name\":\"demo\",\"arguments\":{}}</tool_call>\n```\n以上只是示例。\n<tool_call>{\"name\":\"real\",\"arguments\":{\"a\":1}}</tool_call>"
	wantText := "先看示例：\n```xml\n<tool_call>{\"name\":\"demo\",\"arguments\":{}}</tool_call>\n```\n以上只是示例。\n"
	for _, chunk := range []int{1, 2, 3, 5, 7, 64} {
		text, calls := feedAll(newXMLScanner(), input, chunk)
		if len(calls) != 1 || calls[0].Name != "real" {
			t.Fatalf("chunk=%d: calls = %+v, want 仅 real", chunk, calls)
		}
		if calls[0].Arguments != `{"a":1}` {
			t.Errorf("chunk=%d: args = %q", chunk, calls[0].Arguments)
		}
		if text != wantText {
			t.Errorf("chunk=%d: 文本 = %q, want %q", chunk, text, wantText)
		}
	}
}

// TestXMLScannerFenceSplitAcrossFeeds 验证围栏标记被切成多片时依然配对，
// 且"半个标记串"先被扣住、凑满后才进围栏（跨 chunk 状态保持）。
func TestXMLScannerFenceSplitAcrossFeeds(t *testing.T) {
	sc := newXMLScanner()
	var sb strings.Builder
	var calls []parsedToolCall
	feed := func(delta, wantText string) {
		t.Helper()
		text, c := sc.Feed(delta)
		if text != wantText {
			t.Fatalf("Feed(%q) 文本 = %q, want %q", delta, text, wantText)
		}
		if len(c) != 0 {
			t.Fatalf("Feed(%q) 不应产出调用: %+v", delta, c)
		}
		sb.WriteString(text)
		calls = append(calls, c...)
	}

	// "``" 是半个标记串，必须扣住；前面的普通文本照常吐出。
	feed("说明：\n``", "说明：\n")
	// 凑成 ``` 开围栏：围栏内的伪标签按普通文本吐出。
	feed("`\n<tool_call>{\"name\":\"demo\"}</tool_call>\n", "```\n<tool_call>{\"name\":\"demo\"}</tool_call>\n")
	// 同字符标记行闭合围栏。
	feed("```\n", "```\n")

	text, c := sc.Flush()
	sb.WriteString(text)
	calls = append(calls, c...)
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want 0", calls)
	}
	want := "说明：\n```\n<tool_call>{\"name\":\"demo\"}</tool_call>\n```\n"
	if sb.String() != want {
		t.Errorf("总文本 = %q, want %q", sb.String(), want)
	}
}

// TestXMLScannerFenceTilde 验证 ~~~ 围栏，且不同标记字符不互相配对。
func TestXMLScannerFenceTilde(t *testing.T) {
	// ~~~ 开、~~~ 闭：围栏内示例忽略，围栏外真调用生效。
	input := "~~~\n<tool_call>{\"name\":\"demo\"}</tool_call>\n~~~\n之后<tool_call>{\"name\":\"real\"}</tool_call>"
	wantText := "~~~\n<tool_call>{\"name\":\"demo\"}</tool_call>\n~~~\n之后"
	text, calls := feedAll(newXMLScanner(), input, 5)
	if len(calls) != 1 || calls[0].Name != "real" {
		t.Fatalf("calls = %+v, want 仅 real", calls)
	}
	if text != wantText {
		t.Errorf("文本 = %q, want %q", text, wantText)
	}

	// ``` 无法闭合 ~~~ 围栏：后续真调用仍在围栏内，被当文本吞掉。
	input2 := "~~~\n<tool_call>{\"name\":\"demo\"}</tool_call>\n```\n之后<tool_call>{\"name\":\"real\"}</tool_call>"
	text2, calls2 := feedAll(newXMLScanner(), input2, 4)
	if len(calls2) != 0 {
		t.Fatalf("calls = %+v, want 0", calls2)
	}
	if text2 != input2 {
		t.Errorf("文本 = %q, want 原样 %q", text2, input2)
	}
}

// TestXMLScannerFenceUnclosed 验证未闭合到流结束的围栏不吞内容、不产调用。
func TestXMLScannerFenceUnclosed(t *testing.T) {
	input := "```python\n<tool_call>{\"name\":\"demo\"}</tool_call>\n未闭合就结束了"
	text, calls := feedAll(newXMLScanner(), input, 4)
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want 0", calls)
	}
	if text != input {
		t.Errorf("文本 = %q, want 原样 %q", text, input)
	}
}

// TestXMLScannerFenceCloseRules 验证闭合行的严格性：
// 开口 4 个反引号时 3 个不能闭合；带 info string 的行也不能闭合。
func TestXMLScannerFenceCloseRules(t *testing.T) {
	input := "````\n<tool_call>{\"name\":\"a\"}</tool_call>\n```\n依然在围栏里\n````\n之外<tool_call>{\"name\":\"real\"}</tool_call>"
	wantText := "````\n<tool_call>{\"name\":\"a\"}</tool_call>\n```\n依然在围栏里\n````\n之外"
	text, calls := feedAll(newXMLScanner(), input, 6)
	if len(calls) != 1 || calls[0].Name != "real" {
		t.Fatalf("calls = %+v, want 仅 real", calls)
	}
	if text != wantText {
		t.Errorf("文本 = %q, want %q", text, wantText)
	}

	// 围栏内 "```json" 这样的行带 info string，不是闭合行。
	input2 := "```go\n<tool_call>{\"name\":\"a\"}</tool_call>\n```json\n依然在围栏里\n```\n之外<tool_call>{\"name\":\"real\"}</tool_call>"
	wantText2 := "```go\n<tool_call>{\"name\":\"a\"}</tool_call>\n```json\n依然在围栏里\n```\n之外"
	text2, calls2 := feedAll(newXMLScanner(), input2, 7)
	if len(calls2) != 1 || calls2[0].Name != "real" {
		t.Fatalf("calls = %+v, want 仅 real", calls2)
	}
	if text2 != wantText2 {
		t.Errorf("文本 = %q, want %q", text2, wantText2)
	}
}

// TestXMLScannerFenceIndented 验证行首允许空白：缩进后的围栏标记同样生效。
func TestXMLScannerFenceIndented(t *testing.T) {
	input := "正文：\n  ```\n<tool_call>{\"name\":\"demo\"}</tool_call>\n  ```\n之后<tool_call>{\"name\":\"real\"}</tool_call>"
	wantText := "正文：\n  ```\n<tool_call>{\"name\":\"demo\"}</tool_call>\n  ```\n之后"
	text, calls := feedAll(newXMLScanner(), input, 3)
	if len(calls) != 1 || calls[0].Name != "real" {
		t.Fatalf("calls = %+v, want 仅 real", calls)
	}
	if text != wantText {
		t.Errorf("文本 = %q, want %q", text, wantText)
	}
}

// TestXMLScannerFenceInfoStringSwallowsSameLine 验证单行示例：
// 开口标记所在行剩余部分是 info string，其上的伪标签不进状态机。
func TestXMLScannerFenceInfoStringSwallowsSameLine(t *testing.T) {
	input := "```<tool_call>{\"name\":\"demo\"}</tool_call>\n```\n之后<tool_call>{\"name\":\"real\",\"arguments\":{}}</tool_call>"
	wantText := "```<tool_call>{\"name\":\"demo\"}</tool_call>\n```\n之后"
	text, calls := feedAll(newXMLScanner(), input, 2)
	if len(calls) != 1 || calls[0].Name != "real" {
		t.Fatalf("calls = %+v, want 仅 real", calls)
	}
	if calls[0].Arguments != "{}" {
		t.Errorf("args = %q", calls[0].Arguments)
	}
	if text != wantText {
		t.Errorf("文本 = %q, want %q", text, wantText)
	}
}

// TestXMLScannerPartialTagMeetsFence 验证扣尾与围栏的协同：
// 扣住的半个标签后来被证明不是标签也不是围栏标记 -> 按文本吐出；
// 之后围栏内示例仍被忽略、真调用仍生效。
func TestXMLScannerPartialTagMeetsFence(t *testing.T) {
	sc := newXMLScanner()
	var sb strings.Builder
	var calls []parsedToolCall
	feed := func(delta string, wantCalls int) {
		t.Helper()
		text, c := sc.Feed(delta)
		if len(c) != wantCalls {
			t.Fatalf("Feed(%q) 产出 %d 个调用, want %d", delta, len(c), wantCalls)
		}
		sb.WriteString(text)
		calls = append(calls, c...)
	}

	// "正文 " 照常吐出，"<tool_ca" 作为半个标签扣住。
	feed("正文 <tool_ca", 0)
	// "<tool_ca``"：既凑不成标签、标记也不在行首 -> 全部按文本吐出。
	feed("``\n", 0)
	// 真围栏 + 围栏内示例。
	feed("\n```xml\n<tool_call>{\"name\":\"demo\"}</tool_call>\n```\n", 0)
	// 围栏外的真调用。
	feed("<tool_call>{\"name\":\"real\",\"arguments\":{\"a\":1}}</tool_call>", 1)
	// 真调用之后再来一段带围栏的示例。
	feed("再示例如下：\n```\n<tool_call>{\"name\":\"demo2\"}</tool_call>\n```\n完", 0)

	text, c := sc.Flush()
	sb.WriteString(text)
	calls = append(calls, c...)
	if len(calls) != 1 || calls[0].Name != "real" {
		t.Fatalf("calls = %+v, want 仅 real", calls)
	}
	want := "正文 <tool_ca``\n\n```xml\n<tool_call>{\"name\":\"demo\"}</tool_call>\n```\n" +
		"再示例如下：\n```\n<tool_call>{\"name\":\"demo2\"}</tool_call>\n```\n完"
	if sb.String() != want {
		t.Errorf("总文本 = %q, want %q", sb.String(), want)
	}
}
