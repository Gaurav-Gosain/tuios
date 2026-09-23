package session

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
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
//
// With all_sessions it answers for every session on the daemon at once, which is
// what a hub asks a linked host so its listing covers more than the session that
// host last touched. Every row names its session either way.
func (d *Daemon) verbListAgents(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session     string `json:"session"`
		All         bool   `json:"all"`
		AllSessions bool   `json:"all_sessions"`
		Select      string `json:"select"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	var sel *Selector
	if p.Select != "" {
		var verr *verbError
		if sel, verr = d.parseVerbSelector(p.Select); verr != nil {
			return nil, verr
		}
	}
	if p.AllSessions || (sel != nil && p.Session == "") {
		if p.Session != "" {
			return nil, invalidParam("all_sessions", "all_sessions lists every session, so it takes no session. Drop one or the other")
		}
		return d.listAgentsAllSessions(p.All, sel), nil
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	unread := d.agents.unreadCounts(sess.Name)
	agents := d.agentRows(sess, p.All, unread, time.Now().UnixNano(), sel)

	out := map[string]any{
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
	}
	// Narrowed to one session, the rows are not what a write by the same
	// selector reaches, which is every session, so no token is handed out.
	addSelection(out, sel, agents, true)
	return out, nil
}

// addSelection puts the selector and its confirm token on a list-agents
// answer. The token is the one a write addressed by the same selector takes,
// so a caller can look here and write with it. noToken leaves it out: with
// all, since a write reaches agent panes only and the rows then include other
// windows, and for a listing narrowed to one session.
func addSelection(out map[string]any, sel *Selector, rows []map[string]any, noToken bool) {
	if sel == nil {
		return
	}
	out["select"] = sel.String()
	if noToken {
		return
	}
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		id, _ := r["window_id"].(string)
		keys = append(keys, localAttentionHost+"/"+id)
	}
	out["confirm"] = SelectionToken(keys)
}

// listAgentsAllSessions is list-agents over every session, in session name
// order. human_unread is the person's unread mail summed over the sessions.
// sel, when not nil, keeps only the rows it matches.
func (d *Daemon) listAgentsAllSessions(all bool, sel *Selector) map[string]any {
	sessions := d.manager.AllSessions()
	slices.SortFunc(sessions, func(a, b *Session) int { return strings.Compare(a.Name, b.Name) })
	now := time.Now().UnixNano()
	agents := make([]map[string]any, 0, len(sessions))
	humanUnread := 0
	for _, sess := range sessions {
		unread := d.agents.unreadCounts(sess.Name)
		humanUnread += unread[AgentInboxHuman]
		agents = append(agents, d.agentRows(sess, all, unread, now, sel)...)
	}
	out := map[string]any{
		"type":         "agent_list",
		"all_sessions": true,
		"agents":       agents,
		"total":        len(agents),
		"human_inbox":  AgentInboxHuman,
		"human_unread": humanUnread,
	}
	addSelection(out, sel, agents, all)
	return out
}

// agentRows is one session's rows of a list-agents answer. sel, when not nil,
// keeps only the rows it matches.
func (d *Daemon) agentRows(sess *Session, all bool, unread map[string]int, now int64, sel *Selector) []map[string]any {
	state := sess.GetState()
	group := ""
	if state.Worktree != nil {
		group = state.Worktree.Group
	}
	agents := make([]map[string]any, 0, len(state.Windows))
	for i := range state.Windows {
		w := state.Windows[i]
		if !all && !isAgentWindow(w) {
			continue
		}
		if sel != nil && !sel.Match(d.paneSelectorTarget(sess, w, group)) {
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
			"session":        sess.Name,
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
			// The fan-out group of the pane's session, empty outside one. It
			// is what a group: selector term reads.
			"group": group,
		})
	}
	return agents
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

// resolveSender is resolveMailParty for a sender. With anySession, a sender
// that is not in the session is looked for by its exact window id in every
// session, and labelled with the session it is in. Only the exact id is taken
// there: a name or a prefix means different panes in different sessions.
func (d *Daemon) resolveSender(state *SessionState, from string, anySession bool) (string, string, error) {
	id, label, err := resolveMailParty(state, from)
	if err == nil || !anySession {
		return id, label, err
	}
	for _, sess := range d.manager.AllSessions() {
		if w, ok := findWindowState(sess.GetState(), from); ok {
			return w.ID, windowLabelOf(w) + " in " + sess.Name, nil
		}
	}
	return "", "", err
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
	var p sendAgentMessageParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	p.raw = params
	if p.Select != "" {
		return d.sendAgentMessageSelect(cs, p)
	}
	if p.Confirm != "" {
		return nil, invalidParam("confirm", "confirm goes with select: it is the token for the panes a selector matched")
	}
	return d.sendAgentMessage(cs, p, false)
}

// sendAgentMessageParams are send-agent-message's parameters.
type sendAgentMessageParams struct {
	Session     string   `json:"session"`
	To          string   `json:"to"`
	From        string   `json:"from"`
	FromHost    string   `json:"from_host"`
	Subject     string   `json:"subject"`
	Text        string   `json:"text"`
	ReplyTo     uint64   `json:"reply_to"`
	Attachments []string `json:"attachments"`
	HumanNonce  string   `json:"human_nonce"`
	Host        string   `json:"host"`
	Select      string   `json:"select"`
	Confirm     string   `json:"confirm"`
	// raw is the call's params as sent, which a send with host forwards to
	// the far machine as they are, so an older daemon there sees only the
	// names the caller used.
	raw json.RawMessage
}

// sendAgentMessageSelect sends one message to every pane a selector matches,
// once the caller has confirmed the set (resolveSelection). Each pane gets its
// own directed message in its own session's ring, through the same checks a
// single send makes, rate cap included, so a broadcast costs the sender one
// message per pane. A pane that refuses (the sender itself, a closed window,
// the cap) is reported in its row and does not stop the others.
func (d *Daemon) sendAgentMessageSelect(cs *connState, p sendAgentMessageParams) (any, *verbError) {
	if p.To != "" {
		return nil, invalidParam("to", "select names the recipients, so it takes no to. Drop one or the other")
	}
	if p.Host != "" {
		return nil, invalidParam("host", "select reaches the panes of this machine and its linked hosts by its host: term, so it takes no host. Put a host: term in the selector instead")
	}
	if p.Session != "" {
		return nil, invalidParam("session", "select reaches every session, so it takes no session. Put a session: term in the selector instead")
	}
	if p.ReplyTo != 0 {
		return nil, invalidParam("reply_to", "a reply belongs to one thread in one session's ring, so it cannot be sent by selector")
	}
	if strings.TrimSpace(p.Text) == "" {
		return nil, invalidParam("text", "text is required: a message with no body tells the reader nothing")
	}
	panes, verr := d.resolveSelection(cs, p.Select, p.Confirm, selectWriteMax)
	if verr != nil {
		return nil, verr
	}
	results := make([]map[string]any, 0, len(panes))
	sent := 0
	for _, pane := range panes {
		q := p
		q.Select, q.Confirm = "", ""
		q.Session, q.To = pane.sess.Name, pane.window.ID
		row := map[string]any{"session": pane.sess.Name, "window": pane.window.ID, "name": windowLabelOf(pane.window)}
		res, verr := d.sendAgentMessage(cs, q, true)
		if verr != nil {
			row["ok"] = false
			row["error"] = verr
		} else {
			sent++
			row["ok"] = true
			row["message_id"] = res["message_id"]
			row["thread_id"] = res["thread_id"]
		}
		results = append(results, row)
	}
	return map[string]any{
		"type":    "agent_messages_sent",
		"select":  p.Select,
		"results": results,
		"sent":    sent,
		"failed":  len(results) - sent,
		"total":   len(results),
	}, nil
}

// sendAgentMessage is one send. fromAnySession lets from name a window of
// another session by its exact id, for a send by selector, where the sender's
// pane is in one session and the recipients are in many.
func (d *Daemon) sendAgentMessage(cs *connState, p sendAgentMessageParams, fromAnySession bool) (map[string]any, *verbError) {
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
	// A message for a session on another machine goes over this machine's
	// link to it, and waits here while the link is down. See host_outbox.go.
	if p.Host != "" {
		res, verr := d.sendAgentMessageToHost(cs, p.Host, p.Session, p.To, p.From, p.raw)
		if verr != nil {
			return nil, verr
		}
		out, _ := res.(map[string]any)
		return out, nil
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
		id, label, err := d.resolveSender(state, p.From, fromAnySession)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		msg.From, msg.FromLabel = id, label
	}

	// A process inside a pane of this daemon cannot speak as the person at
	// all, not even as a claim: whatever it was told, the answer it would be
	// forging is the one the person gives. See human_origin.go.
	if msg.From == AgentInboxHuman && !viaLink && !d.mayActAsHuman(cs) {
		return nil, humanForbiddenError("send-agent-message")
	}

	// A message from human is verified only when it carries the nonce of a
	// client attached to this session now; see human_sender.go. The link and
	// the local socket are checked the same way, each against its own attaches.
	if msg.From == AgentInboxHuman {
		msg.VerifiedHuman = d.verifyHumanNonce(p.HumanNonce, sess.ID, cs)
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

	// Mail from a machine whose link policy holds it goes to the person
	// instead, marked with who it was for, and reaches the agent only when
	// the person passes it on with release-agent-message. The recipient was
	// resolved above, so a send to a window that does not exist is still
	// refused rather than held.
	if viaLink && msg.To != AgentInboxHuman && d.linkPolicy(cs).HoldMail {
		msg.HeldFor, msg.HeldForLabel = msg.To, msg.ToLabel
		msg.Held = true
		msg.Kind = agentMsgDirect
		msg.To, msg.ToLabel = AgentInboxHuman, AgentInboxHuman
	}

	for _, path := range p.Attachments {
		// A path from another machine names a file on this one, and the only
		// files another machine may name here are the ones it put in the
		// stash. Anything else is refused before it is looked at, so a
		// remote sender cannot use the missing flag to ask whether a file
		// exists on this machine. A hosted pane's call (paneOnly) is held to
		// the same rule: its process, and the daemon that forwarded it, are
		// on the other machine too.
		if (viaLink || (cs != nil && cs.paneOnly)) && !d.stash.owns(sess.ID, path) {
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
	// Mail to the person waits in the Inbox until it is read.
	d.attention.noteMail(stored)

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
		// held is true when this machine's link policy put the message in
		// the person's Inbox instead of the recipient's. held_for is the
		// window it was for.
		"held":     stored.Held,
		"held_for": stored.HeldFor,
	}, nil
}

// verbReleaseAgentMessage passes a held message on to the agent it was for.
//
// Only the person may: the call needs the nonce of a client attached right
// now, as dismiss-attention does, and a process in a pane of this daemon is
// never issued one. Over a link it also needs respond. The message is sent
// again as a new message to the window it was held for, with the sender and
// the origin it arrived with, and the held copy is marked read so its Inbox
// item closes. A message is released once.
func (d *Daemon) verbReleaseAgentMessage(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session    string `json:"session"`
		ID         uint64 `json:"id"`
		HumanNonce string `json:"human_nonce"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.ID == 0 {
		return nil, invalidParam("id", "id is required: the message_id of the held message")
	}
	if !d.verifyAnyHumanNonce(p.HumanNonce, cs) {
		return nil, hintedVerbError(ErrVerbNotHuman, "release-agent-message is for the person at an attached client", &VerbHint{
			Param:  "human_nonce",
			Detail: "Mail from another machine is held so the person decides whether an agent sees it. Only a client attached right now can pass it on, with the nonce its attach reply carried.",
		})
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	held, ok := d.agents.takeHeld(sess.Name, p.ID)
	if !ok {
		return nil, hintedVerbError(ErrVerbInvalidParams, "no held message has id "+strconv.FormatUint(p.ID, 10)+" in this session", &VerbHint{
			Param:   "id",
			Command: "tuios read-agent-messages -w human",
			Detail:  "The message may already have been passed on, or dropped from the ring. Only a message marked held can be released.",
		})
	}
	// The window it was for may have closed while it was held.
	if held.HeldFor != "" {
		if _, _, err := resolveMailParty(sess.GetState(), held.HeldFor); err != nil {
			return nil, mapResolveErr(err, sess)
		}
	}
	out := AgentMessage{
		Kind: agentMsgNotice, From: held.From, FromLabel: held.FromLabel,
		To: held.HeldFor, ToLabel: held.HeldForLabel,
		Subject: held.Subject, Text: held.Text, ReplyTo: held.ReplyTo,
		Attachments: held.Attachments, Origin: held.Origin, OriginHost: held.OriginHost,
		VerifiedHuman: held.VerifiedHuman, ClaimedHuman: held.ClaimedHuman,
		ReleasedFrom: held.ID,
	}
	if out.To != "" {
		out.Kind = agentMsgDirect
	}
	if out.ReplyTo > d.agents.highestID() {
		out.ReplyTo = 0
	}
	stored := d.agents.send(sess.Name, out)
	d.broadcastToSession(sess.ID, MsgAgentMail, &AgentMailPayload{Message: stored}, "")
	d.events.publish(streamEvent{Type: EventAgentMessage, Session: sess.Name, Window: stored.To})
	// The held copy was marked read when it was taken. Only it: other mail to
	// the person in the same thread stays as it was.
	d.broadcastToSession(sess.ID, MsgAgentMail, &AgentMailPayload{ReadIDs: []uint64{held.ID}, ReadAt: held.ReadAt}, "")
	if _, unread := d.agents.firstUnread(sess.Name, AgentInboxHuman, held.ThreadID); !unread {
		d.attention.noteMailRead(sess.Name, held.ThreadID)
	}
	return map[string]any{
		"type":       "agent_message_released",
		"session":    sess.Name,
		"held_id":    held.ID,
		"message_id": stored.ID,
		"to":         stored.To,
		"to_name":    stored.ToLabel,
		"thread_id":  stored.ThreadID,
	}, nil
}

// verbReadAgentMessages reads a session's ring.
//
// Reading marks a directed message read; it does not consume it. A consumed
// message would leave nothing behind for a human, or for the next agent trying
// to work out what happened, and the ring's cap already bounds what is kept.
func (d *Daemon) verbReadAgentMessages(cs *connState, params json.RawMessage) (any, *verbError) {
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
	// Marking the person's mail read says the person has seen it, and the
	// unread count on their rail is how they learn there is mail at all. A
	// caller that may not act as the person reads it as a peek, so an agent
	// cannot clear the person's inbox by reading it. See human_origin.go.
	peekForced := false
	if q.inbox == AgentInboxHuman && !q.peek && !d.mayActAsHuman(cs) {
		q.peek, peekForced = true, true
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
	// A thread of the person's mail with nothing left unread in it is no
	// longer waiting in the Inbox.
	if q.inbox == AgentInboxHuman && len(marked) > 0 {
		threads := map[uint64]bool{}
		for _, m := range res.Messages {
			if m.WasUnread && m.ReadAt != 0 {
				threads[m.ThreadID] = true
			}
		}
		for thread := range threads {
			if _, unread := d.agents.firstUnread(sess.Name, AgentInboxHuman, thread); !unread {
				d.attention.noteMailRead(sess.Name, thread)
			}
		}
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
		// True when the read asked to mark the person's mail read and was
		// served as a peek instead, because the caller runs in a pane.
		"peek_forced": peekForced,
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
	var p askAgentParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Text) == "" {
		return nil, invalidParam("text", "text is required: there is no question to ask")
	}
	if p.Select != "" {
		return d.askAgentSelect(cs, p)
	}
	if p.Confirm != "" {
		return nil, invalidParam("confirm", "confirm goes with select: it is the token for the panes a selector matched")
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
	res, verr := d.askAgent(cs, sess, state, target, p, false)
	if verr != nil {
		return nil, verr
	}
	return res, nil
}

// askAgentParams are ask-agent's parameters.
type askAgentParams struct {
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
	StallTimeout int    `json:"stall_timeout"`
	Select       string `json:"select"`
	Confirm      string `json:"confirm"`
}

// askAgentSelect asks every pane a selector matches the same question, once
// the caller has confirmed the set (resolveSelection), and answers with each
// pane's reply. The asks run at once, each the whole single ask: it refuses a
// pane on needs_input with agent_blocked unless allow_blocked, waits for the
// pane to come to rest unless force, and records the exchange in that pane's
// session. A pane that refuses or fails is reported in its row and does not
// stop the others, so one blocked agent does not cost the caller every reply.
func (d *Daemon) askAgentSelect(cs *connState, p askAgentParams) (any, *verbError) {
	if p.Window != "" {
		return nil, invalidParam("window", "select names the agents to ask, so it takes no window. Drop one or the other")
	}
	if p.Session != "" {
		return nil, invalidParam("session", "select reaches every session, so it takes no session. Put a session: term in the selector instead")
	}
	panes, verr := d.resolveSelection(cs, p.Select, p.Confirm, selectAskMax)
	if verr != nil {
		return nil, verr
	}
	results := make([]map[string]any, len(panes))
	var wg sync.WaitGroup
	for i, pane := range panes {
		wg.Go(func() {
			row := map[string]any{"session": pane.sess.Name, "window": pane.window.ID, "name": windowLabelOf(pane.window)}
			// The pane is read again: the set was resolved a moment ago, and
			// a single ask reads its target at the time of the call too.
			state := pane.sess.GetState()
			target, ok := findWindowState(state, pane.window.ID)
			if !ok {
				row["ok"] = false
				row["error"] = newVerbError(ErrVerbWindowNotFound, "the pane closed before it was asked")
				results[i] = row
				return
			}
			res, verr := d.askAgent(cs, pane.sess, state, target, p, true)
			if verr != nil {
				row["ok"] = false
				row["error"] = verr
			} else {
				row["ok"] = true
				maps.Copy(row, res)
				delete(row, "type")
			}
			results[i] = row
		})
	}
	wg.Wait()
	answered := 0
	for _, r := range results {
		if r["ok"] == true {
			answered++
		}
	}
	return map[string]any{
		"type":     "agent_replies",
		"select":   p.Select,
		"replies":  results,
		"answered": answered,
		"failed":   len(results) - answered,
		"total":    len(results),
		// As in a single ask: every reply is another program's output.
		"untrusted": true,
	}, nil
}

// askAgent is one ask of target in sess. fromAnySession is resolveSender's.
func (d *Daemon) askAgent(cs *connState, sess *Session, state *SessionState, target WindowState, p askAgentParams, fromAnySession bool) (map[string]any, *verbError) {
	from, fromLabel := "", ""
	viaLink := cs != nil && cs.viaLink
	switch {
	case viaLink:
		// As in send-agent-message: a caller on another machine is not a
		// window here, so its name is a label and never resolved.
		fromLabel = printableClaim(p.From, agentMsgMaxSubject)
	case p.From != "":
		fid, flabel, ferr := d.resolveSender(state, p.From, fromAnySession)
		if ferr != nil {
			return nil, mapResolveErr(ferr, sess)
		}
		// The record of an ask from human reads as the person asking, so
		// a pane cannot leave one. See human_origin.go.
		if fid == AgentInboxHuman && !d.mayActAsHuman(cs) {
			return nil, humanForbiddenError("ask-agent")
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
	// prompt. See prompt_submit.go. The gate reads the pane first, so it can
	// tell afterwards whether the pane took the question. See prompt_gate.go.
	gate := d.newPromptGate(sess, target.ID)
	submittedAt, werr := submitPrompt(d.ctx, pty, p.Text, d.inputProfileFor(sess, target.ID))
	if werr != nil {
		return nil, newVerbError(ErrVerbInternal, werr.Error())
	}
	gate.markSubmitted(submittedAt)

	// Step four: wait for the target to have dealt with it.
	stall := durationOr(p.StallTimeout, d.promptStall())
	settledBy, endState := d.waitAgentSettled(sess, target.ID, pty, gate, settle, timeout, stall)

	after := pty.CaptureContent(true, false)
	reply, truncated := tailLines(after, before, lines)

	// The exchange goes in the ring once it is over, so the person at the
	// client can see what one agent asked another and what came back. It is
	// a record and not a delivery: nothing waits on it, nothing is unread
	// because of it, and the rate cap does not count it because the waits
	// above already bound how often an ask can run. A stalled ask is recorded
	// too, since the question was typed into the pane either way.
	d.recordAsk(sess, from, fromLabel, origin, target, p.Text, reply, settledBy)

	if settledBy == askSettledStalled {
		return nil, hintedVerbError(ErrVerbPromptStalled, gate.stalledMessage(stall)+"; its state is "+endState, &VerbHint{
			Param:   "stall_timeout",
			Verb:    "capture-pane",
			Command: "tuios capture-pane -w " + shortWindowID(target.ID),
			Detail:  "The question was typed and Enter was sent, so do not send it again without looking. Read the pane with capture-pane. If the question sits in the agent's input box, press Enter there with send-keys. If the agent is still starting, wait for it with wait-for agent-state and ask again. An agent that is slow to show it is working can be given more time with stall_timeout.",
		})
	}

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
//
// Neither ends the wait before the stall gate has seen the pane take the
// question, because a pane that never took it is also quiet and also at rest.
// If the gate has seen nothing when stall runs out, the wait ends with
// askSettledStalled. See prompt_gate.go.
func (d *Daemon) waitAgentSettled(sess *Session, windowID string, pty *PTY, gate *promptGate, settle, timeout, stall time.Duration) (string, string) {
	sentAt := gate.submittedAt
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
	resetSettle := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(settle)
	}

	// The gate is polled until it has seen the question taken, and then not at
	// all. taken latches, so the checks below are cheap once it has.
	taken := gate.check(sess, pty.LastOutput)
	stallTimer := time.NewTimer(stall)
	defer stallTimer.Stop()
	poll := time.NewTicker(promptGatePoll)
	defer poll.Stop()

	for {
		select {
		case <-deadline:
			return "timeout", currentState()
		case <-d.ctx.Done():
			return "shutdown", currentState()
		case <-sub.ch:
			resetSettle()
		case <-poll.C:
			if !taken && gate.check(sess, pty.LastOutput) {
				taken = true
				// The quiet clock starts when the pane took the question, so
				// the silence before it does not count toward settling.
				resetSettle()
			}
		case <-stallTimer.C:
			if !taken && !gate.check(sess, pty.LastOutput) {
				return askSettledStalled, currentState()
			}
			taken = true
		case ev := <-stateSub.ch:
			if ev.Type == EventSessionClosed {
				return "session-closed", AgentStateNone.Name()
			}
			if ev.Type == EventWindowClosed && ev.Window == windowID {
				return "window-closed", AgentStateNone.Name()
			}
			gate.observe(ev)
			if !taken && gate.check(sess, pty.LastOutput) {
				taken = true
			}
			// Only a report stamped after the question was sent says anything
			// about this question. A stale rest state is the pane not having
			// noticed yet, and returning on it is the bug this guards.
			if ev.Time <= sentAt || !taken {
				continue
			}
			// needs_input ends the wait too, though it is not a rest state: an
			// agent that answers with a prompt of its own has dealt with the
			// question as far as it can, and the reply is what it printed.
			if name := currentState(); (agentRestStates[name] || name == AgentStateNeedsInput.Name()) && name != AgentStateNone.Name() {
				return "agent-state", name
			}
		case <-timer.C:
			if !taken {
				// Quiet before the pane took the question says nothing: a
				// pane that never took it is quiet too. The stall timer
				// decides that case, and the poll restarts this clock.
				continue
			}
			return "idle", currentState()
		}
	}
}

// askSettledStalled is the settled_by an ask ends with when the stall gate saw
// nothing. It never reaches a caller as a result: ask-agent turns it into
// prompt_stalled.
const askSettledStalled = "stalled"

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
