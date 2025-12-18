//go:build !windows

package session

import (
	"os"
	"os/signal"
	"strconv"
	"syscall"
)

// handleSignals handles Unix signals for daemon shutdown and reload.
func (d *Daemon) handleSignals() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for {
		select {
		case sig := <-sigCh:
			switch sig {
			case syscall.SIGINT, syscall.SIGTERM:
				LogBasic("Received %s, shutting down...", sig)
				d.cancel()
				return
			case syscall.SIGHUP:
				LogBasic("Received SIGHUP, reloading configuration...")
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
