package input

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// A triple-click arrives as a double-click plus a third press, so copying on
// each release wrote the word and then the line. Every triple-click clobbered
// the clipboard with the wrong text on the way to the right one, and a paste
// landing between the two got the word.
//
// The rule these tests hold to: the clipboard is written once per gesture, with
// the gesture's final reading.

// copyCount returns how many clipboard writes have been reported. Notification
// and write are issued together and only together, so counting the messages
// counts the writes.
func copyCount(o *app.OS) int {
	n := 0
	for _, m := range notificationMessages(o) {
		if strings.HasPrefix(m, "Copied") {
			n++
		}
	}
	return n
}

// settle runs a deferred-copy timer to completion and applies it, returning the
// clipboard command if the gesture was still the current one. Running the tick
// really does wait out the multi-click window, so these tests exercise the same
// delay the user sees.
func settle(t *testing.T, o *app.OS, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg, ok := cmd().(app.PendingCopyMsg)
	if !ok {
		t.Fatalf("expected a deferred copy, got %T", msg)
	}
	return o.HandlePendingCopy(msg.Seq)
}

// TestTripleClickCopiesTheLineExactlyOnce is the regression test for the bug.
// Exactly one, not "the last one is right": the word reaching the clipboard at
// all is the defect, however briefly it sits there.
func TestTripleClickCopiesTheLineExactlyOnce(t *testing.T) {
	const line = "alpha bravo charlie"
	o, win := selectPane(t, line)

	// The double-click half of the gesture.
	pressAt(o, 6, 0)
	pressAt(o, 6, 0)
	wordCopy := release(o, 6, 0)
	if got := selectedText(win); got != "bravo" {
		t.Fatalf("double-click selected %q, want the word: the highlight must be immediate", got)
	}
	if n := copyCount(o); n != 0 {
		t.Fatalf("the double-click wrote to the clipboard %d time(s) before its window closed", n)
	}

	// The third click, arriving inside the window.
	pressAt(o, 6, 0)
	lineCopy := release(o, 6, 0)
	if got := selectedText(win); got != line {
		t.Fatalf("triple-click selected %q, want the whole line", got)
	}

	// The word's timer fires and must find itself superseded.
	if cmd := settle(t, o, wordCopy); cmd != nil {
		t.Error("the word was written to the clipboard even though a third click superseded it")
	}
	// The line's timer fires and writes.
	if cmd := settle(t, o, lineCopy); cmd == nil {
		t.Fatal("the line was never written to the clipboard")
	}

	if n := copyCount(o); n != 1 {
		t.Fatalf("a triple-click produced %d clipboard writes, want exactly 1: %v",
			n, notificationMessages(o))
	}
	if want := len(line); !hasNotificationPrefix(o, "Copied "+itoa(want)) {
		t.Errorf("the surviving write is not the line (%d chars): %v", want, notificationMessages(o))
	}
}

// A double-click that stays a double-click still copies the word, once its
// window has closed.
func TestDoubleClickCopiesTheWordAfterItsWindowCloses(t *testing.T) {
	o, win := selectPane(t, "alpha bravo charlie")

	pressAt(o, 6, 0)
	pressAt(o, 6, 0)
	cmd := release(o, 6, 0)

	if got := selectedText(win); got != "bravo" {
		t.Fatalf("selection = %q, want the word highlighted immediately", got)
	}
	if got := o.PendingCopyText(); got != "bravo" {
		t.Fatalf("pending copy = %q, want the word held back until the window closes", got)
	}
	if n := copyCount(o); n != 0 {
		t.Fatalf("the word was written %d time(s) before the window closed", n)
	}

	if settled := settle(t, o, cmd); settled == nil {
		t.Fatal("the word never reached the clipboard")
	}
	if n := copyCount(o); n != 1 {
		t.Errorf("a double-click produced %d clipboard writes, want 1: %v", n, notificationMessages(o))
	}
}

// A drag has nothing to wait out: the button coming up ended it and no later
// click can reinterpret it. Making the user wait for that would be latency for
// nothing.
func TestDragReleaseCopiesWithoutWaiting(t *testing.T) {
	o, _ := selectPane(t, "alpha bravo charlie")

	pressAt(o, 0, 0)
	dragTo(o, 10, 0)
	cmd := release(o, 10, 0)

	if cmd == nil {
		t.Fatal("a drag release did not copy")
	}
	if _, deferred := cmd().(app.PendingCopyMsg); deferred {
		t.Error("a drag release was deferred behind the multi-click window; only a " +
			"multi-click can be superseded")
	}
	if got := o.PendingCopyText(); got != "" {
		t.Errorf("pending copy = %q, want nothing held back after a drag", got)
	}
	if n := copyCount(o); n != 1 {
		t.Errorf("a drag release produced %d clipboard writes, want 1 immediately: %v",
			n, notificationMessages(o))
	}
}

// Dragging out from the second click of a double-click is a common way to
// select several words. It is a drag, so it copies at once, and what it copies
// is what the drag ended on rather than the word the double-click started with.
func TestDragFromTheSecondClickCopiesTheDraggedRange(t *testing.T) {
	o, win := selectPane(t, "alpha bravo charlie")

	pressAt(o, 6, 0)
	pressAt(o, 6, 0)
	dragTo(o, 18, 0)
	cmd := release(o, 18, 0)

	if _, deferred := cmd().(app.PendingCopyMsg); deferred {
		t.Error("a drag out of a double-click was deferred")
	}
	if got, want := selectedText(win), "bravo charlie"; got != want {
		t.Errorf("selection = %q, want %q", got, want)
	}
	if n := copyCount(o); n != 1 {
		t.Errorf("produced %d clipboard writes, want 1: %v", n, notificationMessages(o))
	}
}

// The window restarts on every click, so an unhurried triple-click resolves to
// the line rather than copying the word because the timer expired mid-gesture.
func TestASlowTripleClickStillResolvesToTheLine(t *testing.T) {
	const line = "alpha bravo charlie"
	o, _ := selectPane(t, line)

	pressAt(o, 6, 0)
	pressAt(o, 6, 0)
	wordCopy := release(o, 6, 0)

	// Late, but still inside the window.
	time.Sleep(multiClickInterval * 2 / 3)

	pressAt(o, 6, 0)
	if got := o.PendingCopyText(); got != "" {
		t.Errorf("pending copy = %q after the third press, want it retired", got)
	}
	lineCopy := release(o, 6, 0)
	if got := o.PendingCopyText(); got != line {
		t.Fatalf("pending copy = %q, want the whole line: the third click did not land as a third click", got)
	}

	if cmd := settle(t, o, wordCopy); cmd != nil {
		t.Error("the word was written despite the third click")
	}
	if cmd := settle(t, o, lineCopy); cmd == nil {
		t.Fatal("the line was never written")
	}
	if n := copyCount(o); n != 1 {
		t.Errorf("produced %d clipboard writes, want 1: %v", n, notificationMessages(o))
	}
}

// A fourth click restarts the cycle at a one-character selection, which is not
// something a click copies. The clipboard is therefore left holding whatever it
// had, rather than the line from the third click, because the clipboard always
// ends up agreeing with what is highlighted.
func TestAFourthClickLeavesTheClipboardAlone(t *testing.T) {
	o, _ := selectPane(t, "alpha bravo charlie")

	pressAt(o, 6, 0)
	pressAt(o, 6, 0)
	pressAt(o, 6, 0)
	lineCopy := release(o, 6, 0)

	pressAt(o, 6, 0)
	release(o, 6, 0)

	if cmd := settle(t, o, lineCopy); cmd != nil {
		t.Error("the line was written after a fourth click reduced the selection to one character")
	}
	if n := copyCount(o); n != 0 {
		t.Errorf("four clicks produced %d clipboard writes, want none: %v", n, notificationMessages(o))
	}
}

// The interval is no longer only a detection threshold: it is how long the user
// waits for the clipboard after a double-click. Guarding the range keeps it from
// drifting back up to the half second that was fine when it cost nothing.
func TestMultiClickIntervalStaysResponsive(t *testing.T) {
	if multiClickInterval < 250*time.Millisecond || multiClickInterval > 350*time.Millisecond {
		t.Errorf("multiClickInterval = %v, want 250-350ms: below that a deliberate "+
			"double-click starts being read as two singles, above it the clipboard "+
			"visibly lags the selection", multiClickInterval)
	}
}

// itoa keeps the assertions above readable without pulling strconv into every
// message.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
