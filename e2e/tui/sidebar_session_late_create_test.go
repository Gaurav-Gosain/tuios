package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionConnectsWhenCreatedLater covers an assignment entered
// before its daemon session exists. A temporary attach failure must not leave
// a rail permanently stuck until the whole center client reconnects.
func TestSidebarSessionConnectsWhenCreatedLater(t *testing.T) {
	const cfg = `[appearance.sidebar]
position = "right"
enabled = true
width = 36
[appearance.sidebar.right]
session = "later-side"
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	if out, err := tuiosCLI(t, base, "new", "center", "--detach"); err != nil {
		t.Fatalf("create center: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 110, rows: 36, args: []string{"attach", "center"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "later-side") && strings.Contains(s.Text(), "attach failed")
	}, bootTimeout); err != nil {
		t.Fatalf("missing session failure was not visible: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "new", "later-side", "--detach"); err != nil {
		t.Fatalf("create assigned session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "later-side", "--literal", "printf '%s\\n' LATE-SIDE-READY"); err != nil {
		t.Fatalf("send marker: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "later-side", "Enter"); err != nil {
		t.Fatalf("run marker: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "LATE-SIDE-READY")
	}, uiTimeout); err != nil {
		t.Fatalf("rail did not reconnect after assigned session appeared: %v\n%s", err, term.Snapshot())
	}
	// Removing and recreating the assigned daemon session must also reconnect
	// this passive viewer. Its original PTY and stream sequence no longer exist.
	if out, err := tuiosCLI(t, base, "kill-session", "later-side"); err != nil {
		t.Fatalf("remove assigned session: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "LATE-SIDE-READY")
	}, uiTimeout); err != nil {
		t.Fatalf("rail kept rendering a deleted session: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "new", "later-side", "--detach"); err != nil {
		t.Fatalf("recreate assigned session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "later-side", "--literal", "printf '%s\\n' SIDE-RECREATED"); err != nil {
		t.Fatalf("send recreated marker: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "later-side", "Enter"); err != nil {
		t.Fatalf("run recreated marker: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "SIDE-RECREATED")
	}, uiTimeout); err != nil {
		t.Fatalf("rail did not show the recreated session: %v\n%s", err, term.Snapshot())
	}
	t.Logf("assigned session appeared and reappeared without reconnecting center:\n%s", term.Snapshot())
}
