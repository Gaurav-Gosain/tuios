package session

import (
	"errors"
	"fmt"
	"os"
)

// ErrDaemonStarting reports that another process holds the start lock, so it is
// mid-way through binding the socket. The caller has nothing to fix: waiting for
// the socket is the whole remedy.
var ErrDaemonStarting = errors.New("another TUIOS daemon is starting")

// startLockPath returns the lock file guarding daemon startup. It sits beside
// the socket and is never removed: a lock is an inode, and deleting it would let
// two starters hold locks on two different inodes, which is the race it exists
// to prevent.
func startLockPath() (string, error) {
	socketPath, err := GetSocketPath()
	if err != nil {
		return "", err
	}
	return socketPath + ".lock", nil
}

// acquireStartLock takes the exclusive startup lock, returning ErrDaemonStarting
// if another process already holds it.
//
// Start's stale-socket recovery is what makes this necessary. Between a daemon's
// bind and its listen, a probe from a second starter is refused, which reads
// exactly like the crashed daemon that recovery is for: the second starter would
// unlink a live daemon's socket and bind its own, leaving the first serving an
// inode nothing can reach and its sessions unreachable with it. Two clients
// racing 'tuios attach' is the ordinary way to produce that interleaving.
//
// The returned file must stay open for the daemon's life; closing it releases
// the lock.
func acquireStartLock() (*os.File, error) {
	path, err := startLockPath()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open daemon start lock: %w", err)
	}
	if err := lockFileExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// releaseStartLock drops the startup lock. Safe on a daemon that never took one.
func (d *Daemon) releaseStartLock() {
	if d.startLock == nil {
		return
	}
	_ = d.startLock.Close()
	d.startLock = nil
}
