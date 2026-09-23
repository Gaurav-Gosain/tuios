package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The v0.8.0 looks, driven for real: a fresh install with no config file,
// standalone and daemon-backed, from a wide screen down to one too narrow for
// the rail. The rest of the suite pins the looks from before v0.8.0.

// shippedRailWidth and the two narrower variants are config's
// SidebarDefaultWidth, SidebarNarrowWidth and SidebarGlyphWidth, and the
// breakpoints are config's SidebarBreakpoint*. Spelled out because this
// module does not import the one under test.
const (
	shippedRailWidth = 24
	narrowRailWidth  = 16
	glyphRailWidth   = 3
	paneFloor        = 30
)

// shippedRail is the rail width tuios reserves at a render width.
func shippedRail(cols int) int {
	switch {
	case cols < 40:
		return 0
	case cols < 60:
		return glyphRailWidth
	case cols < 90:
		return narrowRailWidth
	default:
		return shippedRailWidth
	}
}

// startShipped starts tuios with no config file and the shipped looks, and
// waits for it to be up in window-management mode.
func startShipped(t *testing.T, cols, rows int, daemon bool) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	useShippedLooks(base)
	term := startIn(t, base, startOpts{cols: cols, rows: rows, daemonDefault: daemon, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) >= 0
	}, bootTimeout); err != nil {
		t.Fatalf("tuios never drew its dock: %v\n%s", err, term.Snapshot())
	}
	return term, base
}

// dockOnTop reports whether the dock's window count is in the top rows and
// not in the bottom ones.
func dockOnTop(s tuitest.Screen) bool {
	_, rows := s.Size()
	for r := rows - 1; r >= max(0, rows-2); r-- {
		if dockStatus.MatchString(s.Line(r)) {
			return false
		}
	}
	for r := 0; r < min(2, rows); r++ {
		if dockStatus.MatchString(s.Line(r)) {
			return true
		}
	}
	return false
}

// TestShippedLooks opens two tiled panes on a fresh install at each size and
// checks the v0.8.0 looks: the dock on top, the rail on the right at the
// width the breakpoints give, the panes never squeezed under the pane floor,
// and, where the panes are wide enough for one, the title on the top border.
func TestShippedLooks(t *testing.T) {
	// tuios refuses to start below 40 columns, so the last size is reached by
	// shrinking a running client, which is how a screen that small happens.
	sizes := []struct{ cols, rows, startCols int }{
		{120, 40, 0},
		{90, 30, 0},
		{80, 24, 0},
		{79, 24, 0},
		{60, 24, 0},
		{45, 20, 0},
		{38, 20, 45},
	}
	for _, daemon := range []bool{false, true} {
		mode := "standalone"
		if daemon {
			mode = "daemon"
		}
		for _, sz := range sizes {
			t.Run(fmt.Sprintf("%s/%dx%d", mode, sz.cols, sz.rows), func(t *testing.T) {
				startCols := sz.cols
				if sz.startCols > 0 {
					startCols = sz.startCols
				}
				term, base := startShipped(t, startCols, sz.rows, daemon)
				newWindow(t, term)
				renameWindow(t, term, "LOOKS")
				newWindow(t, term)
				waitWindowCount(t, term, 2, "after two 'n' presses")
				if startCols != sz.cols {
					if err := term.Resize(sz.cols, sz.rows); err != nil {
						t.Fatalf("resize to %dx%d: %v", sz.cols, sz.rows, err)
					}
					if err := term.WaitFor(func(s tuitest.Screen) bool {
						return railHeaderColumn(s) < 0 && countWindows(s) == 2
					}, uiTimeout); err != nil {
						t.Fatalf("the rail stayed up after the screen shrank to %d columns: %v\n%s", sz.cols, err, term.Snapshot())
					}
				}
				time.Sleep(300 * time.Millisecond)
				s := term.Screen()
				t.Logf("frame:\n%s", term.Snapshot())

				if !dockOnTop(s) {
					t.Errorf("the dock is not on top\n%s", term.Snapshot())
				}

				rail := shippedRail(sz.cols)
				if rail >= narrowRailWidth {
					col := railHeaderColumn(s)
					if col < 0 {
						t.Errorf("no rail header on screen\n%s", term.Snapshot())
					} else if col < sz.cols-rail {
						t.Errorf("the rail header is at column %d, left of the rail's band [%d,%d)", col, sz.cols-rail, sz.cols)
					}
				}
				if rail == 0 {
					// With no rail the panes run to the last column, so the
					// top border ends on a corner rather than on the rail's
					// separator.
					top := strings.TrimRight(s.Line(2), " ")
					if !strings.HasSuffix(top, "╮") && !strings.HasSuffix(top, "┐") {
						t.Errorf("a %d-column screen still reserves the rail: %q\n%s", sz.cols, top, term.Snapshot())
					}
				}

				// The title sits on the top border: the row carrying the
				// name also carries a top corner.
				if row := rowWith(s, "LOOKS"); row >= 0 {
					if line := s.Line(row); !strings.ContainsAny(line, "╭┌") {
						t.Errorf("the title is not on a top border: %q", line)
					}
				}

				// The daemon's list-windows keeps the box a pane was tiled into
				// before a client resize, rail or no rail, so after the resize
				// the screen checks above are the whole story.
				if daemon && startCols == sz.cols {
					right := sz.cols - rail
					rects := waitForSettledGeometry(t, base, 2)
					for _, r := range rects {
						t.Logf("window %s: (%d,%d) %dx%d", r.ID, r.X, r.Y, r.Width, r.Height)
						if r.X < 0 || r.X+r.Width > right {
							t.Errorf("pane %s spans [%d,%d), past the rail at %d", r.ID, r.X, r.X+r.Width, right)
						}
						if r.Y < 2 {
							t.Errorf("pane %s starts on row %d, under the dock", r.ID, r.Y)
						}
					}
					if rail > 0 && right < paneFloor {
						t.Errorf("the rail leaves the panes %d columns, under the floor %d", right, paneFloor)
					}
				}
			})
		}
	}
}

// TestShippedClickToTypeIsDouble checks the v0.8.0 click policy on a fresh
// install: one click on a pane's content leaves the keyboard with the window
// manager, and a double click starts typing in the pane.
func TestShippedClickToTypeIsDouble(t *testing.T) {
	term, _ := startShipped(t, 120, 40, false)
	// newWindow's 'n' is a window-management key, so the client is in that
	// mode once it returns.
	newWindow(t, term)
	time.Sleep(600 * time.Millisecond)
	if strings.Contains(term.Screen().Text(), "Terminal mode") {
		t.Fatalf("setup: the new window started in terminal mode\n%s", term.Snapshot())
	}

	// The middle of the pane area: left of the rail, below the dock.
	col, row := (120-shippedRailWidth)/2, 20
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	time.Sleep(600 * time.Millisecond)
	if strings.Contains(term.Screen().Text(), "Terminal mode") {
		t.Fatalf("a single click started typing in the pane\n%s", term.Snapshot())
	}

	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Terminal mode", uiTimeout); err != nil {
		t.Fatalf("a double click did not start typing in the pane: %v\n%s", err, term.Snapshot())
	}
}

// railHeaderColumn is the column the rail's sessions header starts at, or -1.
func railHeaderColumn(s tuitest.Screen) int {
	_, rows := s.Size()
	for r := range rows {
		line := []rune(s.Line(r))
		idx := strings.Index(string(line), sidebarHeader)
		if idx >= 0 {
			return len([]rune(string(line)[:idx]))
		}
	}
	return -1
}

// rowWith is the first row holding needle, or -1.
func rowWith(s tuitest.Screen, needle string) int {
	_, rows := s.Size()
	for r := range rows {
		if strings.Contains(s.Line(r), needle) {
			return r
		}
	}
	return -1
}
