package jsoncutil

import (
	"encoding/json"
	"strings"
	"testing"
)

func parseDoc(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	js, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, raw)
	}
	var doc map[string]any
	if err := json.Unmarshal(js, &doc); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, js)
	}
	return doc
}

func TestParseStripsCommentsAndTrailingCommas(t *testing.T) {
	doc := parseDoc(t, []byte("{\n  // head\n  \"a\": 1,\n  \"b\": [1, 2,],\n}\n"))
	if doc["a"] != float64(1) {
		t.Fatalf("a = %v", doc["a"])
	}
	if b, ok := doc["b"].([]any); !ok || len(b) != 2 {
		t.Fatalf("b = %v", doc["b"])
	}
}

func TestSpliceReplacePreservesOutsideComments(t *testing.T) {
	raw := []byte("{\n  // head comment\n  \"theme\": \"x\", // theme note\n  \"mcp\": {\"old\": {\"command\": \"o\"}}\n  // tail comment\n}\n")
	out, err := SpliceMember(raw, "mcp", map[string]any{
		"web": map[string]any{"type": "remote", "url": "https://x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{"head comment", "theme note", "tail comment"} {
		if !strings.Contains(text, want) {
			t.Fatalf("丢失注释 %q:\n%s", want, text)
		}
	}
	doc := parseDoc(t, out)
	mcp, _ := doc["mcp"].(map[string]any)
	if _, old := mcp["old"]; old {
		t.Fatalf("旧服务器未替换: %v", mcp)
	}
	web, _ := mcp["web"].(map[string]any)
	if web["url"] != "https://x" {
		t.Fatalf("web = %v", web)
	}
}

func TestSpliceInsertsMissingMemberWithTrailingComment(t *testing.T) {
	raw := []byte("{\n  \"a\": 1 // last note\n}\n")
	out, err := SpliceMember(raw, "mcp", map[string]any{"s": map[string]any{"command": "c"}})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "last note") {
		t.Fatalf("注释丢失:\n%s", out)
	}
	doc := parseDoc(t, out)
	if doc["a"] != float64(1) {
		t.Fatalf("a = %v", doc["a"])
	}
	mcp, _ := doc["mcp"].(map[string]any)
	if _, ok := mcp["s"]; !ok {
		t.Fatalf("mcp = %v", mcp)
	}
}

func TestSpliceInsertsIntoEmptyObject(t *testing.T) {
	out, err := SpliceMember([]byte("{ // empty\n}\n"), "mcp", map[string]any{"s": map[string]any{"command": "c"}})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "empty") {
		t.Fatalf("注释丢失:\n%s", out)
	}
	mcp, _ := parseDoc(t, out)["mcp"].(map[string]any)
	if _, ok := mcp["s"]; !ok {
		t.Fatalf("mcp = %v", mcp)
	}
}

func TestSpliceHandlesTrickyStrings(t *testing.T) {
	raw := []byte(`{"note":"}{ // /*","mcp":{"old":1}}`)
	out, err := SpliceMember(raw, "mcp", map[string]any{"new": map[string]any{"command": "n"}})
	if err != nil {
		t.Fatal(err)
	}
	doc := parseDoc(t, out)
	if doc["note"] != "}{ // /*" {
		t.Fatalf("note = %v", doc["note"])
	}
	mcp, _ := doc["mcp"].(map[string]any)
	if _, old := mcp["old"]; old {
		t.Fatalf("旧值未替换: %v", mcp)
	}
}

func TestSpliceRejectsNonObject(t *testing.T) {
	if _, err := SpliceMember([]byte(`[]`), "mcp", map[string]any{}); err == nil {
		t.Fatal("非对象应拒绝改写")
	}
}
