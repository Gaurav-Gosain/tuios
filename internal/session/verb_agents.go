package session

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
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
//
// needs_input is not in the set. An agent on needs_input is most often sitting
// on a permission menu, and text typed there is read as the answer to the menu:
// the question approves or denies whatever the agent asked for. ask-agent
// refuses such a pane with agent_blocked unless the caller passes allow_blocked,
// and list-agents reports it as not ready.
var agentRestStates = map[string]bool{
	AgentStateIdle.Name():    true,
	AgentStateDone.Name():    true,
	AgentStateErrored.Name(): true,
	AgentStateNone.Name():    true,
	// unknown is not in the set. It is what the silence timer writes to a pane
	// that said nothing for the stall window when the screen showed nothing a
	// rule knows, and silence is also what a long tool call looks like, so
	// typing at such a pane can land in the middle of a turn. A harness with
	// rules that read its prompt box reaches idle instead, and a caller that
	// knows better passes force. It stays on the rail as a display state.
	//
	// fanReadyStates in verb_worktree.go is a subset of this set: it also
	// leaves out errored and none, because fan types a first prompt into an
	// agent it just started, and neither of those says the agent reached its
	// prompt. Neither set holds needs_input or unknown.
}

// agentReady reports whether a window's agent state is in the given ready set,
// with one exception for unknown. A harness whose manifest has an idle rule
// shows positive evidence when it is at its prompt, so for it unknown stays not
// ready. A harness without one can never reach idle from its screen, and
// without this its pane would never be ready for fan or ask-agent at all, only
// after the wait ends. For those, unknown is the best evidence of rest there is,
// as it was before idle rules existed.
func (d *Daemon) agentReady(w WindowState, set map[string]bool) bool {
	name := w.AgentState.Name()
	if set[name] {
		return true
	}
	if w.AgentState != AgentStateUnknown {
		return false
	}
	reg := d.agentMatcher.registry
	return reg == nil || !reg.CanProveIdle(w.AgentHarness)
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
	now := time.Now().UnixNano()

	agents := make([]map[string]any, 0, len(state.Windows))
	for i := range state.Windows {
		w := state.Windows[i]
		if !p.All && !isAgentWindow(w) {
			continue
		}
		claim := sess.agentClaimFor(w.ID)
		// An unset claim reads back as "report", because that is the default a
		// caller naming no source gets. Reporting it for a pane nothing has
		// claimed would say a pane at a shell prompt reported itself idle, so
		// the absence is shown as an absence.
		source := ""
		if isAgentWindow(w) {
			source = claim.source.Name()
		}
		agents = append(agents, map[string]any{
			"window_id":      w.ID,
			"name":           windowLabelOf(w),
			"state":          w.AgentState.Name(),
			"message":        w.AgentMessage,
			"agent_state_at": w.AgentStateAt,
			"source":         source,
			"harness_id":     firstNonEmpty(w.AgentHarness, claim.harness),
			"foreground":     w.ForegroundCmd,
			"cwd":            w.Cwd,
			"workspace":      w.Workspace,
			"focused":        w.ID == state.FocusedWindowID,
			"unread":         unread[w.ID],
			"ready":          d.agentReady(w, agentRestStates),
			"blocked_by":     agentBlockedBy(w),
			"needs_you":      w.AgentState.NeedsYou(),
			"confidence":     claim.identity.confidence(),
			// completion_seq counts the pane's finished turns, and
			// finished_unread says the latest one has not been in front of
			// anybody: no attached client has pushed state with the pane
			// focused since. See agent_turns.go.
			"completion_seq":  w.CompletionSeq,
			"finished_unread": sess.finishedUnread(&w),
			// The harness's own conversation id, empty until a hook reports
			// one. It is what a resume names.
			"agent_session_id": w.AgentSessionID,
			"meta":             agentMetaMap(w.AgentMeta, now),
		})
	}

	return map[string]any{
		"type":    "agent_list",
		"session": sess.Name,
		"agents":  agents,
		"total":   len(agents),
		// The person's inbox, which is not a row because it is not a pane: it
		// cannot be asked, focused or captured, and a row would invite all three.
		// It is addressed as "human" and read from the attached client's mail
		// overlay.
		"human_inbox":  AgentInboxHuman,
		"human_unread": unread[AgentInboxHuman],
	}, nil
}

// resolveMailParty turns a send, read or wait target into an inbox id and the
// label to print for it. "human" is the person at the attached client and
// resolves to itself; anything else is a window, addressed the way every
// window verb addresses one. The reserved name wins over a window that happens
// to be called human, so an agent addressing the person always reaches them.
func resolveMailParty(state *SessionState, target string) (id, label string, err error) {
	if target == AgentInboxHuman {
		return AgentInboxHuman, AgentInboxHuman, nil
	}
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return "", "", err
	}
	return state.Windows[idx].ID, windowLabelOf(state.Windows[idx]), nil
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
func (d *Daemon) verbSendAgentMessage(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session     string   `json:"session"`
		To          string   `json:"to"`
		From        string   `json:"from"`
		FromHost    string   `json:"from_host"`
		Subject     string   `json:"subject"`
		Text        string   `json:"text"`
		ReplyTo     uint64   `json:"reply_to"`
		Attachments []string `json:"attachments"`
		HumanNonce  string   `json:"human_nonce"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	viaLink := cs != nil && cs.viaLink
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
	// An id past the last one issued names a message that has never existed, so
	// it is a caller mistake rather than the ring having forgotten. The two are
	// worth separating: a parent that aged out is normal and is threaded on the
	// id anyway, while a typed id is a reply nobody will ever find.
	if p.ReplyTo > d.agents.highestID() {
		return nil, hintedVerbError(ErrVerbInvalidParams, "reply_to names a message that has never existed", &VerbHint{
			Param:   "reply_to",
			Command: "tuios read-agent-messages",
			Detail:  "No message has been sent with that id. Read the ring to find the id you meant to answer.",
		})
	}

	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()

	msg := AgentMessage{Kind: agentMsgNotice, Text: p.Text, Subject: p.Subject, ReplyTo: p.ReplyTo}

	switch {
	case viaLink:
		// The sender is on another machine, so it is not a window here and
		// its name is not resolved against this session: it is kept as the
		// label it claimed, and the message is marked with where it came
		// from. The one name that is honoured is human, because the person
		// at a client attached through a link is the person at the attached
		// client. Their reply still carries the origin mark.
		msg.Origin = AgentOriginLink
		msg.OriginHost = printableClaim(p.FromHost, agentMsgMaxHostName)
		if p.From == AgentInboxHuman {
			msg.From, msg.FromLabel = AgentInboxHuman, AgentInboxHuman
		} else {
			msg.FromLabel = printableClaim(p.From, agentMsgMaxSubject)
		}
	case p.From != "":
		id, label, err := resolveMailParty(state, p.From)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		msg.From, msg.FromLabel = id, label
	}

	// A message from human is verified only when it carries the nonce of a
	// client attached to this session now; see human_sender.go. The link and
	// the local socket are checked the same way, each against its own attaches.
	if msg.From == AgentInboxHuman {
		msg.VerifiedHuman = d.verifyHumanNonce(p.HumanNonce, sess.ID, viaLink)
		msg.ClaimedHuman = !msg.VerifiedHuman
	}

	if p.To != "" {
		id, label, err := resolveMailParty(state, p.To)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		msg.Kind = agentMsgDirect
		msg.To, msg.ToLabel = id, label
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
		// A path from another machine names a file on this one, and the only
		// files another machine may name here are the ones it put in the
		// stash. Anything else is refused before it is looked at, so a
		// remote sender cannot use the missing flag to ask whether a file
		// exists on this machine.
		if viaLink && !d.stash.owns(sess.ID, path) {
			return nil, hintedVerbError(ErrVerbInvalidParams, "attachment "+echoName(path)+": a message from another machine can attach only a stashed file", &VerbHint{
				Param:   "attachments",
				Command: "tuios stash put",
				Detail:  "Put the file in this session's stash first and attach the path the stash printed.",
			})
		}
		att, err := classifyAttachment(path)
		if err != nil {
			return nil, hintedVerbError(ErrVerbInvalidParams, "attachment "+echoName(path)+": "+err.Error(), &VerbHint{
				Param:  "attachments",
				Detail: "An attachment is an absolute path to an existing file on the daemon's host. The queue stores the path, never the bytes, so the file has to be there when the reader looks.",
			})
		}
		att.Stashed = d.stash.owns(sess.ID, att.Path)
		msg.Attachments = append(msg.Attachments, att)
	}

	// The rate cap is charged after validation so a caller cannot burn its
	// budget on calls that were never going to be delivered. A sender on
	// another machine has its own bucket, keyed on the name it claims, so a
	// flood from a link cannot spend the anonymous local bucket.
	sender := msg.From
	if viaLink {
		sender = "link:" + msg.OriginHost + ":" + msg.FromLabel
	}
	if !d.agents.checkRate(sess.Name, sender) {
		return nil, hintedVerbError(ErrVerbRateLimited, "this sender is over the message rate cap", &VerbHint{
			Command: "tuios read-agent-messages",
			Detail:  "A sender gets 10 messages back to back and 30 a minute after that. Hitting the cap almost always means two agents are answering each other in a loop; read the ring before sending again.",
		})
	}
	// And what other machines can leave waiting is bounded on its own, so a
	// link cannot fill the ring with mail nobody here asked for.
	if viaLink {
		unread, notices := d.agents.linkQueued(sess.Name)
		if msg.Kind == agentMsgDirect && unread >= agentLinkMaxQueued {
			return nil, hintedVerbError(ErrVerbRateLimited, "this session holds "+strconv.Itoa(unread)+" unread messages from other machines, which is the cap", &VerbHint{
				Command: "tuios read-agent-messages",
				Detail:  "This machine holds a bounded number of unread messages from other machines. Wait for the recipient to read its inbox, then send again.",
			})
		}
		if msg.Kind == agentMsgNotice && notices >= agentLinkMaxQueued {
			return nil, hintedVerbError(ErrVerbRateLimited, "this session holds "+strconv.Itoa(notices)+" notices from other machines, which is the cap", &VerbHint{
				Param:  "to",
				Detail: "A notice from another machine is kept until the ring drops it. Send a message to one window instead.",
			})
		}
	}

	stored := d.agents.send(sess.Name, msg)

	// The attached clients get the whole message, not only the event: they
	// are the readers that cannot come back and read the ring on their own
	// schedule, because the person they draw for is not polling anything.
	d.broadcastToSession(sess.ID, MsgAgentMail, &AgentMailPayload{Message: stored}, "")

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
		"reply_to":   stored.ReplyTo,
		// The thread is reported on every send, not only on a reply, because a
		// message that starts a thread is the one whose id the next reply needs.
		"thread_id": stored.ThreadID,
		// True when the message being answered had already been dropped from the
		// ring. The reply still stands, and it is threaded on the id it named.
		"reply_to_missing": stored.ReplyToMissing,
		// origin is link when the send arrived from another machine. The
		// daemon decides it from the connection; the sender cannot.
		"origin":      stored.Origin,
		"origin_host": stored.OriginHost,
		// For a send from human: whether the daemon verified it came from an
		// attached client, or stored it as a claim.
		"verified_human": stored.VerifiedHuman,
		"claimed_human":  stored.ClaimedHuman,
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
		Thread  uint64 `json:"thread"`
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
	// The filter takes any id in the thread, not only the root's, so a caller
	// that read a reply can pass the id it has rather than tracing back to the
	// first message.
	q.thread = d.agents.resolveThread(sess.Name, p.Thread)
	if p.To != "" {
		id, _, err := resolveMailParty(state, p.To)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		q.inbox = id
	}
	// The person's inbox is always live: it has no window to close.
	live := map[string]bool{AgentInboxHuman: true}
	for i := range state.Windows {
		live[state.Windows[i].ID] = true
	}
	q.live = func(id string) bool { return live[id] }

	res := d.agents.read(sess.Name, q)

	// A read that marked something read is news to the attached clients: the
	// unread count they draw beside a pane just changed, and nothing else would
	// tell them.
	var marked []uint64
	var readAt int64
	for _, m := range res.Messages {
		if m.WasUnread && m.ReadAt != 0 {
			marked = append(marked, m.ID)
			readAt = m.ReadAt
		}
	}
	if len(marked) > 0 {
		d.broadcastToSession(sess.ID, MsgAgentMail, &AgentMailPayload{ReadIDs: marked, ReadAt: readAt}, "")
	}

	return map[string]any{
		"type":    "agent_messages",
		"session": sess.Name,
		"inbox":   q.inbox,
		// The thread the filter resolved to, zero when the read was not filtered.
		// It can differ from what the caller passed: any id in the thread names
		// the thread, and this is the one it named.
		"thread": q.thread,
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
func (d *Daemon) verbAskAgent(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session      string `json:"session"`
		Window       string `json:"window"`
		From         string `json:"from"`
		FromHost     string `json:"from_host"`
		Text         string `json:"text"`
		ReadyTimeout int    `json:"ready_timeout"`
		Settle       int    `json:"settle"`
		Timeout      int    `json:"timeout"`
		Lines        int    `json:"lines"`
		Force        bool   `json:"force"`
		AllowBlocked bool   `json:"allow_blocked"`
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
	// The person has an inbox and no keyboard, so a question for them is left
	// as mail and answered from the client, never typed anywhere.
	if p.Window == AgentInboxHuman {
		return nil, hintedVerbError(ErrVerbNoKeyboard, "human has no pane to type into", &VerbHint{
			Param:   "window",
			Command: "tuios send-agent-message -w human '<your question>'",
			Detail:  "human is the person at the attached client. They read mail in the tuios mail overlay and reply from it. Send the question with send-agent-message -w human, then wait-for agent-message on your own inbox.",
		})
	}
	idx, err := findWindowStateIndex(state.Windows, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	target := state.Windows[idx]

	from, fromLabel := "", ""
	viaLink := cs != nil && cs.viaLink
	switch {
	case viaLink:
		// As in send-agent-message: a caller on another machine is not a
		// window here, so its name is a label and never resolved.
		fromLabel = printableClaim(p.From, agentMsgMaxSubject)
	case p.From != "":
		fid, flabel, ferr := resolveMailParty(state, p.From)
		if ferr != nil {
			return nil, mapResolveErr(ferr, sess)
		}
		from, fromLabel = fid, flabel
	}
	origin := askOrigin{}
	if viaLink {
		origin = askOrigin{origin: AgentOriginLink, host: printableClaim(p.FromHost, agentMsgMaxHostName)}
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

	// Step one: do not type into an agent that is blocked on a prompt, because
	// the text would answer the prompt. This holds with force too: force skips
	// the wait for a working agent, and only allow_blocked says the caller knows
	// the prompt takes free text.
	if !p.AllowBlocked {
		if verr := d.refuseBlockedAgent(sess, target.ID); verr != nil {
			return nil, verr
		}
	}

	// Step two: do not type into an agent that is mid-turn. force skips the
	// wait, and is the caller taking responsibility for interleaving its text
	// with whatever the target is doing.
	waitedFor := ""
	if !p.Force {
		reached, verr := d.waitAgentRest(sess, target.ID, readyTimeout, p.AllowBlocked)
		if verr != nil {
			return nil, verr
		}
		waitedFor = reached
	}

	// Step three: the baseline for the reply. Everything the pane prints from
	// here on is what it printed in answer.
	before := contentLines(pty.CaptureContent(true, false))

	// The pane is checked once more right before anything is typed, since a
	// prompt can have come up while the wait above returned or the baseline was
	// read. Nothing has been written yet, so a refusal here still leaves the
	// pane untouched.
	if !p.AllowBlocked {
		if verr := d.refuseBlockedAgent(sess, target.ID); verr != nil {
			return nil, verr
		}
	}
	// Pasted and submitted with a carriage return, the way fan types its
	// prompt. See prompt_submit.go.
	if werr := submitPrompt(d.ctx, pty, p.Text); werr != nil {
		return nil, newVerbError(ErrVerbInternal, werr.Error())
	}
	sentAt := time.Now().UnixNano()

	// Step four: wait for the target to have dealt with it.
	settledBy, endState := d.waitAgentSettled(sess, target.ID, pty, sentAt, settle, timeout)

	after := pty.CaptureContent(true, false)
	reply, truncated := tailLines(after, before, lines)

	// The exchange goes in the ring once it is over, so the person at the
	// client can see what one agent asked another and what came back. It is
	// a record and not a delivery: nothing waits on it, nothing is unread
	// because of it, and the rate cap does not count it because the waits
	// above already bound how often an ask can run.
	d.recordAsk(sess, from, fromLabel, origin, target, p.Text, reply, settledBy)

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

// recordAsk stores one finished ask-agent exchange in the session's ring and
// pushes it to the attached clients. The question rides in the subject, cut to
// the subject cap, and the captured reply in the text, cut to the body cap.
func (d *Daemon) recordAsk(sess *Session, from, fromLabel string, origin askOrigin, target WindowState, question, reply, settledBy string) {
	if len(question) > agentMsgMaxSubject {
		question = question[:agentMsgMaxSubject]
	}
	if len(reply) > agentMsgMaxText {
		reply = reply[:agentMsgMaxText]
	}
	stored := d.agents.send(sess.Name, AgentMessage{
		Kind:       agentMsgAsk,
		From:       from,
		FromLabel:  fromLabel,
		Origin:     origin.origin,
		OriginHost: origin.host,
		To:         target.ID,
		ToLabel:    windowLabelOf(target),
		Subject:    question,
		Text:       reply,
		SettledBy:  settledBy,
	})
	d.broadcastToSession(sess.ID, MsgAgentMail, &AgentMailPayload{Message: stored}, "")
}

// askOrigin is where an ask came from, for the record it leaves: empty for
// this machine, AgentOriginLink and the claimed host for another.
type askOrigin struct {
	origin string
	host   string
}

// printableClaim bounds a name another machine claimed for itself or its
// sender: printable characters only, cut to limit. It is what keeps a claim
// from carrying a control sequence into a terminal that prints it.
func printableClaim(s string, limit int) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > limit {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// durationOr converts a millisecond parameter to a duration, falling back to a
// default when the caller passed nothing.
func durationOr(ms int, fallback time.Duration) time.Duration {
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return fallback
}

// agentBlockedError is the refusal ask-agent gives a pane on needs_input. It
// names what the pane waits on when that is known, and the remedy is always
// to look first: the prompt is on the pane's screen, and whoever answers it
// should have read it.
func agentBlockedError(w WindowState) *verbError {
	what := "a prompt"
	switch agentBlockedBy(w) {
	case harness.PromptKindApproval:
		what = "an approval"
	case harness.PromptKindQuestion:
		what = "a question"
	}
	msg := "the target agent is waiting on " + what + ", and text typed now would answer it"
	if note := printableClaim(w.AgentMessage, agentMsgMaxSubject); note != "" {
		msg += ": " + note
	}
	return hintedVerbError(ErrVerbAgentBlocked, msg, &VerbHint{
		Verb:    "capture-pane",
		Command: "tuios capture-pane -w " + shortWindowID(w.ID),
		Detail:  "Nothing was typed. Read the prompt with capture-pane first. Then answer it yourself with send-keys if answering it is yours to do, or ask the person with send-agent-message -w human. Pass allow_blocked only when you have read the prompt and it takes free text.",
	})
}

// refuseBlockedAgent returns agentBlockedError when the window is on
// needs_input now, and nil otherwise, including when the window is gone: the
// caller's own lookups report that.
func (d *Daemon) refuseBlockedAgent(sess *Session, windowID string) *verbError {
	st := sess.GetState()
	i, err := findWindowStateIndex(st.Windows, windowID)
	if err != nil {
		return nil
	}
	if st.Windows[i].AgentState == AgentStateNeedsInput {
		return agentBlockedError(st.Windows[i])
	}
	return nil
}

// waitAgentRest blocks until the window is in a state that means it is not
// mid-turn, and reports which state that was.
//
// A window that reaches needs_input ends the wait with agent_blocked, since
// waiting on does not help: the prompt stays until somebody answers it. With
// allowBlocked, needs_input counts as at rest instead, which is what every
// caller got before needs_input left agentRestStates.
func (d *Daemon) waitAgentRest(sess *Session, windowID string, timeout time.Duration, allowBlocked bool) (string, *verbError) {
	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
		types:   map[string]bool{EventAgentState: true, EventWindowClosed: true, EventSessionClosed: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	var blocked *verbError
	check := func() (string, bool) {
		st := sess.GetState()
		i, err := findWindowStateIndex(st.Windows, windowID)
		if err != nil {
			return "", false
		}
		w := st.Windows[i]
		name := w.AgentState.Name()
		if w.AgentState == AgentStateNeedsInput {
			if allowBlocked {
				return name, true
			}
			blocked = agentBlockedError(w)
			return name, false
		}
		return name, d.agentReady(w, agentRestStates)
	}
	if name, ok := check(); ok {
		return name, nil
	}
	if blocked != nil {
		return "", blocked
	}

	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			msg := "the target agent was still working when the ready timeout elapsed"
			detail := "Typing at an agent mid-turn interleaves with what it is doing. Wait for it to come to rest, raise ready_timeout, leave a message with send-agent-message instead, or pass force to send anyway."
			if name, _ := check(); name == AgentStateUnknown.Name() {
				msg = "the target agent's state was unknown when the ready timeout elapsed"
				detail = "unknown means the pane went quiet and nothing on its screen said whether the agent is at its prompt or in the middle of a long call, so it is not treated as ready. Look at the pane with capture-pane, pass force to send anyway, or leave a message with send-agent-message."
			}
			return "", hintedVerbError(ErrVerbNotReady, msg, &VerbHint{
				Param:   "ready_timeout",
				Command: "tuios wait-for agent-state -w " + shortWindowID(windowID) + " --until idle,needs_input,done",
				Detail:  detail,
			})
		case <-d.ctx.Done():
			return "", newVerbError(ErrVerbInternal, "daemon is shutting down")
		case ev := <-sub.ch:
			if ev.Type == EventSessionClosed {
				return "", newVerbError(ErrVerbSessionNotFound, "the session was killed before the target was ready")
			}
			if ev.Type == EventWindowClosed && ev.Window == windowID {
				return "", newVerbError(ErrVerbWindowNotFound, "the target window closed before it was ready")
			}
			if name, ok := check(); ok {
				return name, nil
			}
			if blocked != nil {
				return "", blocked
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
		types:   map[string]bool{EventAgentState: true, EventWindowClosed: true, EventSessionClosed: true},
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
			if ev.Type == EventSessionClosed {
				return "session-closed", AgentStateNone.Name()
			}
			if ev.Type == EventWindowClosed && ev.Window == windowID {
				return "window-closed", AgentStateNone.Name()
			}
			// Only a report stamped after the question was sent says anything
			// about this question. A stale rest state is the pane not having
			// noticed yet, and returning on it is the bug this guards.
			if ev.Time <= sentAt {
				continue
			}
			// needs_input ends the wait too, though it is not a rest state: an
			// agent that answers with a prompt of its own has dealt with the
			// question as far as it can, and the reply is what it printed.
			if name := currentState(); (agentRestStates[name] || name == AgentStateNeedsInput.Name()) && name != AgentStateNone.Name() {
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
	return countContent(strings.Split(s, "\n"))
}

// countContent is contentLines over an already-split capture, so a caller that
// needs both the count and the lines does not split a ten-thousand-line
// scrollback twice.
func countContent(lines []string) int {
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
	if n := countContent(all); n < len(all) {
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
