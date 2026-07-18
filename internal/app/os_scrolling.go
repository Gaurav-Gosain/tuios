package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// GetOrCreateScrollingLayout returns the scrolling layout for the current workspace.
func (m *OS) GetOrCreateScrollingLayout() *layout.ScrollingLayout {
	if m.WorkspaceScrollingLayouts == nil {
		m.WorkspaceScrollingLayouts = make(map[int]*layout.ScrollingLayout)
	}
	sl, ok := m.WorkspaceScrollingLayouts[m.CurrentWorkspace]
	if !ok || sl == nil {
		sl = layout.NewScrollingLayout()
		m.WorkspaceScrollingLayouts[m.CurrentWorkspace] = sl

		// Populate with existing visible windows
		for _, w := range m.Windows {
			if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
				intID := m.getWindowIntID(w.ID)
				sl.AddColumn(intID)
			}
		}

		// Sync FocusedCol with the OS focused window so the viewport
		// shows the correct column instead of always the last one.
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			fw := m.Windows[m.FocusedWindow]
			if fw.Workspace == m.CurrentWorkspace && !fw.IsFloating {
				intID := m.getWindowIntID(fw.ID)
				sl.FocusColumnContaining(intID)
			}
		}
	}
	return sl
}

// scrollingSetPositions applies the scrolling layout positions and dimensions.
// When animate is true, windows slide to their new positions.
func (m *OS) scrollingSetPositions() {
	m.scrollingSetPositionsAnimated(true)
}

// scrollingSetPositionsInstant applies positions without animation (mouse wheel).
func (m *OS) scrollingSetPositionsInstant() {
	m.scrollingSetPositionsAnimated(false)
}
func (m *OS) scrollingSetPositionsAnimated(animate bool) {
	sl := m.GetOrCreateScrollingLayout()
	screenW := m.GetRenderWidth()

	sl.ClampViewport(screenW)

	layouts := sl.ComputePositions(screenW, m.GetUsableHeight(), m.GetTopMargin())

	// Scrolling layout transitions always animate (even with --no-animations)
	// because the viewport shift is disorienting without the slide.
	dur := 150 * time.Millisecond
	if config.GetAnimationDuration() > 0 {
		dur = config.GetAnimationDuration()
	}

	for windowIntID, rect := range layouts {
		win := m.getWindowByIntID(windowIntID)
		if win == nil || win.Workspace != m.CurrentWorkspace || win.Minimized || win.IsFloating {
			continue
		}
		if win.Width != rect.W || win.Height != rect.H {
			win.Resize(rect.W, rect.H)
		}

		// If this window already has an in-flight animation heading to
		// the same target, don't touch it. TileAllWindows and other
		// callers re-run scrollingSetPositions frequently; without this
		// guard each call would cancel + recreate the animation from the
		// current intermediate position, making it stutter.
		if m.windowHasAnimationTo(win, rect.X, rect.Y, rect.W, rect.H) {
			continue
		}

		alreadyPlaced := win.X != 0 || win.Y != 0 || win.Width != 0
		if animate && alreadyPlaced && (win.X != rect.X || win.Y != rect.Y) {
			if !m.windowHasAnimationTo(win, rect.X, rect.Y, rect.W, rect.H) {
				m.CancelAnimationsForWindow(win)
				anim := ui.NewSnapAnimation(win, rect.X, rect.Y, rect.W, rect.H, dur)
				if anim != nil {
					m.Animations = append(m.Animations, anim)
					continue
				}
			} else {
				continue
			}
		}

		win.X = rect.X
		win.Y = rect.Y
		win.Width = rect.W
		win.Height = rect.H
		win.Tiled = false
		win.MarkPositionDirty()
		win.InvalidateCache()
	}
}

// windowHasAnimationTo checks if a window has an active animation
// heading to the exact target position. Used to avoid canceling
// in-flight animations when scrollingSetPositions is called repeatedly.
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
	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSyncFocusToOS()
	m.scrollingSetPositions()
}

// ScrollingFocusRight navigates to the column to the right.
func (m *OS) ScrollingFocusRight() {
	sl := m.GetOrCreateScrollingLayout()
	sl.FocusRight()
	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSyncFocusToOS()
	m.scrollingSetPositions()
}

// ScrollingMoveColumnLeft moves the focused column left.
func (m *OS) ScrollingMoveColumnLeft() {
	sl := m.GetOrCreateScrollingLayout()
	sl.MoveColumnLeft()
	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSetPositions()
}

// ScrollingMoveColumnRight moves the focused column right.
func (m *OS) ScrollingMoveColumnRight() {
	sl := m.GetOrCreateScrollingLayout()
	sl.MoveColumnRight()
	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSetPositions()
}

// ScrollingCycleWidth cycles the focused column through preset widths.
func (m *OS) ScrollingCycleWidth() {
	sl := m.GetOrCreateScrollingLayout()
	sl.CycleWidth()
	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSetPositions()
}

// ScrollingConsumeWindow absorbs the next column's window into the focused column.
func (m *OS) ScrollingConsumeWindow() {
	sl := m.GetOrCreateScrollingLayout()
	sl.ConsumeWindow()
	m.scrollingSetPositions()
}

// ScrollingExpelWindow ejects the last stacked window into its own column.
func (m *OS) ScrollingExpelWindow() {
	sl := m.GetOrCreateScrollingLayout()
	sl.ExpelWindow()
	m.scrollingSetPositions()
}

// ScrollingScrollViewport scrolls the viewport manually (mouse wheel).
// Uses instant positioning so scrolling feels direct and responsive.
func (m *OS) ScrollingScrollViewport(delta int) {
	sl := m.GetOrCreateScrollingLayout()
	screenW := m.GetRenderWidth()
	// Cancel any in-flight slide animations so the wheel feels direct
	m.CompleteAllAnimations()
	sl.ViewportX += delta * (screenW / 5)
	sl.ClampViewport(screenW)
	m.scrollingSetPositionsInstant()
}

// ScrollingOnFocusChange is called when the OS focus changes (click, etc.)
// to sync the scrolling layout and scroll the focused column into view.
// Only updates viewport/positions, never changes dimensions.
func (m *OS) ScrollingOnFocusChange() {
	sl := m.GetOrCreateScrollingLayout()
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}
	intID := m.getWindowIntID(fw.ID)
	if !sl.FocusColumnContaining(intID) {
		sl.AddColumn(intID)
		sl.FocusColumnContaining(intID)
	}

	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSetPositions()
}

// ScrollingOnWindowAdded adds a new window to the scrolling layout.
// Only adds the column  - FocusWindow handles viewport and positioning.
func (m *OS) ScrollingOnWindowAdded(w *terminal.Window) {
	sl := m.GetOrCreateScrollingLayout()
	intID := m.getWindowIntID(w.ID)
	// GetOrCreateScrollingLayout populates from m.Windows on first call.
	// If the window was already appended to m.Windows before this call,
	// the layout already has it. Don't add a duplicate.
	if sl.HasWindow(intID) {
		m.LogInfo("[SCROLL-ADD] ScrollingOnWindowAdded: window=%s intID=%d already in layout, skipping", w.ID[:8], intID)
		return
	}
	m.LogInfo("[SCROLL-ADD] ScrollingOnWindowAdded: window=%s intID=%d", w.ID[:8], intID)
	sl.AddColumn(intID)
}

// ScrollingOnWindowRemoved removes a window and focuses the neighbor.
func (m *OS) ScrollingOnWindowRemoved(windowIntID int) {
	sl := m.GetOrCreateScrollingLayout()
	sl.RemoveWindow(windowIntID)
	if sl.WindowCount() > 0 {
		sl.EnsureFocusedVisible(m.GetRenderWidth())
		m.scrollingSyncFocusToOS()
		m.scrollingSetPositions()
	}
}

// scrollingSyncFocusToOS sets the OS focused window to match the scrolling layout's focus.
// GetWindowIntID returns the integer BSP ID for a window by its string ID.
func (m *OS) GetWindowIntID(windowID string) int {
	return m.getWindowIntID(windowID)
}

// ScrollingSetPositions applies scrolling layout positions (public wrapper).
func (m *OS) ScrollingSetPositions() {
	m.scrollingSetPositions()
}

// GetWindowByIntID returns the window with the given integer BSP ID.
func (m *OS) GetWindowByIntID(intID int) *terminal.Window {
	return m.getWindowByIntID(intID)
}

// scrollingResizeColumn changes the focused column's width by delta pixels.
func (m *OS) scrollingResizeColumn(delta int) {
	sl := m.GetOrCreateScrollingLayout()
	if sl.FocusedCol < 0 || sl.FocusedCol >= len(sl.Columns) {
		return
	}
	col := &sl.Columns[sl.FocusedCol]
	// Get current width and apply delta, capped at 90% of screen
	screenW := m.GetRenderWidth()
	maxWidth := screenW * 9 / 10
	currentWidth := sl.ResolveColumnWidth(sl.FocusedCol, screenW)
	newWidth := max(min(currentWidth+delta, maxWidth), 20)
	col.FixedWidth = newWidth
	col.Proportion = 0 // FixedWidth takes priority
	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSetPositionsInstant() // resize must be instant, not animated
}
func (m *OS) scrollingSyncFocusToOS() {
	sl := m.GetOrCreateScrollingLayout()
	focusedWinID := sl.GetFocusedWindowID()
	if focusedWinID < 0 {
		return
	}
	win := m.getWindowByIntID(focusedWinID)
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
