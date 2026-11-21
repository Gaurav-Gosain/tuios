package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const daemonStartTimeout = 5 * time.Second

var errDaemonUnreachable = errors.New("no daemon is running and one could not be started")

func ensureDaemon() error {
	if err := session.CheckSocketEnv(); err != nil {
		return err
	}
	if session.IsDaemonRunning() {
		return nil
	}

	fmt.Println("Starting TUIOS daemon...")
	if err := spawnDaemon(); err != nil {
		return &diagnosticError{
			What:  fmt.Sprintf("The TUIOS daemon could not be started: %v.", err),
			Cause: "the tuios binary could not be re-executed, or the socket directory is not writable.",
			Fix:   "run 'tuios daemon' in another terminal to see why it fails to start.",
			Err:   errfmt.Errorf("%w: %w", errDaemonUnreachable, err),
		}
	}
	return nil
}

func daemonStderr() *os.File {
	path := session.DefaultDaemonLogPath()
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	return fh
}

var spawnDaemon = func() error { return startDaemonBackground() }

func startDaemonBackground(daemonArgs ...string) error {
	if len(daemonArgs) == 0 && startDaemonThroughService() {
		return nil
	}

	if handled, err := startDaemonThroughApp(daemonArgs); handled {
		return err
	}

	executable, err := os.Executable()
	if err != nil {
		return errfmt.Errorf("failed to get executable path: %w", err)
	}

	cmd := exec.Command(executable, append([]string{"daemon"}, daemonArgs...)...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = daemonStderr()
	cmd.SysProcAttr = daemonSysProcAttr()

	if err := cmd.Start(); err != nil {
		return errfmt.Errorf("failed to start daemon: %w", err)
	}

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.Now().Add(daemonStartTimeout)
	childGone := false
	var childErr error
	for {
		if session.IsDaemonRunning() {
			return nil
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case childErr = <-exited:
			childGone = true
			exited = nil
			if grace := time.Now().Add(time.Second); grace.Before(deadline) {
				deadline = grace
			}
		case <-time.After(50 * time.Millisecond):
		}
	}

	if childGone {
		if childErr != nil {
			return errfmt.Errorf("the daemon exited immediately: %w", childErr)
		}
		return errors.New("the daemon exited immediately without taking the socket")
	}
	return errfmt.Errorf("daemon did not start within %v", daemonStartTimeout)
}

func waitForDaemon(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if session.IsDaemonRunning() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
