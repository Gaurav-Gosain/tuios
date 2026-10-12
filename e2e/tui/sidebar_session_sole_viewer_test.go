package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionSoleViewerKeepsSessionSize checks the no-size-owner path:
// the side session has no ordinary attached client, only a narrow passive rail.
// Attaching and resizing the rail must leave the daemon's existing size alone.
func TestSidebarSessionSoleViewerKeepsSessionSize(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "")
	for _, name := range []string{"center", "side"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	beforeW, beforeH, _ := sessionPolicySize(t, base, "side")
	if beforeW <= 36 || beforeH <= 8 {
		t.Fatalf("side must start larger than rail, got %dx%d", beforeW, beforeH)
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
		t.Fatalf("sole-viewer rail did not attach: %v\n%s", err, center.Snapshot())
	}
	if w, h, _ := sessionPolicySize(t, base, "side"); w != beforeW || h != beforeH {
		t.Fatalf("passive sole viewer resized session: %dx%d -> %dx%d", beforeW, beforeH, w, h)
	}
	if err := center.Resize(85, 30); err != nil {
		t.Fatalf("resize center: %v", err)
	}
	if w, h, _ := sessionPolicySize(t, base, "side"); w != beforeW || h != beforeH {
		t.Fatalf("resizing sole viewer's rail resized session: %dx%d -> %dx%d", beforeW, beforeH, w, h)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "--literal", "printf '%s\\n' SOLO"); err != nil {
		t.Fatalf("send to side: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "Enter"); err != nil {
		t.Fatalf("run in side: %v\n%s", err, out)
	}
	if err := center.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "SOLO")
	}, uiTimeout); err != nil {
		t.Fatalf("sole viewer did not render live output: %v\n%s", err, center.Snapshot())
	}
	saveArtifact(t, center, artifactDir(t), "sole-viewer-session-rail")
}
