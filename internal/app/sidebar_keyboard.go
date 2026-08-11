package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// sidebarNavRow is one keyboard-navigable rail row: what the cursor can land on
// and what activating it targets. It mirrors sidebarRowHit without the screen
// rectangle, since the keyboard addresses rows by position in the list rather
// than by pixel. The render populates m.SidebarNav in row order so the cursor
// walks the same rows a click would hit.
type sidebarNavRow struct {
	Kind        sidebarRowKind
	SessionID   string
	WindowID    string
	WindowIndex int
}

// sidebarNavRowsEqual reports whether two nav rows point at the same target, so
// the render can mark the cursor row by identity instead of by a fragile index
// shared across the render and the keyboard handler.
func sidebarNavRowsEqual(a, b sidebarNavRow) bool {
	return a.Kind == b.Kind && a.SessionID == b.SessionID && a.WindowID == b.WindowID
}

// sidebarCursorRow is the nav row the cursor is on, and whether the cursor is
// valid (in range of the rows the last frame recorded).
func (m *OS) sidebarCursorRow() (sidebarNavRow, bool) {
	if m.SidebarCursor < 0 || m.SidebarCursor >= len(m.SidebarNav) {
		return sidebarNavRow{}, false
	}
	return m.SidebarNav[m.SidebarCursor], true
}

// EnterSidebarFocus gives the keyboard to the rail. If the sidebar is off it is
// revealed first (and hidden again on exit), so the scope is reachable even when
// the rail is not already showing. The cursor lands on the current session so
// navigation starts where the eye is.
func (m *OS) EnterSidebarFocus() {
	if m.SidebarFocused {
		return
	}
	if !config.SidebarEnabled {
		m.ToggleSidebar()
		m.SidebarRevealedForFocus = true
	}
	m.SidebarFocused = true
	// Revealing a hidden rail builds its nav rows only on the next render, so
	// sidebarCurrentSessionNavIndex has nothing to match yet and would land the
	// cursor on row 0. Follow the current session by identity so the next render
	// anchors the cursor on it once the rows exist.
	m.sidebarFollowSession = m.sidebarCurrentSessionID()
	m.SidebarCursor = m.sidebarCurrentSessionNavIndex()
}

// ExitSidebarFocus returns the keyboard to the panes. A sidebar revealed only to
// host the scope is hidden again, matching how it was found.
func (m *OS) ExitSidebarFocus() {
	if !m.SidebarFocused {
		return
	}
	m.SidebarFocused = false
	if m.SidebarRevealedForFocus {
		m.SidebarRevealedForFocus = false
		if config.SidebarEnabled {
			m.ToggleSidebar()
		}
	}
}

// sidebarCurrentSessionNavIndex is the cursor position of the attached session's
// row, or 0 when it is not among the rows the last frame recorded.
func (m *OS) sidebarCurrentSessionNavIndex() int {
	cur := m.sidebarCurrentSessionID()
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowSession && r.SessionID == cur {
			return i
		}
	}
	return 0
}

// SidebarCursorMove steps the cursor by delta over the nav rows, clamped to the
// ends (no wrap, matching a scroll that stops at the top and bottom).
func (m *OS) SidebarCursorMove(delta int) {
	if len(m.SidebarNav) == 0 {
		return
	}
	m.SidebarCursor = max(min(m.SidebarCursor+delta, len(m.SidebarNav)-1), 0)
}

// SidebarCursorFirst and SidebarCursorLast jump to the ends of the rail.
func (m *OS) SidebarCursorFirst() { m.SidebarCursor = 0 }
func (m *OS) SidebarCursorLast() {
	if n := len(m.SidebarNav); n > 0 {
		m.SidebarCursor = n - 1
	}
}

// SidebarCursorExpand expands the session under the cursor. On a window or agent
// row it does nothing (the row is already inside its expanded session).
func (m *OS) SidebarCursorExpand() {
	row, ok := m.sidebarCursorRow()
	if !ok || row.Kind != sidebarRowSession {
		return
	}
	if !m.sidebarCollapsedState(row.SessionID) {
		return // already expanded
	}
	m.sidebarToggleCollapse(row.SessionID)
}

// SidebarCursorCollapse collapses the session under the cursor; on a window or
// agent row it moves the cursor up to the parent session row instead, the
// keyboard equivalent of the design's "h jumps to parent".
func (m *OS) SidebarCursorCollapse() {
	row, ok := m.sidebarCursorRow()
	if !ok {
		return
	}
	if row.Kind != sidebarRowSession {
		m.sidebarCursorToSession(row.SessionID)
		return
	}
	if m.sidebarCollapsedState(row.SessionID) {
		return // already collapsed
	}
	m.sidebarToggleCollapse(row.SessionID)
}

// sidebarCollapsedState reports whether a session row is currently collapsed,
// reading the same default (current session expanded, others collapsed) the
// render uses.
func (m *OS) sidebarCollapsedState(sessionID string) bool {
	if m.SidebarCollapsed != nil {
		if v, ok := m.SidebarCollapsed[sessionID]; ok {
			return v
		}
	}
	return sessionID != m.sidebarCurrentSessionID()
}

// sidebarCursorToSession moves the cursor to a session's own row.
func (m *OS) sidebarCursorToSession(sessionID string) {
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowSession && r.SessionID == sessionID {
			m.SidebarCursor = i
			return
		}
	}
}

// SidebarActivateCursor is the keyboard's enter: it runs exactly what a click on
// the cursor row would (switch session, toggle the current session's expansion,
// or focus a window), reusing the mouse handlers so the two never diverge. It
// reports whether activation should leave the rail: focusing a window is a
// request for that pane, so the scope exits; navigating sessions keeps it.
func (m *OS) SidebarActivateCursor() bool {
	row, ok := m.sidebarCursorRow()
	if !ok {
		return false
	}
	switch row.Kind {
	case sidebarRowWindow, sidebarRowAgent:
		m.sidebarFocusWindow(sidebarRowHit{
			Kind:        row.Kind,
			SessionID:   row.SessionID,
			WindowID:    row.WindowID,
			WindowIndex: row.WindowIndex,
		})
		return true
	case sidebarRowSession:
		if row.SessionID == m.sidebarCurrentSessionID() {
			m.sidebarToggleCollapse(row.SessionID)
		} else {
			m.sidebarSwitchSession(row.SessionID)
			m.sidebarFollowSession = row.SessionID
		}
	}
	return false
}

// SidebarReorderCursor moves the cursor's session up or down in the rail order
// and persists it, the keyboard equivalent of a drag-reorder. The cursor rides
// with the moved session so successive presses keep moving the same one.
func (m *OS) SidebarReorderCursor(delta int) {
	row, ok := m.sidebarCursorRow()
	if !ok || row.Kind != sidebarRowSession {
		return
	}
	order := append([]string(nil), m.SidebarSessionIDs...)
	from := -1
	for i, id := range order {
		if id == row.SessionID {
			from = i
			break
		}
	}
	to := from + delta
	if from < 0 || to < 0 || to >= len(order) {
		return
	}
	order[from], order[to] = order[to], order[from]
	m.SidebarOrder = order
	m.saveSidebarState()
	// The rail relays out next frame; follow the moved session so the cursor and
	// its highlight ride to the new slot rather than staying on a fixed index.
	m.sidebarFollowSession = row.SessionID
}

// SidebarCycleSection jumps the cursor between the sessions section and the
// agents section, landing on the first row of the other one. With no agents
// shown it is a no-op.
func (m *OS) SidebarCycleSection() {
	inAgents := false
	if row, ok := m.sidebarCursorRow(); ok {
		inAgents = row.Kind == sidebarRowAgent
	}
	target := sidebarRowAgent
	if inAgents {
		target = sidebarRowSession
	}
	for i, r := range m.SidebarNav {
		if r.Kind == target || (target == sidebarRowSession && r.Kind != sidebarRowAgent) {
			m.SidebarCursor = i
			return
		}
	}
}

// SidebarJumpToSession switches to the n-th session (1-based) in the rail and
// moves the cursor there, mirroring a click on that session row.
func (m *OS) SidebarJumpToSession(n int) {
	count := 0
	for i, r := range m.SidebarNav {
		if r.Kind != sidebarRowSession {
			continue
		}
		count++
		if count == n {
			m.SidebarCursor = i
			if r.SessionID != m.sidebarCurrentSessionID() {
				m.sidebarSwitchSession(r.SessionID)
			}
			return
		}
	}
}

// SidebarOpenCursorMenu opens the context menu for the cursor row, reusing the
// mouse path so the rows are identical. sessionOnly forces the session menu even
// when the cursor sits on a window row, which is what the kill action wants (no
// silent destruction: the menu opens with its Kill rows).
func (m *OS) SidebarOpenCursorMenu(sessionOnly bool) {
	row, ok := m.sidebarCursorRow()
	if !ok {
		return
	}
	hit := sidebarRowHit{
		Kind:        row.Kind,
		SessionID:   row.SessionID,
		WindowID:    row.WindowID,
		WindowIndex: row.WindowIndex,
	}
	if sessionOnly {
		hit.Kind = sidebarRowSession
		hit.WindowIndex = -1
	}
	x, y := m.sidebarCursorAnchor(row)
	m.openSidebarContextMenu(hit, x, y)
}

// sidebarCursorAnchor is where a cursor-opened menu anchors: the top-left of the
// cursor row if it is on screen (it is, since the cursor auto-scrolls into
// view), else the rail's top corner.
func (m *OS) sidebarCursorAnchor(row sidebarNavRow) (int, int) {
	for _, h := range m.SidebarHits {
		if h.Kind == row.Kind && h.SessionID == row.SessionID && h.WindowID == row.WindowID {
			return h.X0, h.Y0
		}
	}
	x := 0
	if config.SidebarPosition == "right" {
		x = m.GetRenderWidth() - m.GetSidebarWidth()
	}
	return x, m.GetTopMargin()
}

// sidebarSetCursorToHit points the keyboard cursor at a clicked row, so a click
// inside the rail while it holds keyboard focus keeps the cursor where the eye
// went (the mouse and keyboard share one cursor).
func (m *OS) sidebarSetCursorToHit(hit sidebarRowHit) {
	for i, r := range m.SidebarNav {
		if r.Kind == hit.Kind && r.SessionID == hit.SessionID && r.WindowID == hit.WindowID {
			m.SidebarCursor = i
			return
		}
	}
}

// sidebarCursorWindow returns the live window the cursor row names, or nil when
// the cursor is on a session row or on a window of a session this client is not
// attached to (whose windows it does not hold).
func (m *OS) sidebarCursorWindow() *terminal.Window {
	row, ok := m.sidebarCursorRow()
	if !ok || row.WindowID == "" {
		return nil
	}
	if i := m.windowIndexByID(row.WindowID); i >= 0 {
		return m.Windows[i]
	}
	return nil
}

// SidebarRenameCursor starts an inline rename on the cursor row. Sessions are
// the daemon's to name, so the rail renames windows only.
func (m *OS) SidebarRenameCursor() {
	w := m.sidebarCursorWindow()
	if w == nil {
		m.ShowNotification("Rename works on a window of this session", "info", config.NotificationDuration)
		return
	}
	m.BeginRenameWindow(w)
}

// SidebarAccentCursor opens the accent swatches for the cursor row's window.
func (m *OS) SidebarAccentCursor() {
	w := m.sidebarCursorWindow()
	if w == nil {
		m.ShowNotification("Accents work on a window of this session", "info", config.NotificationDuration)
		return
	}
	m.OpenAccentPicker(w.ID)
}
