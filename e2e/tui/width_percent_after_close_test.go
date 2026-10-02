package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// widthPercentSession opens three tiled panes, and when closeFirst is set
// closes the first one. It focuses the pane named game and presses the width
// key for 80%, with send-keys or, when keyboard is set, on the client's own
// keyboard. It returns the client and the isolation root.
func widthPercentSession(t *testing.T, closeFirst, keyboard bool) (*tuitest.Terminal, string) {
	t.Helper()
	term, base := start(t, startOpts{cols: 160, rows: 48, args: []string{"new", "home"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "the first pane")
	enableTiling(t, term)
	for _, name := range []string{"game", "pad"} {
		if out, err := tuiosCLI(t, base, "new-window", name, "-s", "home", "--no-focus", "--", "sleep", "600"); err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
	}
	rects := waitForSettledGeometryIn(t, base, "home", 3)
	settledScreenBoxes(t, term, 3)

	if closeFirst {
		if out, err := tuiosCLI(t, base, "focus-window", "-s", "home", rects[0].ID); err != nil {
			t.Fatalf("focus-window first: %v\n%s", err, out)
		}
		time.Sleep(500 * time.Millisecond)
		closeFocusedWindow(t, term, 2)
		waitForSettledGeometryIn(t, base, "home", 2)
		settledScreenBoxes(t, term, 2)
	}
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "home", "game"); err != nil {
		t.Fatalf("focus-window game: %v\n%s", err, out)
	}
	time.Sleep(time.Second)
	if keyboard {
		send(t, term, tuitest.Ctrl('b'))
		send(t, term, "L")
		send(t, term, "8")
		return term, base
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "home", "PREFIX shift+l 8"); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, out)
	}
	return term, base
}

// assertGameAt80 waits for the pane on the screen and the pane the daemon
// lists to both take about 80% of the width.
func assertGameAt80(t *testing.T, term *tuitest.Terminal, base string, panes int) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		boxes := screenBoxes(term.Screen())
		rects, _ := parseWindowsOut(t, base)
		widest, total := 0, 0
		for _, b := range boxes {
			if b.Y == boxes[0].Y {
				total += b.Width
				widest = max(widest, b.Width)
			}
		}
		listedWidest, listedTotal := 0, 0
		for _, r := range rects {
			if r.Y == rects[0].Y {
				listedTotal += r.Width
				listedWidest = max(listedWidest, r.Width)
			}
		}
		onScreen := len(boxes) == panes && total > 0 && widest*100/total >= 70
		listed := len(rects) == panes && listedTotal > 0 && listedWidest*100/listedTotal >= 70
		if onScreen && listed {
			saveArtifact(t, term, artifactDir(t), "width-80")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the 80%% width key, the screen shows boxes %v and the daemon lists %v: want the focused pane at 80%% in both\n%s",
				boxes, rects, term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func parseWindowsOut(t *testing.T, base string) ([]winRect, bool) {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", "home")
	if err != nil {
		return nil, false
	}
	return parseWindows(out)
}

// The layout width keys, sent with send-keys to the attached client, have to
// move the tiles on the screen and not only the PTYs.
//
// After the last key of a send-keys, the client dropped the workspace's tree
// and tiled it again from nothing. That put every tile back to equal shares,
// while the daemon kept the sizes the keys had set: in the report the daemon
// listed 87 and 22 columns and the screen drew two halves. The report saw it
// after a close. The close is not needed, so both shapes are tested.

// TestWidthPercentAfterAClose is the shape in the report.
func TestWidthPercentAfterAClose(t *testing.T) {
	term, base := widthPercentSession(t, true, false)
	assertGameAt80(t, term, base, 2)
}

// TestWidthPercentWithNoClose is the same keys with no close first.
func TestWidthPercentWithNoClose(t *testing.T) {
	term, base := widthPercentSession(t, false, false)
	assertGameAt80(t, term, base, 3)
}

// TestWidthPercentFromTheKeyboard is the positive half: the same keys pressed
// on the client's own keyboard, after the same close. It passes with and
// without the fix, so the fixture and the assertion are sound.
func TestWidthPercentFromTheKeyboard(t *testing.T) {
	term, base := widthPercentSession(t, true, true)
	assertGameAt80(t, term, base, 2)
}
