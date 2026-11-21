package app

import (
	"errors"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// GetOrCreateBSPTree returns the BSP tree for the current workspace, creating
// it if needed.
//
// A tree created here starts with the configured default scheme
// (appearance.tiling_scheme, read live off m.Settings so a change takes hold
// on the next workspace that needs one). Once a workspace has a tree its
// scheme lives on the tree itself (see layout.BSPTree.AutoScheme), which is
// what keeps a config change from moving a workspace that is already tiled,
// and it travels with the tree through session state and the daemon's
// layout-tree ops.
func (m *OS) GetOrCreateBSPTree() *layout.BSPTree {
	if m.WorkspaceTrees == nil {
		m.WorkspaceTrees = make(map[int]*layout.BSPTree)
	}

	tree, exists := m.WorkspaceTrees[m.CurrentWorkspace]
	if !exists || tree == nil {
		tree = layout.NewBSPTree()
		scheme := m.Settings.TilingScheme
		if scheme == "" {
			scheme = config.TilingSchemeSpiral
		}
		tree.AutoScheme = layout.ParseAutoScheme(scheme)
		m.WorkspaceTrees[m.CurrentWorkspace] = tree
		m.LogInfo("BSP: Created new tree for workspace %d with scheme %s", m.CurrentWorkspace, tree.AutoScheme.String())
	}

	return tree
}

// GetBSPBounds returns the bounds for BSP layout calculation
func (m *OS) GetBSPBounds() layout.Rect {
	return layout.Rect{
		X: m.PaneLeft(),
		Y: m.PaneTop(),
		W: m.PaneWidth(),
		H: m.PaneHeight(),
	}
}

// GetWindowIntID returns a stable integer ID for a window string ID.
// Uses a direct map lookup for reliable ID assignment.
func (m *OS) GetWindowIntID(stringID string) int {
	if stringID == "" {
		return 0
	}

	if m.WindowToBSPID == nil {
		m.WindowToBSPID = make(map[string]int)
	}

	if id, exists := m.WindowToBSPID[stringID]; exists {
		return id
	}

	if m.NextBSPWindowID == 0 {
		m.NextBSPWindowID = 1
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

// GetWindowByIntID returns the window for a given integer ID
func (m *OS) GetWindowByIntID(intID int) *terminal.Window {
	if intID <= 0 {
		return nil
	}

	if stringID, ok := m.BSPIDToWindowID[intID]; ok {
		if id, exists := m.WindowToBSPID[stringID]; exists && id == intID {
			for _, w := range m.Windows {
				if w.ID == stringID {
					return w
				}
			}
		}
	}

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

func openStartRect(rect layout.Rect) (x, y, w, h int) {
	w = min(max(rect.W/2, ui.MinAnimatedWidth), rect.W)
	h = min(max(rect.H/2, ui.MinAnimatedHeight), rect.H)
	return rect.X + (rect.W-w)/2, rect.Y + (rect.H-h)/2, w, h
}

// ApplyBSPLayout applies the BSP tree layout to all windows in the current workspace
func (m *OS) ApplyBSPLayout() {
	defer m.refreshDropZone()
	tree := m.GetOrCreateBSPTree()
	if tree == nil || tree.IsEmpty() {
		return
	}

	bounds := m.GetBSPBounds()
	layouts := tree.ApplyLayout(bounds, m.separatorGap())

	deferring := m.resizeDeferralActive()

	borderless := m.panesBorderless()

	canvas, zoomBounds, ok := m.bspZoomCanvas(tree, bounds, layouts)
	if ok {
		bounds = zoomBounds
		layouts = tree.ApplyLayout(bounds, m.separatorGap())
	}
	m.zoomCanvasNow = canvas

	zoomRetile := m.takeZoomRelayout()

	for windowIntID, rect := range layouts {
		win := m.GetWindowByIntID(windowIntID)
		if win == nil || win.Workspace != m.CurrentWorkspace || win.Minimized || win.IsFloating {
			continue
		}
		rect = canvas.apply(rect)
		if above, below := m.stackRows(win); above+below > 0 && rect.H > above+below+1 {
			rect.Y += above
			rect.H -= above + below
		}
		if win.Zoomed && !m.zoomUsesLayout(win) {
			continue
		}
		if m.noteDragSlot(win, rect) {
			continue
		}

		wasTiled := win.Tiled

		opening := win.Opening
		win.Opening = false

		if deferring {
			m.CancelSnapAnimation(win)
			win.X, win.Y = rect.X, rect.Y
			borderChanged := win.Tiled != borderless
			if borderChanged {
				win.Tiled = borderless
				win.InvalidateCache()
			}
			if borderChanged || win.Width != rect.W || win.Height != rect.H {
				win.ResizeVisual(rect.W, rect.H)
				if m.Resizing {
					win.IsBeingManipulated = true
				}
				m.PendingResizes[win.ID] = [2]int{rect.W, rect.H}
			}
			win.MarkPositionDirty()
			continue
		}

		win.Tiled = borderless
		if borderless != wasTiled {
			win.InvalidateCache()
		}

		dur := m.layoutAnimationDuration()
		if zoomRetile && !m.Settings.MotionOn(config.AspectZoom) {
			dur = 0
		}
		if opening && dur > 0 {
			if x, y, w, h := openStartRect(rect); w != rect.W || h != rect.H {
				m.CancelSnapAnimation(win)
				win.X, win.Y = x, y
				win.Width, win.Height = w, h
			}
		}

		if anim := m.animateWindow(win, rect.X, rect.Y, rect.W, rect.H, dur); anim == nil && borderless != wasTiled {
			win.Resize(rect.W, rect.H)
		}
	}
}

func (m *OS) bspZoomCanvas(tree *layout.BSPTree, bounds layout.Rect, layouts map[int]layout.Rect) (zoomCanvas, layout.Rect, bool) {
	zw := m.zoomedWindow()
	if zw == nil {
		return zoomCanvas{}, bounds, false
	}
	id := m.GetWindowIntID(zw.ID)
	tile, ok := layouts[id]
	if !ok {
		return zoomCanvas{}, bounds, false
	}
	zoomBounds, ok := m.zoomCanvasBounds(zw, tile)
	if !ok {
		return zoomCanvas{}, bounds, false
	}
	grown := tree.ApplyLayout(zoomBounds, m.separatorGap())
	zoomedRect, ok := grown[id]
	if !ok {
		return zoomCanvas{}, bounds, false
	}
	return m.zoomCanvasAt(zoomBounds, zoomedRect), zoomBounds, true
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
//
//go:noinline
func (m *OS) CancelSnapAnimation(win *terminal.Window) {
	for i := len(m.Animations) - 1; i >= 0; i-- {
		if m.Animations[i].Window == win && m.Animations[i].Type == ui.AnimationSnap {
			m.Animations = append(m.Animations[:i], m.Animations[i+1:]...)
		}
	}
}

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
	m.requireRealRelayout()

	tree := m.GetOrCreateBSPTree()
	windowIntID := m.GetWindowIntID(window.ID)

	if verboseLog() {
		m.LogInfo("BSP: AddWindowToBSPTree for window %s (int ID %d)", shortID(window.ID), windowIntID)
	}

	targetID := m.SplitTargetWindowID
	if targetID == "" {
		if fw := m.GetFocusedWindow(); fw != nil && fw.ID != window.ID {
			targetID = fw.ID
		}
	}
	targetIntID := m.bspSplitTarget(tree, targetID)
	if verboseLog() {
		m.LogInfo("BSP: Splitting int ID %d", targetIntID)
	}

	bounds := m.GetBSPBounds()

	if m.PreselectionDir != layout.PreselectionNone {
		m.LogInfo("BSP: Inserting with preselection %d", m.PreselectionDir)
		tree.InsertWindowWithPreselection(windowIntID, targetIntID, m.PreselectionDir, bounds, m.separatorGap())
		m.PreselectionDir = layout.PreselectionNone
	} else {
		tree.InsertWindow(windowIntID, targetIntID, layout.SplitNone, 0.5, bounds, m.separatorGap())
	}

	m.LogInfo("BSP: Tree now has %d windows", tree.WindowCount())

	m.ApplyBSPLayout()
}

func (m *OS) bspSplitTarget(tree *layout.BSPTree, windowID string) int {
	if windowID != "" {
		if id := m.GetWindowIntID(windowID); tree.HasWindow(id) {
			return id
		}
	}
	if ids := tree.GetAllWindowIDs(); len(ids) > 0 {
		return ids[len(ids)-1]
	}
	return 0
}

// RemoveWindowFromBSPTree removes a window from the BSP tree and reapplies the layout.
// This should be called when a window is closed in tiling mode.
func (m *OS) RemoveWindowFromBSPTree(window *terminal.Window) {
	m.requireRealRelayout()

	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return
	}

	if !m.handOverStack(window, tree) {
		windowIntID := m.GetWindowIntID(window.ID)
		tree.RemoveWindow(windowIntID)
	}

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
// SyncBSPTreeFromGeometry unconditionally. That last one cannot be skipped:
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

	geometry := make(map[int]layout.Rect)
	for _, win := range m.Windows {
		if win.Workspace == m.CurrentWorkspace && !win.Minimized {
			windowIntID := m.GetWindowIntID(win.ID)
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

	if m.SharedBorders {
		m.ApplyBSPLayout()
	}
}

// SplitFocusedHorizontal splits the focused window horizontally (top/bottom) and creates a new terminal
func (m *OS) SplitFocusedHorizontal() {
	m.splitFocused(layout.PreselectionDown, false)
}

// SplitFocusedVertical splits the focused window vertically (left/right) and creates a new terminal
func (m *OS) SplitFocusedVertical() {
	m.splitFocused(layout.PreselectionRight, false)
}

func (m *OS) splitFocused(dir layout.PreselectionDir, followSSH bool) {
	if !m.AutoTiling {
		return
	}

	focusedWin := m.GetFocusedWindow()
	if focusedWin == nil {
		return
	}
	if err := m.SplitRefusal(dir); err != nil {
		m.ShowNotification("This pane is too small to split.", "warning", m.Settings.NotificationDuration)
		return
	}

	if m.IsDaemonSession && m.DaemonClient != nil {
		m.pendingSplitDir = dir
		m.pendingSplitTarget = focusedWin.ID
		m.PreselectionDir = layout.PreselectionNone
		if followSSH {
			m.newWindowFollowingSSH(focusedWin)
		} else {
			m.NewWindowHere()
		}
		return
	}

	m.SplitTargetWindowID = focusedWin.ID

	m.PreselectionDir = dir

	if followSSH {
		m.newWindowFollowingSSH(focusedWin)
	} else {
		m.AddWindow("")
	}

	m.SplitTargetWindowID = ""
}

// ErrPaneTooSmall is why a split is refused: one of the two panes it makes
// would be under the smallest pane size.
var ErrPaneTooSmall = errors.New("the pane is too small to split")

// SplitRefusal says why a split of the focused pane toward dir would not run,
// or nil. A split that leaves either pane under the smallest size
// (config.DefaultWindowWidth by config.DefaultWindowHeight cells) is refused,
// because a pane that small cannot be resized back up without a jump and no
// program in it fits. Only the split tree is checked: the other tilers place
// panes by their own rules.
func (m *OS) SplitRefusal(dir layout.PreselectionDir) error {
	if !m.AutoTiling || m.LayoutName() != LayoutModeBSP {
		return nil
	}
	focused := m.GetFocusedWindow()
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if focused == nil || tree == nil || focused.IsFloating {
		return nil
	}
	split := layout.SplitHorizontal
	if dir == layout.PreselectionLeft || dir == layout.PreselectionRight {
		split = layout.SplitVertical
	}
	if !tree.CanSplit(m.GetWindowIntID(focused.ID), split, tree.DefaultRatio, m.GetBSPBounds(), m.separatorGap()) {
		return ErrPaneTooSmall
	}
	return nil
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

	m.SplitTargetWindowID = focusedWin.ID

	m.PreselectionDir = layout.PreselectionNone

	m.AddWindow("")

	m.SplitTargetWindowID = ""
}

// SetPreselection sets the preselection direction for the next window insertion
func (m *OS) SetPreselection(dir layout.PreselectionDir) {
	m.PreselectionDir = dir
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
		m.LogInfo("BSP: RotateSplit ignored, tiling not active")
		return
	}

	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		m.LogInfo("BSP: RotateSplit ignored, no tree for workspace %d", m.CurrentWorkspace)
		return
	}

	focusedWin := m.GetFocusedWindow()
	if focusedWin == nil {
		m.LogInfo("BSP: RotateSplit ignored, no focused window")
		return
	}

	windowIntID := m.GetWindowIntID(focusedWin.ID)

	if !tree.HasWindow(windowIntID) {
		m.LogInfo("BSP: RotateSplit: window %d not in tree, has %d windows", windowIntID, tree.WindowCount())
		m.LogInfo("BSP: Rebuilding tree to include all windows")
		m.TileAllWindows()
		return
	}

	node := tree.FindNode(windowIntID)
	if node == nil || node.Parent == nil {
		m.LogInfo("BSP: RotateSplit: window has no parent (is root), cannot rotate")
		m.ShowNotification("Cannot rotate: window has no parent split", "warning", 2000000000)
		return
	}

	tree.RotateSplit(windowIntID)
	m.LogInfo("BSP: Rotated split for window %d", windowIntID)

	m.ApplyBSPLayout()
}

// EqualizeSplits resets the splits of the layout on screen: every BSP split
// ratio to 0.5, or the master-stack splits to the configured master ratio and
// equal shares for every other pane.
func (m *OS) EqualizeSplits() {
	if !m.AutoTiling {
		return
	}
	if m.inMasterStack() {
		m.equalizeMasterStack()
		return
	}
	if !m.UseBSPLayout {
		return
	}

	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return
	}

	tree.EqualizeRatios()

	m.ApplyBSPLayout()
}

func (m *OS) equalizeMasterStack() {
	ws := m.CurrentWorkspace
	m.setMasterRatio(m.Settings.MasterRatioFraction())
	m.setWorkspaceStackRatio(ws, 0)
	m.setWorkspaceMasterSplits(ws, layout.MasterSplits{})
	m.TileAllWindows()
	m.SyncStateToDaemon()
}

var tilingSchemeCycle = []layout.AutoScheme{
	layout.SchemeSpiral,
	layout.SchemeLongestSide,
	layout.SchemeAlternate,
	layout.SchemeSmartSplit,
}

// CycleTilingScheme moves the current workspace's tree to the next insertion
// scheme in tilingSchemeCycle and returns its name, or "" when there is no
// tiled workspace to change.
//
// This changes only the scheme, which decides where the next split goes; it
// never reflows the windows already on screen. GetOrCreateBSPTree creates the
// tree with the configured default when the workspace has none yet, so this
// always has a tree to turn.
func (m *OS) CycleTilingScheme() string {
	if !m.AutoTiling {
		return ""
	}
	tree := m.GetOrCreateBSPTree()
	idx := slices.Index(tilingSchemeCycle, tree.AutoScheme)
	tree.AutoScheme = tilingSchemeCycle[(idx+1)%len(tilingSchemeCycle)]
	return tree.AutoScheme.String()
}

// SetTilingScheme sets the current workspace's insertion scheme directly. It
// reports whether it applied, which is false only when tiling is off.
func (m *OS) SetTilingScheme(scheme layout.AutoScheme) bool {
	if !m.AutoTiling {
		return false
	}
	m.GetOrCreateBSPTree().AutoScheme = scheme
	return true
}

// TilingSchemeLabel names an insertion scheme the way the dock message and
// the command palette show it, matching config.TilingSchemes.
func TilingSchemeLabel(scheme string) string {
	switch scheme {
	case config.TilingSchemeSpiral:
		return "spiral"
	case config.TilingSchemeLongestSide:
		return "longest side"
	case config.TilingSchemeAlternate:
		return "alternate"
	case config.TilingSchemeSmartSplit:
		return "smart split"
	default:
		return scheme
	}
}

// SwapWindowsInBSPTree swaps two windows in the BSP tree
func (m *OS) SwapWindowsInBSPTree(window1, window2 *terminal.Window) {
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return
	}

	id1 := m.GetWindowIntID(window1.ID)
	id2 := m.GetWindowIntID(window2.ID)
	tree.SwapWindows(id1, id2)
}

func (m *OS) tiledLayoutStale() bool {
	if m.UseScrollingLayout {
		return m.scrollingLayoutStale()
	}

	bounds := m.GetBSPBounds()
	if bounds.W <= 0 || bounds.H <= 0 {
		return false
	}

	if m.zoomedWindow() != nil {
		return true
	}

	left, top := bounds.X+bounds.W, bounds.Y+bounds.H
	right, bottom := 0, 0
	any := false
	for _, w := range m.Windows {
		if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		wx, wy, ww, wh := m.dragSlotOf(w)
		if wx < bounds.X || wy < bounds.Y ||
			wx+ww > bounds.X+bounds.W || wy+wh > bounds.Y+bounds.H {
			return true
		}
		any = true
		left = min(left, wx)
		top = min(top, wy)
		right = max(right, wx+ww)
		bottom = max(bottom, wy+wh)
	}
	if !any {
		return false
	}
	return left != bounds.X || top != bounds.Y ||
		right != bounds.X+bounds.W || bottom != bounds.Y+bounds.H
}
