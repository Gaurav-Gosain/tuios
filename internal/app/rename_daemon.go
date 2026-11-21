package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// RenameAppliedMsg reports the outcome of a daemon-owned label write: a rename,
// or a session's accent. What names the write for the failure notice, so the
// user is told which of them did not land.
type RenameAppliedMsg struct {
	What string
	Err  error
}

func labelVerbCmd(dial agentMailDial, what, verb string, params map[string]any) tea.Cmd {
	return func() tea.Msg {
		c, err := dial()
		if err != nil {
			return RenameAppliedMsg{What: what, Err: err}
		}
		defer func() { _ = c.Close() }()

		if _, err := c.Call(verb, params); err != nil {
			if stale := session.StaleDaemonError(verb, err); stale != nil {
				err = stale
			}
			return RenameAppliedMsg{What: what, Err: err}
		}
		return RenameAppliedMsg{What: what}
	}
}

func sessionAccentVerb(name, accent string) (string, map[string]any, bool) {
	if name == "" {
		return "", nil, false
	}
	return "set-session-accent", map[string]any{"session": name, "accent": accent}, true
}

func (m *OS) setSessionAccentCmd(name, accent string) tea.Cmd {
	verb, params, ok := sessionAccentVerb(name, accent)
	if !ok {
		return nil
	}
	return labelVerbCmd(m.verbDialer(), "Accent", verb, params)
}

type listingRefreshedMsg struct{}

func (m *OS) refreshListingCmd() tea.Cmd {
	client := m.DaemonClient
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		client.TryRefreshSessionList()
		return listingRefreshedMsg{}
	}
}

func (m *OS) refreshGotoRows() {
	g := &m.gotoPicker
	if !g.open {
		return
	}
	g.rows, g.sig, g.built = m.gotoRows(), m.sidebarSignature(), false
}
