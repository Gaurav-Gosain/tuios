package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

type tmlPlacedMsg struct {
	window string
	popup  bool
	err    error
}

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
	nonce, ok := m.tmlNonce()
	if !ok {
		return nil
	}
	call := m.tmlCaller()
	id, wid := v.id, w.ID
	return func() tea.Msg {
		_, err := call("tml-place", map[string]any{"surface": id, "target": target, "human_nonce": nonce}, tmlInputTimeout)
		return tmlPlacedMsg{window: wid, popup: popup, err: err}
	}
}

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
