package app

import (
	"fmt"

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
	m.sidebarSetCursor(m.sidebarCurrentSessionNavIndex())
}

// sidebarSetCursor moves the keyboard cursor and re-derives the preview from
// where it landed, so a browse down the sessions section previews row by row
// exactly as hovering it does.
func (m *OS) sidebarSetCursor(i int) {
	m.SidebarCursor = i
	row, ok := m.sidebarCursorRow()
	if !ok || row.Kind != sidebarRowSession || row.SessionID == m.sidebarCurrentSessionID() {
		m.sidebarClearPeek()
		return
	}
	m.SidebarPeek = row.SessionID
}

// ExitSidebarFocus returns the keyboard to the panes. A sidebar revealed only to
// host the scope is hidden again, matching how it was found.
func (m *OS) ExitSidebarFocus() {
	if !m.SidebarFocused {
		return
	}
	m.SidebarFocused = false
	m.sidebarClearPeek()
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
	m.sidebarSetCursor(max(min(m.SidebarCursor+delta, len(m.SidebarNav)-1), 0))
}

// SidebarCursorFirst and SidebarCursorLast jump to the ends of the rail.
func (m *OS) SidebarCursorFirst() { m.sidebarSetCursor(0) }
func (m *OS) SidebarCursorLast() {
	if n := len(m.SidebarNav); n > 0 {
		m.sidebarSetCursor(n - 1)
	}
}

// SidebarCursorExpand and SidebarCursorCollapse step the cursor one section
// down and up. With the tree gone there is nothing left to expand, and the keys
// that walked its depth are the natural pair for walking the sections. The
// config action names stay, so a config file written before this still resolves
// both keys.
func (m *OS) SidebarCursorExpand()   { m.sidebarStepSection(1) }
func (m *OS) SidebarCursorCollapse() { m.sidebarStepSection(-1) }

// sidebarStepSection moves the cursor to the first row of the next (delta 1) or
// previous (delta -1) section that has rows, stopping at the ends.
func (m *OS) sidebarStepSection(delta int) {
	order := [sidebarSectionCount]sidebarRowKind{sidebarRowSession, sidebarRowWindow, sidebarRowAgent}
	here := 0
	if row, ok := m.sidebarCursorRow(); ok {
		for i, k := range order {
			if row.Kind == k {
				here = i
			}
		}
	}
	for i := here + delta; i >= 0 && i < len(order); i += delta {
		if j := m.sidebarFirstRowOfKind(order[i]); j >= 0 {
			m.sidebarSetCursor(j)
			return
		}
	}
}

// sidebarFirstRowOfKind is the index of the first nav row of a kind, or -1.
func (m *OS) sidebarFirstRowOfKind(kind sidebarRowKind) int {
	for i, r := range m.SidebarNav {
		if r.Kind == kind {
			return i
		}
	}
	return -1
}

// SidebarActivateCursor is the keyboard's enter: it runs exactly what a click on
// the cursor row would (attach to a session, focus a pane, work a footer
// control), reusing the mouse handlers so the two never diverge. It
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
	case sidebarRowAgentFilter:
		m.SidebarCycleAgentsFilter()
	case sidebarRowAgentSort:
		m.SidebarCycleAgentsSort()
	case sidebarRowNewSession:
		m.SidebarNewSession()
	case sidebarRowCollapse:
		m.SidebarToggleCollapsed()
	case sidebarRowSession:
		m.sidebarSwitchSession(row.SessionID)
		m.sidebarFollowSession = row.SessionID
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

// SidebarCycleSection walks the cursor forward through the rail's three
// sections, wrapping at the end and skipping any that has no rows.
func (m *OS) SidebarCycleSection() {
	order := [sidebarSectionCount]sidebarRowKind{sidebarRowSession, sidebarRowWindow, sidebarRowAgent}
	here := 0
	if row, ok := m.sidebarCursorRow(); ok {
		for i, k := range order {
			if row.Kind == k {
				here = i
			}
		}
	}
	for step := 1; step <= len(order); step++ {
		if j := m.sidebarFirstRowOfKind(order[(here+step)%len(order)]); j >= 0 {
			m.sidebarSetCursor(j)
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
			m.sidebarSetCursor(i)
			m.sidebarSwitchSession(r.SessionID)
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
		if sidebarNavRowsEqual(navRowOf(h), row) {
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
	target := navRowOf(hit)
	for i, r := range m.SidebarNav {
		if sidebarNavRowsEqual(r, target) {
			// The pointer, not the cursor, owns the preview while the mouse is
			// steering, so this only re-homes the cursor.
			m.SidebarCursor = i
			return
		}
	}
}

// navRowOf is the nav row a hit rectangle points at: the same identity, minus
// the geometry. It is what keeps the drawn rows, the hit rects, and the cursor
// addressing one target set rather than three hand-matched copies.
func navRowOf(h sidebarRowHit) sidebarNavRow {
	return sidebarNavRow{
		Kind:        h.Kind,
		SessionID:   h.SessionID,
		WindowID:    h.WindowID,
		WindowIndex: h.WindowIndex,
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

// SidebarRenameCursor starts a rename on the cursor row, whatever it is. A
// window is renamed locally; a session row renames the session's label through
// the daemon, which owns it. The session's identity is never touched.
func (m *OS) SidebarRenameCursor() {
	if w := m.sidebarCursorWindow(); w != nil {
		m.BeginRenameWindow(w)
		return
	}
	row, ok := m.sidebarCursorRow()
	if ok && row.Kind == sidebarRowSession && row.SessionID != "" && m.DaemonClient != nil {
		m.BeginRenameSession(row.SessionID)
		return
	}
	m.ShowNotification("Nothing on this row to rename", "info", config.NotificationDuration)
}

// SidebarCanCreateSession reports whether this client can make a session at
// all. Standalone has no daemon and so no session list, which is why the rail's
// new-session row and its key are absent there rather than dimmed.
func (m *OS) SidebarCanCreateSession() bool {
	return m.DaemonClient != nil
}

// SidebarToggleCollapsed folds the rail down to its glyph strip, or back out to
// the width the user last sized it to. Two states and one control: the old
// three-stop ladder had a middle width no control named and no way back out of
// except by stepping through it, and the responsive clamp already owns that
// width on the screens where it belongs.
func (m *OS) SidebarToggleCollapsed() { m.SidebarSetCollapsed(!m.SidebarCollapsed) }

// SidebarSetCollapsed is the directed half: the footer's arrow and the keys
// point somewhere, so pressing the same one twice is a no-op rather than a
// flicker.
func (m *OS) SidebarSetCollapsed(collapsed bool) {
	if collapsed == m.SidebarCollapsed {
		return
	}
	// Expanding cannot help on a screen whose breakpoint already pins the rail
	// to the strip; saying so beats appearing to do nothing.
	if !collapsed && sidebarVariant(m.sidebarWidthFor(m.sidebarStoredWidth())) == sidebarVariantGlyph {
		m.ShowNotification("The screen is too narrow for a wider rail", "info", config.NotificationDuration)
		return
	}
	m.SidebarCollapsed = collapsed
	m.sidebarClearPeek()
	m.tooltipClear()
	if m.AutoTiling {
		m.TileAllWindows()
	} else {
		m.ClampWindowsToView()
	}
	m.saveSidebarState()
	m.MarkAllDirty()
}

// SidebarNewSession creates a detached session and switches to it: create and
// go, no prompt. The name matches what `tuios new` would have picked, so the
// two ways in never invent different conventions.
func (m *OS) SidebarNewSession() {
	if !m.SidebarCanCreateSession() {
		m.ShowNotification("Sessions need the daemon", "info", config.NotificationDuration)
		return
	}
	// Creating a session is a daemon round trip, and this runs on the Update
	// goroutine. Doing it inline parked input, rendering and socket draining for
	// as long as the daemon took, made worse by the background session poll
	// holding the client's round-trip lock. Hand it to a goroutine and let the
	// SessionCreatedMsg handler do the switching, which is the part that has to
	// touch OS state.
	name := m.nextSessionName()
	client, ch := m.DaemonClient, m.sessionCreateChan()
	w, h := m.GetContentWidth(), m.GetUsableHeight()
	go func() {
		ch <- SessionCreatedMsg{Name: name, Err: client.CreateDetachedSession(name, w, h)}
	}()
}

// sessionCreateChan is the buffered channel carrying creation results back to
// Update, made on first use so a client that never creates a session pays
// nothing.
func (m *OS) sessionCreateChan() chan SessionCreatedMsg {
	if m.PendingSessionCreate == nil {
		m.PendingSessionCreate = make(chan SessionCreatedMsg, 4)
	}
	return m.PendingSessionCreate
}

// nextSessionName is the first free "session-N", the same scheme the CLI's
// `tuios new` uses.
func (m *OS) nextSessionName() string {
	taken := make(map[string]bool)
	if m.DaemonClient != nil {
		for _, n := range m.DaemonClient.AvailableSessionNames() {
			taken[n] = true
		}
	}
	for i := 0; ; i++ {
		name := fmt.Sprintf("session-%d", i)
		if !taken[name] {
			return name
		}
	}
}

// SidebarAccentCursor opens the colour picker on the cursor row: the pane's own
// accent on a window row, and the session's on a session row, which is the
// colour every pane in it inherits. The key used to refuse on a session row
// because a session had no colour to set.
func (m *OS) SidebarAccentCursor() {
	if w := m.sidebarCursorWindow(); w != nil {
		m.OpenAccentPicker(w.ID)
		return
	}
	row, ok := m.sidebarCursorRow()
	if ok && row.Kind == sidebarRowSession && row.SessionID != "" {
		m.OpenSessionAccentPicker(row.SessionID)
		return
	}
	m.ShowNotification("Accents work on a pane or a session row", "info", config.NotificationDuration)
}
