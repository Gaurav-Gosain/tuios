package session

import (
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// A worktree session is a session whose directory is a linked git worktree.
// The daemon records which one, so the rail can group it under its repository
// and label it by its branch, and so the worktree verbs can take it away
// again without re-deriving where it came from.
//
// The record has two sources. The worktree verbs write it when they create
// the worktree, and mark it managed: tuios made it, so tuios may remove it.
// Every other session gets it by detection from its first window's directory,
// which is read from files and never from git. A detected record follows the
// directory: when the shell moves out of the worktree the record goes with it.
// A managed record does not, because the session is the worktree.

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

// setPromptStatus updates the fan prompt's status on a worktree session.
func (s *Session) setPromptStatus(status, note string, at int64) {
	_ = s.mutateState(func(st *SessionState) error {
		if st.Worktree == nil {
			return nil
		}
		st.Worktree.PromptStatus = status
		st.Worktree.PromptNote = note
		st.Worktree.PromptAt = at
		return nil
	})
}

// setPromptReadyBy records the evidence the agent was ready on.
func (s *Session) setPromptReadyBy(by string) {
	_ = s.mutateState(func(st *SessionState) error {
		if st.Worktree != nil {
			st.Worktree.PromptReadyBy = by
		}
		return nil
	})
}

// worktreeListing is the worktree record as the listing reports it: the
// state's record with Gone filled in from the directory. A managed session
// whose directory was removed keeps its record, marked gone. A detected
// session whose directory went is no longer in a worktree at all.
func (s *Session) worktreeListing() *WorktreeInfo {
	info := s.Worktree()
	if info == nil {
		return nil
	}
	if _, err := os.Stat(info.Path); err != nil {
		if !info.Managed {
			return nil
		}
		info.Gone = true
	}
	return info
}

// refreshWorktree re-detects a session's worktree from its first window's
// directory. It is called when a window is created and on every dirty save,
// so it costs nothing while the session is idle. A managed record is left
// alone: the verbs that wrote it own it.
//
// cwd is the directory to look at when the caller already knows it. Empty
// means read the first window's process directory.
func (s *Session) refreshWorktree(cwd string) {
	s.stateMu.RLock()
	managed := s.state.Worktree != nil && s.state.Worktree.Managed
	var current *WorktreeInfo
	if s.state.Worktree != nil {
		cp := *s.state.Worktree
		current = &cp
	}
	var firstPTY string
	if len(s.state.Windows) > 0 {
		firstPTY = s.state.Windows[0].PTYID
	}
	s.stateMu.RUnlock()
	if managed {
		return
	}
	if cwd == "" && firstPTY != "" {
		if pty := s.GetPTY(firstPTY); pty != nil {
			cwd, _ = pty.ProcessCwd()
		}
	}
	if cwd == "" {
		return
	}
	detected, ok := worktree.Detect(cwd)
	switch {
	case !ok && current == nil:
		return
	case !ok:
		_ = s.SetWorktree(nil)
	case current != nil && current.Info == detected:
		return
	default:
		_ = s.SetWorktree(&WorktreeInfo{Info: detected})
	}
}
