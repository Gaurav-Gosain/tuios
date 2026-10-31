package session

import (
	"encoding/json"
)

// Quiet panes: a background worker keeps its state visible and stays out of the
// person's Inbox.
//
// The mark is a field on the pane's window state (WindowState.AgentQuiet), so it
// goes when the pane goes and every client draws it. While it is set,
// noteAgentState opens no finished, question or errored item for the pane. It
// changes nothing else: the agent state, completion_seq, the agent-state events
// and list-agents read as for any other pane, because a tool that drives a
// worker reads those to know when a turn ended. Calm mode is another thing: it is
// global and client-side, and it holds back messages without touching the Inbox.
//
// An approval is never held back, since a held prompt that nobody sees is a pane
// stuck for hold_seconds. Nor is anything a person or the daemon's queue opened on
// the pane's key: a stalled queue or a held prompt is about the person's own
// message.
//
// Setting the mark needs the admin grant (scopeDeny in conn_scope.go), because a
// pane that could hide its own questions from the person is the thing to be
// careful about.

// verbSetAgentQuiet sets or clears a window's quiet mark.
func (d *Daemon) verbSetAgentQuiet(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Quiet   *bool  `json:"quiet"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	target := p.Window
	if target == "" {
		id, err := focusedWindowID(sess.GetState())
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	quiet := p.Quiet == nil || *p.Quiet

	var w WindowState
	err := sess.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, target)
		if err != nil {
			return err
		}
		st.Windows[idx].AgentQuiet = quiet
		w = st.Windows[idx]
		return nil
	})
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	closed := 0
	// Going loud opens nothing: the old items stay closed, and new ones come
	// from the next transition.
	if quiet {
		closed = d.attention.closeQuiet(sess.Name(), w)
	}
	return map[string]any{
		"type":      "agent_quiet",
		"session":   sess.Name(),
		"window_id": w.ID,
		"quiet":     quiet,
		"closed":    closed,
	}, nil
}

// closeQuiet closes the items a pane that has just gone quiet would not have
// opened, snoozed ones too, and says how many. The item on the pane's blocking
// key closes only when it is the pane's own question. An approval stays, and so
// does an item the queue opened there, which says something else.
func (a *attentionStore) closeQuiet(sessionName string, w WindowState) int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	closed := 0
	closeKey := func(key string, match func(*AttentionItem) bool) {
		if id, ok := a.byKey[key]; ok && match(a.items[id]) {
			if a.closeLocked(id, AttentionClosedResolved) {
				closed++
			}
		}
		if id, ok := a.snoozedKey[key]; ok && match(a.snoozed[id]) {
			a.dropSnoozedLocked(id, AttentionClosedResolved)
			closed++
		}
	}
	every := func(*AttentionItem) bool { return true }
	closeKey(attentionKey(AttentionFinished, sessionName, w.ID, 0), every)
	closeKey(attentionKey(AttentionErrored, sessionName, w.ID, 0), every)
	closeKey(attentionKey(AttentionQuestion, sessionName, w.ID, 0), func(it *AttentionItem) bool {
		return it.Kind == AttentionQuestion && w.AgentState == AgentStateNeedsInput &&
			it.Summary == attentionText(w.AgentMessage, attentionMaxSummary)
	})
	return closed
}
