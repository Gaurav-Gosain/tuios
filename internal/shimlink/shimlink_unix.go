//go:build !windows

package shimlink

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// EnsureDir creates a runtime directory, or checks an existing one: it must
// be a real directory (not a link), owned by this user, and closed to everyone
// else, since what is in it runs as this user or accepts requests from it.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user", dir)
	}
	if st.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory, which needs the execute bit to be entered
			return fmt.Errorf("%s is open to other users and could not be closed: %w", dir, err)
		}
	}
	return nil
}

// Install points <dir>/bin/<name> at exe, replacing whatever was there, and
// returns the link's path. dir and its bin directory are checked with
// EnsureDir first, so the link is never made where another user can change
// what it points at.
func Install(dir, name, exe string) (string, error) {
	bin := filepath.Join(dir, "bin")
	for _, d := range []string{dir, bin} {
		if err := EnsureDir(d); err != nil {
			return "", err
		}
	}
	link := filepath.Join(bin, name)
	if cur, err := os.Readlink(link); err == nil && cur == exe {
		return link, nil
	}
	tmp := fmt.Sprintf("%s.%d", link, os.Getpid())
	_ = os.Remove(tmp)
	if err := os.Symlink(exe, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return link, nil
}
