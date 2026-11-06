package apppaths

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Paths is the immutable, host-provided path set for the mobile runtime.
//
// DataDir is <noBackupFilesDir>/aether, CacheDir is <filesDir>/aether/cache and
// TempDir is <cacheDir>/aether/tmp. All roots are absolute; the runtime derives
// every store path under them and never falls back to Base() or the process
// working directory. Anything that must survive a restart (settings, accounts,
// the caller key) lives under DataDir, never under CacheDir/TempDir.
type Paths struct {
	DataDir  string
	StateDB  string
	UsageDir string
	LogPath  string
	CacheDir string
	TempDir  string

	SettingsPath   string
	AccountsPath   string
	SnapshotPath   string
	MiddlewarePath string
	ClientsPath    string
	LibraryPath    string
	UsagePath      string
	ModelsDir      string
	SpecDir        string
	// CallerKeyPath 是运行时调用方密钥的持久化文件：首次启动生成、
	// 之后每次启动读取同一把（客户端把它抄进配置里，不能每次重启都变）。
	// 落在 DataDir（noBackupFilesDir）而不是 cache，避免被系统当缓存清理。
	CallerKeyPath string
}

// NewPaths validates and derives the mobile path set. Empty, relative or
// duplicate roots are rejected so a host wiring mistake cannot mix the
// no-backup, cache and temporary stores.
func NewPaths(dataDir, cacheDir, tempDir string) (Paths, error) {
	roots := []struct {
		name  string
		value string
	}{
		{"data", dataDir},
		{"cache", cacheDir},
		{"temp", tempDir},
	}
	seen := make(map[string]string, len(roots))
	for _, root := range roots {
		value := filepath.Clean(strings.TrimSpace(root.value))
		if strings.TrimSpace(root.value) == "" {
			return Paths{}, fmt.Errorf("%s path is required", root.name)
		}
		if !filepath.IsAbs(value) {
			return Paths{}, fmt.Errorf("%s path must be absolute", root.name)
		}
		if previous, ok := seen[value]; ok {
			return Paths{}, fmt.Errorf("%s and %s paths must differ", previous, root.name)
		}
		seen[value] = root.name
	}
	cleanData := filepath.Clean(strings.TrimSpace(dataDir))
	cleanCache := filepath.Clean(strings.TrimSpace(cacheDir))
	cleanTemp := filepath.Clean(strings.TrimSpace(tempDir))
	usageDir := filepath.Join(cleanData, "usage")
	return Paths{
		DataDir:  cleanData,
		StateDB:  filepath.Join(cleanData, "state.db"),
		UsageDir: usageDir,
		LogPath:  filepath.Join(cleanData, "logs", "aether.log"),
		CacheDir: cleanCache,
		TempDir:  cleanTemp,

		SettingsPath:   filepath.Join(cleanData, "settings.json"),
		AccountsPath:   filepath.Join(cleanData, "accounts.json"),
		SnapshotPath:   filepath.Join(cleanData, "state.json"),
		MiddlewarePath: filepath.Join(cleanData, "middleware.json"),
		ClientsPath:    filepath.Join(cleanData, "clients.json"),
		LibraryPath:    filepath.Join(cleanData, "library.json"),
		UsagePath:      filepath.Join(usageDir, "usage.json"),
		ModelsDir:      filepath.Join(cleanCache, "models"),
		SpecDir:        filepath.Join(cleanCache, "spec"),
		CallerKeyPath:  filepath.Join(cleanData, "caller-key"),
	}, nil
}
