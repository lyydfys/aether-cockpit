package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// 品牌图标映射（brandicons.js）里引用的每个文件都必须真实存在。
// 少一个文件不会报错，只会让渠道卡片静默退化成"首字母色块"——很难被发现。
func TestBrandIconFilesExist(t *testing.T) {
	raw, err := embedded.ReadFile("ui2/js/brandicons.js")
	if err != nil {
		t.Fatalf("读取 brandicons.js 失败：%v", err)
	}
	// 映射表里只会出现形如 'xxx.svg' / 'xxx.png' 的文件名。
	ref := regexp.MustCompile(`'([a-z0-9][a-z0-9._-]*\.(?:svg|png))'`)
	seen := map[string]bool{}
	for _, m := range ref.FindAllStringSubmatch(string(raw), -1) {
		file := m[1]
		if seen[file] {
			continue
		}
		seen[file] = true
		if _, err := fs.Stat(embedded, "ui2/assets/icons/"+file); err != nil {
			t.Errorf("brandicons.js 引用了不存在的图标：%s", file)
		}
	}
	if len(seen) < 20 {
		t.Fatalf("只解析到 %d 个图标，正则可能没匹配上", len(seen))
	}
}

// DeepSeek 渠道必须用官方标志与官方品牌色：
//   - 渠道目录里的 BrandColor 是 #4D6BFE；
//   - 图标填充色必须与之一致，否则同一个品牌在卡片上会出现两种蓝。
func TestDeepSeekIconMatchesBrandColor(t *testing.T) {
	raw, err := embedded.ReadFile("ui2/assets/icons/deepseek.svg")
	if err != nil {
		t.Fatalf("读取 deepseek.svg 失败：%v", err)
	}
	svg := string(raw)
	if !strings.Contains(svg, `fill="#4D6BFE"`) {
		t.Fatalf("deepseek.svg 的填充色应为 #4D6BFE（与渠道 BrandColor 一致）：%s", firstLine(svg))
	}
	// 官方标志的路径以 M23.748 开头（simple-icons，CC0）；旧版是手写简化路径（M23.7 4.7q）。
	if !strings.Contains(svg, "M23.748 4.651") {
		t.Fatalf("deepseek.svg 不是官方标志路径：%s", firstLine(svg))
	}
	if !strings.Contains(svg, `viewBox="0 0 24 24"`) {
		t.Fatal("deepseek.svg 缺少 24x24 viewBox")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
