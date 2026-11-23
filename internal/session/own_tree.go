package session

import (
	"encoding/json"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// A pane's own tree: the panes it started, and theirs, down the chain.
//
// A pane with the fan grant starts panes, and it may close them and wait for
// them without the admin grant: close-window on one of them, and wait-for
// agent-state --select, which then watches only the panes of its tree. A pane
// outside the tree still needs admin. The tree is read from StartedBy, which
// the daemon writes when a pane's call makes a pane, and from the root of an
// agent link (AgentLink.Root), which the daemon writes at the same time.
//
// This is a security boundary. The ways it could fail, and what holds each:
//
//   - A pane renamed. The check resolves the window the call names, by id,
//     index or name as the handler would, and the call goes on with that
//     window's id. A rename between the check and the close cannot point the
//     call at another pane, and a stranger renamed to a child's name is
//     checked as the stranger.
//   - A child re-parented. StartedBy and AgentLink are daemon-owned: only the
//     call that makes a pane writes them, a client sync can neither set nor
//     clear them (retainDaemonExclusive), and no verb moves them to another pane.
//     A pane moved to another session keeps them.
//   - A child in another session. The tree follows window ids across
//     sessions, so a child that start-agent put in another session is in the
//     tree, and the call goes on with the session that holds it. A pane of
//     another session outside the tree is refused as before.
//   - A pane id reused. Window ids are uuids, unique on the daemon, so a new
//     pane never takes the id of a closed caller or a closed child.
//   - A pane above a child closed. The walk up StartedBy stops at a pane that
//     is gone, and the child is then outside the tree, unless its agent link
//     names the caller as its root. The check fails closed: close the deepest
//     panes first.
//   - A --select that matches panes inside and outside the tree. The wait
//     watches only the panes inside it (under), so a pane outside the tree
//     can neither end the wait nor show in its answer.
//   - A loop in StartedBy. A pane's StartedBy names a pane that existed
//     before it, so there is none, and the walk stops at paneOriginMaxDepth
//     and at a pane it has seen.
//   - A pane on a link, a hosted pane or a plugin. The rule is not for them:
//     checkGrants holds them before it reaches the rule.

// treeLink is what the own tree reads of one window: the pane that started
// it, and the root of its agent link.
type treeLink struct {
	startedBy string
	root      string
}

// ownTreeLinks reads the tree links of every window of every session. Each
// session's windows are read in place under its state lock, as holdsWindowID
// reads them: no state is copied and no process is read. A wait builds it
// once per check, so the cost of a check is one pass over the windows.
func (d *Daemon) ownTreeLinks() map[string]treeLink {
	links := make(map[string]treeLink)
	for _, sess := range d.manager.AllSessions() {
		sess.addTreeLinks(links)
	}
	return links
}

// addTreeLinks adds the tree links of the session's windows to links.
func (s *Session) addTreeLinks(links map[string]treeLink) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.state == nil {
		return
	}
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		var l treeLink
		if w.StartedBy != nil {
			l.startedBy = w.StartedBy.Window
		}
		if w.AgentLink != nil {
			l.root = w.AgentLink.Root.Window
		}
		links[w.ID] = l
	}
}

// inOwnTree reports whether the window id is in the tree of the pane root:
// root started it, or started a pane that did, down the chain. root itself is
// not in its tree. links is ownTreeLinks.
func inOwnTree(links map[string]treeLink, id, root string) bool {
	if root == "" || id == root {
		return false
	}
	link, ok := links[id]
	if !ok {
		return false
	}
	if link.root == root {
		return true
	}
	seen := map[string]bool{id: true}
	for range paneOriginMaxDepth {
		up := link.startedBy
		if up == "" {
			return false
		}
		if up == root {
			return true
		}
		if seen[up] {
			return false
		}
		seen[up] = true
		if link, ok = links[up]; !ok {
			return false
		}
	}
	return false
}

// holdToOwnTree is the own tree rule of checkGrants for a pane pa that holds
// fan and not admin. ok is false when the rule does not apply to the call,
// and the usual checks follow. Otherwise the call is served with out, or
// refused with verr.
func (d *Daemon) holdToOwnTree(verb string, pa *paneAuth, params json.RawMessage, deny func(string) *verbError) (out json.RawMessage, verr *verbError, ok bool) {
	if !pa.grants.Has(GrantFan) || pa.window == "" {
		return nil, nil, false
	}
	var m map[string]json.RawMessage
	if len(strings.TrimSpace(string(params))) > 0 {
		if err := jsonx.Unmarshal(params, &m); err != nil {
			return nil, nil, false
		}
	}
	str := func(name string) string {
		var s string
		if raw, ok := m[name]; ok {
			_ = jsonx.Unmarshal(raw, &s)
		}
		return s
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	set := func(name, value string) {
		raw, _ := jsonx.Marshal(value)
		m[name] = raw
	}
	switch verb {
	case "close-window":
		window := strings.TrimSpace(str("window"))
		if window == "" {
			// No window is the focused one, which is not the pane's to pick.
			return nil, nil, false
		}
		name := strings.TrimSpace(str("session"))
		if name == "" {
			name = pa.session
		}
		sess := d.manager.GetSession(name)
		var (
			st  *SessionState
			idx = -1
		)
		if sess != nil {
			st = sess.GetState()
			idx, _ = findWindowStateIndex(st.Windows, window)
		}
		// A window id that the session does not hold is found in whichever
		// session holds it. The CLI fills in the pane's own session, so a
		// child in another session closes by its id without -s. The tree
		// check below holds it like any other window.
		if idx < 0 {
			if sess = d.sessionHoldingWindow(window); sess == nil {
				return nil, nil, false
			}
			st = sess.GetState()
			if idx, _ = findWindowStateIndex(st.Windows, window); idx < 0 {
				return nil, nil, false
			}
		}
		w := st.Windows[idx]
		if !inOwnTree(d.ownTreeLinks(), w.ID, pa.window) {
			return nil, deny(verb + " needs the admin grant for window " + shortWindowID(w.ID) +
				". Without it, a pane with the fan grant closes only the panes it started and the panes they started"), true
		}
		set("session", sess.Name())
		set("window", w.ID)
	case "wait-for":
		if strings.TrimSpace(str("select")) == "" {
			return nil, nil, false
		}
		set("under", pa.window)
	default:
		return nil, nil, false
	}
	out, err := jsonx.Marshal(m)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode the params: "+err.Error()), true
	}
	return out, nil, true
}
