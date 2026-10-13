package app

import (
	"encoding/json"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// Switching space. The client chooses which space, from the rows its rail
// draws (sessiontree.SpaceRows). The daemon chooses which of the space's
// sessions, with pick-space, and is the only place that ranks them. The client
// then switches itself to that session the way a click on a session row does,
// so nothing here moves another client, and nothing asks another machine's
// daemon to move this one.

// spacePickTimeout bounds pick-space, one read on the daemon, through a host
// included.
const spacePickTimeout = 5 * time.Second

// spacePickedMsg is pick-space's answer.
type spacePickedMsg struct {
	space   string
	session string
	err     error
	// client and from are the connection and the session the pick was asked
	// from. A pick that comes back after the client moved, to another machine
	// or to another session by any other way, answers a question nobody is
	// asking now, and is dropped rather than undo the later move.
	client *session.TUIClient
	from   string
}

// switchToSpaceCmd switches this client to the session of space the person
// used last, "" for the sessions in no space. It asks pick-space on the daemon
// that holds the attached session (this machine's, or through the link the far
// machine's) and switches on the answer; see applySpacePicked.
func (m *OS) switchToSpaceCmd(space string) tea.Cmd {
	if m.DaemonClient == nil {
		m.ShowNotification("Spaces need the session daemon", "info", m.Settings.NotificationDuration)
		return nil
	}
	params := map[string]any{"space": space}
	if space == "" {
		params = map[string]any{"none": true}
	}
	tui, from := m.DaemonClient, m.SessionName
	dial := m.sessionDaemonDialer()
	return func() tea.Msg {
		done := spacePickedMsg{space: space, client: tui, from: from}
		client, err := dial()
		if err != nil {
			done.err = err
			return done
		}
		defer func() { _ = client.Close() }()
		raw, err := client.CallWithTimeout("pick-space", params, spacePickTimeout)
		if err != nil {
			done.err = err
			return done
		}
		var res struct {
			Session string `json:"session"`
		}
		if err := json.Unmarshal(raw, &res); err != nil || res.Session == "" {
			done.err = errors.New("pick-space answered with no session")
			return done
		}
		done.session = res.Session
		return done
	}
}

// applySpacePicked switches to the picked session, through the same switch a
// session row's click goes through, or says why not. A daemon that has no
// pick-space is too old to ask, and the client does not guess in its place.
func (m *OS) applySpacePicked(msg spacePickedMsg) {
	if msg.client != m.DaemonClient || msg.from != m.SessionName {
		return
	}
	if msg.err == nil {
		m.sidebarSwitchSession(msg.session)
		// The listing the other rows are drawn from was taken while this
		// client was on the session it just left. Ask for a fresh one now
		// rather than at the next poll, or the space left behind reads as
		// attached for seconds.
		m.foreignSessionReplan = true
		return
	}
	call, isCall := errors.AsType[*session.VerbCallError](msg.err)
	if isCall && call.Code == session.ErrVerbUnknownVerb {
		m.ShowNotification("Switching spaces needs a newer tuios daemon", "warning", m.Settings.NotificationWarningDuration)
		return
	}
	where := "space " + msg.space
	if msg.space == "" {
		where = "no space"
	}
	if isCall {
		level := "error"
		if call.Code == session.ErrVerbHostUnreachable {
			level = "warning"
		}
		m.ShowNotification("Could not switch to "+where+": "+call.Message, level, m.Settings.NotificationDuration*3)
		return
	}
	m.ShowNotification("Could not switch to "+where+": "+msg.err.Error(), "error", m.Settings.NotificationDuration*3)
}

// CycleSpace switches to the next (delta 1) or previous (delta -1) space in
// the order the rail draws the spaces, the sessions in no space last when
// there are any, and wrapping. It says so and stays put with no space, or with
// no other space to go to.
func (m *OS) CycleSpace(delta int) tea.Cmd {
	sessions := localSessionNodes(m.BuildSessionTree().Sessions)
	rows := sessiontree.SpaceRows(sessions)
	if len(rows) == 0 {
		m.ShowNotification("No spaces", "info", m.Settings.NotificationDuration)
		return nil
	}
	if len(rows) == 1 {
		m.ShowNotification("No other spaces", "info", m.Settings.NotificationDuration)
		return nil
	}
	current, _ := currentSpace(sessions)
	at := -1
	for i, r := range rows {
		if r.ID == current {
			at = i
		}
	}
	next := 0
	switch {
	case at >= 0:
		next = (at + delta + len(rows)) % len(rows)
	case delta < 0:
		next = len(rows) - 1
	}
	return m.switchToSpaceCmd(rows[next].ID)
}

// CycleSessionInSpace switches to the next (delta 1) or previous (delta -1)
// session in the current session's space, in the rail's order and wrapping. A
// session in no space cycles through the other sessions in no space.
//
// With no space on this machine at all the key says so and stays put: every
// session is then in no space, and walking them would only repeat
// CycleSession under another key.
func (m *OS) CycleSessionInSpace(delta int) {
	sessions := localSessionNodes(m.BuildSessionTree().Sessions)
	space, ok := currentSpace(sessions)
	if !ok {
		m.ShowNotification("No spaces", "info", m.Settings.NotificationDuration)
		return
	}
	target := neighbourSession(sessionsInSpace(sessions, space), delta)
	if target == "" {
		where := "outside a space"
		if space != "" {
			where = "in " + space
		}
		m.ShowNotification("No other sessions "+where, "info", m.Settings.NotificationDuration)
		return
	}
	m.openSession("", target)
}

// currentSpace is the space of the current session among one machine's
// sessions. It reports false when none of them is in a space.
func currentSpace(sessions []sessiontree.Node) (string, bool) {
	current, filed := "", false
	for _, n := range sessions {
		space := sessiontree.SpaceOf(n)
		filed = filed || space != ""
		if n.IsCurrent {
			current = space
		}
	}
	return current, filed
}

// sessionsInSpace is the sessions filed under space, "" for those in none, in
// the order the rail draws them: grouped by space, and by repository inside
// each space.
func sessionsInSpace(sessions []sessiontree.Node, space string) []sessiontree.Node {
	var out []sessiontree.Node
	for _, n := range sessiontree.GroupBySpace(sessions, sessiontree.GroupByRepo) {
		if n.Kind == sessiontree.KindSession && sessiontree.SpaceOf(n) == space {
			out = append(out, n)
		}
	}
	return out
}
