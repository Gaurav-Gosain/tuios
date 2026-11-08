package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// closeQuestion is the start of the one-pane close dialog's question.
const closeQuestion = "Close pane '"

// busyPanes opens two panes and runs sleep in the focused one, so it runs
// something besides its shell, and waits until tuios sees it there. It
// returns in window mode.
func busyPanes(t *testing.T, config string) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, config)
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "two panes")
	enterTerminalMode(t, term)
	if err := term.SendKeys("sleep 600", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	// The daemon names the foreground program on its detector poll.
	time.Sleep(2500 * time.Millisecond)
	leaveTerminalMode(t, term)
	time.Sleep(insertGuard)
	return term
}

// busyCloseDot is the screen cell of the close control of the pane that
// shows "sleep 600": the first dot of the title row above that line, to its
// left. The default controls are close, minimize, zoom, in that order.
func busyCloseDot(t *testing.T, term *tuitest.Terminal) (col, row int) {
	t.Helper()
	s := term.Screen()
	r := lastRowWith(s, "sleep 600")
	if r < 0 {
		t.Fatalf("no sleep 600 on screen\n%s", term.Snapshot())
	}
	c := colOfText(s, r, "sleep 600")
	cols, _ := s.Size()
	for tr := r - 1; tr >= 0; tr-- {
		if !strings.Contains(s.Line(tr), "●") {
			continue
		}
		best := -1
		for x := 0; x < cols && x <= c; x++ {
			if s.Cell(x, tr).Content == "●" && (best < 0 || c-x < c-best) {
				if x+4 < cols && s.Cell(x+2, tr).Content == "●" && s.Cell(x+4, tr).Content == "●" {
					best = x
				}
			}
		}
		if best >= 0 {
			return best, tr
		}
	}
	t.Fatalf("no close control above the busy pane\n%s", term.Snapshot())
	return 0, 0
}

// noDialogFor fails when the close dialog shows within d.
func noDialogFor(t *testing.T, term *tuitest.Terminal, d time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(term.Screen().Text(), closeQuestion) {
			t.Fatalf("ASSERTION: %s: the close dialog showed\n%s", what, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestConfirmCloseAsksForABusyPane is issue 1669. With
// appearance.confirm_close = "busy", x on a pane that runs something besides
// its shell asks first, in the dialog a close link opens. esc keeps the pane,
// and the Close pane row closes it. A pane at its shell prompt closes at once.
func TestConfirmCloseAsksForABusyPane(t *testing.T) {
	term := busyPanes(t, "[appearance]\nconfirm_close = \"busy\"\n")

	sendKeys(t, term, "x")
	if err := term.WaitForText(closeQuestion, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: x on a busy pane did not ask: %v\n%s", err, term.Snapshot())
	}
	if n := countWindows(term.Screen()); n != 2 {
		t.Fatalf("ASSERTION: the pane closed before the answer: %d panes\n%s", n, term.Snapshot())
	}
	// For one pane the dialog says what runs in it.
	if err := term.WaitForText("is running sleep", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dialog does not say what runs in the pane: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "busy-asks")

	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), closeQuestion) }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the dialog: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	waitWindowCount(t, term, 2, "esc keeps the pane")

	// The title bar's close control follows the same rule.
	col, row := busyCloseDot(t, term)
	mousePress(t, term, col, row, tuitest.MouseLeft, 0)
	mouseRelease(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText(closeQuestion, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the close control on a busy pane did not ask: %v\n%s", err, term.Snapshot())
	}
	if n := countWindows(term.Screen()); n != 2 {
		t.Fatalf("ASSERTION: the close control closed the pane before the answer: %d panes\n%s", n, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), closeQuestion) }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the dialog: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	waitWindowCount(t, term, 2, "esc keeps the pane after the close control")

	sendKeys(t, term, "x")
	if err := term.WaitForText(closeQuestion, uiTimeout); err != nil {
		t.Fatalf("x did not ask again: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Down, tuitest.Enter)
	waitWindowCount(t, term, 1, "the Close pane row closes it")

	// The other pane is at its prompt: it closes at once.
	time.Sleep(insertGuard)
	sendKeys(t, term, "x")
	noDialogFor(t, term, 500*time.Millisecond, "x on a pane at its prompt")
	waitWindowCount(t, term, 0, "a pane at its prompt closes at once")
}

// TestConfirmCloseAlwaysAsks: confirm_close = "always" asks for a pane at its
// prompt as well.
func TestConfirmCloseAlwaysAsks(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nconfirm_close = \"always\"\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "one pane")
	time.Sleep(insertGuard)
	sendKeys(t, term, "x")
	if err := term.WaitForText(closeQuestion, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: x on a pane at its prompt did not ask: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Down, tuitest.Enter)
	waitWindowCount(t, term, 0, "the Close pane row closes it")
}

// TestCloseDoesNotAskByDefault keeps today's behaviour: with no
// confirm_close, x closes a busy pane at once.
func TestCloseDoesNotAskByDefault(t *testing.T) {
	term := busyPanes(t, "")
	sendKeys(t, term, "x")
	noDialogFor(t, term, 500*time.Millisecond, "x with confirm_close unset")
	waitWindowCount(t, term, 1, "x closes the busy pane at once")
}
