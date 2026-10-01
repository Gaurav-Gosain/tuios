package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// NvimNavigationMsg is a pane's request to leave a Neovim split at its edge.
type NvimNavigationMsg struct {
	WindowID  string
	Direction string
	State     *bool
}

func ListenForNvimNavigation(ch chan NvimNavigationMsg) tea.Cmd {
	return listenOnce(ch, func(msg NvimNavigationMsg) tea.Msg { return msg })
}

func (m *OS) ensureNvimNavigationChan() chan NvimNavigationMsg {
	if m.PendingNvimNavigation == nil {
		m.PendingNvimNavigation = make(chan NvimNavigationMsg, 16)
	}
	return m.PendingNvimNavigation
}

func (m *OS) setupNvimNavigation(window *terminal.Window) {
	if window == nil {
		return
	}
	ch := m.ensureNvimNavigationChan()
	id := window.ID
	window.NvimNavFunc = func(direction string) {
		select {
		case ch <- NvimNavigationMsg{WindowID: id, Direction: direction}:
		default:
		}
	}
	window.NvimNavStateFunc = func(active bool) {
		select {
		case ch <- NvimNavigationMsg{WindowID: id, State: &active}:
		default:
		}
	}
}

func (m *OS) onNvimNavigation(msg NvimNavigationMsg) {
	if msg.State != nil {
		if m.nvimNavigators == nil {
			m.nvimNavigators = map[string]bool{}
		}
		m.nvimNavigators[msg.WindowID] = *msg.State
		return
	}
	focused := m.GetFocusedWindow()
	if m.Mode != TerminalMode || focused == nil || focused.ID != msg.WindowID {
		return
	}
	previous := m.FocusedWindow
	if m.AutoTiling && m.UseScrollingLayout && (msg.Direction == "left" || msg.Direction == "right") {
		if msg.Direction == "left" {
			m.ScrollingFocusLeft()
		} else {
			m.ScrollingFocusRight()
		}
	} else {
		_ = m.FocusDirection(msg.Direction)
	}
	if m.FocusedWindow != previous {
		m.RevealFocusedColumn()
		m.SyncStateToDaemon()
	}
}

// NvimNavigatorActive reports whether the focused pane owns focus keys.
func (m *OS) NvimNavigatorActive() bool {
	focused := m.GetFocusedWindow()
	return focused != nil && m.nvimNavigators[focused.ID]
}
