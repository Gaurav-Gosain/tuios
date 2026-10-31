package session

import (
	"encoding/json"
	"errors"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
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
// An approval or a login is never held back, since the pane cannot move until a
// person acts, and a held prompt that nobody sees is a pane stuck for
// hold_seconds. Nor is anything a person or the daemon's queue opened on the
// pane's key: a stalled queue or a held prompt is about the person's own message.
//
// Setting the mark needs the admin grant (scopeDeny in conn_scope.go), because a
// pane that could hide its own questions from the person is the thing to be
// careful about. In the default mode open every pane holds admin, so a moderator
// pane can quiet its workers. It marks them before they ask: a pane that sets the
// mark does not close a question already open, because a pane must not answer for
// the person that nobody needs to see it. Only the person closes one that way.
//
// The mark needs an agent in the pane and goes when that agent leaves, as a settle
// does, so the next agent cannot inherit a mark it was not given. Clearing it while the pane waits raises the
// question as if it had just arrived, because the pane is blocked and nothing else
// says so.

// clearQuietOfLeftAgentLocked clears the mark of a pane whose agent has left, so
// the next agent in that pane does not inherit it. It runs inside every daemon-side
// state change, as wakeSettledLocked does. The caller holds the state lock.
func clearQuietOfLeftAgentLocked(before lifecycleSnapshot, st *SessionState) {
	for i := range st.Windows {
		w := &st.Windows[i]
		if !w.AgentQuiet || w.AgentState != AgentStateNone {
			continue
		}
		if idx, ok := before.index[w.ID]; ok && before.windows[idx].agentState != AgentStateNone {
			w.AgentQuiet = false
		}
	}
}

// AgentKindReachesPerson reports whether a needs_input of this kind reaches the
// person from a quiet pane.
func AgentKindReachesPerson(kind string) bool {
	return kind == harness.PromptKindApproval || kind == harness.PromptKindAuth
}

// errQuietNoAgent refuses the mark on a pane with no agent.
var errQuietNoAgent = errors.New("the pane runs no agent")

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
	was := false
	err := sess.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, target)
		if err != nil {
			return err
		}
		if quiet && st.Windows[idx].AgentState == AgentStateNone {
			return errQuietNoAgent
		}
		was = st.Windows[idx].AgentQuiet
		st.Windows[idx].AgentQuiet = quiet
		w = st.Windows[idx]
		return nil
	})
	if errors.Is(err, errQuietNoAgent) {
		return nil, invalidParam("window", "the pane runs no agent, so there is nothing to mark quiet")
	}
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	closed := 0
	switch {
	case quiet:
		closed = d.attention.closeQuiet(sess.Name(), w, d.mayActAsHuman(cs))
	case was && w.AgentState == AgentStateNeedsInput:
		// The pane waits, and this is the first the person hears of it. Finished
		// turns and failures from while it was quiet stay closed.
		d.attention.noteAgentState(sess.Name(), SessionEvent{
			Type:          EventAgentState,
			Window:        w.ID,
			State:         w.AgentState.Name(),
			hookTitle:     windowLabelOf(w),
			hookWorkspace: w.Workspace,
			hookHarness:   w.AgentHarness,
			hookMessage:   w.AgentMessage,
			hookKind:      agentBlockedBy(w),
		})
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
// key closes only when it is the pane's own question and person says the person
// asked. An approval or a login stays, and so does an item the queue opened
// there, which says something else.
func (a *attentionStore) closeQuiet(sessionName string, w WindowState, person bool) int {
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
		return person && it.Kind == AttentionQuestion && w.AgentState == AgentStateNeedsInput &&
			!AgentKindReachesPerson(agentBlockedBy(w)) && it.Summary == attentionText(w.AgentMessage, attentionMaxSummary)
	})
	return closed
}
