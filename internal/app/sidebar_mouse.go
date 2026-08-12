package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// sidebarDragState is the click-or-drag gesture on a session row. A left press
// arms it (PressActive); vertical motion past the press row turns it into a
// reorder drag (Dragging) whose draft Order is displayed live; the release
// either commits the draft or, when the pointer never left the row, performs
// the plain click (switch or toggle).
type sidebarDragState struct {
	PressActive bool
	SessionID   string
	PressX      int
	PressY      int
	Dragging    bool
	Order       []string
}

// sidebarEdgeState is the width-resize gesture on the rail's edge rule. A left
// press on that one-cell column arms it; motion sets the width to the pointer
// column; release persists. It is disjoint from the session reorder drag: the
// edge column belongs to the rail's frame, not to any row.
type sidebarEdgeState struct {
	Active bool
}

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
	m.SidebarScrollS, m.SidebarScrollT, m.SidebarScrollA = 0, 0, 0
	m.sidebarClearPeek()
	m.sidebarTooltipClear()
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

// sidebarClearPeek drops any live preview and the pair rule's arm with it. It
// is called from every path that makes the preview a lie: attaching, leaving
// the band or the rail scope, and hiding the rail.
func (m *OS) sidebarClearPeek() {
	m.SidebarPeek, m.SidebarPeekArm = "", ""
}

// sidebarPeekAt resolves one pointer position against the pair rule and commits
// or clears the preview. A motion event on a non-attached session row peeks
// when the previous event resolved to the same row (so a slow browse peeks row
// by row) or to no session row at all (so entering sideways from the pane area
// peeks instantly); a fast sweep delivers one event per row, forms no pair, and
// commits nothing. Snap-back needs no pair: the first event landing anywhere
// else clears the preview, which is why the pointer can never reach a peeked
// row while the peek is still on screen.
func (m *OS) sidebarPeekAt(x, y int) {
	row := ""
	if hit, ok := m.sidebarRowAt(x, y); ok && hit.Kind == sidebarRowSession {
		row = hit.SessionID
	}
	if row == "" || row == m.sidebarCurrentSessionID() {
		m.SidebarPeek, m.SidebarPeekArm = "", row
		return
	}
	if m.SidebarPeekArm == row || m.SidebarPeekArm == "" {
		m.SidebarPeek = row
	}
	m.SidebarPeekArm = row
}

// SidebarClick routes a left or right press at absolute (x, y) to the sidebar.
// It returns whether the event was consumed (any press in the band is), so the
// caller stops before the press can reach a pane underneath.
//
//   - Terminal or agent row, left press: focus that window, switching session
//     first when the window belongs to another session.
//   - Session row, left press: arm the click-or-drag gesture; the release
//     attaches to that session, a vertical drag reorders the session list.
//   - Footer control, left press: make a session, or step the rail's width.
//   - Right press on any row: open the context menu (pane menu for a window or
//     agent row, the session/desktop menu for a session row).
func (m *OS) SidebarClick(x, y int, right bool) bool {
	if !m.SidebarBandContains(x, y) {
		return false
	}
	// Any press takes the label down: it is a hover readout, and leaving it up
	// over whatever the press opened is how a tooltip becomes litter.
	m.sidebarTooltipClear()

	// The edge rule is the rail's frame, so a left press on it arms the width
	// resize before any row routing: the column belongs to the sidebar, not to
	// the session row whose hit rectangle spans it.
	if !right && m.sidebarOnEdge(x) {
		m.SidebarEdge = sidebarEdgeState{Active: true}
		return true
	}

	hit, ok := m.sidebarRowAt(x, y)
	if !ok {
		if right {
			m.openRailSettingsMenu(x, y)
		}
		return true // consumed a click on a blank sidebar row
	}

	if right {
		m.openSidebarContextMenu(hit, x, y)
		return true
	}

	// A click inside the rail while it holds keyboard focus keeps the focus and
	// moves the cursor to the clicked row: mouse and keyboard share one cursor.
	if m.SidebarFocused {
		m.sidebarSetCursorToHit(hit)
	}

	switch hit.Kind {
	case sidebarRowWindow, sidebarRowAgent:
		m.sidebarFocusWindow(hit)
	case sidebarRowAgentFilter:
		m.SidebarCycleAgentsFilter()
	case sidebarRowAgentSort:
		m.SidebarCycleAgentsSort()
	case sidebarRowNewSession:
		m.SidebarNewSession()
	case sidebarRowCollapse:
		m.SidebarToggleCollapsed()
	case sidebarRowSession:
		m.SidebarDrag = sidebarDragState{
			PressActive: true,
			SessionID:   hit.SessionID,
			PressX:      x,
			PressY:      y,
		}
	}
	return true
}

// SidebarDragActive reports whether a session-row press or drag is in
// progress, so the motion and release handlers route to the sidebar first.
func (m *OS) SidebarDragActive() bool {
	return m.SidebarDrag.PressActive || m.SidebarDrag.Dragging
}

// sidebarOnEdge reports whether column x is the rail's edge rule, the one-cell
// hairline facing the panes: the last band column for a left rail, the first
// for a right one.
func (m *OS) sidebarOnEdge(x int) bool {
	w := m.GetSidebarWidth()
	if w <= 0 {
		return false
	}
	if config.SidebarPosition == "right" {
		return x == m.GetRenderWidth()-w
	}
	return x == w-1
}

// SidebarEdgeActive reports whether a width-resize gesture is in progress, so
// the motion and release handlers route to the sidebar first.
func (m *OS) SidebarEdgeActive() bool {
	return m.SidebarEdge.Active
}

// sidebarWidthBounds returns the clamp range for the rail's full width: no
// narrower than the glyph rail, no wider than about two fifths of the screen so
// the panes always keep the larger share.
func (m *OS) sidebarWidthBounds() (int, int) {
	lo := config.SidebarGlyphWidth
	hi := max(m.GetRenderWidth()*2/5, lo)
	return lo, hi
}

// SidebarEdgeMotion sets the rail width to the pointer column and re-lays the
// panes into the changed content region, exactly as ToggleSidebar does. The
// width comes from the pointer's distance from the far edge, so the hairline
// tracks the cursor.
func (m *OS) SidebarEdgeMotion(x, y int) bool {
	if !m.SidebarEdge.Active {
		return false
	}
	var w int
	if config.SidebarPosition == "right" {
		w = m.GetRenderWidth() - x
	} else {
		w = x + 1
	}
	lo, hi := m.sidebarWidthBounds()
	w = max(min(w, hi), lo)
	// Dragging the edge out of the strip is an expand: the gesture asks for a
	// width, and a collapsed rail that ignored it would look broken.
	collapsed := m.SidebarCollapsed && w <= config.SidebarGlyphWidth
	if w == config.SidebarWidth && collapsed == m.SidebarCollapsed {
		return true
	}
	m.SidebarCollapsed = collapsed
	config.SidebarWidth = w
	if m.AutoTiling {
		m.TileAllWindows()
	} else {
		m.ClampWindowsToView()
	}
	return true
}

// SidebarEdgeRelease ends the resize and persists the new width beside the
// order and collapse state.
func (m *OS) SidebarEdgeRelease(x, y int) bool {
	if !m.SidebarEdge.Active {
		return false
	}
	m.SidebarEdge = sidebarEdgeState{}
	m.saveSidebarState()
	return true
}

// SidebarDragMotion advances the click-or-drag gesture. The first vertical
// step past the press row commits the gesture to a reorder drag; from then on
// the dragged session follows the row under the pointer in a draft order that
// the render displays live.
func (m *OS) SidebarDragMotion(x, y int) bool {
	d := &m.SidebarDrag
	if !d.PressActive && !d.Dragging {
		return false
	}
	if !d.Dragging {
		if y == d.PressY {
			return true // horizontal jitter is still a click
		}
		d.Dragging = true
		d.Order = append([]string(nil), m.SidebarSessionIDs...)
	}

	targetID := m.sidebarSessionRowIDAt(y)
	if targetID == "" || targetID == d.SessionID {
		return true
	}
	from, target := -1, -1
	for i, id := range d.Order {
		if id == d.SessionID {
			from = i
		}
		if id == targetID {
			target = i
		}
	}
	if from < 0 || target < 0 || target == from {
		return true
	}
	id := d.Order[from]
	d.Order = append(d.Order[:from], d.Order[from+1:]...)
	// target was read before the removal, so after it the dragged session
	// lands past the target row when moving down and before it when moving
	// up: the rows swap as the pointer crosses them.
	at := min(target, len(d.Order))
	d.Order = append(d.Order[:at], append([]string{id}, d.Order[at:]...)...)
	return true
}

// sidebarSessionRowIDAt maps a screen row to the session row on it: the last
// visible session row starting at or above y, so a pointer on a window row
// answers with the session it belongs to, and a drag past the bottom lands on
// the last row. Above the first visible session row it clamps to that row.
// Returns "" when no session rows are on screen at all.
func (m *OS) sidebarSessionRowIDAt(y int) string {
	id, first := "", ""
	for _, h := range m.SidebarHits {
		if h.Kind != sidebarRowSession {
			continue
		}
		if first == "" {
			first = h.SessionID
		}
		if y >= h.Y0 {
			id = h.SessionID
		}
	}
	if id == "" {
		return first
	}
	return id
}

// SidebarRelease finishes the click-or-drag gesture: a drag commits its draft
// order and persists it; a plain release on the pressed row attaches to that
// session. One gesture, one meaning: a release on the session already attached
// is simply nothing to do.
func (m *OS) SidebarRelease(x, y int) bool {
	d := m.SidebarDrag
	if !d.PressActive && !d.Dragging {
		return false
	}
	m.SidebarDrag = sidebarDragState{}

	if d.Dragging {
		m.SidebarOrder = d.Order
		m.saveSidebarState()
		return true
	}

	hit, ok := m.sidebarRowAt(x, y)
	if !ok || hit.Kind != sidebarRowSession || hit.SessionID != d.SessionID {
		return true // the pointer left the row; the click is void
	}
	m.sidebarSwitchSession(hit.SessionID)
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
		// The one out-of-band event the motion whitelist keeps flowing is what
		// clears the stale highlight; the preview and the label leave with it.
		m.sidebarClearPeek()
		m.sidebarTooltipClear()
		return false
	}
	m.SidebarHoverActive = true
	m.SidebarHoverX, m.SidebarHoverY = x, y
	m.sidebarPeekAt(x, y)
	m.sidebarTooltipTrack(y)
	return true
}

// SidebarWheel scrolls the section under the pointer and reports whether it
// consumed the event. Per-section rather than rail-wide: one offset over the
// whole rail unpins the headers and can scroll the agents section, the alarm,
// off the screen entirely.
func (m *OS) SidebarWheel(x, y int, up bool) bool {
	if !m.SidebarBandContains(x, y) {
		return false
	}
	offsets := [sidebarSectionCount]*int{&m.SidebarScrollS, &m.SidebarScrollT, &m.SidebarScrollA}
	for s, band := range m.sidebarSectionY {
		if y < band[0] || y >= band[1] {
			continue
		}
		if up {
			*offsets[s] = max(*offsets[s]-config.ScrollLines, 0)
		} else {
			// The upper bound is clamped against the row count on the next render.
			*offsets[s] += config.ScrollLines
		}
		break
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

// sidebarSwitchSession attaches to another session from a sidebar click or key.
// Attaching makes any preview the truth, so it takes the preview down.
func (m *OS) sidebarSwitchSession(sessionID string) {
	m.sidebarClearPeek()
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
	// Resolve by ID, never by the index the row was drawn with. A pane closing
	// between that render and this click shifts every later index, so the index
	// alone could focus a different pane than the row names, and the context menu
	// built on top of it would then offer to close that one instead.
	if hit.WindowID != "" {
		if idx := m.windowIndexByID(hit.WindowID); idx >= 0 {
			m.FocusWindow(idx)
			return
		}
	}
	// WindowIndex still answers for a row with no ID to match on.
	if hit.WindowID == "" && hit.WindowIndex >= 0 && hit.WindowIndex < len(m.Windows) {
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
// existing menu builders (contextmenu_build.go): the pane menu for a window or
// agent row (after focusing it), the desktop menu for a session row.
func (m *OS) openSidebarContextMenu(hit sidebarRowHit, x, y int) {
	cm := &ContextMenu{
		AnchorX:  x,
		AnchorY:  y,
		Selected: -1,
		ItemH:    1,
	}

	switch hit.Kind {
	case sidebarRowWindow, sidebarRowAgent:
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

	// The rail's own settings ride under whatever the row offers, so the tab is
	// reachable from the thing it configures without a gear of its own.
	cm.Items = append(cm.Items, separator(), m.railSettingsItem())

	cm.Selected = cm.Next(1)
	m.ContextMenu = cm
}

// openRailSettingsMenu is the menu for a right-click on blank rail: there is no
// row to act on, so the only thing the click can mean is the rail itself.
func (m *OS) openRailSettingsMenu(x, y int) {
	m.ContextMenu = &ContextMenu{
		AnchorX:     x,
		AnchorY:     y,
		Selected:    0,
		ItemH:       1,
		Target:      CtxTargetDesktop,
		WindowIndex: -1,
		Title:       "Sidebar",
		Items:       []ContextMenuItem{m.railSettingsItem()},
	}
}

// railSettingsItem is the row that deep-links to the settings overlay's Sidebar
// tab, shared by both rail menus.
func (m *OS) railSettingsItem() ContextMenuItem {
	return m.item(glyphSettings, "Sidebar settings", "settings_sidebar", false)
}
