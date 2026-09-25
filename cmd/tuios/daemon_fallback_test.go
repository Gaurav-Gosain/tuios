package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestADaemonThatWillNotStartLeavesTheUserAWayOut covers the cost of shipping
// startup.daemon = true. A bare "tuios" no longer runs standalone, so the
// machine where the daemon cannot come up is now the first run of every new
// user on it. runLocal turns this error into a standalone session, and the
// thing that tells it apart from every other failure on the daemon path is the
// sentinel, so the sentinel is what this pins.
func TestADaemonThatWillNotStartLeavesTheUserAWayOut(t *testing.T) {
	// Its own socket directory: ensureDaemon asks whether a daemon is reachable
	// before it starts one, and the developer's own daemon would answer yes.
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	refuse := errors.New("socket directory is not writable")
	old := spawnDaemon
	spawnDaemon = func() error { return refuse }
	t.Cleanup(func() { spawnDaemon = old })

	err := ensureDaemon()
	if err == nil {
		t.Fatal("ensureDaemon reported success while the daemon refused to start")
	}
	if !errors.Is(err, errDaemonUnreachable) {
		t.Errorf("a daemon that will not start is not recognised as recoverable: %v", err)
	}
	if !errors.Is(err, refuse) {
		t.Errorf("the underlying reason was dropped: %v", err)
	}
	// The fallback must not swallow the diagnosis. runLocal prints its own
	// line, and this message is what "tuios attach" still shows.
	if msg := err.Error(); !strings.Contains(msg, "tuios daemon") {
		t.Errorf("the failure no longer names the command that explains it: %q", msg)
	}
}
