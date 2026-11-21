package app

import (
	"cmp"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// MinimizeWindow minimizes the window at the specified index.
//
// A command popup cannot be minimized. Minimizing puts a pane on the dock as a
// pill the user picks up again later, and a command popup has no later: it
// holds one command and closes when the command exits, which would leave a
// pill for a pane that is gone. Close it instead. A TML popup has a later: its
// surface stays until it is answered or closed, so it goes to the dock as any
// pane does and comes back as a popup.
func (m *OS) MinimizeWindow(i int) {
	if i >= 0 && i < len(m.Windows) && (m.Windows[i].IsPopup && m.Windows[i].Kind != session.WindowKindTML || m.Windows[i].IsScratch) {
		return
	}
	if i >= 0 && i < len(m.Windows) && !m.Windows[i].Minimized {
		window := m.Windows[i]

		window.PreMinimizeX = window.X
		window.PreMinimizeY = window.Y
		window.PreMinimizeWidth = window.Width
		window.PreMinimizeHeight = window.Height

		now := time.Now()
		window.Minimized = true
		if !m.IsDaemonSession {
			window.MinimizedAt = now.UnixNano()
		}

		window.MinimizeHighlightUntil = now.Add(1 * time.Second)

		if i == m.FocusedWindow {
			m.FocusNextVisibleWindow()
		}

		if m.AutoTiling && !window.IsPopup {
			if m.UseScrollingLayout {
				intID := m.GetWindowIntID(window.ID)
				sl := m.GetOrCreateScrollingLayout()
				sl.RemoveWindow(intID)
				sl.EnsureFocusedVisible(m.ScrollingViewWidth())
				m.ScrollingSetPositions()
			} else if m.UseBSPLayout {
				m.RemoveWindowFromBSPTree(window)
				m.ApplyBSPLayout()
			} else {
				m.TileRemainingWindows(i)
			}
		}
	}
}

// RestoreWindow restores a minimized window at the specified index.
//
// The hidden scratch terminal is not restored here. Only the scratch key shows
// it, because showing it also moves it to the current workspace and puts the
// keyboard in it (see ToggleScratch). A "restore all" leaves it hidden.
func (m *OS) RestoreWindow(i int) {
	if i >= 0 && i < len(m.Windows) && m.Windows[i].IsScratch {
		return
	}
	if i >= 0 && i < len(m.Windows) && m.Windows[i].Minimized {
		window := m.Windows[i]
		m.leaveStack(window)

		if m.AutoTiling {
			window.Minimized = false

			if m.UseScrollingLayout && !window.IsPopup {
				intID := m.GetWindowIntID(window.ID)
				sl := m.GetOrCreateScrollingLayout()
				if !sl.HasWindow(intID) {
					sl.AddColumn(intID)
				}
			}

			m.FocusWindow(i)
			m.TileAllWindows()
			return
		}

		if window.PreMinimizeWidth <= 0 || window.PreMinimizeHeight <= 0 {
			window.PreMinimizeX, window.PreMinimizeY, window.PreMinimizeWidth, window.PreMinimizeHeight = m.NewWindowPlacement()
		}

		anim := m.CreateRestoreAnimation(i)
		if anim != nil {
			window.X = anim.StartX
			window.Y = anim.StartY
			window.Width = anim.StartWidth
			window.Height = anim.StartHeight

			m.addAnimation(anim)
		}

		window.Minimized = false

		m.FocusWindow(i)
		m.ChooseMode(WindowManagementMode, ModeCauseClick)
	}
}

// ToggleZoom toggles the focused window between zoomed (fullscreen) and normal state.
// When zoomed, the window fills the entire viewport (minus dock). When unzoomed, it
// returns to its previous size and position. Other windows are hidden while zoomed.
func (m *OS) ToggleZoom() {
	if fw := m.GetFocusedWindow(); fw != nil && fw.IsPopup {
		m.ShowNotification("A popup cannot be zoomed.", "info", m.Settings.NotificationDuration)
		return
	}
	m.settleSizes(func() { m.toggleZoom() })
}

func (m *OS) toggleZoom() {
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}

	m.requireRealLayout()

	m.CancelSnapAnimation(fw)

	if fw.Zoomed {
		camera := m.zoomUsesLayout(fw)
		fw.Zoomed = false
		if camera {
			m.zoomRelayout = true
			m.tileAllWindows()
			m.FlushPTYBuffersAfterResize()
			m.MarkAllDirty()
			return
		}
		m.settleUnzoomedBorder(fw)
		if m.animateToRect(fw, fw.PreZoomX, fw.PreZoomY, fw.PreZoomWidth, fw.PreZoomHeight) {
			if m.AutoTiling {
				m.TileAllWindows()
			}
			m.MarkAllDirty()
			return
		}
		fw.X = fw.PreZoomX
		fw.Y = fw.PreZoomY
		fw.Width = fw.PreZoomWidth
		fw.Height = fw.PreZoomHeight
		fw.InvalidateCache()
		fw.Resize(fw.Width, fw.Height)
		m.FlushPTYBuffersAfterResize()
		if m.AutoTiling {
			m.TileAllWindows()
		}
		m.MarkAllDirty()
	} else {
		retireRetile := m.zoomPane(fw)
		if retireRetile {
			m.tileAllWindows()
		}
		m.FlushPTYBuffersAfterResize()
		m.MarkAllDirty()
	}
}

func (m *OS) zoomedWindow() *terminal.Window {
	for _, w := range m.Windows {
		if w == nil || !w.Zoomed {
			continue
		}
		if w.Workspace != m.CurrentWorkspace || w.Minimized {
			continue
		}
		return w
	}
	return nil
}

func (m *OS) zoomRect() (x, y, w, h int) {
	topMargin := m.PaneTop()
	leftMargin := m.PaneLeft()
	contentWidth := m.PaneWidth()
	zoomWidth := contentWidth
	if !m.Settings.ZoomBorderless && m.Settings.ZoomMaxWidth > 0 && m.Settings.ZoomMaxWidth < contentWidth {
		zoomWidth = m.Settings.ZoomMaxWidth
	}
	return leftMargin + (contentWidth-zoomWidth)/2, topMargin, zoomWidth, m.PaneHeight()
}

func (m *OS) applyZoomRect(w *terminal.Window, deferring bool) {
	m.applyZoomRectAnimated(w, deferring, false)
}

func (m *OS) applyZoomRectAnimated(w *terminal.Window, deferring, animate bool) {
	m.CancelSnapAnimation(w)
	borderChanged := false
	borderless, settle := m.panesBorderless(), !w.IsFloating
	if m.Settings.ZoomBorderless {
		borderless, settle = true, true
	} else if w.IsFloating && w.Tiled {
		borderless, settle = false, true
	}
	if settle && w.Tiled != borderless {
		w.Tiled = borderless
		borderChanged = true
	}
	x, y, width, height := m.zoomRect()
	if !borderChanged && w.X == x && w.Y == y && w.Width == width && w.Height == height {
		return
	}
	if animate && !deferring {
		if anim := m.zoomAnimation(w, x, y, width, height); anim != nil {
			m.addAnimation(anim)
			w.InvalidateCache()
			return
		}
	}
	m.placePaneAt(w, x, y, width, height, deferring)
}

func (m *OS) zoomPane(w *terminal.Window) bool {
	retireRetile := m.retireOtherZooms(w)

	w.PreZoomX = w.X
	w.PreZoomY = w.Y
	w.PreZoomWidth = w.Width
	w.PreZoomHeight = w.Height
	w.Zoomed = true

	if m.zoomUsesLayout(w) {
		m.zoomRelayout = true
		if m.UseScrollingLayout {
			m.ScrollingSetPositions()
			m.RevealFocusedColumn()
		}
		return true
	}
	m.applyZoomRectAnimated(w, false, true)
	return retireRetile
}

func (m *OS) takeZoomRelayout() bool {
	if !m.zoomRelayout {
		return false
	}
	m.zoomRelayout = false
	return true
}

func (m *OS) zoomUsesLayout(w *terminal.Window) bool {
	return m.zoomSize() < 100 &&
		m.AutoTiling && w != nil && !w.IsFloating
}

// ZoomFollowsFocus moves the workspace's zoom onto the pane the focus has just
// landed on, when some other pane on the workspace holds it.
//
// A zoomed workspace shows one pane, and focus used to move underneath it
// regardless: the next-pane key focused the pane after it, the zoomed pane kept
// the box, and the cursor was drawn where the focused pane would have been had
// it been visible. Keys went to a pane nobody could see.
//
// The zoom is the statement that you want one pane and the whole region for it,
// and a focus move is the statement of which pane. They compose by handing the
// box over: the pane that had it slides back to its tile while the pane the
// focus reached slides into the box, both at once, so the frame says what
// happened rather than cutting between two arrangements.
//
// appearance.zoom_follows_focus turns it off, for anyone who would rather the
// zoom stayed on the pane they put it on.
func (m *OS) ZoomFollowsFocus(i int) bool {
	if !m.Settings.ZoomFollowsFocus || i < 0 || i >= len(m.Windows) {
		return false
	}
	w := m.Windows[i]
	if w == nil || w.IsPopup || w.Minimized || w.Workspace != m.CurrentWorkspace {
		return false
	}
	zw := m.zoomedWindow()
	if zw == nil || zw == w {
		return false
	}
	m.settleSizes(func() {
		m.requireRealLayout()
		m.CancelSnapAnimation(w)
		m.handOverZoom(zw, w)
		m.FlushPTYBuffersAfterResize()
		m.MarkAllDirty()
	})
	return true
}

func (m *OS) handOverZoom(from, to *terminal.Window) {
	if m.zoomUsesLayout(to) {
		m.zoomPane(to)
		m.tileAllWindows()
		return
	}

	boxX, boxY, boxW, boxH := from.X, from.Y, from.Width, from.Height

	from.Zoomed = false
	if m.AutoTiling && !from.IsFloating {
		m.tileAllWindows()
	} else if from.PreZoomWidth > 0 && from.PreZoomHeight > 0 {
		from.X, from.Y = from.PreZoomX, from.PreZoomY
		from.Width, from.Height = from.PreZoomWidth, from.PreZoomHeight
		from.Resize(from.PreZoomWidth, from.PreZoomHeight)
	}

	homeX, homeY, homeW, homeH := from.X, from.Y, from.Width, from.Height
	from.X, from.Y, from.Width, from.Height = boxX, boxY, boxW, boxH
	if !m.animateToRect(from, homeX, homeY, homeW, homeH) {
		from.X, from.Y, from.Width, from.Height = homeX, homeY, homeW, homeH
		from.InvalidateCache()
	}

	m.zoomPane(to)
}

func (m *OS) zoomCoversRegion(w *terminal.Window) bool {
	if w == nil {
		return false
	}
	for _, a := range m.Animations {
		if a != nil && a.Window == w && !a.Complete {
			return false
		}
	}
	return w.X <= m.PaneLeft() && w.Y <= m.PaneTop() &&
		w.Width >= m.PaneWidth() && w.Height >= m.PaneHeight()
}

func (m *OS) zoomAnimation(w *terminal.Window, x, y, width, height int) *ui.Animation {
	dur := m.Settings.MotionLength(config.AspectZoom, config.FastAnimationDuration)
	if dur <= 0 {
		return nil
	}
	return ui.NewSnapAnimation(w, x, y, width, height, dur)
}

func (m *OS) animateToRect(w *terminal.Window, x, y, width, height int) bool {
	if w.X == x && w.Y == y && w.Width == width && w.Height == height {
		return false
	}
	anim := m.zoomAnimation(w, x, y, width, height)
	if anim == nil {
		return false
	}
	m.addAnimation(anim)
	w.InvalidateCache()
	return true
}

func (m *OS) applyZoomState(unzoomed []*terminal.Window) bool {
	retile := false
	for _, w := range unzoomed {
		retile = m.unzoomPane(w) || retile
	}
	if zw := m.zoomedWindow(); zw != nil {
		if m.zoomUsesLayout(zw) {
			return true
		}
		m.applyZoomRect(zw, false)
	}
	return retile
}

func (m *OS) retireOtherZooms(keep *terminal.Window) bool {
	retile := false
	for _, w := range m.Windows {
		if w == nil || w == keep || !w.Zoomed || w.Workspace != keep.Workspace {
			continue
		}
		retile = m.unzoomPane(w) || retile
	}
	return retile
}

func (m *OS) unzoomPane(w *terminal.Window) bool {
	w.Zoomed = false
	m.settleUnzoomedBorder(w)
	if m.AutoTiling && !w.IsFloating {
		return true
	}
	if w.PreZoomWidth <= 0 || w.PreZoomHeight <= 0 {
		return false
	}
	w.X, w.Y = w.PreZoomX, w.PreZoomY
	w.InvalidateCache()
	w.Resize(w.PreZoomWidth, w.PreZoomHeight)
	return false
}

// RestoreMinimizedByIndex restores a minimized window by its minimized index.
func (m *OS) RestoreMinimizedByIndex(index int) {
	if mins := m.MinimizedInOrder(); index >= 0 && index < len(mins) {
		m.RestoreWindow(mins[index])
	}
}

// MinimizedInOrder are the indices of the current workspace's minimized
// windows in the order the dock lists them and restore_N counts them: the
// order they were minimized in, oldest first. The daemon stamps that time in
// session state (WindowState.MinimizedAt), so every client of a session has
// one order; a standalone client stamps it itself. Windows with the same
// stamp keep window order. The hidden scratch terminal is minimized but has
// no entry: the scratch key is the way back to it.
func (m *OS) MinimizedInOrder() []int {
	var out []int
	for i, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && w.Minimized && !w.IsScratch {
			out = append(out, i)
		}
	}
	slices.SortStableFunc(out, func(a, b int) int {
		return cmp.Compare(m.Windows[a].MinimizedAt, m.Windows[b].MinimizedAt)
	})
	return out
}

// FocusNextVisibleWindow focuses the next visible window in the current workspace.
func (m *OS) FocusNextVisibleWindow() {
	for i := range len(m.Windows) {
		if m.Windows[i].Workspace == m.CurrentWorkspace && !m.Windows[i].Minimized {
			m.FocusWindow(i)
			return
		}
	}

	m.FocusedWindow = -1
}

// HasMinimizedWindows returns true if there are any minimized windows.
func (m *OS) HasMinimizedWindows() bool {
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && w.Minimized && !w.IsScratch {
			return true
		}
	}
	return false
}

// ZoomWindowByID focuses the pane with id and sets the zoom of its workspace,
// on or off. It is herdr's pane.zoom, which the daemon routes here with the
// pane named: a toggle sent after a focus could reach this client before the
// focus did, and zoom the pane that had the focus before.
func (m *OS) ZoomWindowByID(id string, on bool) error {
	idx := m.windowIndexByID(id)
	if idx < 0 {
		return errfmt.Errorf("no pane %s to zoom", id)
	}
	if m.Windows[idx].Workspace != m.CurrentWorkspace {
		m.SwitchToWorkspace(m.Windows[idx].Workspace)
	}
	target := m.Windows[idx]
	if z := m.zoomedWindow(); z != nil && z != target && !on {
		if i := m.windowIndex(z); i >= 0 {
			m.FocusWindow(i)
		}
		m.ToggleZoom()
	}
	if i := m.windowIndex(target); i >= 0 {
		m.FocusWindow(i)
	}
	if target.Zoomed != on {
		m.ToggleZoom()
	}
	if target.Zoomed != on {
		state := "off"
		if on {
			state = "on"
		}
		return errfmt.Errorf("the pane could not be zoomed %s", state)
	}
	return nil
}

func (m *OS) zoomSize() int {
	if m.Settings.ZoomBorderless || m.FullZoom {
		return 100
	}
	return m.Settings.GetZoomSize()
}

func (m *OS) settleUnzoomedBorder(w *terminal.Window) {
	want := !w.IsFloating && m.panesBorderless()
	if w.Tiled == want {
		return
	}
	w.Tiled = want
	w.InvalidateCache()
}
