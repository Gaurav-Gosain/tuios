//go:build !slim

package session

import (
	"slices"
	"strings"
	"time"
)

// waitAgentState resolves when a window's agent state becomes one of the states
// named in until. With a window it watches that pane; without one it watches the
// whole session, which is the shape automation actually wants: "tell me when any
// agent here needs input" was previously only expressible as a poll loop over
// get-agent-state.
//
// It subscribes before the initial check, so a transition in the race window is
// not missed, and re-reads the canonical state on every event rather than
// trusting the payload, the waitSessionExists discipline.
func (d *Daemon) waitAgentState(sessionName, window, until string, deadline <-chan time.Time) (any, *verbError) {
	states, verr := parseUntilStates(until)
	if verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, verr
	}
	// The target is pinned to a window ID up front, so a wait keeps meaning the
	// same pane if the session's focus or names change while it blocks.
	targetID := ""
	if window != "" {
		state := sess.GetState()
		idx, err := findWindowStateIndex(state.Windows, window)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		targetID = state.Windows[idx].ID
	}

	types := map[string]bool{EventAgentState: true}
	if targetID != "" {
		// A watched pane closing must fail the wait rather than run out the
		// clock: nothing will ever report a state for it again.
		types[EventWindowClosed] = true
	}
	sub := d.events.subscribe(eventFilter{session: sess.Name(), sess: sess, types: types}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	check := func() (string, string, bool) {
		state := sess.GetState()
		for i := range state.Windows {
			w := &state.Windows[i]
			if targetID != "" && w.ID != targetID {
				continue
			}
			if states[w.AgentState.Name()] {
				return w.ID, w.AgentState.Name(), true
			}
		}
		return "", "", false
	}

	matched := func(id, name string) map[string]any {
		return waitMatched("agent-state", map[string]any{"session": sess.Name(), "window": id, "state": name})
	}
	if id, name, ok := check(); ok {
		return matched(id, name), nil
	}
	for {
		select {
		case <-deadline:
			return nil, agentStateTimeout(until)
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case ev := <-sub.ch:
			if ev.Type == EventWindowClosed && targetID != "" && ev.Window == targetID {
				return nil, newVerbError(ErrVerbWindowNotFound, "the watched window closed before reaching "+until)
			}
			if id, name, ok := check(); ok {
				return matched(id, name), nil
			}
		}
	}
}

// agentStateTimeout is the error an agent-state wait returns when it runs out.
func agentStateTimeout(until string) *verbError {
	return hintedVerbError(ErrVerbTimeout, "timed out waiting for agent state "+until, &VerbHint{
		Param:  "until",
		Verb:   "get-agent-state",
		Detail: "No agent reached the named state before the timeout. Read the current state, or raise timeout (milliseconds).",
	})
}

// waitAgentStateAnySession is waitAgentState across every session on the
// daemon, for a supervisor that watches agents in several sessions and would
// otherwise hold one wait per session. It never fails because a session is
// missing: sessions created while it blocks are watched too, since the check
// reads the session list again on every event.
//
// The subscription is taken before the first check, the same discipline as
// the single-session wait, so a transition in between is not missed.
func (d *Daemon) waitAgentStateAnySession(until string, deadline <-chan time.Time) (any, *verbError) {
	states, verr := parseUntilStates(until)
	if verr != nil {
		return nil, verr
	}
	sub := d.events.subscribe(eventFilter{types: map[string]bool{EventAgentState: true}}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	check := func() (map[string]any, bool) {
		sessions := d.manager.AllSessions()
		// Sorted, so when several panes already match the answer does not
		// depend on map order.
		slices.SortFunc(sessions, func(a, b *Session) int { return strings.Compare(a.Name(), b.Name()) })
		for _, sess := range sessions {
			state := sess.GetState()
			for i := range state.Windows {
				w := &state.Windows[i]
				if states[w.AgentState.Name()] {
					return waitMatched("agent-state", map[string]any{
						"session": sess.Name(), "window": w.ID, "state": w.AgentState.Name(),
					}), true
				}
			}
		}
		return nil, false
	}

	if res, ok := check(); ok {
		return res, nil
	}
	for {
		select {
		case <-deadline:
			return nil, agentStateTimeout(until)
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			if res, ok := check(); ok {
				return res, nil
			}
		}
	}
}

// waitAgentStateSelect is the agent-state wait over the panes a selector
// matches, in every session. Without every it ends when any matched pane is
// in one of the until states. With every it ends when at least one pane
// matches and all of them are, which is "wait until the whole fan-out is done".
//
// The selector is resolved again on every event, so a pane that opens during
// the wait and matches is watched, and one that closes stops counting. A state
// term in the selector narrows which panes are watched at the moment of each
// check, so the state to wait for belongs in until, not in the selector.
func (d *Daemon) waitAgentStateSelect(sel *Selector, until string, every bool, deadline <-chan time.Time) (any, *verbError) {
	states, verr := parseUntilStates(until)
	if verr != nil {
		return nil, verr
	}
	sub := d.events.subscribe(eventFilter{types: map[string]bool{
		EventAgentState: true, EventWindowCreated: true, EventWindowClosed: true,
		EventSessionCreated: true, EventSessionClosed: true,
	}}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	check := func() (map[string]any, bool) {
		panes := d.selectPanes(sel, false)
		if every {
			if len(panes) == 0 {
				return nil, false
			}
			matched := make([]map[string]any, 0, len(panes))
			for _, p := range panes {
				name := p.window.AgentState.Name()
				if !states[name] {
					return nil, false
				}
				matched = append(matched, map[string]any{"session": p.sess.Name(), "window": p.window.ID, "state": name})
			}
			return waitMatched("agent-state", map[string]any{"select": sel.String(), "every": true, "panes": matched, "total": len(matched)}), true
		}
		for _, p := range panes {
			if name := p.window.AgentState.Name(); states[name] {
				return waitMatched("agent-state", map[string]any{
					"select": sel.String(), "session": p.sess.Name(), "window": p.window.ID, "state": name,
				}), true
			}
		}
		return nil, false
	}

	if res, ok := check(); ok {
		return res, nil
	}
	for {
		select {
		case <-deadline:
			verr := agentStateTimeout(until)
			verr.Hint.Command = "tuios list-agents --select '" + sel.String() + "'"
			verr.Hint.Detail = "No pane the selector matches reached the named state before the timeout. list-agents with the same selector shows what it matches and where each pane is now."
			if every {
				verr.Hint.Detail = "Not every pane the selector matches reached the named state before the timeout. list-agents with the same selector shows which have not."
			}
			return nil, verr
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			if res, ok := check(); ok {
				return res, nil
			}
		}
	}
}

// parseUntilStates turns the comma-separated until parameter into the set of
// wire spellings a wait accepts.
func parseUntilStates(until string) (map[string]bool, *verbError) {
	if strings.TrimSpace(until) == "" {
		return nil, invalidParam("until", "until is required for the agent-state condition")
	}
	states := map[string]bool{}
	for part := range strings.SplitSeq(until, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		st, ok := ParseAgentState(part)
		if !ok {
			return nil, hintedVerbError(ErrVerbInvalidParams, "unknown agent state "+echoName(part), &VerbHint{
				Param:      "until",
				Accepted:   AgentStateNames,
				DidYouMean: closestMatch(part, AgentStateNames),
			})
		}
		states[st.Name()] = true
	}
	return states, nil
}

// waitAgentMessage resolves when a message arrives for an inbox, and is what
// makes the mailbox cost nothing while it is empty: a waiting agent is blocked
// on the hub rather than asking every second whether anything showed up.
//
// The two shapes differ on purpose. With a window it is "wake me when I have
// mail", so an unread message already sitting in the inbox matches at once; a
// wait that could miss a message queued a moment before it started would be a
// race every caller had to work around. Without a window it is "wake me when
// anything is said here", which cannot use the same rule because the ring is
// almost never empty, so it takes the newest message id as a baseline and
// matches only what arrives after.
//
// A thread narrows either shape to one conversation and changes nothing else.
// A thread the ring holds nothing from simply never matches, and times out like
// any other wait: the ring forgets, so a thread nobody has started and a thread
// that has aged out are the same thing to a reader.
func (d *Daemon) waitAgentMessage(sessionName, window string, thread uint64, deadline <-chan time.Time) (any, *verbError) {
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, verr
	}

	inbox := ""
	if window != "" {
		id, _, err := resolveMailParty(sess.GetState(), window)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		inbox = id
	}

	// The baseline is taken before the subscription so a message published in
	// the race window is newer than it, and is therefore matched rather than
	// missed.
	baseline := d.agents.highestID()

	// Any id in the thread names the thread, the same rule read-agent-messages
	// follows, so a caller can wait on the id of the message it just sent.
	thread = d.agents.resolveThread(sess.Name(), thread)

	types := map[string]bool{EventAgentMessage: true}
	if inbox != "" {
		// A watched inbox closing has to fail the wait rather than run out the
		// clock: nothing will ever be delivered to it again.
		types[EventWindowClosed] = true
	}
	sub := d.events.subscribe(eventFilter{session: sess.Name(), sess: sess, types: types}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	check := func() (AgentMessage, bool) {
		if inbox != "" {
			return d.agents.firstUnread(sess.Name(), inbox, thread)
		}
		return d.agents.newerThan(sess.Name(), baseline, thread)
	}

	if m, ok := check(); ok {
		return agentMessageMatch(inbox, m), nil
	}
	for {
		select {
		case <-deadline:
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for an agent message", &VerbHint{
				Param:   "timeout",
				Command: "tuios read-agent-messages",
				Detail:  "Nothing was sent before the timeout. Read the ring to see what is already there, or raise timeout (milliseconds).",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case ev := <-sub.ch:
			if ev.Type == EventWindowClosed && inbox != "" && ev.Window == inbox {
				return nil, newVerbError(ErrVerbWindowNotFound, "the watched inbox closed before a message arrived")
			}
			if m, ok := check(); ok {
				return agentMessageMatch(inbox, m), nil
			}
		}
	}
}

// agentMessageMatch renders the wait result. It reports the message's identity
// and never its body: the caller reads it with read-agent-messages, which is
// where the untrusted-content framing lives.
func agentMessageMatch(inbox string, m AgentMessage) map[string]any {
	return waitMatched("agent-message", map[string]any{
		"window":     inbox,
		"message_id": m.ID,
		"kind":       m.Kind,
		"from":       m.From,
		"from_name":  m.FromLabel,
		"subject":    m.Subject,
		"reply_to":   m.ReplyTo,
		"thread_id":  m.ThreadID,
	})
}
