package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Workspace management methods

// SwitchToWorkspace switches to the specified workspace.
func (m *OS) SwitchToWorkspace(workspace int) {
	if workspace < 1 || workspace > m.NumWorkspaces {
		return
	}

	if workspace == m.CurrentWorkspace {
		return
	}

	// Save current workspace focus
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		if m.Windows[m.FocusedWindow].Workspace == m.CurrentWorkspace {
			m.WorkspaceFocus[m.CurrentWorkspace] = m.FocusedWindow
		}
	}

	// Switch to new workspace
	m.CurrentWorkspace = workspace

	// Try to restore previous focus for this workspace
	focusedSet := false
	if savedFocus, exists := m.WorkspaceFocus[workspace]; exists {
		// Check if the saved focus is still valid
		if savedFocus >= 0 && savedFocus < len(m.Windows) {
			if m.Windows[savedFocus].Workspace == workspace && !m.Windows[savedFocus].Minimized {
				m.FocusWindow(savedFocus)
				focusedSet = true
			}
		}
	}

	// If no saved focus or it's invalid, find first visible window in new workspace
	if !focusedSet {
		for i, w := range m.Windows {
			if w.Workspace == workspace && !w.Minimized && !w.Minimizing {
				m.FocusWindow(i)
				focusedSet = true
				break
			}
		}
	}

	// If no window to focus in new workspace, set focus to -1
	if !focusedSet {
		m.FocusedWindow = -1
		// Exit terminal mode when switching to empty workspace
		if m.Mode == TerminalMode {
			m.Mode = WindowManagementMode
		}
	}

	// Retile if in tiling mode
	if m.AutoTiling {
		m.TileVisibleWorkspaceWindows()
	}

	// Mark all windows in new workspace as dirty for immediate render
	for _, w := range m.Windows {
		if w.Workspace == workspace {
			w.MarkPositionDirty()
		}
	}
}

// MoveWindowToWorkspace moves a window to the specified workspace without changing focus.
func (m *OS) MoveWindowToWorkspace(windowIndex int, workspace int) {
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		return
	}
	if workspace < 1 || workspace > m.NumWorkspaces {
		return
	}

	window := m.Windows[windowIndex]
	oldWorkspace := window.Workspace

	if oldWorkspace == workspace {
		return // Already in target workspace
	}

	// Move window to new workspace
	window.Workspace = workspace
	window.MarkPositionDirty()

	// If we moved the focused window, find next window to focus in current workspace
	if windowIndex == m.FocusedWindow {
		m.FocusNextVisibleWindowInWorkspace()
	}

	// Retile both workspaces if in tiling mode
	if m.AutoTiling {
		m.TileVisibleWorkspaceWindows()
	}
}

// MoveWindowToWorkspaceAndFollow moves a window to the specified workspace and switches to that workspace.
func (m *OS) MoveWindowToWorkspaceAndFollow(windowIndex int, workspace int) {
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		return
	}
	if workspace < 1 || workspace > m.NumWorkspaces {
		return
	}

	window := m.Windows[windowIndex]
	oldWorkspace := window.Workspace

	if oldWorkspace == workspace {
		return // Already in target workspace
	}

	// Move window to new workspace
	window.Workspace = workspace
	window.MarkPositionDirty()

	// Switch to the new workspace and focus the moved window
	m.SwitchToWorkspace(workspace)
	m.FocusWindow(windowIndex)

	// Retile if in tiling mode
	if m.AutoTiling {
		m.TileVisibleWorkspaceWindows()
	}
}

// FocusNextVisibleWindowInWorkspace focuses the next visible window in the workspace.
func (m *OS) FocusNextVisibleWindowInWorkspace() {
	// Find the next non-minimized window in current workspace to focus
	for i := 0; i < len(m.Windows); i++ {
		w := m.Windows[i]
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			m.FocusWindow(i)
			return
		}
	}

	// No visible windows in workspace
	m.FocusedWindow = -1
	if m.Mode == TerminalMode {
		m.Mode = WindowManagementMode
	}
}

// GetVisibleWindows returns all visible windows in the current workspace.
func (m *OS) GetVisibleWindows() []*terminal.Window {
	visible := make([]*terminal.Window, 0)
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			visible = append(visible, w)
		}
	}
	return visible
}

// GetWorkspaceWindowCount returns the number of windows in a workspace.
func (m *OS) GetWorkspaceWindowCount(workspace int) int {
	count := 0
	for _, w := range m.Windows {
		if w.Workspace == workspace {
			count++
		}
	}
	return count
}

// TileVisibleWorkspaceWindows tiles all visible windows in the current workspace.
func (m *OS) TileVisibleWorkspaceWindows() {
	// Only tile windows in current workspace
	visibleWindows := make([]int, 0)
	for i, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			visibleWindows = append(visibleWindows, i)
		}
	}

	if len(visibleWindows) == 0 {
		return
	}

	// Use existing tiling logic but only for visible workspace windows
	layouts := m.calculateTilingLayout(len(visibleWindows))

	for i, windowIndex := range visibleWindows {
		if i < len(layouts) {
			window := m.Windows[windowIndex]
			window.X = layouts[i].x
			window.Y = layouts[i].y
			window.Width = layouts[i].width
			window.Height = layouts[i].height
			window.PositionDirty = true
		}
	}
}
