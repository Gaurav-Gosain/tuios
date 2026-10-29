package session

import (
	"errors"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// Settling an agent: the person puts it away on the agents view's Settled
// shelf without ending it. The settle is a field on the pane's window state
// (WindowState.AgentSettled), as the queue's count is, so every client that
// draws the pane, on this machine or through a link, draws the same shelf.
// A client never sets it: mark-attention settle and unsettle are the only
// writers from outside, and they take the person's nonce, so an agent cannot
// hide itself or another agent.
//
// A settled agent comes back on its own when it needs the person: it asks
// (needs_input), it fails (errored), it finishes a turn, it sends the person
// mail, or it leaves the pane. wakeSettledLocked does the state half of that
// in the same change that moved the state, and the mail half is in
// send-agent-message. There is no timer: an agent at rest for an hour moves
// to the shelf in the agents view, which compares the state's time with the
// clock it already ticks while it is open.
//
// settleAgent takes a reason, so a later caller that settles on its own (a
// merged branch, say) goes through the same write and the same hook.

// Why an agent was settled, as after-settle's TUIOS_SETTLE_REASON says it.
const settleByPerson = "person"

// errSettleUnchanged tells mutateState that the pane was already as asked, so
// nothing is pushed and no hook fires.
var errSettleUnchanged = errors.New("settle unchanged")

// wakeSettledLocked clears the settle of every pane whose agent needs the
// person again since before: it asked, it failed, it finished a turn, or it
// left the pane. It runs inside every daemon-side state change, so the
// clear goes out with the change that caused it. The caller holds the state
// lock.
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

// settleAgent sets or clears the settle of a pane and, for a settle that
// changed something, runs the after-settle hook with reason. found is false
// for a pane that is not in the session.
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

// wakeSettled clears the settle of a pane that needs the person again for a
// reason its state does not show: it sent the person mail.
func (d *Daemon) wakeSettled(sess *Session, window string) {
	if sess == nil || window == "" {
		return
	}
	if w, ok := findWindowState(sess.GetState(), window); !ok || !w.AgentSettled {
		return
	}
	d.settleAgent(sess, window, false, "")
}

// fireSettleHook runs after-settle for a pane just settled. It starts in the
// session's worktree when tuios made one, else in the folder the pane
// started in, so a hook such as "cargo clean" acts on the agent's tree.
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

// paneOpenItems are the open Inbox items about one pane of this machine.
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

// markAttentionSettle answers mark-attention settle and unsettle. A settle
// closes the pane's finished turn as a dismiss does, since the person is done
// with it, and names that item in the answer as id, so an unsettle that
// passes the id back puts it in the Inbox again. A pane that needs the person
// now is not settled: the answer comes first.
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
