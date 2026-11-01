package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func TestBuildInjectPayloadShape(t *testing.T) {
	if !(&Provider{}).Manifest().Capabilities.LocalInject {
		t.Fatal("Antigravity Manifest 应声明 local_inject")
	}
	if _, ok := interface{}(&Provider{}).(provider.LocalInjectCapability); !ok {
		t.Fatal("Antigravity 应实现 LocalInjectCapability")
	}
	creds := map[string]string{
		"access_token":  "ya29.test",
		"refresh_token": "1//refresh",
		"id_token":      "header.payload.sig",
		"token_type":    "Bearer",
		"expires_at":    "2026-10-30T09:00:00.123456789Z",
	}
	raw, err := buildInjectPayload(creds, time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("buildInjectPayload: %v", err)
	}
	var blob struct {
		Token struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			TokenType    string `json:"token_type"`
			Expiry       string `json:"expiry"`
		} `json:"token"`
		AuthMethod string `json:"auth_method"`
		IDToken    string `json:"id_token"`
	}
	if err := json.Unmarshal(raw, &blob); err != nil {
		t.Fatalf("payload 不是合法 JSON: %v", err)
	}
	if blob.Token.AccessToken != "ya29.test" || blob.Token.RefreshToken != "1//refresh" {
		t.Fatalf("token 字段错误: %+v", blob.Token)
	}
	if blob.Token.TokenType != "Bearer" || blob.AuthMethod != "consumer" || blob.IDToken != "header.payload.sig" {
		t.Fatalf("token_type/auth_method/id_token 错误: %+v", blob)
	}
	if blob.Token.Expiry != "2026-10-30T09:00:00.123456789Z" {
		t.Fatalf("expiry 未按官方格式输出: %q", blob.Token.Expiry)
	}

	// 缺 expires_at 时必须补一个保守过期时间，不能让 IDE 认为永不过期。
	raw, err = buildInjectPayload(map[string]string{"access_token": "a"}, time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("buildInjectPayload(无过期时间): %v", err)
	}
	var bare struct {
		Token struct {
			RefreshToken string `json:"refresh_token"`
			Expiry       string `json:"expiry"`
		} `json:"token"`
	}
	if err := json.Unmarshal(raw, &bare); err != nil {
		t.Fatal(err)
	}
	if bare.Token.Expiry != "2026-10-30T09:00:00Z" {
		t.Fatalf("缺省 expiry = %q", bare.Token.Expiry)
	}
	if bare.Token.RefreshToken != "" {
		t.Fatalf("没有 refresh_token 时不应写入: %q", bare.Token.RefreshToken)
	}

	if _, err := buildInjectPayload(map[string]string{"refresh_token": "1//r"}, time.Now()); err == nil {
		t.Fatal("缺 access_token 时应报错")
	}
}

func TestInjectLocalAccountWritesBothTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	var captured []byte
	prev := writeCredentialFn
	writeCredentialFn = func(b []byte) error {
		captured = append([]byte(nil), b...)
		return nil
	}
	t.Cleanup(func() { writeCredentialFn = prev })

	env := &provider.AccountEnv{
		AccountID:  "acc-1",
		ProviderID: "antigravity",
		Credentials: map[string]string{
			"access_token":  "ya29.live",
			"refresh_token": "1//live",
			"expires_at":    time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	res, err := (&Provider{}).InjectLocalAccount(context.Background(), env)
	if err != nil {
		t.Fatalf("InjectLocalAccount: %v", err)
	}
	if res.Target != "windows-credential:gemini:antigravity" {
		t.Fatalf("target = %q", res.Target)
	}
	if len(res.Warnings) != 0 || len(res.Files) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if len(captured) == 0 {
		t.Fatal("凭据管理器没有收到写入")
	}

	file := filepath.Join(home, ".gemini", "jetski-standalone-oauth-token")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("回退文件不存在: %v", err)
	}
	if string(data) != string(captured) {
		t.Fatal("凭据管理器与回退文件内容不一致")
	}
	if d := parseLocalCredential(data); d == nil || d.Credentials["access_token"] != "ya29.live" {
		t.Fatal("写出的内容无法被本机导入解析回来")
	}
}

func TestInjectLocalAccountFallbackWhenCredentialWriteFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	prev := writeCredentialFn
	writeCredentialFn = func([]byte) error { return errors.New("access denied") }
	t.Cleanup(func() { writeCredentialFn = prev })

	res, err := (&Provider{}).InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:   "acc-2",
		ProviderID:  "antigravity",
		Credentials: map[string]string{"access_token": "ya29.x"},
	})
	if err != nil {
		t.Fatalf("凭据管理器失败但回退文件成功时不应报错: %v", err)
	}
	if len(res.Warnings) != 1 || len(res.Files) != 1 {
		t.Fatalf("res = %+v", res)
	}
}

func TestInjectLocalAccountBothFail(t *testing.T) {
	// 把 HOME 指向一个普通文件，让 ~/.gemini 目录创建失败。
	broken := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(broken, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", broken)
	t.Setenv("HOME", broken)

	prev := writeCredentialFn
	writeCredentialFn = func([]byte) error { return errors.New("access denied") }
	t.Cleanup(func() { writeCredentialFn = prev })

	if _, err := (&Provider{}).InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:   "acc-3",
		ProviderID:  "antigravity",
		Credentials: map[string]string{"access_token": "ya29.x"},
	}); err == nil {
		t.Fatal("两个落点都失败时应返回错误")
	}
}
