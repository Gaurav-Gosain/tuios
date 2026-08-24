// Package session provides daemon auto-start functionality.
package session

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// Global daemon instance for in-process daemon
var (
	inProcessDaemon     *Daemon
	inProcessDaemonOnce sync.Once
	inProcessDaemonErr  error
)

// EnsureDaemonRunning ensures the TUIOS daemon is running.
// If not running, it starts the daemon in-process in a background goroutine.
// Returns nil if daemon is ready, or an error if it fails to start.
//
// version is the build of whoever is starting it, which the daemon reports back
// to every client that connects so the two can tell whether they are the same
// build. Empty is allowed and means the caller does not know.
func EnsureDaemonRunning(version string) error {
	return EnsureDaemonRunningWith(version, nil)
}

// EnsureDaemonRunningWith is EnsureDaemonRunning for a caller that has the
// user's [daemon] settings to hand. This package deliberately does not read the
// config file (it would invert the layering, and the daemon outlives every
// process that could own one), so the settings arrive from whoever starts it:
// `tuios daemon` fills them in runDaemon, and a server does it here.
//
// Without this, a daemon autostarted by tuios-web ran with the built-in
// defaults while the same daemon autostarted by `tuios attach` honoured the
// file, so whether agent detection was on came down to which command happened
// to win the start race.
//
// cfg is used only when this call is the one that starts the daemon. An
// already-running daemon keeps the settings it started with, since they are
// its own and it outlives this process.
func EnsureDaemonRunningWith(version string, cfg *DaemonConfig) error {
	// Check if daemon is already running (either in-process or external)
	if IsDaemonRunning() {
		return nil
	}

	// Start daemon in-process (only once)
	inProcessDaemonOnce.Do(func() {
		if cfg == nil {
			cfg = &DaemonConfig{}
		}
		// The build that started it, not a label saying how. A client compares
		// the daemon's build against its own at the handshake and says so when
		// they differ, and "in-process" made every such comparison meaningless:
		// it is not a version, so it never matched anything and told nobody
		// anything either.
		cfg.Version = version
		if cfg.Version == "" {
			cfg.Version = "in-process"
		}
		inProcessDaemon = NewDaemon(cfg)

		// Start() is non-blocking - it starts goroutines and returns
		if err := inProcessDaemon.Start(); err != nil {
			inProcessDaemonErr = fmt.Errorf("failed to start in-process daemon: %w", err)
			return
		}
	})

	if inProcessDaemonErr != nil {
		return inProcessDaemonErr
	}

	// Wait for daemon to be ready with timeout
	return waitForDaemon(5 * time.Second)
}

// waitForDaemon waits for the daemon to be ready with a timeout.
func waitForDaemon(timeout time.Duration) error {
	socketPath, err := GetSocketPath()
	if err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	return fmt.Errorf("daemon did not start within %v", timeout)
}

// StopInProcessDaemon stops the in-process daemon if it was started.
// This should be called during graceful shutdown.
func StopInProcessDaemon() {
	if inProcessDaemon != nil {
		inProcessDaemon.Stop()
	}
}
