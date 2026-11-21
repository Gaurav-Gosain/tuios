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

func wheelAt(t *testing.T, term *tuitest.Terminal, col, row int, button tuitest.MouseButton, n int) {
	t.Helper()
	for range n {
		mousePress(t, term, col, row, button, 0)
	}
}

func dragSelect(t *testing.T, term *tuitest.Terminal, fromCol, toCol, row int) {
	t.Helper()
	mouseDrag(t, term, fromCol, row, toCol, row, tuitest.MouseLeft, 0)
}

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

func findText(t *testing.T, term *tuitest.Terminal, want string) (row, col int) {
	t.Helper()
	if err := term.WaitForText(want, shellTimeout); err != nil {
		t.Fatalf("%q never appeared: %v\n%s", want, err, term.Snapshot())
	}
	s := term.Screen()
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if before, _, ok := strings.Cut(line, want); ok {
			return r, len([]rune(before))
		}
	}
	t.Fatalf("%q is in the screen text but on no single row\n%s", want, term.Snapshot())
	return 0, 0
}

func fillScrollback(t *testing.T, term *tuitest.Terminal, prefix string, n int) string {
	t.Helper()
	last := fmt.Sprintf("%s-%d-END", prefix, n)
	runInShell(t, term,
		fmt.Sprintf("for i in $(seq 1 %d); do echo \"%s-$i-END\"; done", n, prefix),
		last, bulkTimeout)
	return last
}

func paneCell(t *testing.T, term *tuitest.Terminal) (col, row int) {
	t.Helper()
	cols, rows := term.Screen().Size()
	return cols / 2, rows / 2
}

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

func waitScrolledTo(t *testing.T, term *tuitest.Terminal, prefix, what string, want func(int) bool) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return want(newestVisible(s, prefix))
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the newest line on screen is %s-%d-END: %v\n%s",
			what, prefix, newestVisible(term.Screen(), prefix), err, term.Snapshot())
	}
}

func settledScroll(t *testing.T, term *tuitest.Terminal, prefix string) int {
	t.Helper()
	last := newestVisible(term.Screen(), prefix)
	steady := time.Now()
	deadline := time.Now().Add(scrollSettleCap)
	for time.Now().Before(deadline) {
		n := newestVisible(term.Screen(), prefix)
		if n != last {
			last, steady = n, time.Now()
		} else if time.Since(steady) >= scrollSettleQuiet {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	return last
}

const (
	scrollSettleQuiet = 250 * time.Millisecond
	scrollSettleCap   = 3 * time.Second
)

// TestWheelScrollShowsScrollbackWithoutAnnouncingAMode is the centre of the
// change. Turning the wheel over a pane used to drop the user into copy mode
// and put "Copy mode (hjkl, q to exit)" on the dock along with a line of vim keybindings
// (now the copy-mode legend, "y yank" among its keys),
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

	stop := watchForBanner(term, "Copy mode", "hjkl:move", "y:yank", "y yank")

	wheelAt(t, term, col, row, tuitest.MouseWheelUp, 20)

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

	waitScrolledTo(t, term, "TYPED", "typing did not return the pane to live output",
		func(n int) bool { return n == 200 })
	alive(t, term, "after typing while scrolled")
}

// TestARemoteSendKeysLeavesAScrolledPaneScrolled is the other half of the
// bargain above. Typing ends a scrolled view because the person has stopped
// reading. tuios send-keys goes through the same handler, and used to end it
// too, so an agent typing into the pane returned the person's view to the
// bottom at a moment decided by another process. A remote key now goes to the
// guest and leaves the view where the person put it; the person's own next
// key still brings the pane back.
//
// NEGATIVE CONTROL: measured on the tree before the fix, the pane is back at
// REMOTE-200-END the moment the first remote key lands.
//
// The watch also catches a frame drawn between output landing and the next
// Update, at an offset derived from a shorter history: the view jumps forward
// by the lines that just arrived. NEGATIVE_CONTROLS.md has the run that
// widens that window with HoldScrollAnchorAt left out of renderTerminal.
func TestARemoteSendKeysLeavesAScrolledPaneScrolled(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "remote-keys"}})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	fillScrollback(t, term, "REMOTE", 200)
	col, row := paneCell(t, term)

	wheelAt(t, term, col, row, tuitest.MouseWheelUp, 10)
	waitScrolledTo(t, term, "REMOTE", "the wheel did not scroll",
		func(n int) bool { return n > 0 && n <= 190 })
	before := settledScroll(t, term, "REMOTE")

	for _, keys := range [][]string{{"--raw", "echo remote-$((6*7))"}, {"Enter"}} {
		if out, err := tuiosCLI(t, base, append([]string{"send-keys"}, keys...)...); err != nil {
			t.Fatalf("send-keys %v: %v\n%s", keys, err, out)
		}
	}

	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if n := newestVisible(term.Screen(), "REMOTE"); n != before {
			t.Fatalf("a remote key moved the view from REMOTE-%d-END to REMOTE-%d-END\n%s", before, n, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := term.SendKeys("\r"); err != nil {
		t.Fatal(err)
	}
	waitScrolledTo(t, term, "REMOTE", "the person's key did not return the pane to live output",
		func(n int) bool { return n == 200 })
	if err := term.WaitForText("remote-42", uiTimeout); err != nil {
		t.Fatalf("the remote keys never reached the shell: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after remote keys while scrolled")
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

	runInShell(t, term, `printf '\033[?1000h\033[?1006h'; echo MOUSE""ON`, "MOUSEON", shellTimeout)

	col, row := paneCell(t, term)
	wheelAt(t, term, col, row, tuitest.MouseWheelUp, 2)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return sgrWheelReport.MatchString(s.Text())
	}, uiTimeout); err != nil {
		t.Fatalf("the wheel was not forwarded to the pane that asked for the mouse: %v\n%s",
			err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), last) {
		t.Fatalf("tuios scrolled a mouse-tracking pane's scrollback; %q left the screen\n%s",
			last, term.Snapshot())
	}
	alive(t, term, "after wheeling over a mouse-tracking pane")
}

var sgrWheelReport = regexp.MustCompile(`64;\d+;\d+M`)

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

	runInShell(t, term, `printf '\033[?1003h\033[?1006h'; echo MOTION""ON`, "MOTIONON", shellTimeout)

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

var osc52 = regexp.MustCompile(`\x1b\]52;[^;]*;([A-Za-z0-9+/=]*)(?:\x07|\x1b\\)`)

func clipboardWrites(out *lockedBuffer) []string {
	var got []string
	for _, m := range osc52.FindAllStringSubmatch(out.String(), -1) {
		if data, err := base64.StdEncoding.DecodeString(m[1]); err == nil {
			got = append(got, string(data))
		}
	}
	return got
}

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

const clipboardSettle = 700 * time.Millisecond

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

func describeSpan(start, end int) string {
	if start < 0 {
		return "nothing"
	}
	return fmt.Sprintf("columns [%d,%d)", start, end)
}

const multiClickAttempts = 3

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
			"mouse.double_click_ms (300 ms by default), re-sending", attempt, n, col, row, got, want)
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
	runInShell(t, term, "echo "+splitMarker(marker), marker, shellTimeout)
	row, col := findText(t, term, marker)

	dragSelect(t, term, col, col+len(marker)-1, row)

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
	runInShell(t, term, `P=/opt; echo "PREFIX $P/dblclick/word.txt SUFFIX"`, word, shellTimeout)
	row, col := findText(t, term, word)

	const affix = len("PREFIX ")
	wordStart, wordEnd := col, col+len(word)
	lineStart, lineEnd := col-affix, col+len(word)+affix

	clickAt(t, term, col+6, row, 1)
	waitClipboardSequence(t, term, out, 0)
	if start, end := selectionSpan(term.Screen(), row); start >= 0 {
		t.Fatalf("a single click selected columns [%d,%d); it must select nothing\n%s",
			start, end, term.Snapshot())
	}
	time.Sleep(gestureGap)

	from := selectByMultiClick(t, term, out, col+6, row, 2, wordStart, wordEnd)
	waitClipboardSequence(t, term, out, from, word)

	time.Sleep(gestureGap)

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

	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)

	runInShell(t, term, "echo FREEZE-AFTER-$((3*3))", "FREEZE-AFTER-9", shellTimeout)
	alive(t, term, "after clicking inside a pane")
}

// TestMenuCopiesADragSelection is the whole reported bug in one test: drag over
// a line of output, right-click on the highlight, and take "Copy selection".
//
// Nothing here could have failed for want of a selection: the drag copies on
// release, so the clipboard already holds the text before the menu is opened.
// The baseline is taken after that write, so what is asserted is only what the
// menu row itself produced. That is what nothing tested. Copy-on-select was
// covered end to end, and the row beside it was greyed out over a plainly
// visible highlight for anyone who reached for the menu instead.
func TestMenuCopiesADragSelection(t *testing.T) {
	out := &lockedBuffer{}
	term, _ := start(t, startOpts{out: out})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	const marker = "MENUCOPY-alpha-bravo"
	runInShell(t, term, "echo "+splitMarker(marker), marker, shellTimeout)
	row, col := findText(t, term, marker)

	dragSelect(t, term, col, col+len(marker)-1, row)
	waitClipboardSequence(t, term, out, 0, marker)

	from := len(clipboardWrites(out))

	shiftRightClick(t, term, col+2, row)
	waitMenu(t, term, "pane with a selection", "Copy selection", "Close pane")

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(markedRow(s), "Copy selection")
	}, uiTimeout); err != nil {
		t.Fatalf("the menu opened on a drag-selected pane with \"Copy selection\" out of reach, "+
			"so the row is dimmed over a live selection: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}

	waitClipboardSequence(t, term, out, from, marker)
	alive(t, term, "after copying a selection from the context menu")
}
