package app

import (
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
	// Tiling fills the content region, which starts after the left reserved
	// margin (the sidebar, when on the left) and is narrowed by both margins.
	// This alone re-tiles every pane into the reduced box.
	return layout.Rect{
		X: m.GetLeftMargin(),
		Y: m.GetTopMargin(),
		W: m.GetContentWidth(),
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

// openStartRect is the box a pane opens from: its own destination, halved about
// that destination's centre, so the pane grows outward into the space it is
// about to occupy.
//
// It replaces the start the layout used to inherit by accident. A new pane was
// parked at NewWindowPlacement's box first - half the screen, centred on it -
// and the snap animation captured that as its start, so every pane opened by
// travelling from a half-screen rectangle to a tile. Centred is not what that
// looks like: the box is far larger than a tile, so its top-left corner sits up
// and to the left of any tile but the middle one, and what the eye reads is a
// window flying in from the corner and shrinking, not a window appearing.
//
// Growing from the destination rather than from the centre of the screen is a
// deliberate choice between two fixes that both remove the corner. A pane
// belongs to its tile, and opening in place says so, while a small box crossing
// half the screen from the middle is motion that means nothing - and under
// shared borders it also passes over two neighbours on the way. The exception
// proves it: the first pane on a workspace has the whole screen as its tile, so
// it does grow from the centre of the screen, because there its tile is the
// screen.
//
// The floor is the size the minimize animation shrinks to, which is the smallest
// box the renderer is known to draw a border into. A tile at or below it opens
// from its full size (the caller checks for that and skips the start entirely),
// which is the pre-existing behaviour for a pane too small to animate.
func openStartRect(rect layout.Rect) (x, y, w, h int) {
	w = min(max(rect.W/2, ui.MinAnimatedWidth), rect.W)
	h = min(max(rect.H/2, ui.MinAnimatedHeight), rect.H)
	return rect.X + (rect.W-w)/2, rect.Y + (rect.H-h)/2, w, h
}

// ApplyBSPLayout applies the BSP tree layout to all windows in the current workspace
func (m *OS) ApplyBSPLayout() {
	tree := m.GetOrCreateBSPTree()
	if tree == nil || tree.IsEmpty() {
		return
	}

	bounds := m.GetBSPBounds()
	layouts := tree.ApplyLayout(bounds, m.separatorGap())

	// Asked once for the whole layout, not per pane: it is what decides between
	// the two branches below, and it ends a stale deferral as a side effect.
	deferring := m.resizeDeferralActive()

	// Read alongside the gap the rectangles above were partitioned with, so the
	// border every pane draws and the column the layout left between them are
	// the same decision. A pane given a bordered box inside a rectangle that
	// still holds a separator gap wastes that column; given a borderless one
	// where no gap was reserved, its neighbour's line lands on its own content.
	borderless := m.panesBorderless()

	for windowIntID, rect := range layouts {
		win := m.getWindowByIntID(windowIntID)
		if win == nil || win.Workspace != m.CurrentWorkspace || win.Minimized || win.IsFloating {
			continue
		}

		wasTiled := win.Tiled

		// Read and clear in the same breath, before any of the branches below
		// can leave the loop: a pane opens once, and a flag that survives its
		// placement makes the next retile play the open animation again.
		opening := win.Opening
		win.Opening = false

		// Cancel any existing snap animation for this window to prevent
		// animation pileup during continuous resize.
		m.CancelSnapAnimation(win)

		// A live resize drag reapplies this layout on every composed frame, to
		// keep the separator overlay's ratios in step with the geometry the
		// pointer is producing. Set that geometry directly.
		//
		// A resize is direct manipulation: the edge is where the pointer is,
		// so there is nothing to ease toward. Animating instead built a fresh
		// 300ms snap per window per frame, each one discarded and restarted by
		// the next frame before it could finish, so the panes never arrived
		// anywhere and trailed the cursor on a curve that kept resetting. That
		// costs almost no CPU, which is why it read as mush rather than as a
		// slow frame, and why no timing measurement found it.
		//
		// The normal path is also what tells the PTY, and in daemon mode the
		// daemon over its socket, about the new size: one round trip per window
		// per frame for a size the user is still in the middle of choosing.
		// Resize visually instead and record it in PendingResizes, which mouse
		// release already drains into one real resize per window.
		// A terminal resize is the same kind of event, one step removed: the
		// browser or the terminal emulator delivers a size per frame for as long
		// as the user drags the window edge, and easing toward each one built a
		// fresh 300ms snap per pane per step. The panes were still easing when
		// the next size arrived, so they never arrived anywhere, and after the
		// pointer stopped the layout kept moving for the rest of the last
		// animation - which is exactly the catch-up a drag feels like. The size
		// is not a destination to travel to; it is where the panes already are.
		//
		// Only while the resize is actually live, though. See
		// resize_deferral.go: a deferral that outlives its gesture leaves every
		// later retile placing panes visually and never giving them a real size.
		if deferring {
			win.X, win.Y = rect.X, rect.Y
			borderChanged := win.Tiled != borderless
			if borderChanged {
				win.Tiled = borderless
				win.InvalidateCache()
			}
			// A changed border allowance owes the guest a new box even at the
			// same rectangle: the drawable area is the rectangle less the border
			// cells, and those just appeared or went away.
			if borderChanged || win.Width != rect.W || win.Height != rect.H {
				win.ResizeVisual(rect.W, rect.H)
				// IsBeingManipulated freezes a pane's content at its cached
				// frame, which is right for a pane the pointer is dragging and
				// wrong for a terminal resize: nothing is being manipulated, and
				// the panes should keep drawing their live contents.
				if m.Resizing {
					win.IsBeingManipulated = true
				}
				m.PendingResizes[win.ID] = [2]int{rect.W, rect.H}
			}
			win.MarkPositionDirty()
			continue
		}

		// Border mode is settled here, with the rest of the layout, so that one
		// value decides it for the whole frame. A pane used to keep the old mode
		// until its snap completed, which let a frame draw both border systems at
		// once: the separator overlay reads the shared-borders state live, so it
		// filled the gaps the new layout had already reserved while every pane
		// still drew a box of its own. Any path that retired a snap early then
		// made that permanent, since the flag was waiting on an animation that no
		// longer existed.
		//
		// Before the placement, not after, and for the same reason the deferred
		// branch above does it first: the allowance decides how much of the
		// rectangle the guest gets, so settling it afterwards announces the
		// rectangle twice, once at each allowance.
		win.Tiled = borderless
		if borderless != wasTiled {
			win.InvalidateCache()
		}

		// A pane being placed for the first time starts from a box inside its own
		// destination rather than from wherever it was parked, so it grows into
		// place. See openStartRect. At zero duration there is no animation to
		// give a start to, and NewSnapAnimation lands the pane on the target in
		// one step, so the geometry is left exactly as the un-animated path had
		// it.
		dur := m.Settings.GetAnimationDuration()
		if opening && dur > 0 {
			if x, y, w, h := openStartRect(rect); w != rect.W || h != rect.H {
				win.X, win.Y = x, y
				// Assigned, not resized: the emulator keeps the size the pane was
				// created at for the length of the animation, the way every other
				// frame of a snap leaves it, and Update's completion resizes it
				// once at the destination.
				win.Width, win.Height = w, h
			}
		}

		// Create animation for smooth transition
		anim := ui.NewSnapAnimation(win, rect.X, rect.Y, rect.W, rect.H, dur)

		if anim != nil {
			m.Animations = append(m.Animations, anim)
		} else if borderless != wasTiled {
			// A pane already at its target rectangle is left alone by
			// NewSnapAnimation unless it is daemon-backed, and a changed
			// allowance owes the guest a new box even at the same rectangle.
			win.Resize(rect.W, rect.H)
		}
	}
}

// CancelSnapAnimation drops any in-flight snap animation for win.
//
// A snap animation owns its window's geometry until it finishes: every tick it
// writes an interpolated rectangle, ignoring whatever else has set X, Y, Width
// or Height in the meantime. So anything that positions a window directly has
// to retire the animation first, or the next tick will overwrite it with a
// frame of a transition the user has already moved past. Starting a resize
// drag while a window is still animating into place is the ordinary way to hit
// that: the drag sets geometry per motion event, the animation stamps its own
// back over all of it on the next tick, and the layout jumps to wherever the
// old transition had got to.
func (m *OS) CancelSnapAnimation(win *terminal.Window) {
	for i := len(m.Animations) - 1; i >= 0; i-- {
		if m.Animations[i].Window == win && m.Animations[i].Type == ui.AnimationSnap {
			m.Animations = append(m.Animations[:i], m.Animations[i+1:]...)
		}
	}
}

// landSnapAnimations finishes every snap in flight at its own destination and
// retires it, leaving the other animation kinds alone.
//
// Cancelling is right for a caller that is about to place the window itself.
// A caller that is not - a structural change that settles the layout by some
// other route - has to land the snap instead: a snap deliberately leaves the
// emulator at the size the pane had when it started and catches up in one
// resize at the end, so dropping it mid-flight leaves the pane at an
// interpolated rectangle with an emulator that matches neither end. Turning
// tiling off while the scrolling strip was still sliding did exactly that, and
// a tick later the panes were back at the strip's rectangles with their guests
// writing at the old size.
func (m *OS) landSnapAnimations() {
	if len(m.Animations) == 0 {
		return
	}
	kept := m.Animations[:0]
	for _, anim := range m.Animations {
		if anim.Type != ui.AnimationSnap || anim.Window == nil {
			kept = append(kept, anim)
			continue
		}
		win := anim.Window
		win.X, win.Y = anim.EndX, anim.EndY
		win.Resize(anim.EndWidth, anim.EndHeight)
		win.InvalidateCache()
		win.MarkPositionDirty()
	}
	m.Animations = kept
}

// AddWindowToBSPTree adds a window to the BSP tree and applies the layout.
// This should be called when a new window is created in tiling mode.
func (m *OS) AddWindowToBSPTree(window *terminal.Window) {
	// A new pane is a structural change, not a resize step. Whatever resize was
	// in flight, the layout this produces is what the user is left looking at,
	// so it has to be a real one.
	m.requireRealLayout()

	tree := m.GetOrCreateBSPTree()
	windowIntID := m.getWindowIntID(window.ID)

	if verboseLog {
		m.LogInfo("BSP: AddWindowToBSPTree for window %s (int ID %d)", shortID(window.ID), windowIntID)
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
		tree.InsertWindowWithPreselection(windowIntID, targetIntID, m.PreselectionDir, bounds, m.separatorGap())
		m.PreselectionDir = layout.PreselectionNone // Clear preselection
	} else {
		tree.InsertWindow(windowIntID, targetIntID, layout.SplitNone, 0.5, bounds, m.separatorGap())
	}

	m.LogInfo("BSP: Tree now has %d windows", tree.WindowCount())

	// No pre-positioning here. A pane being created carries Opening and
	// ApplyBSPLayout starts it from its own tile; a pane arriving from
	// ToggleFloating does not, and animates from where the user last left it
	// floating, which this used to throw away by teleporting it to the middle of
	// the screen first.
	m.ApplyBSPLayout()
}

// RemoveWindowFromBSPTree removes a window from the BSP tree and reapplies the layout.
// This should be called when a window is closed in tiling mode.
func (m *OS) RemoveWindowFromBSPTree(window *terminal.Window) {
	// Closing a pane is structural too; the panes that inherit its space must
	// end up at real sizes. See AddWindowToBSPTree.
	m.requireRealLayout()

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
	tree.SyncRatiosFromGeometry(geometry, bounds, m.separatorGap())

	// In shared borders mode, re-apply layout after sync to enforce separator gaps
	if m.SharedBorders {
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

	// In a daemon session the new pane is created daemon-side and arrives through
	// a later state sync, so the preselection set here would never be consumed
	// (AddWindow only asks the daemon and returns). Record the forced direction so
	// the sync path applies it; see adoptSyncedWindows.
	if m.IsDaemonSession && m.DaemonClient != nil {
		m.pendingSplitDir = layout.PreselectionDown
		m.pendingSplitTarget = focusedWin.ID
		m.AddWindow("")
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

	// See SplitFocusedHorizontal: on the daemon path the forced direction has to
	// outlive the round trip that creates the pane.
	if m.IsDaemonSession && m.DaemonClient != nil {
		m.pendingSplitDir = layout.PreselectionRight
		m.pendingSplitTarget = focusedWin.ID
		m.AddWindow("")
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
	m.ShowNotification("Preselection: "+dirName, "info", m.Settings.NotificationDuration)
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

// tiledLayoutStale reports whether the tiled panes no longer fill the box
// tiling is supposed to partition, so the layout has to be recomputed.
//
// Tiling covers GetBSPBounds exactly: every visible, non-floating pane sits
// inside it, and together they reach its right and bottom edges. Either half of
// that failing means the rectangles were computed against some other box - a
// peer client's, at its own size - and this client has to lay them out again.
//
// Checked in both directions on purpose. Asking only whether a pane overflows
// finds a peer that is larger and is blind to one that is smaller, and a
// session shrinking and growing back produces exactly the smaller case.
func (m *OS) tiledLayoutStale() bool {
	bounds := m.GetBSPBounds()
	if bounds.W <= 0 || bounds.H <= 0 {
		return false
	}

	// A zoomed pane is the layout deliberately not tiling: it holds a rectangle
	// tiling did not choose and, under ZoomMaxWidth, one that does not reach the
	// edges. Judging that stale would retile it and drop the user out of zoom.
	if fw := m.GetFocusedWindow(); fw != nil && fw.Zoomed {
		return false
	}

	// The near edges start at the far corner so the minima below need no first
	// pass; any is what says whether they were ever taken.
	left, top := bounds.X+bounds.W, bounds.Y+bounds.H
	right, bottom := 0, 0
	any := false
	for _, w := range m.Windows {
		if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized || w.Minimizing || w.IsFloating {
			continue
		}
		// Outside the box on any side: unambiguously stale.
		if w.X < bounds.X || w.Y < bounds.Y ||
			w.X+w.Width > bounds.X+bounds.W || w.Y+w.Height > bounds.Y+bounds.H {
			return true
		}
		any = true
		left = min(left, w.X)
		top = min(top, w.Y)
		right = max(right, w.X+w.Width)
		bottom = max(bottom, w.Y+w.Height)
	}
	if !any {
		return false
	}
	// Inside the box but not filling it. A separator gap between panes is taken
	// out of the space between them, never off the far edge, so a settled
	// layout always reaches all four.
	//
	// All four, not two. The check used to measure the far edges alone, which
	// misses a layout inset from the near ones - and that is not a hypothetical
	// shape. A client attaching is handed the session's reserve as it stands,
	// lays the panes out against it and pushes; if the reserve then shrinks
	// because that same client asks for less chrome than the one already there,
	// the pushed rectangles start too far in and still reach the far edges
	// exactly. A peer that had already retiled against the smaller reserve
	// adopted them and had no reason to look again, so it drew every shared
	// shell narrower than the shell was actually running.
	return left != bounds.X || top != bounds.Y ||
		right != bounds.X+bounds.W || bottom != bounds.Y+bounds.H
}
