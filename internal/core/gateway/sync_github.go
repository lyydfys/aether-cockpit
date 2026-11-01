package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitHub 仓库同步（Contents API）：一份备份一个文件，按需建提交。
// 变量便于测试指向本地假服务。
var githubAPIBase = "https://api.github.com"

func githubPath(cfg *SyncGitHubConfig, filename string) string {
	p := strings.Trim(cfg.Path, "/")
	if p == "" {
		return filename
	}
	return p + "/" + filename
}

func githubContentURL(cfg *SyncGitHubConfig, path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	u := githubAPIBase + "/repos/" + strings.Trim(cfg.Repo, "/") + "/contents/" + strings.Join(parts, "/")
	if cfg.Branch != "" {
		u += "?ref=" + url.QueryEscape(cfg.Branch)
	}
	return u
}

func githubHeaders(cfg *SyncGitHubConfig) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+cfg.Token)
	h.Set("Accept", "application/vnd.github+json")
	h.Set("X-GitHub-Api-Version", "2022-11-28")
	h.Set("User-Agent", "aether-cockpit")
	return h
}

func githubPutObject(cfg *SyncGitHubConfig, filename string, data []byte) (string, error) {
	if strings.TrimSpace(cfg.Repo) == "" || !strings.Contains(cfg.Repo, "/") {
		return "", fmt.Errorf("GitHub 需要 owner/repo")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return "", fmt.Errorf("GitHub 需要具有 Contents 读写权限的 token")
	}
	path := githubPath(cfg, filename)
	client := &http.Client{Timeout: 60 * time.Second}

	// 已有文件要带 sha 才能更新。
	var sha string
	getReq, _ := http.NewRequest(http.MethodGet, githubContentURL(cfg, path), nil)
	getReq.Header = githubHeaders(cfg)
	if resp, err := client.Do(getReq); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var meta struct {
				SHA string `json:"sha"`
			}
			_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&meta)
			sha = meta.SHA
		} else if resp.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
			return "", fmt.Errorf("GitHub 读取失败: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
	} else {
		return "", fmt.Errorf("GitHub 连接失败: %w", err)
	}

	payload := map[string]any{
		"message": "Aether Cockpit backup " + time.Now().UTC().Format(time.RFC3339),
		"content": base64.StdEncoding.EncodeToString(data),
	}
	if cfg.Branch != "" {
		payload["branch"] = cfg.Branch
	}
	if sha != "" {
		payload["sha"] = sha
	}
	body, _ := json.Marshal(payload)
	putURL := githubAPIBase + "/repos/" + strings.Trim(cfg.Repo, "/") + "/contents/" + escapePath(path)
	req, _ := http.NewRequest(http.MethodPut, putURL, bytes.NewReader(body))
	req.Header = githubHeaders(cfg)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub 上传失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return "", fmt.Errorf("GitHub 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return "github://" + cfg.Repo + "/" + path, nil
}

func escapePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func githubGetObject(cfg *SyncGitHubConfig, filename string) ([]byte, string, error) {
	path := githubPath(cfg, filename)
	client := &http.Client{Timeout: 60 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, githubContentURL(cfg, path), nil)
	req.Header = githubHeaders(cfg)
	req.Header.Set("Accept", "application/vnd.github.raw")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("GitHub 连接失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GitHub 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body[:minInt(len(body), 300)])))
	}
	return body, "github://" + cfg.Repo + "/" + path, nil
}
