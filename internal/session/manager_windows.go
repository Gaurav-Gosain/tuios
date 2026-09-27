//go:build windows

package session

import (
	"fmt"
	"os"
	"path/filepath"
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

// defaultSocketPath is the daemon socket the environment's directories name.
// On Windows, we use Unix sockets in the user's local app data directory.
// Windows 10 build 17063+ supports AF_UNIX sockets.
func defaultSocketPath() (string, error) {
	// Use LOCALAPPDATA for user-specific data
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		// Fallback to user profile
		userProfile := os.Getenv("USERPROFILE")
		if userProfile == "" {
			return "", fmt.Errorf("cannot determine user directory: LOCALAPPDATA and USERPROFILE not set")
		}
		localAppData = filepath.Join(userProfile, "AppData", "Local")
	}

	socketDir := filepath.Join(localAppData, "tuios")
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
