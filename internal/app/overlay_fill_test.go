package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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
