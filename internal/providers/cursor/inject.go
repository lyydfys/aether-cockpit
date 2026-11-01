package cursor

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 一键切号：把账号写回 Cursor 的 state.vscdb（VS Code 全局存储）。
//
// 只更新 cursorAuth/* 这几行；写前把数据库快照到 state.vscdb.aether.bak，
// 写坏可直接改名回滚。Cursor 正在运行时写入可能被内存态覆盖，因此
// 结果里明确提示重启 IDE。
func (p *Provider) InjectLocalAccount(ctx context.Context, env *provider.AccountEnv) (*provider.InjectResult, error) {
	access := strings.TrimSpace(env.GetCredential("access_token"))
	if access == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"账号缺少 access_token：先刷新或重新登录，再写入本机 IDE").WithProvider(channelID)
	}
	dbPath := cursorStateDBPath()
	if _, err := os.Stat(dbPath); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount,
			"没有找到 Cursor 的 state.vscdb：请先启动并登录一次 Cursor，或手动粘贴 token").WithProvider(channelID)
	}

	backup, err := backupSQLite(dbPath)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal,
			"备份 Cursor 数据库失败，已中止写入："+err.Error()).WithProvider(channelID)
	}

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=busy_timeout(3000)")
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "打开 Cursor 数据库失败："+err.Error()).WithProvider(channelID)
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "开启事务失败："+err.Error()).WithProvider(channelID)
	}
	upsert := func(key, value string) error {
		if value == "" {
			return nil
		}
		_, err := tx.Exec("INSERT OR REPLACE INTO ItemTable(key, value) VALUES(?1, ?2)", key, value)
		return err
	}
	authID := strings.TrimSpace(env.GetCredential("auth_id"))
	if authID == "" {
		authID = jwtSubject(access)
	}
	for _, kv := range [][2]string{
		{"cursorAuth/accessToken", access},
		{"cursorAuth/refreshToken", strings.TrimSpace(env.GetCredential("refresh_token"))},
		{"cursorAuth/cachedEmail", strings.TrimSpace(env.GetCredential("email"))},
		{"cursorAuth/authId", authID},
		{"cursorAuth/stripeMembershipType", strings.TrimSpace(env.GetCredential("membership"))},
	} {
		if err := upsert(kv[0], kv[1]); err != nil {
			_ = tx.Rollback()
			return nil, ir.NewAPIError(ir.ErrCodeInternal, "写入 "+kv[0]+" 失败："+err.Error()).WithProvider(channelID)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "提交 Cursor 登录态失败："+err.Error()).WithProvider(channelID)
	}
	return &provider.InjectResult{
		Target:      "sqlite:" + dbPath,
		Files:       []string{backup},
		RestartHint: "完全退出并重启 Cursor 后生效（运行中的 Cursor 会用内存里的旧登录态覆盖）。",
	}, nil
}

// backupSQLite 把主库与 -wal 快照到固定后缀，返回展示用路径。
// 固定后缀只保留最近一次，避免反复切号堆积大文件。
func backupSQLite(path string) (string, error) {
	backup := path + ".aether.bak"
	if err := copyFile(path, backup); err != nil {
		return "", err
	}
	if _, err := os.Stat(path + "-wal"); err == nil {
		if err := copyFile(path+"-wal", backup+"-wal"); err != nil {
			return "", err
		}
	}
	return backup, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("复制 %s 失败: %w", filepath.Base(src), err)
	}
	return out.Close()
}

var _ provider.LocalInjectCapability = (*Provider)(nil)
