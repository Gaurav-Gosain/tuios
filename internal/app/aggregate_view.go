package app

import (
	"fmt"
	"sort"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
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
	IsFloating  bool
}

// GetAggregateViewItems collects every window in the session, in the order the
// picker lists them: by where their workspace is shown, then by the order the
// windows were made. Ordering by the display order rather than by the workspace
// number is what keeps the picker agreeing with the dock strip and the rail
// after a workspace has been dragged somewhere else.
func (m *OS) GetAggregateViewItems() []AggregateViewItem {
	items := make([]AggregateViewItem, 0, len(m.Windows))

	for i, w := range m.Windows {
		// Laundered here rather than at the three places that draw it: the field
		// is only ever shown or searched, never used to find the window again.
		title := printableTitle(w.Title())
		if w.CustomName != "" {
			title = printableTitle(w.CustomName)
		}
		if title == "" {
			title = fmt.Sprintf("Window %s", shortID(w.ID))
		}

		// Cached per window and refreshed at most once a second, so building
		// the list does not cost a readlink per window per keystroke.
		cwd := w.CWD()

		items = append(items, AggregateViewItem{
			Window:      w,
			WindowIndex: i,
			Workspace:   w.Workspace,
			Title:       title,
			CWD:         cwd,
			IsFocused:   i == m.FocusedWindow && w.Workspace == m.CurrentWorkspace,
			IsMinimized: w.Minimized,
			IsFloating:  w.IsFloating,
		})
	}

	sort.SliceStable(items, func(a, b int) bool {
		return m.workspaceRank(items[a].Workspace) < m.workspaceRank(items[b].Workspace)
	})
	return items
}

// FilterAggregateViewItems filters items by query, best match first.
func FilterAggregateViewItems(items []AggregateViewItem, query string) []AggregateViewItem {
	if query == "" {
		return items
	}

	var m fuzzy.Matcher
	hits := m.FilterIndex(query, len(items), func(i int) string {
		// Everything the row shows is searchable, and nothing it does not: a
		// query used to be able to match a pane's scrollback, so a window could
		// be filtered in for a reason invisible on its row.
		return fmt.Sprintf("%s %s %d", items[i].Title, items[i].CWD, items[i].Workspace)
	})

	filtered := make([]AggregateViewItem, len(hits))
	for i, h := range hits {
		filtered[i] = items[h.Index]
	}
	return filtered
}

// AggregateViewJump jumps to row i of the filtered list, which is what Enter
// and a click on the row both do. Shared so the two cannot come to mean
// different things, and so the click gets the same bounds check the key does.
func (m *OS) AggregateViewJump(i int) {
	filtered := FilterAggregateViewItems(m.GetAggregateViewItems(), m.AggregateViewQuery)
	if i < 0 || i >= len(filtered) {
		return
	}
	m.JumpToAggregateViewItem(filtered[i])
	m.AggregateViewQuery = ""
	m.AggregateViewSelected = 0
	m.AggregateViewScroll = 0
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
