package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// ============================================================================
// BSP (Binary Space Partitioning) Tiling Functions
// ============================================================================

// GetOrCreateBSPTree returns the BSP tree for the current workspace, creating it if needed
func (m *OS) GetOrCreateBSPTree() *layout.BSPTree {
	if m.WorkspaceTrees == nil {
		m.WorkspaceTrees = make(map[int]*layout.BSPTree)
	}

	tree, exists := m.WorkspaceTrees[m.CurrentWorkspace]
	if !exists || tree == nil {
		tree = layout.NewBSPTree()
		// Use SchemeSpiral as default if TilingScheme not set
		if m.TilingScheme == layout.SchemeLongestSide {
			// SchemeLongestSide is the zero value, which means it wasn't explicitly set
			// Default to SchemeSpiral for balanced alternating splits
			tree.AutoScheme = layout.SchemeSpiral
		} else {
			tree.AutoScheme = m.TilingScheme
		}
		m.WorkspaceTrees[m.CurrentWorkspace] = tree
		m.LogInfo("BSP: Created new tree for workspace %d with scheme %s", m.CurrentWorkspace, tree.AutoScheme.String())
	}

	return tree
}

// GetBSPBounds returns the bounds for BSP layout calculation
func (m *OS) GetBSPBounds() layout.Rect {
	return layout.Rect{
		X: 0,
		Y: m.GetTopMargin(),
		W: m.GetRenderWidth(),
		H: m.GetUsableHeight(),
	}
}

// getWindowIntID returns a stable integer ID for a window string ID.
// Uses a direct map lookup for reliable ID assignment.
func (m *OS) getWindowIntID(stringID string) int {
	if stringID == "" {
		return 0
	}

	// Initialize the map if needed
	if m.WindowToBSPID == nil {
		m.WindowToBSPID = make(map[string]int)
	}

	// Check if we already have an ID for this window
	if id, exists := m.WindowToBSPID[stringID]; exists {
		return id
	}

	// Assign a new ID
	if m.NextBSPWindowID == 0 {
		m.NextBSPWindowID = 1 // Start from 1, 0 is reserved for "no window"
	}
	newID := m.NextBSPWindowID
	m.NextBSPWindowID++
	m.WindowToBSPID[stringID] = newID
	if m.BSPIDToWindowID == nil {
		m.BSPIDToWindowID = make(map[int]string)
	}
	m.BSPIDToWindowID[newID] = stringID

	return newID
}

// getWindowByIntID returns the window for a given integer ID
func (m *OS) getWindowByIntID(intID int) *terminal.Window {
	if intID <= 0 {
		return nil
	}

	// Fast path: reverse-map lookup. Verify against the forward map so a stale
	// or missing reverse entry (e.g. after a session restore that rebuilt only
	// WindowToBSPID) can never return the wrong window.
	if stringID, ok := m.BSPIDToWindowID[intID]; ok {
		if id, exists := m.WindowToBSPID[stringID]; exists && id == intID {
			for _, w := range m.Windows {
				if w.ID == stringID {
					return w
				}
			}
		}
	}

	// Fallback: resolve via the forward map on a reverse-map miss or mismatch.
	for stringID, id := range m.WindowToBSPID {
		if id == intID {
			for _, w := range m.Windows {
				if w.ID == stringID {
					return w
				}
			}
			break
		}
	}
	return nil
}

// ApplyBSPLayout applies the BSP tree layout to all windows in the current workspace
func (m *OS) ApplyBSPLayout() {
	tree := m.GetOrCreateBSPTree()
	if tree == nil || tree.IsEmpty() {
		return
	}

	bounds := m.GetBSPBounds()
	layouts := tree.ApplyLayout(bounds)

	for windowIntID, rect := range layouts {
		win := m.getWindowByIntID(windowIntID)
		if win == nil || win.Workspace != m.CurrentWorkspace || win.Minimized || win.IsFloating {
			continue
		}

		wasTiled := win.Tiled

		// Cancel any existing snap animation for this window to prevent
		// animation pileup during continuous resize.
		for j := len(m.Animations) - 1; j >= 0; j-- {
			if m.Animations[j].Window == win && m.Animations[j].Type == ui.AnimationSnap {
				m.Animations = append(m.Animations[:j], m.Animations[j+1:]...)
			}
		}

		// Create animation for smooth transition
		anim := ui.NewSnapAnimation(
			win,
			rect.X, rect.Y, rect.W, rect.H,
			config.GetAnimationDuration(),
		)

		if anim != nil {
			if config.SharedBorders && !wasTiled {
				// New window entering tiled mode: keep Tiled=false during animation
				// so it renders with individual borders. TileOnComplete transitions
				// it to shared borders when animation finishes.
				anim.TileOnComplete = true
			} else {
				win.Tiled = config.SharedBorders
				if win.Tiled != wasTiled {
					win.InvalidateCache()
				}
			}
			m.Animations = append(m.Animations, anim)
		} else {
			// Already at target, or animations disabled (NewSnapAnimation applied
			// the size instantly). SetTiled re-syncs the emulator for the new
			// border deduction when the flag actually changes.
			win.SetTiled(config.SharedBorders)
		}
	}
}

// AddWindowToBSPTree adds a window to the BSP tree and applies the layout.
// This should be called when a new window is created in tiling mode.
func (m *OS) AddWindowToBSPTree(window *terminal.Window) {
	tree := m.GetOrCreateBSPTree()
	windowIntID := m.getWindowIntID(window.ID)

	if verboseLog {
		m.LogInfo("BSP: AddWindowToBSPTree for window %s (int ID %d)", window.ID[:8], windowIntID)
	}

	// Determine the target window for splitting
	targetIntID := 0

	// If SplitTargetWindowID is set (for explicit splits like Ctrl+B, -), use that
	if m.SplitTargetWindowID != "" {
		targetIntID = m.getWindowIntID(m.SplitTargetWindowID)
		m.LogInfo("BSP: Using explicit split target (int ID %d)", targetIntID)
	} else {
		// Use the last window in the BSP tree as the target
		// This ensures proper spiral pattern
		existingIDs := tree.GetAllWindowIDs()
		if len(existingIDs) > 0 {
			targetIntID = existingIDs[len(existingIDs)-1]
			m.LogInfo("BSP: Using last tree window as target (int ID %d)", targetIntID)
		}
	}

	bounds := m.GetBSPBounds()

	// Check for preselection
	if m.PreselectionDir != layout.PreselectionNone {
		m.LogInfo("BSP: Inserting with preselection %d", m.PreselectionDir)
		tree.InsertWindowWithPreselection(windowIntID, targetIntID, m.PreselectionDir, bounds)
		m.PreselectionDir = layout.PreselectionNone // Clear preselection
	} else {
		tree.InsertWindow(windowIntID, targetIntID, layout.SplitNone, 0.5, bounds)
	}

	m.LogInfo("BSP: Tree now has %d windows", tree.WindowCount())

	// Position the new window at screen center so it animates from
	// center to its tiled position with visible borders.
	window.X = bounds.X + bounds.W/2 - window.Width/2
	window.Y = bounds.Y + bounds.H/2 - window.Height/2

	// Apply the new layout
	m.ApplyBSPLayout()
}

// RemoveWindowFromBSPTree removes a window from the BSP tree and reapplies the layout.
// This should be called when a window is closed in tiling mode.
func (m *OS) RemoveWindowFromBSPTree(window *terminal.Window) {
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return
	}

	windowIntID := m.getWindowIntID(window.ID)
	tree.RemoveWindow(windowIntID)

	// Apply the new layout
	m.ApplyBSPLayout()
}

// MarkBSPSyncPending records that window geometry moved during a drag and the
// BSP tree's ratios no longer match it. The sync itself is deferred to the next
// composed frame by FlushPendingBSPSync, because its only job during a drag is
// to keep the separator overlay under the pointer, and a frame that is never
// drawn cannot show a stale separator.
//
// The deferral is only safe because it is bounded on both ends: the interaction
// tick composes a frame for as long as a drag is live, so a pending sync is
// never held for more than one frame interval, and mouse release calls
// SyncBSPTreeFromGeometry unconditionally. That last one cannot be skipped -
// the tree ratios, not the window rectangles, are what survives a retile, so a
// drag that ended without a final sync would have its result discarded the next
// time the layout was applied.
func (m *OS) MarkBSPSyncPending() {
	m.pendingBSPSync = true
}

// FlushPendingBSPSync runs a deferred ratio sync if one is outstanding. Its one
// caller is View, immediately before it composes, and that is the only correct
// place for it: the overlay mixes tree ratios with live window geometry, so the
// sync has to happen on every frame that is composed rather than on the paths
// that change geometry. Frames arrive from elsewhere too, PTY output most of
// all, and one composed between a motion event and its sync draws the divider
// where the drag has already left.
func (m *OS) FlushPendingBSPSync() {
	if m.pendingBSPSync {
		m.SyncBSPTreeFromGeometry()
	}
}

// SyncBSPTreeFromGeometry updates the BSP tree's split ratios to match current window positions.
// This should be called after mouse resize operations complete.
func (m *OS) SyncBSPTreeFromGeometry() {
	m.pendingBSPSync = false

	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		return
	}

	// Build geometry map from current window positions
	geometry := make(map[int]layout.Rect)
	for _, win := range m.Windows {
		if win.Workspace == m.CurrentWorkspace && !win.Minimized && !win.Minimizing {
			windowIntID := m.getWindowIntID(win.ID)
			geometry[windowIntID] = layout.Rect{
				X: win.X,
				Y: win.Y,
				W: win.Width,
				H: win.Height,
			}
		}
	}

	bounds := m.GetBSPBounds()
	tree.SyncRatiosFromGeometry(geometry, bounds)

	// In shared borders mode, re-apply layout after sync to enforce separator gaps
	if config.SharedBorders {
		m.ApplyBSPLayout()
	}
}

// SplitFocusedHorizontal splits the focused window horizontally (top/bottom) and creates a new terminal
func (m *OS) SplitFocusedHorizontal() {
	if !m.AutoTiling {
		return
	}

	focusedWin := m.GetFocusedWindow()
	if focusedWin == nil {
		return
	}

	// Store the target window ID BEFORE creating new window (which will change focus)
	m.SplitTargetWindowID = focusedWin.ID

	// Set preselection direction for the next window
	m.PreselectionDir = layout.PreselectionDown

	// Create a new window - it will be added with the preselection
	m.AddWindow("")

	// Clear the split target
	m.SplitTargetWindowID = ""
}

// SplitFocusedVertical splits the focused window vertically (left/right) and creates a new terminal
func (m *OS) SplitFocusedVertical() {
	if !m.AutoTiling {
		return
	}

	focusedWin := m.GetFocusedWindow()
	if focusedWin == nil {
		return
	}

	// Store the target window ID BEFORE creating new window (which will change focus)
	m.SplitTargetWindowID = focusedWin.ID

	// Set preselection direction for the next window
	m.PreselectionDir = layout.PreselectionRight

	// Create a new window - it will be added with the preselection
	m.AddWindow("")

	// Clear the split target
	m.SplitTargetWindowID = ""
}

// SmartSplitFocused splits the focused window using the smart split algorithm:
// it chooses horizontal or vertical based on the focused window's aspect ratio.
func (m *OS) SmartSplitFocused() {
	if !m.AutoTiling {
		return
	}

	focusedWin := m.GetFocusedWindow()
	if focusedWin == nil {
		return
	}

	// Store the target window ID so AddWindowToBSPTree splits at the focused window
	m.SplitTargetWindowID = focusedWin.ID

	// No preselection  - let determineAutoSplit (SchemeSmartSplit) pick the direction
	m.PreselectionDir = layout.PreselectionNone

	// Create a new window  - AddWindowToBSPTree will use SplitNone which triggers auto split
	m.AddWindow("")

	// Clear the split target
	m.SplitTargetWindowID = ""
}

// SetPreselection sets the preselection direction for the next window insertion
func (m *OS) SetPreselection(dir layout.PreselectionDir) {
	m.PreselectionDir = dir
	// Show notification about preselection
	var dirName string
	switch dir {
	case layout.PreselectionLeft:
		dirName = "left"
	case layout.PreselectionRight:
		dirName = "right"
	case layout.PreselectionUp:
		dirName = "up"
	case layout.PreselectionDown:
		dirName = "down"
	default:
		m.PreselectionDir = layout.PreselectionNone
		return
	}
	m.ShowNotification("Preselection: "+dirName, "info", config.NotificationDuration)
}

// ClearPreselection clears any active preselection
func (m *OS) ClearPreselection() {
	m.PreselectionDir = layout.PreselectionNone
}

// RotateFocusedSplit toggles the split direction at the focused window's parent
func (m *OS) RotateFocusedSplit() {
	if !m.AutoTiling {
		m.LogInfo("BSP: RotateSplit ignored - tiling not active")
		return
	}

	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		m.LogInfo("BSP: RotateSplit ignored - no tree for workspace %d", m.CurrentWorkspace)
		return
	}

	focusedWin := m.GetFocusedWindow()
	if focusedWin == nil {
		m.LogInfo("BSP: RotateSplit ignored - no focused window")
		return
	}

	windowIntID := m.getWindowIntID(focusedWin.ID)

	// Check if window is in the tree
	if !tree.HasWindow(windowIntID) {
		m.LogInfo("BSP: RotateSplit - window %d not in tree, has %d windows", windowIntID, tree.WindowCount())
		// Window not in tree - this can happen if tiling was enabled after windows were created
		// but the tree wasn't properly built. Let's rebuild it.
		m.LogInfo("BSP: Rebuilding tree to include all windows")
		m.TileAllWindows()
		return
	}

	node := tree.FindNode(windowIntID)
	if node == nil || node.Parent == nil {
		m.LogInfo("BSP: RotateSplit - window has no parent (is root), cannot rotate")
		m.ShowNotification("Cannot rotate: window has no parent split", "warning", 2000000000)
		return
	}

	tree.RotateSplit(windowIntID)
	m.LogInfo("BSP: Rotated split for window %d", windowIntID)

	// Reapply layout
	m.ApplyBSPLayout()
}

// EqualizeSplits resets all split ratios to 0.5 (equal splits)
func (m *OS) EqualizeSplits() {
	if !m.AutoTiling {
		return
	}

	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return
	}

	tree.EqualizeRatios()

	// Reapply layout
	m.ApplyBSPLayout()
}

// SwapWindowsInBSPTree swaps two windows in the BSP tree
func (m *OS) SwapWindowsInBSPTree(window1, window2 *terminal.Window) {
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return
	}

	id1 := m.getWindowIntID(window1.ID)
	id2 := m.getWindowIntID(window2.ID)
	tree.SwapWindows(id1, id2)
}
