//go:build !windows

package session

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// GetSocketPath returns the path to the daemon socket.
func GetSocketPath() (string, error) {
	// Use XDG_RUNTIME_DIR if available (preferred for sockets)
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir != "" {
		socketDir := filepath.Join(runtimeDir, "tuios")
		if err := ensureSocketDir(socketDir); err != nil {
			return "", err
		}
		return filepath.Join(socketDir, "tuios.sock"), nil
	}

	// Fallback to /tmp/tuios-$UID/
	uid := os.Getuid()
	socketDir := filepath.Join("/tmp", fmt.Sprintf("tuios-%d", uid))
	if err := ensureSocketDir(socketDir); err != nil {
		return "", err
	}
	return filepath.Join(socketDir, "tuios.sock"), nil
}

// GetPidFilePath returns the path to the daemon PID file.
func GetPidFilePath() (string, error) {
	socketPath, err := GetSocketPath()
	if err != nil {
		return "", err
	}
	return socketPath + ".pid", nil
}

// ensureSocketDir creates the directory the daemon's socket and pid file live
// in, or checks the one that is there. Whoever controls this directory can put
// their own socket in place of the daemon's and read what every client sends,
// and without XDG_RUNTIME_DIR its name, /tmp/tuios-<uid>, is one any local
// user can create first. So an existing directory must be a real directory
// (not a link), owned by this user, and closed to everyone else. One this user
// owns that is open is closed rather than refused.
func ensureSocketDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("failed to check socket directory: %w", err)
	}
	if !st.Mode().IsDir() {
		return fmt.Errorf("socket directory %s is not a directory; remove it or set XDG_RUNTIME_DIR to a directory you own", dir)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("socket directory %s belongs to another user; set XDG_RUNTIME_DIR to a directory you own", dir)
	}
	if st.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("socket directory %s is open to other users and could not be closed: %w", dir, err)
		}
	}
	return nil
}
