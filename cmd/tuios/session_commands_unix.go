//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

func stopDaemon(pid int, socketPath string) error {
	if err := killDaemonProcess(pid); err != nil {
		return err
	}
	return awaitDaemonShutdown(pid, socketPath)
}

func forceKillCommand(pid int) string { return fmt.Sprintf("kill -9 %d", pid) }

func killDaemonProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return errfmt.Errorf("failed to find daemon process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return errfmt.Errorf("failed to stop daemon: %w", err)
	}

	fmt.Printf("Sent SIGTERM to daemon (PID %d)\n", pid)
	return nil
}
