//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/stopevent"
	"golang.org/x/sys/windows"
)

// daemonSysProcAttr detaches the spawned daemon from this console so it outlives
// the client that started it.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
	}
}

// forceKillCommand is the command that kills the daemon without a shutdown.
func forceKillCommand(pid int) string { return fmt.Sprintf("taskkill /F /PID %d", pid) }

// stopDaemon stops the daemon on Windows. There is no SIGTERM to send, so
// it sets the daemon's stop event and waits, as kill-server does on Unix,
// for the daemon to save its sessions and remove its socket.
//
// A daemon from a tuios older than the event does not wait on it. That one
// is terminated, as kill-server always did on Windows, and its sockets and
// pid file are removed here, since it cannot remove them itself. Without
// that, kill-server waited for a socket that never went away.
func stopDaemon(pid int, socketPath string) error {
	if err := stopevent.Request(socketPath); err == nil {
		fmt.Printf("Asked the daemon (PID %d) to stop\n", pid)
		return awaitDaemonShutdown(pid, socketPath)
	}
	if err := terminateDaemon(pid); err != nil {
		return err
	}
	for _, f := range session.DaemonFiles(socketPath) {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", f, err)
		}
	}
	fmt.Printf("TUIOS daemon (PID %d) stopped. This daemon is from an older tuios, so it was terminated and could not save its sessions.\n", pid)
	return nil
}

// terminateDaemon calls TerminateProcess and waits for the process to end,
// so its files are no longer in use when they are removed.
func terminateDaemon(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid)) //nolint:gosec // a process id fits
	if err != nil {
		return fmt.Errorf("failed to find daemon process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("failed to stop daemon: %w", err)
	}
	if ev, err := windows.WaitForSingleObject(h, uint32((5 * time.Second).Milliseconds())); err != nil || ev != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("the daemon (PID %d) did not end after it was terminated", pid)
	}
	return nil
}
