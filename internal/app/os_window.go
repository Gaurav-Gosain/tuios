package app

import (
	"fmt"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// ToggleFloating toggles the focused window between floating and tiled mode.
func (m *OS) ToggleFloating() {
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}

	fw.IsFloating = !fw.IsFloating

	if fw.IsFloating {
		// Remove from BSP tree when floating
		if m.AutoTiling {
			m.RemoveWindowFromBSPTree(fw)
		}
		fw.Tiled = false
		fw.InvalidateCache()
		m.RecalcZOrder()
		m.ShowNotification("Window: floating", "info", config.NotificationDuration)
	} else {
		// Re-add to tiling layout when unfloating
		if m.AutoTiling {
			if m.UseScrollingLayout {
				intID := m.getWindowIntID(fw.ID)
				sl := m.GetOrCreateScrollingLayout()
				if !sl.HasWindow(intID) {
					sl.AddColumn(intID)
				}
				m.TileAllWindows()
			} else {
				m.AddWindowToBSPTree(fw)
			}
		}
		fw.InvalidateCache()
		m.RecalcZOrder()
		m.ShowNotification("Window: tiled", "info", config.NotificationDuration)
	}
}

// setupClipboardPassthrough wires a window's OSC 52 clipboard to bubbletea.
func (m *OS) setupClipboardPassthrough(window *terminal.Window) {
	if window == nil {
		return
	}
	window.ClipboardSetFunc = func(text string) {
		if m.PendingClipboardSet != nil {
			select {
			case m.PendingClipboardSet <- text:
			default:
				// Channel full, drop (non-blocking)
			}
		}
	}
}

// ToggleMultifocus toggles a window in/out of the multifocus set.
// When multiple windows are in the set, keystrokes are sent to all of them.
func (m *OS) ToggleMultifocus(windowIndex int) {
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		return
	}
	windowID := m.Windows[windowIndex].ID
	if m.MultifocusSet == nil {
		m.MultifocusSet = make(map[string]bool)
	}
	if m.MultifocusSet[windowID] {
		delete(m.MultifocusSet, windowID)
		if len(m.MultifocusSet) == 0 {
			m.MultifocusSet = nil
		}
		m.ShowNotification("Multifocus: removed window", "info", config.NotificationDuration)
	} else {
		m.MultifocusSet[windowID] = true
		m.ShowNotification(fmt.Sprintf("Multifocus: %d windows", len(m.MultifocusSet)), "info", config.NotificationDuration)
	}
	// Invalidate caches to show visual indicator on all affected windows
	m.Windows[windowIndex].InvalidateCache()
	for _, w := range m.Windows {
		if m.MultifocusSet[w.ID] {
			w.InvalidateCache()
		}
	}
}

// ClearMultifocus removes all windows from the multifocus set.
func (m *OS) ClearMultifocus() {
	if m.MultifocusSet != nil {
		for _, w := range m.Windows {
			if m.MultifocusSet[w.ID] {
				w.InvalidateCache()
			}
		}
	}
	m.MultifocusSet = nil
	m.ShowNotification("Multifocus: cleared", "info", 0)
}

// IsMultifocused returns true if the window at the given index is in the multifocus set.
func (m *OS) IsMultifocused(windowIndex int) bool {
	if m.MultifocusSet == nil || windowIndex < 0 || windowIndex >= len(m.Windows) {
		return false
	}
	return m.MultifocusSet[m.Windows[windowIndex].ID]
}

// GetMultifocusWindows returns the current slice indices of all windows in the multifocus set.
func (m *OS) GetMultifocusWindows() []int {
	if m.MultifocusSet == nil {
		return nil
	}
	var indices []int
	for i, w := range m.Windows {
		if m.MultifocusSet[w.ID] {
			indices = append(indices, i)
		}
	}
	return indices
}

// CycleToNextVisibleWindow cycles focus to the next visible window in the current workspace.
func (m *OS) CycleToNextVisibleWindow() {
	if len(m.Windows) == 0 {
		return
	}
	// Find next visible (non-minimized and non-minimizing) window in current workspace
	visibleWindows := []int{}
	for i, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			visibleWindows = append(visibleWindows, i)
		}
	}
	if len(visibleWindows) == 0 {
		return
	}

	// Find current position in visible windows
	currentPos := -1
	for i, idx := range visibleWindows {
		if idx == m.FocusedWindow {
			currentPos = i
			break
		}
	}

	// Cycle to next visible window
	if currentPos >= 0 && currentPos < len(visibleWindows)-1 {
		m.FocusWindow(visibleWindows[currentPos+1])
	} else {
		m.FocusWindow(visibleWindows[0])
	}
}

// CycleToPreviousVisibleWindow cycles focus to the previous visible window in the current workspace.
func (m *OS) CycleToPreviousVisibleWindow() {
	if len(m.Windows) == 0 {
		return
	}
	// Find previous visible (non-minimized and non-minimizing) window in current workspace
	visibleWindows := []int{}
	for i, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			visibleWindows = append(visibleWindows, i)
		}
	}
	if len(visibleWindows) == 0 {
		return
	}

	// Find current position in visible windows
	currentPos := -1
	for i, idx := range visibleWindows {
		if idx == m.FocusedWindow {
			currentPos = i
			break
		}
	}

	// Cycle to previous visible window
	if currentPos > 0 {
		m.FocusWindow(visibleWindows[currentPos-1])
	} else {
		m.FocusWindow(visibleWindows[len(visibleWindows)-1])
	}
}

// FocusWindow sets focus to the window at the specified index.
func (m *OS) FocusWindow(i int) *OS {
	// Simple bounds check
	if len(m.Windows) == 0 || i < 0 || i >= len(m.Windows) {
		return m
	}

	// Don't do anything if already focused
	if m.FocusedWindow == i {
		return m
	}

	oldFocused := m.FocusedWindow

	// ATOMIC: Set focus and Z-index in one operation
	m.FocusedWindow = i

	// Save focus for current workspace
	if m.Windows[i].Workspace == m.CurrentWorkspace {
		m.WorkspaceFocus[m.CurrentWorkspace] = i
	}

	// Recalculate Z-ordering (floating always above non-floating)
	m.RecalcZOrder()

	// Always invalidate caches for immediate visual feedback on focus change
	// The Z-index change needs to be visible immediately when user clicks
	if oldFocused >= 0 && oldFocused < len(m.Windows) {
		m.Windows[oldFocused].MarkPositionDirty() // Use lighter invalidation
	}

	// Invalidate cache for new focused window (border color change + fresh content)
	m.Windows[i].InvalidateCache() // Full invalidation to show latest content

	m.FireHook(hooks.AfterFocusChange, m.Windows[i].ID, m.Windows[i].Title())

	// Sync scrolling layout focus and scroll into view when focus changes
	// via click or external means (not from scrollingSyncFocusToOS).
	if m.AutoTiling && m.UseScrollingLayout && !m.scrollingFocusSyncing {
		m.LogInfo("[SCROLL-FOCUS] FocusWindow(%d) -> triggering ScrollingOnFocusChange (old=%d)", i, oldFocused)
		m.ScrollingOnFocusChange()
	}

	return m
}

// RecalcZOrder recalculates Z-index values for all windows, ensuring floating
// windows are always above non-floating windows. Call after toggling IsFloating.
func (m *OS) RecalcZOrder() {
	focused := m.FocusedWindow
	z := 0
	// Non-floating, non-focused first
	for j := range m.Windows {
		if j != focused && !m.Windows[j].IsFloating {
			m.Windows[j].Z = z
			z++
		}
	}
	// Focused non-floating
	if focused >= 0 && focused < len(m.Windows) && !m.Windows[focused].IsFloating {
		m.Windows[focused].Z = z
		z++
	}
	// Non-focused floating
	for j := range m.Windows {
		if j != focused && m.Windows[j].IsFloating {
			m.Windows[j].Z = z
			z++
		}
	}
	// Focused floating (very top)
	if focused >= 0 && focused < len(m.Windows) && m.Windows[focused].IsFloating {
		m.Windows[focused].Z = z
	}
	m.MarkAllDirty()
}

// QuitSession performs a deliberate, user-initiated quit. In a daemon session
// that also kills the session, so it records the intent first: the daemon
// announces the session ending and the connection dropping, and either can
// arrive before the program finishes quitting. Update consults QuitRequested so
// those announcements are not mistaken for a session killed from elsewhere,
// which would make a normal exit report an error.
//
// Every deliberate quit path routes through here so they cannot drift apart.
func (m *OS) QuitSession() {
	m.QuitRequested = true
	if m.IsDaemonSession && m.DaemonClient != nil {
		_ = m.DaemonClient.KillSession()
	}
	m.Cleanup()
}

// AddWindow adds a new window to the current workspace.
// In daemon mode, this creates a daemon-managed PTY and window.
func (m *OS) AddWindow(title string) *OS {
	// In daemon mode, use daemon PTY management
	if m.IsDaemonSession && m.DaemonClient != nil {
		return m.AddDaemonWindow(title)
	}

	newID := createID()
	if title == "" {
		title = fmt.Sprintf("Terminal %s", newID[:8])
	}

	m.LogInfo("Creating new window: %s (workspace %d)", title, m.CurrentWorkspace)

	// Handle case where screen dimensions aren't available yet
	screenWidth := m.GetRenderWidth()
	screenHeight := m.GetUsableHeight()

	if screenWidth == 0 || screenHeight == 0 {
		// Use sensible defaults when screen size is unknown
		screenWidth = 80
		screenHeight = 24
		m.LogWarn("Screen dimensions unknown, using defaults (%dx%d)", screenWidth, screenHeight)
	}

	width := screenWidth / 2
	height := screenHeight / 2

	// In floating mode, spawn at cursor position
	// In tiling mode, position doesn't matter as it will be auto-tiled
	var x, y int
	if !m.AutoTiling && m.LastMouseX > 0 && m.LastMouseY > 0 {
		// Spawn at cursor position, but ensure window stays on screen
		x = m.LastMouseX
		y = m.LastMouseY

		// Adjust if window would go off screen
		if x+width > screenWidth {
			x = screenWidth - width
		}
		if y+height > screenHeight {
			y = screenHeight - height
		}
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
	} else {
		// Center the window (default behavior for tiling mode or no cursor position)
		x = screenWidth / 4
		y = screenHeight / 4
	}

	window := terminal.NewWindow(newID, title, x, y, width, height, len(m.Windows), m.WindowExitChan, m.PTYDataChan)
	if window == nil {
		m.LogError("Failed to create window %s (PTY creation failed)", title)
		return m // Failed to create window
	}

	caps := GetHostCapabilities()
	if caps.CellWidth > 0 && caps.CellHeight > 0 {
		window.SetCellPixelDimensions(caps.CellWidth, caps.CellHeight)
	}

	window.Workspace = m.CurrentWorkspace

	m.setupKittyPassthrough(window)
	m.setupSixelPassthrough(window)
	m.setupTextSizingPassthrough(window)
	m.setupClipboardPassthrough(window)
	m.setupNotificationPassthrough(window)

	m.Windows = append(m.Windows, window)
	m.LogInfo("Window created successfully: %s (ID: %s, total windows: %d)", title, newID[:8], len(m.Windows))
	m.FireHook(hooks.AfterNewWindow, newID, title)

	// In scrolling mode, add to layout BEFORE focusing so that
	// ScrollingOnFocusChange can find the window's column.
	if m.AutoTiling && m.UseScrollingLayout {
		m.ScrollingOnWindowAdded(window)
	}

	// Focus the new window, which will bring it to the front
	m.FocusWindow(len(m.Windows) - 1)

	// Auto-tile if in tiling mode
	if m.AutoTiling {
		if m.UseScrollingLayout {
			m.TileAllWindows()
		} else {
			tree := m.GetOrCreateBSPTree()
			if tree != nil {
				m.AddWindowToBSPTree(window)
			} else {
				m.TileAllWindows()
			}
		}
	}

	return m
}

// UpdateAllWindowThemes updates the terminal colors for all windows when the theme changes
func (m *OS) UpdateAllWindowThemes() {
	m.LogInfo("Updating terminal colors for all windows after theme change")
	for _, window := range m.Windows {
		if window != nil {
			window.UpdateThemeColors()
		}
	}
}

// DeleteWindow removes the window at the specified index.
// In daemon mode, this also cleans up the daemon-managed PTY.
func (m *OS) DeleteWindow(i int) *OS {
	if len(m.Windows) == 0 || i < 0 || i >= len(m.Windows) {
		m.LogWarn("Cannot delete window: invalid index %d (total windows: %d)", i, len(m.Windows))
		return m
	}

	// Clean up window resources
	deletedWindow := m.Windows[i]
	m.LogInfo("Deleting window: %s (index: %d, ID: %s)", deletedWindow.Title(), i, deletedWindow.ID[:8])

	// In daemon mode, clean up daemon-managed PTY
	if deletedWindow.DaemonMode && deletedWindow.PTYID != "" && m.DaemonClient != nil {
		m.DaemonClient.UnsubscribePTY(deletedWindow.PTYID)
		if err := m.DaemonClient.ClosePTY(deletedWindow.PTYID); err != nil {
			m.LogError("Failed to close daemon PTY: %v", err)
		}
	}

	// Get the window int ID BEFORE deleting (for BSP tree removal)
	windowIntID := m.getWindowIntID(deletedWindow.ID)

	// Clean up the BSP ID mapping
	if m.WindowToBSPID != nil {
		delete(m.WindowToBSPID, deletedWindow.ID)
		if m.BSPIDToWindowID != nil {
			delete(m.BSPIDToWindowID, windowIntID)
		}
		m.LogInfo("BSP: Removed ID mapping for window %s (int ID %d)", deletedWindow.ID[:8], windowIntID)
	}

	if m.KittyPassthrough != nil {
		m.KittyPassthrough.OnWindowClose(deletedWindow.ID)
		if data := m.KittyPassthrough.FlushPending(); len(data) > 0 {
			m.KittyPassthrough.WriteToHost(data)
		}
	}

	// MultifocusSet is keyed by window ID, so removal is a plain delete.
	if len(m.MultifocusSet) > 0 {
		delete(m.MultifocusSet, deletedWindow.ID)
		if len(m.MultifocusSet) == 0 {
			m.MultifocusSet = nil
		}
	}

	deletedWindow.Close()

	// Remove any animations referencing this window to prevent memory leaks
	cleanedAnimations := make([]*ui.Animation, 0, len(m.Animations))
	animsCleaned := 0
	for _, anim := range m.Animations {
		if anim.Window != deletedWindow {
			cleanedAnimations = append(cleanedAnimations, anim)
		} else {
			animsCleaned++
		}
	}
	m.Animations = cleanedAnimations
	if animsCleaned > 0 {
		m.LogInfo("Cleaned up %d animations for deleted window", animsCleaned)
	}

	movedZ := deletedWindow.Z
	for j := range m.Windows {
		if m.Windows[j].Z > movedZ {
			m.Windows[j].Z--
			// Invalidate cache for windows whose Z changed
			m.Windows[j].InvalidateCache()
		}
	}

	m.Windows = slices.Delete(m.Windows, i, i+1)

	// Explicitly clear the deleted window pointer to help GC
	deletedWindow = nil

	m.LogInfo("Window deleted successfully (remaining windows: %d)", len(m.Windows))

	// Update focused window index
	if len(m.Windows) == 0 {
		m.FocusedWindow = -1
		m.LogInfo("No windows remaining, switching to window management mode")
		// Reset to window management mode when no windows are left
		m.Mode = WindowManagementMode
	} else if i < m.FocusedWindow {
		m.FocusedWindow--
	} else if i == m.FocusedWindow {
		// If we deleted the focused window, find the next visible window to focus
		m.FocusNextVisibleWindow()
	}

	// Retile if in tiling mode
	if m.AutoTiling {
		if m.UseScrollingLayout {
			// Scrolling mode: only touch the scrolling layout
			if windowIntID > 0 {
				m.ScrollingOnWindowRemoved(windowIntID)
			}
		} else {
			// BSP/master-stack mode
			tree := m.WorkspaceTrees[m.CurrentWorkspace]
			if tree != nil && windowIntID > 0 {
				tree.RemoveWindow(windowIntID)
				m.LogInfo("BSP: Removed window from tree, tree now has %d windows", tree.WindowCount())

				if tree.IsEmpty() {
					m.LogInfo("BSP: Tree is now empty, clearing workspace tree")
					m.WorkspaceTrees[m.CurrentWorkspace] = nil
				} else if len(m.Windows) > 0 {
					m.ApplyBSPLayout()
				}
			}

			// If there are still visible windows in this workspace, retile them
			if len(m.Windows) > 0 {
				hasVisibleInWorkspace := false
				for _, w := range m.Windows {
					if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
						hasVisibleInWorkspace = true
						break
					}
				}
				if hasVisibleInWorkspace && (tree == nil || tree.IsEmpty()) {
					m.TileAllWindows()
				}
			}
		}
	}

	// Sync state to daemon after window deletion
	m.SyncStateToDaemon()

	return m
}

// GetFocusedWindow returns the currently focused window.
func (m *OS) GetFocusedWindow() *terminal.Window {
	if len(m.Windows) > 0 && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		// Only return the focused window if it's in the current workspace
		if m.Windows[m.FocusedWindow].Workspace == m.CurrentWorkspace {
			return m.Windows[m.FocusedWindow]
		}
	}
	return nil
}
