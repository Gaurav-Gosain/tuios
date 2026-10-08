//go:build windows

package session

import (
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/stopevent"
	"golang.org/x/sys/windows"
)

// handleSignals handles Windows signals for daemon shutdown.
// Windows only supports SIGINT and SIGTERM (via Ctrl+C and taskkill).
// kill-server cannot send either to a detached daemon, so it sets the stop
// event instead. See internal/stopevent.
func (d *Daemon) handleSignals() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	stopCh, err := stopevent.Watch(d.manager.SocketPath(), d.ctx.Done())
	if err != nil {
		log.Printf("Warning: the stop event could not be made: %v. tuios kill-server will terminate this daemon, and sessions are not saved.", err)
	}

	select {
	case sig := <-sigCh:
		LogBasic("Received %s, shutting down...", sig)
		d.cancel()
	case <-stopCh:
		LogBasic("kill-server asked the daemon to stop, shutting down...")
		d.cancel()
	case <-d.ctx.Done():
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
