//go:build !windows && !js

package session

import (
	"os"
	"os/signal"
	"strconv"
	"syscall"
)

// catchSignals starts catching the signals handleSignals acts on. Start
// calls it first, before any pane starts. A daemon started with SIGHUP
// ignored, as the restart worker starts it, would otherwise pass the ignore
// on to the panes it starts before the handler is in place: their programs
// then outlived the close of their pane. A caught signal is set back to its
// default in a program the daemon starts.
func (d *Daemon) catchSignals() {
	d.sigCh = make(chan os.Signal, 1)
	signal.Notify(d.sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
}

// handleSignals handles Unix signals for daemon shutdown. SIGHUP is caught so
// it does not kill the daemon, and otherwise ignored: config changes are
// picked up by the file watcher.
func (d *Daemon) handleSignals() {
	if d.sigCh == nil {
		d.catchSignals()
	}
	for {
		select {
		case sig := <-d.sigCh:
			switch sig {
			case syscall.SIGINT, syscall.SIGTERM:
				LogBasic("Received %s, shutting down...", sig)
				d.cancel()
				return
			case syscall.SIGHUP:
				LogBasic("Received SIGHUP, ignoring it: config changes are picked up from the file watcher")
			}
		case <-d.ctx.Done():
			return
		}
	}
}

// GetDaemonPID returns the PID of the running daemon.
func GetDaemonPID() int {
	pidPath, err := GetPidFilePath()
	if err != nil {
		return 0
	}

	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return 0
	}

	// On Unix, Signal(0) checks if process exists
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return 0
	}

	return pid
}
