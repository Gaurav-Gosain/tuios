package session

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestScrollbackLinesReachesTheDaemon follows appearance.scrollback_lines from
// the user's config file to the depth a session gives its panes.
func TestScrollbackLinesReachesTheDaemon(t *testing.T) {
	uc := config.DefaultConfig()
	uc.Appearance.ScrollbackLines = 321
	if got := DaemonConfigFromUser(uc).ScrollbackLines; got != 321 {
		t.Fatalf("the daemon config carries %d, want 321", got)
	}

	m := NewManager()
	m.SetSocketPath(t.TempDir() + "/sock")
	m.SetScrollbackLines(321)
	stamped, err := m.CreateSession("stamped", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer stamped.Stop()
	if got := stamped.scrollbackLines(); got != 321 {
		t.Fatalf("a session made by the manager gives its panes %d lines, want 321", got)
	}
	own, err := m.CreateSession("own", &SessionConfig{ScrollbackLines: 77}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer own.Stop()
	if got := own.scrollbackLines(); got != 77 {
		t.Fatalf("a session with its own depth gives its panes %d lines, want 77", got)
	}
	if got := (&Session{}).scrollbackLines(); got != vt.DefaultScrollbackSize {
		t.Fatalf("a session with no config gives its panes %d lines, want the default %d", got, vt.DefaultScrollbackSize)
	}
}

// TestDaemonPanesKeepTheConfiguredScrollback makes a pane in a session with a
// depth and checks the emulator behind it keeps about that many lines. The
// daemon used to make every pane with ten thousand whatever the user set. The
// pure ring lands on the depth exactly; libghostty prunes whole pages, so it
// lands near it, and the check allows that on both.
func TestDaemonPanesKeepTheConfiguredScrollback(t *testing.T) {
	const depth = 1000
	sess, err := NewSession("depth", &SessionConfig{ScrollbackLines: depth}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()
	pty, err := sess.CreatePTY("win-1", 80, 24, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()

	pty.terminalMu.Lock()
	for i := range 5 * depth {
		if _, err := pty.terminal.Write(fmt.Appendf(nil, "line %d\r\n", i)); err != nil {
			pty.terminalMu.Unlock()
			t.Fatal(err)
		}
	}
	got := pty.terminal.ScrollbackLen()
	pty.terminalMu.Unlock()
	if got < depth*7/10 || got > depth*12/10 {
		t.Fatalf("the daemon's emulator keeps %d lines, want about the configured %d", got, depth)
	}
}
