package tuie2e

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// dividerColumn returns the screen column holding the interior vertical divider
// between two side-by-side panes: the column drawn with a vertical line glyph on
// more rows than any other interior column.
func dividerColumn(s tuitest.Screen) int {
	lines := strings.Split(s.Text(), "\n")
	if len(lines) == 0 {
		return -1
	}
	counts := map[int]int{}
	for _, line := range lines {
		col := 0
		for _, r := range line {
			if r == '│' || r == '┃' || r == '║' || r == '|' {
				counts[col]++
			}
			col++
		}
	}
	best, bestCol := 0, -1
	for col, n := range counts {
		// Skip the screen's own outer edges; only an interior divider matters.
		if col <= 1 {
			continue
		}
		if n > best {
			best, bestCol = n, col
		}
	}
	if best < 5 {
		return -1
	}
	return bestCol
}

// placedRect is one a=p placement reduced to where it starts and how wide it is.
type placedRect struct {
	col, row int // zero-based screen cell of the image's top-left
	cols     int // c= , the columns the host is told to draw it over
}

var placementColsRE = regexp.MustCompile(`\bc=(\d+)`)

// lastPlacement returns the final placement in the stream, which is the one the
// host is left showing.
func lastPlacement(t *testing.T, stream []byte) placedRect {
	t.Helper()
	ms := placementRE.FindAllSubmatch(stream, -1)
	if len(ms) == 0 {
		t.Fatalf("no kitty placement reached the host")
	}
	m := ms[len(ms)-1]
	row, _ := strconv.Atoi(string(m[1]))
	col, _ := strconv.Atoi(string(m[2]))
	got := placedRect{col: col - 1, row: row - 1}
	if c := placementColsRE.FindSubmatch(m[3]); c != nil {
		got.cols, _ = strconv.Atoi(string(c[1]))
	}
	return got
}

// TestKittyImageStopsAtPaneBorder is the reported scenario: shared borders on,
// two tiled panes, and a full-window graphical guest (terminal-browser) drawing
// into the left one. The image must be given exactly the columns the guest was
// told it had, and its right edge must land on the pane's last content column -
// the column before the divider, which tuios still has to draw.
//
// A narrower c= is the reported symptom from the other side: kitty scales the
// frame's full pixel width into fewer cells than it was rendered for, so the
// right-hand side of the page is squeezed off the visible area.
func TestKittyImageStopsAtPaneBorder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		clickX int
	}{
		{"shared-borders-left", []string{"--shared-borders"}, 20},
		{"shared-borders-right", []string{"--shared-borders"}, 90},
		{"own-borders-left", nil, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := &hostStream{}
			term, _ := start(t, startOpts{
				cols: 120, rows: 40, args: tc.args,
				env: []string{"TUIOS_KITTY_GRAPHICS=1", "TUIOS_SIXEL_GRAPHICS=0"},
				out: stream,
			})
			waitBoot(t, term)
			newWindow(t, term)
			newWindow(t, term)
			enableTiling(t, term)
			waitWindowCount(t, term, 2, "two tiled panes")

			mouseClick(t, term, tc.clickX, 12, tuitest.MouseLeft, 0)
			time.Sleep(400 * time.Millisecond)
			enterTerminalMode(t, term)
			// A click into a pane can already be terminal mode, in which case
			// enterTerminalMode's own 'i' was typed at the shell prompt. Discard
			// whatever is on the line before the shell is asked anything.
			if err := term.SendKeys("\x15"); err != nil {
				t.Fatalf("clear prompt line: %v", err)
			}
			time.Sleep(insertGuard)
			rows, cols := reportedPaneSize(t, term, "A")

			// terminal-browser renders at the pane's own pixel size, which is
			// its cell size times the cell metrics tuios reports. The e2e host
			// has no pixel size of its own, so tuios falls back to 9x20.
			frame := kittyFrameFile(t, t.TempDir(), cols*9, rows*20)
			stream.mark("frame")
			typeLine(t, term, "cat "+frame)
			leaveTerminalMode(t, term)
			time.Sleep(2 * time.Second)

			if dump := os.Getenv("TUIOS_KITTY_CAPTURE"); dump != "" {
				_ = os.WriteFile(fmt.Sprintf("%s.%s", dump, tc.name), stream.bytes(), 0o644)
			}

			got := lastPlacement(t, stream.bytes())
			divider := dividerColumn(term.Screen())
			t.Logf("pane=%dx%d placement col=%d row=%d c=%d rightEdge=%d divider=%d",
				cols, rows, got.col, got.row, got.cols, got.col+got.cols-1, divider)

			if got.cols != cols {
				t.Errorf("image drawn over %d columns in a pane the guest was told was %d wide "+
					"(placed at col %d)", got.cols, cols, got.col)
			}
			// A full-pane image ends where the pane does: on the column tuios
			// draws the divider in for the pane on the divider's left, and on
			// the screen edge for the pane on its right. Landing anywhere else
			// is either a gap the guest rendered pixels for or a column of the
			// neighbour painted over.
			wantEnd := 120
			if divider >= 0 && got.col < divider {
				wantEnd = divider
			}
			if got.col+got.cols != wantEnd {
				t.Errorf("image spans columns %d..%d; its right edge should be the last column "+
					"before %d (divider at %d)", got.col, got.col+got.cols-1, wantEnd, divider)
			}
		})
	}
}
