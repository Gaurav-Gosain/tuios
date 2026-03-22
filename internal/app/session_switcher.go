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
// If not in daemon mode, returns nil.
func (m *OS) RefreshSessionList() []SessionSwitcherItem {
	if m.DaemonClient == nil {
		return nil
	}

	names := m.DaemonClient.AvailableSessionNames()
	currentSession := m.DaemonClient.SessionName()

	items := make([]SessionSwitcherItem, 0, len(names))
	for _, name := range names {
		items = append(items, SessionSwitcherItem{
			Name:      name,
			IsCurrent: name == currentSession,
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
