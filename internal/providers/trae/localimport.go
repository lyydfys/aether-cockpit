package trae

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const authStorageKey = "iCubeAuthInfo://icube.cloudide"

var soloDeviceKeyRe = regexp.MustCompile(`^iCubeAuthInfo://icube-dc:(\d+)$`)

// productDirs 返回该区域官方客户端的 %APPDATA% 子目录名。
func (p *Provider) productDirs() []string {
	if p.region == "CN" {
		return []string{"Trae CN", "TRAE CN", "TRAE SOLO CN"}
	}
	return []string{"Trae", "TRAE", "TRAE SOLO"}
}

// localRoots 返回可能存放产品目录的根目录（Windows %APPDATA% 与 macOS Application Support）。
func localRoots() []string {
	var roots []string
	seen := map[string]bool{}
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" || seen[root] {
			return
		}
		seen[root] = true
		roots = append(roots, root)
	}

	add(os.Getenv("APPDATA"))
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, "AppData", "Roaming"))
		add(filepath.Join(home, "Library", "Application Support"))
	}
	return roots
}

// DiscoverLocalAccounts 扫描本机 Trae 客户端的 storage.json。
//
// 只返回 CredentialDraft，不打印/泄露 token；由内核负责落盘加密与 UI 脱敏。
func (p *Provider) DiscoverLocalAccounts(ctx context.Context) ([]*provider.CredentialDraft, error) {
	var drafts []*provider.CredentialDraft
	seen := map[string]bool{}

	for _, root := range localRoots() {
		for _, name := range p.productDirs() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			storagePath := filepath.Join(root, name, "User", "globalStorage", "storage.json")
			raw, err := os.ReadFile(storagePath)
			if err != nil {
				continue
			}
			draft := p.parseStorageFile(storagePath, raw)
			if draft == nil {
				continue
			}
			if key := draft.Credentials["user_id"] + "|" + draft.Credentials["token"]; seen[key] {
				continue
			} else {
				seen[key] = true
			}
			drafts = append(drafts, draft)
		}
	}

	if len(drafts) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount,
			"没有找到 Trae 的本机登录态。请确认 Trae IDE / SOLO 已登录，或在渠道页手动粘贴 token。").
			WithProvider(p.id)
	}
	return drafts, nil
}

// parseStorageFile 读取单个 storage.json 并生成凭证草稿。
func (p *Provider) parseStorageFile(path string, raw []byte) *provider.CredentialDraft {
	var storage map[string]any
	if err := json.Unmarshal(raw, &storage); err != nil {
		return nil
	}
	authRaw := anyString(storage[authStorageKey])
	if authRaw == "" {
		return nil
	}
	authJSON, err := decodeAuthValue(authRaw)
	if err != nil {
		return nil
	}
	var auth struct {
		Token        string `json:"token"`
		RefreshToken string `json:"refreshToken"`
		ExpiredAt    any    `json:"expiredAt"`
		UserID       any    `json:"userId"`
		Host         string `json:"host"`
		UserRegion   any    `json:"userRegion"`
		Account      struct {
			Username           string `json:"username"`
			NonPlainTextMobile string `json:"nonPlainTextMobile"`
		} `json:"account"`
	}
	if err := json.Unmarshal(authJSON, &auth); err != nil {
		return nil
	}
	token := strings.TrimSpace(auth.Token)
	if token == "" {
		return nil
	}

	machineID := anyString(storage["telemetry.machineId"])
	devDeviceID := anyString(storage["telemetry.devDeviceId"])
	sqmID := anyString(storage["telemetry.sqmId"])

	deviceID := ""
	for key := range storage {
		if m := soloDeviceKeyRe.FindStringSubmatch(key); m != nil && m[1] != "" {
			deviceID = m[1]
			break
		}
	}
	if deviceID == "" {
		deviceID = hashDeviceID(machineID)
	}

	region := p.region
	if p.region != "CN" {
		switch code := strings.ToUpper(anyString(auth.UserRegion)); code {
		case "CN", "SG", "US":
			region = code
		}
	}
	if obj, ok := auth.UserRegion.(map[string]any); ok {
		switch code := strings.ToUpper(anyString(obj["region"])); code {
		case "CN", "SG", "US":
			region = code
		}
	}

	userID := anyString(auth.UserID)
	label := strings.TrimSpace(auth.Account.Username)
	if label == "" {
		label = strings.TrimSpace(auth.Account.NonPlainTextMobile)
	}
	if label == "" {
		label = userID
	}
	if label == "" {
		label = "Trae"
	}
	label += " (" + region + ")"

	creds := map[string]string{
		"token":         token,
		"refresh_token": strings.TrimSpace(auth.RefreshToken),
		"user_id":       userID,
		"machine_id":    machineID,
		"dev_device_id": devDeviceID,
		"sqm_id":        sqmID,
		"device_id":     deviceID,
		"region":        region,
	}

	draft := &provider.CredentialDraft{
		Label:       label,
		Credentials: creds,
		Source:      "local_import:" + path,
	}
	if expiry := expiryFromAny(auth.ExpiredAt); !expiry.IsZero() {
		draft.Credentials["expires_at"] = expiry.Format(time.RFC3339)
		draft.ExpiresAt = expiry.Unix()
	}
	return draft
}

// decodeAuthValue 处理 auth 值的两种形态：明文 JSON 或 base64 tc 密文。
func decodeAuthValue(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") {
		return []byte(trimmed), nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var unquoted string
		if err := json.Unmarshal([]byte(trimmed), &unquoted); err == nil {
			if strings.HasPrefix(strings.TrimSpace(unquoted), "{") {
				return []byte(strings.TrimSpace(unquoted)), nil
			}
			trimmed = strings.TrimSpace(unquoted)
		}
	}
	plain, err := decryptTcStorageValue(trimmed)
	if err != nil {
		return nil, err
	}
	return []byte(plain), nil
}
