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

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/invisible"
)

var agentRestStates = map[string]bool{
	AgentStateIdle.Name():         true,
	AgentStateDone.Name():         true,
	AgentStateErrored.Name():      true,
	AgentStateNone.Name():         true,
	AgentStateLimitReached.Name(): true,
}

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

const (
	askDefaultReadyTimeout = 30 * time.Second
	askDefaultSettle       = 2 * time.Second
	askDefaultTimeout      = 300 * time.Second
	askDefaultLines        = 200
)

func windowLabelOf(w WindowState) string {
	if w.CustomName != "" {
		return w.CustomName
	}
	return w.Title
}

func isAgentWindow(w WindowState) bool {
	return w.AgentState != AgentStateNone || w.AgentHarness != ""
}

func (d *Daemon) verbListAgents(cs *connState, params json.RawMessage) (any, *verbError) {
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
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	unread := d.agents.unreadCounts(sess.Name())
	agents := d.agentRows(sess, p.All, unread, d.evidenceNow().UnixNano(), sel)

	out := map[string]any{
		"type":         "agent_list",
		"session":      sess.Name(),
		"agents":       agents,
		"total":        len(agents),
		"human_inbox":  AgentInboxHuman,
		"human_unread": unread[AgentInboxHuman],
	}
	addSelection(out, sel, agents, true)
	return out, nil
}

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

func (d *Daemon) listAgentsAllSessions(all bool, sel *Selector) map[string]any {
	sessions := d.manager.AllSessions()
	slices.SortFunc(sessions, func(a, b *Session) int { return strings.Compare(a.Name(), b.Name()) })
	now := d.evidenceNow().UnixNano()
	agents := make([]map[string]any, 0, len(sessions))
	humanUnread := 0
	for _, sess := range sessions {
		unread := d.agents.unreadCounts(sess.Name())
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

func (d *Daemon) agentRows(sess *Session, all bool, unread map[string]int, now int64, sel *Selector) []map[string]any {
	seen := d.agents.seenCounts(sess.Name())
	state := sess.GetState()
	group := ""
	if state.Worktree != nil {
		group = state.Worktree.Group
	}
	agents := make([]map[string]any, 0, len(state.Windows))
	held := heldTasks(state.Tasks)
	for i := range state.Windows {
		w := state.Windows[i]
		if !all && !isAgentWindow(w) {
			continue
		}
		if sel != nil && !sel.Match(d.paneSelectorTarget(sess, w, group, held[w.ID])) {
			continue
		}
		claim := sess.agentClaimFor(w.ID)
		meta := agentMetaMap(w.AgentMeta, now)
		source := ""
		if isAgentWindow(w) {
			source = claim.source.Name()
		}
		agents = append(agents, map[string]any{
			"session":          sess.Name(),
			"window_id":        w.ID,
			"name":             windowLabelOf(w),
			"state":            w.ShownAgentState().Name(),
			"last_state":       w.AgentState.Name(),
			"last_heard_at":    max(w.AgentStateAt, claim.heardAt),
			"reason":           w.AgentReason,
			"quiet":            w.AgentQuiet,
			"message":          w.AgentMessage,
			"agent_state_at":   w.AgentStateAt,
			"source":           source,
			"harness_id":       firstNonEmpty(w.AgentHarness, claim.harness),
			"foreground":       w.ForegroundCmd,
			"cwd":              w.Cwd,
			"workspace":        w.Workspace,
			"focused":          w.ID == state.FocusedWindowID,
			"unread":           unread[w.ID],
			"seen":             seen[w.ID],
			"ready":            d.agentReady(w, agentRestStates),
			"blocked_by":       agentBlockedBy(w),
			"waits_on":         agentWaitsOn(w),
			"needs_you":        w.AgentState.NeedsYou(),
			"confidence":       claim.identity.confidence(),
			"identity":         string(claim.identity),
			"evidence_age_ms":  evidenceAgeMS(sess.evidenceStamp(w, claim), time.Unix(0, now)),
			"completion_seq":   w.CompletionSeq,
			"finished_unread":  sess.finishedUnread(&w),
			"agent_session_id": w.AgentSessionID,
			"meta":             meta,
			"context":          agentContextOf(meta),
			"limit_resets":     agentLimitResets(&w),
			"group":            group,
			"protocol":         d.paneProtocol(w.ID),
			"queued":           w.AgentQueued,
			"draft_held":       w.AgentDraftHeld,
			"settled":          w.AgentSettled,
			"subagents":        w.AgentSubagents,
			"subagent_list":    subagentListOut(w.AgentSubagentList),
			"program_status":   programStatusList(w.ProgramStatus),
		})
		if w.HostEnded != "" {
			agents[len(agents)-1]["exited"] = true
			agents[len(agents)-1]["host_ended"] = w.HostEnded
		}
		if w.StartedBy != nil {
			agents[len(agents)-1]["started_by"] = w.StartedBy
		}
		d.addAgentLink(agents[len(agents)-1], w)
		d.addDelegation(agents[len(agents)-1], w)
	}
	return agents
}

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

func (d *Daemon) resolveSender(cs *connState, state *SessionState, from string, anySession bool) (string, string, error) {
	id, label, err := resolveMailParty(state, from)
	if err == nil || !anySession {
		return id, label, err
	}
	for _, sess := range d.manager.AllSessions() {
		if w, ok := findWindowState(sess.GetState(), from); ok {
			return w.ID, windowLabelOf(w) + " in " + sess.Name(), nil
		}
	}
	if ref := d.callerPaneRef(cs); ref != nil {
		if own := d.sessionHoldingWindow(ref.Window); own != nil {
			if id, label, oerr := resolveMailParty(own.GetState(), from); oerr == nil && id != AgentInboxHuman {
				return id, label + " in " + own.Name(), nil
			}
		}
	}
	return "", "", err
}

func senderNotFound(err error, sess *Session) *verbError {
	verr := mapResolveErr(err, sess)
	if verr.Code != ErrVerbWindowNotFound || verr.Hint == nil {
		return verr
	}
	verr.Message = "from: " + verr.Message
	verr.Hint.Param = "from"
	verr.Hint.Available = nil
	verr.Hint.DidYouMean = ""
	verr.Hint.Detail = "from names the sender, not the recipient. A name, a short id or an index names a window of the recipient's session or of the caller's own session. A window of another session is named by its full id."
	return verr
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

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
	return d.sendAgentMessage(cs, p, true)
}

type sendAgentMessageParams struct {
	Session     string      `json:"session"`
	To          string      `json:"to"`
	From        string      `json:"from"`
	FromHost    string      `json:"from_host"`
	Subject     string      `json:"subject"`
	Text        string      `json:"text"`
	ReplyTo     uint64      `json:"reply_to"`
	Attachments []string    `json:"attachments"`
	Parts       []queuePart `json:"parts"`
	HumanNonce  string      `json:"human_nonce"`
	Host        string      `json:"host"`
	Select      string      `json:"select"`
	Confirm     string      `json:"confirm"`
	raw         json.RawMessage
}

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
		q.Session, q.To = pane.sess.Name(), pane.window.ID
		row := map[string]any{"session": pane.sess.Name(), "window": pane.window.ID, "name": windowLabelOf(pane.window)}
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

func (d *Daemon) sendAgentMessage(cs *connState, p sendAgentMessageParams, fromAnySession bool) (map[string]any, *verbError) {
	viaLink := cs != nil && cs.viaLink
	var filed bool
	if p.Parts != nil && p.Host == "" {
		shown, size, verr := checkParts(p.Parts, agentMsgMaxText)
		if verr != nil {
			return nil, verr
		}
		joined, images := partsText(p.Parts)
		filed = images || size > agentMsgMaxText
		if filed {
			p.Text = shown
			n := len(p.Attachments)
			for _, part := range p.Parts {
				if part.Kind != queuePartText {
					n++
				}
			}
			if n > agentMsgMaxAttachments {
				return nil, invalidParam("parts", "a message carries at most 8 attachments, its pastes and images included")
			}
			if viaLink && !d.linkPolicy(cs).Allows(config.LinkAllowFiles) {
				cs.mu.Lock()
				peer := cs.linkPeer
				cs.mu.Unlock()
				return nil, linkFilesRefusal(peer)
			}
		} else {
			p.Text = joined
		}
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
	if p.Host != "" {
		res, verr := d.sendAgentMessageToHost(cs, p.Host, p.Session, p.To, p.From, p.raw)
		if verr != nil {
			return nil, verr
		}
		out, _ := res.(map[string]any)
		return out, nil
	}
	if p.ReplyTo > d.agents.highestID() {
		return nil, hintedVerbError(ErrVerbInvalidParams, "reply_to names a message that has never existed", &VerbHint{
			Param:   "reply_to",
			Command: "tuios read-agent-messages",
			Detail:  "No message has been sent with that id. Read the ring to find the id you meant to answer.",
		})
	}

	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()

	msg := AgentMessage{Kind: agentMsgNotice, Text: p.Text, Subject: p.Subject, ReplyTo: p.ReplyTo}

	switch {
	case viaLink:
		msg.Origin = AgentOriginLink
		msg.OriginHost = printableClaim(p.FromHost, agentMsgMaxHostName)
		msg.OriginLink = cs.linkPeer
		if p.From == AgentInboxHuman {
			msg.From, msg.FromLabel = AgentInboxHuman, AgentInboxHuman
		} else {
			msg.FromLabel = printableClaim(p.From, agentMsgMaxSubject)
		}
	case p.From != "":
		id, label, err := d.resolveSender(cs, state, p.From, fromAnySession)
		if err != nil {
			return nil, senderNotFound(err, sess)
		}
		msg.From, msg.FromLabel = id, label
		if pa := d.paneAuthority(cs); pa != nil && pa.window == id {
			msg.FromProven = true
		}
	}

	if msg.From == AgentInboxHuman && !viaLink && !d.mayActAsHuman(cs) {
		return nil, humanForbiddenError("send-agent-message")
	}

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

	if msg.To != "" && msg.To == msg.From {
		return nil, hintedVerbError(ErrVerbLoopRefused, "a pane cannot send a message to itself", &VerbHint{
			Param:  "to",
			Detail: "Sender and recipient resolve to the same window. Write a note to a file instead of into your own inbox.",
		})
	}

	if viaLink && msg.To != AgentInboxHuman && d.linkPolicy(cs).HoldMail {
		msg.HeldFor, msg.HeldForLabel = msg.To, msg.ToLabel
		msg.Held = true
		msg.Kind = agentMsgDirect
		msg.To, msg.ToLabel = AgentInboxHuman, AgentInboxHuman
	}

	for _, path := range p.Attachments {
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

	sender := msg.From
	if viaLink {
		sender = queueByLinkPrefix + msg.OriginHost + ":" + msg.FromLabel
	}
	if !d.agents.checkRate(sess.Name(), sender) {
		return nil, hintedVerbError(ErrVerbRateLimited, "this sender is over the message rate cap", &VerbHint{
			Command: "tuios read-agent-messages " + sessionArg(sess),
			Detail:  "A sender gets 10 messages back to back and 30 a minute after that. Hitting the cap almost always means two agents are answering each other in a loop; read the ring before sending again.",
		})
	}
	if viaLink {
		unread, notices := d.agents.linkQueued(sess.Name())
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

	if filed {
		text, paths, err := d.fileParts(p.Parts)
		if err != nil {
			return nil, newVerbError(ErrVerbInternal, "send-agent-message: a pasted block could not be saved: "+err.Error())
		}
		msg.Text = text
		for _, path := range paths {
			att, err := classifyAttachment(path)
			if err != nil {
				return nil, newVerbError(ErrVerbInternal, "send-agent-message: a saved block cannot be attached: "+err.Error())
			}
			msg.Attachments = append(msg.Attachments, att)
		}
	}

	stored := d.agents.send(sess.Name(), msg)

	d.broadcastToSession(sess.ID, MsgAgentMail, &AgentMailPayload{Message: stored}, "")

	d.events.publish(streamEvent{
		Type:    EventAgentMessage,
		Session: sess.Name(),
		Window:  stored.To,
	})
	d.attention.noteMail(stored)
	if stored.Kind == agentMsgDirect && stored.To == AgentInboxHuman {
		d.wakeSettled(sess, stored.From)
	}
	if stored.Kind == agentMsgDirect {
		if origin, ok := d.mailOriginOf(cs, stored.VerifiedHuman); ok {
			d.noteMailSent(sess.Name(), stored.To, stored.ID, origin)
		}
	}

	return map[string]any{
		"type":             "agent_message_sent",
		"session":          sess.Name(),
		"message_id":       stored.ID,
		"kind":             stored.Kind,
		"to":               stored.To,
		"to_name":          stored.ToLabel,
		"from":             stored.From,
		"sent_at":          stored.SentAt,
		"reply_to":         stored.ReplyTo,
		"thread_id":        stored.ThreadID,
		"reply_to_missing": stored.ReplyToMissing,
		"origin":           stored.Origin,
		"origin_host":      stored.OriginHost,
		"verified_human":   stored.VerifiedHuman,
		"claimed_human":    stored.ClaimedHuman,
		"held":             stored.Held,
		"held_for":         stored.HeldFor,
	}, nil
}

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
	if !d.humanNonceHeld(p.HumanNonce, cs) {
		return nil, hintedVerbError(ErrVerbNotHuman, "release-agent-message is for the person at an attached client", &VerbHint{
			Param:  "human_nonce",
			Detail: "Mail from another machine is held so the person decides whether an agent sees it. Only a client attached right now can pass it on, with the nonce its attach reply carried.",
		})
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if _, ok := d.humanNonceFor(p.HumanNonce, sess.ID, cs); !ok {
		return nil, nonceScopeError("release-agent-message")
	}
	held, ok := d.agents.takeHeld(sess.Name(), p.ID)
	if !ok {
		return nil, hintedVerbError(ErrVerbInvalidParams, "no held message has id "+strconv.FormatUint(p.ID, 10)+" in this session", &VerbHint{
			Param:   "id",
			Command: "tuios read-agent-messages -w human",
			Detail:  "The message may already have been passed on, or dropped from the ring. Only a message marked held can be released.",
		})
	}
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
	stored := d.agents.send(sess.Name(), out)
	d.broadcastToSession(sess.ID, MsgAgentMail, &AgentMailPayload{Message: stored}, "")
	d.events.publish(streamEvent{Type: EventAgentMessage, Session: sess.Name(), Window: stored.To})
	d.broadcastToSession(sess.ID, MsgAgentMail, &AgentMailPayload{ReadIDs: []uint64{held.ID}, ReadAt: held.ReadAt}, "")
	if _, unread := d.agents.firstUnread(sess.Name(), AgentInboxHuman, held.ThreadID); !unread {
		d.attention.noteMailRead(sess.Name(), held.ThreadID)
	}
	return map[string]any{
		"type":       "agent_message_released",
		"session":    sess.Name(),
		"held_id":    held.ID,
		"message_id": stored.ID,
		"to":         stored.To,
		"to_name":    stored.ToLabel,
		"thread_id":  stored.ThreadID,
	}, nil
}

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
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()

	q := readQuery{unreadOnly: p.Unread, notices: p.Notices, peek: p.Peek, limit: p.Limit}
	q.thread = d.agents.resolveThread(sess.Name(), p.Thread)
	if p.To != "" {
		id, _, err := resolveMailParty(state, p.To)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		q.inbox = id
	}
	peekForced := false
	if q.inbox == AgentInboxHuman && !q.peek && !d.mayActAsHuman(cs) {
		q.peek, peekForced = true, true
	}
	q.markSeen = q.peek && !peekForced && d.callerReadsInbox(cs, q.inbox)
	live := map[string]bool{AgentInboxHuman: true}
	for i := range state.Windows {
		live[state.Windows[i].ID] = true
	}
	q.live = func(id string) bool { return live[id] }

	res := d.agents.read(sess.Name(), q)
	if lead := d.liveLeadOf(q.inbox); lead != "" {
		for i := range res.Messages {
			if res.Messages[i].FromProven && res.Messages[i].From == lead {
				res.Messages[i].FromLead = true
			}
		}
	}

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
	var seenIDs []uint64
	var seenAt int64
	seenThreads := map[uint64]bool{}
	for _, m := range res.Messages {
		if q.markSeen && m.WasUnread && !m.WasSeen && m.SeenAt != 0 && m.ReadAt == 0 {
			seenIDs = append(seenIDs, m.ID)
			seenAt = m.SeenAt
			seenThreads[m.ThreadID] = true
		}
	}
	if len(seenIDs) > 0 {
		d.broadcastToSession(sess.ID, MsgAgentMail, &AgentMailPayload{SeenIDs: seenIDs, SeenAt: seenAt}, "")
		if q.inbox == AgentInboxHuman {
			for thread := range seenThreads {
				d.attention.noteMailSeen(sess.Name(), thread, d.agents.threadSeen(sess.Name(), AgentInboxHuman, thread))
			}
		}
	}
	if q.inbox == AgentInboxHuman && len(marked) > 0 {
		threads := map[uint64]bool{}
		for _, m := range res.Messages {
			if m.WasUnread && m.ReadAt != 0 {
				threads[m.ThreadID] = true
			}
		}
		for thread := range threads {
			if _, unread := d.agents.firstUnread(sess.Name(), AgentInboxHuman, thread); !unread {
				d.attention.noteMailRead(sess.Name(), thread)
			}
		}
	}

	return map[string]any{
		"type":           "agent_messages",
		"session":        sess.Name(),
		"inbox":          q.inbox,
		"thread":         q.thread,
		"untrusted":      true,
		"messages":       res.Messages,
		"unread":         res.Unread,
		"seen":           res.Seen,
		"marked_read":    res.MarkedRead,
		"marked_seen":    res.MarkedSeen,
		"total":          res.Total,
		"evicted":        res.Evicted,
		"evicted_unread": res.EvictedUnread,
		"peek_forced":    peekForced,
	}, nil
}

func (d *Daemon) callerReadsInbox(cs *connState, inbox string) bool {
	switch inbox {
	case "":
		return false
	case AgentInboxHuman:
		return d.mayActAsHuman(cs)
	}
	pa := d.paneAuthority(cs)
	return pa != nil && pa.window == inbox
}

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
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()
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
	gone, stop := askCancel(cs)
	defer stop()
	res, verr := d.askAgent(cs, sess, state, target, p, true, gone)
	if verr != nil {
		return nil, verr
	}
	return res, nil
}

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
	gone, stop := askCancel(cs)
	defer stop()
	var wg sync.WaitGroup
	for i, pane := range panes {
		wg.Go(func() {
			row := map[string]any{"session": pane.sess.Name(), "window": pane.window.ID, "name": windowLabelOf(pane.window)}
			state := pane.sess.GetState()
			target, ok := findWindowState(state, pane.window.ID)
			if !ok {
				row["ok"] = false
				row["error"] = newVerbError(ErrVerbWindowNotFound, "the pane closed before it was asked")
				results[i] = row
				return
			}
			res, verr := d.askAgent(cs, pane.sess, state, target, p, true, gone)
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
		"type":      "agent_replies",
		"select":    p.Select,
		"replies":   results,
		"answered":  answered,
		"failed":    len(results) - answered,
		"total":     len(results),
		"untrusted": true,
	}, nil
}

func (d *Daemon) askAgent(cs *connState, sess *Session, state *SessionState, target WindowState, p askAgentParams, fromAnySession bool, gone <-chan struct{}) (map[string]any, *verbError) {
	var hidden int
	p.Text, hidden = invisible.StripHidden(p.Text)
	from, fromLabel := "", ""
	viaLink := cs != nil && cs.viaLink
	switch {
	case viaLink:
		fromLabel = printableClaim(p.From, agentMsgMaxSubject)
	case p.From != "":
		fid, flabel, ferr := d.resolveSender(cs, state, p.From, fromAnySession)
		if ferr != nil {
			return nil, senderNotFound(ferr, sess)
		}
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

	if !d.agents.openAsk(from, target.ID) {
		detail := "The target is already waiting, directly or through another agent, on the pane making this call, so answering would leave both sides blocked on each other. Leave a message instead: send-agent-message does not block."
		if edges := d.agents.openAskEdges(); len(edges) > 0 {
			detail += " Asks in flight: " + strings.Join(edges, ", ") + "."
		}
		return nil, hintedVerbError(ErrVerbLoopRefused, "this ask would close a loop with one already in flight", &VerbHint{
			Command: "tuios send-agent-message " + paneArgs(sess, target.ID) + " '<what you wanted to ask>'",
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

	turnStart := time.Now()
	ahead, release := d.askTurns.take(d.ctx, gone, target.ID, timeout)
	if release == nil {
		if d.ctx.Err() != nil {
			return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
		}
		if closed(gone) {
			return nil, askCallerGoneError()
		}
		return nil, hintedVerbError(ErrVerbNotReady, "other asks of this agent were still running when the timeout elapsed: "+strconv.Itoa(ahead)+" were ahead of this one", &VerbHint{
			Param:   "timeout",
			Command: "tuios send-agent-message -w " + shortWindowID(target.ID) + " '<what you wanted to ask>'",
			Detail:  "Nothing was typed. Asks of one agent take turns, first come first served, and this one waited its whole timeout for its turn. Raise timeout, or leave a message instead: tuios tells the agent about its mail.",
		})
	}
	defer release()
	waitedTurn := time.Since(turnStart)

	if !p.AllowBlocked {
		if verr := d.refuseBlockedAgent(sess, target.ID); verr != nil {
			return nil, verr
		}
	}

	waitedFor := ""
	if !p.Force {
		reached, verr := d.waitAgentRest(sess, target.ID, readyTimeout, p.AllowBlocked, gone)
		if verr != nil {
			return nil, verr
		}
		waitedFor = reached
	}

	before := contentLines(pty.CaptureContent(true, false))
	mark := d.askTranscriptMark(sess, target.ID)

	if !p.AllowBlocked {
		if verr := d.refuseBlockedAgent(sess, target.ID); verr != nil {
			return nil, verr
		}
	}
	if verr := d.recheckTyping(cs, "ask-agent", sess, target.ID); verr != nil {
		return nil, verr
	}
	if closed(gone) {
		return nil, askCallerGoneError()
	}
	gate := d.newPromptGate(sess, target.ID)
	ctx := withSubmitGuard(d.ctx, func() *verbError {
		if !p.AllowBlocked {
			if verr := d.refuseBlockedAgent(sess, target.ID); verr != nil {
				return verr
			}
		}
		return d.recheckTyping(cs, "ask-agent", sess, target.ID)
	})
	submittedAt, werr := submitPrompt(ctx, pty, p.Text, d.inputProfileFor(sess, target.ID))
	if werr != nil {
		return nil, promptWriteError(werr)
	}
	gate.markSubmitted(submittedAt)

	stall := durationOr(p.StallTimeout, d.promptStall())
	settledBy, endState := d.waitAgentSettled(sess, target.ID, pty, gate, settle, timeout, stall, gone)

	after := pty.CaptureContent(true, false)
	reply, truncated := tailLines(after, before, lines)
	replySource := "screen"
	if settledBy != askSettledStalled {
		if text, ok := d.askTranscriptReply(sess, target.ID, mark); ok {
			reply, truncated = tailLines(text, 0, lines)
			replySource = "transcript"
		}
	}

	d.recordAsk(sess, from, fromLabel, origin, target, p.Text, reply, settledBy)

	if settledBy == askSettledStalled {
		return nil, hintedVerbError(ErrVerbPromptStalled, gate.stalledMessage(stall)+"; its state is "+endState, &VerbHint{
			Param:   "stall_timeout",
			Verb:    "capture-pane",
			Command: "tuios capture-pane " + paneArgs(sess, target.ID),
			Detail:  "The question was typed and Enter was sent, so do not send it again without looking. Read the pane with capture-pane. If the question sits in the agent's input box, press Enter there with send-keys. If the agent is still starting, wait for it with wait-for agent-state and ask again. An agent that is slow to show it is working can be given more time with stall_timeout.",
		})
	}

	return map[string]any{
		"type":              "agent_reply",
		"session":           sess.Name(),
		"window":            target.ID,
		"name":              windowLabelOf(target),
		"waited_for":        waitedFor,
		"queued_behind":     ahead,
		"waited_turn_ms":    waitedTurn.Milliseconds(),
		"settled_by":        settledBy,
		"state":             endState,
		"untrusted":         true,
		"reply":             reply,
		"lines":             countLines(reply),
		"truncated":         truncated,
		"reply_source":      replySource,
		"invisible_removed": hidden,
	}, nil
}

func (d *Daemon) recordAsk(sess *Session, from, fromLabel string, origin askOrigin, target WindowState, question, reply, settledBy string) {
	if len(question) > agentMsgMaxSubject {
		question = question[:agentMsgMaxSubject]
	}
	if len(reply) > agentMsgMaxText {
		reply = reply[:agentMsgMaxText]
	}
	stored := d.agents.send(sess.Name(), AgentMessage{
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

type askOrigin struct {
	origin string
	host   string
}

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

func durationOr(ms int, fallback time.Duration) time.Duration {
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return fallback
}

func agentBlockedError(sess *Session, w WindowState) *verbError {
	what := "a prompt"
	switch agentBlockedBy(w) {
	case harness.PromptKindApproval:
		what = "an approval"
	case harness.PromptKindQuestion:
		what = "a question"
	case harness.PromptKindAuth:
		what = "a login"
	}
	msg := "the target agent is waiting on " + what + ", and text typed now would answer it"
	if note := printableClaim(w.AgentMessage, agentMsgMaxSubject); note != "" {
		msg += ": " + note
	}
	return hintedVerbError(ErrVerbAgentBlocked, msg, &VerbHint{
		Verb:    "peek-prompt",
		Command: "tuios peek-prompt " + paneArgs(sess, w.ID),
		Detail: "Nothing was typed. Read the prompt with peek-prompt. Do not type at it: only the person answers another agent's prompt. " +
			"Tell the person which pane waits and on what, with tuios ask-human or tuios send-agent-message -w human. " +
			"Pass allow_blocked only when you have read the prompt and it takes free text.",
	})
}

func (d *Daemon) refuseBlockedAgent(sess *Session, windowID string) *verbError {
	st := sess.GetState()
	i, err := findWindowStateIndex(st.Windows, windowID)
	if err != nil {
		return nil
	}
	if st.Windows[i].AgentState == AgentStateNeedsInput {
		return agentBlockedError(sess, st.Windows[i])
	}
	return nil
}

func (d *Daemon) waitAgentRest(sess *Session, windowID string, timeout time.Duration, allowBlocked bool, gone <-chan struct{}) (string, *verbError) {
	sub := d.events.subscribe(eventFilter{
		session: sess.Name(),
		sess:    sess,
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
			blocked = agentBlockedError(sess, w)
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
			pane := paneArgs(sess, windowID)
			notWaiting := "To not wait, run tuios queue " + pane + " 'TEXT': it types the text when the agent next comes to rest. " +
				"Or leave a message with tuios send-agent-message " + pane + " 'TEXT'."
			msg := "the target agent was still working when the ready timeout elapsed"
			detail := "Nothing was typed. Text typed at an agent mid-turn mixes with its work. To wait for it, run the command below, or raise ready_timeout. " + notWaiting
			if name, _ := check(); name == AgentStateUnknown.Name() {
				msg = "the target agent's state was unknown when the ready timeout elapsed"
				detail = "Nothing was typed. unknown means the pane went quiet and nothing on its screen said whether the agent is at its prompt or in the middle of a long call, so it is not treated as ready. " +
					"Look at the pane with tuios capture-pane " + pane + ". " + notWaiting
			}
			return "", hintedVerbError(ErrVerbNotReady, msg, &VerbHint{
				Param:   "ready_timeout",
				Command: "tuios wait-for agent-state " + pane + " --until idle,needs_input,done",
				Detail:  detail,
			})
		case <-d.ctx.Done():
			return "", newVerbError(ErrVerbInternal, "daemon is shutting down")
		case <-gone:
			return "", askCallerGoneError()
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

func (d *Daemon) waitAgentSettled(sess *Session, windowID string, pty *PTY, gate *promptGate, settle, timeout, stall time.Duration, gone <-chan struct{}) (string, string) {
	sentAt := gate.submittedAt
	sub := d.events.subscribe(eventFilter{
		session: sess.Name(),
		sess:    sess,
		ptyID:   pty.ID,
		types:   map[string]bool{EventOutput: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	stateSub := d.events.subscribe(eventFilter{
		session: sess.Name(),
		sess:    sess,
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
		case <-gone:
			return askSettledCallerGone, currentState()
		case <-sub.ch:
			resetSettle()
		case <-poll.C:
			if !taken && gate.check(sess, pty.LastOutput) {
				taken = true
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
			if !taken && ev.Window == windowID && gate.check(sess, pty.LastOutput) {
				taken = true
			}
			if ev.Time <= sentAt || !taken {
				continue
			}
			if name := currentState(); (agentRestStates[name] || name == AgentStateNeedsInput.Name()) && name != AgentStateNone.Name() {
				return "agent-state", name
			}
		case <-timer.C:
			if !taken {
				continue
			}
			return "idle", currentState()
		}
	}
}

const askSettledStalled = "stalled"

const askSettledCallerGone = "caller-gone"

func contentLines(s string) int {
	return countContent(strings.Split(s, "\n"))
}

func countContent(lines []string) int {
	for i, line := range slices.Backward(lines) {
		if strings.TrimSpace(line) != "" {
			return i + 1
		}
	}
	return 0
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

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

func askCancel(cs *connState) (<-chan struct{}, func()) {
	if cs == nil {
		return nil, func() {}
	}
	var gone <-chan struct{}
	stopWatch := func() {}
	if cs.conn != nil {
		gone, stopWatch = watchPeerClose(cs.conn)
	}
	out := make(chan struct{})
	quit := make(chan struct{})
	go func() {
		select {
		case <-gone:
		case <-cs.done:
		case <-cs.hostedEnded:
		case <-quit:
			return
		}
		close(out)
	}()
	var once sync.Once
	return out, func() {
		once.Do(func() {
			close(quit)
			stopWatch()
		})
	}
}

func closed(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func askCallerGoneError() *verbError {
	return newVerbError(ErrVerbInternal, "the caller went away before the question was typed, so nothing was typed")
}

func (d *Daemon) liveLeadOf(window string) string {
	if window == "" || window == AgentInboxHuman {
		return ""
	}
	sess := d.sessionHoldingWindow(window)
	if sess == nil {
		return ""
	}
	w, ok := findWindowState(sess.GetState(), window)
	if !ok || w.StartedBy == nil || w.AgentLink == nil || d.sessionHoldingWindow(w.StartedBy.Window) == nil {
		return ""
	}
	return w.StartedBy.Window
}
