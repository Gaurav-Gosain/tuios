package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	scratchZoomCols = 140
	scratchZoomRows = 44
)

var scratchFills = []struct {
	char   string
	escape string
}{
	{"@", `\100`},
	{"#", `\043`},
	{"&", `\046`},
}

func fillScratchPane(t *testing.T, term *tuitest.Terminal, escape string) {
	t.Helper()
	cmd := fmt.Sprintf(`clear; head -c $(( $(tput cols) * ($(tput lines) - 1) )) /dev/zero | tr '\0' '%s'`, escape)
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("type the fill: %v", err)
	}
}

func waitFillShown(t *testing.T, term *tuitest.Terminal, char string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), char)
	}, uiTimeout); err != nil {
		t.Fatalf("the fill %q never showed: %v\n%s", char, err, term.Snapshot())
	}
}

type scratchBoxRect struct{ x0, y0, x1, y1 int }

func (b scratchBoxRect) inside(x, y int) bool {
	return x >= b.x0 && x <= b.x1 && y >= b.y0 && y <= b.y1
}

func findScratchBox(s tuitest.Screen) (scratchBoxRect, bool) {
	cols, rows := s.Size()
	for y := range rows {
		for x := range cols - 11 {
			var b strings.Builder
			for k := range 11 {
				b.WriteString(s.Cell(x+k, y).Content)
			}
			if b.String() != "╭─ scratch " {
				continue
			}
			box := scratchBoxRect{x0: x, y0: y, x1: -1, y1: -1}
			for xx := x + 11; xx < cols; xx++ {
				if s.Cell(xx, y).Content == "╮" {
					box.x1 = xx
					break
				}
			}
			for yy := y + 1; yy < rows; yy++ {
				if s.Cell(x, yy).Content == "╰" {
					box.y1 = yy
					break
				}
			}
			return box, box.x1 > 0 && box.y1 > 0
		}
	}
	return scratchBoxRect{}, false
}

type fillBounds struct {
	n              int
	x0, y0, x1, y1 int
	outside        int
	firstOutX      int
	firstOutY      int
}

func measureFills(s tuitest.Screen, box scratchBoxRect) map[string]*fillBounds {
	cols, _ := s.Size()
	top := dockTop(s)
	out := map[string]*fillBounds{}
	for _, f := range scratchFills {
		out[f.char] = &fillBounds{x0: cols, y0: top, x1: -1, y1: -1, firstOutX: -1, firstOutY: -1}
	}
	for y := range top {
		for x := range cols {
			fb, ok := out[s.Cell(x, y).Content]
			if !ok {
				continue
			}
			fb.n++
			fb.x0, fb.y0 = min(fb.x0, x), min(fb.y0, y)
			fb.x1, fb.y1 = max(fb.x1, x), max(fb.y1, y)
			if !box.inside(x, y) {
				if fb.outside == 0 {
					fb.firstOutX, fb.firstOutY = x, y
				}
				fb.outside++
			}
		}
	}
	return out
}

func framePerimeterIntact(s tuitest.Screen, box scratchBoxRect) (x, y int, got string, ok bool) {
	frame := "╭╮╰╯─│"
	for xx := box.x0; xx <= box.x1; xx++ {
		if c := s.Cell(xx, box.y1).Content; !strings.Contains(frame, c) || c == "" {
			return xx, box.y1, c, false
		}
	}
	for yy := box.y0; yy <= box.y1; yy++ {
		for _, xx := range []int{box.x0, box.x1} {
			if c := s.Cell(xx, yy).Content; !strings.Contains(frame, c) || c == "" {
				return xx, yy, c, false
			}
		}
	}
	if s.Cell(box.x0+1, box.y0).Content != "─" {
		return box.x0 + 1, box.y0, s.Cell(box.x0+1, box.y0).Content, false
	}
	return 0, 0, "", true
}

func waitPanes(t *testing.T, term *tuitest.Terminal, n int, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Count(text, "● ● ●") == n
	}, uiTimeout); err != nil {
		t.Fatalf("%s: never %d panes: %v\n%s", what, n, err, term.Snapshot())
	}
}

func cellsOutside(s tuitest.Screen, box scratchBoxRect) map[[2]int]tuitest.Cell {
	cols, _ := s.Size()
	out := map[[2]int]tuitest.Cell{}
	for y := range dockTop(s) {
		for x := range cols {
			if !box.inside(x, y) {
				out[[2]int{x, y}] = s.Cell(x, y)
			}
		}
	}
	return out
}

func sameCell(a, b tuitest.Cell) bool {
	return a.Content == b.Content && a.Fg == b.Fg && a.Bg == b.Bg && a.Reverse == b.Reverse
}

func borderRun(s tuitest.Screen, x0, y0, x1, y1 int) string {
	first := s.Cell(x0, y0)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			c := s.Cell(x, y)
			if c.Content == "" || !strings.Contains("╭╮╰╯─│┌┐└┘━┃", c.Content) {
				return fmt.Sprintf("(%d,%d) is %q, not a border glyph", x, y, c.Content)
			}
			if c.Fg != first.Fg {
				return fmt.Sprintf("(%d,%d) is drawn in %v, the run starts in %v", x, y, c.Fg, first.Fg)
			}
		}
	}
	return ""
}

func checkZoomInBox(t *testing.T, term *tuitest.Terminal, box scratchBoxRect, zoomed, who string, before map[[2]int]tuitest.Cell) {
	t.Helper()
	s := term.Screen()
	if strings.Contains(s.Text(), "Spotlight") {
		t.Fatalf("%s: the spotlight is on, and it shades the frame under test\n%s", who, term.Snapshot())
	}
	if before != nil {
		changed := 0
		var first [2]int
		for k, now := range cellsOutside(s, box) {
			if was, ok := before[k]; ok && !sameCell(was, now) {
				if changed == 0 || k[1] < first[1] || (k[1] == first[1] && k[0] < first[0]) {
					first = k
				}
				changed++
			}
		}
		if changed > 0 {
			t.Errorf("%s: %d cells outside the scratch box changed with the zoom, the first at (%d,%d): %q was %q\n%s",
				who, changed, first[0], first[1], s.Cell(first[0], first[1]).Content, before[first].Content, term.Snapshot())
		}
	}
	if why := borderRun(s, box.x0, box.y1, box.x1, box.y1); why != "" {
		t.Errorf("%s: the scratch frame's bottom edge is broken: %s\n%s", who, why, term.Snapshot())
	}
	fills := measureFills(s, box)
	for _, f := range scratchFills {
		if fb := fills[f.char]; fb.outside > 0 {
			t.Errorf("%s: %d cells of pane %q are outside the scratch box %+v, the first at (%d,%d)\n%s",
				who, fb.outside, f.char, box, fb.firstOutX, fb.firstOutY, term.Snapshot())
		}
	}
	if x, y, got, ok := framePerimeterIntact(s, box); !ok {
		t.Errorf("%s: the scratch frame is drawn over at (%d,%d): %q\n%s", who, x, y, got, term.Snapshot())
	}
	innerW, innerH := box.x1-box.x0-1, box.y1-box.y0-1
	z := fills[zoomed]
	if z.n == 0 {
		t.Fatalf("%s: the zoomed pane's fill %q is not on the screen\n%s", who, zoomed, term.Snapshot())
	}
	if w, h := z.x1-z.x0+1, z.y1-z.y0+2; w*100 < innerW*75 || h*100 < innerH*75 {
		t.Errorf("%s: the zoomed pane's fill is %dx%d, want at least 75%% of the box's inside %dx%d\n%s",
			who, w, h, innerW, innerH, term.Snapshot())
	}
	left, right, bottom := z.x0-1, z.x1+1, z.y1+2
	for _, side := range []struct {
		name           string
		x0, y0, x1, y1 int
	}{
		{"bottom", left, bottom, right, bottom},
		{"left", left, z.y0, left, bottom},
		{"right", right, z.y0, right, bottom},
	} {
		if why := borderRun(s, side.x0, side.y0, side.x1, side.y1); why != "" {
			t.Errorf("%s: the zoomed pane's %s border is broken: %s\n%s", who, side.name, why, term.Snapshot())
		}
	}
	peek := 0
	for _, f := range scratchFills {
		if f.char != zoomed {
			peek += fills[f.char].n
		}
	}
	if peek == 0 {
		t.Errorf("%s: no pane shows beside the zoomed one at zoom_size 90\n%s", who, term.Snapshot())
	}
}

func TestScratchZoomStaysInTheBox(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		name := "standalone"
		if daemon {
			name = "daemon"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, "[startup]\ntiled = true\n[appearance]\nzoom_size = 90\nzoom_animation = false\n")
			o := startOpts{cols: scratchZoomCols, rows: scratchZoomRows}
			var term *tuitest.Terminal
			if daemon {
				o.args = []string{"new", "work"}
			}
			term = startIn(t, base, o)
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			typeUntil(t, term, "echo BASE-$((5*5))", "BASE-25")

			dir := artifactDir(t)
			if root := os.Getenv("TUIOS_E2E_PROOF"); root != "" {
				dir = filepath.Join(root, name)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("make the proof directory: %v", err)
				}
			}
			host := hostPalette(t, "")
			save := func(term *tuitest.Terminal, frame string) {
				if err := term.WaitStable(uiTimeout); err != nil {
					t.Fatalf("%s: the frame never settled: %v", frame, err)
				}
				saveArtifact(t, term, dir, frame)
				savePNG(t, term.Screen(), host, dir, frame)
			}

			toggleScratch(t, term)
			allOnScreen(t, term, "the group shown", "─ scratch ")
			waitPanes(t, term, 2, "the group shown")
			typeUntil(t, term, "echo AAA-$((1+1))", "AAA-2")
			fillScratchPane(t, term, scratchFills[0].escape)
			waitFillShown(t, term, scratchFills[0].char)
			prefix(t, term, "|")
			waitPanes(t, term, 3, "the first split")
			typeUntil(t, term, "echo BBB-$((2+2))", "BBB-4")
			fillScratchPane(t, term, scratchFills[1].escape)
			waitFillShown(t, term, scratchFills[1].char)
			prefix(t, term, "-")
			waitPanes(t, term, 4, "the second split")
			typeUntil(t, term, "echo CCC-$((3+3))", "CCC-6")
			fillScratchPane(t, term, scratchFills[2].escape)
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				text := s.Text()
				return strings.Count(text, "@") > 50 && strings.Count(text, "#") > 50 && strings.Count(text, "&") > 50
			}, uiTimeout); err != nil {
				t.Fatalf("the three panes never filled: %v\n%s", err, term.Snapshot())
			}
			save(term, "tiled")

			s := term.Screen()
			box, ok := findScratchBox(s)
			if !ok {
				t.Fatalf("the scratch frame is not on the screen\n%s", term.Snapshot())
			}
			tiled := measureFills(s, box)
			for _, f := range scratchFills {
				if fb := tiled[f.char]; fb.outside > 0 || fb.n == 0 {
					t.Fatalf("before the zoom, pane %q has %d cells, %d outside the box %+v\n%s",
						f.char, fb.n, fb.outside, box, term.Snapshot())
				}
			}
			tiledW := tiled["&"].x1 - tiled["&"].x0 + 1
			outsideBefore := cellsOutside(s, box)

			windowManagementMode(t, term)
			if err := term.SendKeys("z"); err != nil {
				t.Fatalf("send z: %v", err)
			}
			enterTerminalMode(t, term)
			fillScratchPane(t, term, scratchFills[2].escape)
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				fb := measureFills(s, box)["&"]
				return fb.x1-fb.x0+1 > tiledW+10
			}, uiTimeout); err != nil {
				t.Fatalf("the zoom never came on: %v\n%s", err, term.Snapshot())
			}
			save(term, "zoomed")
			checkZoomInBox(t, term, box, "&", "the zooming client", outsideBefore)

			if daemon {
				second := attachIn(t, base, "work", startOpts{cols: scratchZoomCols, rows: scratchZoomRows})
				allOnScreen(t, second, "the group on the second client", "─ scratch ")
				save(second, "zoomed-second-client")
				box2, ok := findScratchBox(second.Screen())
				if !ok {
					t.Fatalf("the second client shows no scratch frame\n%s", second.Snapshot())
				}
				checkZoomInBox(t, second, box2, "&", "the second client", nil)
			}

			windowManagementMode(t, term)
			if err := term.SendKeys("z"); err != nil {
				t.Fatalf("send z: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				fb := measureFills(s, box)["&"]
				return fb.n > 0 && fb.x1-fb.x0+1 <= tiledW+1
			}, uiTimeout); err != nil {
				t.Fatalf("the zoom never went off: %v\n%s", err, term.Snapshot())
			}
			save(term, "unzoomed")
			after := measureFills(term.Screen(), box)
			for _, f := range scratchFills {
				if fb := after[f.char]; fb.outside > 0 || fb.n == 0 {
					t.Errorf("after the zoom out, pane %q has %d cells, %d outside the box\n%s",
						f.char, fb.n, fb.outside, term.Snapshot())
				}
			}
			if x, y, got, ok := framePerimeterIntact(term.Screen(), box); !ok {
				t.Errorf("after the zoom out, the frame is drawn over at (%d,%d): %q\n%s", x, y, got, term.Snapshot())
			}
			alive(t, term, "after the scratch zoom")
		})
	}
}
