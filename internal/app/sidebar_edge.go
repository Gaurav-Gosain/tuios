package app

import "github.com/Gaurav-Gosain/tuios/internal/config"

type sidebarEdge uint8

const (
	sidebarLeft sidebarEdge = iota
	sidebarRight
)

func (m *OS) legacySidebarEdge() sidebarEdge {
	if m.Settings.SidebarPosition == "right" {
		return sidebarRight
	}
	return sidebarLeft
}

func (m *OS) railEdge() sidebarEdge {
	if m.sidebarDrawing {
		return m.sidebarDrawingEdge
	}
	return m.legacySidebarEdge()
}

func sidebarEdgeX(edge sidebarEdge, w, screenWidth int) int {
	if edge == sidebarRight {
		return screenWidth - w
	}
	return 0
}

func (m *OS) secondarySidebarBandContains(x, y int) bool {
	x, y = m.ScreenPoint(x, y)
	w := m.secondarySidebarWidth()
	if w == 0 || y < m.viewReserve().Top || y >= m.viewReserve().Top+m.ViewUsableHeight() {
		return false
	}
	edge := sidebarLeft
	if m.legacySidebarEdge() == sidebarLeft {
		edge = sidebarRight
	}
	left := sidebarEdgeX(edge, w, m.GetRenderWidth())
	return x >= left && x < left+w
}

// SidebarFocusOnSecondary reports which edge owns the keyboard scope.
func (m *OS) SidebarFocusOnSecondary() bool {
	return m.SidebarFocused && m.sidebarFocusSecondary
}

// WithSecondarySidebar scopes a keyboard action to the opposite edge's state.
// The action runs synchronously on the UI goroutine; it must not retain a
// pointer to the swapped presentation fields after it returns.
func (m *OS) WithSecondarySidebar(action func()) {
	m.withSecondaryRail(func() bool { action(); return true })
}

func (m *OS) sidebarRailFocused() bool {
	return m.SidebarFocused && m.sidebarDrawing == m.sidebarFocusSecondary
}

func (m *OS) withSecondaryRail(action func() bool) bool {
	if m.UserConfig == nil || m.secondarySidebarWidth() == 0 {
		return false
	}
	edge := sidebarLeft
	cfg := m.UserConfig.Appearance.Sidebar.Left
	if m.legacySidebarEdge() == sidebarLeft {
		edge, cfg = sidebarRight, m.UserConfig.Appearance.Sidebar.Right
	}
	sections := config.SidebarDefaultSections
	if cfg != nil && cfg.Sections != "" {
		sections = cfg.Sections
	}
	oldSections, oldSubagents := m.Settings.SidebarSections, m.Settings.SidebarSubagents
	m.swapSecondaryRail()
	m.Settings.SidebarSections = sections
	m.Settings.SidebarSubagents = config.ResolveSidebarSubagentView(m.UserConfig.Appearance.Sidebar, cfg)
	m.sidebarDrawing = true
	m.sidebarDrawingEdge = edge
	m.sidebarDrawingWidth = m.secondarySidebarWidth()
	defer func() {
		m.sidebarDrawing = false
		m.Settings.SidebarSections = oldSections
		m.Settings.SidebarSubagents = oldSubagents
		m.swapSecondaryRail()
	}()
	return action()
}
