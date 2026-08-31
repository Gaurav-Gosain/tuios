package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// A popup is a floating pane that runs one command and closes when the command
// exits. The daemon opens it and marks it; this file is the client's half, which
// is the box.
//
// The box is computed here rather than adopted from the sync, the way the zoom
// box is and for the same reason: a popup is centred in the content region, and
// the content region is this client's own size less the reserve the session
// agreed. A peer of another size that adopted the rectangle would draw the popup
// in a corner or off the edge, and would hand the shared shell a second size.
// So the size the caller asked for travels and every client resolves it against
// its own bounds.

// popupRect is the box a popup fills on this client: the size the caller asked
// for, centred in the content region.
//
// The content region is the box the session agreed the panes go in, beside a
// reserved sidebar band and clear of the dock rows, and it is the same box
// zoomRect measures against, so a popup can never be drawn over the chrome.
//
// A request wider or taller than the region gives up its space rather than the
// region: it is cut down to the region instead of overhanging it. That is the
// tiler's rule (internal/layout.spans) applied to a size a person typed.
func (m *OS) popupRect(w *terminal.Window) (x, y, width, height int) {
	topMargin := m.GetTopMargin()
	leftMargin := m.GetLeftMargin()
	contentWidth := m.GetContentWidth()
	contentHeight := m.GetUsableHeight()

	width = session.ResolvePopupSize(w.PopupWidth, session.PopupDefaultWidth, contentWidth, session.PopupMinWidth)
	height = session.ResolvePopupSize(w.PopupHeight, session.PopupDefaultHeight, contentHeight, session.PopupMinHeight)

	x = leftMargin + (contentWidth-width)/2
	y = topMargin + (contentHeight-height)/2
	return x, y, width, height
}

// applyPopupRect puts one popup in this client's popup box and tells its guest,
// through the shared path so the daemon-hosted PTY is told its new size too.
// It is idempotent, so it can be run on any pass that might have moved the box.
//
// deferring is the tiler's own answer to resizeDeferralActive, threaded through
// the way applyZoomRect takes it: while the pointer is dragging an edge the box
// is given visually and the real announcement waits for the release.
func (m *OS) applyPopupRect(w *terminal.Window, deferring bool) {
	// A snap still in flight owns this pane's rectangle and would stamp its own
	// back on the next tick, so it is retired first. The same thing every other
	// placer does before it sets a box.
	m.CancelSnapAnimation(w)
	x, y, width, height := m.popupRect(w)
	if w.X == x && w.Y == y && w.Width == width && w.Height == height {
		return
	}
	w.X, w.Y = x, y
	w.InvalidateCache()
	if deferring {
		w.ResizeVisual(width, height)
		m.PendingResizes[w.ID] = [2]int{width, height}
		w.MarkPositionDirty()
		return
	}
	w.Resize(width, height)
}

// applyPopupRects re-centres every popup on the current workspace.
//
// It runs unconditionally rather than only when a popup arrives, because the box
// also moves when this client resizes, when the sidebar band changes width and
// when the session's reserve is renegotiated. A retile is exactly when that has
// happened, so this is called from the same place applyZoomRect is.
func (m *OS) applyPopupRects(deferring bool) {
	for _, w := range m.Windows {
		if w == nil || !w.IsPopup || w.Minimized || w.Minimizing {
			continue
		}
		if w.Workspace != m.CurrentWorkspace {
			continue
		}
		m.applyPopupRect(w, deferring)
	}
}

// FocusedPopup returns the popup that holds the focus, or nil. It asks the
// focused window rather than the workspace because closing a popup is an act on
// the pane the user is looking at, not on whichever popup happens to be open.
func (m *OS) FocusedPopup() *terminal.Window {
	w := m.GetFocusedWindow()
	if w == nil || !w.IsPopup {
		return nil
	}
	return w
}

// CloseFocusedPopup closes the focused popup and reports whether it closed one.
//
// It is the keyboard's way out of a popup whose command does not exit on its
// own, and it goes through DeleteWindow so a popup is closed by exactly the path
// every other pane is closed by.
func (m *OS) CloseFocusedPopup() bool {
	w := m.FocusedPopup()
	if w == nil {
		return false
	}
	for i := range m.Windows {
		if m.Windows[i] == w {
			m.DeleteWindow(i)
			return true
		}
	}
	return false
}
