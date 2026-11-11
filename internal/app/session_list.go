package app

import "github.com/Gaurav-Gosain/tuios/internal/sessiontree"

// RefreshSessionList lists the daemon's sessions, for a tape's session commands.
// Queries the daemon for an up-to-date list (so newly created sessions appear).
// If not in daemon mode, returns nil.
//
// Each entry is a coarse sessiontree.Node (KindSession, no Children): the same
// type the command palette's session entries and BuildSessionTree use, so a
// session name and its "current" flag are read from one shape everywhere.
func (m *OS) RefreshSessionList() []sessiontree.Node {
	if m.DaemonClient == nil {
		return nil
	}

	// Query daemon for fresh session list (not cached)
	sessions, err := m.DaemonClient.RefreshSessionList()
	currentSession := m.DaemonClient.SessionName()

	if err != nil {
		// Fall back to cached names on error
		m.LogWarn("Failed to refresh session list from daemon: %v", err)
		names := m.DaemonClient.AvailableSessionNames()
		items := make([]sessiontree.Node, 0, len(names))
		for _, name := range names {
			items = append(items, sessiontree.BuildSession(sessiontree.SessionInput{
				Name:      name,
				IsCurrent: name == currentSession,
			}))
		}
		return items
	}

	items := make([]sessiontree.Node, 0, len(sessions))
	for _, s := range sessions {
		dir, branch := m.sessionPlace(s.Name)
		items = append(items, sessiontree.BuildSession(sessiontree.SessionInput{
			Name:        s.Name,
			DisplayName: s.DisplayName,
			Dir:         dir,
			Branch:      branch,
			AgentTitle:  m.sessionAgentTitle(s.Name),
			IsCurrent:   s.Name == currentSession,
			WindowCount: s.WindowCount,
			Restored:    s.Restored,
			Note:        s.Note,
		}))
	}
	return items
}
