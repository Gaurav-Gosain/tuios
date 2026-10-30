package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestNotificationLevelKeepsInfoOffTheDock sets notifications.level to
// warning (#1361) and drives a real client:
//
//   - clear_scrollback with no pane open raises a warning, and the dock shows
//     it;
//   - toggling tiling raises an info message, and for the next two seconds the
//     dock never shows it;
//   - the log viewer lists that info message, so a notice the dock left out
//     can still be read.
//
// A frame of the dock with the warning, and one of the log viewer, are saved
// under TUIOS_E2E_FRAMES.
//
// Negative control: with the level check taken out of showNotification, the
// dock shows "Tiling o..." and the wait for it to stay away fails.
func TestNotificationLevelKeepsInfoOffTheDock(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[notifications]\nlevel = \"warning\"\nduration = 30\n\n[keybindings.global]\nclear_scrollback = [\"ctrl+g\"]\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	dir := artifactDir(t)

	if err := term.SendKeys(tuitest.Ctrl('g')); err != nil {
		t.Fatalf("send ctrl+g: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(dockRow(s), "No pane is open")
	}, uiTimeout); err != nil {
		t.Fatalf("the warning is not in the dock: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "notification-level-warning")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "No pane is open")
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not dismiss the warning: %v\n%s", err, term.Snapshot())
	}

	newWindow(t, term)
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("send 't': %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(term.Screen().Text(), "Tiling o") {
			t.Fatalf("ASSERTION: the dock shows an info message under level warning\n%s", term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "D", "l"); err != nil {
		t.Fatalf("open the log viewer: %v", err)
	}
	if err := term.WaitForText("Tiling o", uiTimeout); err != nil {
		t.Fatalf("the log viewer does not list the info message the dock left out: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "notification-level-log")
}
