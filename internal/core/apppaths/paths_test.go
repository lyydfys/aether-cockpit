package apppaths

import (
	"path/filepath"
	"testing"
)

func TestNewPathsDerivesMobilePaths(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "aether")
	cache := filepath.Join(root, "files-cache")
	temp := filepath.Join(root, "cache-tmp")

	paths, err := NewPaths(data, cache, temp)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}
	if paths.DataDir != data || paths.CacheDir != cache || paths.TempDir != temp {
		t.Fatalf("roots = %+v", paths)
	}
	if want := filepath.Join(data, "state.db"); paths.StateDB != want {
		t.Fatalf("StateDB = %q, want %q", paths.StateDB, want)
	}
	if want := filepath.Join(data, "usage"); paths.UsageDir != want {
		t.Fatalf("UsageDir = %q, want %q", paths.UsageDir, want)
	}
}

func TestNewPathsRejectsEmptyRoot(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "aether")
	cache := filepath.Join(root, "files-cache")
	temp := filepath.Join(root, "cache-tmp")
	cases := []struct {
		name  string
		data  string
		cache string
		temp  string
	}{
		{"data", "", cache, temp},
		{"cache", data, "", temp},
		{"temp", data, cache, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewPaths(tc.data, tc.cache, tc.temp); err == nil {
				t.Fatalf("NewPaths accepted empty %s root", tc.name)
			}
		})
	}
}

func TestNewPathsRejectsRelativeRoot(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "aether")
	cache := filepath.Join(root, "files-cache")
	temp := filepath.Join(root, "cache-tmp")
	cases := []struct {
		name  string
		data  string
		cache string
		temp  string
	}{
		{"data", "relative-data", cache, temp},
		{"cache", data, "relative-cache", temp},
		{"temp", data, cache, "relative-temp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewPaths(tc.data, tc.cache, tc.temp); err == nil {
				t.Fatalf("NewPaths accepted relative %s root", tc.name)
			}
		})
	}
}

func TestNewPathsRejectsDuplicateRoots(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "aether")
	cache := filepath.Join(root, "files-cache")
	temp := filepath.Join(root, "cache-tmp")
	if _, err := NewPaths(data, cache, cache); err == nil {
		t.Fatal("NewPaths accepted cache == temp")
	}
	if _, err := NewPaths(data, data, temp); err == nil {
		t.Fatal("NewPaths accepted data == cache")
	}
}
