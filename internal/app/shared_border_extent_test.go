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

// A divider is the whole boundary between two panes, so it has to reach both
// ends of the region it splits: the dock's hairline where the dock closes the
// content region, the sidebar's edge rule where the sidebar does, and the last
// cell where nothing does. These read the composed frame, because the extent the
// user sees is the glyph in the cell and not the numbers in the split.

// extentOS builds a shared-border session of n tiled panes with the dock and the
// sidebar on the given sides ("" for no sidebar), so the separator overlay is
// exercised against every shape the content region takes.
func extentOS(t *testing.T, n int, dock, sidebar string) *OS {
	t.Helper()
	shared, anim, ascii := config.SharedBorders, config.AnimationsEnabled, config.UseASCIIOnly
	style, dockPos := config.BorderStyle, config.DockbarPosition
	sidebarOn, sidebarPos := config.SidebarEnabled, config.SidebarPosition
	config.SharedBorders = true
	config.AnimationsEnabled = false
	config.UseASCIIOnly = false
	config.BorderStyle = "rounded"
	config.DockbarPosition = dock
	config.SidebarEnabled = sidebar != ""
	if sidebar != "" {
		config.SidebarPosition = sidebar
	}
	t.Cleanup(func() {
		config.SharedBorders, config.AnimationsEnabled, config.UseASCIIOnly = shared, anim, ascii
		config.BorderStyle, config.DockbarPosition = style, dockPos
		config.SidebarEnabled, config.SidebarPosition = sidebarOn, sidebarPos
	})

	m := &OS{
		Windows:          make([]*terminal.Window, 0, n),
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		WorkspaceTrees:   map[int]*layout.BSPTree{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            160,
		Height:           48,
		AutoTiling:       true,
		UseBSPLayout:     true,
		MasterRatio:      0.5,
	}
	for i := range n {
		win := newTestWindow(t, fmt.Sprintf("extent-%s-%s-%d-%d", dock, sidebar, n, i), 40, 20)
		win.Workspace = 1
		m.Windows = append(m.Windows, win)
	}
	m.TileAllWindows()
	return m
}

// frameCells composes the frame with its chrome and returns it as rows of runes,
// so a cell can be read where the user sees it.
func frameCells(t *testing.T, m *OS) [][]rune {
	t.Helper()
	var g [][]rune
	for _, line := range strings.Split(lipgloss.Sprint(m.GetCanvas(true).Render()), "\n") {
		g = append(g, []rune(ansi.Strip(line)))
	}
	return g
}

// dockRuleRow is the row holding the dock's hairline, which is the rule that
// closes the content region on the dock's side. It is -1 with the dock hidden,
// where the region runs to the screen edge instead.
func dockRuleRow(m *OS) int {
	b := m.GetBSPBounds()
	switch config.DockbarPosition {
	case "top":
		return b.Y - 1
	case "hidden":
		return -1
	}
	return b.Y + b.H
}

func sidebarName(side string) string {
	if side == "" {
		return "no-sidebar"
	}
	return side + "-sidebar"
}

// firstSplit returns the first divider on the given axis, failing when the
// layout has none: a test that asserts nothing is worse than a red one.
func firstSplit(t *testing.T, m *OS, vertical bool) layout.SplitLine {
	t.Helper()
	for _, s := range m.separatorSplits() {
		if s.Vertical == vertical {
			return s
		}
	}
	axis := "horizontal"
	if vertical {
		axis = "vertical"
	}
	t.Fatalf("this layout drew no %s divider", axis)
	return layout.SplitLine{}
}

func cellIn(g [][]rune, x, y int) string { return fmt.Sprintf("%q", string(cellAt(g, x, y))) }

// TestVerticalDividerRunsItsWholeDivision: with two panes side by side the
// divider splits the whole content region, so every row of it carries the line
// and the end rows are the line rather than a corner that paints half a cell.
// Where the dock closes the region, the divider reaches the hairline and meets
// it with a junction.
func TestVerticalDividerRunsItsWholeDivision(t *testing.T) {
	for _, dock := range []string{"top", "bottom"} {
		for _, side := range []string{"left", "right", ""} {
			t.Run(dock+"-dock/"+sidebarName(side), func(t *testing.T) {
				m := extentOS(t, 2, dock, side)
				b := m.GetBSPBounds()
				border := config.GetBorderForStyle()
				vert := firstRune(border.Left, '│')
				s := firstSplit(t, m, true)
				g := frameCells(t, m)

				for y := b.Y; y < b.Y+b.H; y++ {
					if got := cellAt(g, s.Pos, y); got != vert {
						t.Errorf("divider row %d is %s, want %s: the line does not run the whole division (%d-%d)",
							y, cellIn(g, s.Pos, y), fmt.Sprintf("%q", string(vert)), b.Y, b.Y+b.H-1)
					}
				}

				rule := dockRuleRow(m)
				want := firstRune(border.MiddleTop, '┬') // the divider hangs off the rule
				caps := border.TopLeft + border.TopRight
				if dock != "top" {
					want = firstRune(border.MiddleBottom, '┴')
					caps = border.BottomLeft + border.BottomRight
				}
				// The focused pane caps its own corner there, which is the same
				// meeting drawn with a hook instead of a T.
				if got := cellAt(g, s.Pos, rule); got != want && !strings.ContainsRune(caps, got) {
					t.Errorf("the divider meets the dock hairline at row %d with %s, want %q or one of %q",
						rule, cellIn(g, s.Pos, rule), string(want), caps)
				}
			})
		}
	}
}

// TestHorizontalDividerRunsItsWholeDivision is the same question on the other
// axis: the divider between two stacked panes reaches the sidebar's edge rule
// where the sidebar closes the region, and the last content column where the
// screen does.
func TestHorizontalDividerRunsItsWholeDivision(t *testing.T) {
	for _, side := range []string{"left", "right", ""} {
		t.Run(sidebarName(side), func(t *testing.T) {
			m := extentOS(t, 3, "bottom", side)
			b := m.GetBSPBounds()
			border := config.GetBorderForStyle()
			horiz := firstRune(border.Top, '─')
			s := firstSplit(t, m, false)
			g := frameCells(t, m)

			for x := s.From; x <= s.To; x++ {
				if got := cellAt(g, x, s.Pos); got != horiz {
					t.Errorf("divider column %d is %s, want %q: the line does not run its whole division",
						x, cellIn(g, x, s.Pos), string(horiz))
				}
			}

			if s.To != b.X+b.W-1 {
				t.Fatalf("this divider ends at column %d, inside the region (%d-%d); it cannot answer for the region's edge",
					s.To, b.X, b.X+b.W-1)
			}
			if side != "right" {
				return
			}
			want := firstRune(border.MiddleRight, '┤')
			caps := border.TopRight + border.BottomRight
			if got := cellAt(g, b.X+b.W, s.Pos); got != want && !strings.ContainsRune(caps, got) {
				t.Errorf("the divider meets the sidebar's edge rule at column %d with %s, want %q or one of %q",
					b.X+b.W, cellIn(g, b.X+b.W, s.Pos), string(want), caps)
			}
		})
	}
}

// TestDividerStopsAtACrossing: a divider that ends inside the region ends on the
// divider it meets, drawn as a junction rather than one line over another, and
// nothing of it is drawn past that cell.
func TestDividerStopsAtACrossing(t *testing.T) {
	for _, n := range []int{3, 4} {
		t.Run(fmt.Sprintf("%d-panes", n), func(t *testing.T) {
			m := extentOS(t, n, "bottom", "")
			border := config.GetBorderForStyle()
			junctions := border.Middle + border.MiddleTop + border.MiddleBottom +
				border.MiddleLeft + border.MiddleRight
			g := frameCells(t, m)
			b := m.GetBSPBounds()

			for _, s := range m.separatorSplits() {
				if !s.Vertical || s.From <= b.Y {
					continue
				}
				// This divider starts below the region's top, so it starts on the
				// horizontal divider that bounds it.
				if got := cellAt(g, s.Pos, s.From-1); !strings.ContainsRune(junctions, got) {
					t.Errorf("the divider at column %d starts at row %d but the cell above it is %s, want a junction from %q",
						s.Pos, s.From, cellIn(g, s.Pos, s.From-1), junctions)
				}
				if got := cellAt(g, s.Pos, s.From-2); strings.ContainsRune(junctions+string(firstRune(border.Left, '│')), got) {
					t.Errorf("the divider at column %d is drawn past its crossing, into row %d (%s)",
						s.Pos, s.From-2, cellIn(g, s.Pos, s.From-2))
				}
			}
		})
	}
}

// TestDividerNeverPaintsPaneCells is the invariant the extension must not break:
// a divider cell belongs to no pane. It runs with the sidebar on each side and
// off, because the content region's bounds move with it and the extension is
// measured from them.
func TestDividerNeverPaintsPaneCells(t *testing.T) {
	for _, dock := range []string{"top", "bottom"} {
		for _, side := range []string{"left", "right", ""} {
			for _, n := range []int{2, 3, 4} {
				t.Run(fmt.Sprintf("%s-dock/%s/%d-panes", dock, sidebarName(side), n), func(t *testing.T) {
					m := extentOS(t, n, dock, side)
					paintMarkers(m)
					g := frameCells(t, m)
					for i, w := range m.Windows {
						want := paneMarker(i)
						if w.Y >= len(g) {
							t.Fatalf("pane %d is at row %d, past the %d-row frame", i, w.Y, len(g))
						}
						row := g[w.Y]
						if w.X+len(want) > len(row) {
							t.Fatalf("pane %d row is %d cells, too short for its marker at column %d", i, len(row), w.X)
						}
						if got := string(row[w.X : w.X+len(want)]); got != want {
							t.Errorf("pane %d first columns are %q, want %q: a divider is drawn on pane content", i, got, want)
						}
					}
				})
			}
		}
	}
}
