package tuie2e

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The host terminal's cursor shape is not part of the grid, so no screen
// assertion can reach it. What the host is told is DECSCUSR on the wire, and the
// shape it is showing at any moment is the last DECSCUSR it was sent. These
// tests read that off the recorded PTY stream, which is the same thing the
// user's eye reports.
//
// DECSCUSR is CSI Ps SP q: 0/1 blinking block, 2 steady block, 3 blinking
// underline, 4 steady underline, 5 blinking bar, 6 steady bar. Only the shape is
// asserted; whether tuios also asks for blinking is a separate question from the
// reported bug, which is a bar turning into a block.
var decscusrRE = regexp.MustCompile(`\x1b\[([0-9]{0,2}) q`)

type cursorShape string

const (
	shapeBlock     cursorShape = "block"
	shapeUnderline cursorShape = "underline"
	shapeBar       cursorShape = "bar"
	shapeNone      cursorShape = "none"
)

func shapeOfDECSCUSR(param string) cursorShape {
	n := 1
	if param != "" {
		v, err := strconv.Atoi(param)
		if err != nil {
			return shapeBlock
		}
		n = v
	}
	switch n {
	case 3, 4:
		return shapeUnderline
	case 5, 6:
		return shapeBar
	default:
		return shapeBlock
	}
}

// hostCursorShape is the shape the host is currently showing: the last DECSCUSR
// anywhere in the stream so far.
func hostCursorShape(stream []byte) cursorShape {
	m := decscusrRE.FindAllSubmatch(stream, -1)
	if len(m) == 0 {
		return shapeNone
	}
	return shapeOfDECSCUSR(string(m[len(m)-1][1]))
}

// cursorTrail renders the DECSCUSR the host was sent, attributed to the phase it
// arrived in, so a failure says which pane or which transition stomped the shape
// rather than only that it is wrong now. A pane writing its cursor on a timer
// produces hundreds of identical lines, so runs are collapsed to one.
func cursorTrail(stream []byte) string {
	var b strings.Builder
	emit := func(name string, chunk []byte) {
		last, runs := "", 0
		flush := func() {
			if runs == 1 {
				fmt.Fprintf(&b, "  %-16s %s\n", name, last)
			} else if runs > 1 {
				fmt.Fprintf(&b, "  %-16s %s (x%d)\n", name, last, runs)
			}
		}
		for _, m := range decscusrRE.FindAllSubmatch(chunk, -1) {
			line := fmt.Sprintf("CSI %q SP q -> %s", m[1], shapeOfDECSCUSR(string(m[1])))
			if line == last {
				runs++
				continue
			}
			flush()
			last, runs = line, 1
		}
		flush()
	}
	prevEnd, prevName := 0, "boot"
	for _, m := range markRE.FindAllSubmatchIndex(stream, -1) {
		emit(prevName, stream[prevEnd:m[0]])
		prevEnd, prevName = m[1], string(stream[m[2]:m[3]])
	}
	emit(prevName, stream[prevEnd:])
	if b.Len() == 0 {
		return "  (the host was never sent a cursor shape)\n"
	}
	return b.String()
}

// waitCursorShape blocks until the host is showing want, and fails with the
// whole trail if it never does. Polling rather than sampling once keeps the
// assertion honest in both directions: a correct binary has a bounded window to
// land the sequence in, and one that never sends it still fails.
func waitCursorShape(t *testing.T, stream *hostStream, want cursorShape, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	got := shapeNone
	for time.Now().Before(deadline) {
		if got = hostCursorShape(stream.bytes()); got == want {
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatalf("%s: the host cursor is %s, want %s\nDECSCUSR the host was sent:\n%s",
		what, got, want, cursorTrail(stream.bytes()))
}

// decscusrCount is how many cursor shapes the host was sent in the given slice
// of the stream.
func decscusrCount(stream []byte) int {
	return len(decscusrRE.FindAllIndex(stream, -1))
}

// barPane puts the focused pane's shell into a steady bar cursor, the shape fish
// and vim's insert mode ask for and the one the report says is lost.
func barPane(t *testing.T, term *tuitest.Terminal, marker string) {
	t.Helper()
	runInShell(t, term, `printf '\033[6 q'; echo `+marker, marker, shellTimeout)
}

// TestCursorShapeSurvivesNeighbourAndSwitches is the reported bug on a single
// client: a pane that asked for a bar keeps it while the pane beside it is
// writing, across a trip through window management mode, and across a trip to
// another workspace.
//
// The neighbour is what makes the bug reproducible rather than occasional. An
// unfocused pane still produces output, and if its cursor sequences reach the
// host they replace the shape of the pane the user is actually typing in.
// Nothing repaints that shape afterwards, because the focused pane's own shape
// has not changed, so the block sticks until the guest happens to set its cursor
// again. That is the "sometimes" in the report.
func TestCursorShapeSurvivesNeighbourAndSwitches(t *testing.T) {
	stream := &hostStream{}
	term, _ := start(t, startOpts{cols: 120, rows: 40, out: stream})
	waitBoot(t, term)

	// The neighbour: a background job in its own pane that keeps asking for a
	// block cursor, the way a shell repainting its prompt does.
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term,
		`(while true; do printf '\033[2 q'; sleep 0.2; done) & echo STOMPER`,
		"STOMPER", shellTimeout)
	leaveTerminalMode(t, term)

	// The pane under test, focused, asking for a bar.
	newWindow(t, term)
	enableTiling(t, term)
	waitWindowCount(t, term, 2, "two tiled panes")
	enterTerminalMode(t, term)
	stream.mark("bar-set")
	barPane(t, term, "BARPANE")
	waitCursorShape(t, stream, shapeBar, "right after the pane asked for a bar")

	stream.mark("neighbour")
	// Long enough for the neighbour to write several times.
	before := decscusrCount(stream.bytes())
	time.Sleep(time.Second)
	waitCursorShape(t, stream, shapeBar, "while the neighbouring pane is writing")

	// Holding the shape by sending it on every frame would be correct on screen
	// and wrong on the wire: this pane repaints for each of the neighbour's
	// writes, and tuios counts bytes per frame. The shape has not changed, so
	// the host should hear nothing about it.
	if n := decscusrCount(stream.bytes()) - before; n > 2 {
		t.Errorf("the host was sent %d cursor shapes in a second of a neighbour writing, want at most 2:\n%s",
			n, cursorTrail(stream.bytes()))
	}

	stream.mark("mode-switch")
	leaveTerminalMode(t, term)
	time.Sleep(600 * time.Millisecond)
	enterTerminalMode(t, term)
	waitCursorShape(t, stream, shapeBar, "back in terminal mode")

	stream.mark("workspace-switch")
	leaveTerminalMode(t, term)
	if err := term.SendKeys("2"); err != nil {
		t.Fatalf("switch to workspace 2: %v", err)
	}
	time.Sleep(600 * time.Millisecond)
	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("switch back to workspace 1: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 2
	}, uiTimeout); err != nil {
		t.Fatalf("never got back to workspace 1: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	waitCursorShape(t, stream, shapeBar, "back on the pane's own workspace")

	alive(t, term, "after the cursor shape checks")
}

// TestCursorShapeSurvivesReattach is the detach half. The daemon holds the pane
// and keeps an emulator fed, so the cursor shape the guest asked for is state
// the daemon knows and the reattaching client has to be told, the same as the
// cursor position and the pen. A client that is not told rebuilds the pane with
// a default block.
func TestCursorShapeSurvivesReattach(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-cursor", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	firstStream := &hostStream{}
	first := startIn(t, base, startOpts{args: []string{"attach", "e2e-cursor"}, out: firstStream})
	if err := first.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("first client never attached: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	firstStream.mark("bar-set")
	barPane(t, first, "BARBEFORE")
	waitCursorShape(t, firstStream, shapeBar, "before the detach")

	if err := first.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("send leader d: %v", err)
	}
	waitExit(t, first, "after leader d")
	if !sessionListed(t, base, "e2e-cursor") {
		out, _ := tuiosCLI(t, base, "ls")
		t.Fatalf("the session did not survive the detach\nls:\n%s", out)
	}

	secondStream := &hostStream{}
	second := startIn(t, base, startOpts{args: []string{"attach", "e2e-cursor"}, out: secondStream})
	if err := second.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the reattached client never got its window back: %v\n%s", err, second.Snapshot())
	}
	if err := second.WaitForText("BARBEFORE", shellTimeout); err != nil {
		t.Fatalf("pane content did not survive the detach: %v\n%s", err, second.Snapshot())
	}
	// The guest emits nothing further, so the shape can only come from what the
	// daemon told this client about the pane.
	waitCursorShape(t, secondStream, shapeBar, "after the reattach")
	alive(t, second, "after reattach")
}
