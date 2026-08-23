package session

import (
	"encoding/json"
	"strings"
	"time"
)

// This file implements the cross-agent verbs: who is here (list-agents), leaving
// a message (send-agent-message), reading one (read-agent-messages), and asking
// an agent a question and waiting for it to answer (ask-agent).
//
// The addressing scheme is deliberately not a new one. An agent is a window, and
// a window is already addressable by uuid, unique id prefix, list index or exact
// name, which is what every other window-targeted verb takes. Inventing a second
// namespace for agents would mean two ways to name the same pane and a rule for
// when they disagree. Discovery is list-agents, so an agent finds its
// correspondents rather than being told them, and $TUIOS_PANE_ID is its own
// address.
//
// An inbox therefore lives and dies with its window. A message addressed to a
// window that has since closed reads back undeliverable rather than being handed
// to whatever pane later takes that name.

// agentRestStates are the states that mean an agent is not mid-turn, so a
// question sent to it will be read rather than typed over whatever it is doing.
// errored is in the set on purpose: an agent that stopped on an error is at its
// prompt and can be told about it.
var agentRestStates = map[string]bool{
	AgentStateNeedsInput.Name(): true,
	AgentStateIdle.Name():       true,
	AgentStateDone.Name():       true,
	AgentStateErrored.Name():    true,
	AgentStateNone.Name():       true,
}

// askDefaults bound the three waits ask-agent performs.
const (
	askDefaultReadyTimeout = 30 * time.Second
	askDefaultSettle       = 2 * time.Second
	askDefaultTimeout      = 300 * time.Second
	askDefaultLines        = 200
)

// windowLabelOf is the name a human would call a window: the name someone gave
// it, else the title its shell set.
func windowLabelOf(w WindowState) string {
	if w.CustomName != "" {
		return w.CustomName
	}
	return w.Title
}

// isAgentWindow reports whether a pane looks like it is running an agent at all.
// Any tier having an opinion is enough: a reported state, a named harness, or
// the foreground detector having promoted the pane.
func isAgentWindow(w WindowState) bool {
	return w.AgentState != AgentStateNone || w.AgentHarness != ""
}

// verbListAgents reports the agent panes in a session: who is there, what each
// one is doing, and how much unread mail is waiting for it.
//
// It adds no state of its own. Every field except the unread count is already
// tracked per window; the verb exists because an agent that wants to talk to
// another agent had no way to discover one without listing every window and
// working out which were agents.
func (d *Daemon) verbListAgents(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		All     bool   `json:"all"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	state := sess.GetState()
	unread := d.agents.unreadCounts(sess.Name)

	agents := make([]map[string]any, 0, len(state.Windows))
	for i := range state.Windows {
		w := state.Windows[i]
		if !p.All && !isAgentWindow(w) {
			continue
		}
		claim := sess.agentClaimFor(w.ID)
		agents = append(agents, map[string]any{
			"window_id":      w.ID,
			"name":           windowLabelOf(w),
			"state":          w.AgentState.Name(),
			"message":        w.AgentMessage,
			"agent_state_at": w.AgentStateAt,
			"source":         claim.source.Name(),
			"harness_id":     firstNonEmpty(w.AgentHarness, claim.harness),
			"foreground":     w.ForegroundCmd,
			"cwd":            w.Cwd,
			"workspace":      w.Workspace,
			"focused":        w.ID == state.FocusedWindowID,
			"unread":         unread[w.ID],
			"ready":          agentRestStates[w.AgentState.Name()],
		})
	}

	return map[string]any{
		"type":    "agent_list",
		"session": sess.Name,
		"agents":  agents,
		"total":   len(agents),
	}, nil
}

// firstNonEmpty returns the first argument that is not empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// verbSendAgentMessage puts a message in a session's ring, addressed to one
// window's inbox or, with no recipient, to the session as a notice.
//
// It does not touch the recipient's keyboard. That is the whole point of having
// a queue: a message can be left for an agent that is mid-turn, which is exactly
// when typing at it would be wrong.
func (d *Daemon) verbSendAgentMessage(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session     string   `json:"session"`
		To          string   `json:"to"`
		From        string   `json:"from"`
		Subject     string   `json:"subject"`
		Text        string   `json:"text"`
		Attachments []string `json:"attachments"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Text) == "" {
		return nil, invalidParam("text", "text is required: a message with no body tells the reader nothing")
	}
	if len(p.Text) > agentMsgMaxText {
		return nil, hintedVerbError(ErrVerbInvalidParams, "text is longer than the message cap", &VerbHint{
			Param:  "text",
			Detail: "A message body is capped at 8 KiB. Write the long form to a file and attach the path instead.",
		})
	}
	if len(p.Subject) > agentMsgMaxSubject {
		return nil, invalidParam("subject", "subject is longer than 120 characters")
	}
	if len(p.Attachments) > agentMsgMaxAttachments {
		return nil, invalidParam("attachments", "a message carries at most 8 attachments")
	}

	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()

	msg := AgentMessage{Kind: agentMsgNotice, Text: p.Text, Subject: p.Subject}

	if p.From != "" {
		idx, err := findWindowStateIndex(state.Windows, p.From)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		msg.From = state.Windows[idx].ID
		msg.FromLabel = windowLabelOf(state.Windows[idx])
	}

	if p.To != "" {
		idx, err := findWindowStateIndex(state.Windows, p.To)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		msg.Kind = agentMsgDirect
		msg.To = state.Windows[idx].ID
		msg.ToLabel = windowLabelOf(state.Windows[idx])
	}

	// A pane messaging itself is the shortest loop there is, and no legitimate
	// caller writes it: an agent that wants to remember something writes a file.
	if msg.To != "" && msg.To == msg.From {
		return nil, hintedVerbError(ErrVerbLoopRefused, "a pane cannot send a message to itself", &VerbHint{
			Param:  "to",
			Detail: "Sender and recipient resolve to the same window. Write a note to a file instead of into your own inbox.",
		})
	}

	for _, path := range p.Attachments {
		att, err := classifyAttachment(path)
		if err != nil {
			return nil, hintedVerbError(ErrVerbInvalidParams, "attachment "+echoName(path)+": "+err.Error(), &VerbHint{
				Param:  "attachments",
				Detail: "An attachment is an absolute path to an existing file on the daemon's host. The queue stores the path, never the bytes, so the file has to be there when the reader looks.",
			})
		}
		msg.Attachments = append(msg.Attachments, att)
	}

	// The rate cap is charged after validation so a caller cannot burn its
	// budget on calls that were never going to be delivered.
	if !d.agents.checkRate(sess.Name, msg.From) {
		return nil, hintedVerbError(ErrVerbRateLimited, "this sender is over the message rate cap", &VerbHint{
			Command: "tuios read-agent-messages",
			Detail:  "A sender gets 10 messages back to back and 30 a minute after that. Hitting the cap almost always means two agents are answering each other in a loop; read the ring before sending again.",
		})
	}

	stored := d.agents.send(sess.Name, msg)

	// The event carries only what a subscriber needs to filter on. Everything
	// else is read back from the ring, the discipline the other waits follow: a
	// payload that is trusted rather than re-read goes stale the moment anything
	// about the message changes.
	d.events.publish(streamEvent{
		Type:    EventAgentMessage,
		Session: sess.Name,
		Window:  stored.To,
	})

	return map[string]any{
		"type":       "agent_message_sent",
		"session":    sess.Name,
		"message_id": stored.ID,
		"kind":       stored.Kind,
		"to":         stored.To,
		"to_name":    stored.ToLabel,
		"from":       stored.From,
		"sent_at":    stored.SentAt,
	}, nil
}

// verbReadAgentMessages reads a session's ring.
//
// Reading marks a directed message read; it does not consume it. A consumed
// message would leave nothing behind for a human, or for the next agent trying
// to work out what happened, and the ring's cap already bounds what is kept.
func (d *Daemon) verbReadAgentMessages(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		To      string `json:"to"`
		Unread  bool   `json:"unread"`
		Notices bool   `json:"notices"`
		Peek    bool   `json:"peek"`
		Limit   int    `json:"limit"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()

	q := readQuery{unreadOnly: p.Unread, notices: p.Notices, peek: p.Peek, limit: p.Limit}
	if p.To != "" {
		idx, err := findWindowStateIndex(state.Windows, p.To)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		q.inbox = state.Windows[idx].ID
	}
	live := map[string]bool{}
	for i := range state.Windows {
		live[state.Windows[i].ID] = true
	}
	q.live = func(id string) bool { return live[id] }

	res := d.agents.read(sess.Name, q)

	return map[string]any{
		"type":    "agent_messages",
		"session": sess.Name,
		"inbox":   q.inbox,
		// untrusted is a constant, and that is deliberate. Every body here was
		// written by something other than the reader, so a consumer that keys on
		// this field is right every time, and a consumer that never read the
		// skill trips over it in the shape of the answer.
		"untrusted": true,
		"messages":  res.Messages,
		"unread":    res.Unread,
		"total":     res.Total,
		"evicted":   res.Evicted,
	}, nil
}

// verbAskAgent is the composition that turns "type into a pane" into "ask
// another agent a question": it waits until the target is not mid-turn, writes
// the question to its PTY, waits until the target has actually dealt with it,
// and answers with what the pane printed in between.
//
// It exists because the honest signal that a message landed is the target's
// state coming back to rest, and assembling that from send-text plus two
// wait-fors is the composition every caller would otherwise write, incorrectly:
// the naive version returns as soon as the pane is quiet, which for an agent
// that thinks before it types is immediately.
//
// It is also the only half of this feature that works with the agents that exist
// today. None of them read a tuios mailbox; all of them read their keyboard.
func (d *Daemon) verbAskAgent(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session      string `json:"session"`
		Window       string `json:"window"`
		From         string `json:"from"`
		Text         string `json:"text"`
		ReadyTimeout int    `json:"ready_timeout"`
		Settle       int    `json:"settle"`
		Timeout      int    `json:"timeout"`
		Lines        int    `json:"lines"`
		Force        bool   `json:"force"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Text) == "" {
		return nil, invalidParam("text", "text is required: there is no question to ask")
	}
	if p.Window == "" {
		return nil, invalidParam("window", "window is required: name the agent to ask")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	target := state.Windows[idx]

	from := ""
	if p.From != "" {
		fidx, ferr := findWindowStateIndex(state.Windows, p.From)
		if ferr != nil {
			return nil, mapResolveErr(ferr, sess)
		}
		from = state.Windows[fidx].ID
	}
	if from != "" && from == target.ID {
		return nil, hintedVerbError(ErrVerbLoopRefused, "a pane cannot ask itself", &VerbHint{
			Param:  "window",
			Detail: "The caller and the target resolve to the same window.",
		})
	}

	// The cycle guard runs before anything is typed. Releasing the edge is
	// deferred so a wait that times out does not leave the graph claiming an ask
	// is still open.
	if !d.agents.openAsk(from, target.ID) {
		detail := "The target is already waiting, directly or through another agent, on the pane making this call, so answering would leave both sides blocked on each other. Leave a message instead: send-agent-message does not block."
		if edges := d.agents.openAskEdges(); len(edges) > 0 {
			detail += " Asks in flight: " + strings.Join(edges, ", ") + "."
		}
		return nil, hintedVerbError(ErrVerbLoopRefused, "this ask would close a loop with one already in flight", &VerbHint{
			Command: "tuios send-agent-message -w " + shortWindowID(target.ID) + " '<what you wanted to ask>'",
			Detail:  detail,
		})
	}
	defer d.agents.closeAsk(from, target.ID)

	pty, err := d.resolvePTYForTarget(sess, target.ID)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}

	readyTimeout := durationOr(p.ReadyTimeout, askDefaultReadyTimeout)
	settle := durationOr(p.Settle, askDefaultSettle)
	timeout := durationOr(p.Timeout, askDefaultTimeout)
	lines := p.Lines
	if lines <= 0 {
		lines = askDefaultLines
	}

	// Step one: do not type into an agent that is mid-turn. force skips the
	// wait, and is the caller taking responsibility for interleaving its text
	// with whatever the target is doing.
	waitedFor := ""
	if !p.Force {
		reached, verr := d.waitAgentRest(sess, target.ID, readyTimeout)
		if verr != nil {
			return nil, verr
		}
		waitedFor = reached
	}

	// Step two: the baseline for the reply. Everything the pane prints from here
	// on is what it printed in answer.
	before := contentLines(pty.CaptureContent(true, false))

	text := p.Text
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if _, werr := pty.Write([]byte(text)); werr != nil {
		return nil, newVerbError(ErrVerbInternal, werr.Error())
	}
	sentAt := time.Now().UnixNano()

	// Step three: wait for the target to have dealt with it.
	settledBy, endState := d.waitAgentSettled(sess, target.ID, pty, sentAt, settle, timeout)

	after := pty.CaptureContent(true, false)
	reply, truncated := tailLines(after, before, lines)

	return map[string]any{
		"type":       "agent_reply",
		"session":    sess.Name,
		"window":     target.ID,
		"name":       windowLabelOf(target),
		"waited_for": waitedFor,
		"settled_by": settledBy,
		"state":      endState,
		// untrusted, as in read-agent-messages: this is another program's output.
		"untrusted": true,
		"reply":     reply,
		"lines":     countLines(reply),
		"truncated": truncated,
	}, nil
}

// durationOr converts a millisecond parameter to a duration, falling back to a
// default when the caller passed nothing.
func durationOr(ms int, fallback time.Duration) time.Duration {
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return fallback
}

// waitAgentRest blocks until the window is in a state that means it is not
// mid-turn, and reports which state that was.
func (d *Daemon) waitAgentRest(sess *Session, windowID string, timeout time.Duration) (string, *verbError) {
	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
		types:   map[string]bool{EventAgentState: true, EventWindowClosed: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	check := func() (string, bool) {
		st := sess.GetState()
		i, err := findWindowStateIndex(st.Windows, windowID)
		if err != nil {
			return "", false
		}
		name := st.Windows[i].AgentState.Name()
		return name, agentRestStates[name]
	}
	if name, ok := check(); ok {
		return name, nil
	}

	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			return "", hintedVerbError(ErrVerbNotReady, "the target agent was still working when the ready timeout elapsed", &VerbHint{
				Param:   "ready_timeout",
				Command: "tuios wait-for agent-state -w " + shortWindowID(windowID) + " --until idle,needs_input,done",
				Detail:  "Typing at an agent mid-turn interleaves with what it is doing. Wait for it to come to rest, raise ready_timeout, leave a message with send-agent-message instead, or pass force to send anyway.",
			})
		case <-d.ctx.Done():
			return "", newVerbError(ErrVerbInternal, "daemon is shutting down")
		case ev := <-sub.ch:
			if ev.Type == EventWindowClosed && ev.Window == windowID {
				return "", newVerbError(ErrVerbWindowNotFound, "the target window closed before it was ready")
			}
			if name, ok := check(); ok {
				return name, nil
			}
		}
	}
}

// waitAgentSettled blocks until the target has finished dealing with what was
// just sent, and reports which of the two signals ended the wait.
//
// Two signals rather than one, because neither is sufficient alone. An agent
// that reports its own state says so contractually, and that is the honest
// answer; but most panes report nothing, and for those the only evidence is the
// pane going quiet. Quiet alone is wrong for a reporting agent, which is silent
// while it thinks. So: whichever arrives first, and the answer says which.
func (d *Daemon) waitAgentSettled(sess *Session, windowID string, pty *PTY, sentAt int64, settle, timeout time.Duration) (string, string) {
	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
		ptyID:   pty.ID,
		types:   map[string]bool{EventOutput: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	stateSub := d.events.subscribe(eventFilter{
		session: sess.Name,
		types:   map[string]bool{EventAgentState: true, EventWindowClosed: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(stateSub)

	currentState := func() string {
		st := sess.GetState()
		i, err := findWindowStateIndex(st.Windows, windowID)
		if err != nil {
			return AgentStateNone.Name()
		}
		return st.Windows[i].AgentState.Name()
	}

	deadline := time.After(timeout)
	timer := time.NewTimer(settle)
	defer timer.Stop()

	for {
		select {
		case <-deadline:
			return "timeout", currentState()
		case <-d.ctx.Done():
			return "shutdown", currentState()
		case <-sub.ch:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(settle)
		case ev := <-stateSub.ch:
			if ev.Type == EventWindowClosed && ev.Window == windowID {
				return "window-closed", AgentStateNone.Name()
			}
			// Only a report stamped after the question was sent says anything
			// about this question. A stale rest state is the pane not having
			// noticed yet, and returning on it is the bug this guards.
			if ev.Time <= sentAt {
				continue
			}
			if name := currentState(); agentRestStates[name] && name != AgentStateNone.Name() {
				return "agent-state", name
			}
		case <-timer.C:
			return "idle", currentState()
		}
	}
}

// contentLines counts a capture up to its last line with anything on it.
//
// A capture is the pane's full height, so it ends in the blank rows below the
// cursor. Counting those would give a baseline that is the pane's height rather
// than what it has printed, and a baseline that never moves makes every reply
// empty. This is the same rule capture-pane's --lines already follows.
func contentLines(s string) int {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return i + 1
		}
	}
	return 0
}

// countLines counts the lines in a reply.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// tailLines returns the content that arrived after the first `before` content
// lines, capped to the newest maxLines, and reports whether anything was cut.
//
// A pane whose scrollback has wrapped can end up with fewer content lines than
// the baseline recorded. That reads as an empty reply rather than as the whole
// screen, which is the safe way to be wrong: a caller sees nothing and captures
// the pane, instead of being handed text from before it asked.
func tailLines(content string, before, maxLines int) (string, bool) {
	all := strings.Split(content, "\n")
	if n := contentLines(content); n < len(all) {
		all = all[:n]
	}
	if before < 0 {
		before = 0
	}
	if before > len(all) {
		before = len(all)
	}
	added := all[before:]
	truncated := false
	if len(added) > maxLines {
		added = added[len(added)-maxLines:]
		truncated = true
	}
	return strings.TrimRight(strings.Join(added, "\n"), "\n \t"), truncated
}
