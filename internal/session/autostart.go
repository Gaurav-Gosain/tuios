package session

import (
	"net"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

var (
	inProcessDaemon     *Daemon
	inProcessDaemonOnce sync.Once
	inProcessDaemonErr  error
)

// EnsureDaemonRunningWith ensures the TUIOS daemon is running. If it is not,
// it starts the daemon in-process in a background goroutine, and returns nil
// once the daemon is ready or an error if it fails to start.
//
// version is the build of whoever is starting it, which the daemon reports back
// to every client that connects so the two can tell whether they are the same
// build. Empty is allowed and means the caller does not know.
//
// The caller passes the user's [daemon] settings. This package deliberately does not read the
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
	if IsDaemonRunning() {
		return nil
	}

	inProcessDaemonOnce.Do(func() {
		if cfg == nil {
			cfg = &DaemonConfig{}
		}
		cfg.Version = version
		if cfg.Version == "" {
			cfg.Version = "in-process"
		}
		inProcessDaemon = NewDaemon(cfg)

		if err := inProcessDaemon.Start(); err != nil {
			inProcessDaemonErr = errfmt.Errorf("failed to start in-process daemon: %w", err)
			return
		}
	})

	if inProcessDaemonErr != nil {
		return inProcessDaemonErr
	}

	return waitForDaemon(5 * time.Second)
}

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

	return errfmt.Errorf("daemon did not start within %v", timeout)
}

// StopInProcessDaemon stops the in-process daemon if it was started.
// This should be called during graceful shutdown.
func StopInProcessDaemon() {
	if inProcessDaemon != nil {
		inProcessDaemon.Stop()
	}
}
