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
func EnsureDaemonRunning() error {
	// Check if daemon is already running (either in-process or external)
	if IsDaemonRunning() {
		return nil
	}

	// Start daemon in-process (only once)
	inProcessDaemonOnce.Do(func() {
		cfg := &DaemonConfig{
			Version: "in-process",
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
