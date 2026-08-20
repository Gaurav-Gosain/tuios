package app

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// An overlay panel owns every cell of its rectangle: no row shorter than the
// box, no row longer, and every cell in it painted in the panel's own colours.
//
// Width alone does not say that. A row can measure the full width and still be
// made of bare spaces, and a bare space carries no background: it is written
// with the pen reset, so the desktop behind the panel shows through it. That is
// what the all-windows picker did with the three cells at the head of every
// row, the leading space and the two-cell state mark, and three unpainted cells
// down the left edge of a list is what "artifacts on the left" looks like. On
// the selected row they cut a notch out of the highlight.
//
// The check decodes the panel with the compositor's own decoder, so it sees the
// cells the screen will get rather than the string that produced them.
func assertPanelOwnsEveryCell(t *testing.T, name, out string, geo overlay.Geometry) {
	t.Helper()
	if out == "" {
		t.Errorf("%s: rendered nothing", name)
		return
	}

	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w != geo.Width {
			t.Errorf("%s: line %d is %d cells, the panel is %d: %q", name, i, w, geo.Width, ansi.Strip(ln))
			return
		}
	}

	buf := uv.NewScreenBuffer(geo.Width, len(lines))
	uv.NewStyledString(out).Draw(buf, uv.Rect(0, 0, geo.Width, len(lines)))
	for y := range len(lines) {
		for x := range geo.Width {
			c := buf.CellAt(x, y)
			if c == nil {
				t.Errorf("%s: cell (%d,%d) is not drawn at all", name, x, y)
				return
			}
			// The trailing column of a wide glyph, painted by the head cell.
			if c.Width == 0 {
				continue
			}
			if c.Style.Bg == nil {
				t.Errorf("%s: cell (%d,%d) %q carries no background, so the panel is transparent there: %q",
					name, x, y, c.Content, ansi.Strip(lines[y]))
				return
			}
		}
	}
}

// assertRowHitsMatchPanel fails unless every recorded row rectangle spans the
// panel it was drawn in. The rows are recorded by the renderer as it draws and
// never recomputed, so a row that moved without its rectangle following is a
// click that lands on the wrong window.
func assertRowHitsMatchPanel(t *testing.T, name string, geo overlay.Geometry, rows []overlayRowHit) {
	t.Helper()
	for _, r := range rows {
		if r.Rect.X0 != 0 || r.Rect.X1 != geo.Width {
			t.Errorf("%s: row %d spans x=%d..%d, the panel is 0..%d", name, r.Idx, r.Rect.X0, r.Rect.X1, geo.Width)
		}
		if r.Rect.Y0 < geo.BodyY || r.Rect.Y1 > geo.Height {
			t.Errorf("%s: row %d spans y=%d..%d, the panel body is %d..%d",
				name, r.Idx, r.Rect.Y0, r.Rect.Y1, geo.BodyY, geo.Height)
		}
	}
}

// aggregateFillPanes is the awkward content the picker has to hold: a title
// longer than the box, a title of wide runes, a title of one character, panes
// wearing every flag and every agent state, and a named workspace fattening the
// right-hand cluster the name has to yield to.
var aggregateFillPanes = []struct {
	name      string
	workspace int
	minimized bool
	floating  bool
	agent     session.AgentState
}{
	{name: strings.Repeat("a-very-long-window-title ", 6), workspace: 1},
	{name: "日本語のタイトルがとても長いウィンドウ名前", workspace: 1},
	{name: "s", workspace: 2, minimized: true},
	{name: "floating", workspace: 2, floating: true},
	{name: "働", workspace: 3, agent: session.AgentStateWorking},
	{name: "needs", workspace: 3, agent: session.AgentStateNeedsInput},
	{name: "done", workspace: 3, agent: session.AgentStateDone},
	{name: "errored", workspace: 3, agent: session.AgentStateErrored},
}

// aggregateFillWindows builds n windows off that list, cycling through it.
func aggregateFillWindows(n int) []*terminal.Window {
	windows := make([]*terminal.Window, 0, n)
	for i := range n {
		p := aggregateFillPanes[i%len(aggregateFillPanes)]
		windows = append(windows, &terminal.Window{
			ID: strconv.Itoa(i), CustomName: p.name, Width: 80, Height: 24,
			Workspace: p.workspace, Minimized: p.minimized, IsFloating: p.floating,
			AgentState: string(p.agent),
		})
	}
	return windows
}

// aggregateFillCounts covers the list sizes whose row arithmetic differs: none,
// one, a panel-full, and more than fits, which is the only case that draws the
// scroll readout.
var aggregateFillCounts = []int{0, 1, aggregateViewMaxRows, 40}

// TestAggregateViewOwnsEveryCell pins the picker's fill contract against the
// content that broke it and the screen sizes that squeeze it.
func TestAggregateViewOwnsEveryCell(t *testing.T) {
	for _, sc := range narrowScreens {
		t.Run(sc.name, func(t *testing.T) {
			m := newNarrowOS(t, sc.w, sc.h)
			m.CurrentWorkspace = 1
			m.WorkspaceNames = map[int]string{2: "a-long-workspace-name"}

			for _, n := range aggregateFillCounts {
				m.Windows = aggregateFillWindows(n)
				// The last row, so the list is scrolled to its end and the
				// readout carries its widest number.
				m.AggregateViewSelected = max(n-1, 0)
				m.AggregateViewScroll = 0

				name := fmt.Sprintf("%d windows", n)
				out, geo, rows := m.renderAggregateView()
				assertPanelOwnsEveryCell(t, name, out, geo)
				assertRowHitsMatchPanel(t, name, geo, rows)
			}

			// A query that hides everything: the panel still owns its box, and
			// the empty message is the only thing in it.
			m.Windows = aggregateFillWindows(len(aggregateFillPanes))
			m.AggregateViewQuery = "zzz-no-window-matches-this"
			m.AggregateViewSelected, m.AggregateViewScroll = 0, 0
			out, geo, rows := m.renderAggregateView()
			assertPanelOwnsEveryCell(t, "no match", out, geo)
			assertRowHitsMatchPanel(t, "no match", geo, rows)
			m.AggregateViewQuery = ""

			// The focused pane wears the accent mark, which is the other shape
			// the row's leading cells take.
			m.FocusedWindow = 1
			out, geo, rows = m.renderAggregateView()
			assertPanelOwnsEveryCell(t, "focused", out, geo)
			assertRowHitsMatchPanel(t, "focused", geo, rows)
		})
	}
}

// TestAggregateViewOwnsEveryCellASCII repeats the check for terminals without a
// capable font, where the marks, the ellipsis and the rule all change glyph.
func TestAggregateViewOwnsEveryCellASCII(t *testing.T) {
	overlay.SetASCII(true)
	t.Cleanup(func() { overlay.SetASCII(false) })

	m := newNarrowOS(t, 120, 40)
	m.CurrentWorkspace = 1
	m.Windows = aggregateFillWindows(len(aggregateFillPanes))
	m.FocusedWindow = 0
	out, geo, rows := m.renderAggregateView()
	assertPanelOwnsEveryCell(t, "ascii", out, geo)
	assertRowHitsMatchPanel(t, "ascii", geo, rows)
}

// TestListOverlaysOwnEveryCell holds the rest of the search-and-list family to
// the same contract, so the picker is not fixed alone and the next overlay
// built on the shared helper inherits the check.
func TestListOverlaysOwnEveryCell(t *testing.T) {
	for _, sc := range narrowScreens {
		t.Run(sc.name, func(t *testing.T) {
			m := newNarrowOS(t, sc.w, sc.h)

			out, geo, rows := m.renderCommandPalette()
			assertPanelOwnsEveryCell(t, "palette", out, geo)
			assertRowHitsMatchPanel(t, "palette", geo, rows)

			m.CommandPaletteQuery = "zzzz-no-match"
			out, geo, rows = m.renderCommandPalette()
			assertPanelOwnsEveryCell(t, "palette empty", out, geo)
			assertRowHitsMatchPanel(t, "palette empty", geo, rows)
			m.CommandPaletteQuery = ""

			m.LayoutPickerItems = []LayoutTemplate{
				{Name: strings.Repeat("a-layout-name ", 6), AutoTiling: true},
				{Name: "dev"},
			}
			out, geo, rows = m.renderLayoutPicker()
			assertPanelOwnsEveryCell(t, "layout picker", out, geo)
			assertRowHitsMatchPanel(t, "layout picker", geo, rows)

			m.OpenThemePicker()
			out, geo, rows = m.renderThemePicker()
			assertPanelOwnsEveryCell(t, "theme picker", out, geo)
			assertRowHitsMatchPanel(t, "theme picker", geo, rows)
			m.CancelThemePicker()

			m.OpenQuitMenu()
			out, geo, rows = m.renderQuitMenu()
			assertPanelOwnsEveryCell(t, "quit", out, geo)
			assertRowHitsMatchPanel(t, "quit", geo, rows)
			m.CloseQuitMenu()
		})
	}
}
