//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// daemonSysProcAttr detaches the spawned daemon from this process group so it
// outlives the client that started it.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// killDaemonProcess sends SIGTERM to the daemon process on Unix.
func killDaemonProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find daemon process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to stop daemon: %w", err)
	}

	fmt.Printf("Sent SIGTERM to daemon (PID %d)\n", pid)
	return nil
}
