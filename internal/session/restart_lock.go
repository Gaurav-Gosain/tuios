//go:build !js

package session

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrRestartRunning says another tuios restart-server holds the restart lock.
var ErrRestartRunning = errors.New("another restart of the daemon is running")

// LockRestart takes the lock that lets one daemon restart run at a time. It
// sits beside the daemon's socket, so each named server has its own. The
// lock goes with the file: closing it, or the holder exiting in any way,
// drops it.
func LockRestart() (*os.File, error) {
	sock, err := GetSocketPath()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(sock)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "restart.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFileExclusive(f); err != nil {
		_ = f.Close()
		if errors.Is(err, ErrDaemonStarting) {
			return nil, ErrRestartRunning
		}
		return nil, err
	}
	return f, nil
}

// RestartLogPath is where tuios restart-server writes its steps, one JSON
// object per line, beside the daemon's own log.
func RestartLogPath() string {
	return filepath.Join(filepath.Dir(DefaultDaemonLogPath()), "restart.log")
}
