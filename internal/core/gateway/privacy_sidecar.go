package gateway

import (
	"aether-cockpit/internal/core/executil"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// SidecarConfig 是 PII 边车的配置。
//
// 为什么是"外部进程"而不是内置模型：单 exe 约束下打包 GGUF 权重会让产物
// 膨胀到数百 MB，而且模型与推理后端（llama.cpp / ONNX）版本强耦合。
// 这里只约定一个极简的行协议，模型由用户自带（任意语言实现均可）。
//
// 协议：stdin 每行一个 JSON 请求 {"text":"..."}，stdout 每行一个响应
// {"text":"..."}（成功）或 {"error":"..."}（失败）。空行忽略。
type SidecarConfig struct {
	Command        string   `json:"command"`
	Args           []string `json:"args,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	// Enabled 是设置页的 on/off（nil=开）。关掉只是不启动边车，
	// 命令保留，下次打开不用重填。
	Enabled *bool `json:"enabled,omitempty"`
}

// sidecarOn 判断边车是否被用户打开（未设置视为开）。
func sidecarOn(cfg *SidecarConfig) bool {
	if cfg == nil || strings.TrimSpace(cfg.Command) == "" {
		return false
	}
	if cfg.Enabled == nil {
		return true
	}
	return *cfg.Enabled
}

// sidecarClient 串行驱动一个常驻子进程。
//
// 串行是刻意的：PII 模型通常单实例、显存有限，并发请求只会互相拖慢；
// 隐私遮蔽本来就在请求关键路径上，多等几毫秒换取确定性更划算。
type sidecarClient struct {
	cfg    SidecarConfig
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
}

func newSidecarClient(cfg SidecarConfig) (*sidecarClient, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("边车 command 不能为空")
	}
	c := &sidecarClient{cfg: cfg}
	if err := c.start(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *sidecarClient) start() error {
	cmd := exec.Command(c.cfg.Command, c.cfg.Args...)
	executil.Hide(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// stderr 必须排空：管道写满会让子进程阻塞在写日志上。
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动边车失败: %w", err)
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Printf("[PII] %s", sc.Text())
		}
	}()
	c.cmd = cmd
	c.stdin = stdin
	c.reader = bufio.NewReaderSize(stdout, 64<<10)
	return nil
}

func (c *sidecarClient) timeout() time.Duration {
	if c.cfg.TimeoutSeconds > 0 {
		return time.Duration(c.cfg.TimeoutSeconds) * time.Second
	}
	return 5 * time.Second
}

// Mask 把一段文本交给边车做实体替换。
// 第二个返回值是"占位符 -> 原文"的映射，用于在回复流里还原。
func (c *sidecarClient) Mask(ctx context.Context, text string) (string, map[string]string, error) {
	if strings.TrimSpace(text) == "" {
		return text, nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	type req struct {
		Text string `json:"text"`
	}
	type resp struct {
		Text    string            `json:"text"`
		Mapping map[string]string `json:"mapping,omitempty"`
		Error   string            `json:"error"`
	}

	var mapping map[string]string
	if c.stdin == nil {
		if err := c.start(); err != nil {
			return "", nil, err
		}
	}
	payload, _ := json.Marshal(req{Text: text})
	payload = append(payload, '\n')

	done := make(chan error, 1)
	go func() {
		if _, err := c.stdin.Write(payload); err != nil {
			done <- fmt.Errorf("写入边车失败: %w", err)
			return
		}
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			done <- fmt.Errorf("读取边车响应失败: %w", err)
			return
		}
		var r resp
		if err := json.Unmarshal(line, &r); err != nil {
			done <- fmt.Errorf("边车响应不是合法 JSON: %w", err)
			return
		}
		if r.Error != "" {
			done <- fmt.Errorf("边车返回错误: %s", r.Error)
			return
		}
		text, mapping = r.Text, r.Mapping
		done <- nil
	}()

	select {
	case <-ctx.Done():
		c.kill()
		return "", nil, fmt.Errorf("边车调用已取消: %w", ctx.Err())
	case err := <-done:
		if err != nil {
			c.kill() // 协议错乱后进程状态不可信，杀掉下次重建
			return "", nil, err
		}
		return text, mapping, nil
	case <-time.After(c.timeout()):
		c.kill()
		return "", nil, fmt.Errorf("边车响应超时（%s），已终止进程", c.timeout())
	}
}

func (c *sidecarClient) kill() {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	c.cmd = nil
	c.stdin = nil
	c.reader = nil
}

// Close 结束常驻进程（设置重建时调用）。
func (c *sidecarClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kill()
}
