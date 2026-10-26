package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	for line := range strings.SplitSeq(s.Text(), "\n") {
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
// content region (ClampWindowsToView only keeps a strip of it reachable), so
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
	for r, line := range slices.Backward(lines) {
		trimmed := strings.TrimRight(line, " ")
		if len([]rune(trimmed)) > 100 && strings.Count(trimmed, "─") > 100 {
			return r
		}
	}
	return -1
}

// TestKittyImageStaysOutOfTheDock is the vertical half of the same contract. The
// dock reserves rows at the bottom the way the rail reserves columns at the
// side, and a floating pane is allowed to hang past them: the clamp only keeps
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
// cannot be a special case for a pane that overlaps chrome. The ordinary pane
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

// hostRect is one placement the host is showing, in screen cells.
type hostRect struct{ col, row, cols, rows int }

func (r hostRect) has(col, row int) bool {
	return col >= r.col && col < r.col+r.cols && row >= r.row && row < r.row+r.rows
}

// hostImageRects replays every placement and delete in a host stream and
// returns the rectangles the host is still drawing at its end, every image
// and every placement id. An a=p with no c= or r= is counted as one cell on
// that axis, which can only make an overlap harder to find, never easier.
func hostImageRects(stream []byte) []hostRect {
	type key struct{ image, placement string }
	live := map[key]hostRect{}
	row, col := 0, 0
	for _, m := range movedPlacementRE.FindAllSubmatch(stream, -1) {
		if len(m[1]) > 0 {
			row, _ = strconv.Atoi(string(m[1]))
			col, _ = strconv.Atoi(string(m[2]))
		}
		keys := map[string]string{}
		for kv := range strings.SplitSeq(string(m[3]), ",") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				keys[k] = v
			}
		}
		switch keys["a"] {
		case "p":
			r := hostRect{col: col - 1, row: row - 1, cols: 1, rows: 1}
			if n, err := strconv.Atoi(keys["c"]); err == nil && n > 0 {
				r.cols = n
			}
			if n, err := strconv.Atoi(keys["r"]); err == nil && n > 0 {
				r.rows = n
			}
			live[key{keys["i"], keys["p"]}] = r
		case "d":
			switch keys["d"] {
			case "a", "A", "":
				clear(live)
			case "i", "I":
				for k := range live {
					if k.image == keys["i"] && (keys["p"] == "" || k.placement == keys["p"]) {
						delete(live, k)
					}
				}
			}
		}
	}
	out := make([]hostRect, 0, len(live))
	for _, r := range live {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b hostRect) int {
		if a.row != b.row {
			return a.row - b.row
		}
		return a.col - b.col
	})
	return out
}

// paintedRects is paintedFrame for every rectangle the host is drawing.
func paintedRects(s tuitest.Screen, rects []hostRect) string {
	lines := strings.Split(s.Text(), "\n")
	for _, r := range rects {
		for y := r.row; y < r.row+r.rows && y < len(lines); y++ {
			runes := []rune(lines[y])
			for len(runes) < r.col+r.cols {
				runes = append(runes, ' ')
			}
			for x := r.col; x < r.col+r.cols; x++ {
				runes[x] = '▓'
			}
			lines[y] = string(runes)
		}
	}
	return strings.Join(lines, "\n")
}

// TestKittyImageStaysUnderPopovers opens things over a pane that shows a kitty
// image and checks that the host is never told to draw the image on a cell
// they cover. The host paints an image over the finished frame, so an image
// that is not cut around a popover is drawn on top of it, whatever tuios
// composed there.
//
// Two kinds of cover, because they reach the passthrough by two roads. A
// popup is a window, ranked above the tiles by the floating band rather than
// by its own z. The rename dialog is not a window at all: it is a layer of
// chrome. Each must leave the clear part of the image showing, and closing it
// must give the whole image back.
//
// How this could pass wrongly, written down first:
//   - the image might never be placed: the test waits for a placement first;
//   - the image might be hidden whole: the test needs part of it still drawn;
//   - the cover might not be on the image: the test needs a covered cell that
//     the image drew on before the cover opened.
//
// Negative controls: on a build without the fix both cases fail with the
// image drawn over the cover. With only WindowZ set from the raw Z again, the
// popup case fails. See NEGATIVE_CONTROLS.md.
func TestKittyImageStaysUnderPopovers(t *testing.T) {
	for _, cover := range []string{"popup", "rename"} {
		t.Run(cover, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, "[appearance]\nsidebar_enabled = false\n\n[startup]\nopen_default_window = true\ntiled = true\n")
			host := newKittyHost()
			term := startIn(t, base, startOpts{
				cols: 120, rows: 40,
				args: []string{"new", "kio"},
				env:  []string{"TUIOS_SIXEL_GRAPHICS=0"},
				out:  host,
			})
			host.answerProbe(t, term)
			waitWindowCount(t, term, 1, "the first pane")
			art := artifactDir(t)

			enterTerminalMode(t, term)
			if err := term.SendKeys("\x15"); err != nil {
				t.Fatalf("clear prompt line: %v", err)
			}
			time.Sleep(insertGuard)
			rows, cols := reportedPaneSize(t, term, "A")
			frame := kittyFrameFile(t, t.TempDir(), cols*cellW, rows*cellH)
			typeLine(t, term, "clear; cat "+frame)
			var before []hostRect
			deadline := time.Now().Add(uiTimeout)
			for len(before) == 0 && time.Now().Before(deadline) {
				time.Sleep(200 * time.Millisecond)
				before = hostImageRects(host.bytes())
			}
			if len(before) == 0 {
				t.Fatalf("the image was never placed on the host\n%s", term.Snapshot())
			}
			leaveTerminalMode(t, term)
			time.Sleep(time.Second)
			before = hostImageRects(host.bytes())
			plain := term.Screen()
			beforeText := strings.Split(plain.Text(), "\n")
			_ = os.WriteFile(filepath.Join(art, "1-before.txt"), []byte(paintedRects(plain, before)), 0o644)

			var popupBox hostRect
			var popupID string
			switch cover {
			case "popup":
				res := openPopup(t, base, "--width", "40", "--height", "10",
					"--", "sh", "-c", "printf 'POPUP-IS-HERE\\n'; sleep 120")
				if err := term.WaitForText("POPUP-IS-HERE", uiTimeout); err != nil {
					t.Fatalf("the popup never printed: %v\n%s", err, term.Snapshot())
				}
				popupID = res.WindowID
				r := rectFor(t, waitForSettledGeometryIn(t, base, "kio", 2), popupID)
				popupBox = hostRect{r.X, r.Y, r.Width, r.Height}
			case "rename":
				if err := term.SendKeys("r"); err != nil {
					t.Fatalf("open the rename dialog: %v", err)
				}
				if err := term.WaitForText("rename", uiTimeout); err != nil {
					t.Fatalf("the rename dialog never opened: %v\n%s", err, term.Snapshot())
				}
			}
			time.Sleep(1500 * time.Millisecond)
			during := hostImageRects(host.bytes())
			s := term.Screen()
			_ = os.WriteFile(filepath.Join(art, "2-covered.txt"), []byte(paintedRects(s, during)), 0o644)
			t.Logf("before:\n%s\n\nwith the %s open (image cells drawn as ▓):\n%s",
				paintedRects(plain, before), cover, paintedRects(s, during))

			// The covered cells: the popup's box when the daemon can say where
			// it is, and every cell whose text the cover changed besides.
			nowText := strings.Split(s.Text(), "\n")
			covered := 0
			for y := range min(len(nowText), len(beforeText)) {
				a, b := []rune(beforeText[y]), []rune(nowText[y])
				for x := range max(len(a), len(b)) {
					inBox := popupBox.cols > 0 && popupBox.has(x, y)
					changed := x >= len(a) || x >= len(b) || a[x] != b[x]
					if !inBox && !changed {
						continue
					}
					wasImage := slices.ContainsFunc(before, func(r hostRect) bool { return r.has(x, y) })
					if !wasImage {
						continue
					}
					covered++
					for _, r := range during {
						if r.has(x, y) {
							t.Fatalf("the image is drawn over the %s at column %d row %d (placement %+v)",
								cover, x, y, r)
						}
					}
				}
			}
			if covered == 0 {
				t.Fatalf("the %s covers no cell of the image; nothing was tested", cover)
			}
			if len(during) == 0 {
				t.Errorf("the image was hidden whole; the part the %s leaves clear must still show", cover)
			}

			// Closing the cover gives the whole image back.
			if popupID != "" {
				if out, err := tuiosCLI(t, base, "close-window", "-s", "kio", popupID); err != nil {
					t.Fatalf("close the popup: %v\n%s", err, out)
				}
				waitWindowCount(t, term, 1, "the popup closed")
			} else if err := term.SendKeys(tuitest.Esc); err != nil {
				t.Fatalf("close the %s: %v", cover, err)
			}
			time.Sleep(1500 * time.Millisecond)
			after := hostImageRects(host.bytes())
			_ = os.WriteFile(filepath.Join(art, "3-after.txt"), []byte(paintedRects(term.Screen(), after)), 0o644)
			if !slices.Equal(after, before) {
				t.Errorf("after the %s closed the host draws %v, want the image back as %v\n%s",
					cover, after, before, paintedRects(term.Screen(), after))
			}
		})
	}
}
