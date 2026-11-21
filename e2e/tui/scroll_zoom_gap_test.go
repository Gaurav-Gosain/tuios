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

const (
	zoomGapCols = 120
	zoomGapRows = 30
)

func zoomGapClient(t *testing.T, daemon bool, zoomSize int, animations bool) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, fmt.Sprintf(
		"[startup]\ntiled = true\nlayout = \"scrolling\"\n[appearance]\nzoom_size = %d\n", zoomSize))
	o := startOpts{cols: zoomGapCols, rows: zoomGapRows, animations: animations}
	var term *tuitest.Terminal
	if daemon {
		if out, err := tuiosCLI(t, base, "new", "zoomgap", "--detach"); err != nil {
			t.Fatalf("create session: %v: %s", err, out)
		}
		term = attachIn(t, base, "zoomgap", o)
	} else {
		term = startIn(t, base, o)
		waitBoot(t, term)
	}
	for i, name := range []string{"ALPHA", "BRAVO", "CHARLIE", "DELTA"} {
		if i > 0 || countWindows(term.Screen()) == 0 {
			newWindow(t, term)
		}
		renameWindow(t, term, name)
	}
	waitWindowCount(t, term, 4, "four panes")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the frame never settled: %v", err)
	}
	return term
}

func stripGap(frame string) string {
	row := []rune(strings.SplitN(frame, "\n", 2)[0])
	if len(row) < zoomGapCols-1 {
		return fmt.Sprintf("the border row ends at column %d of %d", len(row), zoomGapCols)
	}
	for x, r := range row {
		if r != ' ' {
			continue
		}
		if x+1 < len(row) && row[x+1] == ' ' {
			return fmt.Sprintf("blank cells at columns %d and %d of the border row", x, x+1)
		}
		if x+1 < len(row) && row[x+1] == '╭' {
			return fmt.Sprintf("a blank cell at column %d, before a pane's corner", x)
		}
	}
	return ""
}

func moveFrames(t *testing.T, term *tuitest.Terminal, keys ...any) []string {
	t.Helper()
	if err := term.SendKeys(keys...); err != nil {
		t.Fatalf("send keys: %v", err)
	}
	return framesUntilStill(t, term)
}

func framesUntilStill(t *testing.T, term *tuitest.Terminal) []string {
	t.Helper()
	var frames []string
	start := time.Now()
	last := start
	for time.Since(start) < 5*time.Second {
		f := term.Snapshot()
		if len(frames) == 0 || frames[len(frames)-1] != f {
			frames = append(frames, f)
			last = time.Now()
		}
		if time.Since(last) > 400*time.Millisecond {
			return frames
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the screen never held still\n%s", term.Snapshot())
	return nil
}

func checkFrames(t *testing.T, dir, move string, frames []string) {
	t.Helper()
	for i, f := range frames {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%02d.txt", move, i)), []byte(f), 0o644)
	}
	for i, f := range frames {
		if gap := stripGap(f); gap != "" {
			t.Errorf("%s, frame %d of %d: %s\n%s", move, i, len(frames), gap, f)
			return
		}
	}
}

func zoomedPaneShown(frame string) bool {
	row := []rune(strings.SplitN(frame, "\n", 2)[0])
	for i, r := range row {
		if r != '╭' {
			continue
		}
		for j := i + 1; j < len(row); j++ {
			if row[j] == '╮' {
				if j-i+1 >= 100 {
					return true
				}
				break
			}
		}
	}
	return false
}

func requireZoom(t *testing.T, term *tuitest.Terminal, on bool, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return zoomedPaneShown(s.Text()) == on
	}, uiTimeout); err != nil {
		t.Fatalf("zoomed pane shown = %v expected %s: %v\n%s", on, what, err, term.Snapshot())
	}
}

// TestScrollingZoomMoveShowsNoGap is the report, in standalone and daemon mode,
// for a zoom of part of the screen (a wide column) and of the whole screen (a
// box over the strip). The last run has animations off, where the strip still
// slides when it only moves panes and places them in one step when a column
// changes width.
func TestScrollingZoomMoveShowsNoGap(t *testing.T) {
	runs := []struct {
		zoomSize   int
		daemon     bool
		animations bool
	}{
		{95, false, true}, {95, true, true}, {100, false, true}, {100, true, true},
		{95, false, false},
	}
	for _, r := range runs {
		name := fmt.Sprintf("zoom%d/daemon=%v", r.zoomSize, r.daemon)
		if !r.animations {
			name += "/no-animations"
		}
		t.Run(name, func(t *testing.T) {
			term := zoomGapClient(t, r.daemon, r.zoomSize, r.animations)
			dir := artifactDir(t)

			if gap := stripGap(term.Snapshot()); gap != "" {
				t.Fatalf("the settled strip already shows ground: %s\n%s", gap, term.Snapshot())
			}
			checkFrames(t, dir, "zoom", moveFrames(t, term, "z"))
			requireZoom(t, term, true, "after z")

			left, right := tuitest.Alt(tuitest.Left), tuitest.Alt(tuitest.Right)
			moves := []struct {
				name string
				key  any
			}{
				{"left1", left}, {"left2", left}, {"left3", left}, {"left-past-first", left},
				{"right1", right}, {"right2", right}, {"right3", right}, {"right-past-last", right},
			}
			slid := 0
			for _, mv := range moves {
				frames := moveFrames(t, term, mv.key)
				checkFrames(t, dir, mv.name, frames)
				if len(frames) >= 3 {
					slid++
				}
			}
			requireZoom(t, term, true, "after the moves")
			if r.animations && slid < 6 {
				t.Fatalf("only %d of the six real moves drew an in-between frame; nothing was checked mid-slide", slid)
			}

			checkFrames(t, dir, "unzoom-mid-slide", moveFrames(t, term, left, "z"))
			requireZoom(t, term, false, "after z during the slide")

			checkFrames(t, dir, "rezoom", moveFrames(t, term, "z"))
			requireZoom(t, term, true, "after zooming again")
			for _, button := range []tuitest.MouseButton{tuitest.MouseWheelUp, tuitest.MouseWheelDown} {
				for range 4 {
					mousePress(t, term, 60, 15, button, tuitest.ModAlt)
				}
				checkFrames(t, dir, fmt.Sprintf("wheel-%d", button), framesUntilStill(t, term))
			}
		})
	}
}
