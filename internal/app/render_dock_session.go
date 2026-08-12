package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// dockSessionCell is one drawn session control: the styled cells and how many
// columns they take, so the caller can turn the strip's internal offsets into
// screen columns without measuring styled text a second time.
type dockSessionCell struct {
	Text   string
	Width  int
	Action DockSessionAction
}

// buildDockSessionStrip renders the session controls as they sit at the dock's
// right-hand end, and returns the cells in drawn order.
//
// The strip opens and closes with a bare column. The closing one is what keeps
// the destructive control off the screen's last column: a pointer thrown at the
// right edge stops on a cell that does nothing, rather than on the one button
// here that cannot be undone.
func (m *OS) buildDockSessionStrip() (string, []dockSessionCell) {
	tier := dockSessionTierFor(m.GetRenderWidth())
	if tier == dockSessionTierOff {
		return "", nil
	}

	pal := theme.UI()
	cells := make([]dockSessionCell, 0, 2)
	if m.CanLeaveRunning() {
		cells = append(cells, m.dockSessionCell(DockSessionLeave, tier, pal))
	}
	cells = append(cells, m.dockSessionCell(DockSessionClose, tier, pal))

	var b strings.Builder
	b.WriteString(" ")
	for _, c := range cells {
		b.WriteString(c.Text)
	}
	b.WriteString(" ")
	return b.String(), cells
}

// dockSessionStripWidth is the strip's column span, used by the layout pass to
// lay the rest of the bar out against what the controls leave. It builds the
// same strip the renderer does rather than adding up the constants again, which
// is the arithmetic that would silently drift the moment a label changes.
func (m *OS) dockSessionStripWidth() int {
	strip, _ := m.buildDockSessionStrip()
	return lipgloss.Width(strip)
}

// dockSessionCell styles one control.
//
// The weight split is the whole design: leaving is normal dock text and bold,
// closing is muted until the pointer arrives and then goes destructive. Neither
// wears a fill, which is still spent entirely on the mode pill.
func (m *OS) dockSessionCell(a DockSessionAction, tier dockSessionTier, pal overlay.Palette) dockSessionCell {
	body := dockSessionIcon(a)
	if tier == dockSessionTierLabel {
		body += " " + dockSessionLabel(a)
	}
	// A column of padding either side, so the target is a button and not a
	// glyph, and so the two controls do not touch.
	body = " " + body + " "

	st := lipgloss.NewStyle()
	hovered := m.dockSessionHover == a
	switch {
	case a == DockSessionLeave && hovered:
		st = st.Foreground(pal.AccentBright).Bold(true)
	case a == DockSessionLeave:
		st = st.Foreground(pal.Fg).Bold(true)
	case hovered:
		st = st.Foreground(pal.Warn).Bold(true)
	default:
		st = st.Foreground(pal.FgMute)
	}

	return dockSessionCell{Text: st.Render(body), Width: lipgloss.Width(body), Action: a}
}
