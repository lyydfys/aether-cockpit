package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func backupTestGateway(t *testing.T) *Gateway {
	t.Helper()
	dir := t.TempDir()
	return New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		SettingsPath:     filepath.Join(dir, "settings.json"),
	})
}

type backupExportResp struct {
	Data      string `json:"data"`
	Path      string `json:"path"`
	Bytes     int    `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	Error     string `json:"error"`
}

// TestBackupExportInlineReturnsSealedBase64 是移动端的前置条件：
// 手机上没有 Downloads 目录可写、也没有文件管理器路径，所以导出必须能把
// **密封后的密文**直接回给客户端（base64），由客户端自己落盘再分享。
// 关键约束：返回的密文必须能被现有的 openBackup 与 /api/backup/import 解开，
// 否则就是造了一个只属于手机端的私货格式。
func TestBackupExportInlineReturnsSealedBase64(t *testing.T) {
	g := backupTestGateway(t)
	const pass = "correct-horse-battery-staple"

	rec := httptest.NewRecorder()
	g.handleBackupExportFile(rec, httptest.NewRequest(
		http.MethodPost, "/api/backup/export", strings.NewReader(`{"pass":"`+pass+`","inline":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out backupExportResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v %s", err, rec.Body.String())
	}
	if out.Data == "" {
		t.Fatalf("inline 导出必须返回 data，实际: %s", rec.Body.String())
	}
	if out.Path != "" {
		t.Fatalf("inline 导出不该写文件，实际 path=%q", out.Path)
	}

	blob, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil {
		t.Fatalf("data 不是合法 base64: %v", err)
	}
	if !bytes.HasPrefix(blob, []byte(sealMagicV2)) {
		t.Fatalf("密文必须以 %q 开头（现有密封格式），实际前缀 %q", sealMagicV2, blob[:min(7, len(blob))])
	}
	// 与桌面导出同一种密封：同一个口令能解开，错口令必须解不开。
	plain, err := openBackup(blob, pass)
	if err != nil {
		t.Fatalf("用导出时的口令解不开: %v", err)
	}
	if _, err := openBackup(blob, pass+"-wrong"); err == nil {
		t.Fatal("错误口令竟然解开了：密封参数被削弱了")
	}
	var bundle backupBundle
	if err := json.Unmarshal(plain, &bundle); err != nil {
		t.Fatalf("解出的明文不是备份 bundle: %v", err)
	}
	if bundle.Note == "" {
		t.Fatal("备份 bundle 缺少 note（说明这是真实 bundle 而不是空对象）")
	}

	// 再把这份密文喂回真实的导入 handler，确认跨端可用。
	importBody, _ := json.Marshal(map[string]any{"data": out.Data, "pass": pass})
	rec = httptest.NewRecorder()
	g.handleBackupImport(rec, httptest.NewRequest(http.MethodPost, "/api/backup/import", bytes.NewReader(importBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("导入 inline 导出的密文失败: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestBackupExportWithoutInlineKeepsWritingAFile 保证桌面端默认行为不变：
// 不传 inline 时仍然落盘并回 {path}。
func TestBackupExportWithoutInlineKeepsWritingAFile(t *testing.T) {
	g := backupTestGateway(t)
	rec := httptest.NewRecorder()
	g.handleBackupExportFile(rec, httptest.NewRequest(
		http.MethodPost, "/api/backup/export", strings.NewReader(`{"pass":"another-passphrase"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out backupExportResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if out.Path == "" {
		t.Fatalf("非 inline 模式必须写文件并返回 path，实际: %s", rec.Body.String())
	}
	if out.Data != "" {
		t.Fatal("非 inline 模式不应返回 data（避免把整份备份塞进响应体）")
	}
	if st, err := os.Stat(out.Path); err != nil || st.Size() == 0 {
		t.Fatalf("返回的 path 不可用: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(out.Path) })
}

// TestBackupExportInlineStillRequiresPassphrase inline 不是"免口令"的后门。
func TestBackupExportInlineStillRequiresPassphrase(t *testing.T) {
	g := backupTestGateway(t)
	rec := httptest.NewRecorder()
	g.handleBackupExportFile(rec, httptest.NewRequest(
		http.MethodPost, "/api/backup/export", strings.NewReader(`{"inline":true}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空口令必须 400，实际 %d body=%s", rec.Code, rec.Body.String())
	}
}
