package app

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The three layout modes are held to one contract, and until this file existed
// only one of them was checked against it. The shared-border cases covered BSP
// and master-stack at a single size with the gap at zero, which is the one cell
// of the matrix where all three happened to agree.
//
// The contract: every pane's rectangle is inside the region, no two of them
// overlap, and the cells between two neighbours are exactly the ones the
// settings asked for - the divider's column when the panes gave up their
// borders, appearance.gap of empty ground when they did not.

// modeOS builds a tiled session of n panes at a given size, under the given
// pane-geometry settings, arranged by the named mode.
//
// The BSP tree is built first whatever the mode, because that is what happens
// in the app: BSP is the default, so a session that ends up in another mode has
// been through it, and the tree it left behind outlives the switch.
func modeOS(t *testing.T, mode string, shared bool, gap, n, w, h int) *OS {
	t.Helper()
	prevAnim, prevShared, prevGap := config.AnimationsEnabled, config.SharedBorders, config.PaneGap
	config.AnimationsEnabled, config.SharedBorders, config.PaneGap = false, shared, gap
	t.Cleanup(func() {
		config.AnimationsEnabled, config.SharedBorders, config.PaneGap = prevAnim, prevShared, prevGap
	})

	m := &OS{
		SharedBorders:        shared,
		PaneGap:              gap,
		ScrollColumnWidth:    config.ScrollColumnWidthDefault,
		Windows:              make([]*terminal.Window, 0, n),
		FocusedWindow:        0,
		WorkspaceFocus:       map[int]int{},
		WorkspaceTrees:       map[int]*layout.BSPTree{},
		NumWorkspaces:        9,
		CurrentWorkspace:     1,
		Width:                w,
		Height:               h,
		AutoTiling:           true,
		MasterRatio:          0.5,
		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceMasterRatio: map[int]float64{},
		PendingResizes:       map[string][2]int{},
	}
	for i := range n {
		win := newTestWindow(t, fmt.Sprintf("mode-%s-%d-%dx%d-%d", mode, n, w, h, i), 40, 20)
		win.Workspace = 1
		m.Windows = append(m.Windows, win)
	}
	m.UseBSPLayout = true
	m.TileAllWindows()
	m.ApplyLayoutModeName(mode)
	m.TileAllWindows()
	// The strip always animates, whatever the animation setting, so its panes
	// are still travelling when TileAllWindows returns. Geometry is what these
	// cases are about, so the transitions are finished rather than watched.
	m.CompleteAllAnimations()
	return m
}

// paneAt returns the index of the pane covering a cell, or -1.
func paneAt(m *OS, x, y int) int {
	for i, w := range m.Windows {
		if x >= w.X && x < w.X+w.Width && y >= w.Y && y < w.Y+w.Height {
			return i
		}
	}
	return -1
}

// wantGap is the cells a layout keeps between two neighbours.
//
// Deliberately not m.separatorGap(): that is the function under test, and an
// expectation computed with it would pass whatever it returned. This is the
// rule stated from the settings instead. Borderless panes need a column for the
// divider even when the user asked for no gap, since their rectangles are guest
// output edge to edge and the line has nowhere else to go. Panes with their own
// borders need only what the user asked for, because their two adjacent border
// columns already divide them.
func wantGap(mode string, shared bool, gap int) int {
	if shared && mode != LayoutModeScrolling {
		return max(gap, 1)
	}
	return gap
}

// checkLayoutContract asserts the whole contract on one arrangement: every pane
// inside the region, no two overlapping, and exactly the asked-for cells
// between neighbours.
//
// Neighbours are found by scanning cell by cell out from each pane's edge
// rather than by comparing rectangles, because two panes separated by a third
// are not neighbours and a rectangle comparison cannot tell the difference.
func checkLayoutContract(t *testing.T, m *OS, label, mode string, shared bool, gap int) {
	t.Helper()
	want := wantGap(mode, shared, gap)
	bounds := m.GetBSPBounds()

	for i, w := range m.Windows {
		if w.Width <= 0 || w.Height <= 0 {
			t.Errorf("%s: pane %d is %dx%d", label, i, w.Width, w.Height)
		}
		// The strip is wider than the viewport by design, so only its vertical
		// extent is bounded by the region.
		if mode != LayoutModeScrolling {
			if w.X < bounds.X || w.X+w.Width > bounds.X+bounds.W {
				t.Errorf("%s: pane %d spans columns %d..%d, outside the region %d..%d",
					label, i, w.X, w.X+w.Width, bounds.X, bounds.X+bounds.W)
			}
		}
		if w.Y < bounds.Y || w.Y+w.Height > bounds.Y+bounds.H {
			t.Errorf("%s: pane %d spans rows %d..%d, outside the region %d..%d",
				label, i, w.Y, w.Y+w.Height, bounds.Y, bounds.Y+bounds.H)
		}
	}

	for a := 0; a < len(m.Windows); a++ {
		for b := a + 1; b < len(m.Windows); b++ {
			wa, wb := m.Windows[a], m.Windows[b]
			if wa.X < wb.X+wb.Width && wb.X < wa.X+wa.Width &&
				wa.Y < wb.Y+wb.Height && wb.Y < wa.Y+wa.Height {
				t.Errorf("%s: panes %d (%d,%d %dx%d) and %d (%d,%d %dx%d) overlap",
					label, a, wa.X, wa.Y, wa.Width, wa.Height, b, wb.X, wb.Y, wb.Width, wb.Height)
			}
		}
	}

	for i, a := range m.Windows {
		for y := a.Y; y < a.Y+a.Height; y++ {
			for x := a.X + a.Width; x < bounds.X+bounds.W; x++ {
				if j := paneAt(m, x, y); j >= 0 {
					if d := x - (a.X + a.Width); d != want {
						t.Errorf("%s: %d cells between pane %d and pane %d on row %d, want %d",
							label, d, i, j, y, want)
					}
					break
				}
			}
		}
		for x := a.X; x < a.X+a.Width; x++ {
			for y := a.Y + a.Height; y < bounds.Y+bounds.H; y++ {
				if j := paneAt(m, x, y); j >= 0 {
					if d := y - (a.Y + a.Height); d != want {
						t.Errorf("%s: %d cells between pane %d and pane %d on column %d, want %d",
							label, d, i, j, x, want)
					}
					break
				}
			}
		}
	}
}

// contractSizes are the shapes the contract is checked at. The last three are
// where the arithmetic gets tight: a tall terminal that reads as landscape by
// the numbers, an odd width that will not divide, and a screen small enough
// that a pane is near its floor.
var contractSizes = []struct{ w, h int }{{160, 48}, {120, 40}, {80, 24}, {51, 37}, {61, 20}, {45, 14}}

// contractCounts is the pane counts each mode is held to the contract at.
//
// The two flat tilers are held to it at every count on every screen in the
// list: they divide the region once, so the arithmetic is the same shape at
// nine panes as at two.
//
// BSP is held to it up to six panes everywhere. Past that its own insertion
// scheme is the limit, not its arithmetic: a fresh tree splits the pane it just
// inserted, so the nth pane is a 2^(n-1)th of the screen and the ninth is one
// 256th of it - a single row on any terminal a person owns. Six is where a
// split is still a split. TestBSPExhaustsTheRegionByHalvingIt states that limit
// on its own so it cannot drift quietly.
func contractCounts(mode string) []int {
	if mode == LayoutModeBSP {
		return []int{1, 2, 3, 4, 5, 6}
	}
	return []int{1, 2, 3, 4, 5, 6, 7, 9}
}

// TestLayoutModesKeepTheirContract walks all three modes against both
// shared-border settings, three gaps, several pane counts and six screen shapes.
//
// The failures it was written against, each of which it catches with the fix
// removed:
//
//   - master-stack grew every pane to a fixed twenty by five minimum and then
//     shoved it back inside the screen, so seven panes on a 51x37 terminal were
//     drawn on top of each other. That is a comfortable half of a laptop screen,
//     not a corner case.
//   - master-stack took the whole gap out of the far side of each split, so at
//     appearance.gap 2 the far pane was two cells smaller than the near one at
//     every pane count and on every axis.
//   - the scrolling strip ignored appearance.gap entirely: its columns butted
//     against each other however wide the gap was set.
func TestLayoutModesKeepTheirContract(t *testing.T) {
	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack, LayoutModeScrolling} {
		for _, shared := range []bool{false, true} {
			for _, gap := range []int{0, 1, 2} {
				for _, n := range contractCounts(mode) {
					for _, sz := range contractSizes {
						label := fmt.Sprintf("%s/shared=%v/gap=%d/n=%d/%dx%d", mode, shared, gap, n, sz.w, sz.h)
						t.Run(label, func(t *testing.T) {
							m := modeOS(t, mode, shared, gap, n, sz.w, sz.h)
							checkLayoutContract(t, m, label, mode, shared, gap)
						})
					}
				}
			}
		}
	}
}

// TestBSPExhaustsTheRegionByHalvingIt states where BSP stops being able to keep
// the contract, and why, so the exclusion in contractCounts is a recorded limit
// rather than a quiet hole.
//
// Building a fresh tree inserts each pane against the one before it, so every
// pane halves the last one's region. Six panes still leave every pane several
// rows; nine leave the last one a single row on a 120x40 screen, and a region
// one row tall cannot hold two panes and a divider however the arithmetic is
// written. Improving the insertion scheme - inserting into the largest region
// rather than the newest - would fail this test, which is the point: it would
// mean the exclusion above can be lifted.
func TestBSPExhaustsTheRegionByHalvingIt(t *testing.T) {
	roomy := modeOS(t, LayoutModeBSP, true, 0, 6, 120, 40)
	for i, w := range roomy.Windows {
		if w.Height < 4 || w.Width < 8 {
			t.Errorf("six panes on 120x40: pane %d is %dx%d, too small for a split that should still be comfortable",
				i, w.Width, w.Height)
		}
	}

	cramped := modeOS(t, LayoutModeBSP, true, 0, 9, 120, 40)
	smallest := cramped.Windows[0]
	for _, w := range cramped.Windows {
		if w.Width*w.Height < smallest.Width*smallest.Height {
			smallest = w
		}
	}
	if smallest.Height > 2 {
		t.Errorf("nine panes on 120x40 leave the smallest at %dx%d; the halving limit this records has moved, "+
			"so contractCounts can take BSP further", smallest.Width, smallest.Height)
	}
}

// fillGuest paints a pane's whole drawable width with one character of its own,
// so the composed frame says exactly which cells that guest owns.
func fillGuest(w *terminal.Window, mark rune) {
	w.LockIO()
	_, _ = w.Terminal.Write([]byte(strings.Repeat(string(mark), w.ContentWidth())))
	w.UnlockIO()
	w.MarkContentDirty()
}

// frameRows renders the composed frame and returns it as rows of runes with the
// styling stripped, which is the frame as a person sees it.
func frameRows(m *OS) [][]rune {
	lines := strings.Split(lipgloss.Sprint(m.GetCanvas(true).Render()), "\n")
	rows := make([][]rune, len(lines))
	for i, line := range lines {
		rows[i] = []rune(ansi.Strip(line))
	}
	return rows
}

// isBoxDrawing reports whether a rune is one of the glyphs a border or a
// divider is drawn with.
func isBoxDrawing(r rune) bool {
	return (r >= 0x2500 && r <= 0x257F) || r == '|' || r == '-'
}

// TestGapBetweenBorderedPanesIsEmptyGround is the regression for a divider
// drawn down the middle of a gap between two panes that both still have their
// own borders.
//
// appearance.gap is i3's inner gap: ground between the panes. With shared
// borders off, each pane draws its own box, so the cells between two of them
// are that pane's border, then the ground, then the other pane's border - and
// nothing else. The BSP path asked the tree for its splits and the tree knows
// the gap but not what the panes look like, so it offered a divider and the
// overlay drew it: three vertical rules where the user asked for two blank
// columns. The master-stack path never had the bug, because it reads its splits
// back off panes it only collects while they are borderless.
//
// Asserted on the composed frame rather than on the split list, because the
// split list is what was wrong and a frame is where a person saw it.
func TestGapBetweenBorderedPanesIsEmptyGround(t *testing.T) {
	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack} {
		for _, gap := range []int{1, 2, 3} {
			t.Run(fmt.Sprintf("%s/gap=%d", mode, gap), func(t *testing.T) {
				m := modeOS(t, mode, false, gap, 2, 120, 40)
				marks := []rune{'A', 'B'}
				for i, w := range m.Windows {
					fillGuest(w, marks[i])
				}
				rows := frameRows(m)

				left, right := m.Windows[0], m.Windows[1]
				if left.X > right.X {
					left, right, marks[0], marks[1] = right, left, marks[1], marks[0]
				}
				if left.X+left.Width >= right.X {
					t.Fatalf("the two panes are not side by side: %d..%d and %d..%d",
						left.X, left.X+left.Width, right.X, right.X+right.Width)
				}

				checked := 0
				for y := max(left.Y, right.Y) + 1; y < min(left.Y+left.Height, right.Y+right.Height)-1; y++ {
					if y >= len(rows) {
						break
					}
					row := rows[y]
					// The ground is the cells strictly between the two panes'
					// own border columns.
					for x := left.X + left.Width; x < right.X; x++ {
						if x >= len(row) {
							break
						}
						checked++
						if isBoxDrawing(row[x]) {
							t.Fatalf("row %d column %d between the panes reads %q: appearance.gap is ground, not a rule",
								y, x, string(row[x]))
						}
					}
				}
				if checked == 0 {
					t.Fatal("no gap cell was examined, so this proves nothing")
				}
			})
		}
	}
}

// TestSharedBorderPanesStillGetTheirDivider is the other side of the check
// above, so a fix that simply stopped drawing dividers cannot pass both. With
// shared borders on, the cells between two panes carry exactly one drawn line
// and the rest is ground.
func TestSharedBorderPanesStillGetTheirDivider(t *testing.T) {
	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack} {
		for _, gap := range []int{0, 2} {
			t.Run(fmt.Sprintf("%s/gap=%d", mode, gap), func(t *testing.T) {
				m := modeOS(t, mode, true, gap, 2, 120, 40)
				marks := []rune{'A', 'B'}
				for i, w := range m.Windows {
					fillGuest(w, marks[i])
				}
				rows := frameRows(m)

				left, right := m.Windows[0], m.Windows[1]
				if left.X > right.X {
					left, right = right, left
				}
				want := max(gap, 1)
				if got := right.X - (left.X + left.Width); got != want {
					t.Fatalf("%d cells between the panes, want %d", got, want)
				}

				checked := 0
				for y := max(left.Y, right.Y); y < min(left.Y+left.Height, right.Y+right.Height); y++ {
					if y >= len(rows) {
						break
					}
					row := rows[y]
					drawn := 0
					for x := left.X + left.Width; x < right.X; x++ {
						if x < len(row) && isBoxDrawing(row[x]) {
							drawn++
						}
					}
					checked++
					if drawn != 1 {
						t.Fatalf("row %d has %d drawn cells between the panes, want exactly one divider", y, drawn)
					}
				}
				if checked == 0 {
					t.Fatal("no row showed both panes, so this proves nothing")
				}
			})
		}
	}
}

// TestScrollingColumnsHonourThePaneGap pins the strip's half of the gap. The
// column gap existed in the layout code and nothing ever set it, so
// appearance.gap did nothing at all in scrolling mode and the documentation
// carried it as a limitation.
func TestScrollingColumnsHonourThePaneGap(t *testing.T) {
	for _, gap := range []int{0, 1, 3} {
		t.Run(fmt.Sprintf("gap=%d", gap), func(t *testing.T) {
			m := modeOS(t, LayoutModeScrolling, false, gap, 3, 160, 48)
			cols := make([]*terminal.Window, len(m.Windows))
			copy(cols, m.Windows)
			for i := range cols {
				for j := i + 1; j < len(cols); j++ {
					if cols[j].X < cols[i].X {
						cols[i], cols[j] = cols[j], cols[i]
					}
				}
			}
			for i := 1; i < len(cols); i++ {
				prev := cols[i-1]
				if got := cols[i].X - (prev.X + prev.Width); got != gap {
					t.Errorf("%d cells between column %d and column %d, want %d", got, i-1, i, gap)
				}
			}
		})
	}
}

// TestScrollingStackHonoursThePaneGap is the same for windows stacked inside
// one column, which divide its height.
func TestScrollingStackHonoursThePaneGap(t *testing.T) {
	m := modeOS(t, LayoutModeScrolling, false, 2, 3, 160, 48)
	sl := m.GetOrCreateScrollingLayout()
	// Pull the second and third windows into the first column, which is what
	// the consume action does.
	sl.FocusedCol = 0
	sl.ConsumeWindow()
	sl.ConsumeWindow()
	if len(sl.Columns) != 1 || len(sl.Columns[0].WindowIDs) != 3 {
		t.Fatalf("expected one column of three windows, got %d columns", len(sl.Columns))
	}
	m.ScrollingSetPositions()
	m.CompleteAllAnimations()

	stack := make([]*terminal.Window, len(m.Windows))
	copy(stack, m.Windows)
	for i := range stack {
		for j := i + 1; j < len(stack); j++ {
			if stack[j].Y < stack[i].Y {
				stack[i], stack[j] = stack[j], stack[i]
			}
		}
	}
	for i := 1; i < len(stack); i++ {
		prev := stack[i-1]
		if got := stack[i].Y - (prev.Y + prev.Height); got != 2 {
			t.Errorf("%d rows between stacked window %d and %d, want 2", got, i-1, i)
		}
	}
	// The stack still fills the column: no rows are lost to rounding on top of
	// the gaps it reserved.
	first, last := stack[0], stack[len(stack)-1]
	if got, want := last.Y+last.Height-first.Y, m.GetUsableHeight(); got != want {
		t.Errorf("the stack covers %d rows of a %d row column", got, want)
	}
}

// TestDisablingTilingRemembersTheLayoutMode pins the one path that forgot it.
// The tiling toggle has always kept the mode, so a scrolling session that
// toggled tiling off and on came back scrolling; the palette's "Disable Tiling"
// cleared the scrolling flag, and since the mode travels in session state the
// session itself forgot, on every client and across a reattach.
func TestDisablingTilingRemembersTheLayoutMode(t *testing.T) {
	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack, LayoutModeScrolling} {
		t.Run(mode, func(t *testing.T) {
			m := modeOS(t, mode, false, 0, 3, 120, 40)
			m.DisableAllTiling()
			if m.AutoTiling {
				t.Fatal("tiling must be off")
			}
			if got := m.LayoutModeName(); got != mode {
				t.Errorf("layout mode is %q after disabling tiling, want %q", got, mode)
			}
			if got := m.BuildSessionState().LayoutMode; got != mode {
				t.Errorf("session state carries layout mode %q, want %q", got, mode)
			}
			if got := m.LayoutName(); got != LayoutFloating {
				t.Errorf("the layout in force is %q, want %q", got, LayoutFloating)
			}
		})
	}
}

// TestMasterStackSharesAreEven pins the half of the gap fix a spacing check
// cannot see. Taking the whole gap out of the far side of a split leaves the
// rectangles correctly spaced and the far pane smaller by the gap, at every
// pane count and on both axes, so the contract test above passes on it.
//
// With the master ratio at its default the two sides of a split are the same
// size, give or take the one cell an odd extent cannot divide. The grid rows
// and columns are the same rule: neighbours differ by at most one cell, and no
// pane carries the whole remainder.
func TestMasterStackSharesAreEven(t *testing.T) {
	for _, gap := range []int{0, 1, 2, 3} {
		for _, n := range []int{2, 3, 4, 5, 6, 7, 9} {
			for _, sz := range contractSizes {
				label := fmt.Sprintf("gap=%d/n=%d/%dx%d", gap, n, sz.w, sz.h)
				t.Run(label, func(t *testing.T) {
					m := modeOS(t, LayoutModeMasterStack, false, gap, n, sz.w, sz.h)

					// Panes sharing a row have to be the same width, and panes
					// sharing a column the same height, to within a cell.
					byRow := map[int][]*terminal.Window{}
					byCol := map[int][]*terminal.Window{}
					for _, w := range m.Windows {
						byRow[w.Y] = append(byRow[w.Y], w)
						byCol[w.X] = append(byCol[w.X], w)
					}
					for y, row := range byRow {
						lo, hi := row[0].Width, row[0].Width
						for _, w := range row {
							lo, hi = min(lo, w.Width), max(hi, w.Width)
						}
						if hi-lo > 1 {
							t.Errorf("%s: panes on row %d are %d to %d columns wide; a share differs by more than a cell",
								label, y, lo, hi)
						}
					}
					for x, col := range byCol {
						lo, hi := col[0].Height, col[0].Height
						for _, w := range col {
							lo, hi = min(lo, w.Height), max(hi, w.Height)
						}
						if hi-lo > 1 {
							t.Errorf("%s: panes in column %d are %d to %d rows tall; a share differs by more than a cell",
								label, x, lo, hi)
						}
					}
				})
			}
		}
	}
}
