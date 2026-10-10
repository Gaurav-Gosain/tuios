package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionStackShowsActivePaneWhenFull verifies that a short rail
// keeps usable frame heights instead of compressing every session pane into
// zero-height slots. Focusing an offscreen pane moves the visible group.
func TestSidebarSessionStackShowsActivePaneWhenFull(t *testing.T) {
	const cfg = `[appearance.sidebar]
enabled = true
position = "right"
width = 36
[appearance.sidebar.right]
session = "side"
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, name := range []string{"center", "side"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	first, err := daemonWindows(base, "side")
	if err != nil || len(first.Windows) != 1 {
		t.Fatalf("first side pane: %+v %v", first, err)
	}
	firstID := first.Windows[0].ID
	for i := 2; i <= 6; i++ {
		if out, err := tuiosCLI(t, base, "new-window", "extra", "-s", "side"); err != nil {
			t.Fatalf("add side pane %d: %v\n%s", i, err, out)
		}
	}
	latest, err := daemonWindows(base, "side")
	if err != nil || latest.Total != 6 {
		t.Fatalf("six side panes: %+v %v", latest, err)
	}
	for _, item := range []struct{ id, marker string }{{firstID, "FIRSTPANE"}, {latest.FocusedWindowID, "LASTPANE"}} {
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "-w", item.id, "--literal", "printf '%s\\n' "+item.marker); err != nil {
			t.Fatalf("send %s: %v\n%s", item.marker, err, out)
		}
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "-w", item.id, "Enter"); err != nil {
			t.Fatalf("run %s: %v\n%s", item.marker, err, out)
		}
	}
	term := startIn(t, base, startOpts{cols: 100, rows: 18, args: []string{"attach", "center"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(0), "6/6") && strings.Contains(s.Text(), "LASTPANE") && !strings.Contains(s.Text(), "FIRSTPANE")
	}, bootTimeout); err != nil {
		t.Fatalf("active pane did not remain visible in a short rail: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "focus-window", firstID, "-s", "side"); err != nil {
		t.Fatalf("focus earlier pane: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(0), "1/6") && strings.Contains(s.Text(), "FIRSTPANE") && !strings.Contains(s.Text(), "LASTPANE")
	}, uiTimeout); err != nil {
		t.Fatalf("offscreen pane did not come into view: %v\n%s", err, term.Snapshot())
	}
	t.Logf("small rail focuses earlier pane without crushing six frames:\n%s", term.Snapshot())
}
