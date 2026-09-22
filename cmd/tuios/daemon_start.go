package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// daemonStartTimeout is how long a client waits for a spawned daemon to be
// reachable before giving up on it.
const daemonStartTimeout = 5 * time.Second

// errDaemonUnreachable marks the one failure a bare "tuios" can recover from:
// no daemon is running and a new one will not start. Every other failure on the
// daemon path is about a session, not about the daemon, and has to be reported.
//
// It is a sentinel rather than a type because the only question asked of it is
// "was this the daemon refusing to come up", and errors.Is answers that through
// the diagnostic wrapper that carries the message the user reads.
var errDaemonUnreachable = errors.New("no daemon is running and one could not be started")

// ensureDaemon starts a daemon if none is reachable, and says so once. Every
// command that may bring a daemon up funnels through here so the wording, the
// timeout and the failure explanation cannot drift between them.
func ensureDaemon() error {
	if session.IsDaemonRunning() {
		return nil
	}

	fmt.Println("Starting TUIOS daemon...")
	if err := spawnDaemon(); err != nil {
		return &diagnosticError{
			What:  fmt.Sprintf("The TUIOS daemon could not be started: %v.", err),
			Cause: "the tuios binary could not be re-executed, or the socket directory is not writable.",
			Fix:   "run 'tuios daemon' in another terminal to see why it fails to start.",
			Err:   fmt.Errorf("%w: %w", errDaemonUnreachable, err),
		}
	}
	return nil
}

// daemonStderr opens the daemon log file for the child to write its stderr to,
// and returns nil when it cannot be opened.
//
// Everything the daemon logs reaches the same file through its own sink.
// Stderr is for the one thing it cannot log: a Go runtime panic the recover in
// handleConnection does not catch writes its message and stack straight to
// stderr, and then the process is gone. Sending it here is what makes a crashed daemon leave a
// reason behind.
//
// The daemon appends to this file too, and may rotate it. Both ends open it
// O_APPEND, so no line is interleaved with another mid-line. A rotation renames
// the file rather than deleting it, so this descriptor keeps writing to what is
// then daemon.log.old and nothing is lost.
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

// spawnDaemon is the call ensureDaemon makes to bring a daemon up. It is a
// variable so a test can make the daemon refuse to start, which is the one
// failure a bare "tuios" recovers from and therefore the one that has to be
// proved rather than assumed.
var spawnDaemon = startDaemonBackground

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
	cmd.Stderr = daemonStderr()
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
