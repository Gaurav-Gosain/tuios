//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// startDaemonBackground starts the daemon as a background process on Windows.
func startDaemonBackground() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	cmd := exec.Command(executable, "daemon")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	// Detach from parent process (Windows-specific)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS = 0x00000008
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Wait for daemon to be ready
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		if session.IsDaemonRunning() {
			return nil
		}
	}

	return fmt.Errorf("daemon did not start within timeout")
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
