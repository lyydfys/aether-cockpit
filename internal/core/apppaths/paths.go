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
// StateDB and UsageDir under DataDir and never falls back to Base() or the
// process working directory.
type Paths struct {
	DataDir  string
	StateDB  string
	UsageDir string
	CacheDir string
	TempDir  string
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
	return Paths{
		DataDir:  cleanData,
		StateDB:  filepath.Join(cleanData, "state.db"),
		UsageDir: filepath.Join(cleanData, "usage"),
		CacheDir: cleanCache,
		TempDir:  cleanTemp,
	}, nil
}
