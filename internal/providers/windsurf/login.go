package windsurf

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 登录与刷新链路（参考 cockpit-tools windsurf_devin_oauth）：
// /_devin-auth/password/login → auth1
// WindsurfPostAuth(auth1) → session/account/org
// GetOneTimeAuthToken(session) → ott
// RegisterUser(ott) → ide_token

var (
	passwordLoginURL = "https://windsurf.com/_devin-auth/password/login"
	postAuthURL      = "https://windsurf.com/_backend/exa.seat_management_pb.SeatManagementService/WindsurfPostAuth"
	getOTTURL        = "https://windsurf.com/_backend/exa.seat_management_pb.SeatManagementService/GetOneTimeAuthToken"
	registerUserURL  = "https://register.windsurf.com/exa.seat_management_pb.SeatManagementService/RegisterUser"
)

const windsufUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36"

// LoginResult 是密码登录结果。
type LoginResult struct {
	Auth1Token string `json:"auth1_token"`
	Email      string `json:"email,omitempty"`
}

// FullRefreshResult 是四步链刷新结果。
type FullRefreshResult struct {
	Auth1Token   string
	SessionToken string
	IDEToken     string
	AccountID    string
	OrgID        string
}

// LoginWithPassword 邮箱密码登录，返回 auth1_token。
func LoginWithPassword(ctx context.Context, email, password string) (*LoginResult, error) {
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return nil, fmt.Errorf("邮箱和密码不能为空")
	}
	payload, _ := json.Marshal(map[string]string{"email": email, "password": password, "product": "Windsurf"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, passwordLoginURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Origin", "https://windsurf.com")
	req.Header.Set("Referer", "https://windsurf.com/account/login")
	req.Header.Set("User-Agent", windsufUA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Windsurf 登录请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("邮箱或密码错误")
	case http.StatusNotFound:
		return nil, fmt.Errorf("邮箱未注册")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("请求过于频繁，请稍后再试")
	default:
		return nil, fmt.Errorf("Windsurf 登录失败 (HTTP %d)", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析 Windsurf 登录响应失败: %w", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.Token), "auth1_") {
		return nil, fmt.Errorf("Windsurf 登录响应缺少 auth1_token")
	}
	return &LoginResult{Auth1Token: strings.TrimSpace(out.Token), Email: out.Email}, nil
}

// FullRefresh 用 auth1_token 跑 PostAuth→OTT→RegisterUser。
func FullRefresh(ctx context.Context, auth1 string) (*FullRefreshResult, error) {
	if err := ValidateAuth1(auth1); err != nil {
		return nil, err
	}
	session, auth1Back, accountID, orgID, err := postAuth(ctx, auth1)
	if err != nil {
		return nil, err
	}
	time.Sleep(300 * time.Millisecond)
	ott, err := getOTT(ctx, session, auth1Back, accountID, orgID)
	if err != nil {
		return nil, err
	}
	time.Sleep(300 * time.Millisecond)
	ide, err := registerUser(ctx, ott)
	if err != nil {
		return nil, err
	}
	return &FullRefreshResult{
		Auth1Token: auth1Back, SessionToken: session, IDEToken: ide,
		AccountID: accountID, OrgID: orgID,
	}, nil
}

func postAuth(ctx context.Context, auth1 string) (session, auth1Back, account, org string, err error) {
	req, err := protoRequest(ctx, postAuthURL, pbString(1, auth1))
	if err != nil {
		return "", "", "", "", err
	}
	req.Header.Set("x-devin-auth1-token", auth1)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", "", fmt.Errorf("WindsurfPostAuth 请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", "", "", fmt.Errorf("WindsurfPostAuth 返回 %d", resp.StatusCode)
	}
	fields := pbParse(raw)
	session = pbFirst(fields, 1)
	auth1Back = pbFirst(fields, 3)
	account = pbFirst(fields, 4)
	org = pbFirst(fields, 5)
	if session == "" {
		return "", "", "", "", fmt.Errorf("WindsurfPostAuth 响应缺少 session_token")
	}
	if auth1Back == "" {
		auth1Back = auth1
	}
	return session, auth1Back, account, org, nil
}

func getOTT(ctx context.Context, session, auth1, account, org string) (string, error) {
	req, err := protoRequest(ctx, getOTTURL, pbString(1, session))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-auth-token", session)
	req.Header.Set("x-devin-session-token", session)
	req.Header.Set("x-devin-auth1-token", auth1)
	req.Header.Set("x-devin-account-id", account)
	req.Header.Set("x-devin-primary-org-id", org)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GetOneTimeAuthToken 请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GetOneTimeAuthToken 返回 %d", resp.StatusCode)
	}
	ott := pbFirst(pbParse(raw), 1)
	if ott == "" {
		return "", fmt.Errorf("GetOneTimeAuthToken 响应缺少 ott")
	}
	return ott, nil
}

func registerUser(ctx context.Context, ott string) (string, error) {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		req, err := protoRequest(ctx, registerUserURL, pbString(1, ott))
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "connect-es/1.5.0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			last = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			last = fmt.Errorf("RegisterUser 返回 %d", resp.StatusCode)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if tok := findToken(raw); tok != "" {
			return tok, nil
		}
		last = fmt.Errorf("RegisterUser 响应未含 ide_token")
	}
	if last == nil {
		last = fmt.Errorf("RegisterUser 失败")
	}
	return "", last
}

func findToken(raw []byte) string {
	const needle = "devin-session-token$"
	idx := bytes.Index(raw, []byte(needle))
	if idx < 0 {
		return ""
	}
	end := idx
	for end < len(raw) && raw[end] >= 0x20 && raw[end] < 0x7f {
		end++
	}
	return string(raw[idx:end])
}

func protoRequest(ctx context.Context, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/proto")
	req.Header.Set("Accept", "application/proto")
	req.Header.Set("connect-protocol-version", "1")
	req.Header.Set("Origin", "https://windsurf.com")
	req.Header.Set("Referer", "https://windsurf.com/account/login")
	req.Header.Set("User-Agent", windsufUA)
	return req, nil
}

// pbString 编码 protobuf 的 string 字段。
func pbString(field int, value string) []byte {
	buf := make([]byte, 0, len(value)+8)
	buf = append(buf, byte(field<<3|2))
	var v [10]byte
	n := binary.PutUvarint(v[:], uint64(len(value)))
	buf = append(buf, v[:n]...)
	buf = append(buf, value...)
	return buf
}

// pbParse 解析顶层字段，只保留可变长（wire type 2）的字节串。
func pbParse(raw []byte) map[int][][]byte {
	out := map[int][][]byte{}
	for i := 0; i < len(raw); {
		tag, n := binary.Uvarint(raw[i:])
		if n <= 0 {
			break
		}
		i += n
		field := int(tag >> 3)
		wire := int(tag & 7)
		switch wire {
		case 0:
			_, n := binary.Uvarint(raw[i:])
			if n <= 0 {
				return out
			}
			i += n
		case 2:
			l, n := binary.Uvarint(raw[i:])
			if n <= 0 || i+n+int(l) > len(raw) {
				return out
			}
			i += n
			out[field] = append(out[field], raw[i:i+int(l)])
			i += int(l)
		case 1:
			i += 8
		case 5:
			i += 4
		default:
			return out
		}
	}
	return out
}

func pbFirst(fields map[int][][]byte, field int) string {
	if vals := fields[field]; len(vals) > 0 {
		return string(vals[0])
	}
	return ""
}
