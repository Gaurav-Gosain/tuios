package session

import (
	"strconv"
	"strings"
)

const agentLinkMaxDepth = 8

type agentParent struct {
	ref  PaneRef
	link AgentLink
}

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

func (d *Daemon) parentRefusal(pa *paneAuth, sess *Session, w WindowState) string {
	if !pa.grants.Has(GrantWrite) && !pa.grants.Has(GrantFan) {
		return "naming another pane as the parent needs the write grant"
	}
	if why := d.paneWriteReach(pa, sess.Name()); why != "" {
		return why
	}
	return d.typingRefusal(pa, w, true)
}

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

func (d *Daemon) agentLinkRef(ref PaneRef) map[string]any {
	out := map[string]any{"session": ref.Session, "window": ref.Window}
	if sess := d.sessionHoldingWindow(ref.Window); sess != nil {
		out["session"] = sess.Name()
	} else {
		out["gone"] = true
	}
	return out
}

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

func agentParentRef(w WindowState) *PaneRef {
	if w.AgentLink == nil {
		return nil
	}
	return w.StartedBy
}
