package trae

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 一键切号：把账号写回 Trae / TRAE SOLO 的 storage.json。
//
// 只改 iCubeAuthInfo 条目里的 token/refreshToken/userId/expiredAt，
// 其余键（遥测、窗口状态、其他扩展数据）原样保留；auth 条目里的
// host/account 等未知字段也保留，避免把官方客户端写坏。
func (p *Provider) InjectLocalAccount(ctx context.Context, env *provider.AccountEnv) (*provider.InjectResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	token := strings.TrimSpace(env.GetCredential("token"))
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"账号缺少 token：先刷新或重新登录，再写入本机 IDE").WithProvider(p.id)
	}

	target, err := p.locateInjectTarget()
	if err != nil {
		return nil, err
	}
	auth := map[string]any{}
	if len(target.existing) > 0 {
		_ = json.Unmarshal(target.existing, &auth)
	}
	auth["token"] = token
	if v := strings.TrimSpace(env.GetCredential("refresh_token")); v != "" {
		auth["refreshToken"] = v
	}
	if v := strings.TrimSpace(env.GetCredential("user_id")); v != "" {
		auth["userId"] = v
	}
	if raw := strings.TrimSpace(env.GetCredential("expires_at")); raw != "" {
		if expiry, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			// 原文件里 expiredAt 是数字（毫秒）就继续写数字；官方客户端只认原形态。
			if _, isNum := auth["expiredAt"].(float64); isNum {
				auth["expiredAt"] = expiry.UnixMilli()
			} else {
				auth["expiredAt"] = expiry.Format(time.RFC3339)
			}
		}
	}
	authJSON, err := json.Marshal(auth)
	if err != nil {
		return nil, err
	}
	target.storage[target.key] = string(authJSON)

	raw, err := json.Marshal(target.storage)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(target.path, raw); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal,
			"写入 Trae storage.json 失败："+err.Error()).WithProvider(p.id)
	}
	return &provider.InjectResult{
		Target:      "file:" + target.path,
		RestartHint: "完全退出并重启 Trae 后生效（同一账号的旧窗口会继续用内存里的 token）。",
	}, nil
}

// traeInjectTarget 是反写落点：storage.json 路径、auth 键名、现有内容。
type traeInjectTarget struct {
	path     string
	key      string
	existing []byte
	storage  map[string]any
}

// locateInjectTarget 优先复用已经登录过的 storage.json（保留全部既有数据）；
// 全都没有时在第一个产品目录里新建。
func (p *Provider) locateInjectTarget() (*traeInjectTarget, error) {
	for _, root := range localRoots() {
		for _, name := range p.productDirs() {
			path := filepath.Join(root, name, "User", "globalStorage", "storage.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			storage := map[string]any{}
			if json.Unmarshal(raw, &storage) != nil {
				continue
			}
			key := authStorageKey
			if anyString(storage[authStorageKey]) == "" {
				for k := range storage {
					if soloDeviceKeyRe.MatchString(k) {
						key = k
						break
					}
				}
			}
			t := &traeInjectTarget{path: path, key: key, storage: storage}
			if existing, err := decodeAuthValue(anyString(storage[key])); err == nil && len(existing) > 0 {
				t.existing = existing
			}
			return t, nil
		}
	}
	roots := localRoots()
	if len(roots) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "找不到 APPDATA / Application Support 目录").
			WithProvider(p.id)
	}
	path := filepath.Join(roots[0], p.productDirs()[0], "User", "globalStorage", "storage.json")
	return &traeInjectTarget{path: path, key: authStorageKey, storage: map[string]any{}}, nil
}

// writeFileAtomic 原子写 JSON（0600），避免客户端读到半截文件。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".storage-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

var _ provider.LocalInjectCapability = (*Provider)(nil)
