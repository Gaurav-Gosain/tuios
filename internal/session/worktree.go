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
	// Managed marks a worktree tuios created under its own directory.
	Managed bool `json:"managed,omitempty"`
	// Prompt is the text a fan-out delivers to the agent in this session.
	Prompt string `json:"prompt,omitempty"`
	// PromptStatus says what became of Prompt: "pending" while the daemon waits
	// for the agent to be ready, "sent" once typed, "not_sent" when the wait
	// ended without an agent to type at. Empty when there was no prompt.
	PromptStatus string `json:"prompt_status,omitempty"`
	// PromptNote is one sentence explaining a not_sent status.
	PromptNote string `json:"prompt_note,omitempty"`
	// PromptAt is when the prompt was typed, as Unix nanoseconds.
	PromptAt int64 `json:"prompt_at,omitempty"`
	// Gone is set on the listing copy only: the worktree directory no longer
	// exists. The session is kept, because a shell whose directory was removed
	// under it still runs and an agent in it may still have something to say.
	Gone bool `json:"gone,omitempty"`
}

// Prompt statuses, as the wire carries them.
const (
	PromptPending = "pending"
	PromptSent    = "sent"
	PromptNotSent = "not_sent"
)

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
