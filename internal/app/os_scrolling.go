package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
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
	// The two geometry inputs the session settles are pushed in on every access
	// rather than stored once at creation. Both can move under a live layout -
	// a peer client's setting arriving by state sync, or this client's own
	// settings row - and a strip built before the change would otherwise keep
	// laying its columns out with the old arithmetic until something happened
	// to rebuild it. Every caller reaches the strip through here, so this is the
	// one place that has to be right.
	sl.Gap = m.PaneGap
	sl.DefaultWidth = m.ScrollColumnWidthFraction()
	return sl
}

// ScrollingViewWidth is the horizontal space the scrolling strip works in: the
// content width beside any reserved sidebar band. Every viewport computation
// (clamping, centering, resolving column widths) runs against this width, and
// the computed strip positions are then shifted right by GetLeftMargin, so the
// strip scrolls within the content box instead of underneath the sidebar.
func (m *OS) ScrollingViewWidth() int {
	return m.GetContentWidth()
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
	viewW := m.ScrollingViewWidth()
	leftMargin := m.GetLeftMargin()

	sl.ClampViewport(viewW)

	layouts := sl.ComputePositions(viewW, m.GetUsableHeight(), m.GetTopMargin())

	// Scrolling layout transitions always animate (even with --no-animations)
	// because the viewport shift is disorienting without the slide.
	dur := 150 * time.Millisecond
	if m.Settings.GetAnimationDuration() > 0 {
		dur = m.Settings.GetAnimationDuration()
	}

	// Asked once for the whole layout, as ApplyBSPLayout does, because it ends a
	// stale deferral as a side effect. The strip used to skip the deferral
	// altogether and announce a real size per pane per resize step, which is one
	// SIGWINCH per pane for every column the user drags the host edge through -
	// the exact storm the deferral exists to stop.
	deferring := m.resizeDeferralActive()

	for windowIntID, rect := range layouts {
		// ComputePositions works in strip coordinates; place the strip inside
		// the content region.
		rect.X += leftMargin
		win := m.getWindowByIntID(windowIntID)
		if win == nil || win.Workspace != m.CurrentWorkspace || win.Minimized || win.IsFloating {
			continue
		}
		// The strip has no dividers to share, so its panes always draw their own
		// border. Settle that allowance before the rectangle, as placePane does:
		// it decides how much of the rectangle the guest gets, so settling it
		// afterwards announces the rectangle twice, once at each allowance.
		borderChanged := win.Tiled
		if borderChanged {
			win.Tiled = false
			win.InvalidateCache()
		}
		// A changed allowance owes the guest a new box even at the same rectangle.
		if borderChanged || win.Width != rect.W || win.Height != rect.H {
			if deferring {
				win.ResizeVisual(rect.W, rect.H)
				m.PendingResizes[win.ID] = [2]int{rect.W, rect.H}
			} else {
				win.Resize(rect.W, rect.H)
			}
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

		// A snap left over from an earlier placement owns this window's geometry
		// and stamps its own rectangle back on the next tick, without resizing
		// the emulator with it. The branch above only retires one when it creates
		// a replacement, so a column that changed width without changing column -
		// the host resizing while the strip stays put - fell through to here with
		// the old snap still live, and one tick later the pane was drawing at one
		// size while its guest wrote at another.
		m.CancelSnapAnimation(win)
		win.X = rect.X
		win.Y = rect.Y
		win.Width = rect.W
		win.Height = rect.H
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
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.scrollingSyncFocusToOS()
	m.scrollingSetPositions()
}

// ScrollingFocusRight navigates to the column to the right.
func (m *OS) ScrollingFocusRight() {
	sl := m.GetOrCreateScrollingLayout()
	sl.FocusRight()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.scrollingSyncFocusToOS()
	m.scrollingSetPositions()
}

// ScrollingMoveColumnLeft moves the focused column left.
func (m *OS) ScrollingMoveColumnLeft() {
	sl := m.GetOrCreateScrollingLayout()
	sl.MoveColumnLeft()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.scrollingSetPositions()
}

// ScrollingMoveColumnRight moves the focused column right.
func (m *OS) ScrollingMoveColumnRight() {
	sl := m.GetOrCreateScrollingLayout()
	sl.MoveColumnRight()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.scrollingSetPositions()
}

// ScrollingCycleWidth cycles the focused column through preset widths.
func (m *OS) ScrollingCycleWidth() {
	sl := m.GetOrCreateScrollingLayout()
	sl.CycleWidth()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.scrollingSetPositions()
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
	m.scrollingSetPositions()
}

// ScrollingExpelWindow pushes the focused window out into its own column, and
// follows it there.
func (m *OS) ScrollingExpelWindow() {
	sl := m.GetOrCreateScrollingLayout()
	sl.ExpelWindow()
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
	m.scrollingSyncFocusToOS()
	m.scrollingSetPositions()
}

// ScrollingScrollViewport scrolls the viewport manually (mouse wheel).
// Uses instant positioning so scrolling feels direct and responsive.
func (m *OS) ScrollingScrollViewport(delta int) {
	sl := m.GetOrCreateScrollingLayout()
	viewW := m.ScrollingViewWidth()
	// Cancel any in-flight slide animations so the wheel feels direct
	m.CompleteAllAnimations()
	sl.ViewportX += delta * (viewW / 5)
	sl.ClampViewport(viewW)
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

	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
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
		m.scrollingSetPositions()
	}
}

// scrollingLayoutStale reports whether the panes on screen are somewhere other
// than this client's own strip puts them, which is the scrolling layout's
// version of the question tiledLayoutStale asks of the other two.
//
// It is asked against the strip rather than against the box because the strip
// is longer than the box by design. What it catches is the same thing: a
// rectangle computed by somebody else and adopted here. What it must not catch
// is a strip scrolled off the focused column, which is a position the user
// chose and not a layout in need of recomputing.
func (m *OS) scrollingLayoutStale() bool {
	sl := m.WorkspaceScrollingLayouts[m.CurrentWorkspace]
	if sl == nil || len(sl.Columns) == 0 {
		return false
	}
	want := sl.ComputePositions(m.ScrollingViewWidth(), m.GetUsableHeight(), m.GetTopMargin())
	leftMargin := m.GetLeftMargin()
	for _, w := range m.Windows {
		if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized || w.Minimizing || w.IsFloating {
			continue
		}
		rect, ok := want[m.getWindowIntID(w.ID)]
		if !ok {
			// A pane the strip has never heard of: the strip is behind the
			// window list, which the retile is what fixes.
			return true
		}
		rect.X += leftMargin
		// A pane already sliding to where the strip wants it is not stale, it is
		// in flight. The strip's own transitions always animate, so without this
		// every sync that arrived during a slide would read the intermediate
		// position as somebody else's layout.
		if m.windowHasAnimationTo(w, rect.X, rect.Y, rect.W, rect.H) {
			continue
		}
		if w.X != rect.X || w.Y != rect.Y || w.Width != rect.W || w.Height != rect.H {
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

// adoptScrollStrip takes the strip as the session has it: the offset a peer
// scrolled to, and the column the session's focus is on.
//
// Both halves are needed and neither is enough. The offset alone leaves the
// strip pointing at the column this client last focused, which is what decides
// where a new column is inserted and where a left/right step starts from. The
// focused column alone leaves the viewport where it was, which is the report
// this exists for: the border moves to a window that is not on screen.
//
// It acts only on a change. A broadcast repeating the state everyone already
// holds must not restart the slide, and - now that the offset is shared - must
// not drag the strip back to the focused column either: a peer scrolling away
// from the focused window is a decision, and re-broadcasting it is not a
// request to undo it. EnsureFocusedVisible therefore runs on a focus change and
// not otherwise, which is also what keeps it agreeing with the threshold it was
// given in 5f3af88a: a column any of which is on screen is left alone.
//
// Called from inside a sync, so it never pushes. The answer owed to the daemon
// for the whole sync is sent once, by ApplyStateSync.
func (m *OS) adoptScrollStrip(strip *session.ScrollStripState, focusChanged bool) {
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
			// Only a column change is a move. Focus landing on another window
			// stacked in the column it was already on is worth recording - it is
			// what the column returns to when it is focused again - but every
			// window in a column keeps its row whichever of them is active, so
			// there is nothing to lay out again.
			was := sl.FocusedCol
			if sl.FocusColumnContaining(m.getWindowIntID(fw.ID)) && sl.FocusedCol != was {
				moved = true
			}
		}
		// The offset the peer sent has usually done this already, and then this
		// is a no-op. It is what answers the cases where nothing sent one: a
		// focus the daemon moved on its own, or a peer too old to say where its
		// strip is. Every client works it out from the same strip and the same
		// content width, so they all land on the same offset.
		before := sl.ViewportX
		sl.EnsureFocusedVisible(m.ScrollingViewWidth())
		moved = moved || sl.ViewportX != before
	}
	if moved {
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
	// Get current width and apply delta, capped at 90% of the content width
	viewW := m.ScrollingViewWidth()
	maxWidth := viewW * 9 / 10
	currentWidth := sl.ResolveColumnWidth(sl.FocusedCol, viewW)
	newWidth := max(min(currentWidth+delta, maxWidth), 20)
	col.FixedWidth = newWidth
	col.Proportion = 0 // FixedWidth takes priority
	sl.ScrollToFocusedColumn(m.ScrollingViewWidth())
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
