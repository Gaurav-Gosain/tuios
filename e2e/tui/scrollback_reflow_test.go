package tuie2e

import (
	"fmt"
	"strconv"
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

// TestScrollbackResizeKeepsOutputUnderALoneA: a shell that marks only the
// start of its prompt with OSC 133 A, as foot's minimal PS1 does, leaves the
// mark standing over the output of every command. The output under it must
// reflow like any other text. Freezing it as a prompt cut the line at the
// narrow width for good.
func TestScrollbackResizeKeepsOutputUnderALoneA(t *testing.T) {
	term, base, w := scrollbackResizeSession(t, "sb-lonea", 140, 30)
	body := strings.Repeat("abcdefghij", 7)
	full := "LONGSTART-" + body + "-LONGEND"
	// The mark, the line, and a sleep so the shell draws no prompt (and no
	// new mark) while the pane is resized.
	emit := "\x1b[2J\x1b[H\x1b]133;A\x07$ cmd\r\n" + full + "\r\n"
	cmd := strings.TrimSuffix(paneEmitCmd(emit), "\n") + "; sleep 60\n"
	if err := paneSend(base, "sb-lonea", w.ID, cmd); err != nil {
		t.Fatal(err)
	}
	waitDaemonText(t, base, "sb-lonea", w.ID, full)
	wide, _ := sbGridSize(t, base, "sb-lonea", w.ID)
	if err := term.Resize(50, 30); err != nil {
		t.Fatal(err)
	}
	waitPaneWidth(t, base, "sb-lonea", func(w, _ int) bool { return w < 60 }, "narrow")
	time.Sleep(500 * time.Millisecond)
	if err := term.Resize(140, 30); err != nil {
		t.Fatal(err)
	}
	waitPaneWidth(t, base, "sb-lonea", func(c, _ int) bool { return c == wide }, "widen")
	time.Sleep(500 * time.Millisecond)
	hist, err := daemonScrollback(base, "sb-lonea", w.ID, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(hist, "\n"), full) {
		t.Errorf("output under a lone OSC 133 A lost its tail across 140, 50 and 140 columns:\n%s", lastLines(hist, 12))
	}
}

// dragPanes starts a client with two tiled panes, runs cmd in both, and
// waits for the client to draw ready in both.
func dragPanes(t *testing.T, session, cmd, ready string) (*tuitest.Terminal, string, daemonWindowList) {
	t.Helper()
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 160, rows: 30, args: []string{"new", session}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "drag setup")
	enableTiling(t, term)
	time.Sleep(time.Second)
	wl, err := daemonWindows(base, session)
	if err != nil || len(wl.Windows) != 2 {
		t.Fatalf("list-windows: %v %+v", err, wl)
	}
	for _, w := range wl.Windows {
		if err := paneSend(base, session, w.ID, cmd); err != nil {
			t.Fatal(err)
		}
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), ready) >= 2
	}, shellTimeout); err != nil {
		t.Fatalf("the client never drew %q in both panes: %v\n%s", ready, err, term.Snapshot())
	}
	return term, base, wl
}

// dragDivider drags the divider between the two panes left by by columns, and
// then to end columns left of where it started.
func dragDivider(t *testing.T, term *tuitest.Terminal, by, end int) {
	t.Helper()
	div := sbDivider(t, term)
	const row = 10
	mousePress(t, term, div, row, tuitest.MouseLeft, 0)
	for c := div - 1; c >= div-by; c-- {
		mouseMotion(t, term, c, row, tuitest.MouseLeft, 0)
	}
	time.Sleep(300 * time.Millisecond)
	for c := div - by + 1; c <= div-end; c++ {
		mouseMotion(t, term, c, row, tuitest.MouseLeft, 0)
	}
	mouseRelease(t, term, div-end, row, tuitest.MouseLeft, 0)
	time.Sleep(2 * time.Second)
}

// TestScrollbackResizeDragKeepsATypedCommand: a shell's open prompt, marked
// with OSC 133 A and B, with a command typed on it. A drag that narrows the
// pane and brings it back sends the shell no SIGWINCH, because the size it
// ends at is the size the shell already has, so nothing repaints the prompt.
// The client must still show the whole command, as the daemon holds it.
func TestScrollbackResizeDragKeepsATypedCommand(t *testing.T) {
	typed := "echo TYPED-abcdefghijklmnopqrst"
	emit := "\x1b[2J\x1b[H\x1b]133;A\x07$ \x1b]133;B\x07" + typed
	cmd := strings.TrimSuffix(paneEmitCmd(emit), "\n") + "; sleep 60\n"
	term, _, _ := dragPanes(t, "sbtyped", cmd, typed)
	dragDivider(t, term, 60, 0)
	if got := strings.Count(term.Screen().Text(), "$ "+typed); got != 2 {
		t.Errorf("after a drag that ended where it started, %d of 2 panes show the whole typed command\n%s", got, term.Snapshot())
	}
}

// TestScrollbackResizeDragAgreesWithTheDaemonAroundAParkedCursor: a program
// that draws long lines and a footer and parks its cursor above the footer,
// as an agent's input box does. Whatever the drag does on the way, the client
// must end up showing what the daemon holds: the same lines whole, the same
// lines cut by the screen's top, and the footer. A client that reflowed at
// each motion step and the daemon once at the end laid these out apart.
func TestScrollbackResizeDragAgreesWithTheDaemonAroundAParkedCursor(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  int // columns left of the start the drag ends at
	}{
		{"ends where it started", 0},
		{"ends narrower", 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			b.WriteString("\x1b[2J\x1b[H")
			for i := 1; i <= 14; i++ {
				fmt.Fprintf(&b, "PK%02d-%s-END\r\n", i, strings.Repeat("abcdefghij", 5))
			}
			b.WriteString("\x1b[28;1HFOOTERMARK\x1b[20;3H")
			cmd := strings.TrimSuffix(paneEmitCmd(b.String()), "\n") + "; sleep 60\n"
			session := "sbpark" + strconv.Itoa(tc.end)
			term, base, wl := dragPanes(t, session, cmd, "FOOTERMARK")
			dragDivider(t, term, 45, tc.end)

			// Which marker lines each side shows whole on screen.
			whole := func(text string) map[string]int {
				got := map[string]int{}
				for i := 1; i <= 14; i++ {
					tag := fmt.Sprintf("PK%02d-%s-END", i, strings.Repeat("abcdefghij", 5))
					got[tag[:4]] = strings.Count(text, tag)
				}
				got["FOOTER"] = strings.Count(text, "FOOTERMARK")
				return got
			}
			var daemonText strings.Builder
			for _, w := range wl.Windows {
				got, err := daemonJSON[struct {
					Content string `json:"content"`
				}](base, "capture-pane", "-s", session, "-w", w.ID)
				if err != nil {
					t.Fatal(err)
				}
				// The capture gives the screen as lines; the client's screen
				// has them side by side, so only whole-line counts compare.
				daemonText.WriteString(got.Content)
				daemonText.WriteByte('\n')
			}
			client, daemon := whole(term.Screen().Text()), whole(daemonText.String())
			if fmt.Sprint(client) != fmt.Sprint(daemon) {
				t.Errorf("the client and the daemon show different lines after the drag\nclient %v\ndaemon %v\n%s",
					client, daemon, term.Snapshot())
			}
		})
	}
}
