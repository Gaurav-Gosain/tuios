package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const (
	edgeTolerance = 2
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

func (m *OS) contentTileLayouts(n int) []layout.TileLayout {
	return m.tileLayoutsIn(n, layout.Rect{
		X: m.PaneLeft(), Y: m.PaneTop(),
		W: m.PaneWidth(), H: m.PaneHeight(),
	})
}

func (m *OS) tileLayoutsIn(n int, bounds layout.Rect) []layout.TileLayout {
	layouts := layout.CalculateMasterLayout(n, bounds.W, bounds.H, bounds.Y, m.masterParams())
	if bounds.X != 0 {
		for i := range layouts {
			layouts[i].X += bounds.X
		}
	}
	return layouts
}

// TileAllWindows arranges all visible windows in a tiling layout
func (m *OS) TileAllWindows() {
	m.settleSizes(func() { m.tileAllWindows() })
}

func (m *OS) tileAllWindows() {
	deferring := m.resizeDeferralActive()

	m.applyPopupRects(deferring)

	m.openOrphanStacks()

	var visibleWindows []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
			visibleWindows = append(visibleWindows, w)
		}
	}

	if len(visibleWindows) == 0 {
		if m.WorkspaceTrees != nil {
			m.WorkspaceTrees[m.CurrentWorkspace] = nil
		}
		return
	}

	m.LogInfo("TileAllWindows called with %d visible windows, BSP=%v, Scrolling=%v", len(visibleWindows), m.UseBSPLayout, m.UseScrollingLayout)

	if m.UseScrollingLayout {
		for _, w := range visibleWindows {
			w.Opening = false
		}
	}

	if zw := m.zoomedWindow(); zw != nil && !m.zoomUsesLayout(zw) {
		m.applyZoomRect(zw, deferring)
	}

	if m.UseScrollingLayout {
		m.LogInfo("[SCROLL-TILE] TileAllWindows scrolling path, %d visible windows", len(visibleWindows))
		m.ScrollingSetPositions()
		return
	}

	if !m.UseBSPLayout {
		opening := make(map[string]bool, len(visibleWindows))
		for _, w := range visibleWindows {
			if w.Opening {
				opening[w.ID] = true
			}
			w.Opening = false
		}
		dur := m.layoutAnimationDuration()
		if deferring {
			dur = 0
		}
		if m.takeZoomRelayout() && !m.Settings.MotionOn(config.AspectZoom) {
			dur = 0
		}

		layouts := m.contentTileLayouts(len(visibleWindows))
		canvas, zoomBounds, ok := m.masterZoomCanvas(visibleWindows, layouts)
		if ok {
			layouts = m.tileLayoutsIn(len(visibleWindows), zoomBounds)
		}
		m.zoomCanvasNow = canvas
		for i, l := range layouts {
			if i < len(visibleWindows) {
				if visibleWindows[i].Zoomed && !m.zoomUsesLayout(visibleWindows[i]) {
					continue
				}
				if r := canvas.apply(layout.Rect{X: l.X, Y: l.Y, W: l.Width, H: l.Height}); canvas.on {
					l.X, l.Y, l.Width, l.Height = r.X, r.Y, r.W, r.H
				}
				if m.noteDragSlot(visibleWindows[i], layout.Rect{X: l.X, Y: l.Y, W: l.Width, H: l.Height}) {
					continue
				}
				if dur <= 0 {
					m.CancelSnapAnimation(visibleWindows[i])
				}

				if dur > 0 {
					rect := layout.Rect{X: l.X, Y: l.Y, W: l.Width, H: l.Height}
					if opening[visibleWindows[i].ID] {
						if x, y, w, h := openStartRect(rect); w != rect.W || h != rect.H {
							m.CancelSnapAnimation(visibleWindows[i])
							visibleWindows[i].X, visibleWindows[i].Y = x, y
							visibleWindows[i].Width, visibleWindows[i].Height = w, h
						}
					}
					visibleWindows[i].Tiled = m.panesBorderless()
					if anim := m.animateWindow(visibleWindows[i], rect.X, rect.Y, rect.W, rect.H, dur); anim != nil {
						visibleWindows[i].InvalidateCache()
						continue
					}
				}

				visibleWindows[i].X = l.X
				visibleWindows[i].Y = l.Y
				visibleWindows[i].Tiled = m.panesBorderless()
				m.resizePane(visibleWindows[i], l.Width, l.Height, deferring)
				visibleWindows[i].InvalidateCache()
			}
		}
		return
	}

	tree := m.WorkspaceTrees[m.CurrentWorkspace]

	if tree != nil && !tree.IsEmpty() {
		treeIDs := tree.GetAllWindowIDs()
		visibleIDs := make(map[int]bool)
		for _, win := range visibleWindows {
			intID := m.GetWindowIntID(win.ID)
			visibleIDs[intID] = true
			if verboseLog() {
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

	if tree == nil || tree.IsEmpty() {
		m.LogInfo("BSP: Creating fresh tree for %d windows", len(visibleWindows))
		tree = m.GetOrCreateBSPTree()

		bounds := m.GetBSPBounds()
		var lastInsertedID = 0

		for i, win := range visibleWindows {
			windowIntID := m.GetWindowIntID(win.ID)
			tree.InsertWindow(windowIntID, lastInsertedID, layout.SplitNone, 0.5, bounds, m.separatorGap())
			lastInsertedID = windowIntID
			m.LogInfo("BSP: Added window %d (int ID %d) with target %d", i+1, windowIntID, lastInsertedID)
		}

		m.ApplyBSPLayout()
		return
	}

	allInTree := true
	for _, win := range visibleWindows {
		windowIntID := m.GetWindowIntID(win.ID)
		if !tree.HasWindow(windowIntID) {
			allInTree = false
			break
		}
	}

	if allInTree {
		m.ApplyBSPLayout()
		return
	}

	m.LogInfo("BSP: Adding missing windows to existing tree")

	for _, win := range visibleWindows {
		windowIntID := m.GetWindowIntID(win.ID)
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
// them. One copy means every entry point writes the same fields. Separate
// copies drift: a path that forgets the borderless flag, for example, draws
// panes with no borders and no dividers after tiling is turned off.
func (m *OS) SetAutoTiling(on bool) {
	if m.InScratchView() && !on {
		m.ShowNotification("A scratch terminal is always tiled.", "info", m.Settings.NotificationDuration)
		return
	}
	m.settleSizes(func() { m.setAutoTiling(on) })
}

func (m *OS) setAutoTiling(on bool) {
	m.requireRealLayout()

	if m.AutoTiling == on {
		return
	}
	m.AutoTiling = on
	defer m.FireLayoutChanged()
	defer m.SyncStateToDaemon()

	if !on {
		m.leaveTiling()
		return
	}

	if m.UseScrollingLayout {
		m.LogInfo("Scrolling: Re-enabling scrolling tiling mode")
		delete(m.WorkspaceScrollingLayouts, m.CurrentWorkspace)
		sl := m.GetOrCreateScrollingLayout()
		sl.EnsureFocusedVisible(m.ScrollingViewWidth())
		m.ScrollingSetPositions()
		for _, w := range m.Windows {
			if w.Workspace == m.CurrentWorkspace {
				w.InvalidateCache()
			}
		}
		return
	}

	m.LogInfo("Tiling: enabling, mode=%s", m.LayoutModeName())
	m.tileAllWindows()
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace {
			w.InvalidateCache()
		}
	}
}

func (m *OS) leaveTiling() {
	m.LogInfo("Tiling: disabling")
	m.PreselectionDir = layout.PreselectionNone
	m.reclaimSeparatorGaps()
	m.bringPanesIntoView()
	for i := range m.Windows {
		m.Windows[i].SetTiled(false)
		m.Windows[i].InvalidateCache()
		m.Windows[i].ContentDirty = true
		m.Windows[i].Dirty = true
		m.Windows[i].PositionDirty = true
		m.Windows[i].HasNewOutput.Store(true)
	}
	m.MarkAllDirty()
}

func (m *OS) bringPanesIntoView() {
	left := m.PaneLeft()
	top := m.PaneTop()
	width := m.PaneWidth()
	height := m.PaneHeight()
	if width <= 0 || height <= 0 {
		return
	}
	for _, w := range m.Windows {
		if w.Minimized || w.IsFloating {
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

func (m *OS) masterZoomCanvas(wins []*terminal.Window, layouts []layout.TileLayout) (zoomCanvas, layout.Rect, bool) {
	zw := m.zoomedWindow()
	if zw == nil {
		return zoomCanvas{}, layout.Rect{}, false
	}
	slot := -1
	for i, w := range wins {
		if w == zw && i < len(layouts) {
			slot = i
			break
		}
	}
	if slot < 0 {
		return zoomCanvas{}, layout.Rect{}, false
	}
	l := layouts[slot]
	zoomBounds, ok := m.zoomCanvasBounds(zw, layout.Rect{X: l.X, Y: l.Y, W: l.Width, H: l.Height})
	if !ok {
		return zoomCanvas{}, layout.Rect{}, false
	}
	grown := m.tileLayoutsIn(len(wins), zoomBounds)
	if slot >= len(grown) {
		return zoomCanvas{}, layout.Rect{}, false
	}
	g := grown[slot]
	return m.zoomCanvasAt(zoomBounds, layout.Rect{X: g.X, Y: g.Y, W: g.Width, H: g.Height}), zoomBounds, true
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
	if name, ok := m.WorkspaceLayoutMode[workspace]; ok {
		m.ApplyLayoutModeName(name)
	} else if m.sessionLayoutMode != "" {
		m.ApplyLayoutModeName(m.sessionLayoutMode)
	} else if m.IsDaemonSession && !session.IsScratchWorkspace(workspace) {
		if m.WorkspaceLayoutMode == nil {
			m.WorkspaceLayoutMode = make(map[int]string)
		}
		m.WorkspaceLayoutMode[workspace] = m.LayoutModeName()
	} else if m.UserConfig != nil && m.UserConfig.Startup.Layout != "" {
		m.ApplyLayoutModeName(m.UserConfig.Startup.Layout)
	}

	if !m.AutoTiling {
		return
	}

	if ratio, exists := m.WorkspaceMasterRatio[workspace]; exists {
		m.MasterRatio = ratio
	} else {
		m.MasterRatio = m.Settings.MasterRatioFraction()
	}

	savedLayouts := m.WorkspaceLayouts[workspace]
	if len(savedLayouts) == 0 {
		return
	}

	for _, saved := range savedLayouts {
		for _, win := range m.Windows {
			if win.ID == saved.WindowID && win.Workspace == workspace {
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
}

// MarkLayoutCustom marks the current workspace as having a custom layout
func (m *OS) MarkLayoutCustom() {
	if m.AutoTiling {
		m.WorkspaceHasCustom[m.CurrentWorkspace] = true
		m.SaveCurrentLayout()
	}
}
