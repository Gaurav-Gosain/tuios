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

// SessionSpace returns the space the cached listing files the named session
// under, or "" when it is in none, is unknown, or is served by a daemon too
// old to send the field.
func (c *TUIClient) SessionSpace(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return s.Space
		}
	}
	return ""
}

// SessionAttached reports whether the cached listing says a client is
// attached to the named session. False when it is unknown.
func (c *TUIClient) SessionAttached(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return s.Attached
		}
	}
	return false
}
