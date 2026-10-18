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
	runInShell(t, term, `printf '\033[6 q'; echo `+splitMarker(marker), marker, shellTimeout)
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
		`(while true; do printf '\033[2 q'; sleep 0.2; done) & echo STOM""PER`,
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
	// alt+N, which is what switch_workspace_N is bound to. This used to send a
	// bare "2" and a bare "1", which have never switched a workspace: the
	// digits were snap_corner_N, and makeSnapCornerHandler returns early while
	// tiling is on, which this test turns on. So the step did nothing at all
	// and the shape survived a switch that never happened. The digits select a
	// window now, which moved focus to the neighbour and made the silence
	// audible.
	if err := term.SendKeys(tuitest.Alt("2")); err != nil {
		t.Fatalf("switch to workspace 2: %v", err)
	}
	time.Sleep(600 * time.Millisecond)
	if err := term.SendKeys(tuitest.Alt("1")); err != nil {
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

// cursorColourRE is the cursor colour the host is told: OSC 12 with a colour,
// or OSC 112, which hands the cursor back to the host's own colour.
var cursorColourRE = regexp.MustCompile(`\x1b\](?:12;([^\x07\x1b]*)|(112))(?:\x07|\x1b\\)`)

// hostCursorColour is the colour the host cursor has now: the last OSC 12 or
// OSC 112 in the stream. "reset" is OSC 112, and "" is that the host was never
// told anything, which leaves its own colour as well.
func hostCursorColour(stream []byte) string {
	m := cursorColourRE.FindAllSubmatch(stream, -1)
	if len(m) == 0 {
		return ""
	}
	last := m[len(m)-1]
	if len(last[2]) > 0 {
		return "reset"
	}
	return strings.ToLower(string(last[1]))
}

// waitCursorColour blocks until the host cursor has the colour want.
func waitCursorColour(t *testing.T, stream *hostStream, want, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	got := ""
	for time.Now().Before(deadline) {
		if got = hostCursorColour(stream.bytes()); got == want {
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	var trail strings.Builder
	for _, m := range cursorColourRE.FindAll(stream.bytes(), -1) {
		fmt.Fprintf(&trail, "  %q\n", m)
	}
	t.Fatalf("%s: the host cursor colour is %q, want %q\ncursor colours the host was sent:\n%s",
		what, got, want, trail.String())
}

// TestCursorColourFollowsFocusedPane: a pane that sets its cursor colour with
// OSC 12 gets that colour on the host cursor, the same as its shape. A pane
// that set none leaves the host its own colour, so moving the focus there
// resets it, and moving back sets it again. OSC 112 in the pane resets it too.
//
// tuios used to read OSC 12 into the pane's emulator and stop there, so the
// host cursor kept its own colour whatever the guest asked for.
func TestCursorColourFollowsFocusedPane(t *testing.T) {
	stream := &hostStream{}
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "colour"}, out: stream})
	waitBoot(t, term)

	newWindow(t, term)
	enterTerminalMode(t, term)
	stream.mark("colour-set")
	runInShell(t, term, `printf '\033]12;#ff00ff\007'; echo `+splitMarker("COLOURSET"), "COLOURSET", shellTimeout)
	waitCursorColour(t, stream, "#ff00ff", "after the focused pane set its cursor colour")

	// A second pane that set no colour. The focus moves to it.
	leaveTerminalMode(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	stream.mark("plain-pane")
	runInShell(t, term, `echo `+splitMarker("PLAINPANE"), "PLAINPANE", shellTimeout)
	waitCursorColour(t, stream, "reset", "with the focus on a pane that set no colour")

	stream.mark("focus-back")
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "colour", "0"); err != nil {
		t.Fatalf("focus-window: %v\n%s", err, out)
	}
	waitCursorColour(t, stream, "#ff00ff", "with the focus back on the pane that set a colour")

	stream.mark("colour-reset")
	runInShell(t, term, `printf '\033]112\007'; echo `+splitMarker("COLOURRESET"), "COLOURRESET", shellTimeout)
	waitCursorColour(t, stream, "reset", "after the pane reset its cursor colour")

	alive(t, term, "after the cursor colour checks")
}

// textCell is the screen cell where text starts, found on the grid the host
// terminal shows. Columns are cells, so a border drawn with box glyphs or a
// wide rune before the text does not move the answer.
func textCell(s tuitest.Screen, text string) (col, row int, ok bool) {
	cols, rows := s.Size()
	for r := range rows {
		var line strings.Builder
		at := make([]int, 0, cols)
		for c := range cols {
			content := s.Cell(c, r).Content
			if content == "" {
				continue
			}
			for range len(content) {
				at = append(at, c)
			}
			line.WriteString(content)
		}
		if i := strings.Index(line.String(), text); i >= 0 {
			return at[i], r, true
		}
	}
	return 0, 0, false
}

// waitHostCursor blocks until the host terminal's own cursor, as the PTY
// stream left it, is at (col, row) with the given visibility. An input
// method opens its candidate window at that cursor, hidden or not, so this is
// where a Chinese or Japanese IME would put it.
func waitHostCursor(t *testing.T, term *tuitest.Terminal, col, row int, visible bool, what string) {
	t.Helper()
	waitHostCursorWithin(t, term, col, row, visible, uiTimeout, what)
}

// waitHostCursorWithin is waitHostCursor with its own deadline, for a state
// that only holds for a while.
func waitHostCursorWithin(t *testing.T, term *tuitest.Terminal, col, row int, visible bool, within time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(within)
	var gotCol, gotRow int
	var gotVisible bool
	for time.Now().Before(deadline) {
		if gotCol, gotRow, gotVisible = term.Screen().Cursor(); gotCol == col && gotRow == row && gotVisible == visible {
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatalf("%s: the host cursor is at (%d,%d) visible=%v, want (%d,%d) visible=%v\n%s",
		what, gotCol, gotRow, gotVisible, col, row, visible, term.Snapshot())
}

// TestHiddenCursorParksAtGuestCursor is #649. Claude Code hides the cursor
// and draws its own, but moves the hidden cursor to its prompt, because an
// input method opens its candidate window at the terminal's real cursor.
// tuios hid the host cursor too and then left it wherever the frame's last
// write ended, so the candidate window opened in the bottom-right corner.
//
// The guest here does the same: it hides its cursor, writes a word and puts
// the cursor on an empty cell away from it. The host cursor has to be on that cell, hidden. Then the
// guest shows the cursor without drawing anything, which changes no cell, and
// the host cursor has to come back on in the same place. Then the guest hides
// it again, still without drawing, and the host cursor has to go off there.
// Neither change writes a frame.
//
// The second pane makes the offsets real: the pane under test is the right
// half of a tiled screen, so its cells are not at the screen's.
//
// Negative controls (NEGATIVE_CONTROLS.md): with viewCursor parking nothing,
// the host cursor stays where the frame ended and the first wait times out.
// With cursorPark.frame passing frames through, the renderer shows the parked
// cursor and the first wait times out. With the write in cursorPark.set
// removed, the show and the hide without a frame never reach the host.
func TestHiddenCursorParksAtGuestCursor(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	waitWindowCount(t, term, 2, "two tiled panes")
	enterTerminalMode(t, term)

	runInShell(t, term,
		`clear; printf '\033[?25l\033[6;12H'IME""CARET'\033[10;30H'; sleep 5; printf '\033[?25h'; sleep 5; printf '\033[?25l'; sleep 60`,
		"IMECARET", shellTimeout)
	word, top, ok := textCell(term.Screen(), "IMECARET")
	if !ok {
		t.Fatalf("the word is not on the screen\n%s", term.Snapshot())
	}
	if word < 60 {
		t.Fatalf("the word is at column %d, want it in the right pane\n%s", word, term.Snapshot())
	}
	// The guest cursor is on an empty cell 4 rows down and 18 columns right
	// of the word, a cell no frame writes to, so the host cursor cannot be
	// there by chance.
	col, row := word+18, top+4
	// Within the 5 seconds before the guest shows its cursor. Later, the
	// guest hides it again on the same cell, and a build that parks nothing
	// leaves the host cursor there by chance.
	waitHostCursorWithin(t, term, col, row, false, 4*time.Second, "with the guest cursor hidden")
	saveFrame(t, term, "ime-cursor-hidden")
	waitHostCursor(t, term, col, row, true, "after the guest showed its cursor again")
	waitHostCursor(t, term, col, row, false, "after the guest hid its cursor again without drawing")

	alive(t, term, "after the hidden cursor checks")
}
