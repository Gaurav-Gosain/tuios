package session

import "strconv"

// The depth cap: agents.max_depth.
//
// Every pane a pane makes records how deep it is (WindowState.ChainDepth): one
// more than the pane that made it. A pane the person opens, or a call from
// outside every pane makes, is at depth 0. A shell pane counts like an agent
// pane, so an agent that opens a shell and starts an agent from there does not
// start the count again. The depth is written when the pane is made and saved
// with it, so it holds after the panes above it close.
//
// With the cap set, every verb that starts an agent (start-agent, and so
// presets, delegate and a judge, fan, and new-worktree with a command) works
// out the depth of the pane it would make before it makes anything, and
// refuses past the cap. The grants of the caller do not matter: fan stays a
// grant a parent may hand on, and the cap is what bounds the tree. A start
// that names a parent takes the deeper of the caller and the parent, so a
// pane cannot reset its depth by naming a shallow parent.
//
// A call over a link has no pane on this machine, so it is at depth 0 here:
// each machine holds the cap for the panes its own panes start.

// setAgentMaxDepth sets agents.max_depth. Zero or less sets no cap.
func (d *Daemon) setAgentMaxDepth(n int) {
	d.agentMaxDepth.Store(int32(min(max(n, 0), agentLinkMaxDepth)))
}

// windowChainDepth is the depth of w: its own record, or its agent link's
// depth for a pane saved before the record existed, whichever is deeper.
func windowChainDepth(w WindowState) int {
	depth := w.ChainDepth
	if w.AgentLink != nil {
		depth = max(depth, w.AgentLink.Depth)
	}
	return depth
}

// paneChainDepth is the depth of the pane with id window in any session, 0
// when no session holds it.
func (d *Daemon) paneChainDepth(window string) int {
	sess := d.sessionHoldingWindow(window)
	if sess == nil {
		return 0
	}
	w, ok := findWindowState(sess.GetState(), window)
	if !ok {
		return 0
	}
	return windowChainDepth(w)
}

// callerChainDepth is the depth of the pane the caller on cs runs in, 0 for a
// caller outside every pane of this daemon.
func (d *Daemon) callerChainDepth(cs *connState) int {
	ref := d.callerPaneRef(cs)
	if ref == nil {
		return 0
	}
	return d.paneChainDepth(ref.Window)
}

// startDepth is the depth of a pane the caller on cs would start, with the
// parent it names when there is one.
func (d *Daemon) startDepth(cs *connState, parent *agentParent) int {
	depth := d.callerChainDepth(cs) + 1
	if parent != nil {
		depth = max(depth, parent.chainDepth)
	}
	return depth
}

// depthRefusal is the refusal of an agent start by the caller on cs that
// would make a pane deeper than agents.max_depth, nil when it may start or no
// cap is set.
func (d *Daemon) depthRefusal(cs *connState, verb string, parent *agentParent) *verbError {
	limit := int(d.agentMaxDepth.Load())
	if limit <= 0 {
		return nil
	}
	depth := d.startDepth(cs, parent)
	if depth <= limit {
		return nil
	}
	LogBasic("Refused %s: the new pane would be at depth %d, and agents.max_depth is %d", verb, depth, limit)
	return hintedVerbError(ErrVerbForbidden, verb+" is refused: the new agent would be at depth "+strconv.Itoa(depth)+
		", and agents.max_depth is "+strconv.Itoa(limit), &VerbHint{
		Detail: "The person set agents.max_depth in config.toml, and this start would go deeper. Nothing was started. " +
			"Do the work in this pane, or tell the agent that started you what you need.",
	})
}
