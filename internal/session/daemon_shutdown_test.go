package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// startShutdownTestDaemon starts a daemon like startTestDaemon but hands back
// the resurrection directory too, so a test can watch what has been written at
// the moment shutdown reports itself complete. It deliberately does not register
// a Stop cleanup, because these tests drive shutdown themselves.
func startShutdownTestDaemon(t *testing.T) (*Daemon, string, string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	stateDir := t.TempDir()
	t.Cleanup(useResurrectionDir(stateDir))

	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon Start: %v", err)
	}
	t.Cleanup(d.Stop)

	sp, err := GetSocketPath()
	if err != nil {
		t.Fatalf("GetSocketPath: %v", err)
	}
	return d, sp, stateDir
}

// TestSocketRemovalMeansStateIsPersisted pins the ordering contract that
// WaitForDaemonShutdown depends on: by the time the socket is gone, every
// session's final resurrection state is on disk.
//
// This is what makes 'tuios kill-server' followed by a fresh start safe. If the
// unlink were ordered before the saves, the socket could vanish while a session
// was still being written, and the next daemon would restore a truncated or
// missing file.
func TestSocketRemovalMeansStateIsPersisted(t *testing.T) {
	d, socketPath, stateDir := startShutdownTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	statePath := filepath.Join(stateDir, "work.json")
	// Remove any state the periodic saver may already have written, so the file
	// observed below can only have come from the shutdown path.
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clearing pre-shutdown state: %v", err)
	}

	go d.Stop()

	if err := WaitForDaemonShutdown(10 * time.Second); err != nil {
		t.Fatalf("WaitForDaemonShutdown: %v", err)
	}

	// Check immediately, with no polling: the wait is only meaningful if the
	// state is already durable the instant it returns.
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("socket was removed before the session's state was saved: %v", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Errorf("wait returned but the socket is still present: %v", err)
	}
}

// TestWaitForDaemonShutdownReturnsWhenSocketGoes covers the ordinary case: the
// wait blocks while the daemon is up and returns once it has finished.
func TestWaitForDaemonShutdownReturnsWhenSocketGoes(t *testing.T) {
	d, _, _ := startShutdownTestDaemon(t)

	// While the daemon is up the wait must not report success, or kill-server
	// would return before anything had happened.
	if err := WaitForDaemonShutdown(100 * time.Millisecond); !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("wait on a live daemon = %v, want ErrShutdownTimeout", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		d.Stop()
	}()

	start := time.Now()
	if err := WaitForDaemonShutdown(10 * time.Second); err != nil {
		t.Fatalf("WaitForDaemonShutdown: %v", err)
	}
	// It must have actually waited rather than returned on a stale observation.
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("wait returned after %v, before the daemon could have stopped", elapsed)
	}
}

// TestWaitForDaemonShutdownTimesOut checks the bounded-wait behaviour when a
// daemon never finishes: the caller gets a typed timeout rather than hanging or
// being told the daemon stopped.
func TestWaitForDaemonShutdownTimesOut(t *testing.T) {
	startShutdownTestDaemon(t)

	start := time.Now()
	err := WaitForDaemonShutdown(200 * time.Millisecond)
	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("err = %v, want ErrShutdownTimeout", err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Errorf("returned after %v, before the %v timeout elapsed", elapsed, 200*time.Millisecond)
	}
}

// TestWaitForDaemonShutdownReturnsImmediatelyWhenAbsent covers kill-server being
// run when no daemon is there: the signal is already in its final state, so the
// wait must not burn the full timeout.
func TestWaitForDaemonShutdownReturnsImmediatelyWhenAbsent(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	start := time.Now()
	if err := WaitForDaemonShutdown(5 * time.Second); err != nil {
		t.Fatalf("WaitForDaemonShutdown with no daemon: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v with no daemon present, should return at once", elapsed)
	}
}
