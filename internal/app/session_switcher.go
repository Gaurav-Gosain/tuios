package app

import "strings"

// SessionSwitcherItem represents a single session entry in the session switcher overlay.
type SessionSwitcherItem struct {
	Name      string
	Windows   int
	Clients   int
	IsCurrent bool
}

// RefreshSessionList populates the session switcher items from the daemon client.
// Queries the daemon for an up-to-date list (so newly created sessions appear).
// If not in daemon mode, returns nil.
func (m *OS) RefreshSessionList() []SessionSwitcherItem {
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
		items := make([]SessionSwitcherItem, 0, len(names))
		for _, name := range names {
			items = append(items, SessionSwitcherItem{
				Name:      name,
				IsCurrent: name == currentSession,
			})
		}
		return items
	}

	items := make([]SessionSwitcherItem, 0, len(sessions))
	for _, s := range sessions {
		items = append(items, SessionSwitcherItem{
			Name:      s.Name,
			Windows:   s.WindowCount,
			IsCurrent: s.Name == currentSession,
		})
	}
	return items
}

// FilterSessionItems filters session switcher items by a query string.
// It performs case-insensitive substring matching on Name.
func FilterSessionItems(items []SessionSwitcherItem, query string) []SessionSwitcherItem {
	if query == "" {
		return items
	}
	q := strings.ToLower(query)
	var filtered []SessionSwitcherItem
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Name), q) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
