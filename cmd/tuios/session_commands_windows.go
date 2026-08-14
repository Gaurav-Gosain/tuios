//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// daemonSysProcAttr detaches the spawned daemon from this console so it outlives
// the client that started it.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
	}
}

// killDaemonProcess terminates the daemon process on Windows.
// Windows doesn't support SIGTERM, so we use Process.Kill() which calls TerminateProcess.
func killDaemonProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find daemon process: %w", err)
	}

	// On Windows, Kill() calls TerminateProcess
	if err := process.Kill(); err != nil {
		return fmt.Errorf("failed to stop daemon: %w", err)
	}

	fmt.Printf("Terminated daemon (PID %d)\n", pid)
	return nil
}
