package session

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

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
// disk. That is how a test of the saved state failed under load: the wait's
// first poll lost a microsecond race with listener.Close. The
// daemon opts out of unlink-on-close, which makes shutdown's own explicit
// Remove the only unlink and the documented order the real one.
func TestListenerCloseDoesNotUnlinkTheSocket(t *testing.T) {
	d, socketPath, _ := startShutdownTestDaemon(t)

	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("socket not present while the daemon runs: %v", err)
	}

	if err := d.listener.Close(); err != nil {
		t.Fatalf("listener.Close: %v", err)
	}
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("closing the listener unlinked the socket; state saved after this point is invisible to a waiting kill-server: %v", err)
	}

	d.Stop()
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket still present after shutdown finished: %v", err)
	}
}
