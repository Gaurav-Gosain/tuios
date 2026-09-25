package session

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// startShutdownTestDaemon starts a daemon like startTestDaemon but hands back
// the resurrection directory too, so a test can watch what has been written at
// the moment shutdown reports itself complete. It deliberately does not register
// a Stop cleanup, because these tests drive shutdown themselves.
func startShutdownTestDaemon(t *testing.T) (*Daemon, string, string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))

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

// TestListenerCloseDoesNotUnlinkTheSocket pins the mechanism behind the
// ordering contract, deterministically. A *net.UnixListener unlinks its socket
// file on Close by default, and shutdown closes the listener first, so the
// socket used to vanish at the top of shutdown, before any state was saved,
// and WaitForDaemonShutdown could return mid-shutdown and find nothing on
// disk. That is how TestSocketRemovalMeansStateIsPersisted failed under load:
// the wait's first poll lost a microsecond race with listener.Close. The
// daemon opts out of unlink-on-close, which makes shutdown's own explicit
// Remove the only unlink and the documented order the real one.
func TestListenerCloseDoesNotUnlinkTheSocket(t *testing.T) {
	d, socketPath, _ := startShutdownTestDaemon(t)

	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("socket not present while the daemon runs: %v", err)
	}

	// The first thing shutdown does, in isolation.
	if err := d.listener.Close(); err != nil {
		t.Fatalf("listener.Close: %v", err)
	}
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("closing the listener unlinked the socket; state saved after this point is invisible to a waiting kill-server: %v", err)
	}

	// The full shutdown still removes it, at the end.
	d.Stop()
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket still present after shutdown finished: %v", err)
	}
}
