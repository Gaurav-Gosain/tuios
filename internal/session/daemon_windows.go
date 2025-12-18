//go:build windows

package session

import (
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// handleSignals handles Windows signals for daemon shutdown.
// Windows only supports SIGINT and SIGTERM (via Ctrl+C and taskkill).
func (d *Daemon) handleSignals() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case sig := <-sigCh:
			LogBasic("Received %s, shutting down...", sig)
			d.cancel()
			return
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

	// On Windows, we need to use OpenProcess to check if the process exists
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0
	}
	windows.CloseHandle(handle)

	return pid
}
