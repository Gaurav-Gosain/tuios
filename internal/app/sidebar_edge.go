package app

import "github.com/Gaurav-Gosain/tuios/internal/config"

// sidebarEdge identifies the screen edge a rail is drawn on. Keeping the edge
// explicit in the geometry prevents a second rail from inheriting the legacy
// rail's position merely because a caller read Settings.SidebarPosition.
type sidebarEdge uint8

const (
	sidebarLeft sidebarEdge = iota
	sidebarRight
)

// legacySidebarEdge is the position of the existing rail. A hidden rail still
// has a position for its controls when it is revealed for keyboard focus.
func (m *OS) legacySidebarEdge() sidebarEdge {
	if m.Settings.SidebarPosition == "right" {
		return sidebarRight
	}
	return sidebarLeft
}

// railEdge is the edge of the rail currently drawn or handled. The secondary
// rail temporarily swaps its view state, but does not change the legacy config.
func (m *OS) railEdge() sidebarEdge {
	if m.sidebarDrawing {
		return m.sidebarDrawingEdge
	}
	return m.legacySidebarEdge()
}

// sidebarEdgeX locates the first column of a rail of width w on an edge.
// Both drawing and pointer geometry use it so they cannot disagree about
// which side of the screen owns the reserved columns.
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

// withSecondaryRail scopes a pointer action to the opposite edge's state.
// Restore the original state before returning to the update loop, including
// when the gesture did not change a row.
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
	oldSections := m.Settings.SidebarSections
	m.swapSecondaryRail()
	m.Settings.SidebarSections = sections
	m.sidebarDrawing = true
	m.sidebarDrawingEdge = edge
	m.sidebarDrawingWidth = m.secondarySidebarWidth()
	defer func() {
		m.sidebarDrawing = false
		m.Settings.SidebarSections = oldSections
		m.swapSecondaryRail()
	}()
	return action()
}
