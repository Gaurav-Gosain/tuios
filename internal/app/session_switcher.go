package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// RefreshSessionList populates the session switcher items from the daemon client.
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
		items = append(items, sessiontree.BuildSession(sessiontree.SessionInput{
			Name:        s.Name,
			DisplayName: s.DisplayName,
			IsCurrent:   s.Name == currentSession,
			WindowCount: s.WindowCount,
			Restored:    s.Restored,
		}))
	}
	return items
}

// OpenSessionSwitcher shows the session switcher with a fresh session list.
// Shared by the keybinding, the palette entry, and the quit menu, so all of
// them reset the same state in the same place.
//
// The list comes from the same tree the rail draws, so the switcher agrees with
// the rail on labels, order and rolled-up agent state, and opening costs no
// daemon round trip. The cache behind it is refreshed off the UI goroutine, at
// the faster cadence Update uses while this overlay is open.
func (m *OS) OpenSessionSwitcher() {
	m.ShowSessionSwitcher = true
	m.SessionSwitcherQuery = ""
	m.SessionSwitcherSelected = 0
	m.SessionSwitcherScroll = 0
	m.SessionSwitcherError = ""
	m.SessionSwitcherItems = m.BuildSessionTree().Sessions
}

// SessionSwitcherTarget returns the session the switcher's selection points at,
// resolved against the FILTERED list. Every activation path goes through it:
// resolving an index against the unfiltered items is the off-by-one this exists
// to prevent, because with a query typed, row n on screen is not item n.
func (m *OS) SessionSwitcherTarget(idx int) (sessiontree.Node, bool) {
	filtered := FilterSessionItems(m.SessionSwitcherItems, m.SessionSwitcherQuery)
	if idx < 0 || idx >= len(filtered) {
		return sessiontree.Node{}, false
	}
	return filtered[idx], true
}

// FilterSessionItems filters session switcher items by a query string, matching
// case-insensitively on the label and on the identity name behind it. Both are
// matched because a renamed session is still found by what it is called on the
// command line, and an unrenamed one has only the identity to match.
func FilterSessionItems(items []sessiontree.Node, query string) []sessiontree.Node {
	if query == "" {
		return items
	}
	q := strings.ToLower(query)
	var filtered []sessiontree.Node
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Title), q) || strings.Contains(strings.ToLower(item.ID), q) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
