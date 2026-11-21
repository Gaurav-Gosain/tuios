package app

import (
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func (m *OS) applyScrollingGeometry(sl *layout.ScrollingLayout) {
	sl.Gap = m.separatorGap()
	sl.DefaultWidth = m.ScrollColumnWidthFraction()
	sl.MaxProportion = float64(m.Settings.GetScrollColumnMax()) / 100
}

// GetOrCreateScrollingLayout returns the scrolling layout for the current workspace.
func (m *OS) GetOrCreateScrollingLayout() *layout.ScrollingLayout {
	if m.WorkspaceScrollingLayouts == nil {
		m.WorkspaceScrollingLayouts = make(map[int]*layout.ScrollingLayout)
	}
	sl, ok := m.WorkspaceScrollingLayouts[m.CurrentWorkspace]
	created := !ok || sl == nil
	if created {
		sl = layout.NewScrollingLayout()
		m.WorkspaceScrollingLayouts[m.CurrentWorkspace] = sl

		if pending, ok := m.pendingScrollColumns[m.CurrentWorkspace]; ok {
			delete(m.pendingScrollColumns, m.CurrentWorkspace)
			sl.Columns = m.scrollColumnsFromState(m.CurrentWorkspace, pending, -1)
		} else {
			for _, w := range m.Windows {
				if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
					intID := m.GetWindowIntID(w.ID)
					sl.AddColumn(intID)
				}
			}
		}

		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			fw := m.Windows[m.FocusedWindow]
			if fw.Workspace == m.CurrentWorkspace && !fw.IsFloating {
				intID := m.GetWindowIntID(fw.ID)
				sl.FocusColumnContaining(intID)
			}
		}
	}
	m.applyScrollingGeometry(sl)
	sl.PresetWidths = layout.DefaultPresetWidths
	if len(m.Settings.ScrollColumnPresets) > 0 {
		sl.PresetWidths = m.Settings.ScrollColumnPresets
	}
	sl.ZoomedCol, sl.ZoomProportion = -1, 0
	if zw := m.zoomedWindow(); zw != nil && m.zoomSize() < 100 {
		if i := sl.ColumnContaining(m.GetWindowIntID(zw.ID)); i >= 0 {
			sl.ZoomedCol = i
			sl.ZoomProportion = float64(m.zoomSize()) / 100
		}
	}
	if created {
		sl.EnsureFocusedVisible(m.ScrollingViewWidth())
	}
	return sl
}

// ScrollingViewWidth is the horizontal space the scrolling strip works in: the
// content width beside any reserved sidebar band. Every viewport computation
// (clamping, centering, resolving column widths) runs against this width, and
// the computed strip positions are then shifted right by GetLeftMargin, so the
// strip scrolls within the content box instead of underneath the sidebar.
func (m *OS) ScrollingViewWidth() int {
	return m.PaneWidth()
}

// ScrollingSetPositions applies the scrolling layout positions and dimensions,
// sliding windows to their new positions.
func (m *OS) ScrollingSetPositions() {
	m.scrollingSetPositionsAnimated(true)
}

func (m *OS) scrollingSetPositionsInstant() {
	m.scrollingSetPositionsAnimated(false)
}

func (m *OS) scrollDuration() time.Duration {
	if m.Settings.Aspects[config.AspectScroll].Off {
		return 0
	}
	if d := m.Settings.MotionLength(config.AspectScroll, config.DefaultAnimationDuration); d > 0 {
		return d
	}
	return 150 * time.Millisecond
}

func (m *OS) scrollingSetPositionsAnimated(animate bool) {
	sl := m.GetOrCreateScrollingLayout()
	viewW := m.ScrollingViewWidth()
	leftMargin := m.PaneLeft()

	sl.ClampViewport(viewW)

	layouts := sl.ComputePositions(viewW, m.PaneHeight(), m.PaneTop())

	dur := m.scrollDuration()
	if m.layoutInstant {
		dur = 0
	}

	deferring := m.resizeDeferralActive()
	borderless := m.panesBorderless()

	if animate && m.Settings.MotionLength(config.AspectScroll, config.DefaultAnimationDuration) <= 0 && m.scrollingPassResizes(layouts) {
		animate = false
	}

	for windowIntID, rect := range layouts {
		rect.X += leftMargin
		win := m.GetWindowByIntID(windowIntID)
		if win == nil || win.Workspace != m.CurrentWorkspace || win.Minimized || win.IsFloating {
			continue
		}
		if win.Zoomed && !m.zoomUsesLayout(win) {
			continue
		}
		if m.noteDragSlot(win, rect) {
			continue
		}
		borderChanged := win.Tiled != borderless
		if borderChanged {
			win.Tiled = borderless
			win.InvalidateCache()
		}
		moved := win.X != rect.X || win.Y != rect.Y
		resized := win.Width != rect.W || win.Height != rect.H
		alreadyPlaced := win.X != 0 || win.Y != 0 || win.Width != 0
		slide := animate && alreadyPlaced && !deferring && dur > 0 && (moved || resized)
		if borderChanged || (!slide && resized) {
			m.resizePane(win, rect.W, rect.H, deferring)
		}

		if animate && m.windowHasAnimationTo(win, rect.X, rect.Y, rect.W, rect.H) {
			continue
		}

		if slide || (animate && alreadyPlaced && moved) {
			if anim := m.animateWindow(win, rect.X, rect.Y, rect.W, rect.H, dur); anim != nil {
				continue
			}
		}
		if slide {
			m.resizePane(win, rect.W, rect.H, deferring)
		}

		m.CancelSnapAnimation(win)
		win.X = rect.X
		win.Y = rect.Y
		win.Width = rect.W
		win.Height = rect.H
		win.MarkPositionDirty()
		win.InvalidateCache()
	}
}

func (m *OS) scrollingPassResizes(layouts map[int]layout.Rect) bool {
	for windowIntID, rect := range layouts {
		win := m.GetWindowByIntID(windowIntID)
		if win == nil || win.Workspace != m.CurrentWorkspace || win.Minimized || win.IsFloating {
			continue
		}
		if win.Zoomed && !m.zoomUsesLayout(win) {
			continue
		}
		if win.Width != rect.W || win.Height != rect.H {
			return true
		}
	}
	return false
}

func (m *OS) windowHasAnimationTo(win *terminal.Window, x, y, w, h int) bool {
	for _, anim := range m.Animations {
		if anim.Window == win && !anim.Complete &&
			anim.EndX == x && anim.EndY == y &&
			anim.EndWidth == w && anim.EndHeight == h {
			return true
		}
	}
	return false
}

// ScrollingFocusLeft navigates to the column to the left.
func (m *OS) ScrollingFocusLeft() {
	sl := m.GetOrCreateScrollingLayout()
	sl.FocusLeft()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.scrollingSyncFocusToOS()
	m.ScrollingSetPositions()
}

// ScrollingFocusRight navigates to the column to the right.
func (m *OS) ScrollingFocusRight() {
	sl := m.GetOrCreateScrollingLayout()
	sl.FocusRight()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.scrollingSyncFocusToOS()
	m.ScrollingSetPositions()
}

// ScrollingMoveColumnLeft moves the focused column left.
func (m *OS) ScrollingMoveColumnLeft() {
	sl := m.GetOrCreateScrollingLayout()
	sl.MoveColumnLeft()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.ScrollingSetPositions()
}

// ScrollingMoveColumnRight moves the focused column right.
func (m *OS) ScrollingMoveColumnRight() {
	sl := m.GetOrCreateScrollingLayout()
	sl.MoveColumnRight()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.ScrollingSetPositions()
}

// ScrollingCycleWidth cycles the focused column through preset widths.
func (m *OS) ScrollingCycleWidth() {
	sl := m.GetOrCreateScrollingLayout()
	sl.CycleWidth()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.ScrollingSetPositions()
}

// ScrollingMaximizeColumn widens the focused column to its widest allowed
// width. The widest allowed width is set by scroll_column_max: 90% by default,
// 100% when the user has raised it. It is the one-key path to the 1.0 preset
// the cycle width chain walks past 0.9 to reach.
func (m *OS) ScrollingMaximizeColumn() {
	sl := m.GetOrCreateScrollingLayout()
	sl.MaximizeColumn()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.ScrollingSetPositions()
}

// ScrollingConsumeWindow absorbs the next column's window into the focused
// column. Focus follows the window that moved, so the keyboard is where the
// user is looking; without the sync the OS focus stayed on a pane in a column
// that may no longer exist.
func (m *OS) ScrollingConsumeWindow() {
	sl := m.GetOrCreateScrollingLayout()
	sl.ConsumeWindow()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.scrollingSyncFocusToOS()
	m.ScrollingSetPositions()
}

// ScrollingExpelWindow pushes the focused window out into its own column, and
// follows it there.
func (m *OS) ScrollingExpelWindow() {
	sl := m.GetOrCreateScrollingLayout()
	sl.ExpelWindow()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.scrollingSyncFocusToOS()
	m.ScrollingSetPositions()
}

// ScrollingScrollViewport scrolls the viewport manually (mouse wheel).
// Uses instant positioning so scrolling feels direct and responsive.
func (m *OS) ScrollingScrollViewport(delta int) {
	m.ScrollingScrollViewportCells(delta * max(m.Settings.NiriScrollCells, config.NiriScrollCellsMin))
}

// ScrollingScrollViewportCells moves the strip by cells, to the right when
// positive. A finger on tuios-web moves it this way, by the cells it crossed.
func (m *OS) ScrollingScrollViewportCells(cells int) {
	sl := m.GetOrCreateScrollingLayout()
	viewW := m.ScrollingViewWidth()
	m.CompleteAllAnimations()
	sl.ViewportX += cells
	sl.ClampViewport(viewW)
	m.scrollingSetPositionsInstant()
}

// ScrollingOnFocusChange points the strip at the focused window and brings its
// column on screen if none of it is there. It moves no pane's size.
//
// It runs on every focus change that is not the user walking the strip: a
// click, a workspace switch restoring the workspace's own focus, a focus the
// daemon moved. Those all get the least-scroll rule, EnsureFocusedVisible: a
// column the user can already see does not need the strip to move under them.
// ScrollToFocusedColumn is the other rule and it belongs to the eight keyboard
// paths above, where the user asked to be taken to a column and wants all of
// it. Used here, it would scroll the strip to the focused column on any of the
// three events above even when that column never left the screen. A switch
// away from a workspace and back restores the workspace's saved focus, so a
// round trip would throw away wherever the user had scrolled that
// workspace's strip to. The offset is per workspace and no client and no sync
// loses it.
//
// Still open, and deliberately not decided here: a strip parked so that the
// focused column is entirely off screen is revealed on the way back, because
// every retile reveals it (tileAllWindows, a resize, a peer sync) and this is
// not the place to change that rule.
func (m *OS) ScrollingOnFocusChange() {
	sl := m.GetOrCreateScrollingLayout()
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}
	intID := m.GetWindowIntID(fw.ID)
	if !sl.FocusColumnContaining(intID) {
		sl.AddColumn(intID)
		sl.FocusColumnContaining(intID)
	}

	sl.EnsureFocusedVisible(m.ScrollingViewWidth())
	m.ScrollingSetPositions()
}

// FocusWindowFromClick focuses a pane the user pressed on, and arms the
// scrolling layout to bring its whole column on screen when the button comes up.
//
// Focus moves for several reasons and they are not the same statement. A
// workspace switch restoring its saved focus, or a focus the daemon moved, says
// nothing about where the viewport should be, and revealing on those would
// throw away wherever the user had scrolled that workspace's strip. That is
// why every other focus change goes to the least-scroll rule. A click is the
// other kind: a column half off the edge that you deliberately clicked is one
// you picked to work in.
//
// It waits for the release, and that is the whole trick. Revealing on the press
// moves the pane out from under the pointer, so a press that turns out to be the
// start of a drag, a title grab, a resize, a selection, measures every later
// coordinate against where the pane was before it moved, and a title drop
// would land a pane twelve cells off. By the release the gesture is over and it
// is safe, and a gesture that moved is not a click so it does not reveal at all.
// appearance.niri_click_reveals turns it off, for anyone who would rather the
// strip stayed exactly where they left it.
func (m *OS) FocusWindowFromClick(i, x, y int) *OS {
	out := m.FocusWindow(i)
	m.ArmClickReveal(x, y)
	return out
}

// RevealFocusedColumn brings the focused column fully on screen now, with no
// press and release to wait for.
//
// It is the rule for asking to be taken to a pane: picking one off the rail,
// or a keyboard command that walks to the next one. Both are the plainest
// possible statement that you want that pane, there is no gesture to finish,
// and nothing under the pointer that could be moved out from under it, which
// is the only thing the click path has to wait for.
//
// The other rule is EnsureFocusedVisible, in ScrollingOnFocusChange: a focus
// that moved for a reason the user did not ask for, such as a workspace
// switch restoring its own focus, must not throw away where they had scrolled
// to. See the comment there.
//
// It is not gated on appearance.niri_click_reveals. That setting is about the
// pointer, and it is checked by the two callers that are about the pointer. A
// keyboard command that says "take me to the next pane" and then leaves half
// of it off the edge has not done what was asked, whatever the pointer is set
// to do.
func (m *OS) RevealFocusedColumn() {
	if !m.AutoTiling || !m.UseScrollingLayout {
		return
	}
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}
	sl := m.GetOrCreateScrollingLayout()
	if sl.FocusColumnContaining(m.GetWindowIntID(fw.ID)) {
		sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
		m.ScrollingSetPositions()
	}
}

// RevealHoveredColumn brings the focused column fully on screen after
// focus-follows-mouse moved the focus onto it.
//
// Hovering a column with that setting on is the same statement clicking one is:
// it is how you pick the pane to work in, and there is no other gesture to
// make. Focus goes through the least-scroll rule, which by design leaves a
// column that is already partly visible where it is, so without this the pane
// you had just focused could be the one you cannot see.
//
// Unlike the click path there is nothing to wait for. A press might turn out to
// be a drag, which is why that one reveals on the release; a motion event is
// already over by the time it arrives. Scrolling does move the column out from
// under the pointer, and the pointer may then be over a different pane, but
// nothing acts on that until the user moves the mouse again, and when they do
// they are pointing at what they are pointing at.
//
// appearance.niri_hover_reveals turns it off.
func (m *OS) RevealHoveredColumn() {
	if !m.Settings.NiriHoverReveals || !m.Settings.FocusFollowsMouse {
		return
	}
	m.RevealFocusedColumn()
}

// ArmClickReveal remembers where the pointer was when a press focused a pane.
func (m *OS) ArmClickReveal(x, y int) {
	if !m.Settings.NiriClickReveals || !m.AutoTiling || !m.UseScrollingLayout {
		return
	}
	m.clickReveal.armed = true
	m.clickReveal.x, m.clickReveal.y = x, y
}

// ReleaseClickReveal brings the focused column fully on screen if the press that
// armed it turned out to be a click rather than a drag.
//
// The pointer not having moved is the test. A gesture that moved is a drag
// whatever it was dragging, and a drag has already been laid out against the
// strip where it started.
func (m *OS) ReleaseClickReveal(x, y int) {
	if !m.clickReveal.armed {
		return
	}
	armedX, armedY := m.clickReveal.x, m.clickReveal.y
	m.clickReveal.armed = false
	if x != armedX || y != armedY {
		return
	}
	if !m.Settings.NiriClickReveals || !m.AutoTiling || !m.UseScrollingLayout {
		return
	}
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}
	sl := m.GetOrCreateScrollingLayout()
	if sl.FocusColumnContaining(m.GetWindowIntID(fw.ID)) {
		sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
		m.ScrollingSetPositions()
	}
}

// ScrollingOnWindowAdded adds a new window to the scrolling layout.
// Only adds the column. FocusWindow handles viewport and positioning.
func (m *OS) ScrollingOnWindowAdded(w *terminal.Window) {
	sl := m.GetOrCreateScrollingLayout()
	intID := m.GetWindowIntID(w.ID)
	if sl.HasWindow(intID) {
		m.LogInfo("[SCROLL-ADD] ScrollingOnWindowAdded: window=%s intID=%d already in layout, skipping", shortID(w.ID), intID)
		return
	}
	m.LogInfo("[SCROLL-ADD] ScrollingOnWindowAdded: window=%s intID=%d", shortID(w.ID), intID)
	sl.AddColumn(intID)
}

// ScrollingOnWindowRemoved removes a window and focuses the neighbor.
func (m *OS) ScrollingOnWindowRemoved(windowIntID int) {
	sl := m.GetOrCreateScrollingLayout()
	sl.RemoveWindow(windowIntID)
	if sl.WindowCount() > 0 {
		sl.EnsureFocusedVisible(m.ScrollingViewWidth())
		m.scrollingSyncFocusToOS()
		m.ScrollingSetPositions()
	}
}

func (m *OS) scrollingLayoutStale() bool {
	sl := m.WorkspaceScrollingLayouts[m.CurrentWorkspace]
	if sl == nil || len(sl.Columns) == 0 {
		return false
	}
	want := sl.ComputePositions(m.ScrollingViewWidth(), m.PaneHeight(), m.PaneTop())
	leftMargin := m.PaneLeft()
	for _, w := range m.Windows {
		if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		rect, ok := want[m.GetWindowIntID(w.ID)]
		if !ok {
			return true
		}
		rect.X += leftMargin
		if m.windowHasAnimationTo(w, rect.X, rect.Y, rect.W, rect.H) {
			continue
		}
		wx, wy, ww, wh := m.dragSlotOf(w)
		if wx != rect.X || wy != rect.Y || ww != rect.W || wh != rect.H {
			return true
		}
	}
	return false
}

// ScrollStripState is this client's strip on the workspace it is showing, or
// nil when it has none to report. It is read for the state push, so it never
// builds a strip as a side effect: a client that has never laid this workspace
// out has no offset to send and says so, rather than inventing a home position
// and scrolling everyone else to it.
func (m *OS) ScrollStripState() *session.ScrollStripState {
	if !m.UseScrollingLayout {
		return nil
	}
	sl := m.WorkspaceScrollingLayouts[m.CurrentWorkspace]
	if sl == nil {
		return nil
	}
	return &session.ScrollStripState{ViewportX: sl.ViewportX}
}

func (m *OS) adoptScrollStrip(strip *session.ScrollStripState, focusChanged, focusOnNew bool) {
	if !m.AutoTiling || !m.UseScrollingLayout {
		return
	}
	if strip == nil && !focusChanged {
		return
	}
	sl := m.GetOrCreateScrollingLayout()
	moved := false
	if strip != nil && sl.ViewportX != strip.ViewportX {
		sl.ViewportX = strip.ViewportX
		moved = true
	}
	if focusChanged {
		if fw := m.GetFocusedWindow(); fw != nil && !fw.IsFloating && !fw.Minimized &&
			fw.Workspace == m.CurrentWorkspace {
			was := sl.FocusedCol
			if sl.FocusColumnContaining(m.GetWindowIntID(fw.ID)) && sl.FocusedCol != was {
				moved = true
			}
		}
		before := sl.ViewportX
		if focusOnNew {
			sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
		} else {
			sl.EnsureFocusedVisible(m.ScrollingViewWidth())
		}
		moved = moved || sl.ViewportX != before
	}
	if moved {
		m.ScrollingSetPositions()
	}
}

// ScrollingResizeColumnVisual sets the width of the column holding win while a
// pointer drags it, and lays the strip out again on screen only: the panes
// take their new rectangles now and their guests hear the size on release,
// through PendingResizes. keepRight holds the column's right edge still, for a
// drag on its left side, by scrolling the strip by the width the column gains.
//
// The width is held between the smallest pane and the strip's ceiling, the
// same one the keyboard resize reads.
//
// The viewport is not clamped to the strip's right end until the release.
// With the strip scrolled to that end, a column that shrinks leaves ground
// past the last column, and the clamp would pull the strip left under the
// pointer on every motion event: the column's left edge would move away from
// the pointer, and a width measured from it would run away. Only a viewport
// before the strip's start is held back here. The release lays the strip out
// again through ScrollingSetPositions, which clamps it fully.
func (m *OS) ScrollingResizeColumnVisual(win *terminal.Window, width int, keepRight bool) {
	sl := m.GetOrCreateScrollingLayout()
	viewW := m.ScrollingViewWidth()
	width = max(min(width, sl.MaxColumnWidth(viewW)), config.DefaultWindowWidth)
	col := sl.ColumnContaining(m.GetWindowIntID(win.ID))
	if col < 0 {
		return
	}
	oldWidth := sl.ResolveColumnWidth(col, viewW)
	sl.Columns[col].FixedWidth = width
	sl.Columns[col].Proportion = 0
	if keepRight && oldWidth > 0 {
		sl.ViewportX += width - oldWidth
	}
	sl.ViewportX = max(sl.ViewportX, 0)
	stripLeft := m.PaneLeft()
	for winID, rect := range sl.ComputePositions(viewW, m.PaneHeight(), m.PaneTop()) {
		w := m.GetWindowByIntID(winID)
		if w == nil {
			continue
		}
		w.X = stripLeft + rect.X
		w.Y = rect.Y
		w.Width = rect.W
		w.MarkPositionDirty()
		w.InvalidateCache()
	}
	for _, wid := range sl.Columns[col].WindowIDs {
		if w := m.GetWindowByIntID(wid); w != nil {
			m.PendingResizes[w.ID] = [2]int{width, w.Height}
		}
	}
}

func (m *OS) scrollingResizeColumn(delta int) {
	sl := m.GetOrCreateScrollingLayout()
	if sl.FocusedCol < 0 || sl.FocusedCol >= len(sl.Columns) {
		return
	}
	col := &sl.Columns[sl.FocusedCol]
	viewW := m.ScrollingViewWidth()
	maxWidth := sl.MaxColumnWidth(viewW)
	currentWidth := sl.ResolveColumnWidth(sl.FocusedCol, viewW)
	newWidth := max(min(currentWidth+delta, maxWidth), 20)
	col.FixedWidth = newWidth
	col.Proportion = 0
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.scrollingSetPositionsInstant()
}
func (m *OS) scrollingSyncFocusToOS() {
	sl := m.GetOrCreateScrollingLayout()
	focusedWinID := sl.GetFocusedWindowID()
	if focusedWinID < 0 {
		return
	}
	win := m.GetWindowByIntID(focusedWinID)
	if win == nil {
		return
	}
	m.scrollingFocusSyncing = true
	defer func() { m.scrollingFocusSyncing = false }()
	for i, w := range m.Windows {
		if w == win {
			m.FocusWindow(i)
			return
		}
	}
}

func (m *OS) scrollColumnsState() map[int][]session.SerializedScrollColumn {
	out := make(map[int][]session.SerializedScrollColumn)
	for ws, sl := range m.WorkspaceScrollingLayouts {
		if sl == nil {
			continue
		}
		if cols := m.scrollColumnsToState(sl.Columns); len(cols) > 0 {
			out[ws] = cols
		}
	}
	for ws, cols := range m.pendingScrollColumns {
		if _, built := out[ws]; !built && len(cols) > 0 {
			out[ws] = cols
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (m *OS) scrollColumnsToState(cols []layout.ScrollColumn) []session.SerializedScrollColumn {
	out := make([]session.SerializedScrollColumn, 0, len(cols))
	for _, c := range cols {
		ids := make([]string, 0, len(c.WindowIDs))
		active := 0
		for i, intID := range c.WindowIDs {
			w := m.GetWindowByIntID(intID)
			if w == nil {
				continue
			}
			if i == c.Active {
				active = len(ids)
			}
			ids = append(ids, w.ID)
		}
		if len(ids) == 0 {
			continue
		}
		out = append(out, session.SerializedScrollColumn{
			Windows:    ids,
			Proportion: c.Proportion,
			FixedWidth: c.FixedWidth,
			Active:     active,
		})
	}
	return out
}

func (m *OS) scrollColumnsFromState(ws int, cols []session.SerializedScrollColumn, after int) []layout.ScrollColumn {
	tileable := func(w *terminal.Window) bool {
		return w != nil && w.Workspace == ws && !w.Minimized && !w.IsFloating
	}
	placed := make(map[int]bool)
	out := make([]layout.ScrollColumn, 0, len(cols))
	for _, c := range cols {
		ids := make([]int, 0, len(c.Windows))
		active := 0
		for i, windowID := range c.Windows {
			if !tileable(m.windowByID(windowID)) {
				continue
			}
			intID := m.GetWindowIntID(windowID)
			if placed[intID] {
				continue
			}
			placed[intID] = true
			if i == c.Active {
				active = len(ids)
			}
			ids = append(ids, intID)
		}
		if len(ids) == 0 {
			continue
		}
		out = append(out, layout.ScrollColumn{
			WindowIDs:  ids,
			Proportion: c.Proportion,
			FixedWidth: c.FixedWidth,
			Active:     active,
		})
	}
	at := len(out)
	for i, c := range out {
		if slices.Contains(c.WindowIDs, after) {
			at = i + 1
			break
		}
	}
	for _, w := range m.Windows {
		if !tileable(w) {
			continue
		}
		if intID := m.GetWindowIntID(w.ID); !placed[intID] {
			placed[intID] = true
			out = slices.Insert(out, at, layout.ScrollColumn{WindowIDs: []int{intID}})
			at++
		}
	}
	return out
}

func (m *OS) adoptScrollColumns(cols map[int][]session.SerializedScrollColumn) {
	if len(cols) == 0 {
		return
	}
	if m.pendingScrollColumns == nil {
		m.pendingScrollColumns = make(map[int][]session.SerializedScrollColumn)
	}
	for ws, c := range cols {
		sl := m.WorkspaceScrollingLayouts[ws]
		if sl == nil {
			m.pendingScrollColumns[ws] = c
			continue
		}
		for _, col := range slices.Clone(sl.Columns) {
			for _, intID := range col.WindowIDs {
				w := m.GetWindowByIntID(intID)
				if w == nil || w.Workspace != ws || w.Minimized || w.IsFloating {
					sl.RemoveWindow(intID)
				}
			}
		}
		focused := -1
		if sl.FocusedCol >= 0 && sl.FocusedCol < len(sl.Columns) {
			if col := sl.Columns[sl.FocusedCol]; col.Active >= 0 && col.Active < len(col.WindowIDs) {
				focused = col.WindowIDs[col.Active]
			}
		}
		sl.Columns = m.scrollColumnsFromState(ws, c, focused)
		if focused < 0 || !sl.FocusColumnContaining(focused) {
			sl.FocusedCol = clampInt(sl.FocusedCol, 0, max(len(sl.Columns)-1, 0))
		}
	}
}
