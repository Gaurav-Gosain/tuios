package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Reflow through the daemon and the client. A line on the screen that is
// wider than a narrower pane wraps instead of losing its tail, and joins
// back when the pane widens. The client's emulator resizes on every step of
// a divider drag and the daemon's only when the drag ends, so a client that
// cut a line on the way would disagree with the daemon after it.

// TestScrollbackResizeNarrowKeepsScreenLine: a line on the screen, wider than
// a narrower pane, must survive narrowing and widening again. The command echo
// carries a space after LONGSTART-, so only the printed line matches.
func TestScrollbackResizeNarrowKeepsScreenLine(t *testing.T) {
	term, base, w := scrollbackResizeSession(t, "sb-narrow", 140, 30)
	full := "LONGSTART-" + longBody + "-LONGEND"
	if err := paneSend(base, "sb-narrow", w.ID, "clear; printf '%s%s\\n' LONGSTART- "+longBody+"-LONGEND\n"); err != nil {
		t.Fatal(err)
	}
	waitDaemonText(t, base, "sb-narrow", w.ID, full)
	wide, _ := sbGridSize(t, base, "sb-narrow", w.ID)
	if wide <= len(full) {
		t.Fatalf("fixture: the pane is %d wide, the line %d; it must start whole", wide, len(full))
	}

	if err := term.Resize(50, 30); err != nil {
		t.Fatal(err)
	}
	waitPaneWidth(t, base, "sb-narrow", func(w, _ int) bool { return w < 60 }, "narrow")
	time.Sleep(500 * time.Millisecond)
	if err := term.Resize(140, 30); err != nil {
		t.Fatal(err)
	}
	waitPaneWidth(t, base, "sb-narrow", func(w, _ int) bool { return w == wide }, "widen")
	time.Sleep(500 * time.Millisecond)

	hist, err := daemonScrollback(base, "sb-narrow", w.ID, 5000)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(hist, "\n")
	if !strings.Contains(joined, full) {
		t.Errorf("after narrowing to 50 and widening back, the daemon lost the tail of a screen line.\n"+
			"want %q\ndaemon holds:\n%s", full, lastLines(hist, 12))
	}
}

// TestScrollbackResizeDragKeepsClientInStepWithDaemon: a divider drag
// resizes the client's emulator on every motion (ResizeVisual) and the
// daemon's once, when the gesture ends. A drag that narrows a pane and brings
// it back to its width tells the daemon nothing, so whatever the client's
// emulator lost on the way is a difference between the client and the daemon.
func TestScrollbackResizeDragKeepsClientInStepWithDaemon(t *testing.T) {
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 160, rows: 30, args: []string{"new", "sbdrag"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "drag setup")
	enableTiling(t, term)
	time.Sleep(time.Second)
	wl, err := daemonWindows(base, "sbdrag")
	if err != nil || len(wl.Windows) != 2 {
		t.Fatalf("list-windows: %v %+v", err, wl)
	}
	// Both panes print the line, so whichever is on the left holds it.
	const body = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMN"
	full := "LONGSTART-" + body + "-LONGEND"
	for _, w := range wl.Windows {
		if err := paneSend(base, "sbdrag", w.ID, "clear; printf '%s%s\\n' LONGSTART- "+body+"-LONGEND\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "-LONGEND") >= 2
	}, shellTimeout); err != nil {
		t.Fatalf("the client never drew the line in both panes: %v\n%s", err, term.Snapshot())
	}
	div := sbDivider(t, term)
	row := 10
	mousePress(t, term, div, row, tuitest.MouseLeft, 0)
	for c := div - 1; c >= div-45; c-- {
		mouseMotion(t, term, c, row, tuitest.MouseLeft, 0)
	}
	time.Sleep(500 * time.Millisecond)
	t.Logf("mid-drag\n%s", term.Snapshot())
	for c := div - 44; c <= div; c++ {
		mouseMotion(t, term, c, row, tuitest.MouseLeft, 0)
	}
	mouseRelease(t, term, div, row, tuitest.MouseLeft, 0)
	time.Sleep(2 * time.Second)
	t.Logf("after\n%s", term.Snapshot())

	clientHas := strings.Count(term.Screen().Text(), "-LONGEND")
	daemonHas := 0
	for _, w := range wl.Windows {
		hist, _ := daemonScrollback(base, "sbdrag", w.ID, 5000)
		if strings.Contains(strings.Join(hist, "\n"), full) {
			daemonHas++
		}
	}
	t.Logf("panes whose line is whole: daemon %d, client %d", daemonHas, clientHas)
	if clientHas != daemonHas {
		t.Errorf("the client and the daemon disagree about a line after a drag that ended at the start size")
	}
	if clientHas != 2 {
		t.Errorf("the client lost the tail of a screen line across a drag that ended where it started")
	}
}

// TestScrollbackResizeDragResizesDaemonOnce measures what a divider drag
// costs the daemon. The pane's revision counts the bytes its emulator has
// consumed plus the resizes it has applied, and the pane runs sleep, so it
// prints nothing during the gesture: the change in revision is the number of
// resizes the daemon's emulator applied for 30 motion steps.
func TestScrollbackResizeDragResizesDaemonOnce(t *testing.T) {
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 160, rows: 30, args: []string{"new", "sbdrag1"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "drag setup")
	enableTiling(t, term)
	time.Sleep(time.Second)
	wl, err := daemonWindows(base, "sbdrag1")
	if err != nil || len(wl.Windows) != 2 {
		t.Fatalf("list-windows: %v %+v", err, wl)
	}
	for _, w := range wl.Windows {
		if err := paneSend(base, "sbdrag1", w.ID, "clear; sleep 60\n"); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(time.Second)
	rev := func() (sum int64) {
		for _, w := range wl.Windows {
			got, err := daemonJSON[struct {
				Revision int64 `json:"revision"`
			}](base, "capture-pane", "-s", "sbdrag1", "-w", w.ID)
			if err != nil {
				t.Fatal(err)
			}
			sum += got.Revision
		}
		return sum
	}
	before := rev()
	div := sbDivider(t, term)
	const steps = 30
	mousePress(t, term, div, 10, tuitest.MouseLeft, 0)
	for i := 1; i <= steps; i++ {
		mouseMotion(t, term, div-i, 10, tuitest.MouseLeft, 0)
		time.Sleep(20 * time.Millisecond)
	}
	mouseRelease(t, term, div-steps, 10, tuitest.MouseLeft, 0)
	time.Sleep(2 * time.Second)
	after := rev()
	t.Logf("%d motion steps across two panes: the daemon emulators applied %d resizes", steps, after-before)
	if after-before > 2 {
		t.Errorf("a drag of %d steps cost the daemon %d emulator resizes, want one per pane", steps, after-before)
	}
}

// sbDivider finds the column of the division between two side-by-side panes.
func sbDivider(t *testing.T, term *tuitest.Terminal) int {
	t.Helper()
	s := term.Screen()
	line := []rune(s.Line(10))
	cols, _ := s.Size()
	for c := 5; c < min(len(line), cols)-5; c++ {
		if isWindowBorder(line[c]) {
			return c
		}
	}
	t.Fatalf("no divider on row 10\n%s", term.Snapshot())
	return 0
}
