package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// nvimNavigationReplyWindow bounds replies to forwarded focus keys.
const nvimNavigationReplyWindow = 500 * time.Millisecond

type pendingNvimNavigation struct {
	WindowID  string
	Direction string
	ExpiresAt time.Time
}

// NvimNavigationMsg is a pane's request to leave a Neovim split at its edge.
type NvimNavigationMsg struct {
	WindowID  string
	Direction string
	State     *bool
	Reset     bool
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
	window.NvimNavResetFunc = func() {
		select {
		case ch <- NvimNavigationMsg{WindowID: id, Reset: true}:
		default:
		}
	}
}

func (m *OS) onNvimNavigation(msg NvimNavigationMsg) {
	if msg.Reset {
		m.clearNvimNavigation(msg.WindowID)
		return
	}
	if !m.Settings.NvimNavigation {
		return
	}
	if msg.State != nil {
		if m.nvimNavigators == nil {
			m.nvimNavigators = map[string]bool{}
		}
		m.nvimNavigators[msg.WindowID] = *msg.State
		return
	}
	focused := m.GetFocusedWindow()
	if m.Mode != TerminalMode || focused == nil || focused.ID != msg.WindowID ||
		!m.NvimNavigatorActive() || !m.matchesPendingNvimNavigation(msg) {
		return
	}
	m.pendingNvimNavigation = nil
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

func (m *OS) clearNvimNavigation(windowID string) {
	delete(m.nvimNavigators, windowID)
	if pending := m.pendingNvimNavigation; pending != nil && pending.WindowID == windowID {
		m.pendingNvimNavigation = nil
	}
}

// NvimNavigatorActive reports whether the focused pane owns focus keys.
func (m *OS) NvimNavigatorActive() bool {
	focused := m.GetFocusedWindow()
	return m.Settings.NvimNavigation && focused != nil && m.nvimNavigators[focused.ID]
}

// ArmNvimNavigation permits one matching focus reply.
func (m *OS) ArmNvimNavigation(direction string) bool {
	if !m.NvimNavigatorActive() {
		return false
	}
	focused := m.GetFocusedWindow()
	m.pendingNvimNavigation = &pendingNvimNavigation{
		WindowID:  focused.ID,
		Direction: direction,
		ExpiresAt: time.Now().Add(nvimNavigationReplyWindow),
	}
	return true
}

func (m *OS) matchesPendingNvimNavigation(msg NvimNavigationMsg) bool {
	pending := m.pendingNvimNavigation
	return pending != nil && time.Now().Before(pending.ExpiresAt) &&
		pending.WindowID == msg.WindowID && pending.Direction == msg.Direction
}
