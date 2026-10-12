package session

// SessionWorktree returns the worktree record the cached listing carries for
// the named session, or nil when the session is not a worktree, is unknown, or
// is served by a daemon too old to send the field.
//
// It reads the cache the way SessionRestored and SessionLabel do, and for the
// same reason: the rail asks on the UI goroutine and a round trip there would
// freeze the client. A copy is returned, so a refresh replacing the entry
// cannot change the record under the caller.
func (c *TUIClient) SessionWorktree(name string) *WorktreeInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name != name {
			continue
		}
		if s.Worktree == nil {
			return nil
		}
		cp := *s.Worktree
		return &cp
	}
	return nil
}

// RepoRoot returns the main checkout of the repository the rail groups under
// repo, read from the worktree record of any cached session in it, or "" when
// no cached session names the repository. It reads the cache for the same
// reason SessionWorktree does.
//
// The rail groups sessions by the repository's base name, so two different
// checkouts both named app share one group header, and this returns whichever
// root a cached session carries first — not necessarily the one the user
// meant. The dialog's context line names the path it resolved to, so the
// mismatch is visible before enter; the session-row way in is exact, since it
// reads one session's own record.
func (c *TUIClient) RepoRoot(repo string) string {
	if repo == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Worktree != nil && s.Worktree.Repo == repo && s.Worktree.RepoRoot != "" {
			return s.Worktree.RepoRoot
		}
	}
	return ""
}
