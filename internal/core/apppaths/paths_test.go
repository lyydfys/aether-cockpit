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
	if want := filepath.Join(data, "logs", "aether.log"); paths.LogPath != want {
		t.Fatalf("LogPath = %q, want %q", paths.LogPath, want)
	}
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"SettingsPath", paths.SettingsPath, filepath.Join(data, "settings.json")},
		{"AccountsPath", paths.AccountsPath, filepath.Join(data, "accounts.json")},
		{"SnapshotPath", paths.SnapshotPath, filepath.Join(data, "state.json")},
		{"MiddlewarePath", paths.MiddlewarePath, filepath.Join(data, "middleware.json")},
		{"ClientsPath", paths.ClientsPath, filepath.Join(data, "clients.json")},
		{"LibraryPath", paths.LibraryPath, filepath.Join(data, "library.json")},
		{"UsagePath", paths.UsagePath, filepath.Join(data, "usage", "usage.json")},
		{"ModelsDir", paths.ModelsDir, filepath.Join(cache, "models")},
		{"SpecDir", paths.SpecDir, filepath.Join(cache, "spec")},
	} {
		if tc.got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
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
