//go:build !windows

package session

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/sockpath"
)

// GetSocketPath returns the path to the daemon socket. It refuses when
// TUIOS_SOCKET names a different socket that no daemon listens on; see
// socket_env.go.
func GetSocketPath() (string, error) {
	path, err := defaultSocketPath()
	if err != nil {
		return "", err
	}
	if err := checkSocketEnv(path); err != nil {
		return "", err
	}
	return path, nil
}

func defaultSocketPath() (string, error) {
	socketDir := sockpath.Dir()
	dirs := []string{socketDir}
	if base := sockpath.BaseDir(); base != socketDir {
		dirs = []string{base, filepath.Dir(socketDir), socketDir}
	}
	for _, dir := range dirs {
		if err := ensureSocketDir(dir); err != nil {
			return "", err
		}
	}
	return pickLiveSocket(filepath.Join(socketDir, "tuios.sock")), nil
}

// GetPidFilePath returns the path to the daemon PID file.
func GetPidFilePath() (string, error) {
	socketPath, err := GetSocketPath()
	if err != nil {
		return "", err
	}
	return socketPath + ".pid", nil
}

func ensureSocketDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errfmt.Errorf("failed to create socket directory: %w", err)
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return errfmt.Errorf("failed to check socket directory: %w", err)
	}
	if !st.Mode().IsDir() {
		return errfmt.Errorf("socket directory %s is not a directory; remove it or set XDG_RUNTIME_DIR to a directory you own", dir)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Getuid() {
		return errfmt.Errorf("socket directory %s belongs to another user; set XDG_RUNTIME_DIR to a directory you own", dir)
	}
	if st.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory, which needs the execute bit to be entered
			return errfmt.Errorf("socket directory %s is open to other users and could not be closed: %w", dir, err)
		}
	}
	return nil
}
