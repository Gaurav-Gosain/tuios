package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A message longer than the dock's block used to be cut with an ellipsis and
// could not be read anywhere: a click on it went to its pane, and the log
// viewer cut the same line at the panel's edge (issue #381). These tests raise
// a long OSC 9 notification from a pane and read its last word, which only the
// full message holds.
//
// The message is built by the shell, so neither marker is in the typed command
// and the echo of the keystrokes cannot satisfy a wait: the command holds
// "LONG%s" and the shell prints LONGHEAD and LONGTAIL.
const (
	longHead = "LONGHEAD"
	longTail = "LONGTAIL"
)

// raiseLongMessage makes the focused pane send an OSC 9 notification whose
// words are the numbers from first to last, between the two markers. It
// leaves the client in terminal mode.
func raiseLongMessage(t *testing.T, term *tuitest.Terminal, first, last int) {
	t.Helper()
	enterTerminalMode(t, term)
	cmd := fmt.Sprintf(`printf '\033]9;LONG%%s %%s LONG%%s\007' HEAD "$(seq -s ' ' %d %d)" TAIL; clear`, first, last)
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("send the notification: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(dockRow(s), longHead)
	}, uiTimeout); err != nil {
		t.Fatalf("the long message never reached the dock: %v\n%s", err, term.Snapshot())
	}
}

// longMessageShot keeps a frame in artifactDir, and in $TUIOS_E2E_SHOTS as a
// PNG when that is set.
func longMessageShot(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	saveArtifact(t, term, artifactDir(t), name)
	if dir := os.Getenv("TUIOS_E2E_SHOTS"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("make the shot directory: %v", err)
		}
		savePNG(t, term.Screen(), hostPalette(t, ""), dir, filepath.Base(artifactDir(t))+"-"+name)
	}
}

// dockColumnOf is the column of needle on the dock row.
func dockColumnOf(t *testing.T, term *tuitest.Terminal, needle string) (col, row int) {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for y := rows - 1; y >= 0; y-- {
		line := s.Line(y)
		if i := strings.Index(line, needle); i >= 0 {
			return len([]rune(line[:i])), y
		}
	}
	t.Fatalf("%q is not on the screen\n%s", needle, term.Snapshot())
	return 0, 0
}

// TestLongNotificationOpensInFull clicks a cut message in the dock and reads
// its end in the message view, then reopens it from the keyboard with prefix
// N after the view was closed.
func TestLongNotificationOpensInFull(t *testing.T) {
	term, _ := start(t, startOpts{cols: 110, rows: 30})
	waitBoot(t, term)
	newWindow(t, term)
	raiseLongMessage(t, term, 100, 220)

	s := term.Screen()
	if strings.Contains(s.Text(), longTail) {
		t.Fatalf("the whole message fits the dock, so this test proves nothing\n%s", term.Snapshot())
	}
	longMessageShot(t, term, "1-dock")

	col, row := dockColumnOf(t, term, longHead)
	mouseHover(t, term, col+2, row)
	if err := term.WaitForText("Click to show the full message.", uiTimeout); err != nil {
		t.Errorf("hovering the cut message showed no label: %v\n%s", err, term.Snapshot())
	}
	longMessageShot(t, term, "2-hover")

	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText(longTail, uiTimeout); err != nil {
		t.Fatalf("a click on the cut message did not show its end: %v\n%s", err, term.Snapshot())
	}
	longMessageShot(t, term, "3-view")

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), longTail) }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the message view: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "N"); err != nil {
		t.Fatalf("send prefix N: %v", err)
	}
	if err := term.WaitForText(longTail, uiTimeout); err != nil {
		t.Fatalf("prefix N did not show the last message in full: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys("y"); err != nil {
		t.Fatalf("send y: %v", err)
	}
	if err := term.WaitForText("Copied the message.", uiTimeout); err != nil {
		t.Errorf("y did not copy the message: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after reading a long message")
}

// TestLogViewerShowsALongEntryInFull opens the log viewer on the long message
// and reads its end: the selected entry wraps under the list, and enter opens
// it in the message view.
func TestLogViewerShowsALongEntryInFull(t *testing.T) {
	term, _ := start(t, startOpts{cols: 110, rows: 30})
	waitBoot(t, term)
	newWindow(t, term)
	raiseLongMessage(t, term, 100, 220)

	if err := term.SendKeys(tuitest.Ctrl('b'), "D", "l"); err != nil {
		t.Fatalf("open the log viewer: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(paneRows(s), longHead)
	}, uiTimeout); err != nil {
		t.Fatalf("the log viewer does not list the long message: %v\n%s", err, term.Snapshot())
	}
	// Opening the viewer logs a line of its own when it is verbose. Move the
	// cursor onto the long entry, wherever it is.
	for range 4 {
		if strings.Count(paneRows(term.Screen()), longHead) > 1 {
			break
		}
		if err := term.SendKeys("k"); err != nil {
			t.Fatalf("send k: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if got := strings.Count(paneRows(term.Screen()), longHead); got < 2 {
		t.Errorf("the selected long entry is not shown wrapped under the list (%d copies)\n%s", got, term.Snapshot())
	}
	longMessageShot(t, term, "1-logs")

	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send enter: %v", err)
	}
	if err := term.WaitForText(longTail, uiTimeout); err != nil {
		t.Fatalf("enter on the long entry did not show its end: %v\n%s", err, term.Snapshot())
	}
	longMessageShot(t, term, "2-entry")

	// Esc closes the message view and leaves the log viewer open behind it.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), longTail) && strings.Contains(paneRows(s), longHead)
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not go back to the log viewer: %v\n%s", err, term.Snapshot())
	}
}

// TestMessageViewScrolls reads a message longer than the view on a small
// screen: the keys and the wheel both reach its last line and come back to its
// first. The message stays under the 512 byte cap a notification is held to.
func TestMessageViewScrolls(t *testing.T) {
	const cols, rows = 60, 16
	term, _ := start(t, startOpts{cols: cols, rows: rows})
	waitBoot(t, term)
	newWindow(t, term)
	raiseLongMessage(t, term, 100, 199)

	if err := term.SendKeys(tuitest.Ctrl('b'), "N"); err != nil {
		t.Fatalf("send prefix N: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(paneRows(s), longHead) && strings.Contains(s.Text(), "Lines 1 to")
	}, uiTimeout); err != nil {
		t.Fatalf("prefix N did not open the message at its first line: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), longTail) {
		t.Fatalf("the whole message fits on one page, so this test proves nothing\n%s", term.Snapshot())
	}
	longMessageShot(t, term, "1-top")

	atEnd := func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), longTail) && !strings.Contains(paneRows(s), longHead)
	}
	atStart := func(s tuitest.Screen) bool {
		return strings.Contains(paneRows(s), longHead) && !strings.Contains(s.Text(), longTail)
	}

	// Keys: G to the end, g back to the start, then j one line down.
	if err := term.SendKeys("G"); err != nil {
		t.Fatalf("send G: %v", err)
	}
	if err := term.WaitFor(atEnd, uiTimeout); err != nil {
		t.Fatalf("G did not reach the last line: %v\n%s", err, term.Snapshot())
	}
	longMessageShot(t, term, "2-end")
	if err := term.SendKeys("g"); err != nil {
		t.Fatalf("send g: %v", err)
	}
	if err := term.WaitFor(atStart, uiTimeout); err != nil {
		t.Fatalf("g did not come back to the first line: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("j"); err != nil {
		t.Fatalf("send j: %v", err)
	}
	if err := term.WaitForText("Lines 2 to", uiTimeout); err != nil {
		t.Fatalf("j did not move one line: %v\n%s", err, term.Snapshot())
	}

	// The wheel: down until the last line shows, then up until the first.
	for range 60 {
		if atEnd(term.Screen()) {
			break
		}
		wheelAt(t, term, cols/2, rows/2, tuitest.MouseWheelDown, 1)
	}
	if err := term.WaitFor(atEnd, uiTimeout); err != nil {
		t.Fatalf("the wheel did not reach the last line: %v\n%s", err, term.Snapshot())
	}
	for range 60 {
		if atStart(term.Screen()) {
			break
		}
		wheelAt(t, term, cols/2, rows/2, tuitest.MouseWheelUp, 1)
	}
	if err := term.WaitFor(atStart, uiTimeout); err != nil {
		t.Fatalf("the wheel did not come back to the first line: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after scrolling the message view")
}

// TestHoverHoldsAMessage rests the pointer on a message past the time it would
// burn down in, and the message is still there. With the pointer moved away it
// burns down as usual, which is the positive half: the message was not made
// sticky by the hover.
func TestHoverHoldsAMessage(t *testing.T) {
	term, _ := start(t, startOpts{cols: 110, rows: 30})
	waitBoot(t, term)
	newWindow(t, term)
	raiseLongMessage(t, term, 100, 220)

	col, row := dockColumnOf(t, term, longHead)
	mouseHover(t, term, col+2, row)
	// An info message lasts six seconds unless configured.
	time.Sleep(8 * time.Second)
	if !strings.Contains(dockRow(term.Screen()), longHead) {
		t.Fatalf("the message burned down under the pointer\n%s", term.Snapshot())
	}

	mouseHover(t, term, 5, 5)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(dockRow(s), longHead)
	}, 15*time.Second); err != nil {
		t.Fatalf("the message stayed after the pointer left it: %v\n%s", err, term.Snapshot())
	}
}

// TestClipboardAskIsAllowedByAClick: a background pane's clipboard write waits
// on a dock message, and a click on it allows the write. The ask is always
// longer than the dock, so a click that opened the message view instead left
// the write with no way to be allowed.
func TestClipboardAskIsAllowedByAClick(t *testing.T) {
	term, _ := start(t, startOpts{cols: 110, rows: 30})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	// UFJPQkUzODY= is PROBE386, eight characters.
	if err := term.SendKeys(`(sleep 4; printf '\033]52;c;UFJPQkUzODY=\007') &`, tuitest.Enter); err != nil {
		t.Fatalf("start the background write: %v", err)
	}
	leaveTerminalMode(t, term)
	newWindow(t, term)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(dockRow(s), "asks to copy")
	}, uiTimeout); err != nil {
		t.Fatalf("the background write raised no ask: %v\n%s", err, term.Snapshot())
	}
	longMessageShot(t, term, "1-ask")
	col, row := dockColumnOf(t, term, "asks to copy")
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Copied 8 characters", uiTimeout); err != nil {
		t.Fatalf("a click on the ask did not allow the write: %v\n%s", err, term.Snapshot())
	}
}

// TestLongNoSpaceMessageKeepsTheClientResponsive raises an 8000 byte OSC 9
// with no spaces, hovers it and opens it, and the shell still answers at once.
// Wrapping such a text used to take seconds on every frame.
func TestLongNoSpaceMessageKeepsTheClientResponsive(t *testing.T) {
	term, _ := start(t, startOpts{cols: 110, rows: 30})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	cmd := `printf '\033]9;FRZ%s\007' "$(head -c 8000 /dev/zero | tr '\0' x)"; clear`
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("send the notification: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(dockRow(s), "FRZxxx")
	}, uiTimeout); err != nil {
		t.Fatalf("the message never reached the dock: %v\n%s", err, term.Snapshot())
	}
	col, row := dockColumnOf(t, term, "FRZxxx")
	mouseHover(t, term, col+2, row)
	time.Sleep(time.Second)
	mouseHover(t, term, 5, 5)

	if err := term.SendKeys(tuitest.Ctrl('b'), "N"); err != nil {
		t.Fatalf("send prefix N: %v", err)
	}
	if err := term.WaitForText("go to source", 3*time.Second); err != nil {
		t.Fatalf("the message view did not open in time: %v\n%s", err, term.Snapshot())
	}
	longMessageShot(t, term, "1-view")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "go to source") }, 3*time.Second); err != nil {
		t.Fatalf("esc did not close the view in time: %v\n%s", err, term.Snapshot())
	}
	// The beat every mode change gets here before typing: closing the view
	// hands the keyboard back to the pane.
	time.Sleep(insertGuard + 150*time.Millisecond)
	runInShell(t, term, `echo $((6*7))ANS`, "42ANS", 3*time.Second)
}

// TestKeyPressEndsTheHold: a key press ends the hover hold, so the message
// burns down even with the pointer still on it. TestHoverHoldsAMessage is the
// positive half.
func TestKeyPressEndsTheHold(t *testing.T) {
	term, _ := start(t, startOpts{cols: 110, rows: 30})
	waitBoot(t, term)
	newWindow(t, term)
	raiseLongMessage(t, term, 100, 220)

	col, row := dockColumnOf(t, term, longHead)
	mouseHover(t, term, col+2, row)
	time.Sleep(500 * time.Millisecond)
	if err := term.SendKeys("x"); err != nil {
		t.Fatalf("send a key: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(dockRow(s), longHead)
	}, 12*time.Second); err != nil {
		t.Fatalf("the message stayed after a key press: %v\n%s", err, term.Snapshot())
	}
}

// TestViewersOverTheRailAndTheLeader opens the message view with the rail
// focused, and its keys reach it, the leader included: a prefix chord works
// over it, and a paste does not reach the pane under it.
func TestViewersOverTheRailAndTheLeader(t *testing.T) {
	term, _ := start(t, startOpts{cols: 110, rows: 30})
	waitBoot(t, term)
	newWindow(t, term)
	raiseLongMessage(t, term, 100, 220)

	// A paste while the view is open is dropped.
	if err := term.SendKeys(tuitest.Ctrl('b'), "N"); err != nil {
		t.Fatalf("send prefix N: %v", err)
	}
	if err := term.WaitForText(longTail, uiTimeout); err != nil {
		t.Fatalf("prefix N did not open the view: %v\n%s", err, term.Snapshot())
	}
	if err := term.Paste("PASTE" + "LEAK"); err != nil {
		t.Fatalf("paste: %v", err)
	}
	// The leader works over the view: ctrl+b D l opens the log viewer.
	if err := term.SendKeys(tuitest.Ctrl('b'), "D", "l"); err != nil {
		t.Fatalf("open the log viewer: %v", err)
	}
	if err := term.WaitForText("copy errors", uiTimeout); err != nil {
		t.Errorf("the leader did not work over the message view: %v\n%s", err, term.Snapshot())
	}
	for _, k := range []tuitest.Key{tuitest.Esc, tuitest.Esc} {
		if err := term.SendKeys(k); err != nil {
			t.Fatalf("send esc: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), longTail) }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the viewers: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(term.Screen().Text(), "PASTELEAK") {
		t.Errorf("a paste made while the view was open reached the pane\n%s", term.Snapshot())
	}

	// With the rail focused, the view still takes its keys.
	if err := term.SendKeys(tuitest.Ctrl('b'), "e"); err != nil {
		t.Fatalf("focus the rail: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys(tuitest.Ctrl('b'), "N"); err != nil {
		t.Fatalf("send prefix N: %v", err)
	}
	if err := term.WaitForText(longTail, uiTimeout); err != nil {
		t.Fatalf("prefix N did not open the view over the rail: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), longTail) }, uiTimeout); err != nil {
		t.Fatalf("esc did not reach the view over the rail: %v\n%s", err, term.Snapshot())
	}
}
