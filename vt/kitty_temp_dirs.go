package vt

import (
	"os"
	"path/filepath"
	"strings"
)

var kittySensitiveDirs = []string{"/proc", "/sys", "/dev"}

// KittySensitivePath reports whether path lies in a place no file medium may
// name.
func KittySensitivePath(path string) bool {
	if path == "/dev/shm" || strings.HasPrefix(path, "/dev/shm/") {
		return false
	}
	for _, dir := range kittySensitiveDirs {
		if path == dir || strings.HasPrefix(path, dir+"/") {
			return true
		}
	}
	return false
}

// KittyTempMarker is the text the kitty spec asks for in a t=t file name. It
// is for a host that reads such files itself.
const KittyTempMarker = "tty-graphics-protocol"

var kittyFixedTempDirs = []string{"/tmp", "/dev/shm", "/var/tmp"}

// KittyTempDirs lists the temporary directories: the fixed ones and TMPDIR.
// TMPDIR is ignored when it is /, the home directory or a parent of it, since
// that would make every file of the user a temporary one. Inside the home
// directory it counts only under ~/tmp or ~/.tmp.
func KittyTempDirs() []string {
	dirs := append([]string(nil), kittyFixedTempDirs...)
	tmp := os.Getenv("TMPDIR")
	if tmp == "" || !filepath.IsAbs(tmp) {
		return dirs
	}
	tmp = filepath.Clean(tmp)
	if tmp == "/" {
		return dirs
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		home = filepath.Clean(home)
		if r, err := filepath.EvalSymlinks(home); err == nil {
			home = r
		}
		t := tmp
		if r, err := filepath.EvalSymlinks(t); err == nil {
			t = r
		}
		if t == home || strings.HasPrefix(home, strings.TrimSuffix(t, "/")+"/") {
			return dirs
		}
		if rel, err := filepath.Rel(home, t); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			first, _, _ := strings.Cut(rel, "/")
			if first != "tmp" && first != ".tmp" {
				return dirs
			}
		}
	}
	return append(dirs, tmp)
}

// KittyInTempDir reports whether path lies in a temporary directory: /tmp,
// /var/tmp, /dev/shm, or the one TMPDIR names.
func KittyInTempDir(path string) bool {
	for _, dir := range KittyTempDirs() {
		if dir == "" {
			continue
		}
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			dir = r
		}
		if strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/") {
			return true
		}
	}
	return false
}
