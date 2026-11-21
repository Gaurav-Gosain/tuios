package app

import (
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) dockSessionTooltipTrack(a DockSessionAction) {
	if a == DockSessionNone {
		if m.Tooltip.Source == tooltipDockSession {
			m.tooltipClear()
		}
		return
	}
	m.tooltipTrack(tooltipDockSession, int(a))
}

// DockHoverActive reports whether the dock is showing something the pointer
// put there: a brightened session control, or a label pending or up on a
// control or a pill. The motion filter passes one more event while it is, so
// the pointer leaving the band is the event that clears it.
func (m *OS) DockHoverActive() bool {
	return m.dockSessionHover != DockSessionNone || m.dockButtonHover != 0 ||
		m.Tooltip.Source == tooltipDockButton ||
		m.Tooltip.Source == tooltipDockSession || m.Tooltip.Source == tooltipDockWorkspace ||
		m.notifPaused() || m.Tooltip.Source == tooltipDockNotif
}

func (m *OS) renderDockSessionTooltip() *lipgloss.Layer {
	if !m.tooltipVisible(tooltipDockSession) {
		return nil
	}
	m.Tooltip.Shown = true

	for _, h := range m.dockSessionHits {
		if int(h.Action) != m.Tooltip.Key {
			continue
		}
		renderW := m.GetRenderWidth()
		label := tooltipLabel(m.dockSessionTooltipText(h.Action), renderW, theme.UI())
		return tooltipLayer(label, overlay.Rect{X0: h.X0, Y0: h.Y, X1: h.X1, Y1: h.Y + 1}, m.dockTooltipSide(), m.screenRect(), "dock-session-tooltip")
	}
	return nil
}

func (m *OS) dockWorkspaceTooltipTrack(ws int) {
	if ws <= 0 || !m.workspacePillHidesName(ws) {
		if m.Tooltip.Source == tooltipDockWorkspace {
			m.tooltipClear()
		}
		return
	}
	m.tooltipTrack(tooltipDockWorkspace, ws)
}

func (m *OS) renderDockWorkspaceTooltip() *lipgloss.Layer {
	if !m.tooltipVisible(tooltipDockWorkspace) {
		return nil
	}
	m.Tooltip.Shown = true

	for _, h := range m.dockWorkspaceHits {
		if h.Workspace != m.Tooltip.Key {
			continue
		}
		renderW := m.GetRenderWidth()
		label := tooltipLabel(m.workspacePillName(h.Workspace), renderW, theme.UI())
		return tooltipLayer(label, overlay.Rect{X0: h.X0, Y0: h.Y, X1: h.X1, Y1: h.Y + 1}, m.dockTooltipSide(), m.screenRect(), "dock-workspace-tooltip")
	}
	return nil
}

// DockWorkspaceHoverAt arms the pill label for whatever workspace pill covers
// (x, y), and drops it when the pointer is on none. It reports whether the
// pointer is on a pill.
//
// Like the session controls' hover it does not consume the motion: the strip has
// no other reaction to a pointer crossing it, and the arriving motion is the
// only clock the label has.
func (m *OS) DockWorkspaceHoverAt(x, y int) bool {
	x, y = m.ScreenPoint(x, y)
	ws := m.DockWorkspacePillAt(x, y)
	m.dockWorkspaceTooltipTrack(ws)
	return ws > 0
}

func (m *OS) dockHoverChangesAt(x, y int) bool {
	if m.notifHoverChangesAt(x, y) {
		return true
	}
	if i := m.DockButtonAt(x, y); i+1 != m.dockButtonHover {
		return true
	} else if i >= 0 {
		return m.Tooltip.Source != tooltipDockButton || m.Tooltip.Key != i
	}
	a := m.DockSessionActionAt(x, y)
	if a != m.dockSessionHover {
		return true
	}
	src, key := tooltipNone, 0
	if a != DockSessionNone {
		src, key = tooltipDockSession, int(a)
	} else if ws := m.DockWorkspacePillAt(x, y); ws > 0 && m.workspacePillHidesName(ws) {
		src, key = tooltipDockWorkspace, ws
	}
	switch {
	case src == tooltipNone && m.notifBlockAt(x, y):
		return false
	case src == tooltipNone:
		return m.Tooltip.Source == tooltipDockSession || m.Tooltip.Source == tooltipDockWorkspace ||
			m.Tooltip.Source == tooltipDockButton
	case !m.tooltipsEnabled(src):
		return m.Tooltip.Source != tooltipNone
	default:
		return m.Tooltip.Source != src || m.Tooltip.Key != key
	}
}
