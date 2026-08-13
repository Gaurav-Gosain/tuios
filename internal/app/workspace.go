package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// Workspace management methods

// SwitchToWorkspace switches to the specified workspace, picking a default focus
// (the workspace's saved window, else its first visible one).
func (m *OS) SwitchToWorkspace(workspace int) {
	m.switchToWorkspace(workspace, -1)
}

// switchToWorkspace switches to the workspace and resolves focus. A focusTarget
// of -1 keeps the default pick; a valid index focuses that window and skips the
// default, so a cross-workspace FocusWindow lands its target without first
// firing the focus hooks for an intermediate window.
func (m *OS) switchToWorkspace(workspace, focusTarget int) {
	m.settleSizes(func() { m.switchToWorkspaceHeld(workspace, focusTarget) })
}

// switchToWorkspaceHeld is switchToWorkspace with the announcements already held.
func (m *OS) switchToWorkspaceHeld(workspace, focusTarget int) {
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

	// Clear all animations BEFORE switching to prevent windows from getting stuck
	// mid-animation. A pane left at an interpolated rectangle keeps it until
	// something retiles the workspace, which may be never.
	//
	// Land each one where it was already heading, through the normal path. The
	// old code recomputed a master-stack layout instead - the wrong rectangles
	// under BSP or scrolling - and stamped Width and Height straight onto the
	// window, so the emulator and the guest kept the size the pane had before the
	// switch and only heard the real one on some later, unrelated action.
	if len(m.Animations) > 0 {
		for _, anim := range m.Animations {
			if anim.Type != ui.AnimationSnap || anim.Window == nil {
				continue
			}
			win := anim.Window
			win.X, win.Y = anim.EndX, anim.EndY
			win.Resize(anim.EndWidth, anim.EndHeight)
			win.InvalidateCache()
			win.MarkPositionDirty()
		}
		m.Animations = m.Animations[:0]
		m.LogInfo("Landed and cancelled animations during workspace switch")
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

	// A caller-supplied target wins: focus exactly it, so the switch does not fire
	// the focus hooks for a default window the caller is about to override.
	focusedSet := false
	if focusTarget >= 0 && focusTarget < len(m.Windows) && m.Windows[focusTarget].Workspace == workspace {
		m.FocusWindow(focusTarget)
		focusedSet = true
	}

	// Try to restore previous focus for this workspace
	if !focusedSet {
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
	} else {
		m.settleBorderMode(workspace)
	}

	// Mark all windows in new workspace as dirty for immediate render
	for _, w := range m.Windows {
		if w.Workspace == workspace {
			w.MarkPositionDirty()
		}
	}

	// Sync state to daemon after workspace switch
	m.SyncStateToDaemon()

	// Fire after the switch has fully landed (focus resolved, layout applied),
	// so a hook that inspects the session sees the workspace it was told about.
	// The newly focused window is reported alongside the workspace pair.
	focusedID, focusedName := "", ""
	if w := m.GetFocusedWindow(); w != nil {
		focusedID, focusedName = w.ID, w.Title()
	}
	m.FireHookContext(hooks.AfterWorkspaceSwitch, hooks.Context{
		WindowID:          focusedID,
		WindowName:        focusedName,
		Workspace:         workspace,
		PreviousWorkspace: oldWorkspace,
	})
}

// settleBorderMode gives a workspace's panes the border mode its layout already
// implies, moving nothing.
//
// Border mode is not geometry. Disabling tiling clears Tiled on every window in
// the session, while enabling it again only tiles the workspace that happens to
// be current, so every other workspace is left claiming a border of its own
// inside rectangles whose separator gaps are still reserved. Switching back
// normally settles that as a side effect of the retile, but a workspace holding
// a custom layout skips the retile to keep its rectangles, and then the panes
// draw boxes while the overlay fills the gaps between them: two border systems
// in one frame, with the divider stuck on the unfocused color because a pane
// that is not Tiled contributes no focus perimeter.
//
// Settling here, as the workspace becomes visible, is what a flag written by a
// retile that may never run cannot do.
func (m *OS) settleBorderMode(workspace int) {
	if !m.AutoTiling || m.UseScrollingLayout {
		return
	}
	// Without a tree there is no tiled layout to match, and borderless panes
	// would leave the frame with no pane edges at all.
	if m.UseBSPLayout {
		if tree := m.WorkspaceTrees[workspace]; tree == nil || tree.IsEmpty() {
			return
		}
	}
	for _, w := range m.Windows {
		if w.Workspace != workspace || w.Minimized || w.Minimizing || w.IsFloating {
			continue
		}
		w.SetTiled(m.panesBorderless())
	}
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

	m.LogInfo("Moving window %s: workspace %d → %d", window.Title(), oldWorkspace, workspace)

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
	// BSP and scrolling layouts carry their own geometry (split ratios, column
	// offsets) that the master-stack tiler below would silently overwrite,
	// desyncing the separator overlay and eventually tripping the stale-ID check
	// in TileAllWindows (which then discards the whole tree). Defer to
	// TileAllWindows, which branches correctly per layout mode and filters
	// floating windows.
	if m.UseBSPLayout || m.UseScrollingLayout {
		m.TileAllWindows()
		return
	}

	// Master-stack path: animate visible, non-floating windows into place.
	visibleWindows := make([]int, 0)
	for i, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing && !w.IsFloating {
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
