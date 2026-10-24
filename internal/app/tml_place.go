package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Where a TML surface sits is the person's choice once it is open. The agent
// picks a popup or a pane with tml-open; the float toggle (ToggleFloating,
// ctrl+b t f, the palette, and the control in the popup's top border or the
// pane's title bar) moves it to the other. The window and its surface stay,
// so the view, its state and what the person typed in a form go with it.
// The daemon holds the placement (tml-place), so every client and a reattach
// show the same, and a patch or a replace from the agent leaves it alone.

// tmlPlacedMsg is the daemon's answer to a tml-place.
type tmlPlacedMsg struct {
	window string
	popup  bool
	err    error
}

// tmlTogglePlacement asks the daemon to move a TML window to the other
// placement.
func (m *OS) tmlTogglePlacement(w *terminal.Window) tea.Cmd {
	v := m.tmlViewOf(w)
	if v == nil {
		return nil
	}
	popup := !w.IsPopup
	target := session.TMLTargetPane
	if popup {
		target = session.TMLTargetPopup
	}
	call, nonce := m.inboxCaller(), m.inboxNonce()
	id, wid := v.id, w.ID
	return func() tea.Msg {
		_, err := call("tml-place", map[string]any{"surface": id, "target": target, "human_nonce": nonce}, tmlInputTimeout)
		return tmlPlacedMsg{window: wid, popup: popup, err: err}
	}
}

// applyTMLPlaced lays a moved TML window out at once, before the daemon's
// state reaches this client: a popup leaves the layout and takes its box, a
// pane joins the layout. The state that follows says the same.
func (m *OS) applyTMLPlaced(msg tmlPlacedMsg) {
	if msg.err != nil {
		m.ShowNotification("tuios could not move the surface: "+msg.err.Error(), "warning", m.Settings.NotificationDuration)
		return
	}
	w := m.windowByID(msg.window)
	if w == nil {
		return
	}
	if w.IsPopup != msg.popup || w.IsFloating != msg.popup || w.Tiled == msg.popup {
		w.IsPopup, w.IsFloating = msg.popup, msg.popup
		if msg.popup {
			m.liftFromLayout(w)
			m.applyPopupRect(w, false)
		} else {
			m.addToLayout(w)
		}
	}
	for i, win := range m.Windows {
		if win == w {
			m.FocusWindow(i)
		}
	}
	m.markTMLDirty(session.TMLSurfaceID(w.URI))
	m.MarkAllDirty()
	words := "Surface: docked as a pane"
	if msg.popup {
		words = "Surface: floating"
	}
	m.ShowNotification(words, "info", m.Settings.NotificationDuration)
	m.SyncStateToDaemon()
}
