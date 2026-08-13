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

// labelVerbCmd calls a daemon verb from a command, never from Update. The verb
// protocol is a blocking round trip, and the TUI client's own round trips are
// serialised behind one mutex, so a call made inline would park input, rendering
// and socket draining for as long as the daemon took to answer.
//
// The daemon owns the label: it writes it into the session state, pushes it to
// every attached client, and saves it with the rest, which is what makes the
// change outlive the client that made it.
func labelVerbCmd(what, verb string, params map[string]any) tea.Cmd {
	return func() tea.Msg {
		c, err := session.DialVerbClient()
		if err != nil {
			return RenameAppliedMsg{What: what, Err: err}
		}
		defer func() { _ = c.Close() }()

		if _, err := c.Call(verb, params); err != nil {
			return RenameAppliedMsg{What: what, Err: err}
		}
		return RenameAppliedMsg{What: what}
	}
}

// sessionAccentVerb builds the daemon call that records a session's accent. An
// empty accent is how the verb says "clear it", which puts the session back on
// the colour it is assigned automatically rather than on no colour.
func sessionAccentVerb(name, accent string) (string, map[string]any, bool) {
	if name == "" {
		return "", nil, false
	}
	return "set-session-accent", map[string]any{"session": name, "accent": accent}, true
}

// setSessionAccentCmd writes a session's accent through the daemon, which is
// what makes it survive a reattach and reach every other client attached to it.
func (m *OS) setSessionAccentCmd(name, accent string) tea.Cmd {
	verb, params, ok := sessionAccentVerb(name, accent)
	if !ok {
		return nil
	}
	return labelVerbCmd("Accent", verb, params)
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
