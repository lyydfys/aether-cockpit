package spec

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxRemoteBytes 远程 spec 的体量上限。它只是一堆参数，1MB 已经极端宽松。
const maxRemoteBytes = 1 << 20

// SetRemote 设置远程 spec 地址。传空字符串表示禁用远程更新。
//
// 安全约束：远程 spec 能改 base_url，也就意味着它能决定凭证发往哪里。
// 因此非 HTTPS 一律拒绝（localhost 除外，便于本地联调）。
func (s *Store) SetRemote(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)

	s.mu.Lock()
	defer s.mu.Unlock()

	if rawURL == "" {
		s.remoteURL = ""
		return nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("spec: 远程地址无法解析: %w", err)
	}
	if u.Scheme != "https" {
		host := u.Hostname()
		isLocal := host == "localhost" || host == "127.0.0.1" || host == "::1"
		if !isLocal {
			return fmt.Errorf("spec: 远程地址必须使用 HTTPS（收到 %q）", u.Scheme)
		}
	}
	s.remoteURL = rawURL
	return nil
}

// Fetch 拉取一次远程 spec。
func (s *Store) Fetch(ctx context.Context) (int, error) {
	s.mu.RLock()
	raw := s.remoteURL
	s.mu.RUnlock()

	if raw == "" {
		return 0, nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, raw, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "aether-cockpit/spec-updater")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.markFetch(err)
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("spec: 远程返回 %d", resp.StatusCode)
		s.markFetch(err)
		return 0, err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteBytes))
	if err != nil {
		s.markFetch(err)
		return 0, err
	}

	ids, err := s.applyPayload(body, "remote:"+raw)
	if err != nil {
		s.markFetch(err)
		return 0, err
	}
	s.markFetch(nil)
	return len(ids), nil
}

func (s *Store) markFetch(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastFetchAt = time.Now()
	s.fetchCount++
	if err != nil {
		s.lastError = err.Error()
	} else {
		s.lastError = ""
	}
}

// StartAutoUpdate 启动周期拉取，返回停止函数。
func (s *Store) StartAutoUpdate(ctx context.Context, interval time.Duration) func() {
	s.mu.RLock()
	enabled := s.remoteURL != ""
	s.mu.RUnlock()

	if !enabled || interval <= 0 {
		return func() {}
	}

	stop := make(chan struct{})

	go func() {
		// 启动后先拉一次，避免必须等一个周期。
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
		_, _ = s.Fetch(ctx)

		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-t.C:
				_, _ = s.Fetch(ctx)
			}
		}
	}()

	var closed bool
	return func() {
		if closed {
			return
		}
		closed = true
		close(stop)
	}
}
