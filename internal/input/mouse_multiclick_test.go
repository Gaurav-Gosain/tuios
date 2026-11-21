package input

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func copyCount(o *app.OS) int {
	n := 0
	for _, m := range notificationMessages(o) {
		if strings.HasPrefix(m, "Copied") {
			n++
		}
	}
	return n
}

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
	o, win := selectPane(t, line)

	pressAt(o, 6, 0)
	pressAt(o, 6, 0)
	wordCopy := release(o, 6, 0)

	win.LastClickTime = time.Now().Add(-testClickInterval * 2 / 3)

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

func notificationMessages(o *app.OS) []string {
	msgs := make([]string, 0, len(o.Notifications))
	for _, n := range o.Notifications {
		msgs = append(msgs, n.Message)
	}
	return msgs
}
