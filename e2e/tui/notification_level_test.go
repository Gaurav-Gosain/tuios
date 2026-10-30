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

// TestNotificationLevelLetsPanesAndAgentsThrough sets notifications.level to
// warning and checks that a message from a pane and an agent's alert still
// reach the dock: [notifications.agent] decides those, not the level. Both are
// info or success, which the level would otherwise hide. Frames of the dock
// with each are saved under TUIOS_E2E_FRAMES.
//
// Negative control: with the target check taken out of showNotification, the
// pane's OSC 9 message never reaches the dock.
func TestNotificationLevelLetsPanesAndAgentsThrough(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, `[notifications]
level = "warning"
duration = 30
[notifications.agent]
suppress_focused = false
settle_seconds = 0
`)
	if out, err := tuiosCLI(t, base, "new", "lvl", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "lvl", "--name", "lvlagent"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "lvl", "working", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state working: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "lvl"}})
	if err := term.WaitForText("lvlagent", bootTimeout); err != nil {
		t.Fatalf("the client never showed the pane: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)

	if out, err := tuiosCLI(t, base, "send-keys", "-s", "lvl", "--raw", `printf '\033]9;LVLPING build finished\007'; clear`); err != nil {
		t.Fatalf("type the notification: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "lvl", "Enter"); err != nil {
		t.Fatalf("send enter: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(calmDockLines(s), "LVLPING")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a pane's message never reached the dock under level warning: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "notification-level-pane")

	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "lvl", "done", "-m", "LVLDONE tests pass"); err != nil {
		t.Fatalf("set-agent-state done: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(calmDockLines(s), "LVLDONE")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: an agent's alert never reached the dock under level warning: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "notification-level-agent")
}

// TestNotificationLogHiddenOffKeepsTheLogClean sets notifications.level to
// warning and notifications.log_hidden to false. A warning still shows in the
// dock and goes to the log; the info message the level hides goes to neither.
// A frame of the log viewer is saved under TUIOS_E2E_FRAMES.
//
// Negative control: with the log_hidden check taken out of showNotification,
// the log viewer lists "Tiling o..." and the test fails.
func TestNotificationLogHiddenOffKeepsTheLogClean(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[notifications]\nlevel = \"warning\"\nlog_hidden = false\nduration = 30\n\n[keybindings.global]\nclear_scrollback = [\"ctrl+g\"]\n")
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
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}

	newWindow(t, term)
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("send 't': %v", err)
	}
	time.Sleep(time.Second)

	if err := term.SendKeys(tuitest.Ctrl('b'), "D", "l"); err != nil {
		t.Fatalf("open the log viewer: %v", err)
	}
	if err := term.WaitForText("No pane is open", uiTimeout); err != nil {
		t.Fatalf("the log viewer does not list the warning: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "notification-log-hidden-off")
	if strings.Contains(term.Screen().Text(), "Tiling o") {
		t.Fatalf("ASSERTION: the log viewer lists an info message the level hid, with log_hidden off\n%s", term.Snapshot())
	}
}

// TestNotificationLevelShowsWhyAKeyDidNothing sets notifications.level to
// warning, opens the agents view and presses u with nothing to undo. The
// notice that says why the key did nothing is a warning, so the dock still
// shows it. A frame is saved under TUIOS_E2E_FRAMES.
//
// Negative control: with InboxUndo's notice raised as info again, the wait for
// it fails.
func TestNotificationLevelShowsWhyAKeyDidNothing(t *testing.T) {
	term, base, _, _ := startCockpitFleet(t, 200, 55, nil)
	// Set after the start: the fleet's start waits on an info message.
	if out, err := tuiosCLI(t, base, "set-config", "notifications.level", "warning"); err != nil {
		t.Fatalf("set-config notifications.level: %v\n%s", err, out)
	}
	time.Sleep(answerSettle)
	if err := term.SendKeys("u"); err != nil {
		t.Fatalf("u: %v", err)
	}
	if err := term.WaitForText("Nothing to undo", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: under level warning the dock does not say why u did nothing: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "notification-level-refusal")
}

// TestNotificationLevelErrorStillLogsAWarning sets notifications.level to error
// and notifications.log_hidden to false. The dock does not show a warning at
// that level, and the log viewer still lists it: a warning always goes to the
// log, whatever the switch says.
//
// Negative control: with the warning's log line skipped when log_hidden is off,
// the log viewer does not list it and the test fails.
func TestNotificationLevelErrorStillLogsAWarning(t *testing.T) {
	const warning = "No pane is open"
	base := t.TempDir()
	writeConfig(t, base, "[notifications]\nlevel = \"error\"\nlog_hidden = false\n\n[keybindings.global]\nclear_scrollback = [\"ctrl+g\"]\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)

	if err := term.SendKeys(tuitest.Ctrl('g')); err != nil {
		t.Fatalf("send ctrl+g: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(term.Screen().Text(), warning) {
			t.Fatalf("ASSERTION: the dock shows a warning under level error\n%s", term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "D", "l"); err != nil {
		t.Fatalf("open the log viewer: %v", err)
	}
	if err := term.WaitForText(warning, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the log viewer does not list the warning: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "notification-level-error-log")
}
