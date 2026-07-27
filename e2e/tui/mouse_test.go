package tuie2e

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
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
//
// A wheel notch is a press and nothing else: SGR reports buttons 64 and 65 with
// the press final byte and never emits a matching release, because there is no
// button to let go of. That asymmetry is real, not a shortcut, so this is the
// one gesture in the suite with unpaired events.
func wheelAt(t *testing.T, term *tuitest.Terminal, col, row int, button tuitest.MouseButton, n int) {
	t.Helper()
	for range n {
		mousePress(t, term, col, row, button, 0)
	}
}

// dragSelect presses at one cell, drags across the row, and releases at the far
// end, emitting a motion report for every cell in between as a real drag does.
func dragSelect(t *testing.T, term *tuitest.Terminal, fromCol, toCol, row int) {
	t.Helper()
	mouseDrag(t, term, fromCol, row, toCol, row, tuitest.MouseLeft, 0)
}

// clickAt clicks n times at one cell, fast enough to count as a single
// multi-click gesture.
//
// Each click is a press AND a release, which is what a physical mouse does and
// what this helper used to get wrong: it sent n presses and one trailing
// release outside the loop. tuios finishes a selection and writes the clipboard
// on release (internal/input.finishMouseSelection), so under the old shape a
// triple click generated exactly one clipboard write. The two intermediate
// releases a real triple click produces, and everything tuios does on them,
// could not be generated at all, so no assertion could have observed them.
// TestDoubleClickCopiesAWordAndTripleClickTheLine now asserts on the whole
// sequence of writes rather than on whether the wanted text is somewhere in it.
//
// The clicks are paced by multiClickHold and multiClickGap rather than by the
// harness's ordinary mouseGap, because for this one gesture the spacing is not
// presentation, it is the input: tuios decides how many clicks it received by
// how far apart it processed them.
func clickAt(t *testing.T, term *tuitest.Terminal, col, row, n int) {
	t.Helper()
	for i := range n {
		if i > 0 {
			time.Sleep(multiClickGap)
		}
		ev := tuitest.MouseEvent{Col: col, Row: row, Button: tuitest.MouseLeft}
		ev.Action = tuitest.MousePress
		sendMouseThenWait(t, term, "press", ev, multiClickHold)
		ev.Action = tuitest.MouseRelease
		sendMouseThenWait(t, term, "release", ev, 0)
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

// sgrBareMotionReport matches a forwarded pointer motion with no button held,
// echoed back by the pane's tty: button code 35, which is the no-button value 3
// plus the motion bit 32.
var sgrBareMotionReport = regexp.MustCompile(`35;\d+;\d+M`)

// TestBareMotionReachesAnEventTrackingApp is the only place in this suite where
// a pointer motion with no button held can be observed at all, and it exists as
// much to pin that fact as to test the forwarding.
//
// cmd/tuios/run.go installs tea.WithFilter(filterMouseMotion), a whitelist that
// drops every motion event unless a drag, a resize, an overlay drag, the
// scrollback browser or a mouse-tracking pane is active. In every other state
// the model never sees motion, so hover behaviour is not merely untested here,
// it is unobservable: a helper that sends motion and an assertion on its effect
// would be asserting on an event the shipping binary discards. That includes
// the context menu's own hover highlight, which handleMouseMotion implements
// and the filter never lets it reach.
//
// A pane that has asked for any-event tracking (DECSET 1003) is the exception,
// and with the tty's echo on, the report it receives comes straight back as
// text. See NEGATIVE_CONTROLS.md for the full list of blind spots.
func TestBareMotionReachesAnEventTrackingApp(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	// 1003 is any-event tracking: motion is reported whether or not a button is
	// down. 1006 asks for the SGR encoding.
	runInShell(t, term, `printf '\033[?1003h\033[?1006h'; echo MOTIONON`, "MOTIONON", shellTimeout)

	col, row := paneCell(t, term)
	for i := range 4 {
		mouseHover(t, term, col+i, row)
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return sgrBareMotionReport.MatchString(s.Text())
	}, uiTimeout); err != nil {
		t.Fatalf("a bare pointer motion was not forwarded to the pane that asked for "+
			"any-event tracking: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after moving the pointer over an any-event tracking pane")
}

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

// clipboardSince returns the clipboard writes made after the first n, with
// surrounding whitespace trimmed the way the assertions compare them.
func clipboardSince(out *lockedBuffer, n int) []string {
	all := clipboardWrites(out)
	if len(all) <= n {
		return nil
	}
	got := make([]string, 0, len(all)-n)
	for _, s := range all[n:] {
		got = append(got, strings.TrimSpace(s))
	}
	return got
}

// clipboardSettle is how long waitClipboardSequence waits after the wanted
// sequence arrives to be sure nothing else follows it.
const clipboardSettle = 700 * time.Millisecond

// waitClipboardSequence blocks until the clipboard writes made after the first
// `from` are exactly `want`, in order, and then holds still to confirm nothing
// further arrives.
//
// This exists because "the wanted text is somewhere in the list of writes" is
// the wrong assertion for a gesture whose whole risk is how many times it
// writes. A triple click passes through a word selection on its way to a line
// selection and copies both, in that order; a paste taken between the two gets
// the word. Asking only whether the line was written at some point cannot see
// that, cannot see a stray write before it, and cannot see a duplicate after
// it. The count and the order are the property, so they are what is asserted.
//
// The settle window is not decoration either. Without it a gesture that writes
// one time too many satisfies the condition on its correct prefix and the extra
// write lands after the test has moved on.
func waitClipboardSequence(t *testing.T, term *tuitest.Terminal, out *lockedBuffer, from int, want ...string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		got := clipboardSince(out, from)
		if slices.Equal(got, want) {
			break
		}
		if len(got) > len(want) {
			t.Fatalf("the gesture wrote the clipboard more times than it should: got %q, want %q\n%s",
				got, want, term.Snapshot())
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("the gesture never produced the clipboard writes %q; it wrote %q\n%s",
				want, got, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(clipboardSettle)
	if got := clipboardSince(out, from); !slices.Equal(got, want) {
		t.Fatalf("the gesture kept writing the clipboard after it was done: got %q, want %q\n%s",
			got, want, term.Snapshot())
	}
}

// selectionSpan reports which columns of a row tuios is painting as selected,
// as a half-open [start, end), and (-1, -1) when it is painting none.
//
// The highlight is the only thing on a row of ordinary pane output that carries
// a background colour: shell output is drawn on the terminal's default
// background and internal/app.visualSelectionStyle sets one. So "this cell has
// a background at all" identifies the selection without the test having to know
// which colour the style resolved to on this terminal, which depends on the
// colour profile the child detects.
func selectionSpan(s tuitest.Screen, row int) (start, end int) {
	cols, _ := s.Size()
	start, end = -1, -1
	for c := range cols {
		if s.Cell(c, row).Bg.Kind == tuitest.ColorDefault {
			continue
		}
		if start < 0 {
			start = c
		}
		end = c + 1
	}
	return start, end
}

// describeSpan renders what selectionSpan returned for a failure message.
func describeSpan(start, end int) string {
	if start < 0 {
		return "nothing"
	}
	return fmt.Sprintf("columns [%d,%d)", start, end)
}

// multiClickAttempts is how many times one multi-click gesture is re-sent when
// tuios did not read it as a multi-click at all. Three is enough that losing
// the race every time is not a plausible accident, and small enough that a
// product that has genuinely stopped selecting still fails quickly.
const multiClickAttempts = 3

// selectByMultiClick clicks n times at one cell and returns once tuios has
// highlighted exactly the columns that many clicks should select. It reports
// how many clipboard writes had already been made when the gesture that
// succeeded began, which is the baseline the caller's waitClipboardSequence
// needs.
//
// This exists because a test that sends three clicks is not thereby a test of
// what tuios does with a triple click. A click joins the gesture in progress
// only if it arrives within internal/input.multiClickInterval of the last one,
// measured in tuios at the moment it processes the press. The harness cannot
// control that: it can only put the bytes in the pty and hope tuios is not
// busy. When it is, the third press lands outside the window, tuios reads a
// double click followed by a single one, and everything it does afterwards is
// correct for the input it actually received. Asserting on the clipboard
// without checking that first conflates "the product mishandled a triple
// click", which must fail, with "the harness failed to deliver one", which
// means nothing.
//
// The highlight is what makes the difference visible, and it is a sound witness
// for the gesture because it is painted on the press: it is set before, and
// independently of, the clipboard write being asserted on, so reading it does
// not assume the answer. What it shows when a click falls outside the window is
// a shorter gesture. Losing the third press leaves nothing highlighted, because
// the stray single click starts a one-cell selection its own release finds
// empty and drops the implicit copy-mode session with it. Losing the second
// leaves the word highlighted, because the last two clicks are then a double
// click of their own. Both of those were observed while measuring this.
//
// One outcome cannot be told from a lost race by looking at a single gesture: a
// tuios that selected the word on three clicks would paint exactly what a lost
// second press paints. Repetition separates them, and that is all the retry is
// for. A lost race is a coin flip that comes up differently on the next throw;
// a product that selects the wrong thing does it every time and fails on the
// last attempt with the span it painted in the message. So the retry is driven
// by a measured property of the gesture, never by a failed assertion, and it is
// bounded.
//
// Measured on a 16-core machine oversubscribed with 256 busy loops, which is
// the only way the race was made to happen at all: at the pacing this harness
// sends, 4 to 9 of every 100 triple clicks came out short. Idle, and with the
// machine merely busy rather than swamped, none of several hundred did.
func selectByMultiClick(t *testing.T, term *tuitest.Terminal, out *lockedBuffer, col, row, n, wantStart, wantEnd int) int {
	t.Helper()
	for attempt := 1; ; attempt++ {
		from := len(clipboardWrites(out))
		clickAt(t, term, col, row, n)

		err := term.WaitFor(func(s tuitest.Screen) bool {
			start, end := selectionSpan(s, row)
			return start == wantStart && end == wantEnd
		}, uiTimeout)
		if err == nil {
			return from
		}

		got := describeSpan(selectionSpan(term.Screen(), row))
		want := describeSpan(wantStart, wantEnd)
		if attempt == multiClickAttempts {
			t.Fatalf("%d clicks at (%d,%d) selected %s on all %d attempts, and %d clicks select "+
				"%s; a gesture that comes out short every time is not the machine losing a "+
				"race, it is multi-click selection selecting the wrong thing\n%s",
				n, col, row, got, attempt, n, want, term.Snapshot())
		}
		t.Logf("attempt %d: %d clicks at (%d,%d) selected %s rather than %s, so tuios read them "+
			"as a shorter gesture; the harness lost the race against "+
			"internal/input.multiClickInterval, re-sending", attempt, n, col, row, got, want)
		// Long enough that the retry's first press cannot join the gesture that
		// just ended, and that any deferred write the short gesture did produce
		// has landed before the next baseline is taken.
		time.Sleep(gestureGap)
	}
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

	// Exactly one write: a drag is one gesture and copies once at the end of it.
	waitClipboardSequence(t, term, out, 0, marker)
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
//
// Every gesture asserts the whole sequence of clipboard writes it produced, not
// just that the wanted text turns up in it. A single click copies nothing, a
// double click copies once, and a triple click copies twice because it passes
// through the word on its way to the line and each release is a real release.
// Those counts are the contract: they are what a paste taken mid-gesture gets,
// and they are what the old helper could not generate, let alone assert on.
//
// Each multi-click also asserts the highlight it painted, which is both a
// stronger claim about the gesture and the thing that lets the test tell a
// mishandled triple click apart from one the harness never managed to deliver;
// see selectByMultiClick.
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

	// Where each gesture's highlight has to land. A double click selects the
	// word and nothing either side of it, which is the same claim about the
	// word-character set that the clipboard assertion makes, in the other place
	// the user can see it. A triple click selects the whole line: it starts
	// seven columns earlier, at the P of PREFIX, and ends seven later, after the
	// X of SUFFIX.
	const affix = len("PREFIX ")
	wordStart, wordEnd := col, col+len(word)
	lineStart, lineEnd := col-affix, col+len(word)+affix

	// One click is not a selection. It must leave the clipboard alone, or every
	// stray click in a pane destroys whatever the user had copied.
	clickAt(t, term, col+6, row, 1)
	waitClipboardSequence(t, term, out, 0)
	if start, end := selectionSpan(term.Screen(), row); start >= 0 {
		t.Fatalf("a single click selected columns [%d,%d); it must select nothing\n%s",
			start, end, term.Snapshot())
	}
	time.Sleep(gestureGap)

	from := selectByMultiClick(t, term, out, col+6, row, 2, wordStart, wordEnd)
	waitClipboardSequence(t, term, out, from, word)

	// Let the multi-click window lapse, or the first press of the triple
	// continues the double and the counts come out shifted.
	time.Sleep(gestureGap)

	// A triple click writes the line exactly once. The clipboard is not touched
	// on the intermediate word: the write is deferred until the multi-click
	// window closes, and the second click's word is retired when the third
	// arrives (see internal/app/clipboard_copy.go). This is the whole point of
	// the deferral, and asserting the count rather than the final contents is
	// what makes it observable, since the final contents were always the line.
	from = selectByMultiClick(t, term, out, col+6, row, 3, lineStart, lineEnd)
	waitClipboardSequence(t, term, out, from, line)
	alive(t, term, "after multi-click selection")
}

// TestClickInPaneDoesNotFreezeOutput is the standing guard on the press/release
// pairing this harness now enforces, and on the product behaviour that pairing
// depends on.
//
// A left press inside a pane sets OS.Dragging, and app.updateTerminals returns
// early while it is set: tuios stops polling every pane's output for the
// duration of the gesture, deliberately, so content cannot shift under the
// pointer. The release is what clears it.
//
// So a helper that presses without releasing does not leave a harmless bit set.
// It stops the whole program reading its panes, permanently, and every later
// assertion in that test is made against a frozen screen. Measured: sending
// this click press-only against a correct binary leaves FREEZE-AFTER-9 off the
// screen for the full 20s budget. Guarded: a binary that fails to clear
// OS.Dragging on release fails here, and passes every other mouse and context
// menu test in this package.
func TestClickInPaneDoesNotFreezeOutput(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	runInShell(t, term, "echo FREEZE-BEFORE-$((1+1))", "FREEZE-BEFORE-2", shellTimeout)
	col, row := paneCell(t, term)

	// A plain click in the middle of the pane: press and release, as a mouse does.
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)

	// The shell must still be able to put something new on screen. On a program
	// left in interaction mode it never will, however long the timeout.
	runInShell(t, term, "echo FREEZE-AFTER-$((3*3))", "FREEZE-AFTER-9", shellTimeout)
	alive(t, term, "after clicking inside a pane")
}
