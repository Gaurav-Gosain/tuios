package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// daemonStartTimeout is how long a client waits for a spawned daemon to be
// reachable before giving up on it.
const daemonStartTimeout = 5 * time.Second

// ensureDaemon starts a daemon if none is reachable, and says so once. Every
// command that may bring a daemon up funnels through here so the wording, the
// timeout and the failure explanation cannot drift between them.
func ensureDaemon() error {
	if session.IsDaemonRunning() {
		return nil
	}

	fmt.Println("Starting TUIOS daemon...")
	if err := startDaemonBackground(); err != nil {
		return &diagnosticError{
			What:  fmt.Sprintf("The TUIOS daemon could not be started: %v.", err),
			Cause: "the tuios binary could not be re-executed, or the socket directory is not writable.",
			Fix:   "run 'tuios daemon' in another terminal to see why it fails to start.",
			Err:   err,
		}
	}
	return nil
}

// startDaemonBackground spawns a detached daemon and returns once a daemon is
// reachable.
//
// Success is "a daemon is up", not "my child is up". Two clients can decide to
// start one at the same moment; the daemon's own start lock picks a winner and
// the loser exits, which is the right outcome for both clients.
func startDaemonBackground() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	cmd := exec.Command(executable, "daemon")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = daemonSysProcAttr()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Reap the child. A daemon that loses the start race exits at once, and
	// without a Wait it would sit as a zombie for as long as this process runs,
	// which for an attach is the length of the whole session.
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
			exited = nil // a nil channel blocks, so this case cannot fire twice
			// The child may have exited because another client's daemon won the
			// lock, so give that one a moment to finish binding before deciding
			// nothing is coming.
			if grace := time.Now().Add(time.Second); grace.Before(deadline) {
				deadline = grace
			}
		case <-time.After(50 * time.Millisecond):
		}
	}

	if childGone {
		if childErr != nil {
			return fmt.Errorf("the daemon exited immediately: %w", childErr)
		}
		return errors.New("the daemon exited immediately without taking the socket")
	}
	return fmt.Errorf("daemon did not start within %v", daemonStartTimeout)
}
