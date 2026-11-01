package wsl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Ubuntu-22.04":   "ubuntu-22-04",
		"Ubuntu":         "ubuntu",
		"Debian":         "debian",
		"docker-desktop": "docker-desktop",
		"  kali linux ":  "kali-linux",
		"":               "",
		"!!!":            "",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHomesOverride(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home", "u")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AETHER_WSL_HOMES", home)
	t.Setenv("AETHER_WSL_DISTRO", "Ubuntu-22.04")
	t.Cleanup(func() { Invalidate() })
	Invalidate()

	homes := Homes()
	if len(homes) != 1 {
		t.Fatalf("Homes() = %d 条，want 1：%+v", len(homes), homes)
	}
	if homes[0].Distro != "Ubuntu-22.04" {
		t.Errorf("Distro = %q，want Ubuntu-22.04", homes[0].Distro)
	}
	if homes[0].Path != home {
		t.Errorf("Path = %q，want %q", homes[0].Path, home)
	}
}

func TestHomesOverrideInfersDistro(t *testing.T) {
	t.Setenv("AETHER_WSL_HOMES", `//wsl.localhost/Ubuntu-22.04/home/alice`+string(os.PathListSeparator)+`//wsl.localhost/Debian/home/bob`)
	t.Setenv("AETHER_WSL_DISTRO", "")
	t.Cleanup(func() { Invalidate() })
	Invalidate()

	homes := Homes()
	if len(homes) != 2 {
		t.Fatalf("Homes() = %d 条，want 2", len(homes))
	}
	if homes[0].Distro != "Ubuntu-22.04" || homes[1].Distro != "Debian" {
		t.Errorf("发行版推断错误：%+v", homes)
	}
}

func TestDistroFromPathFallsBack(t *testing.T) {
	t.Setenv("AETHER_WSL_DISTRO", "")
	if got := distroFromPath(`C:\Users\me`, "WSL"); got != "WSL" {
		t.Errorf("distroFromPath 普通目录 = %q，want WSL", got)
	}
	if got := distroFromPath(`//wsl$/Ubuntu/home/u`, "WSL"); got != "Ubuntu" {
		t.Errorf("distroFromPath 回退前缀 = %q，want Ubuntu", got)
	}
}
