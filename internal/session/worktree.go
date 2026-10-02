//go:build !slim

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

// detectWorktree is the worktree record of a session whose first window
// starts in dir, or nil when dir is not in a git worktree.
func detectWorktree(dir string) *WorktreeInfo {
	if info, ok := worktree.Detect(dir); ok {
		return &WorktreeInfo{Info: info}
	}
	return nil
}
