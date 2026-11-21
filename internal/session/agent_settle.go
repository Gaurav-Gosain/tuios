package session

import (
	"errors"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

const settleByPerson = "person"

var errSettleUnchanged = errors.New("settle unchanged")

func wakeSettledLocked(before lifecycleSnapshot, st *SessionState) {
	for i := range st.Windows {
		w := &st.Windows[i]
		if !w.AgentSettled {
			continue
		}
		idx, ok := before.index[w.ID]
		if !ok {
			continue
		}
		prev := &before.windows[idx]
		switch {
		case w.CompletionSeq > prev.completionSeq:
		case w.AgentState == prev.agentState:
			continue
		case w.AgentState == AgentStateNeedsInput, w.AgentState == AgentStateErrored, w.AgentState == AgentStateNone:
		default:
			continue
		}
		w.AgentSettled = false
	}
}

func (d *Daemon) settleAgent(sess *Session, window string, settled bool, reason string) (changed, found bool) {
	var w WindowState
	err := sess.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, window)
		if err != nil {
			return err
		}
		found = true
		if st.Windows[idx].AgentSettled == settled {
			return errSettleUnchanged
		}
		st.Windows[idx].AgentSettled = settled
		w = st.Windows[idx]
		return nil
	})
	if err != nil {
		return false, found
	}
	if settled {
		d.fireSettleHook(sess, w, reason)
	}
	return true, true
}

func (d *Daemon) wakeSettled(sess *Session, window string) {
	if sess == nil || window == "" {
		return
	}
	if w, ok := findWindowState(sess.GetState(), window); !ok || !w.AgentSettled {
		return
	}
	d.settleAgent(sess, window, false, "")
}

func (d *Daemon) fireSettleHook(sess *Session, w WindowState, reason string) {
	if d.hooks == nil || !d.hooks.HasEvent(hooks.AfterSettle) || d.agentsOff.Load() {
		return
	}
	ctx := hooks.Context{
		SessionID:    sess.Name(),
		WindowID:     w.ID,
		WindowName:   windowLabelOf(w),
		Workspace:    w.Workspace,
		SettleReason: reason,
		Dir:          w.StartDir,
	}
	if wt := sess.GetState().Worktree; wt != nil && wt.Path != "" {
		ctx.WorktreePath, ctx.WorktreeBranch, ctx.WorktreeRepoRoot = wt.Path, wt.Branch, wt.RepoRoot
		ctx.Dir = wt.Path
	}
	d.hooks.Fire(hooks.AfterSettle, ctx)
}

func (a *attentionStore) paneOpenItems(sessionName, window string) []AttentionItem {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []AttentionItem
	for _, it := range a.items {
		if it.Host == "" && it.Session == sessionName && it.Window == window {
			out = append(out, *it)
		}
	}
	return out
}

func (d *Daemon) markAttentionSettle(cs *connState, p markAttentionParams) (any, *verbError) {
	if p.Window == "" {
		return nil, invalidParam("window", "settle and unsettle name the pane with session and window")
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if _, ok := d.humanNonceFor(p.HumanNonce, sess.ID, cs); !ok {
		return nil, nonceScopeError("mark-attention")
	}
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := state.Windows[idx]
	out := map[string]any{"type": "attention_marked", "action": p.Action, "session": sess.Name(), "window": w.ID}
	if p.Action == "unsettle" {
		d.settleAgent(sess, w.ID, false, "")
		if p.ID != "" {
			p.Session, p.Window = "", ""
			if _, verr := d.markAttentionRestore(p); verr == nil {
				out["id"] = p.ID
			}
		}
		return out, nil
	}
	if w.AgentState == AgentStateNone {
		return nil, invalidParam("window", "the pane runs no agent, so there is nothing to settle")
	}
	if w.AgentState == AgentStateNeedsInput {
		return nil, invalidParam("window", "the agent needs you now: answer it first")
	}
	items := d.attention.paneOpenItems(sess.Name(), w.ID)
	for _, it := range items {
		if it.Kind != AttentionFinished {
			return nil, invalidParam("window", "the agent has a "+it.Kind+" item in the Inbox: answer it or dismiss it first")
		}
	}
	for _, it := range items {
		if closed, ok := d.attention.dismiss(it.ID); ok {
			sess.MarkCompletionSeen(closed.Window)
			out["id"] = closed.ID
		}
	}
	d.settleAgent(sess, w.ID, true, settleByPerson)
	return out, nil
}
