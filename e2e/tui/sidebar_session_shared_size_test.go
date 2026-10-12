package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionDoesNotShrinkAnotherViewer keeps a full-size client on a
// session while a second client docks that same session in a narrow rail.
// The rail may crop the guest, but it must not resize the shared session.
func TestSidebarSessionDoesNotShrinkAnotherViewer(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "")
	for _, name := range []string{"center", "side"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	big := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "side"}})
	if err := big.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "╭") }, bootTimeout); err != nil {
		t.Fatalf("full-size client did not attach: %v\n%s", err, big.Snapshot())
	}
	beforeW, beforeH, policy := sessionPolicySize(t, base, "side")
	if beforeW < 100 || beforeH < 30 {
		t.Fatalf("full-size session starts at %dx%d (%s), want near 120x40", beforeW, beforeH, policy)
	}
	writeConfig(t, base, `[appearance.sidebar]
position = "right"
enabled = true
width = 36
[appearance.sidebar.right]
session = "side"
`)
	center := startIn(t, base, startOpts{cols: 100, rows: 35, args: []string{"attach", "center"}})
	if err := center.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "╭") >= 2 && strings.Count(s.Text(), "$") >= 2
	}, bootTimeout); err != nil {
		t.Fatalf("rail pane did not appear: %v\n%s", err, center.Snapshot())
	}
	width, height, _ := sessionPolicySize(t, base, "side")
	if width != beforeW || height != beforeH {
		t.Fatalf("rail changed shared session size: %dx%d -> %dx%d (%s)\n%s", beforeW, beforeH, width, height, policy, center.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "--literal", `printf '%s\n' RAIL_SHARED_SIZE_INPUT`); err != nil {
		t.Fatalf("send to docked session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "Enter"); err != nil {
		t.Fatalf("run in docked session: %v\n%s", err, out)
	}
	if err := center.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "RAIL_SHARED_SIZE_INPUT")
	}, uiTimeout); err != nil {
		t.Fatalf("docked session did not show live output: %v\n%s", err, center.Snapshot())
	}
	saveArtifact(t, center, artifactDir(t), "shared-session-rail")
}
