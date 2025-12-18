//go:build !windows

package session

import (
	"fmt"
	"os"
	"path/filepath"
)

// GetSocketPath returns the path to the daemon socket.
func GetSocketPath() (string, error) {
	// Use XDG_RUNTIME_DIR if available (preferred for sockets)
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir != "" {
		socketDir := filepath.Join(runtimeDir, "tuios")
		if err := os.MkdirAll(socketDir, 0700); err != nil {
			return "", fmt.Errorf("failed to create socket directory: %w", err)
		}
		return filepath.Join(socketDir, "tuios.sock"), nil
	}

	// Fallback to /tmp/tuios-$UID/
	uid := os.Getuid()
	socketDir := filepath.Join("/tmp", fmt.Sprintf("tuios-%d", uid))
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create socket directory: %w", err)
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
