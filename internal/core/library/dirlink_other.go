//go:build !windows

package library

import "os"

// DirLink makes link a link to the folder target.
func DirLink(target, link string) error { return os.Symlink(target, link) }

// IsDirLink reports whether path itself is a symlink.
func IsDirLink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}
