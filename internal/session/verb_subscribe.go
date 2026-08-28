package session

import (
	"encoding/json"
	"maps"
	"regexp"
	"strings"
	"time"
)

// This file implements the subscribe and wait-for verbs on top of the event hub
// (daemon_events.go). subscribe turns a JSON connection into a long-lived event
// stream; wait-for is sugar over a short-lived internal subscription that blocks
// until a condition matches or a timeout elapses, replacing a caller's
// capture-pane polling loop.

// defaultWaitTimeout bounds a wait-for verb that omits an explicit timeout.
const defaultWaitTimeout = 30 * time.Second

// defaultIdleWindow is the quiet period a window-idle wait uses when the request
// omits an idle duration.
const defaultIdleWindow = 500 * time.Millisecond

// waitOutputRecheck is a cheap in-process backstop interval for wait-for-output.
// The output events drive an immediate re-check; this ticker only guards the rare
// case where the final matching output event was dropped by the slow-subscriber
// policy, so the wait still resolves without a caller-side poll loop.
const waitOutputRecheck = 200 * time.Millisecond

// verbSubscribe opens a long-lived event stream on this connection. It registers
// a hub subscription with the requested filter, returns a subscribed ack (with
// the current sequence baseline), and hands the subscription to the dispatch loop
// which starts the streamer after the ack is written. Only a connection that
// issued this verb ever receives events.
func (d *Daemon) verbSubscribe(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string   `json:"session"`
		Window  string   `json:"window"`
		Types   []string `json:"types"`
		Queue   int      `json:"queue"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	cs.mu.Lock()
	if cs.streaming {
		cs.mu.Unlock()
		return nil, hintedVerbError(ErrVerbInvalidRequest, "connection is already subscribed", &VerbHint{
			Verb:   "unsubscribe",
			Detail: "One event stream per connection. Call unsubscribe first, or open a second connection for the second filter.",
		})
	}
	cs.mu.Unlock()

	filter := eventFilter{session: p.Session, window: p.Window}
	if len(p.Types) > 0 {
		filter.types = make(map[string]bool, len(p.Types))
		for _, t := range p.Types {
			filter.types[t] = true
		}
	}

	sub := d.events.subscribe(filter, p.Queue)

	cs.mu.Lock()
	cs.eventSub = sub
	cs.pendingStream = sub
	cs.streaming = true
	cs.mu.Unlock()

	return map[string]any{"type": EventSubscribed, "seq": d.events.currentSeq()}, nil
}

// verbUnsubscribe closes this connection's event stream. The streamer observes
// the stop signal, clears the connection's stream state, and unsubscribes from
// the hub.
func (d *Daemon) verbUnsubscribe(cs *connState, _ json.RawMessage) (any, *verbError) {
	cs.mu.Lock()
	sub := cs.eventSub
	cs.mu.Unlock()
	if sub == nil {
		return nil, hintedVerbError(ErrVerbInvalidRequest, "connection is not subscribed", &VerbHint{
			Verb:   "subscribe",
			Detail: "There is no event stream on this connection to close.",
		})
	}
	// Signal the streamer to exit; it clears cs.eventSub/streaming and removes the
	// hub subscription on its way out.
	sub.close()
	return map[string]any{"type": "unsubscribed"}, nil
}

// startPendingStream launches the event streamer for a subscription handed over
// by the subscribe handler, once its ack has been written. It is a no-op for
// every other verb.
func (d *Daemon) startPendingStream(cs *connState) {
	cs.mu.Lock()
	sub := cs.pendingStream
	cs.pendingStream = nil
	cs.mu.Unlock()
	if sub == nil {
		return
	}
	d.wg.Go(func() {
		d.streamEvents(cs, sub)
	})
}

// streamEvents pushes events from a subscription to the connection until the
// connection closes, the daemon shuts down, or the subscription is stopped. A
// full subscriber queue drops events at publish time; the streamer surfaces those
// drops as a gap marker written just before the next surviving event, so a slow
// subscriber never blocks the daemon (the daemon_stream.go discipline).
func (d *Daemon) streamEvents(cs *connState, sub *eventSub) {
	defer d.events.unsubscribe(sub)
	defer func() {
		cs.mu.Lock()
		if cs.eventSub == sub {
			cs.eventSub = nil
			cs.streaming = false
		}
		cs.mu.Unlock()
	}()

	for {
		select {
		case <-cs.done:
			return
		case <-d.ctx.Done():
			return
		case <-sub.stop:
			return
		case ev := <-sub.ch:
			if dropped := sub.dropped.Swap(0); dropped > 0 {
				if err := d.writeEventLine(cs, streamEvent{Type: EventGap, Dropped: dropped}); err != nil {
					cs.drop()
					return
				}
			}
			if err := d.writeEventLine(cs, ev); err != nil {
				cs.drop()
				return
			}
		}
	}
}

// writeEventLine serializes ev as one newline-terminated JSON line and writes it
// under the connection's send mutex with a write deadline.
func (d *Daemon) writeEventLine(cs *connState, ev streamEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	cs.sendMu.Lock()
	defer cs.sendMu.Unlock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, werr := cs.conn.Write(data)
	return werr
}

// verbWaitFor blocks until a condition matches or a timeout elapses, returning a
// wait_result on match and a timeout error otherwise. It is sugar over a
// short-lived internal hub subscription, so a caller need not poll capture-pane.
func (d *Daemon) verbWaitFor(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Condition string `json:"condition"`
		Session   string `json:"session"`
		Window    string `json:"window"`
		Pattern   string `json:"pattern"`
		Source    string `json:"source"`
		Until     string `json:"until"`
		Idle      int    `json:"idle"`
		Thread    uint64 `json:"thread"`
		Timeout   int    `json:"timeout"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	timeout := defaultWaitTimeout
	if p.Timeout > 0 {
		timeout = time.Duration(p.Timeout) * time.Millisecond
	}
	deadline := time.After(timeout)

	switch p.Condition {
	case "session-exists":
		return d.waitSessionExists(p.Session, deadline)
	case "window-output":
		return d.waitWindowOutput(p.Session, p.Window, p.Pattern, p.Source, deadline)
	case "window-exit":
		return d.waitWindowExit(p.Session, p.Window, deadline)
	case "window-idle":
		return d.waitWindowIdle(p.Session, p.Window, p.Idle, deadline)
	case "agent-state":
		return d.waitAgentState(p.Session, p.Window, p.Until, deadline)
	case "agent-message":
		return d.waitAgentMessage(p.Session, p.Window, p.Thread, deadline)
	default:
		message := "unknown condition " + p.Condition
		if p.Condition == "" {
			message = "condition is required"
		}
		return nil, hintedVerbError(ErrVerbInvalidParams, message, &VerbHint{
			Param:      "condition",
			Accepted:   waitConditions,
			DidYouMean: closestMatch(p.Condition, waitConditions),
		})
	}
}

// waitMatched builds a successful wait_result for the given condition.
func waitMatched(condition string, extra map[string]any) map[string]any {
	res := map[string]any{"type": "wait_result", "condition": condition, "matched": true}
	maps.Copy(res, extra)
	return res
}

// waitSessionExists resolves when a session named name exists. It subscribes
// before the initial check so a session created in the race window is not missed.
func (d *Daemon) waitSessionExists(name string, deadline <-chan time.Time) (any, *verbError) {
	if name == "" {
		return nil, invalidParam("session", "session is required for the session-exists condition")
	}
	sub := d.events.subscribe(eventFilter{
		session: name,
		types:   map[string]bool{EventSessionCreated: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	if d.manager.GetSession(name) != nil {
		return waitMatched("session-exists", map[string]any{"session": name}), nil
	}
	for {
		select {
		case <-deadline:
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for session "+name, &VerbHint{
				Param:  "timeout",
				Verb:   "list-sessions",
				Detail: "The session was never created within the timeout. Check the name, or raise timeout (milliseconds).",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			if d.manager.GetSession(name) != nil {
				return waitMatched("session-exists", map[string]any{"session": name}), nil
			}
		}
	}
}

// waitWindowExit resolves when the target window's shell process exits.
func (d *Daemon) waitWindowExit(sessionName, window string, deadline <-chan time.Time) (any, *verbError) {
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}

	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
		ptyID:   pty.ID,
		types:   map[string]bool{EventWindowExit: true, EventWindowClosed: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	if pty.IsExited() {
		return waitMatched("window-exit", map[string]any{"window": window}), nil
	}
	for {
		select {
		case <-deadline:
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for window "+window+" to exit", &VerbHint{
				Param:  "timeout",
				Detail: "The window's shell was still running when the timeout elapsed. Raise timeout (milliseconds), or send the command that ends it.",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			return waitMatched("window-exit", map[string]any{"window": window}), nil
		}
	}
}

// waitWindowIdle resolves when the target window produces no output for idleMs
// milliseconds. Each output event resets the idle timer.
func (d *Daemon) waitWindowIdle(sessionName, window string, idleMs int, deadline <-chan time.Time) (any, *verbError) {
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}

	idle := defaultIdleWindow
	if idleMs > 0 {
		idle = time.Duration(idleMs) * time.Millisecond
	}

	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
		ptyID:   pty.ID,
		types:   map[string]bool{EventOutput: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	timer := time.NewTimer(idle)
	defer timer.Stop()
	for {
		select {
		case <-deadline:
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for window "+window+" to go idle", &VerbHint{
				Param:  "idle",
				Detail: "The window never stayed quiet for the idle window. Raise timeout, or raise idle if the process outputs in bursts.",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			// Output arrived; restart the idle window.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idle)
		case <-timer.C:
			return waitMatched("window-idle", map[string]any{"window": window, "idle_ms": int(idle / time.Millisecond)}), nil
		}
	}
}

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
	sub := d.events.subscribe(eventFilter{session: sess.Name, types: types}, defaultEventQueue)
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

	if id, name, ok := check(); ok {
		return waitMatched("agent-state", map[string]any{"window": id, "state": name}), nil
	}
	for {
		select {
		case <-deadline:
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for agent state "+until, &VerbHint{
				Param:  "until",
				Verb:   "get-agent-state",
				Detail: "No agent reached the named state before the timeout. Read the current state, or raise timeout (milliseconds).",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case ev := <-sub.ch:
			if ev.Type == EventWindowClosed && targetID != "" && ev.Window == targetID {
				return nil, newVerbError(ErrVerbWindowNotFound, "the watched window closed before reaching "+until)
			}
			if id, name, ok := check(); ok {
				return waitMatched("agent-state", map[string]any{"window": id, "state": name}), nil
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
	for _, part := range strings.Split(until, ",") {
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

// waitWindowOutput resolves when the target window's captured content matches
// pattern. It subscribes and checks once before waiting (so already-present
// output matches immediately), then re-checks on each output event; a gap marker
// or dropped event cannot hang the wait because a low-rate backstop ticker also
// re-checks.
func (d *Daemon) waitWindowOutput(sessionName, window, pattern, source string, deadline <-chan time.Time) (any, *verbError) {
	if pattern == "" {
		return nil, invalidParam("pattern", "pattern is required for the window-output condition")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, "invalid pattern: "+err.Error(), &VerbHint{
			Param:  "pattern",
			Detail: "pattern is a Go regular expression (RE2 syntax).",
		})
	}
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}

	// Default to matching recent (scrollback-inclusive) content so output that has
	// already scrolled off the visible screen still matches; source "visible"
	// restricts to the current screen.
	scrollback := source != "visible"
	matches := func() bool { return re.MatchString(pty.CaptureContent(scrollback, false)) }

	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
		ptyID:   pty.ID,
		types:   map[string]bool{EventOutput: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	if matches() {
		return waitMatched("window-output", map[string]any{"window": window, "pattern": pattern}), nil
	}

	backstop := time.NewTicker(waitOutputRecheck)
	defer backstop.Stop()
	for {
		select {
		case <-deadline:
			return nil, hintedVerbError(ErrVerbTimeout, "timed out waiting for output matching "+pattern, &VerbHint{
				Param:  "pattern",
				Verb:   "capture-pane",
				Detail: "No output matched before the timeout. Capture the pane to see what it actually printed, then adjust the pattern or raise timeout.",
			})
		case <-d.ctx.Done():
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-sub.ch:
			if matches() {
				return waitMatched("window-output", map[string]any{"window": window, "pattern": pattern}), nil
			}
		case <-backstop.C:
			if matches() {
				return waitMatched("window-output", map[string]any{"window": window, "pattern": pattern}), nil
			}
		}
	}
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
		state := sess.GetState()
		idx, err := findWindowStateIndex(state.Windows, window)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		inbox = state.Windows[idx].ID
	}

	// The baseline is taken before the subscription so a message published in
	// the race window is newer than it, and is therefore matched rather than
	// missed.
	baseline := d.agents.highestID()

	// Any id in the thread names the thread, the same rule read-agent-messages
	// follows, so a caller can wait on the id of the message it just sent.
	thread = d.agents.resolveThread(sess.Name, thread)

	types := map[string]bool{EventAgentMessage: true}
	if inbox != "" {
		// A watched inbox closing has to fail the wait rather than run out the
		// clock: nothing will ever be delivered to it again.
		types[EventWindowClosed] = true
	}
	sub := d.events.subscribe(eventFilter{session: sess.Name, types: types}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	check := func() (AgentMessage, bool) {
		if inbox != "" {
			return d.agents.firstUnread(sess.Name, inbox, thread)
		}
		return d.agents.newerThan(sess.Name, baseline, thread)
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
