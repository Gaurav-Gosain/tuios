package app

import "github.com/Gaurav-Gosain/tuios/internal/terminal"

// The pointer's side of the link feature: what it is on, and what changes when
// that moves.
//
// The rule for who owns a pointer event over pane content is the one the click
// path already keeps: a pane in terminal mode whose guest asked for mouse
// reporting owns the mouse, and tuios does not draw on top of a program that is
// tracking the cursor itself. Underlining a link a click would forward to vim
// anyway would be a promise tuios does not keep, so the highlight is suppressed
// under exactly the condition the click is forwarded under, and nowhere else.

// LinkHoverActive reports whether the pointer is on a link. The motion filter
// reads it so one event still arrives after the pointer leaves, which is what
// clears the highlight.
func (m *OS) LinkHoverActive() bool { return m.linkHoverOn }

// HoveredLink returns the run under the pointer and whether there is one.
func (m *OS) HoveredLink() (PaneLink, bool) {
	if !m.linkHoverOn {
		return PaneLink{}, false
	}
	return m.linkHover, true
}

// linkHoverFor returns the run to highlight in this window, if any. The render
// loop asks per pane, and only the pane under the pointer ever answers.
func (m *OS) linkHoverFor(window *terminal.Window) (PaneLink, bool) {
	if !m.linkHoverOn || window == nil || m.linkHover.WindowID != window.ID {
		return PaneLink{}, false
	}
	return m.linkHover, true
}

// PointerOverPaneContent reports whether absolute screen (x, y) is inside some
// pane's content box, which is the only place a link can be.
//
// It exists for the motion filter, which is a whitelist and drops every event it
// does not recognise. Link hover is the first hover in tuios that comes from
// what a program printed rather than from where the chrome is, so it cannot be
// answered by a rectangle the renderer recorded: the whole pane is the target.
// This is therefore as narrow as the clause can honestly be, and it is why
// appearance.links = off exists, since that turns the clause off entirely and
// restores the filter to exactly what it dropped before.
func (m *OS) PointerOverPaneContent(x, y int) bool {
	if !linksEnabled() {
		return false
	}
	idx := m.WindowAt(x, y)
	if idx < 0 {
		return false
	}
	_, _, inContent := m.Windows[idx].ScreenToTerminal(x, y)
	return inContent
}

// TrackLinkPointer is what the motion handler calls. It refuses the whole
// question wherever pane content is not what the pointer is really on, and
// clears the run when it does, so a pointer that leaves a link for the rail,
// the dock, an overlay or a gesture takes the underline with it.
//
// It also owns the pointer shape for the run, which is why it runs after
// UpdatePointerForPosition rather than before: the border and corner shapes are
// geometry and win, and the hand is only offered over content that has a link
// under it.
func (m *OS) TrackLinkPointer(x, y int) {
	if m.Dragging || m.Resizing || m.ScrollbarDragging ||
		m.AnyOverlayOpen() || m.ContextMenuActive() ||
		m.SidebarBandContains(x, y) || m.InDockBand(y) {
		m.clearLinkHover()
		return
	}
	if m.LinkHoverAt(x, y) {
		m.SetPointerShape(PointerPointer)
	}
}

// LinkHoverAt resolves what the pointer at absolute screen (x, y) is on and
// records it. It returns whether that is a link.
//
// Called from the motion handler on every move that is not owned by a gesture.
// The cost of a move over ordinary content is one window lookup and one cell
// read; the cost of a move over a link is that plus one row scan. Nothing here
// is reached from a render.
func (m *OS) LinkHoverAt(x, y int) bool {
	if !linksEnabled() {
		return m.clearLinkHover()
	}

	idx := m.WindowAt(x, y)
	if idx < 0 {
		return m.clearLinkHover()
	}
	window := m.Windows[idx]
	if m.guestOwnsPointer(window) {
		return m.clearLinkHover()
	}

	termX, termY, inContent := window.ScreenToTerminal(x, y)
	if !inContent {
		return m.clearLinkHover()
	}

	link, ok := resolvePaneLink(window, termX, termY)
	if !ok {
		return m.clearLinkHover()
	}
	m.setLinkHover(link)
	return true
}

// LinkAt returns the link at absolute screen (x, y), resolved now rather than
// read back from the hover.
//
// A click is answered from the click's own coordinates on purpose. The hover is
// built from motion, and a press can arrive without any: a touch client has no
// pointer to move, and a press one cell away from where the pointer last
// reported would otherwise act on the run it left behind.
//
// It does not apply the guest-owns-the-pointer test that the hover does. The
// caller reaches this only with the modifier held, and holding it is the user
// saying this click is the terminal's rather than the program's.
func (m *OS) LinkAt(x, y int) (PaneLink, bool) {
	if !linksEnabled() {
		return PaneLink{}, false
	}
	idx := m.WindowAt(x, y)
	if idx < 0 {
		return PaneLink{}, false
	}
	window := m.Windows[idx]
	termX, termY, inContent := window.ScreenToTerminal(x, y)
	if !inContent {
		return PaneLink{}, false
	}
	return resolvePaneLink(window, termX, termY)
}

// guestOwnsPointer reports whether the program in this pane is tracking the
// mouse and would receive a click on it. It is the same three-part test the
// click handler applies before forwarding: the pane is focused, tuios is in
// terminal mode, and the guest asked for mouse reporting.
func (m *OS) guestOwnsPointer(window *terminal.Window) bool {
	if window == nil || window.Terminal == nil || m.Mode != TerminalMode {
		return false
	}
	if f := m.GetFocusedWindow(); f == nil || f.ID != window.ID {
		return false
	}
	return window.Terminal.HasMouseMode()
}

// setLinkHover records a new run and repaints the panes that changed. Moving
// from one cell of a link to the next is not a change, so a pointer travelling
// along a link repaints once on arrival and not again.
func (m *OS) setLinkHover(link PaneLink) {
	if m.linkHoverOn && m.linkHover.Same(link) {
		// The run is the same; only the cell the label hangs off moved.
		m.linkHover.Row, m.linkHover.Col = link.Row, link.Col
		return
	}
	prev := m.linkHover.WindowID
	if m.linkHoverOn && prev != link.WindowID {
		m.repaintWindowID(prev)
	}
	m.linkHover = link
	m.linkHoverOn = true
	m.repaintWindowID(link.WindowID)
}

// clearLinkHover drops the run and repaints the pane that was carrying it.
// Returns false so the callers above can end on it.
func (m *OS) clearLinkHover() bool {
	if !m.linkHoverOn {
		return false
	}
	id := m.linkHover.WindowID
	m.linkHover = PaneLink{}
	m.linkHoverOn = false
	m.repaintWindowID(id)
	return false
}

// repaintWindowID marks one pane's content dirty by id, so the compositor drops
// its cached layer and the cell loop runs again with the new highlight.
func (m *OS) repaintWindowID(id string) {
	if id == "" {
		return
	}
	for _, w := range m.Windows {
		if w != nil && w.ID == id {
			w.MarkContentDirty()
			return
		}
	}
}
