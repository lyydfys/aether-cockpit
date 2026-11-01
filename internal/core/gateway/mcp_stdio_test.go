package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMCPStdioHelper 是被 checkMCPStdio 启动的子进程：按行读 JSON-RPC 并回应。
func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	if os.Getenv("GO_WANT_MCP_EXIT") == "1" {
		os.Exit(3)
	}
	if os.Getenv("GO_WANT_MCP_SLEEP") == "1" {
		time.Sleep(2 * time.Second)
	}
	sc := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var msg struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{}})
		case "tools/list":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{
				"tools": []any{map[string]any{"name": "a"}, map[string]any{"name": "t"}},
			}})
		}
	}
}

func TestCheckMCPStdio(t *testing.T) {
	helper := []string{"-test.run=TestMCPStdioHelper"}
	baseEnv := map[string]string{"GO_WANT_MCP_HELPER": "1"}

	res := checkMCPStdio(context.Background(), os.Args[0], helper, baseEnv)
	if res.State != "ok" || res.Tools != 2 || len(res.ToolList) != 2 {
		t.Fatalf("ok 路径 = %+v", res)
	}

	res = checkMCPStdio(context.Background(), "definitely-not-a-cmd-xyz", nil, nil)
	if res.Why != "notfound" {
		t.Fatalf("notfound = %+v", res)
	}

	res = checkMCPStdio(context.Background(), os.Args[0], append(append([]string{}, helper...), "${MCP_MISSING_VAR_XYZ}"), baseEnv)
	if res.Why != "novar" || !strings.Contains(res.Detail, "MCP_MISSING_VAR_XYZ") {
		t.Fatalf("novar = %+v", res)
	}

	res = checkMCPStdio(context.Background(), os.Args[0], helper, map[string]string{
		"GO_WANT_MCP_HELPER": "1", "GO_WANT_MCP_EXIT": "1",
	})
	if res.Why != "exited" {
		t.Fatalf("exited = %+v", res)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res = checkMCPStdio(ctx, os.Args[0], helper, map[string]string{
		"GO_WANT_MCP_HELPER": "1", "GO_WANT_MCP_SLEEP": "1",
	})
	if res.Why == "start" && resourceStarved(res.Detail) {
		// 这台机器在跑全量测试时会把提交内存打满，fork 自身都失败
		// （"The paging file is too small"）。那是环境上限，不是超时逻辑
		// 的缺陷：按 NOT-RUN 处理，别报成失败。
		t.Skipf("系统资源不足，无法 fork 子进程：%s", res.Detail)
	}
	if res.Why != "timeout" {
		t.Fatalf("timeout = %+v", res)
	}
}

// resourceStarved 判断进程启动失败是不是因为系统内存/分页文件不足。
func resourceStarved(detail string) bool {
	for _, s := range []string{"paging file is too small", "not enough memory", "Not enough memory resources", "commitment limit"} {
		if strings.Contains(strings.ToLower(detail), strings.ToLower(s)) {
			return true
		}
	}
	return false
}

func TestResourceStarved(t *testing.T) {
	if !resourceStarved("fork/exec x.exe: The paging file is too small for this operation to complete.") {
		t.Fatal("应识别分页文件不足")
	}
	if resourceStarved("exit status 3") {
		t.Fatal("普通退出不应算资源不足")
	}
}
