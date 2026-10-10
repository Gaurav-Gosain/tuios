package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionFrameAlignsWithCenter keeps each session name visible
// without reserving a row above its framed terminal. A center pane can be
// floating, but both rails start at the viewport's first usable row.
func TestSidebarSessionFrameAlignsWithCenter(t *testing.T) {
	const cfg = `[appearance.sidebar]
enabled = true
position = "right"
width = 36
[appearance.sidebar.left]
enabled = true
width = 36
session = "side-left"
[appearance.sidebar.right]
session = "side-right"
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, name := range []string{"center", "side-left", "side-right"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	left, err := daemonWindows(base, "side-left")
	if err != nil || len(left.Windows) != 1 {
		t.Fatalf("left rail pane: %+v %v", left, err)
	}
	id := left.Windows[0].ID
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side-left", "-w", id, "--literal", "printf 'SAVED-LINE\\n'"); err != nil {
		t.Fatalf("write shell history: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "side-left", "-w", id, "Enter"); err != nil {
		t.Fatalf("shell history enter: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "center"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "side-left") && strings.Contains(s.Text(), "side-right") && strings.Contains(s.Text(), "SAVED-LINE")
	}, bootTimeout); err != nil {
		t.Fatalf("rails did not attach: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		row := s.Line(0)
		if len(row) < 120 {
			return false
		}
		// Both rail frames start on the first row, with their session
		// identities inside the title bars, not on a row above them.
		return strings.HasPrefix(row, "╭") && strings.Contains(row[:36], "side-left") &&
			strings.Contains(row[84:], "side-right")
	}, uiTimeout); err != nil {
		t.Fatalf("rail frames sit below the center frame: %v\n%s", err, term.Snapshot())
	}
	t.Logf("aligned frame and session labels:\n%s", term.Snapshot())
}
