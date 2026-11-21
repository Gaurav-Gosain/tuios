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

func borderColumns(s tuitest.Screen) []int {
	counts := map[int]int{}
	widest := 0
	for line := range strings.SplitSeq(s.Text(), "\n") {
		col := 0
		for _, r := range line {
			if r == '│' || r == '┃' || r == '║' {
				counts[col]++
			}
			col++
		}
		widest = max(widest, col)
	}
	var cols []int
	for col := range widest {
		if counts[col] >= 10 {
			cols = append(cols, col)
		}
	}
	return cols
}

func borderRightOf(cols []int, col int) int {
	for _, c := range cols {
		if c > col {
			return c
		}
	}
	return -1
}

type placedRect struct {
	col, row int
	cols     int
}

var placementColsRE = regexp.MustCompile(`\bc=(\d+)`)

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
// told it had, and its right edge must land on the pane's last content column:
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
			if err := term.SendKeys("\x15"); err != nil {
				t.Fatalf("clear prompt line: %v", err)
			}
			rows, cols := reportedPaneSize(t, term, "A")

			frame := kittyFrameFile(t, t.TempDir(), cols*9, rows*20)
			stream.mark("frame")
			typeLine(t, term, "cat "+frame)
			leaveTerminalMode(t, term)
			time.Sleep(2 * time.Second)

			if dump := os.Getenv("TUIOS_KITTY_CAPTURE"); dump != "" {
				_ = os.WriteFile(fmt.Sprintf("%s.%s", dump, tc.name), stream.bytes(), 0o644)
			}

			got := lastPlacement(t, stream.bytes())
			bars := borderColumns(term.Screen())
			t.Logf("pane=%dx%d placement col=%d row=%d c=%d rightEdge=%d borders=%v",
				cols, rows, got.col, got.row, got.cols, got.col+got.cols-1, bars)

			if got.cols != cols {
				t.Errorf("image drawn over %d columns in a pane the guest was told was %d wide "+
					"(placed at col %d)", got.cols, cols, got.col)
			}
			wantEnd := borderRightOf(bars, got.col)
			if wantEnd < 0 {
				wantEnd = 120
			}
			if got.col+got.cols != wantEnd {
				t.Errorf("image spans columns %d..%d; its right edge should be the column "+
					"before %d, the pane's right-hand boundary (rules at %v)",
					got.col, got.col+got.cols-1, wantEnd, bars)
			}
		})
	}
}
