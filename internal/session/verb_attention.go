package session

import (
	"encoding/json"
	"strings"
)

// The Inbox verbs: list-attention reads the queue, dismiss-attention closes an
// item. Changes arrive as attention events on subscribe. See attention.go.

// attentionLive reports whether a session is live, and with a window, whether
// the window is in it. It is how a restarted daemon decides which saved items
// still describe something.
func (d *Daemon) attentionLive(sessionName, window string) bool {
	sess := d.manager.GetSession(sessionName)
	if sess == nil {
		return false
	}
	if window == "" {
		return true
	}
	_, ok := findWindowState(sess.GetState(), window)
	return ok
}

// verbListAttention answers the queue, in Inbox order, with the counts per kind
// and the stream position the answer is current to.
func (d *Daemon) verbListAttention(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string   `json:"session"`
		Kinds   []string `json:"kinds"`
		Host    string   `json:"host"`
		Select  string   `json:"select"`
		// IncludeSnoozed also lists the snoozed items, after the open ones,
		// each with snoozed_until. They are not in the counts.
		IncludeSnoozed bool `json:"include_snoozed"`
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
	if p.Host != "" && p.Host != localAttentionHost {
		if verr := d.checkHostParam(p.Host); verr != nil {
			return nil, verr
		}
	}
	q := attentionQuery{session: p.Session, host: p.Host, snoozed: p.IncludeSnoozed}
	for _, k := range p.Kinds {
		if AttentionKindRank(k) == len(AttentionKindNames) {
			return nil, hintedVerbError(ErrVerbInvalidParams, "kinds: "+echoName(k)+" is not an attention kind", &VerbHint{
				Param:     "kinds",
				Available: AttentionKindNames,
				Detail:    "An attention item is one of " + strings.Join(AttentionKindNames, ", ") + ".",
			})
		}
		if q.kinds == nil {
			q.kinds = make(map[string]bool, len(p.Kinds))
		}
		q.kinds[k] = true
	}
	items, counts, seq := d.attention.list(q)
	if sel != nil {
		items = d.selectAttention(items, sel)
	}
	out := map[string]any{
		"type":    "attention_list",
		"items":   items,
		"counts":  counts,
		"total":   len(items),
		"seq":     seq,
		"boot_id": d.events.bootIdentity(),
	}
	if sel != nil {
		out["select"] = sel.String()
	}
	return out, nil
}

// AttentionSelectorTarget is what a selector reads from an Inbox item on its
// own. The state is the one the kind stands for: an approval or a question is a
// pane on needs_input, errored is errored and finished is done. Mail and
// resume stand for no state. group and cwd are not on an item, and the caller
// fills them in when it knows the pane.
func AttentionSelectorTarget(it AttentionItem) SelectorTarget {
	t := SelectorTarget{
		Host:    it.Host,
		Session: it.Session,
		Name:    it.Name,
		Harness: it.Harness,
	}
	switch it.Kind {
	case AttentionApproval, AttentionQuestion, AttentionPlan:
		t.State, t.NeedsYou = AgentStateNeedsInput.Name(), true
	case AttentionErrored:
		t.State, t.NeedsYou = AgentStateErrored.Name(), true
	case AttentionFinished:
		t.State = AgentStateDone.Name()
	}
	return t
}

// selectAttention keeps the items sel matches. An item of this machine also
// answers group and cwd from its session and pane.
func (d *Daemon) selectAttention(items []AttentionItem, sel *Selector) []AttentionItem {
	kept := make([]AttentionItem, 0, len(items))
	for _, it := range items {
		t := AttentionSelectorTarget(it)
		if it.Host == "" {
			if sess := d.manager.GetSession(it.Session); sess != nil {
				st := sess.GetState()
				if st.Worktree != nil {
					t.Group = st.Worktree.Group
				}
				if w, ok := findWindowState(st, it.Window); ok {
					t.Cwd = w.Cwd
				}
			}
		}
		if sel.Match(t) {
			kept = append(kept, it)
		}
	}
	return kept
}

// verbDismissAttention closes one item for the person.
//
// Only the person may clear what is waiting for the person, so the call must
// carry the attach nonce of a TUI client attached right now, over the same kind
// of connection (see human_sender.go), and the caller must be allowed to act as
// the person (see human_origin.go): not inside a pane of this daemon, not on an
// unvouched link, and, where the kernel gave both pids, the process that holds
// the attach. The Inbox sends its client's nonce. An agent in a pane is issued
// no nonce and could not use one it copied, so it cannot empty the queue the
// person reads to find out what the agents want.
//
// Dismissing does what reading would have: a finished item marks the pane's
// turns seen, so finished_unread clears too, and a mail item marks the
// person's mail in the thread read, so the rail's count agrees with the Inbox.
func (d *Daemon) verbDismissAttention(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		ID         string `json:"id"`
		HumanNonce string `json:"human_nonce"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.ID == "" {
		return nil, invalidParam("id", "id is required: pass the id list-attention printed")
	}
	if !d.verifyAnyHumanNonce(p.HumanNonce, cs) {
		return nil, hintedVerbError(ErrVerbNotHuman, "dismiss-attention is for the person at an attached client", &VerbHint{
			Param:  "human_nonce",
			Detail: "Only a client attached right now can clear the Inbox, by passing the nonce its attach reply carried. An agent that wants the person to stop waiting on it should change its own state, or reply to the mail.",
		})
	}
	it, ok := d.attention.dismiss(p.ID)
	if !ok {
		return nil, hintedVerbError(ErrVerbInvalidParams, "no open attention item has id "+echoName(p.ID), &VerbHint{
			Param:   "id",
			Verb:    "list-attention",
			Command: "tuios list-attention",
			Detail:  "The item may already be closed: its pane moved on, the mail was read, or someone else dismissed it.",
		})
	}
	// Outside the queue's lock, since both take the session's. An item from
	// another machine was only hidden here: its pane and its mail are on that
	// machine, and nothing here marks them.
	if it.Host != "" {
		return map[string]any{
			"type":      "attention_dismissed",
			"id":        it.ID,
			"kind":      it.Kind,
			"session":   it.Session,
			"host":      it.Host,
			"dismissed": true,
		}, nil
	}
	// Dismissing a machine's outbox discards what still waits for it: that is
	// the person deciding the mail should not go.
	if it.Kind == AttentionOutbox {
		dropped := d.outbox.discard(it.ForHost)
		return map[string]any{
			"type":      "attention_dismissed",
			"id":        it.ID,
			"kind":      it.Kind,
			"for_host":  it.ForHost,
			"discarded": dropped,
			"dismissed": true,
		}, nil
	}
	if sess := d.manager.GetSession(it.Session); sess != nil {
		switch it.Kind {
		case AttentionFinished:
			sess.MarkCompletionSeen(it.Window)
		case AttentionMail:
			d.markHumanThreadRead(sess, it.Thread)
		}
	}
	return map[string]any{
		"type":      "attention_dismissed",
		"id":        it.ID,
		"kind":      it.Kind,
		"session":   it.Session,
		"dismissed": true,
	}, nil
}

// markHumanThreadRead marks the person's unread mail in one thread read, the
// way the mail overlay's read does, receipt to the attached clients included.
func (d *Daemon) markHumanThreadRead(sess *Session, thread uint64) {
	if thread == 0 {
		return
	}
	raw, err := json.Marshal(map[string]any{
		"session": sess.Name,
		"to":      AgentInboxHuman,
		"thread":  thread,
		"limit":   agentMailboxMaxMessages,
	})
	if err != nil {
		return
	}
	_, _ = d.verbReadAgentMessages(nil, raw)
}
