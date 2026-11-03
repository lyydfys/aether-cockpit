package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 无头 CLI 的子命令。
//
// 为什么不再只有 flag：服务器 / 容器 / WSL 里没有 GUI，`aether-cockpit-cli` 是
// 唯一入口。此前它只能"起一个网关"，查看状态、导出用量、管会话全得自己拼
// curl——这些能力后端全都有，缺的只是一层分派。
//
// 设计取舍：子命令**不直接调内部函数**，而是打本机 /api/*（回环免鉴权）。
// 这样：(1) 行为与 GUI 完全一致，不会出现"CLI 与界面结果不同"；
// (2) 可以对**已经在跑**的实例操作；(3) 不需要为 CLI 再造一套初始化流程。

const cliUsage = `Aether Cockpit 命令行

用法：
  aether-cockpit-cli [serve] [启动参数]     启动网关（默认行为）
  aether-cockpit-cli status                 查看运行状态与渠道数
  aether-cockpit-cli providers              列出渠道与可用账号
  aether-cockpit-cli usage [--days N] [--csv]
                                            查看（或导出）用量
  aether-cockpit-cli quota                  查看订阅窗口额度
  aether-cockpit-cli alerts                 查看额度提醒
  aether-cockpit-cli sessions [--agent X]   列出会话
  aether-cockpit-cli backup export --out F  导出配置备份到文件
  aether-cockpit-cli backup import --in F   从文件导入配置备份

公共参数（也可用环境变量）：
  --addr  网关地址（默认取 AETHER_ADDR，否则 http://127.0.0.1:7860）
  --key   网关 API Key（默认取 AETHER_API_KEY；本机访问可省略）
`

// cliArgs 是子命令解析后的公共参数。
type cliArgs struct {
	addr string
	key  string
	rest []string
}

func parseCLIArgs(args []string) cliArgs {
	out := cliArgs{addr: strings.TrimSpace(os.Getenv("AETHER_ADDR")), key: strings.TrimSpace(os.Getenv("AETHER_API_KEY"))}
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--addr":
			if i+1 < len(args) {
				out.addr = args[i+1]
				i++
			}
		case "--key":
			if i+1 < len(args) {
				out.key = args[i+1]
				i++
			}
		default:
			if v, ok := strings.CutPrefix(args[i], "--addr="); ok {
				out.addr = v
			} else if v, ok := strings.CutPrefix(args[i], "--key="); ok {
				out.key = v
			} else {
				rest = append(rest, args[i])
			}
		}
	}
	if out.addr == "" {
		out.addr = "http://127.0.0.1:7860"
	}
	if !strings.HasPrefix(out.addr, "http") {
		out.addr = "http://" + out.addr
	}
	out.addr = strings.TrimSuffix(out.addr, "/")
	out.rest = rest
	return out
}

// apiGet / apiPost 访问本机管理接口。
func (c cliArgs) apiGet(path string) ([]byte, error) {
	return c.do(http.MethodGet, path, nil)
}

func (c cliArgs) apiPost(path string, body any) ([]byte, error) {
	raw, _ := json.Marshal(body)
	return c.do(http.MethodPost, path, raw)
}

func (c cliArgs) do(method, path string, body []byte) ([]byte, error) {
	url := c.addr + "/api/" + strings.TrimPrefix(path, "/")
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.key != "" {
		// 管理接口认 Bearer；远程访问时它是唯一凭据。
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接网关失败（%s）：%w", c.addr, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("网关返回 %s：%s", resp.Status, strings.TrimSpace(string(firstN(data, 300))))
	}
	return data, nil
}

func firstN(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

// isSubcommand 判断首个位置参数是否是本程序认得的子命令。
func isSubcommand(name string) bool {
	switch name {
	case "status", "providers", "usage", "quota", "alerts", "sessions", "backup",
		"help", "-h", "--help":
		return true
	}
	return false
}

// runCLI 执行子命令；返回 true 表示"这次调用是子命令，不要继续启动网关"。
func runCLI(args []string) bool {
	if len(args) == 0 {
		return false
	}
	cmd := args[0]
	if cmd == "serve" {
		return false // 落回默认的启动流程
	}
	if !isSubcommand(cmd) {
		return false // 不是子命令：让原有 flag 解析继续（向后兼容）
	}
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(cliUsage)
		return true
	}

	c := parseCLIArgs(args[1:])
	var err error
	switch cmd {
	case "status":
		err = cliStatus(c)
	case "providers":
		err = cliProviders(c)
	case "usage":
		err = cliUsageCmd(c)
	case "quota":
		err = cliQuota(c)
	case "alerts":
		err = cliAlerts(c)
	case "sessions":
		err = cliSessions(c)
	case "backup":
		err = cliBackup(c)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误："+err.Error())
		os.Exit(1)
	}
	return true
}

func cliStatus(c cliArgs) error {
	raw, err := c.apiGet("state")
	if err != nil {
		return err
	}
	var st struct {
		Agents  []map[string]any `json:"agents"`
		Catalog string           `json:"catalog"`
		Served  int              `json:"served"`
	}
	if json.Unmarshal(raw, &st) != nil {
		return fmt.Errorf("返回不是合法 JSON")
	}
	runtime, _ := c.apiGet("runtime")
	var rt map[string]any
	_ = json.Unmarshal(runtime, &rt)
	fmt.Printf("网关地址   %s\n", c.addr)
	if addr, ok := rt["public_addr"].(string); ok && addr != "" {
		fmt.Printf("公开入口   %s\n", addr)
	}
	fmt.Printf("已服务请求 %d\n", st.Served)
	fmt.Printf("接入 Agent %d 个\n", len(st.Agents))
	if st.Catalog != "" {
		fmt.Printf("目录       %s\n", st.Catalog)
	}
	return nil
}

func cliProviders(c cliArgs) error {
	raw, err := c.apiGet("providers")
	if err != nil {
		return err
	}
	var body struct {
		Providers []map[string]any `json:"providers"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return fmt.Errorf("返回不是合法 JSON")
	}
	sort.SliceStable(body.Providers, func(i, j int) bool {
		a, _ := body.Providers[i]["id"].(string)
		b, _ := body.Providers[j]["id"].(string)
		return a < b
	})
	fmt.Printf("%-24s %-28s %s\n", "ID", "名称", "状态")
	for _, p := range body.Providers {
		id, _ := p["id"].(string)
		name, _ := p["display_name"].(string)
		state := "可用"
		if off, ok := p["off"].(bool); ok && off {
			state = "已停用"
		}
		fmt.Printf("%-24s %-28s %s\n", id, name, state)
	}
	return nil
}

func cliUsageCmd(c cliArgs) error {
	days := 14
	csvOut := false
	for i := 0; i < len(c.rest); i++ {
		switch c.rest[i] {
		case "--days":
			if i+1 < len(c.rest) {
				if n, err := strconv.Atoi(c.rest[i+1]); err == nil && n > 0 {
					days = n
				}
				i++
			}
		case "--csv":
			csvOut = true
		}
	}
	if csvOut {
		raw, err := c.apiGet("usage/requests.csv?period=" + strconv.Itoa(days) + "d")
		if err != nil {
			return err
		}
		_, _ = os.Stdout.Write(raw)
		return nil
	}
	raw, err := c.apiGet("usage?days=" + strconv.Itoa(days))
	if err != nil {
		return err
	}
	var u struct {
		Calls     int64            `json:"calls"`
		Errors    int64            `json:"errors"`
		Input     int64            `json:"input"`
		Output    int64            `json:"output"`
		CacheRead int64            `json:"cache_read"`
		Cost      float64          `json:"cost"`
		Unpriced  int64            `json:"unpriced"`
		Models    []map[string]any `json:"models"`
	}
	if json.Unmarshal(raw, &u) != nil {
		return fmt.Errorf("返回不是合法 JSON")
	}
	fmt.Printf("统计窗口   最近 %d 天\n", days)
	fmt.Printf("请求        %d（失败 %d）\n", u.Calls, u.Errors)
	fmt.Printf("Token       输入 %d / 输出 %d / 缓存读 %d\n", u.Input, u.Output, u.CacheRead)
	fmt.Printf("费用        $%.4f（未定价请求 %d）\n", u.Cost, u.Unpriced)
	if len(u.Models) > 0 {
		fmt.Println("\n按模型（花费降序，前 10）：")
		for i, m := range u.Models {
			if i >= 10 {
				break
			}
			id, _ := m["id"].(string)
			cost, _ := m["cost"].(float64)
			calls, _ := m["calls"].(float64)
			fmt.Printf("  %-40s $%-10.4f %d 次\n", id, cost, int64(calls))
		}
	}
	return nil
}

func cliQuota(c cliArgs) error {
	raw, err := c.apiGet("usage/quotas")
	if err != nil {
		return err
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return fmt.Errorf("返回不是合法 JSON")
	}
	pretty, _ := json.MarshalIndent(body, "", "  ")
	fmt.Println(string(pretty))
	return nil
}

func cliAlerts(c cliArgs) error {
	raw, err := c.apiGet("alerts")
	if err != nil {
		return err
	}
	var body struct {
		Alerts []struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"alerts"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return fmt.Errorf("返回不是合法 JSON")
	}
	if len(body.Alerts) == 0 {
		fmt.Println("没有需要提醒的额度问题。")
		return nil
	}
	for _, a := range body.Alerts {
		label := map[string]string{"expiring": "即将重置", "low": "额度偏低", "exhausted": "已用尽"}[a.Kind]
		if label == "" {
			label = a.Kind
		}
		fmt.Printf("[%s] %s\n", label, a.Message)
	}
	return nil
}

func cliSessions(c cliArgs) error {
	agent := ""
	for i := 0; i < len(c.rest); i++ {
		if c.rest[i] == "--agent" && i+1 < len(c.rest) {
			agent = c.rest[i+1]
			i++
		}
	}
	raw, err := c.apiGet("sessions" + map[bool]string{true: "", false: "?agent=" + agent}[agent == ""])
	if err != nil {
		return err
	}
	var body struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return fmt.Errorf("返回不是合法 JSON")
	}
	if len(body.Sessions) == 0 {
		fmt.Println("没有找到会话。")
		return nil
	}
	for _, s := range body.Sessions {
		id, _ := s["id"].(string)
		title, _ := s["title"].(string)
		cwd, _ := s["cwd"].(string)
		if len(title) > 40 {
			title = title[:40] + "…"
		}
		fmt.Printf("%-24s %-42s %s\n", id, title, cwd)
	}
	return nil
}

func cliBackup(c cliArgs) error {
	if len(c.rest) == 0 {
		return fmt.Errorf("用法：backup export --out FILE / backup import --in FILE")
	}
	action := c.rest[0]
	out, in := "", ""
	for i := 1; i < len(c.rest); i++ {
		switch c.rest[i] {
		case "--out", "--in":
			if i+1 < len(c.rest) {
				if c.rest[i] == "--out" {
					out = c.rest[i+1]
				} else {
					in = c.rest[i+1]
				}
				i++
			}
		}
	}
	switch action {
	case "export":
		if out == "" {
			out = "aether-backup-" + time.Now().Format("20060102-150405") + ".json"
		}
		raw, err := c.apiGet("backup/export")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil && filepath.Dir(out) != "." {
			return err
		}
		if err := os.WriteFile(out, raw, 0o600); err != nil {
			return err
		}
		fmt.Printf("已导出 %s（%d 字节）\n", out, len(raw))
		return nil
	case "import":
		if in == "" {
			return fmt.Errorf("缺少 --in FILE")
		}
		raw, err := os.ReadFile(in)
		if err != nil {
			return err
		}
		if _, err := c.apiPost("backup/import", json.RawMessage(raw)); err != nil {
			return err
		}
		fmt.Printf("已导入 %s（%d 字节）\n", in, len(raw))
		return nil
	}
	return fmt.Errorf("未知的 backup 动作: %s", action)
}

var _ = flag.String // 保持 flag 依赖（serve 路径仍用它）
