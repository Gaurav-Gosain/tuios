package session

import "github.com/Gaurav-Gosain/tuios/internal/worktree"

// WorktreeInfo is the daemon's record of a worktree session. It sits on
// SessionState, so it is pushed to every client and saved for resurrection,
// and it is copied onto SessionInfo for the listing.
type WorktreeInfo struct {
	worktree.Info
	// Base is the ref the branch was created from, when tuios created it.
	Base string `json:"base,omitempty"`
	// Group names the fan-out this session belongs to: the branch stem the
	// siblings share. Empty for a worktree made on its own.
	Group string `json:"group,omitempty"`
	// LaunchedFrom names the session whose pane ran the fan that made this
	// one, when a pane of this daemon ran it. A connection restricted to its
	// own session reaches the sessions its session launched. Empty when the
	// fan came from outside every pane, and on a record from before the field.
	LaunchedFrom string `json:"launched_from,omitempty"`
	// Managed marks a worktree tuios created under its own directory.
	Managed bool `json:"managed,omitempty"`
	// Prompt is the text a fan-out delivers to the agent in this session.
	Prompt string `json:"prompt,omitempty"`
	// PromptStatus says what became of Prompt: "pending" while the daemon waits
	// for the agent to be ready, "sent" once typed and taken, "not_sent" when
	// the wait ended without an agent to type at, "stalled" when it was typed
	// and the agent showed no sign of taking it. Empty when there was no prompt.
	PromptStatus string `json:"prompt_status,omitempty"`
	// PromptNote is one sentence explaining a not_sent or stalled status.
	PromptNote string `json:"prompt_note,omitempty"`
	// PromptAt is when the prompt was typed, as Unix nanoseconds.
	PromptAt int64 `json:"prompt_at,omitempty"`
	// PromptReadyBy is the evidence the agent was ready when the prompt was
	// typed: the state it read (idle, done), or quiet for unknown on a
	// harness that can never show more. Empty until the prompt is typed.
	PromptReadyBy string `json:"prompt_ready_by,omitempty"`
	// Agent is the agent a fan-out started in this session, as the caller
	// named it ("claude", "codex --model o5"). A fan of several agents names
	// a different one per session. Empty for a worktree made on its own.
	Agent string `json:"agent,omitempty"`
	// Gone is set on the listing copy only: the worktree directory no longer
	// exists. The session is kept, because a shell whose directory was removed
	// under it still runs and an agent in it may still have something to say.
	Gone bool `json:"gone,omitempty"`
	// Verify is the last check verify-fan ran in this session, nil when none
	// has run. It is saved with the session, so a compare after a restart
	// still says what the last check found. Additive: an older client drops
	// it. Replaced whole, never edited in place, so a copy of the info can
	// share it. See verb_fan_compare.go.
	Verify *FanVerify `json:"verify,omitempty"`
}

// FanVerify is one verify-fan check in one fan sibling: the command, and what
// became of it.
type FanVerify struct {
	// Command is the command as the caller gave it, which the daemon ran with
	// sh -c in a window named verify.
	Command string `json:"command"`
	// State is running, passed or failed.
	State string `json:"state"`
	// Exit is the command's exit status once it finished, nil while it runs
	// or when the process reported none.
	Exit *int `json:"exit,omitempty"`
	// StartedAt and FinishedAt are unix nanoseconds. FinishedAt is zero while
	// the command runs.
	StartedAt  int64 `json:"started_at,omitempty"`
	FinishedAt int64 `json:"finished_at,omitempty"`
	// Note says why a failed check has no exit status: it timed out, or the
	// daemon restarted while it ran. Empty otherwise.
	Note string `json:"note,omitempty"`
}

// Verify states, as the wire carries them.
const (
	VerifyRunning = "running"
	VerifyPassed  = "passed"
	VerifyFailed  = "failed"
)

// VerifyStateNames lists the verify states.
var VerifyStateNames = []string{VerifyRunning, VerifyPassed, VerifyFailed}

// Prompt statuses, as the wire carries them.
const (
	PromptPending = "pending"
	PromptSent    = "sent"
	PromptNotSent = "not_sent"
	// PromptStalled is a prompt that was typed and submitted, after which the
	// agent showed no sign of taking it within the stall window. The text may
	// still be in the agent's input box. See prompt_gate.go.
	PromptStalled = "stalled"
	// PromptHeld is a prompt still waiting after the agent has not been
	// ready for a while (agentHeldAfter), at a screen tuios does not
	// recognise. It is typed as soon as the agent is ready, and the Inbox
	// holds a question for the pane meanwhile. It ends sent, stalled or
	// not_sent like a pending one.
	PromptHeld = "held"
)

// PromptWaiting reports whether a prompt status is one the daemon is still
// going to act on: pending or held.
func PromptWaiting(status string) bool {
	return status == PromptPending || status == PromptHeld
}

// SetWorktree records the worktree this session is, or clears it with nil. It
// propagates and persists the way SetDisplayName does.
func (s *Session) SetWorktree(info *WorktreeInfo) error {
	return s.mutateState(func(st *SessionState) error {
		st.Worktree = info
		return nil
	})
}

// Worktree returns a copy of the session's worktree record, or nil.
func (s *Session) Worktree() *WorktreeInfo {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.state.Worktree == nil {
		return nil
	}
	cp := *s.state.Worktree
	return &cp
}

// restoredWorktree is a saved managed worktree record as a restored session
// takes it, or nil for a record detection owns. A fan prompt still waiting
// when the daemon went down is said not to have been sent: nothing resumes the
// wait across a restart, so a record left pending would say so forever. A
// check that was running needs nothing here; fanVerifyReport reads it as ended
// by the restart.
func restoredWorktree(saved *WorktreeInfo) *WorktreeInfo {
	if saved == nil || !saved.Managed {
		return nil
	}
	wt := *saved
	if PromptWaiting(wt.PromptStatus) {
		wt.PromptStatus = PromptNotSent
		wt.PromptNote = "The daemon restarted before the prompt was typed. Send the prompt with send-text."
	}
	return &wt
}
