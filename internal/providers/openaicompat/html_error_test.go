package openaicompat

import "testing"

// 端点路径配错时上游会回 HTML（HTTP 200），必须识别成"地址不对"，
// 而不是报"无法解析"让人以为上游故障。
func TestLooksLikeHTML(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"标准落地页", "<!doctype html>\n<html lang=\"en\"><head><title>x</title></head><body></body></html>", true},
		{"大写 DOCTYPE", "<!DOCTYPE HTML><html><head></head><body></body></html>", true},
		{"只有 html 标签", "<html><body>oops</body></html>", true},
		{"带有 head/body 的片段", "<div><head></head><body></body></div>", true},
		{"正常 JSON", `{"choices":[{"message":{"content":"hi"}}]}`, false},
		{"JSON 错误体", `{"error":{"message":"Invalid token"}}`, false},
		{"SSE 帧", "data: {\"id\":\"c\",\"choices\":[]}\n\n", false},
		{"空体", "", false},
	}
	for _, c := range cases {
		if got := looksLikeHTML([]byte(c.body)); got != c.want {
			t.Errorf("%s: looksLikeHTML = %v, want %v", c.name, got, c.want)
		}
	}
}

// 只检查开头，避免把正文里恰好含 <html> 的正常响应误判。
func TestLooksLikeHTMLLargeBodyPrefixedByJSON(t *testing.T) {
	body := `{"choices":[{"message":{"content":"这段文字提到了 <html> 标签"}}]}`
	if looksLikeHTML([]byte(body)) {
		t.Fatal("JSON 正文里提到 <html> 不应判定为网页")
	}
}
