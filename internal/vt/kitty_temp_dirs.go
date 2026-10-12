package vt

import (
	"os"
	"path/filepath"
	"strings"
)

// The places a kitty file medium may name. The client's passthrough and the
// emulator's answer to an upload both apply them, so they live here.

// kittySensitiveDirs are the places no file medium may name. /dev/shm is the one
// part of /dev that holds image data, and it is allowed.
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

// KittyTempMarker is the text the kitty spec asks for in a t=t file name.
const KittyTempMarker = "tty-graphics-protocol"

// KittyFixedTempDirs are the temporary directories on every unix. A variable
// so a test, whose own home is under /tmp, can take them away.
var KittyFixedTempDirs = []string{"/tmp", "/dev/shm", "/var/tmp"}

// KittyTempDirs lists the temporary directories: the fixed ones and TMPDIR.
// TMPDIR is ignored when it is /, the home directory or a parent of it, since
// that would make every file of the user a temporary one. Inside the home
// directory it counts only under ~/tmp or ~/.tmp.
func KittyTempDirs() []string {
	dirs := append([]string(nil), KittyFixedTempDirs...)
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
		// Inside the home directory, only ~/tmp and ~/.tmp count. Anywhere
		// else there (~/.ssh, ~/.config) holds the user's own files.
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
