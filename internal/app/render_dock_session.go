package app

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type dockSessionCell struct {
	Text   string
	Width  int
	Action DockSessionAction
}

func (m *OS) buildDockSessionStrip() (string, []dockSessionCell) {
	m.ensureDockPlan()
	if !m.dockPlan.Has(config.DockComponentSessionControls) {
		return "", nil
	}
	if !dockSessionControlsFit(m.GetRenderWidth()) {
		return "", nil
	}

	pal := m.groundUI()
	cells := make([]dockSessionCell, 0, 4)
	if present, waiting := m.dockAgents(); present {
		cells = append(cells, m.dockAgentsCell(waiting, pal))
	}
	if m.CanCreateSession() {
		cells = append(cells, m.dockSessionCell(DockSessionNew, pal))
	}
	if m.CanLeaveRunning() {
		cells = append(cells, m.dockSessionCell(DockSessionLeave, pal))
	}
	cells = append(cells, m.dockSessionCell(DockSessionClose, pal))

	var b strings.Builder
	b.WriteString(" ")
	for _, c := range cells {
		b.WriteString(c.Text)
	}
	b.WriteString(" ")
	return b.String(), cells
}

func (m *OS) dockSessionStripWidth() int {
	strip, _ := m.buildDockSessionStrip()
	return lipgloss.Width(strip)
}

func (m *OS) dockAgentsCell(waiting int, pal overlay.Palette) dockSessionCell {
	body := " " + dockSessionIcon(DockSessionAgents, &m.Settings) + " "
	st := lipgloss.NewStyle().Bold(true)
	switch {
	case waiting > 0:
		body += strconv.Itoa(min(waiting, 99)) + " "
		st = st.Foreground(theme.Readable(pal.Attention, pal.Canvas))
	case m.dockSessionHover == DockSessionAgents:
		st = st.Foreground(theme.Readable(pal.AccentBright, pal.Canvas))
	default:
		st = st.Foreground(pal.Fg)
	}
	return dockSessionCell{Text: st.Render(body), Width: lipgloss.Width(body), Action: DockSessionAgents}
}

func (m *OS) dockSessionCell(a DockSessionAction, pal overlay.Palette) dockSessionCell {
	body := " " + dockSessionIcon(a, &m.Settings) + " "

	st := lipgloss.NewStyle()
	hovered := m.dockSessionHover == a
	switch {
	case a != DockSessionClose && hovered:
		st = st.Foreground(theme.Readable(pal.AccentBright, pal.Canvas)).Bold(true)
	case a != DockSessionClose:
		st = st.Foreground(pal.Fg).Bold(true)
	case hovered:
		st = st.Foreground(theme.Readable(pal.Warn, pal.Canvas)).Bold(true)
	default:
		st = st.Foreground(theme.Readable(pal.FgMute, pal.Canvas))
	}

	return dockSessionCell{Text: st.Render(body), Width: lipgloss.Width(body), Action: a}
}
