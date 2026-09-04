package app

import (
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

// contentTileLayouts runs the master-stack tiler inside the content region:
// computed against the content width and shifted right by the left margin, the
// same box GetBSPBounds hands the BSP tree, so panes never tile under a
// reserved sidebar band on either side.
func (m *OS) contentTileLayouts(n int) []layout.TileLayout {
	layouts := layout.CalculateTilingLayout(n, m.GetContentWidth(), m.GetUsableHeight(), m.GetTopMargin(), m.MasterRatio, m.separatorGap())
	if lm := m.GetLeftMargin(); lm != 0 {
		for i := range layouts {
			layouts[i].X += lm
		}
	}
	return layouts
}

// calculateTilingLayout is a wrapper around contentTileLayouts for internal use
func (m *OS) calculateTilingLayout(n int) []tileLayout {
	layouts := m.contentTileLayouts(n)
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
	m.settleSizes(func() { m.tileAllWindows() })
}

// tileAllWindows is TileAllWindows with the announcements already held.
func (m *OS) tileAllWindows() {
	// Ends a deferral whose gesture is over, so the popup pass below, the
	// master-stack branch and ApplyBSPLayout further down agree about which path
	// they are on.
	deferring := m.resizeDeferralActive()

	// A popup is floating, so every branch below skips it - but skipping is not
	// the whole answer, for the reason it is not the whole answer for a zoomed
	// pane: the popup box is this client's own and it moves whenever the box the
	// panes go in moves. A retile is exactly when that has happened, and every
	// resize path ends here. This runs before the empty-list return below,
	// because a session whose only pane is a popup has no tiled windows at all
	// and still has a popup to place.
	m.applyPopupRects(deferring)

	// Get list of visible windows in current workspace (not minimized)
	var visibleWindows []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing && !w.IsFloating {
			visibleWindows = append(visibleWindows, w)
		}
	}

	if len(visibleWindows) == 0 {
		// No visible windows means no tiling structure, so a tree left behind here
		// still feeds CollectSplits and paints stale separators over the splash
		// after the last pane closes. The local close path nils the tree when it
		// empties (DeleteWindow); the daemon close path arrives through here
		// instead, where the early return used to skip the cleanup, so clear the
		// current workspace's tree to keep the two paths in step.
		if m.WorkspaceTrees != nil {
			m.WorkspaceTrees[m.CurrentWorkspace] = nil
		}
		return
	}

	m.LogInfo("TileAllWindows called with %d visible windows, BSP=%v, Scrolling=%v", len(visibleWindows), m.UseBSPLayout, m.UseScrollingLayout)

	// Only the BSP path plays an open animation, and it clears Opening itself as
	// it places each pane. Clear it here for the two layouts that do not, so a
	// pane created under a scrolling or master-stack layout cannot carry the flag
	// until whenever the user next switches to BSP and then bloom there, long
	// after it opened.
	if m.UseScrollingLayout || !m.UseBSPLayout {
		for _, w := range visibleWindows {
			w.Opening = false
		}
	}

	// A zoomed pane is not tiled, and the three branches below all skip its
	// rectangle - but skipping is not the whole answer, because the zoom box is
	// this client's own and it moves whenever the box the panes go in moves. A
	// retile is exactly when that has happened: every resize path ends here.
	// Left out, a client that resized while a peer held the zoom kept drawing
	// the pane at the old box, so the shell had two sizes.
	if zw := m.zoomedWindow(); zw != nil {
		m.applyZoomRect(zw, deferring)
	}

	// Scrolling layout mode (niri-like)
	//
	// It lays the strip out where the strip already is. It does not reveal the
	// focused column, which is the one thing this branch used to do that a
	// retile has no business doing: a retile is not a focus change. The events
	// that reach here are mostly not even this client's user - a peer
	// attaching, a routed setting change, a config file reload, a peer opening
	// its sidebar, any client adding or closing a window on any workspace - and
	// each of them dragged a strip the user had deliberately scrolled past the
	// focused column back to it, then pushed that offset to every peer as
	// session state.
	//
	// Revealing belongs to the events where the focus or the column set really
	// changed, and each of those has its own call: ScrollingOnFocusChange,
	// ScrollingOnWindowRemoved, adoptScrollStrip, and the restore in
	// os_minimize.go. scrollingSetPositions still clamps, so a retile that
	// narrowed the box or removed columns pulls the viewport back into range.
	if m.UseScrollingLayout {
		m.LogInfo("[SCROLL-TILE] TileAllWindows scrolling path, %d visible windows", len(visibleWindows))
		m.scrollingSetPositions()
		return
	}

	// Use master-stack layout if BSP is disabled
	if !m.UseBSPLayout {
		layouts := m.contentTileLayouts(len(visibleWindows))
		for i, l := range layouts {
			if i < len(visibleWindows) {
				// A zoomed pane keeps its slot and loses its rectangle to the
				// zoom box. See the same skip in ApplyBSPLayout.
				if visibleWindows[i].Zoomed {
					continue
				}
				// A snap still in flight owns this window's geometry and stamps
				// its own rectangle back on the next tick, without resizing the
				// emulator with it. ApplyBSPLayout and placePane both retire it
				// first; this branch did not, so a mode switch away from a
				// scrolling layout mid-slide left the pane drawing at one size
				// and its guest writing at another.
				m.CancelSnapAnimation(visibleWindows[i])
				visibleWindows[i].X = l.X
				visibleWindows[i].Y = l.Y
				// Set Tiled before Resize so the border deduction (and therefore
				// the emulator size) matches the shared-borders state.
				visibleWindows[i].Tiled = m.panesBorderless()
				// Mid-resize the PTY round trip is deferred, exactly as the BSP
				// path does it; ViewportResizeSettledMsg drains PendingResizes.
				if deferring {
					visibleWindows[i].ResizeVisual(l.Width, l.Height)
					m.PendingResizes[visibleWindows[i].ID] = [2]int{l.Width, l.Height}
				} else {
					visibleWindows[i].Resize(l.Width, l.Height)
				}
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
				m.LogInfo("BSP: Visible window %s has int ID %d", shortID(win.ID), intID)
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

		// Drop the stale windows and keep the splits around them. Rebuilding
		// the whole tree here used to throw the arrangement away whenever a
		// pane closed while tiling was off, which was every time the toggle
		// remembered a layout worth keeping.
		if hasStaleWindows {
			m.LogInfo("BSP: Removing stale windows from the tree")
			for _, id := range treeIDs {
				if !visibleIDs[id] {
					tree.RemoveWindow(id)
				}
			}
			if tree.IsEmpty() {
				m.WorkspaceTrees[m.CurrentWorkspace] = nil
				tree = nil
			}
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
			tree.InsertWindow(windowIntID, lastInsertedID, layout.SplitNone, 0.5, bounds, m.separatorGap())
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
			tree.InsertWindow(windowIntID, targetIntID, layout.SplitNone, 0.5, bounds, m.separatorGap())
			m.LogInfo("BSP: Added missing window (int ID %d) with target %d", windowIntID, targetIntID)
		}
	}
	m.ApplyBSPLayout()
}

// ToggleAutoTiling toggles automatic tiling mode
func (m *OS) ToggleAutoTiling() {
	m.SetAutoTiling(!m.AutoTiling)
}

// SetAutoTiling turns tiling on or off.
//
// It is the one transition every entry point goes through: the tiling key,
// the two palette rows, the tape commands, and the set-layout verb behind
// them. There used to be five copies, each writing the fields it remembered.
// The tape copy left every pane flagged borderless, so turning tiling off
// from a tape or the CLI drew panes with no borders and no dividers between
// them. The palette copy forgot the same flag on other workspaces and left a
// preselection armed. None of them brought a scrolling strip back on screen.
func (m *OS) SetAutoTiling(on bool) {
	m.settleSizes(func() { m.setAutoTiling(on) })
}

// setAutoTiling is SetAutoTiling with the announcements already held.
func (m *OS) setAutoTiling(on bool) {
	// Switching mode is structural: the layout it lands on is final, not a step
	// on the way to a size the user is still choosing.
	m.requireRealLayout()

	if m.AutoTiling == on {
		return
	}
	m.AutoTiling = on
	// Both deferred because the enabling branch returns early for scrolling
	// mode. The sync is the one that has to be: the daemon holds AutoTiling and
	// echoes it back to every client on the next push, so a client that turns
	// tiling on without telling the daemon has it turned off again by the first
	// state the daemon sends. That is what made scrolling mode refuse to tile in
	// a daemon session while BSP, which reached the sync at the end, worked.
	// Deferred in this order so the sync still runs before the hook, the way it
	// did when the BSP branch fell through to it.
	defer m.FireLayoutChanged()
	defer m.SyncStateToDaemon()

	if !on {
		m.leaveTiling()
		return
	}

	// If scrolling mode was active, re-enable it
	if m.UseScrollingLayout {
		m.LogInfo("Scrolling: Re-enabling scrolling tiling mode")
		// Clear old scrolling layout to rebuild from current windows
		delete(m.WorkspaceScrollingLayouts, m.CurrentWorkspace)
		sl := m.GetOrCreateScrollingLayout()
		sl.EnsureFocusedVisible(m.ScrollingViewWidth())
		m.scrollingSetPositions()
		for _, w := range m.Windows {
			if w.Workspace == m.CurrentWorkspace {
				w.InvalidateCache()
			}
		}
		return
	}

	// The BSP tree a workspace was tiled with is kept while tiling is off, and
	// tileAllWindows lays the panes out from it again, dropping the panes that
	// closed in between and adding the ones that opened. Building a fresh tree
	// here, as this used to, threw a deliberately arranged layout away on every
	// toggle. A master-stack layout has nothing to keep: its order is the
	// window order and its ratio is already remembered per workspace.
	m.LogInfo("Tiling: enabling, mode=%s", m.LayoutModeName())
	m.tileAllWindows()
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace {
			w.InvalidateCache()
		}
	}
}

// leaveTiling is the half of setAutoTiling that runs when tiling goes off. It
// gives every pane its own border back and makes sure every pane is somewhere
// the user can reach it.
func (m *OS) leaveTiling() {
	m.LogInfo("Tiling: disabling")
	// Clear preselection when disabling tiling
	m.PreselectionDir = layout.PreselectionNone
	// Every pane draws its own border again, so the column each split was
	// holding open for a divider now draws nothing at all. Hand it back to
	// the panes on either side instead of leaving it empty between them.
	//
	// First, so each tilable pane hears its new box once. The loop below used
	// to clear the flag at the pane's old rectangle and reclaim then gave it
	// the real one, which is two SIGWINCHes for one settled size.
	m.reclaimSeparatorGaps()
	// A scrolling strip is longer than the screen, and the panes past its edge
	// keep the rectangles the strip gave them: with nothing left to scroll the
	// strip, they were floating panes at x = -144 that no click could reach.
	m.bringPanesIntoView()
	for i := range m.Windows {
		// Still needed for the panes reclaim does not place - minimized and
		// floating ones - which keep their rectangle and owe the guest the two
		// columns and rows their border has just taken back. A no-op for the
		// panes reclaim already settled.
		m.Windows[i].SetTiled(false)
		m.Windows[i].InvalidateCache()
		m.Windows[i].ContentDirty = true
		m.Windows[i].Dirty = true
		m.Windows[i].PositionDirty = true
		m.Windows[i].HasNewOutput.Store(true)
	}
	m.MarkAllDirty()
}

// bringPanesIntoView moves every pane that lies partly outside the content
// region to the nearest place inside it. A pane wider or taller than the region
// keeps its size and is pinned to the region's top-left edge.
func (m *OS) bringPanesIntoView() {
	left := m.GetLeftMargin()
	top := m.GetTopMargin()
	width := m.GetContentWidth()
	height := m.GetUsableHeight()
	if width <= 0 || height <= 0 {
		return
	}
	for _, w := range m.Windows {
		// A floating pane was never under the tiler, and where it sits is where
		// its user dragged it, edge and all.
		if w.Minimized || w.Minimizing || w.IsFloating {
			continue
		}
		x := min(max(w.X, left), left+max(width-w.Width, 0))
		y := min(max(w.Y, top), top+max(height-w.Height, 0))
		if x == w.X && y == w.Y {
			continue
		}
		m.CancelSnapAnimation(w)
		w.X, w.Y = x, y
		w.MarkPositionDirty()
	}
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

	// Restore the master ratio this workspace was last left at. The map is the
	// session's, not this client's: it is adopted from session state on every
	// sync, so a workspace another client tuned is laid out here at the ratio that
	// client left it at rather than at whatever this one has configured. A
	// workspace no client has a value for has no entry, and falls back to the
	// configured ratio rather than to a literal half: with appearance.master_ratio
	// at 70 the first visit to a workspace used to snap the split back to 50 and
	// stay there, which reads as the setting being ignored and is the same
	// surprise for a ratio the resize keys had moved.
	if ratio, exists := m.WorkspaceMasterRatio[workspace]; exists {
		m.MasterRatio = ratio
	} else {
		m.MasterRatio = m.Settings.MasterRatioFraction()
	}

	// The rectangles this client cached the last time it left this workspace.
	//
	// Having none is not an answer about whether the workspace is custom. It used
	// to be read as one, and the flag was cleared here - which was harmless while
	// the flag was this client's own private memory, because a client only ever
	// asked about a workspace it had been to. The flag is the session's now (see
	// SessionState.WorkspaceHasCustom), and a client that has never been to a
	// workspace caches nothing for it, so clearing the flag here was the whole
	// bug: the first visit threw away the flag another client had set, retiled the
	// workspace and pushed the tiler's rectangles over the layout a user had
	// arranged.
	//
	// Nothing to re-apply is now just that. The panes are already at the session's
	// rectangles, which arrived on the windows themselves, so they are left where
	// they are and the flag is left as the session set it. The cache fills itself
	// in on the way out of the workspace, from those same rectangles.
	savedLayouts := m.WorkspaceLayouts[workspace]
	if len(savedLayouts) == 0 {
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

	// Do NOT force WorkspaceHasCustom = true here. SaveCurrentLayout runs on
	// every workspace switch, so a saved layout always exists after the first
	// switch; marking it custom on restore permanently suppressed the
	// retile-if-not-custom check (workspace.go), disabling auto-retiling for
	// both workspaces after a single round-trip. The custom flag is owned by
	// MarkLayoutCustom, which fires on an actual user resize.
}

// MarkLayoutCustom marks the current workspace as having a custom layout
func (m *OS) MarkLayoutCustom() {
	if m.AutoTiling {
		m.WorkspaceHasCustom[m.CurrentWorkspace] = true
		m.SaveCurrentLayout()
	}
}
