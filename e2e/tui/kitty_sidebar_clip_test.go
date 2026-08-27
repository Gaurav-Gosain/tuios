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

// railBand returns the first and last screen column the sidebar rail draws in,
// found from the rail's own header row rather than from a config value. It
// returns (-1, -1) when the rail is not on screen.
func railBand(s tuitest.Screen) (first, last int) {
	for _, line := range strings.Split(s.Text(), "\n") {
		runes := []rune(line)
		i := runeIndex(runes, sidebarHeader)
		if i < 0 {
			continue
		}
		// The header sits inside the rail's own padding; the band is the run of
		// non-space either side of it on this row.
		first, last = i, i+len([]rune(sidebarHeader))-1
		for first > 0 && runes[first-1] != ' ' {
			first--
		}
		for last+1 < len(runes) && runes[last+1] != ' ' {
			last++
		}
		return first, last
	}
	return -1, -1
}

// TestKittyImageStaysOutOfTheRail is the reported scenario: a graphical guest
// filling its pane while the sidebar reserves columns at a screen edge. The
// image the host is told to draw has to end inside the pane. The host draws a
// kitty placement over the composed frame, so a placement that reaches into the
// rail's columns paints over the sidebar even though every cell tuios composed
// was correct.
//
// Both edges are covered, and both orders: the rail already up when the image
// arrives, and the rail opening under an image that is already placed.
func TestKittyImageStaysOutOfTheRail(t *testing.T) {
	for _, tc := range []struct {
		name string
		side string
		// railFirst boots with the rail already up. Otherwise the image is
		// placed against the full-width layout first and the rail is toggled on
		// underneath it, which is the order the report was hit in.
		railFirst bool
	}{
		{"rail-right-first", "right", true},
		{"rail-right-after", "right", false},
		{"rail-left-first", "left", true},
		{"rail-left-after", "left", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			enabled := "false"
			if tc.railFirst {
				enabled = "true"
			}
			writeConfig(t, base, "[appearance]\nsidebar_enabled = "+enabled+
				"\nsidebar_position = \""+tc.side+"\"\n")

			stream := &hostStream{}
			term := startIn(t, base, startOpts{
				cols: 120, rows: 40,
				env: []string{"TUIOS_KITTY_GRAPHICS=1", "TUIOS_SIXEL_GRAPHICS=0"},
				out: stream,
			})
			waitBoot(t, term)
			newWindow(t, term)
			newWindow(t, term)
			enableTiling(t, term)
			waitWindowCount(t, term, 2, "two tiled panes")
			if tc.railFirst {
				waitRailUp(t, term)
			}

			// The pane that shares an edge with the rail.
			clickX := 90
			if tc.side == "left" {
				clickX = 30
			}
			mouseClick(t, term, clickX, 12, tuitest.MouseLeft, 0)
			time.Sleep(400 * time.Millisecond)
			enterTerminalMode(t, term)
			if err := term.SendKeys("\x15"); err != nil {
				t.Fatalf("clear prompt line: %v", err)
			}
			time.Sleep(insertGuard)
			rows, cols := reportedPaneSize(t, term, "A")

			// A guest that renders at exactly the pane it was told it has, the
			// way terminal-browser does. The e2e host reports no pixel size, so
			// tuios falls back to 9x20 per cell.
			frame := kittyFrameFile(t, t.TempDir(), cols*9, rows*20)
			stream.mark("frame")
			typeLine(t, term, "cat "+frame)
			leaveTerminalMode(t, term)
			time.Sleep(2 * time.Second)

			if !tc.railFirst {
				toggleSidebarViaPalette(t, term)
				waitRailUp(t, term)
				time.Sleep(2 * time.Second)
			}

			if dump := os.Getenv("TUIOS_KITTY_CAPTURE"); dump != "" {
				_ = os.WriteFile(fmt.Sprintf("%s.%s", dump, tc.name), stream.bytes(), 0o644)
			}

			got := lastPlacement(t, stream.bytes())
			bars := borderColumns(term.Screen())
			railFirst, railLast := railBand(term.Screen())
			t.Logf("pane=%dx%d placement cols %d..%d (col=%d row=%d c=%d) borders=%v rail=%d..%d\n%s",
				cols, rows, got.col, got.col+got.cols-1, got.col, got.row, got.cols,
				bars, railFirst, railLast, term.Snapshot())

			if railFirst < 0 {
				t.Fatalf("the rail is not on screen; nothing to clip against\n%s", term.Snapshot())
			}
			if got.col+got.cols-1 >= railFirst && got.col <= railLast {
				t.Errorf("image spans columns %d..%d, which overlaps the rail at %d..%d",
					got.col, got.col+got.cols-1, railFirst, railLast)
			}
		})
	}
}

func waitRailUp(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), sidebarHeader)
	}, uiTimeout); err != nil {
		t.Fatalf("sidebar did not come up: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(600 * time.Millisecond)
}

// runeIndex is strings.Index in screen columns rather than bytes.
func runeIndex(line []rune, want string) int {
	w := []rune(want)
	for i := 0; i+len(w) <= len(line); i++ {
		if string(line[i:i+len(w)]) == want {
			return i
		}
	}
	return -1
}

// windowCorner finds a pane box corner on screen. The top-left one is the handle
// a drag has to grab; the top-right one is the only one still visible when the
// pane has been shoved under a left-hand rail.
func windowCorner(t *testing.T, term *tuitest.Terminal, corner string) (col, row int) {
	t.Helper()
	for r, line := range strings.Split(term.Screen().Text(), "\n") {
		if i := runeIndex([]rune(line), corner); i >= 0 {
			return i, r
		}
	}
	t.Fatalf("no pane box corner %q on screen\n%s", corner, term.Snapshot())
	return 0, 0
}

func windowTopLeft(t *testing.T, term *tuitest.Terminal) (col, row int) {
	t.Helper()
	return windowCorner(t, term, "\u256d")
}

// TestKittyImageStaysOutOfTheRailWhenPaneOverlapsIt is the same contract for a
// floating pane. A floating pane is deliberately allowed to hang past the
// content region - ClampWindowsToView only keeps a strip of it reachable - so
// its guest is told a width that runs under the rail. Every cell tuios composes
// for such a pane still stops at the rail, because the rail is drawn over the
// panes. The image has to stop there too.
//
// Both edges, because a crop off the left is not the same operation as a crop
// off the right: it moves the image as well as narrowing it, so it needs a
// source rectangle that starts part way into the bitmap. Getting that wrong
// shows up as an image that fits but is drawn from the wrong pixel column.
func TestKittyImageStaysOutOfTheRailWhenPaneOverlapsIt(t *testing.T) {
	for _, side := range []string{"right", "left"} {
		t.Run(side, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, "[appearance]\nsidebar_enabled = false\nsidebar_position = \""+side+"\"\n")

			stream := &hostStream{}
			term := startIn(t, base, startOpts{
				cols: 120, rows: 40,
				env: []string{"TUIOS_KITTY_GRAPHICS=1", "TUIOS_SIXEL_GRAPHICS=0"},
				out: stream,
			})
			waitBoot(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 1, "one floating pane")

			// The image goes up while the pane is where it can be read, then the
			// pane is shoved against the screen edge the rail will take and the
			// rail is opened under it. A floating pane is not re-tiled, and the
			// clamp only guarantees a reachable strip, so the pane keeps a
			// rectangle that runs under the rail.
			col, row := windowTopLeft(t, term)
			mouseClick(t, term, col+5, row+4, tuitest.MouseLeft, 0)
			time.Sleep(400 * time.Millisecond)
			enterTerminalMode(t, term)
			if err := term.SendKeys("\x15"); err != nil {
				t.Fatalf("clear prompt line: %v", err)
			}
			time.Sleep(insertGuard)
			rows, cols := reportedPaneSize(t, term, "A")

			frame := kittyFrameFile(t, t.TempDir(), cols*9, rows*20)
			stream.mark("frame")
			typeLine(t, term, "cat "+frame)
			leaveTerminalMode(t, term)
			time.Sleep(2 * time.Second)

			target := 112
			if side == "left" {
				target = 0
			}
			col, row = windowTopLeft(t, term)
			mouseDrag(t, term, col+10, row, target, row, tuitest.MouseLeft, 0)
			time.Sleep(700 * time.Millisecond)

			toggleSidebarViaPalette(t, term)
			waitRailUp(t, term)
			time.Sleep(2 * time.Second)

			got := lastPlacement(t, stream.bytes())
			drawnRows := lastPlacementRows(t, stream.bytes())
			railFirst, railLast := railBand(term.Screen())
			t.Logf("pane=%dx%d placement cols %d..%d (col=%d row=%d c=%d r=%d) rail=%d..%d\nas the host paints it:\n%s",
				cols, rows, got.col, got.col+got.cols-1, got.col, got.row, got.cols, drawnRows,
				railFirst, railLast,
				paintedFrame(term.Screen(), got.col, got.row, got.cols, drawnRows))

			if railFirst < 0 {
				t.Fatalf("the rail is not on screen; nothing to clip against\n%s", term.Snapshot())
			}
			if got.col+got.cols-1 >= railFirst && got.col <= railLast {
				t.Errorf("image spans columns %d..%d, which reaches the rail at %d..%d",
					got.col, got.col+got.cols-1, railFirst, railLast)
			}
			if side == "left" {
				// Cropping off the left moves the image as well as narrowing
				// it. Without a source rectangle that starts past the cropped
				// columns the host draws the same number of cells from the
				// wrong pixel column: the page slides right by the width of the
				// rail.
				if srcX := lastPlacementSrcX(t, stream.bytes()); srcX <= 0 {
					t.Errorf("placement carries x=%d; a left crop needs a source rectangle "+
						"that starts past the columns the rail took", srcX)
				}
			}
		})
	}
}

var (
	placementRowsRE = regexp.MustCompile(`\br=(\d+)`)
	placementSrcXRE = regexp.MustCompile(`,x=(\d+)`)
)

// lastPlacementRows is lastPlacement's r=, the rows the host is told to draw the
// image over.
func lastPlacementRows(t *testing.T, stream []byte) int {
	t.Helper()
	ms := placementRE.FindAllSubmatch(stream, -1)
	if len(ms) == 0 {
		t.Fatalf("no kitty placement reached the host")
	}
	m := ms[len(ms)-1][3]
	r := placementRowsRE.FindSubmatch(m)
	if r == nil {
		t.Fatalf("last placement carries no r=: %q", m)
	}
	n, _ := strconv.Atoi(string(r[1]))
	return n
}

// dockTopRow is the first screen row the dock draws in, found from the rule it
// draws across the whole screen above its status line.
func dockTopRow(s tuitest.Screen) int {
	lines := strings.Split(s.Text(), "\n")
	for r := len(lines) - 1; r >= 0; r-- {
		trimmed := strings.TrimRight(lines[r], " ")
		if len([]rune(trimmed)) > 100 && strings.Count(trimmed, "─") > 100 {
			return r
		}
	}
	return -1
}

// TestKittyImageStaysOutOfTheDock is the vertical half of the same contract. The
// dock reserves rows at the bottom the way the rail reserves columns at the
// side, and a floating pane is allowed to hang past them - the clamp only keeps
// a few rows of it reachable. The image in such a pane has to stop where the
// pane layout box stops, not one row short of the screen.
func TestKittyImageStaysOutOfTheDock(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nsidebar_enabled = false\n")

	stream := &hostStream{}
	term := startIn(t, base, startOpts{
		cols: 120, rows: 40,
		env: []string{"TUIOS_KITTY_GRAPHICS=1", "TUIOS_SIXEL_GRAPHICS=0"},
		out: stream,
	})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "one floating pane")

	// Shove the pane down until it hangs past the dock.
	col, row := windowTopLeft(t, term)
	mouseDrag(t, term, col+10, row, col+10, 25, tuitest.MouseLeft, 0)
	time.Sleep(700 * time.Millisecond)

	col, row = windowTopLeft(t, term)
	mouseClick(t, term, col+5, row+2, tuitest.MouseLeft, 0)
	time.Sleep(400 * time.Millisecond)
	enterTerminalMode(t, term)
	if err := term.SendKeys("\x15"); err != nil {
		t.Fatalf("clear prompt line: %v", err)
	}
	time.Sleep(insertGuard)
	rows, cols := reportedPaneSize(t, term, "A")

	frame := kittyFrameFile(t, t.TempDir(), cols*9, rows*20)
	stream.mark("frame")
	typeLine(t, term, "cat "+frame)
	leaveTerminalMode(t, term)
	time.Sleep(2 * time.Second)

	got := lastPlacement(t, stream.bytes())
	drawnRows := lastPlacementRows(t, stream.bytes())
	dockRow := dockTopRow(term.Screen())
	t.Logf("pane=%dx%d placement rows %d..%d (col=%d row=%d r=%d c=%d) dock starts at row %d\nas the host paints it:\n%s",
		cols, rows, got.row, got.row+drawnRows-1, got.col, got.row, drawnRows, got.cols,
		dockRow, paintedFrame(term.Screen(), got.col, got.row, got.cols, drawnRows))

	if dockRow < 0 {
		t.Fatalf("the dock is not on screen; nothing to clip against\n%s", term.Snapshot())
	}
	if got.row+drawnRows-1 >= dockRow {
		t.Errorf("image spans rows %d..%d, which reaches the dock at row %d",
			got.row, got.row+drawnRows-1, dockRow)
	}
}

// lastPlacementSrcX is the x= source offset in pixels on the final placement,
// or 0 when it carries none.
func lastPlacementSrcX(t *testing.T, stream []byte) int {
	t.Helper()
	ms := placementRE.FindAllSubmatch(stream, -1)
	if len(ms) == 0 {
		t.Fatalf("no kitty placement reached the host")
	}
	m := placementSrcXRE.FindSubmatch(ms[len(ms)-1][3])
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// paintedFrame is the composed frame with the host's placement rectangle drawn
// over it, which is what the user actually sees: tuios composes every cell,
// then the host paints the image on top. The image itself is not in the cell
// grid, so this is the only way to see the two layers together.
func paintedFrame(s tuitest.Screen, col, row, cols, rows int) string {
	lines := strings.Split(s.Text(), "\n")
	for r := row; r < row+rows && r < len(lines); r++ {
		runes := []rune(lines[r])
		for len(runes) < col+cols {
			runes = append(runes, ' ')
		}
		for c := col; c < col+cols; c++ {
			runes[c] = '\u2593'
		}
		lines[r] = string(runes)
	}
	return strings.Join(lines, "\n")
}

// TestKittyImageStopsAtAShortPaneBottom is the same contract against a pane that
// is neither full height nor hanging past anything: the image has to end on the
// pane's last content row, the row above its bottom rule. It is here so the clip
// cannot be a special case for a pane that overlaps chrome - the ordinary pane
// has to keep working, and its bottom is its own border, not the layout box.
func TestKittyImageStopsAtAShortPaneBottom(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nsidebar_enabled = false\n")

	stream := &hostStream{}
	term := startIn(t, base, startOpts{
		cols: 120, rows: 40,
		env: []string{"TUIOS_KITTY_GRAPHICS=1", "TUIOS_SIXEL_GRAPHICS=0"},
		out: stream,
	})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "one floating pane")

	col, row := windowTopLeft(t, term)
	mouseClick(t, term, col+5, row+4, tuitest.MouseLeft, 0)
	time.Sleep(400 * time.Millisecond)
	enterTerminalMode(t, term)
	if err := term.SendKeys("\x15"); err != nil {
		t.Fatalf("clear prompt line: %v", err)
	}
	time.Sleep(insertGuard)
	rows, cols := reportedPaneSize(t, term, "A")

	frame := kittyFrameFile(t, t.TempDir(), cols*9, rows*20)
	stream.mark("frame")
	typeLine(t, term, "cat "+frame)
	leaveTerminalMode(t, term)
	time.Sleep(2 * time.Second)

	got := lastPlacement(t, stream.bytes())
	drawnRows := lastPlacementRows(t, stream.bytes())
	_, bottomRow := windowCorner(t, term, "╰")
	t.Logf("pane=%dx%d placement rows %d..%d (col=%d row=%d r=%d c=%d) pane bottom rule at row %d\nas the host paints it:\n%s",
		cols, rows, got.row, got.row+drawnRows-1, got.col, got.row, drawnRows, got.cols,
		bottomRow, paintedFrame(term.Screen(), got.col, got.row, got.cols, drawnRows))

	if want := bottomRow - 1; got.row+drawnRows-1 != want {
		t.Errorf("image spans rows %d..%d; its last row should be %d, the pane's last content row "+
			"above the rule at %d", got.row, got.row+drawnRows-1, want, bottomRow)
	}
}
