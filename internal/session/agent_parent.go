package session

import (
	"strconv"
	"strings"
)

// The parent link: which agent started which.
//
// When an agent in a pane starts another agent with start-agent, the daemon
// records the caller as the child's parent: the pane (WindowState.StartedBy)
// and, in WindowState.AgentLink, the parent's harness and conversation id and
// the root of the chain. list-agents shows parent and root, the selector reads
// them (parent: and root:), and the rail draws a child's row under its
// parent's. Delegation builds on it: approvals roll up to the root, and a
// parent hears from its children.
//
// Who may name a parent. A call from a pane links to that pane on its own
// when the pane runs an agent. start-agent's parent names another one, for an
// orchestrator that runs outside the parent's pane:
//
//   - the person, from outside every pane, may name any agent pane here;
//   - a pane may name itself, or a pane it could type into (the write reach
//     and typingRefusal of pane_grants.go), since a child reports to its
//     parent and a pane that cannot reach a parent must not route work to it;
//   - a call over a link, from a pane this machine hosts for another machine,
//     or from a plugin names none: the link is between panes of this daemon.
//
// A cycle cannot happen: the parent exists before the child, and a link is
// written only when the child is made. Depth is still capped at
// agentLinkMaxDepth, because an agent that starts agents in a loop is a
// runaway, and the start is refused past the cap with nothing made.
//
// When the parent pane closes, the child keeps its link and list-agents marks
// the parent gone. The child's history still says who started it, and the
// liveness is read when it is asked for, so nothing has to run on a close and
// nothing can go stale. A gone root is a root a reader skips, never one it
// follows into nothing.

// agentLinkMaxDepth is the deepest a chain of agents may go: a root's child is
// at depth 1.
const agentLinkMaxDepth = 8

// agentParent is the parent a new agent pane will be linked to, resolved
// before the pane is made.
type agentParent struct {
	ref  PaneRef
	link AgentLink
}

// resolveAgentParent decides the parent of an agent pane the caller on cs
// starts. named is start-agent's parent parameter, empty for the caller's own
// pane. It returns nil with no error when there is no parent to record: a
// caller outside every pane, or in a pane that runs no agent.
func (d *Daemon) resolveAgentParent(cs *connState, verb, named string) (*agentParent, *verbError) {
	named = strings.TrimSpace(named)
	if named == "" {
		ref := d.callerPaneRef(cs)
		if ref == nil {
			return nil, nil
		}
		sess := d.sessionHoldingWindow(ref.Window)
		if sess == nil {
			return nil, nil
		}
		w, ok := findWindowState(sess.GetState(), ref.Window)
		if !ok || !isAgentWindow(w) {
			return nil, nil
		}
		return d.agentParentOf(verb, sess, w)
	}
	if cs != nil && cs.viaLink {
		return nil, invalidParam("parent", "parent names an agent pane on this machine, and a call over a link cannot name one")
	}
	pa := d.paneAuthority(cs)
	if pa != nil && (!pa.inPane() || pa.hosted) {
		return nil, grantForbidden(verb, pa, "only a pane of this machine, or the person, may name a parent")
	}
	sess, w, verr := d.findParentWindow(named)
	if verr != nil {
		return nil, verr
	}
	if pa != nil && w.ID != pa.window {
		if why := d.parentRefusal(pa, sess, w); why != "" {
			return nil, grantForbidden(verb, pa, why)
		}
	}
	if !isAgentWindow(w) {
		return nil, invalidParam("parent", "window "+shortWindowID(w.ID)+" runs no agent. A parent is an agent pane that list-agents shows")
	}
	return d.agentParentOf(verb, sess, w)
}

// parentRefusal is why the pane pa may not name w as a parent, "" when it
// may: it may when it could type into w.
func (d *Daemon) parentRefusal(pa *paneAuth, sess *Session, w WindowState) string {
	if !pa.grants.Has(GrantWrite) && !pa.grants.Has(GrantFan) {
		return "naming another pane as the parent needs the write grant"
	}
	if why := d.paneWriteReach(pa, sess.Name()); why != "" {
		return why
	}
	// blockedChecked: a parent on a prompt is still a parent. The rule asked
	// here is whether the pane reaches it, not whether a write would answer
	// the prompt.
	return d.typingRefusal(pa, w, true)
}

// findParentWindow finds the window parent names, by its id or a unique
// start of it, in any session.
func (d *Daemon) findParentWindow(id string) (*Session, WindowState, *verbError) {
	if sess := d.sessionHoldingWindow(id); sess != nil {
		if w, ok := findWindowState(sess.GetState(), id); ok {
			return sess, w, nil
		}
	}
	var (
		found *Session
		win   WindowState
		n     int
	)
	for _, sess := range d.manager.AllSessions() {
		for _, w := range sess.GetState().Windows {
			if strings.HasPrefix(w.ID, id) {
				found, win = sess, w
				n++
			}
		}
	}
	switch n {
	case 0:
		return nil, WindowState{}, invalidParam("parent", "no window has the id "+echoName(id)+". Name the parent by the window id that list-agents shows")
	case 1:
		return found, win, nil
	}
	return nil, WindowState{}, invalidParam("parent", strconv.Itoa(n)+" windows have an id that starts with "+echoName(id)+". Give more of the id")
}

// agentParentOf is the link a child of w in sess gets, refused when the child
// would be deeper than agentLinkMaxDepth.
func (d *Daemon) agentParentOf(verb string, sess *Session, w WindowState) (*agentParent, *verbError) {
	ref := PaneRef{Session: sess.Name(), Window: w.ID}
	link := AgentLink{
		Harness:        firstNonEmpty(w.AgentHarness, sess.agentClaimFor(w.ID).harness),
		AgentSessionID: w.AgentSessionID,
		Root:           ref,
		Depth:          1,
	}
	if up := w.AgentLink; up != nil {
		link.Root, link.Depth = up.Root, up.Depth+1
	}
	if link.Depth > agentLinkMaxDepth {
		return nil, hintedVerbError(ErrVerbForbidden, verb+" is refused: window "+shortWindowID(w.ID)+" is "+strconv.Itoa(link.Depth-1)+
			" agents deep, and a chain of agents stops at "+strconv.Itoa(agentLinkMaxDepth), &VerbHint{
			Detail: "The chain is at its limit, so nothing was started. Start the agent from a pane higher in the chain, or from outside every pane.",
		})
	}
	return &agentParent{ref: ref, link: link}, nil
}

// setAgentParent records p as the parent of window: the pane as StartedBy,
// and the link.
func (s *Session) setAgentParent(window string, p *agentParent) {
	ref, link := p.ref, p.link
	_ = s.mutateState(func(state *SessionState) error {
		idx, err := findWindowStateIndex(state.Windows, window)
		if err != nil {
			return err
		}
		state.Windows[idx].StartedBy = &ref
		state.Windows[idx].AgentLink = &link
		return nil
	})
}

// agentLinkRef is a pane of a link as list-agents gives it: where it is now,
// or gone with where it was. A pane is found by its window id, so a renamed
// session still finds it.
func (d *Daemon) agentLinkRef(ref PaneRef) map[string]any {
	out := map[string]any{"session": ref.Session, "window": ref.Window}
	if sess := d.sessionHoldingWindow(ref.Window); sess != nil {
		out["session"] = sess.Name()
	} else {
		out["gone"] = true
	}
	return out
}

// addAgentLink puts parent, root and depth on a list-agents row for a pane
// with an agent link.
func (d *Daemon) addAgentLink(row map[string]any, w WindowState) {
	l := w.AgentLink
	if l == nil || w.StartedBy == nil {
		return
	}
	parent := d.agentLinkRef(*w.StartedBy)
	if l.Harness != "" {
		parent["harness"] = l.Harness
	}
	if l.AgentSessionID != "" {
		parent["agent_session_id"] = l.AgentSessionID
	}
	row["parent"] = parent
	row["root"] = d.agentLinkRef(l.Root)
	row["depth"] = l.Depth
}

// agentParentRef is the parent pane of w when it has an agent link, nil
// otherwise. A pane some call opened without a link keeps StartedBy alone,
// and is no child to draw under its opener.
func agentParentRef(w WindowState) *PaneRef {
	if w.AgentLink == nil {
		return nil
	}
	return w.StartedBy
}
