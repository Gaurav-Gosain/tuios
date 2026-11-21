package session

import (
	"encoding/json"
	"errors"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

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

var errQuietNoAgent = errors.New("the pane runs no agent")

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
