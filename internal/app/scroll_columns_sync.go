package app

import (
	"maps"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The scrolling layout's columns as ops (see scroll_columns.go in the
// session package for the daemon's half).
//
// This client changes its strip at once, wherever it changes it. What it
// tells the daemon is worked out where it speaks, SyncStateToDaemon: every
// workspace whose columns are not the columns the session was last known to
// hold goes out as MsgScrollColumns, and the push leaves the columns out.
// colsSeen is that record, one key per workspace.
//
// The same record decides what a state may do to a strip. Columns that differ
// from colsSeen are a change this client has made and not yet sent, and stay.
// Any other workspace takes the session's columns from every state, newer or
// not: the daemon's copy is the only answer, which is what lets a change the
// daemon made (the arrange verb's scroll ops, guios's strip keys) stand
// against this client's next push. A state built before this client's own op
// landed never gets that far: the op is counted as a push (PushSeen).

// scrollOpsOn reports whether this client sends its columns as ops.
func (m *OS) scrollOpsOn() bool {
	return m.IsDaemonSession && m.DaemonClient != nil && m.DaemonClient.ScrollColumnOps()
}

// colsKey keys one workspace's columns, "" for none. A column's Active row
// is left out: it follows the focus, which travels in the push, and a focus
// move rewrote it here. Keyed, every focus move read as a change of this
// client's not yet sent: the client stopped taking the session's columns for
// the workspace, the two clients drew the strip two ways, and the columns it
// then sent, built on an older strip, could put a daemon change back.
func colsKey(cols []session.SerializedScrollColumn) string {
	if len(cols) == 0 {
		return ""
	}
	cols = slices.Clone(cols)
	for i := range cols {
		cols[i].Active = 0
	}
	b, err := jsonx.Marshal(cols)
	if err != nil {
		return ""
	}
	return string(b)
}

// noteSessionColumns records cols as the columns the session holds. It is for
// an attach, where the strip was just taken from the session whole.
func (m *OS) noteSessionColumns(cols map[int][]session.SerializedScrollColumn) {
	m.colsSeen = make(map[int]string, len(cols))
	for ws, c := range cols {
		m.colsSeen[ws] = colsKey(c)
	}
}

// sendScrollColumnOps sends every workspace's columns that are a change of
// this client's as an op. It reports false when an op could not be sent: the
// socket is gone, and the push is not sent either.
func (m *OS) sendScrollColumnOps() bool {
	if !m.AutoTiling {
		return true
	}
	if m.colsSeen == nil {
		m.colsSeen = make(map[int]string)
	}
	cur := m.scrollColumnsState()
	workspaces := slices.Sorted(maps.Keys(cur))
	for ws := range m.colsSeen {
		if _, ok := cur[ws]; !ok {
			workspaces = append(workspaces, ws)
		}
	}
	for _, ws := range workspaces {
		key := colsKey(cur[ws])
		if key == m.colsSeen[ws] {
			continue
		}
		if err := m.DaemonClient.SendScrollColumns(ws, cur[ws]); err != nil {
			m.LogError("Failed to send the scrolling columns to the daemon: %v", err)
			return false
		}
		m.colsSeen[ws] = key
	}
	return true
}

// columnsToAdopt is the part of the session's columns this client takes:
// every workspace that holds no unsent change of its own. It notes what it
// takes as the session's.
func (m *OS) columnsToAdopt(cols map[int][]session.SerializedScrollColumn) map[int][]session.SerializedScrollColumn {
	if m.colsSeen == nil {
		m.colsSeen = make(map[int]string)
	}
	cur := m.scrollColumnsState()
	out := make(map[int][]session.SerializedScrollColumn, len(cols))
	for ws, c := range cols {
		key := colsKey(c)
		mine := colsKey(cur[ws])
		if mine != m.colsSeen[ws] && mine != key {
			continue // a change of this client's not yet sent
		}
		out[ws] = c
		m.colsSeen[ws] = key
	}
	return out
}

// noteDerivedColumns records, for each workspace just taken from a state,
// the columns this client holds after taking them as the session's, when
// they hold the same panes. Taking them can rework them without a pane more
// or fewer (a column's row follows the focus); that is a local reading of the
// state, shown and not sent, as treeDerived is for the trees. Sent, it was a
// whole-columns op built on the strip of that state, and it could land after
// the daemon's next strip change and put it back. A pane the session's
// columns do not hold yet is placed here, and that is sent: no other side
// places it.
func (m *OS) noteDerivedColumns(adopted map[int][]session.SerializedScrollColumn) {
	if len(adopted) == 0 {
		return
	}
	cur := m.scrollColumnsState()
	for ws, theirs := range adopted {
		if samePanes(cur[ws], theirs) {
			m.colsSeen[ws] = colsKey(cur[ws])
		}
	}
}

// samePanes reports whether two workspaces' columns hold the same panes.
func samePanes(a, b []session.SerializedScrollColumn) bool {
	set := func(cols []session.SerializedScrollColumn) map[string]bool {
		out := map[string]bool{}
		for _, c := range cols {
			for _, id := range c.Windows {
				out[id] = true
			}
		}
		return out
	}
	return maps.Equal(set(a), set(b))
}
