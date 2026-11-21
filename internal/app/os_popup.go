package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func (m *OS) popupRect(w *terminal.Window) (x, y, width, height int) {
	topMargin := m.PaneTop()
	leftMargin := m.PaneLeft()
	contentWidth := m.PaneWidth()
	contentHeight := m.PaneHeight()

	width = session.ResolvePopupSize(w.PopupWidth, session.PopupDefaultWidth, contentWidth, session.PopupMinWidth)
	height = session.ResolvePopupSize(w.PopupHeight, session.PopupDefaultHeight, contentHeight, session.PopupMinHeight)

	x = leftMargin + (contentWidth-width)/2
	y = topMargin + (contentHeight-height)/2
	if _, _, inScratch := m.scratchRegion(); !inScratch {
		if sw := m.GetRenderWidth(); sw > 0 {
			x = clampInt((sw-width)/2, leftMargin, leftMargin+contentWidth-width)
		}
		if sh := m.GetRenderHeight(); sh > 0 {
			y = clampInt((sh-height)/2, topMargin, topMargin+contentHeight-height)
		}
	}
	return x, y, width, height
}

func (m *OS) applyPopupRect(w *terminal.Window, deferring bool) {
	m.CancelSnapAnimation(w)
	x, y, width, height := m.popupRect(w)
	if w.X == x && w.Y == y && w.Width == width && w.Height == height {
		return
	}
	m.placePaneAt(w, x, y, width, height, deferring)
}

func (m *OS) applyPopupRects(deferring bool) {
	for _, w := range m.Windows {
		if w == nil || !w.IsPopup || w.Minimized {
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
			m.CloseWindowByHand(i)
			return true
		}
	}
	return false
}

// CloseWindowByHand is what a close key, the close button and the palette's
// close do to the pane at i. A pane of a scratch group closes like any pane.
// When it was the group's last one, the group ends and the view goes back to
// the workspace it was shown over.
func (m *OS) CloseWindowByHand(i int) {
	if i < 0 || i >= len(m.Windows) {
		return
	}
	m.DeleteWindow(i)
	m.leaveEmptyScratchView()
}
