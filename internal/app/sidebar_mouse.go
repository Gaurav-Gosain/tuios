package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// ToggleSidebar flips the sidebar on or off live, re-tiles or re-clamps the
// windows into the changed content region, and records the new state on the
// loaded config so a later save keeps it. This is what the palette entry and the
// keybind both call.
func (m *OS) ToggleSidebar() {
	config.SidebarEnabled = !config.SidebarEnabled
	if m.UserConfig != nil {
		v := config.SidebarEnabled
		m.UserConfig.Appearance.SidebarEnabled = &v
	}
	m.SidebarScroll = 0
	if m.AutoTiling {
		m.TileAllWindows()
	} else {
		m.ClampWindowsToView()
	}
}

// SidebarActive reports whether the sidebar reserves any columns this frame, so
// the mouse handlers know to test it before the window layer.
func (m *OS) SidebarActive() bool {
	return m.GetSidebarWidth() > 0
}

// SidebarBandContains reports whether the absolute cell (x, y) falls inside the
// sidebar's reserved column band. A click or wheel anywhere in the band is the
// sidebar's, even on a blank row, so it never leaks to the pane the sidebar sits
// in front of.
func (m *OS) SidebarBandContains(x, y int) bool {
	w := m.GetSidebarWidth()
	if w <= 0 {
		return false
	}
	topMargin := m.GetTopMargin()
	if y < topMargin || y >= topMargin+m.GetUsableHeight() {
		return false
	}
	sidebarX := 0
	if config.SidebarPosition == "right" {
		sidebarX = m.GetRenderWidth() - w
	}
	return x >= sidebarX && x < sidebarX+w
}

// sidebarRowAt returns the recorded row hit at absolute (x, y), if any.
func (m *OS) sidebarRowAt(x, y int) (sidebarRowHit, bool) {
	for _, h := range m.SidebarHits {
		if h.Contains(x, y) {
			return h, true
		}
	}
	return sidebarRowHit{}, false
}

// SidebarClick routes a click at absolute (x, y) to the sidebar. It returns
// whether the event was consumed (any click in the band is), so the caller stops
// before the click can reach a pane underneath.
//
//   - Window row, left click: focus that window, switching session first when the
//     window belongs to another session.
//   - Session row, left click: switch to it, or toggle its expand/collapse when it
//     is already the current session.
//   - Right click on any row: open the context menu (pane menu for a window, the
//     desktop menu for a session).
func (m *OS) SidebarClick(x, y int, right bool) bool {
	if !m.SidebarBandContains(x, y) {
		return false
	}

	hit, ok := m.sidebarRowAt(x, y)
	if !ok {
		return true // consumed a click on a blank sidebar row
	}

	if right {
		m.openSidebarContextMenu(hit, x, y)
		return true
	}

	switch hit.Kind {
	case sidebarRowWindow:
		m.sidebarFocusWindow(hit)
	case sidebarRowSession:
		if hit.SessionID == m.sidebarCurrentSessionID() {
			m.sidebarToggleCollapse(hit.SessionID)
		} else {
			m.sidebarSwitchSession(hit.SessionID)
		}
	}
	return true
}

// SidebarMotion tracks the pointer over the sidebar band so the row under the
// cursor is highlighted, the way overlay rows track hover. It returns whether
// the motion was consumed (any motion inside the band is, so it never reaches
// the pane the sidebar sits in front of). Motion outside the band clears the
// hover so no stale highlight lingers.
func (m *OS) SidebarMotion(x, y int) bool {
	if !m.SidebarBandContains(x, y) {
		m.SidebarHoverActive = false
		return false
	}
	m.SidebarHoverActive = true
	m.SidebarHoverX, m.SidebarHoverY = x, y
	return true
}

// SidebarWheel scrolls the sidebar list when the cursor is over the band, and
// reports whether it consumed the event. The list scrolls, never the pane under
// it.
func (m *OS) SidebarWheel(x, y int, up bool) bool {
	if !m.SidebarBandContains(x, y) {
		return false
	}
	if up {
		m.SidebarScroll = max(m.SidebarScroll-config.ScrollLines, 0)
	} else {
		// The upper bound is clamped against the row count on the next render.
		m.SidebarScroll += config.ScrollLines
	}
	return true
}

// sidebarCurrentSessionID is the session this client is attached to, matching the
// name BuildSessionTree marks IsCurrent.
func (m *OS) sidebarCurrentSessionID() string {
	if m.SessionName == "" {
		return "local"
	}
	return m.SessionName
}

// sidebarToggleCollapse flips a session's expand/collapse state in the sidebar.
func (m *OS) sidebarToggleCollapse(sessionID string) {
	if m.SidebarCollapsed == nil {
		m.SidebarCollapsed = make(map[string]bool)
	}
	// Default expanded state is IsCurrent; storing the negation of the current
	// shown state toggles it regardless of whether an entry already exists.
	shown := true
	if v, ok := m.SidebarCollapsed[sessionID]; ok {
		shown = !v
	}
	m.SidebarCollapsed[sessionID] = shown // collapsed = shown was true
}

// sidebarSwitchSession switches to another session from a sidebar click.
func (m *OS) sidebarSwitchSession(sessionID string) {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		return
	}
	if err := m.SwitchToSession(sessionID); err != nil {
		m.ShowNotification("Switch failed: "+err.Error(), "error", config.NotificationDuration*2)
	}
}

// sidebarFocusWindow focuses the window a window row points at, switching session
// first when it lives in another session.
func (m *OS) sidebarFocusWindow(hit sidebarRowHit) {
	if hit.WindowIndex >= 0 && hit.WindowIndex < len(m.Windows) {
		m.FocusWindow(hit.WindowIndex)
		return
	}
	// Window of another session: switch first, then focus by ID.
	if hit.SessionID != "" && hit.SessionID != m.sidebarCurrentSessionID() {
		if err := m.SwitchToSession(hit.SessionID); err != nil {
			m.ShowNotification("Switch failed: "+err.Error(), "error", config.NotificationDuration*2)
			return
		}
	}
	if idx := m.windowIndexByID(hit.WindowID); idx >= 0 {
		m.FocusWindow(idx)
	}
}

// openSidebarContextMenu opens the context menu for a sidebar row, reusing the
// existing menu builders (contextmenu_build.go): the pane menu for a window row
// (after focusing it), the desktop menu for a session row.
func (m *OS) openSidebarContextMenu(hit sidebarRowHit, x, y int) {
	cm := &ContextMenu{
		AnchorX:  x,
		AnchorY:  y,
		Selected: -1,
		ItemH:    1,
	}

	switch hit.Kind {
	case sidebarRowWindow:
		m.sidebarFocusWindow(hit)
		cm.Target = CtxTargetPane
		cm.WindowIndex = m.FocusedWindow
		cm.Title, cm.Items = m.paneMenu(m.FocusedWindow)
	default:
		cm.Target = CtxTargetDesktop
		cm.WindowIndex = -1
		if m.IsDaemonSession {
			// A session row gets the session lifecycle menu: the same rows the
			// quit menu offers, anchored where the user right-clicked.
			cm.Title, cm.Items = m.sessionMenu()
		} else {
			cm.Title, cm.Items = m.desktopMenu()
		}
	}

	cm.Selected = cm.Next(1)
	m.ContextMenu = cm
}
