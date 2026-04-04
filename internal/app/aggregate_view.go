package app

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// AggregateViewItem represents a window entry in the aggregate view.
type AggregateViewItem struct {
	Window      *terminal.Window
	WindowIndex int
	Workspace   int
	Title       string
	CWD         string
	IsFocused   bool
	IsMinimized bool
	Preview     string // First few lines of terminal content
}

// GetAggregateViewItems collects all windows across all workspaces.
func (m *OS) GetAggregateViewItems() []AggregateViewItem {
	var items []AggregateViewItem

	for i, w := range m.Windows {
		title := w.Title
		if w.CustomName != "" {
			title = w.CustomName
		}
		if title == "" {
			title = fmt.Sprintf("Window %s", w.ID[:8])
		}

		cwd := "" // CWD not directly accessible from emulator

		preview := ""
		if w.Terminal != nil {
			w.RLockIO()
			raw := w.Terminal.String()
			w.RUnlockIO()
			// Take first 3 non-empty lines as preview
			lines := strings.Split(raw, "\n")
			var previewLines []string
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" {
					previewLines = append(previewLines, trimmed)
					if len(previewLines) >= 3 {
						break
					}
				}
			}
			preview = strings.Join(previewLines, " | ")
			if len(preview) > 80 {
				preview = preview[:77] + "..."
			}
		}

		items = append(items, AggregateViewItem{
			Window:      w,
			WindowIndex: i,
			Workspace:   w.Workspace,
			Title:       title,
			CWD:         cwd,
			IsFocused:   i == m.FocusedWindow && w.Workspace == m.CurrentWorkspace,
			IsMinimized: w.Minimized,
			Preview:     preview,
		})
	}

	return items
}

// FilterAggregateViewItems filters items by query using fuzzy matching.
func FilterAggregateViewItems(items []AggregateViewItem, query string) []AggregateViewItem {
	if query == "" {
		return items
	}

	query = strings.ToLower(query)
	var filtered []AggregateViewItem

	for _, item := range items {
		// Match against title, CWD, workspace number, or preview
		searchText := strings.ToLower(fmt.Sprintf("%s %s %d %s",
			item.Title, item.CWD, item.Workspace+1, item.Preview))

		if fuzzyMatch(searchText, query) {
			filtered = append(filtered, item)
		}
	}

	return filtered
}

// fuzzyMatch checks if all characters in query appear in text in order.
func fuzzyMatch(text, query string) bool {
	ti := 0
	for qi := 0; qi < len(query); qi++ {
		found := false
		for ti < len(text) {
			if text[ti] == query[qi] {
				ti++
				found = true
				break
			}
			ti++
		}
		if !found {
			return false
		}
	}
	return true
}

// JumpToAggregateViewItem switches to the workspace and focuses the window.
func (m *OS) JumpToAggregateViewItem(item AggregateViewItem) {
	// Switch workspace if needed
	if item.Workspace != m.CurrentWorkspace {
		m.SwitchWorkspace(item.Workspace)
	}

	// Restore if minimized
	if item.IsMinimized {
		item.Window.Minimized = false
	}

	// Find and focus the window
	for i, w := range m.Windows {
		if w == item.Window {
			m.FocusWindow(i)
			break
		}
	}

	m.ShowAggregateView = false
}

