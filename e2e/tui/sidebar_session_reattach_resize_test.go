package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionReattachDoesNotResizeUnchangedPTY catches a redundant
// SIGWINCH to a daemon shell when a rail viewer reattaches at the same size.
// The daemon's PTY already has the rail size from the first attachment.
func TestSidebarSessionReattachDoesNotResizeUnchangedPTY(t *testing.T) {
	const cfg = `[appearance.sidebar]
enabled = true
position = "left"
width = 36
[appearance.sidebar.left]
session = "side"
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, name := range []string{"center", "side"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	list, err := daemonWindows(base, "side")
	if err != nil || len(list.Windows) != 1 {
		t.Fatalf("side window: %+v %v", list, err)
	}
	id := list.Windows[0].ID
	first := startIn(t, base, startOpts{cols: 100, rows: 35, args: []string{"attach", "center"}})
	if err := first.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "side") && strings.Contains(s.Text(), "sh-3.2$")
	}, bootTimeout); err != nil {
		t.Fatalf("first attach: %v\n%s", err, first.Snapshot())
	}
	// Arm the guest shell after the first legitimate resize. The command
	// clears its visible text so the only later occurrence of the marker
	// must come from a second, unnecessary window-size signal.
	for _, command := range []string{`trap 'printf "WINCH_MARK\n"' WINCH`, `printf '\033[2J\033[H'`} {
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "-w", id, "--literal", command); err != nil {
			t.Fatalf("arm WINCH trap: %v\n%s", err, out)
		}
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "-w", id, "Enter"); err != nil {
			t.Fatalf("run trap setup: %v\n%s", err, out)
		}
	}
	time.Sleep(500 * time.Millisecond)
	if err := first.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatal(err)
	}
	waitExit(t, first, "after detaching center")
	before, err := tuiosCLI(t, base, "capture-pane", "-s", "side", "-w", id)
	if err != nil || strings.Contains(before, "WINCH_MARK") {
		t.Fatalf("marker already present before reattach: %v\n%s", err, before)
	}
	second := startIn(t, base, startOpts{cols: 100, rows: 35, args: []string{"attach", "center"}})
	if err := second.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "side") && strings.Contains(s.Text(), "sh-3.2$")
	}, bootTimeout); err != nil {
		t.Fatalf("second attach: %v\n%s", err, second.Snapshot())
	}
	time.Sleep(800 * time.Millisecond)
	after, err := tuiosCLI(t, base, "capture-pane", "-s", "side", "-w", id)
	if err != nil || strings.Contains(after, "WINCH_MARK") {
		t.Fatalf("unchanged rail PTY was resized on reattach: %v\n%s\n%s", err, after, second.Snapshot())
	}
}
