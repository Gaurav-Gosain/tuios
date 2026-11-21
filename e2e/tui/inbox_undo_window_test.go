package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestUndoOfferEndsWithTheUndoWindow sets the undo window to one second and
// info messages to fifteen, and closes two errored items in the agents view.
// The dock's "Dismissed ... u undoes." and "Snoozed ... undoes." must leave
// the dock with the window, not stay up for the fifteen seconds every other
// info message gets, because after the window u restores nothing. u pressed
// then must say so and name the window that is set: "Nothing to undo in the
// last 1s.", with the snoozed item still snoozed. The dismiss comes first and
// the snooze is made on the last item left. Frames of the dock after each
// offer is gone and of the warning are saved with saveFrame.
//
// How this could pass wrongly, written down first:
//   - An offer that is never shown would pass "gone". Each row waits for the
//     offer's head on screen before it waits for it to leave.
//   - A key could be sent before the list is drawn, or d before j has moved
//     the selection, and close the wrong item or none. The test waits for both
//     items and the count, then for the e2e-undo-b row to be the selected one,
//     and each gone check is made on a frame that still lists the other item,
//     or says one is snoozed.
//   - A later message could take the dock's block over an offer and hide it,
//     which would read as gone. Each gone check also needs a dock row with no
//     message block (no notification cap on it), and the config turns the
//     dock's agent alerts off, so errored items raise none. The test first
//     waits for the attach's own mode message to leave the dock.
//   - notifications.duration could go unread, which would leave the base
//     offer at five seconds: still past the four seconds each gone check
//     waits, so the test still fails on main.
//   - A TUIOS_E2E_TIME_SCALE of 4 or more stretches the four second wait past
//     the fifteen seconds the base offer lives, and main would pass.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: main, with the dismiss
// offer still up after four seconds, fails; so do the limit in
// showNotification cut, the dismiss offer put back on ShowNotification, and
// the old warning text put back.
func TestUndoOfferEndsWithTheUndoWindow(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[agents]\nundo_window_seconds = 1\n\n[notifications]\nduration = 15\n\n[notifications.agent]\ndock = false\n")
	term := attachClientIn(t, base, nil)

	for _, s := range []struct{ name, message string }{
		{"e2e-undo-a", "build failed on main"},
		{"e2e-undo-b", "lint failed on main"},
	} {
		if out, err := tuiosCLI(t, base, "new", s.name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", s.name, err, out)
		}
		if out, err := tuiosCLI(t, base, "set-agent-state", "-s", s.name, "errored",
			"--harness", "claude-code", "-m", s.message); err != nil {
			t.Fatalf("set-agent-state errored in %s: %v\n%s", s.name, err, out)
		}
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "both errored items in the agents view", "2 needs you", "build failed on main", "lint failed on main")

	dockEmpty := func(s tuitest.Screen) bool { return !strings.ContainsAny(dockRow(s), "▎▌▊") }
	waitUntil(t, "the dock shows no message", 25*time.Second, func() (bool, string) {
		s := term.Screen()
		return dockEmpty(s), s.Text()
	})

	gone := func(what, head, mark string) {
		t.Helper()
		waitUntil(t, what, 4*time.Second, func() (bool, string) {
			s := term.Screen()
			text := s.Text()
			return !strings.Contains(text, head) && dockEmpty(s) && strings.Contains(text, mark), text
		})
	}

	if err := term.SendKeys("j"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the e2e-undo-b item is selected", uiTimeout, func() (bool, string) {
		text := term.Screen().Text()
		for line := range strings.SplitSeq(text, "\n") {
			if strings.HasPrefix(strings.TrimLeft(line, " "), "▌") && strings.Contains(line, "e2e-undo-b") {
				return true, text
			}
		}
		return false, text
	})
	if err := term.SendKeys("d"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the dismiss offer", "Dismissed ")
	gone("the dismiss offer leaves the dock with the undo window", "Dismissed ", "build failed on main")
	saveFrame(t, term, "undo-offer-dismiss-gone")

	if err := term.SendKeys("z"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the snooze picker", "2 1h")
	if err := term.SendKeys("2"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the snooze offer", "Snoozed ")
	gone("the snooze offer leaves the dock with the undo window", "Snoozed ", "1 snoozed")
	saveFrame(t, term, "undo-offer-snooze-gone")

	if err := term.SendKeys("u"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the warning naming the undo window, with the item still snoozed", "Nothing to undo in the last 1s.", "1 snoozed")
	saveFrame(t, term, "undo-nothing")
	alive(t, term, "after the undo offers and the warning")
}
