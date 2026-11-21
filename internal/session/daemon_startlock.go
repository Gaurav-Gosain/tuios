package session

import (
	"errors"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// ErrDaemonStarting reports that another process holds the start lock, so it is
// mid-way through binding the socket. The caller has nothing to fix: waiting for
// the socket is the whole remedy.
var ErrDaemonStarting = errors.New("another TUIOS daemon is starting")

func startLockPath(socketPath string) string {
	return socketPath + ".lock"
}

func acquireStartLock(socketPath string) (*os.File, error) {
	f, err := os.OpenFile(startLockPath(socketPath), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errfmt.Errorf("failed to open daemon start lock: %w", err)
	}
	if err := lockFileExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func (d *Daemon) releaseStartLock() {
	if d.startLock == nil {
		return
	}
	_ = d.startLock.Close()
	d.startLock = nil
}
