package app

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// A division is two panes facing each other across a split, and the cells the
// layout left between their rectangles.
type division struct {
	a, b     *terminal.Window
	cells    int
	vertical bool
}

// wantCells is what a division between these two panes is allowed to hold.
//
// It reads the answer off the panes as drawn rather than off the setting that
// put them that way. Borderless panes are guest output edge to edge, so the
// divider the user sees needs a column of its own between them. Panes drawing
// their own borders have two adjacent border columns doing that job already,
// and a third column would draw nothing while costing both panes a column.
func wantCells(d division) int {
	if d.a.Tiled && d.b.Tiled {
		return 1
	}
	return 0
}

// paneDivisions finds every pair of visible panes that face each other across a
// split. Pairs further apart than a division could plausibly be are ignored:
// they are separated by a third pane, not by a divider.
func paneDivisions(m *OS) []division {
	var vis []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing && !w.IsFloating {
			vis = append(vis, w)
		}
	}
	var out []division
	for _, a := range vis {
		for _, b := range vis {
			if a == b {
				continue
			}
			if b.X > a.X && a.Y < b.Y+b.Height && b.Y < a.Y+a.Height {
				if g := b.X - (a.X + a.Width); g >= 0 && g <= 2 {
					out = append(out, division{a: a, b: b, cells: g, vertical: true})
				}
			}
			if b.Y > a.Y && a.X < b.X+b.Width && b.X < a.X+a.Width {
				if g := b.Y - (a.Y + a.Height); g >= 0 && g <= 2 {
					out = append(out, division{a: a, b: b, cells: g})
				}
			}
		}
	}
	return out
}

// checkDivisions asserts no division holds a cell nothing is drawn in. It is
// called from checkPaneSizes so that every layout transition the announce
// matrix already walks is checked for a stranded gap as well.
func checkDivisions(t *testing.T, m *OS, label string) {
	t.Helper()
	for _, d := range paneDivisions(m) {
		if want := wantCells(d); d.cells != want {
			t.Errorf("%s: division between %s and %s holds %d cells, want %d (tiled %v/%v)",
				label, d.a.ID, d.b.ID, d.cells, want, d.a.Tiled, d.b.Tiled)
		}
	}
}

// fillPanes paints each pane's guest full of one character of its own, so the
// composed frame shows exactly which cells that guest owns.
func fillPanes(m *OS) []rune {
	marks := make([]rune, len(m.Windows))
	for i, w := range m.Windows {
		marks[i] = rune('A' + i)
		w.LockIO()
		_, _ = w.Terminal.Write([]byte(strings.Repeat(string(marks[i]), w.ContentWidth())))
		w.UnlockIO()
		w.MarkContentDirty()
	}
	return marks
}

// checkFrameDivision reads the division between two panes off the composed
// frame: the cells between the last cell one guest owns and the first cell the
// other owns. Borderless panes are separated by the one separator cell;
// bordered panes by their own two border cells and nothing else.
//
// This is the measurement the report was made from, taken where the user takes
// it, and it doubles as the check that nothing paints into a guest-owned cell:
// each guest's run has to be unbroken across its whole drawable width.
func checkFrameDivision(t *testing.T, m *OS, label string) {
	t.Helper()
	marks := fillPanes(m)
	lines := strings.Split(lipgloss.Sprint(m.GetCanvas(true).Render()), "\n")

	for _, d := range paneDivisions(m) {
		if !d.vertical {
			continue
		}
		ai, bi := -1, -1
		for i, w := range m.Windows {
			if w == d.a {
				ai = i
			}
			if w == d.b {
				bi = i
			}
		}
		want := 2
		if wantCells(d) == 1 {
			// Borderless: one separator cell, no border cells either side.
			want = 1
		}

		checked := 0
		for y := max(d.a.Y, d.b.Y); y < min(d.a.Y+d.a.Height, d.b.Y+d.b.Height); y++ {
			if y >= len(lines) {
				break
			}
			row := []rune(ansi.Strip(lines[y]))
			lastA, firstB := -1, -1
			for x, r := range row {
				if r == marks[ai] {
					lastA = x
				}
				if r == marks[bi] && firstB < 0 {
					firstB = x
				}
			}
			if lastA < 0 || firstB < 0 || firstB < lastA {
				continue
			}
			checked++
			if got := firstB - lastA - 1; got != want {
				t.Errorf("%s row %d: %d cells between the two guests, want %d",
					label, y, got, want)
			}
			// The guest owns every cell of its drawable width, divider or no
			// divider.
			for x := d.a.X + d.a.BorderOffset(); x < d.a.X+d.a.BorderOffset()+d.a.ContentWidth(); x++ {
				if x < len(row) && row[x] != marks[ai] {
					t.Fatalf("%s row %d: column %d inside %s reads %q, not the guest's own output",
						label, y, x, d.a.ID, string(row[x]))
				}
			}
		}
		if checked == 0 {
			t.Errorf("%s: no frame row showed both %s and %s", label, d.a.ID, d.b.ID)
		}
	}
}

// TestDivisionReservesOnlyWhatItDraws walks the four cells of the
// tiling-by-shared-borders matrix and pins what the division between two panes
// holds in each, on the rectangles and again on the composed frame. The sidebar
// runs off, left and right because the content region's width moves with it.
func TestDivisionReservesOnlyWhatItDraws(t *testing.T) {
	prevShared, prevSide, prevPos := config.SharedBorders, config.SidebarEnabled, config.SidebarPosition
	t.Cleanup(func() {
		config.SharedBorders, config.SidebarEnabled, config.SidebarPosition = prevShared, prevSide, prevPos
	})

	for _, side := range []struct {
		name string
		on   bool
		pos  string
	}{{"sidebar-off", false, "left"}, {"sidebar-left", true, "left"}, {"sidebar-right", true, "right"}} {
		for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack} {
			for _, shared := range []bool{false, true} {
				for _, tiling := range []bool{true, false} {
					name := fmt.Sprintf("%s/%s/shared=%v/tiling=%v", side.name, mode, shared, tiling)
					t.Run(name, func(t *testing.T) {
						config.SidebarEnabled, config.SidebarPosition = side.on, side.pos
						config.SharedBorders = shared
						m := gapTestOS(t, 2)
						m.UseBSPLayout = true
						m.TileAllWindows()
						m.ApplyLayoutModeName(mode)
						m.TileAllWindows()
						if !tiling {
							m.ToggleAutoTiling()
						}

						// The panes must be drawn the way this cell says before
						// their division means anything.
						for _, w := range m.Windows {
							if got := w.Tiled; got != (shared && tiling) {
								t.Fatalf("%s: pane %s borderless=%v, want %v", name, w.ID, got, shared && tiling)
							}
						}
						divs := paneDivisions(m)
						if len(divs) == 0 {
							t.Fatalf("%s: found no adjacent panes to measure", name)
						}
						checkDivisions(t, m, name)
						checkFrameDivision(t, m, name)
					})
				}
			}
		}
	}
}

// TestLiveToggleReclaimsTheDivider drives both settings in both directions at
// runtime. Each toggle has to re-lay-out: a layout computed under the other
// setting and then left alone strands its panes around a column nobody fills,
// which is the shape of failure this area keeps producing.
func TestLiveToggleReclaimsTheDivider(t *testing.T) {
	prevShared := config.SharedBorders
	t.Cleanup(func() { config.SharedBorders = prevShared })

	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack} {
		for _, start := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/from-shared=%v", mode, start), func(t *testing.T) {
				config.SharedBorders = start
				m := gapTestOS(t, 3)
				m.UseBSPLayout = true
				m.TileAllWindows()
				m.ApplyLayoutModeName(mode)
				m.TileAllWindows()

				step := func(label string) {
					t.Helper()
					checkDivisions(t, m, label)
				}
				step("settled")

				setSharedBorders(m, !start)
				step("shared toggled")
				setSharedBorders(m, start)
				step("shared toggled back")

				m.ToggleAutoTiling()
				step("tiling off")
				m.ToggleAutoTiling()
				step("tiling on")

				// Off again, then flip the setting underneath a layout that is
				// no longer tiling, and back on: no combination may leave a
				// column stranded.
				m.ToggleAutoTiling()
				setSharedBorders(m, !start)
				step("tiling off, shared flipped")
				setSharedBorders(m, start)
				step("tiling off, shared restored")
				m.ToggleAutoTiling()
				step("tiling on again")
			})
		}
	}
}

// TestDividerReclaimReachesEveryWorkspace pins the stranding case: tiling is
// turned off while another workspace is on screen, so the workspace holding the
// gap is not the one being retiled. Its panes must not be left drawing their
// own boxes inside rectangles still spaced for a separator.
func TestDividerReclaimReachesEveryWorkspace(t *testing.T) {
	prevShared := config.SharedBorders
	config.SharedBorders = true
	t.Cleanup(func() { config.SharedBorders = prevShared })

	m := gapTestOS(t, 4)
	m.UseBSPLayout = true
	m.Windows[2].Workspace = 2
	m.Windows[3].Workspace = 2
	m.TileAllWindows()
	m.SwitchToWorkspace(2)
	m.TileAllWindows()
	m.SwitchToWorkspace(1)

	m.ToggleAutoTiling()
	checkDivisions(t, m, "workspace 1 after toggle")
	m.SwitchToWorkspace(2)
	checkDivisions(t, m, "workspace 2 after toggle")
}
