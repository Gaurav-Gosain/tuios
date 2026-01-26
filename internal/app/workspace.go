package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// Workspace management methods

// SwitchToWorkspace switches to the specified workspace.
func (m *OS) SwitchToWorkspace(workspace int) {
	if workspace < 1 || workspace > m.NumWorkspaces {
		m.LogWarn("Cannot switch to workspace %d: out of range (1-%d)", workspace, m.NumWorkspaces)
		return
	}

	if workspace == m.CurrentWorkspace {
		return
	}

	// Record workspace switch for tape recording
	if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
		m.TapeRecorder.RecordWorkspaceSwitch(workspace)
	}

	oldWorkspace := m.CurrentWorkspace
	windowsInNew := m.GetWorkspaceWindowCount(workspace)
	m.LogInfo("Switching workspace: %d → %d (%d windows)", oldWorkspace, workspace, windowsInNew)

	// Clear all animations BEFORE switching to prevent windows from getting stuck mid-animation
	// Then directly position windows to correct tiled layout WITHOUT creating new animations
	if len(m.Animations) > 0 {
		m.Animations = m.Animations[:0] // Cancel all animations without snapping to potentially wrong end positions
		m.LogInfo("Cancelled animations during workspace switch")

		// For tiling mode: directly position windows to correct tiled layout
		if m.AutoTiling {
			visibleWindows := make([]int, 0)
			for i, w := range m.Windows {
				if w.Workspace == oldWorkspace && !w.Minimized && !w.Minimizing {
					visibleWindows = append(visibleWindows, i)
				}
			}

			if len(visibleWindows) > 0 {
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
				m.LogInfo("Repositioned windows in workspace %d to correct tiled layout", oldWorkspace)
			}
		}
	}

	// Save current workspace focus and layout
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		if m.Windows[m.FocusedWindow].Workspace == m.CurrentWorkspace {
			m.WorkspaceFocus[m.CurrentWorkspace] = m.FocusedWindow
		}
	}
	m.SaveCurrentLayout() // Save layout before switching

	// Unsubscribe from old workspace PTYs and subscribe to new workspace PTYs
	// This optimization reduces network traffic by only streaming output for visible windows
	if m.IsDaemonSession && m.DaemonClient != nil {
		m.UnsubscribeWorkspaceWindows(oldWorkspace)
		m.SubscribeWorkspaceWindows(workspace)
	}

	// Switch to new workspace
	m.CurrentWorkspace = workspace
	m.RestoreWorkspaceLayout(workspace) // Restore layout after switching

	// Try to restore previous focus for this workspace
	focusedSet := false
	if savedFocus, exists := m.WorkspaceFocus[workspace]; exists {
		// Check if the saved focus is still valid
		if savedFocus >= 0 && savedFocus < len(m.Windows) {
			if m.Windows[savedFocus].Workspace == workspace && !m.Windows[savedFocus].Minimized {
				m.FocusWindow(savedFocus)
				m.LogInfo("Restored focus to saved window (index: %d)", savedFocus)
				focusedSet = true
			}
		}
	}

	// If no saved focus or it's invalid, find first visible window in new workspace
	if !focusedSet {
		for i, w := range m.Windows {
			if w.Workspace == workspace && !w.Minimized && !w.Minimizing {
				m.FocusWindow(i)
				m.LogInfo("Focused first visible window (index: %d)", i)
				focusedSet = true
				break
			}
		}
	}

	// If no window to focus in new workspace, set focus to -1
	if !focusedSet {
		m.FocusedWindow = -1
		m.LogInfo("No visible windows in workspace %d", workspace)
		// Exit terminal mode when switching to empty workspace
		if m.Mode == TerminalMode {
			// Record mode switch for tape recording
			if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
				m.TapeRecorder.RecordModeSwitch(tape.CommandTypeWindowManagementMode)
			}
			m.Mode = WindowManagementMode
			m.LogInfo("Switched to window management mode (empty workspace)")
		}
	} else {
		// Record the preserved mode after workspace switch (for consistent playback)
		// This ensures playback maintains the correct mode even if window state differs
		if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
			if m.Mode == TerminalMode {
				m.TapeRecorder.RecordModeSwitch(tape.CommandTypeTerminalMode)
			} else {
				m.TapeRecorder.RecordModeSwitch(tape.CommandTypeWindowManagementMode)
			}
		}
	}

	// Retile if in tiling mode and no custom layout
	if m.AutoTiling && !m.WorkspaceHasCustom[workspace] {
		m.LogInfo("Auto-tiling workspace %d (no custom layout)", workspace)
		m.TileVisibleWorkspaceWindows()
	}

	// Mark all windows in new workspace as dirty for immediate render
	for _, w := range m.Windows {
		if w.Workspace == workspace {
			w.MarkPositionDirty()
		}
	}

	// Sync state to daemon after workspace switch
	m.SyncStateToDaemon()
}

// MoveWindowToWorkspace moves a window to the specified workspace without changing focus.
func (m *OS) MoveWindowToWorkspace(windowIndex int, workspace int) {
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		m.LogWarn("Cannot move window: invalid index %d", windowIndex)
		return
	}
	if workspace < 1 || workspace > m.NumWorkspaces {
		m.LogWarn("Cannot move window: workspace %d out of range (1-%d)", workspace, m.NumWorkspaces)
		return
	}

	window := m.Windows[windowIndex]
	oldWorkspace := window.Workspace

	if oldWorkspace == workspace {
		return // Already in target workspace
	}

	m.LogInfo("Moving window %s: workspace %d → %d", window.Title, oldWorkspace, workspace)

	// If window is moving away from the current visible workspace, unsubscribe from its PTY
	if m.IsDaemonSession && m.DaemonClient != nil && oldWorkspace == m.CurrentWorkspace {
		m.unsubscribeFromPTY(window)
	}

	// Move window to new workspace FIRST
	window.Workspace = workspace
	window.MarkPositionDirty()

	// If we moved the focused window, find next window to focus in current workspace
	if windowIndex == m.FocusedWindow {
		m.LogInfo("Moved focused window, finding next in workspace %d", m.CurrentWorkspace)
		m.FocusNextVisibleWindowInWorkspace()
	}

	// Retile current workspace AFTER moving (if in tiling mode)
	// Now the filter excludes the moved window, so we tile N-1 windows correctly
	if m.AutoTiling {
		m.LogInfo("Auto-tiling workspace %d after window move", m.CurrentWorkspace)
		m.TileVisibleWorkspaceWindows()
		// Save the layout immediately to capture the correct window positions
		m.SaveCurrentLayout()
		// Mark as non-custom so it can be retiled later if needed
		m.WorkspaceHasCustom[m.CurrentWorkspace] = false
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

	// If window is moving away from the current visible workspace, unsubscribe from its PTY
	// This must be done BEFORE changing window.Workspace, so we can track it correctly
	if m.IsDaemonSession && m.DaemonClient != nil && oldWorkspace == m.CurrentWorkspace {
		m.unsubscribeFromPTY(window)
	}

	// Move window to new workspace FIRST
	window.Workspace = workspace
	window.MarkPositionDirty()

	// Retile old workspace AFTER moving (while still on it)
	// Now the filter excludes the moved window, so we tile N-1 windows correctly
	if m.AutoTiling && m.CurrentWorkspace == oldWorkspace {
		m.TileVisibleWorkspaceWindows()
		// Save the layout immediately to capture the correct window positions
		m.SaveCurrentLayout()
		// Mark as non-custom so it can be retiled later if needed
		m.WorkspaceHasCustom[m.CurrentWorkspace] = false
	}

	// Switch to the new workspace and focus the moved window
	m.SwitchToWorkspace(workspace)
	m.FocusWindow(windowIndex)

	// Retile new workspace if in tiling mode
	if m.AutoTiling {
		m.TileVisibleWorkspaceWindows()
		// Save the layout for the new workspace too
		m.SaveCurrentLayout()
		m.WorkspaceHasCustom[m.CurrentWorkspace] = false
	}
}

// FocusNextVisibleWindowInWorkspace focuses the next visible window in the workspace.
func (m *OS) FocusNextVisibleWindowInWorkspace() {
	// Find the next non-minimized window in current workspace to focus
	for i := range m.Windows {
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

// TileVisibleWorkspaceWindows tiles all visible windows in the current workspace with animations.
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

	// Create animations for smooth transitions (matching TileAllWindows behavior)
	for i, windowIndex := range visibleWindows {
		if i < len(layouts) {
			window := m.Windows[windowIndex]

			// Create animation for smooth transition
			anim := ui.NewSnapAnimation(
				window,
				layouts[i].x,
				layouts[i].y,
				layouts[i].width,
				layouts[i].height,
				config.GetAnimationDuration(),
			)

			if anim != nil {
				m.Animations = append(m.Animations, anim)
			} else {
				// Fallback if animation creation fails
				window.X = layouts[i].x
				window.Y = layouts[i].y
				window.Width = layouts[i].width
				window.Height = layouts[i].height
				window.PositionDirty = true
			}
		}
	}
}
