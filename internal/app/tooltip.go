package app

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	tooltipPad = 1
)

type tooltipSource int

const (
	tooltipNone tooltipSource = iota
	tooltipRailStrip
	tooltipRailAdd
	tooltipDockSession
	tooltipRailHost
	tooltipDockWorkspace
	tooltipDockNotif
	tooltipDockButton
)

type tooltipState struct {
	Source    tooltipSource
	Key       int
	Secondary bool
	At        time.Time
	Shown     bool
}

func (m *OS) tooltipsEnabled(src tooltipSource) bool {
	if !m.Settings.Tooltips || src == tooltipNone {
		return false
	}
	w := m.GetSidebarWidth()
	if !m.sidebarDrawing && m.Tooltip.Secondary {
		w = m.secondarySidebarWidth()
	}
	if src == tooltipRailStrip {
		return sidebarVariant(w) == sidebarVariantGlyph
	}
	if src == tooltipRailAdd || src == tooltipRailHost {
		return sidebarVariant(w) != sidebarVariantGlyph
	}
	if src == tooltipDockWorkspace {
		return m.Settings.DockWorkspaceTooltip
	}
	return true
}

func (m *OS) tooltipTrack(src tooltipSource, key int) {
	if !m.tooltipsEnabled(src) {
		m.tooltipClear()
		return
	}
	if m.Tooltip.Source == src && m.Tooltip.Key == key && m.Tooltip.Secondary == m.sidebarDrawing {
		return
	}
	m.Tooltip = tooltipState{Source: src, Key: key, Secondary: m.sidebarDrawing, At: time.Now(), Shown: m.Tooltip.Shown}
}

func (m *OS) tooltipClear() { m.Tooltip = tooltipState{} }

func (m *OS) tooltipVisible(src tooltipSource) bool {
	if m.Tooltip.Source != src || !m.tooltipsEnabled(src) {
		return false
	}
	return m.Tooltip.Shown || time.Since(m.Tooltip.At) >= m.Settings.TooltipDelay()
}

// TooltipPending reports whether a label is waiting to be drawn, which is the
// only state that needs the maintenance tick: nothing else will bring the frame
// that draws it. It goes false on that frame, so a shown tooltip is free and the
// pointer at rest anywhere else costs nothing. Bounded by the delay: at worst it
// holds the tick for one gesture's 350 ms.
func (m *OS) TooltipPending() bool {
	return m.tooltipsEnabled(m.Tooltip.Source) && !m.Tooltip.Shown
}

func tooltipLabel(text string, room int, pal overlay.Palette) string {
	pad := strings.Repeat(" ", tooltipPad)
	body := pad + overlay.Truncate(text, max(room-2*tooltipPad, 1)) + pad
	return lipgloss.NewStyle().Background(pal.Surface).Foreground(pal.Fg).Render(body)
}

func tooltipLayer(label string, anchor overlay.Rect, side overlay.Side, screen overlay.Rect, id string) *lipgloss.Layer {
	x, y, _ := overlay.Place(anchor, lipgloss.Width(label), lipgloss.Height(label), screen, overlay.Placement{Side: side})
	return lipgloss.NewLayer(label).X(x).Y(y).Z(config.ZIndexDock + 1).ID(id)
}

func (m *OS) screenRect() overlay.Rect {
	return overlay.Rect{X1: m.GetRenderWidth(), Y1: m.GetRenderHeight()}
}

func (m *OS) railTooltipLayer(label string, row int) *lipgloss.Layer {
	railW, renderW := m.GetSidebarWidth(), m.GetRenderWidth()
	anchor, side := overlay.Rect{X0: 0, Y0: row, X1: railW, Y1: row + 1}, overlay.SideRight
	if m.railEdge() == sidebarRight {
		anchor, side = overlay.Rect{X0: renderW - railW, Y0: row, X1: renderW, Y1: row + 1}, overlay.SideLeft
	}
	return tooltipLayer(label, anchor, side, m.screenRect(), "sidebar-tooltip")
}

func (m *OS) dockTooltipSide() overlay.Side {
	if m.Settings.DockbarPosition == "top" {
		return overlay.SideBelow
	}
	return overlay.SideAbove
}

func (m *OS) pointerLabelLayer(label, id string) *lipgloss.Layer {
	screen := m.screenRect()
	screen.Y1 = m.viewReserve().Top + m.ViewUsableHeight()
	return tooltipLayer(label, overlay.PointRect(m.LastMouseX+2, m.LastMouseY), overlay.SideBelow, screen, id)
}

func (m *OS) renderTooltip() *lipgloss.Layer {
	if m.Tooltip.Secondary {
		var layer *lipgloss.Layer
		m.withSecondaryRail(func() bool {
			layer = m.renderTooltipOnRail()
			return true
		})
		return layer
	}
	return m.renderTooltipOnRail()
}

func (m *OS) renderTooltipOnRail() *lipgloss.Layer {
	switch m.Tooltip.Source {
	case tooltipRailStrip:
		return m.renderRailTooltip()
	case tooltipRailAdd:
		return m.renderRailAddTooltip()
	case tooltipRailHost:
		return m.renderRailHostTooltip()
	case tooltipDockSession:
		return m.renderDockSessionTooltip()
	case tooltipDockWorkspace:
		return m.renderDockWorkspaceTooltip()
	case tooltipDockNotif:
		return m.renderDockNotifTooltip()
	case tooltipDockButton:
		return m.renderDockButtonTooltip()
	}
	return nil
}
