package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// RenameAppliedMsg reports the outcome of a daemon-owned rename.
type RenameAppliedMsg struct {
	Err error
}

// renameVerbCmd calls a daemon verb from a command, never from Update. The verb
// protocol is a blocking round trip, and the TUI client's own round trips are
// serialised behind one mutex, so a call made inline would park input, rendering
// and socket draining for as long as the daemon took to answer.
//
// The daemon owns the label: it writes it into the session state, pushes it to
// every attached client, and saves it with the rest, which is what makes the
// rename outlive the client that made it.
func renameVerbCmd(verb string, params map[string]any) tea.Cmd {
	return func() tea.Msg {
		c, err := session.DialVerbClient()
		if err != nil {
			return RenameAppliedMsg{Err: err}
		}
		defer func() { _ = c.Close() }()

		if _, err := c.Call(verb, params); err != nil {
			return RenameAppliedMsg{Err: err}
		}
		return RenameAppliedMsg{}
	}
}

// refreshSwitcherItems rebuilds whichever switcher is open, so a rename (this
// client's or another client's) shows up in the list it was made from.
func (m *OS) refreshSwitcherItems() {
	if m.ShowSessionSwitcher {
		m.SessionSwitcherItems = m.BuildSessionTree().Sessions
	}
	if m.ShowWorkspaceSwitcher {
		m.WorkspaceSwitcherItems = m.buildWorkspaceItems()
	}
}
