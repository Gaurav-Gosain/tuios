package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Tiling constants
const (
	// edgeTolerance is the pixel tolerance for detecting window edges at screen boundaries
	edgeTolerance = 2
	// swapTolerance is the pixel tolerance for detecting adjacent windows during swap operations
	swapTolerance = 5
)

// Direction represents a cardinal direction for window operations
type Direction int

const (
	DirLeft Direction = iota
	DirRight
	DirUp
	DirDown
)

// tileLayout is a private type for compatibility with existing code
type tileLayout struct {
	x, y, width, height int
}

// calculateTilingLayout is a wrapper around layout.CalculateTilingLayout for internal use
func (m *OS) calculateTilingLayout(n int) []tileLayout {
	layouts := layout.CalculateTilingLayout(n, m.GetRenderWidth(), m.GetUsableHeight(), m.GetTopMargin(), m.MasterRatio)
	result := make([]tileLayout, len(layouts))
	for i, l := range layouts {
		result[i] = tileLayout{
			x:      l.X,
			y:      l.Y,
			width:  l.Width,
			height: l.Height,
		}
	}
	return result
}

// TileAllWindows arranges all visible windows in a tiling layout
func (m *OS) TileAllWindows() {
	// Get list of visible windows in current workspace (not minimized)
	var visibleWindows []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing && !w.IsFloating {
			visibleWindows = append(visibleWindows, w)
		}
	}

	if len(visibleWindows) == 0 {
		return
	}

	m.LogInfo("TileAllWindows called with %d visible windows, BSP=%v, Scrolling=%v", len(visibleWindows), m.UseBSPLayout, m.UseScrollingLayout)

	// Scrolling layout mode (niri-like)
	if m.UseScrollingLayout {
		sl := m.GetOrCreateScrollingLayout()
		m.LogInfo("[SCROLL-TILE] TileAllWindows scrolling path, %d visible windows", len(visibleWindows))
		sl.EnsureFocusedVisible(m.GetRenderWidth())
		m.scrollingSetPositions()
		return
	}

	// Use master-stack layout if BSP is disabled
	if !m.UseBSPLayout {
		layouts := layout.CalculateTilingLayout(len(visibleWindows), m.GetRenderWidth(), m.GetUsableHeight(), m.GetTopMargin(), m.MasterRatio)
		for i, l := range layouts {
			if i < len(visibleWindows) {
				visibleWindows[i].X = l.X
				visibleWindows[i].Y = l.Y
				// Set Tiled before Resize so the border deduction (and therefore
				// the emulator size) matches the shared-borders state.
				visibleWindows[i].Tiled = config.SharedBorders
				visibleWindows[i].Resize(l.Width, l.Height)
				visibleWindows[i].InvalidateCache()
			}
		}
		return
	}

	// Try to use BSP tree if available
	tree := m.WorkspaceTrees[m.CurrentWorkspace]

	// Check if tree is valid and in sync with visible windows
	if tree != nil && !tree.IsEmpty() {
		// First, check if tree has any stale windows (windows not in visibleWindows)
		treeIDs := tree.GetAllWindowIDs()
		visibleIDs := make(map[int]bool)
		for _, win := range visibleWindows {
			intID := m.getWindowIntID(win.ID)
			visibleIDs[intID] = true
			if verboseLog {
				m.LogInfo("BSP: Visible window %s has int ID %d", win.ID[:8], intID)
			}
		}
		m.LogInfo("BSP: Tree has IDs: %v, visible IDs: %v", treeIDs, visibleIDs)

		hasStaleWindows := false
		for _, id := range treeIDs {
			if !visibleIDs[id] {
				hasStaleWindows = true
				m.LogInfo("BSP: Tree has stale window ID %d, will rebuild", id)
				break
			}
		}

		// If tree has stale windows, clear it and rebuild
		if hasStaleWindows {
			m.LogInfo("BSP: Clearing stale tree and rebuilding")
			m.WorkspaceTrees[m.CurrentWorkspace] = nil
			tree = nil
		}
	}

	// If no tree or tree was cleared, create fresh one
	if tree == nil || tree.IsEmpty() {
		m.LogInfo("BSP: Creating fresh tree for %d windows", len(visibleWindows))
		tree = m.GetOrCreateBSPTree()

		bounds := m.GetBSPBounds()
		var lastInsertedID = 0

		for i, win := range visibleWindows {
			windowIntID := m.getWindowIntID(win.ID)
			tree.InsertWindow(windowIntID, lastInsertedID, layout.SplitNone, 0.5, bounds)
			lastInsertedID = windowIntID
			m.LogInfo("BSP: Added window %d (int ID %d) with target %d", i+1, windowIntID, lastInsertedID)
		}

		m.ApplyBSPLayout()
		return
	}

	// Tree exists and is valid - check if all visible windows are in it
	allInTree := true
	for _, win := range visibleWindows {
		windowIntID := m.getWindowIntID(win.ID)
		if !tree.HasWindow(windowIntID) {
			allInTree = false
			break
		}
	}

	if allInTree {
		m.ApplyBSPLayout()
		return
	}

	// Some windows missing from tree - add them individually
	m.LogInfo("BSP: Adding missing windows to existing tree")

	for _, win := range visibleWindows {
		windowIntID := m.getWindowIntID(win.ID)
		if !tree.HasWindow(windowIntID) {
			existingIDs := tree.GetAllWindowIDs()
			targetIntID := 0
			if len(existingIDs) > 0 {
				targetIntID = existingIDs[len(existingIDs)-1]
			}

			bounds := m.GetBSPBounds()
			tree.InsertWindow(windowIntID, targetIntID, layout.SplitNone, 0.5, bounds)
			m.LogInfo("BSP: Added missing window (int ID %d) with target %d", windowIntID, targetIntID)
		}
	}
	m.ApplyBSPLayout()
}

// ToggleAutoTiling toggles automatic tiling mode
func (m *OS) ToggleAutoTiling() {
	m.AutoTiling = !m.AutoTiling

	if m.AutoTiling {
		// If scrolling mode was active, re-enable it
		if m.UseScrollingLayout {
			m.LogInfo("Scrolling: Re-enabling scrolling tiling mode")
			// Clear old scrolling layout to rebuild from current windows
			delete(m.WorkspaceScrollingLayouts, m.CurrentWorkspace)
			sl := m.GetOrCreateScrollingLayout()
			sl.EnsureFocusedVisible(m.GetRenderWidth())
			m.scrollingSetPositions()
			for _, w := range m.Windows {
				if w.Workspace == m.CurrentWorkspace {
					w.InvalidateCache()
				}
			}
			return
		}

		m.LogInfo("BSP: Enabling tiling mode")

		// Initialize the workspace trees map if needed
		if m.WorkspaceTrees == nil {
			m.WorkspaceTrees = make(map[int]*layout.BSPTree)
		}

		// When enabling, create a fresh BSP tree and add all visible windows
		m.WorkspaceTrees[m.CurrentWorkspace] = nil
		tree := m.GetOrCreateBSPTree()

		var visibleWindows []*terminal.Window
		for _, w := range m.Windows {
			if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing && !w.IsFloating {
				visibleWindows = append(visibleWindows, w)
			}
		}

		bounds := m.GetBSPBounds()
		var lastInsertedID = 0

		for i, win := range visibleWindows {
			windowIntID := m.getWindowIntID(win.ID)
			tree.InsertWindow(windowIntID, lastInsertedID, layout.SplitNone, 0.5, bounds)
			lastInsertedID = windowIntID
			m.LogInfo("BSP: Added window %d (int ID %d) with target %d, split count now: %d",
				i+1, windowIntID, lastInsertedID, tree.WindowCount())
		}

		m.ApplyBSPLayout()
		for _, win := range visibleWindows {
			win.InvalidateCache()
		}
		m.LogInfo("BSP: Tiling enabled with %d windows", len(visibleWindows))
	} else {
		m.LogInfo("BSP: Disabling tiling mode")
		// Clear preselection when disabling tiling
		m.PreselectionDir = layout.PreselectionNone
		// Reset Tiled flag and resize PTY to account for borders reappearing
		for i := range m.Windows {
			m.Windows[i].Tiled = false
			m.Windows[i].CachedContent = ""
			m.Windows[i].CachedLayer = nil
			m.Windows[i].ContentDirty = true
			m.Windows[i].Dirty = true
			m.Windows[i].PositionDirty = true
			m.Windows[i].HasNewOutput.Store(true)
			// Resize PTY: now uses border deduction (Tiled=false → width-2)
			m.Windows[i].Resize(m.Windows[i].Width, m.Windows[i].Height)
		}
		m.MarkAllDirty()
	}

	// Sync state to daemon so tiling mode persists across reconnects
	m.SyncStateToDaemon()
}

// TileNewWindow arranges the new window in the tiling layout
func (m *OS) TileNewWindow() {
	if !m.AutoTiling {
		return
	}

	// Retile all windows including the new one
	m.TileAllWindows()
}

// RetileAfterClose handles window close in tiling mode
func (m *OS) RetileAfterClose() {
	if !m.AutoTiling {
		return
	}

	// Retile remaining windows
	m.TileAllWindows()
}

// SaveCurrentLayout saves the current window layout for the active workspace
func (m *OS) SaveCurrentLayout() {
	if !m.AutoTiling {
		return
	}

	layouts := make([]WindowLayout, 0, len(m.Windows))
	for _, win := range m.Windows {
		if win.Workspace == m.CurrentWorkspace && !win.Minimized {
			layouts = append(layouts, WindowLayout{
				WindowID: win.ID,
				X:        win.X,
				Y:        win.Y,
				Width:    win.Width,
				Height:   win.Height,
			})
		}
	}

	m.WorkspaceLayouts[m.CurrentWorkspace] = layouts
	m.WorkspaceMasterRatio[m.CurrentWorkspace] = m.MasterRatio
}

// RestoreWorkspaceLayout restores saved layout when switching to a workspace
func (m *OS) RestoreWorkspaceLayout(workspace int) {
	if !m.AutoTiling {
		return
	}

	// Restore master ratio for this workspace (or use default)
	if ratio, exists := m.WorkspaceMasterRatio[workspace]; exists {
		m.MasterRatio = ratio
	} else {
		m.MasterRatio = 0.5 // Default
	}

	// Check if we have a saved layout for this workspace
	savedLayouts, hasCustom := m.WorkspaceLayouts[workspace]
	if !hasCustom || len(savedLayouts) == 0 {
		// No custom layout - use default tiling
		m.WorkspaceHasCustom[workspace] = false
		return
	}

	// Apply saved layout
	for _, saved := range savedLayouts {
		// Find window by ID
		for _, win := range m.Windows {
			if win.ID == saved.WindowID && win.Workspace == workspace {
				// Restore saved position/size
				win.X = saved.X
				win.Y = saved.Y
				win.Width = saved.Width
				win.Height = saved.Height
				win.Resize(win.Width, win.Height)
				win.MarkPositionDirty()
				break
			}
		}
	}

	m.WorkspaceHasCustom[workspace] = true
}

// MarkLayoutCustom marks the current workspace as having a custom layout
func (m *OS) MarkLayoutCustom() {
	if m.AutoTiling {
		m.WorkspaceHasCustom[m.CurrentWorkspace] = true
		m.SaveCurrentLayout()
	}
}
