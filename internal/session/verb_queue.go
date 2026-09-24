package session

import (
	"encoding/json"
	"strconv"
	"strings"
)

// The delivery queue's verbs: queue-prompt, list-queued and cancel-queued. The
// queue itself is agent_queue.go.
//
// queue-prompt types into a pane, later, so it is scopeWrite and a typing verb
// (typingVerbs): at queue time the target must hold nothing the caller does
// not, and must not be on needs_input unless the caller holds respond. The
// delivery repeats those checks against the caller's grants as they are then,
// and refuses to type over a prompt whatever the entry. cancel-queued writes
// the queue, so it is scopeWrite too, and a pane drops only what it queued.
// list-queued reads. Over a link, queue-prompt and cancel-queued need write
// and list-queued needs list.
//
// Who queued an entry is decided here, once, from the connection, never from
// a parameter: human only with a live human_nonce (verifyAnyHumanNonce, which
// also refuses any process inside a pane), link:HOST for a call over a link,
// the pane's window id for a process in a pane, and shell for anything else.
// A call a pane on another machine forwards through its report channel
// (paneOnly) is none of these: paneAuthority places it nowhere, which would
// read as shell, so both writing verbs refuse it (refuseForwardedPane).

// refuseForwardedPane refuses a queue write from a hosted pane's report
// channel. Such a call is a pane by construction, but not one of this
// daemon's, so it has no grants here to check an entry against, and it must
// not pass for the person's shell. The queue verbs are not forwarded today
// (hostedCallVerbs); this holds if they ever are.
func refuseForwardedPane(cs *connState, verb string) *verbError {
	if cs == nil || !cs.paneOnly {
		return nil
	}
	return hintedVerbError(ErrVerbForbidden, verb+" from a pane on another machine is refused", &VerbHint{
		Detail: "Nothing changed. A pane that runs here for another machine queues through the machine that owns it.",
	})
}

// ErrVerbQueueFull reports a pane whose delivery queue holds as many entries
// as [agents.queue] max allows. Nothing was queued.
const ErrVerbQueueFull = "queue_full"

// queueMaxText bounds one queued message, in bytes.
const queueMaxText = 16 << 10

// queuePromptParams are what queue-prompt takes.
type queuePromptParams struct {
	Session    string `json:"session"`
	Window     string `json:"window"`
	Text       string `json:"text"`
	HumanNonce string `json:"human_nonce"`
	From       string `json:"from"`
}

// verbQueuePrompt answers queue-prompt.
func (d *Daemon) verbQueuePrompt(cs *connState, params json.RawMessage) (any, *verbError) {
	var p queuePromptParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Text) == "" {
		return nil, invalidParam("text", "text is required: the message to type when the agent is at rest")
	}
	if len(p.Text) > queueMaxText {
		return nil, invalidParam("text", "text is longer than "+strconv.Itoa(queueMaxText)+" bytes")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()
	target, verr := queueTarget(sess, state, p.Window)
	if verr != nil {
		return nil, verr
	}
	e, verr := d.newQueueEntry(cs, sess, state, p.Text, p.From, p.HumanNonce)
	if verr != nil {
		return nil, verr
	}
	res, verr := d.queuePrompt(sess, target, e)
	if verr != nil {
		return nil, verr
	}
	return map[string]any{
		"type":       "prompt_queued",
		"id":         res.ID,
		"position":   res.Position,
		"queued":     res.Queued,
		"delivering": res.Delivering,
	}, nil
}

// queueTarget resolves the pane a message is queued for: the named window, or
// the focused one. The person's inbox has no keyboard, and a pane that runs
// no agent tuios knows of has nobody to take a prompt.
func queueTarget(sess *Session, state *SessionState, window string) (WindowState, *verbError) {
	if window == AgentInboxHuman {
		return WindowState{}, hintedVerbError(ErrVerbNoKeyboard, "human has no pane to type into", &VerbHint{
			Param:   "window",
			Command: "tuios send-agent-message -w human '<your message>'",
			Detail:  "human is the person at the attached client. Leave them mail with send-agent-message -w human.",
		})
	}
	if window == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return WindowState{}, mapResolveErr(err, sess)
		}
		window = id
	}
	idx, err := findWindowStateIndex(state.Windows, window)
	if err != nil {
		return WindowState{}, mapResolveErr(err, sess)
	}
	target := state.Windows[idx]
	if !isAgentWindow(target) {
		return WindowState{}, hintedVerbError(ErrVerbInvalidParams, "window "+shortWindowID(target.ID)+" runs no agent tuios knows of, so nothing would take a queued prompt", &VerbHint{
			Param:   "window",
			Verb:    "list-agents",
			Command: "tuios send-text -w " + shortWindowID(target.ID) + " '<text>'",
			Detail:  "Nothing was queued. A queue waits for an agent to come to rest. list-agents lists the panes with one; send-text types into a plain pane now.",
		})
	}
	return target, nil
}

// newQueueEntry builds an entry for text from the caller on cs, deciding who
// queued it (see the file comment). from is the caller's label for the
// sender: a window of the session, resolved like ask-agent's, or a free label
// over a link. A pane may name only itself, and human needs the nonce.
func (d *Daemon) newQueueEntry(cs *connState, sess *Session, state *SessionState, text, from, nonce string) (*queueEntry, *verbError) {
	if verr := refuseForwardedPane(cs, "queue-prompt"); verr != nil {
		return nil, verr
	}
	e := &queueEntry{text: text}
	human := nonce != "" && d.verifyAnyHumanNonce(nonce, cs)
	if nonce != "" && !human {
		return nil, hintedVerbError(ErrVerbNotHuman, "human_nonce does not belong to a client attached right now", &VerbHint{
			Param:  "human_nonce",
			Detail: "Nothing was queued. Only the person's attached client holds a nonce, and a process inside a pane cannot use one. Leave human_nonce out to queue as yourself.",
		})
	}
	switch {
	case cs != nil && cs.viaLink:
		cs.mu.Lock()
		peer := cs.linkPeer
		cs.mu.Unlock()
		e.origin = queueOrigin{kind: "link", peer: peer, conn: cs.clientID}
		e.by = queueByLinkPrefix + firstNonEmpty(peer, "*")
		if human {
			e.by = queueByHuman
		}
		e.from = printableClaim(from, agentMsgMaxSubject)
		return e, nil
	case human:
		e.origin = queueOrigin{kind: queueByHuman}
		e.by = queueByHuman
	default:
		if pa := d.paneAuthority(cs); pa != nil {
			if pa.hosted {
				e.origin = queueOrigin{kind: "pane", window: pa.window, hosted: true}
			} else {
				e.origin = queueOrigin{kind: "pane", window: pa.window, session: pa.session}
			}
			e.by = pa.window
			if from != "" && from != pa.window {
				if fid, _, err := d.resolveSender(state, from, true); err != nil || fid != pa.window {
					return nil, hintedVerbError(ErrVerbForbidden, "queue-prompt from "+echoName(from)+" is refused: a pane queues only as itself", &VerbHint{
						Param:  "from",
						Detail: "Nothing was queued. Leave from out, or pass $TUIOS_PANE_ID.",
					})
				}
			}
			e.from = pa.window
			return e, nil
		}
		e.origin = queueOrigin{kind: queueByShell}
		e.by = queueByShell
	}
	if from != "" {
		fid, _, err := d.resolveSender(state, from, true)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		if fid == AgentInboxHuman && !human {
			return nil, humanForbiddenError("queue-prompt")
		}
		e.from = fid
	}
	return e, nil
}

// listQueuedParams are what list-queued takes.
type listQueuedParams struct {
	Session string `json:"session"`
	Window  string `json:"window"`
}

// verbListQueued answers list-queued: one pane's queue, or every queue in the
// session when window is omitted.
func (d *Daemon) verbListQueued(_ *connState, params json.RawMessage) (any, *verbError) {
	var p listQueuedParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()
	var windows []WindowState
	if p.Window != "" {
		idx, err := findWindowStateIndex(state.Windows, p.Window)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		windows = []WindowState{state.Windows[idx]}
	} else {
		windows = state.Windows
	}
	entries := []map[string]any{}
	q := &d.queue
	q.mu.Lock()
	for _, w := range windows {
		pq := q.panes[w.ID]
		if pq == nil {
			continue
		}
		for _, e := range pq.entries {
			row := map[string]any{
				"id":      e.id,
				"session": sess.Name,
				"window":  w.ID,
				"name":    windowLabelOf(w),
				"at":      e.at,
				"by":      e.by,
				"preview": queuePreview(e.text),
				"state":   e.state,
			}
			if e.from != "" {
				row["from"] = e.from
			}
			entries = append(entries, row)
		}
	}
	q.mu.Unlock()
	return map[string]any{
		"type":    "queued_prompts",
		"session": sess.Name,
		"entries": entries,
	}, nil
}

// cancelQueuedParams are what cancel-queued takes.
type cancelQueuedParams struct {
	Session    string `json:"session"`
	Window     string `json:"window"`
	ID         string `json:"id"`
	All        bool   `json:"all"`
	HumanNonce string `json:"human_nonce"`
}

// verbCancelQueued answers cancel-queued.
//
// Who may drop what: the person, with a live human_nonce, any entry. A pane
// only the entries it queued. A linked machine only the entries it queued:
// matched by the name its link-peer handshake gave, or, for a machine that
// gave none, by the connection that queued them. A caller outside every pane
// without the nonce every entry but the person's, which need the nonce. A
// pane on another machine, forwarded through its report channel, nothing.
func (d *Daemon) verbCancelQueued(cs *connState, params json.RawMessage) (any, *verbError) {
	var p cancelQueuedParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := refuseForwardedPane(cs, "cancel-queued"); verr != nil {
		return nil, verr
	}
	if p.ID == "" && !p.All {
		return nil, invalidParam("id", "name the entry to drop with id, or pass all")
	}
	if p.ID != "" && p.All {
		return nil, invalidParam("all", "pass id or all, not both")
	}
	human := p.HumanNonce != "" && d.verifyAnyHumanNonce(p.HumanNonce, cs)
	if p.HumanNonce != "" && !human {
		return nil, newVerbError(ErrVerbNotHuman, "human_nonce does not belong to a client attached right now. Nothing was dropped")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()

	// Who the caller is, to match against each entry's origin.
	var mayDrop func(e *queueEntry) bool
	switch {
	case human:
		mayDrop = func(*queueEntry) bool { return true }
	case cs != nil && cs.viaLink:
		cs.mu.Lock()
		peer := cs.linkPeer
		cs.mu.Unlock()
		conn := cs.clientID
		mayDrop = func(e *queueEntry) bool {
			if e.origin.kind != "link" || e.by == queueByHuman || e.origin.peer != peer {
				return false
			}
			// Every machine that gave no name shares the empty peer, so
			// for those only the connection that queued it may drop it.
			return peer != "" || e.origin.conn == conn
		}
	default:
		if pa := d.paneAuthority(cs); pa != nil {
			mayDrop = func(e *queueEntry) bool { return e.origin.kind == "pane" && e.origin.window == pa.window }
		} else {
			mayDrop = func(e *queueEntry) bool { return e.by != queueByHuman }
		}
	}

	// The panes to look in: the named one, or, for an id with no window,
	// every pane of the session.
	var windows []string
	if p.Window != "" || p.All {
		target, verr := resolveQueueWindow(sess, state, p.Window)
		if verr != nil {
			return nil, verr
		}
		windows = []string{target}
	} else {
		for _, w := range state.Windows {
			windows = append(windows, w.ID)
		}
	}

	q := &d.queue
	var cancelled []string
	var stalledIn []string
	found, refused := false, false
	holder := ""
	if len(windows) == 1 {
		holder = windows[0]
	}
	q.mu.Lock()
	for _, window := range windows {
		pq := q.panes[window]
		if pq == nil {
			continue
		}
		gone := q.removeLocked(window, pq, func(e *queueEntry) bool {
			if !p.All && e.id != p.ID {
				return false
			}
			found = true
			holder = window
			if e.state == queueDelivering || !mayDrop(e) {
				refused = true
				return false
			}
			return true
		})
		for _, e := range gone {
			cancelled = append(cancelled, e.id)
			if e.state == queueStalled {
				stalledIn = append(stalledIn, window)
			}
		}
		if len(gone) > 0 {
			// A stalled head held the rest; with it gone, the next is
			// looked at again. It is typed at a rest reached after the
			// stalled one was typed, since that text may still sit in
			// the agent's input box.
			if len(pq.entries) > 0 && !pq.delivering && pq.entries[0].state == queueWaiting {
				q.armLocked(d, window, pq, q.restFor())
			}
		}
	}
	queued := 0
	if pq := q.panes[holder]; pq != nil {
		queued = len(pq.entries)
	}
	q.mu.Unlock()

	if !p.All && !found {
		return nil, hintedVerbError(ErrVerbInvalidParams, "no queued entry "+echoName(p.ID)+" in session "+echoName(sess.Name), &VerbHint{
			Param:   "id",
			Verb:    "list-queued",
			Command: "tuios queue ls",
			Detail:  "Nothing was dropped. The entry may have been typed already, or dropped when its pane or agent went away.",
		})
	}
	if !p.All && refused {
		return nil, hintedVerbError(ErrVerbForbidden, "cancel-queued of "+echoName(p.ID)+" is refused: it is being typed now, or it was queued by someone the caller may not speak for", &VerbHint{
			Detail: "Nothing was dropped. A pane drops only what it queued, and the person's own entries need the attached client's nonce. An entry being typed cannot be taken back.",
		})
	}
	if len(cancelled) > 0 {
		LogBasic("Cancelled queued %s in session %s", strings.Join(cancelled, ","), sess.Name)
		for _, window := range stalledIn {
			if w, ok := findWindowState(state, window); ok {
				d.attention.closeHeldPrompt(sess.Name, window, attentionText(queueStalledSummary(w), attentionMaxSummary))
			}
		}
		d.publishQueued(holder)
	}
	if cancelled == nil {
		cancelled = []string{}
	}
	return map[string]any{
		"type":      "queue_cancelled",
		"cancelled": cancelled,
		"queued":    queued,
	}, nil
}

// resolveQueueWindow resolves a window of the session, the focused one when
// window is empty.
func resolveQueueWindow(sess *Session, state *SessionState, window string) (string, *verbError) {
	if window == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return "", mapResolveErr(err, sess)
		}
		return id, nil
	}
	idx, err := findWindowStateIndex(state.Windows, window)
	if err != nil {
		return "", mapResolveErr(err, sess)
	}
	return state.Windows[idx].ID, nil
}
