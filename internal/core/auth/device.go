package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

// deviceCodeResponse 是设备码申请端点的响应。
type deviceCodeResponse struct {
	DeviceCode       string `json:"device_code"`
	UserCode         string `json:"user_code"`
	VerificationURI  string `json:"verification_uri"`
	VerificationURL  string `json:"verification_url"` // 部分服务用这个名字
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn        int    `json:"expires_in"`
	Interval         int    `json:"interval"`
}

// StartDeviceCode 启动 OAuth 设备码流程（RFC 8628）。
//
// 适用于无法起本地回调的场景（如 AWS Builder ID 这类 OIDC 提供方）。
// 返回的 Session 里带 user_code 与 verification_uri，前端展示给用户；
// 后台协程按 interval 自动轮询，成功后 Session 变成 success。
func (c *Coordinator) StartDeviceCode(providerID string, cfg *provider.OAuthConfig) (*Session, error) {
	if cfg == nil || cfg.DeviceCodeURL == "" || cfg.TokenURL == "" || cfg.ClientID == "" {
		return nil, errors.New("auth: 设备码流程需要 device_code_url / token_url / client_id")
	}

	// ① 申请设备码
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	if len(cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(cfg.Scopes, " "))
	}

	req, err := http.NewRequest(http.MethodPost, cfg.DeviceCodeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: 申请设备码失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: 设备码端点返回 %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var dc deviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return nil, fmt.Errorf("auth: 设备码响应不是合法 JSON: %w", err)
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return nil, fmt.Errorf("auth: 设备码响应缺少必要字段: %s", truncate(string(body), 200))
	}

	verifyURI := dc.VerificationURI
	if verifyURI == "" {
		verifyURI = dc.VerificationURL
	}
	if dc.VerificationURIComplete != "" {
		// 完整链接自带 user_code，用户点开就能直接确认，体验更好。
		verifyURI = dc.VerificationURIComplete
	}

	interval := dc.Interval
	if interval <= 0 {
		interval = 5 // RFC 8628 建议默认 5 秒
	}
	expiresIn := dc.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 600
	}

	s := &Session{
		ID:              newSessionID(),
		ProviderID:      providerID,
		Method:          provider.AuthDeviceCode,
		Status:          StatusPending,
		UserCode:        dc.UserCode,
		VerificationURI: verifyURI,
		Interval:        interval,
		CreatedAt:       time.Now(),
		ExpiresAt:       time.Now().Add(time.Duration(expiresIn) * time.Second),
	}
	c.register(s)

	// ② 后台轮询
	go c.pollDeviceToken(s, cfg, dc.DeviceCode, time.Duration(interval)*time.Second)

	return s, nil
}

// pollDeviceToken 按 RFC 8628 轮询 token 端点。
//
// 三种非终态错误必须区分处理：
//   - authorization_pending：用户还没确认，继续等
//   - slow_down：轮询太快了，服务端要求降频（按规范加 5 秒）
//   - 其他：视为失败
func (c *Coordinator) pollDeviceToken(s *Session, cfg *provider.OAuthConfig, deviceCode string, interval time.Duration) {
	ctx, cancel := context.WithDeadline(context.Background(), s.ExpiresAt)
	defer cancel()

	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			c.finish(s, nil, errors.New("设备码已过期，请重新发起登录"))
			return
		case <-timer.C:
		}

		form := url.Values{}
		form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		form.Set("device_code", deviceCode)
		form.Set("client_id", cfg.ClientID)
		if cfg.ClientSecret != "" {
			form.Set("client_secret", cfg.ClientSecret)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
		if err != nil {
			c.finish(s, nil, err)
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			// 网络抖动不算失败，继续轮询直到超时。
			timer.Reset(interval)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			cred, perr := parseTokenResponse(body, cfg)
			if perr != nil {
				c.finish(s, nil, perr)
				return
			}
			cred.Source = "oauth_device_code"
			c.finish(s, cred, nil)
			return
		}

		// 解析 OAuth 错误码
		var errBody struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &errBody)

		switch errBody.Error {
		case "authorization_pending":
			// 正常等待
		case "slow_down":
			interval += 5 * time.Second
		case "expired_token":
			c.finish(s, nil, errors.New("设备码已过期，请重新发起登录"))
			return
		case "access_denied":
			c.finish(s, nil, errors.New("用户拒绝了授权"))
			return
		default:
			c.finish(s, nil, fmt.Errorf("token 端点返回 %d: %s %s",
				resp.StatusCode, errBody.Error, errBody.Description))
			return
		}

		timer.Reset(interval)
	}
}

// RefreshToken 用 refresh_token 换新的 access_token。
//
// 供 TokenRefreshCapability 的实现与定时刷新复用。
func (c *Coordinator) RefreshToken(cfg *provider.OAuthConfig, refreshToken string) (*provider.CredentialDraft, error) {
	if cfg == nil || cfg.TokenURL == "" || cfg.ClientID == "" {
		return nil, errors.New("auth: 刷新需要 token_url / client_id")
	}
	if refreshToken == "" {
		return nil, errors.New("auth: 没有 refresh_token，需要重新登录")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", cfg.ClientID)
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}

	req, err := http.NewRequest(http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: 刷新请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: 刷新返回 %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return parseTokenResponse(body, cfg)
}
