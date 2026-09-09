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
