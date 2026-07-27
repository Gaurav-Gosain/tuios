package tuie2e

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The screen assertions below are what makes this suite evidence rather than
// decoration: every claim about the wheel and about selection is checked
// against a real tuios binary painting a real PTY, driven by real SGR mouse
// reports.

// wheelAt turns the wheel n notches over a screen cell.
func wheelAt(t *testing.T, term *tuitest.Terminal, col, row int, button tuitest.MouseButton, n int) {
	t.Helper()
	for range n {
		if err := term.SendMouse(tuitest.MouseEvent{
			Col: col, Row: row, Button: button, Action: tuitest.MousePress,
		}); err != nil {
			t.Fatalf("wheel: %v", err)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// dragSelect presses at one cell, drags across the row, and releases.
func dragSelect(t *testing.T, term *tuitest.Terminal, fromCol, toCol, row int) {
	t.Helper()
	send := func(ev tuitest.MouseEvent) {
		t.Helper()
		if err := term.SendMouse(ev); err != nil {
			t.Fatalf("mouse %+v: %v", ev, err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	send(tuitest.MouseEvent{Col: fromCol, Row: row, Button: tuitest.MouseLeft, Action: tuitest.MousePress})
	// MouseMove with a button set is a drag on the wire: the motion bit plus
	// the button code, which is what SGR mode 1002 reports while dragging.
	step := 1
	if toCol < fromCol {
		step = -1
	}
	for c := fromCol + step; c != toCol+step; c += step {
		send(tuitest.MouseEvent{Col: c, Row: row, Button: tuitest.MouseLeft, Action: tuitest.MouseMove})
	}
	send(tuitest.MouseEvent{Col: toCol, Row: row, Button: tuitest.MouseLeft, Action: tuitest.MouseRelease})
}

// clickAt sends n presses and a release at one cell, fast enough to count as a
// single multi-click gesture.
func clickAt(t *testing.T, term *tuitest.Terminal, col, row, n int) {
	t.Helper()
	for range n {
		if err := term.SendMouse(tuitest.MouseEvent{
			Col: col, Row: row, Button: tuitest.MouseLeft, Action: tuitest.MousePress,
		}); err != nil {
			t.Fatalf("click: %v", err)
		}
		time.Sleep(40 * time.Millisecond)
	}
	if err := term.SendMouse(tuitest.MouseEvent{
		Col: col, Row: row, Button: tuitest.MouseLeft, Action: tuitest.MouseRelease,
	}); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// findText locates a substring on screen and returns its row and starting
// column. Tests use it so they click on real content rather than on coordinates
// guessed from the layout.
//
// The column is counted in runes, not bytes. A pane's own border is U+2502,
// three bytes wide and one cell wide, so a byte index puts every click two
// cells to the right of where the test meant it.
func findText(t *testing.T, term *tuitest.Terminal, want string) (row, col int) {
	t.Helper()
	if err := term.WaitForText(want, shellTimeout); err != nil {
		t.Fatalf("%q never appeared: %v\n%s", want, err, term.Snapshot())
	}
	s := term.Screen()
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if b := strings.Index(line, want); b >= 0 {
			return r, len([]rune(line[:b]))
		}
	}
	t.Fatalf("%q is in the screen text but on no single row\n%s", want, term.Snapshot())
	return 0, 0
}

// fillScrollback prints n tagged lines into the focused pane so there is
// history to scroll through, and returns the tag of the last one.
func fillScrollback(t *testing.T, term *tuitest.Terminal, prefix string, n int) string {
	t.Helper()
	last := fmt.Sprintf("%s-%d-END", prefix, n)
	runInShell(t, term,
		fmt.Sprintf("for i in $(seq 1 %d); do echo \"%s-$i-END\"; done", n, prefix),
		last, bulkTimeout)
	return last
}

// paneCell picks a cell inside the focused pane: the middle of the screen is
// always pane content with one window open.
func paneCell(t *testing.T, term *tuitest.Terminal) (col, row int) {
	t.Helper()
	cols, rows := term.Screen().Size()
	return cols / 2, rows / 2
}

// watchForBanner samples the screen until the returned stop function is called,
// and reports the first of the given strings it ever saw. Sampling throughout
// is what makes an absence assertion meaningful: a banner that has already
// expired by the time of a single check would otherwise pass.
func watchForBanner(term *tuitest.Terminal, banners ...string) (stop func() string) {
	done := make(chan struct{})
	result := make(chan string, 1)
	go func() {
		seen := ""
		for seen == "" {
			select {
			case <-done:
				result <- seen
				return
			default:
			}
			text := term.Screen().Text()
			for _, b := range banners {
				if strings.Contains(text, b) {
					seen = b
					break
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		<-done
		result <- seen
	}()
	return func() string {
		close(done)
		return <-result
	}
}

// newestVisible returns the highest numbered PREFIX-n-END marker on screen, or
// -1 when none is there.
//
// Assertions go through this rather than waiting for one specific line number.
// Waiting for a line by name races the gesture: the notches are sent back to
// back, and by the time the wait starts the view has already moved past the
// line the test named. Asking where the viewport ended up instead is both
// stable and a more direct statement of what scrolling means.
func newestVisible(s tuitest.Screen, prefix string) int {
	re := regexp.MustCompile(regexp.QuoteMeta(prefix) + `-(\d+)-END`)
	newest := -1
	for _, m := range re.FindAllStringSubmatch(s.Text(), -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > newest {
			newest = n
		}
	}
	return newest
}

// waitScrolledTo blocks until the newest marker on screen satisfies want, and
// reports where the viewport actually settled if it never does.
func waitScrolledTo(t *testing.T, term *tuitest.Terminal, prefix, what string, want func(int) bool) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return want(newestVisible(s, prefix))
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the newest line on screen is %s-%d-END: %v\n%s",
			what, prefix, newestVisible(term.Screen(), prefix), err, term.Snapshot())
	}
}

// TestWheelScrollShowsScrollbackWithoutAnnouncingAMode is the centre of the
// change. Turning the wheel over a pane used to drop the user into copy mode
// and put "COPY MODE (hjkl/q)" on the dock along with a line of vim keybindings,
// which is tmux's behaviour. In kitty, WezTerm, iTerm2 or GNOME Terminal the
// view just scrolls.
//
// Two things are asserted, and both matter. Older output must appear, so this
// is really testing scrolling. And no mode may be announced at any point during
// the gesture, which is sampled continuously rather than once, because a
// notification that has already expired by the time of a single sample would
// let the old behaviour through.
func TestWheelScrollShowsScrollbackWithoutAnnouncingAMode(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	const total = 300
	last := fillScrollback(t, term, "WHEEL", total)

	col, row := paneCell(t, term)

	// Watch for any announcement for the whole gesture, not just after it: a
	// notification that expired before a single sample would slip through.
	stop := watchForBanner(term, "COPY MODE", "hjkl:move", "y:yank")

	wheelAt(t, term, col, row, tuitest.MouseWheelUp, 20)

	// The viewport must have moved back by roughly what was asked for: sixty
	// lines of history, minus the pane's own height.
	waitScrolledTo(t, term, "WHEEL", "the wheel did not scroll back through the history",
		func(n int) bool { return n > 0 && n <= total-40 })
	time.Sleep(500 * time.Millisecond)
	if seen := stop(); seen != "" {
		t.Fatalf("scrolling announced a mode: %q was on screen. Turning the wheel must not "+
			"put the user in a mode or teach them keybindings.\n%s", seen, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), last) {
		t.Errorf("the newest line %q is still on screen; the viewport did not really move\n%s",
			last, term.Snapshot())
	}
	alive(t, term, "after wheel scrolling")
}

// TestWheelDownToBottomReturnsToLiveOutput drives the whole round trip: scroll
// up, scroll back down, then type. The typing is the assertion. tuios used to
// leave the pane in copy mode after the wheel came back to the bottom, with the
// scroll offset at zero so it looked like live output, and every subsequent
// keystroke was eaten as a vim motion. Nothing the user typed reached the shell
// and nothing said why.
func TestWheelDownToBottomReturnsToLiveOutput(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	fillScrollback(t, term, "ROUND", 200)
	col, row := paneCell(t, term)

	wheelAt(t, term, col, row, tuitest.MouseWheelUp, 10)
	waitScrolledTo(t, term, "ROUND", "the wheel did not scroll up",
		func(n int) bool { return n > 0 && n <= 190 })
	wheelAt(t, term, col, row, tuitest.MouseWheelDown, 10)
	waitScrolledTo(t, term, "ROUND", "the wheel did not scroll back to live output",
		func(n int) bool { return n == 200 })

	// The shell must have the keyboard again. The marker is computed by the
	// shell, so an echo of the keystrokes cannot satisfy it.
	runInShell(t, term, "echo WHEELBACK-$((6*7))", "WHEELBACK-42", shellTimeout)
	alive(t, term, "after a wheel round trip")
}

// TestTypingWhileScrolledSnapsBackToLiveOutput covers the other half: the user
// scrolls up, reads, and then starts typing without scrolling back. A terminal
// with no modes jumps to the bottom and types the character. tuios used to feed
// the keystrokes to copy mode's motions instead.
func TestTypingWhileScrolledSnapsBackToLiveOutput(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	fillScrollback(t, term, "TYPED", 200)
	col, row := paneCell(t, term)

	wheelAt(t, term, col, row, tuitest.MouseWheelUp, 10)
	waitScrolledTo(t, term, "TYPED", "the wheel did not scroll",
		func(n int) bool { return n > 0 && n <= 190 })

	runInShell(t, term, "echo TYPEBACK-$((6*7))", "TYPEBACK-42", shellTimeout)

	// And the pane is back at the bottom, not left hanging in the scrollback.
	waitScrolledTo(t, term, "TYPED", "typing did not return the pane to live output",
		func(n int) bool { return n == 200 })
	alive(t, term, "after typing while scrolled")
}

// TestMouseTrackingAppKeepsItsOwnWheel is the regression guard on the thing
// that already worked and must keep working: vim, less and htop ask for the
// mouse, and the wheel belongs to them.
//
// The fixture is the terminal line discipline itself. With echo on, whatever
// tuios writes into the pane's PTY is echoed straight back, so a forwarded SGR
// wheel report appears in the pane as its own text. Nothing has to be running
// but the shell.
func TestMouseTrackingAppKeepsItsOwnWheel(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	last := fillScrollback(t, term, "OWNED", 200)

	// Ask for mouse tracking the way an application does, in SGR encoding.
	runInShell(t, term, `printf '\033[?1000h\033[?1006h'; echo MOUSEON`, "MOUSEON", shellTimeout)

	col, row := paneCell(t, term)
	wheelAt(t, term, col, row, tuitest.MouseWheelUp, 2)

	// The report reaches the pane and the tty echoes it back. The ESC and the
	// CSI introducer are swallowed on the way through the emulator, so what
	// lands on screen is the tail of the SGR report: button 64 (wheel up) and
	// the cell it happened over, in terminal-relative coordinates.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return sgrWheelReport.MatchString(s.Text())
	}, uiTimeout); err != nil {
		t.Fatalf("the wheel was not forwarded to the pane that asked for the mouse: %v\n%s",
			err, term.Snapshot())
	}
	// And tuios did not scroll its own scrollback underneath it: the newest
	// line is still there.
	if !strings.Contains(term.Screen().Text(), last) {
		t.Fatalf("tuios scrolled a mouse-tracking pane's scrollback; %q left the screen\n%s",
			last, term.Snapshot())
	}
	alive(t, term, "after wheeling over a mouse-tracking pane")
}

// sgrWheelReport matches a forwarded wheel-up report as it comes back out of
// the pane's own tty echo: button 64, a column, a row, and the press marker.
var sgrWheelReport = regexp.MustCompile(`64;\d+;\d+M`)

// osc52 matches a clipboard write on the wire. tuios's copy path is
// tea.SetClipboard, which is OSC 52, so this is what a copy looks like to the
// terminal the user is actually sitting in front of.
var osc52 = regexp.MustCompile(`\x1b\]52;[^;]*;([A-Za-z0-9+/=]*)(?:\x07|\x1b\\)`)

// clipboardWrites decodes every OSC 52 payload tuios has written so far.
func clipboardWrites(out *lockedBuffer) []string {
	var got []string
	for _, m := range osc52.FindAllStringSubmatch(out.String(), -1) {
		if data, err := base64.StdEncoding.DecodeString(m[1]); err == nil {
			got = append(got, string(data))
		}
	}
	return got
}

// lockedBuffer is a bytes.Buffer the output pump can write while the test reads.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForClipboard blocks until tuios has written the wanted text to the
// clipboard, and reports what it did write if it never does.
func waitForClipboard(t *testing.T, term *tuitest.Terminal, out *lockedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		for _, got := range clipboardWrites(out) {
			if strings.TrimSpace(got) == want {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no clipboard write of %q. tuios wrote %q\n%s",
		want, clipboardWrites(out), term.Snapshot())
}

// TestDragSelectionCopiesOnRelease drives a real click-drag across a line of
// output and asserts the text left the process on the clipboard.
//
// Before this change a left drag inside a pane in terminal mode did not select
// at all: it grabbed the window, moved it, and dropped the user into window
// management mode.
func TestDragSelectionCopiesOnRelease(t *testing.T) {
	out := &lockedBuffer{}
	term, _ := start(t, startOpts{out: out})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	const marker = "DRAGME-alpha-bravo"
	runInShell(t, term, "echo "+marker, marker, shellTimeout)
	row, col := findText(t, term, marker)

	dragSelect(t, term, col, col+len(marker)-1, row)

	waitForClipboard(t, term, out, marker)
	if err := term.WaitForText("Copied", uiTimeout); err != nil {
		t.Errorf("no copy confirmation in the dock: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after a drag selection")
}

// TestDoubleClickCopiesAWordAndTripleClickTheLine covers the two gestures every
// terminal has had since the nineties and tuios had neither of.
//
// The word is a path so the assertion also pins the word-character set: a
// double-click that stopped at every punctuation mark would select "usr" and
// the test would say so.
func TestDoubleClickCopiesAWordAndTripleClickTheLine(t *testing.T) {
	out := &lockedBuffer{}
	term, _ := start(t, startOpts{out: out})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	const word = "/opt/dblclick/word.txt"
	const line = "PREFIX " + word + " SUFFIX"
	// The word is assembled from a variable so the shell's echo of the command
	// does not itself contain it: otherwise findText lands on the command line
	// rather than on the output, and the test asserts about the wrong row.
	runInShell(t, term, `P=/opt; echo "PREFIX $P/dblclick/word.txt SUFFIX"`, word, shellTimeout)
	row, col := findText(t, term, word)

	clickAt(t, term, col+6, row, 2)
	waitForClipboard(t, term, out, word)

	// Let the multi-click window lapse, or the first press of the triple
	// continues the double and the counts come out shifted.
	time.Sleep(800 * time.Millisecond)

	clickAt(t, term, col+6, row, 3)
	waitForClipboard(t, term, out, line)
	alive(t, term, "after multi-click selection")
}
