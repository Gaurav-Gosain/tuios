package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The deferred half of a resize, and the rule that it can never outlive the
// gesture that asked for it.
//
// A retile that happens while a resize is still in progress places panes
// directly and resizes them visually only, recording the real size in
// PendingResizes for later. That is right while the size is still moving: the
// expensive half (reallocating every emulator's backing store, TIOCSWINSZ,
// SIGWINCH, the daemon round trip, every guest's full repaint) costs more than
// a frame and the user is not finished choosing a size yet.
//
// It is wrong the moment the gesture ends, and the original design ended it
// only when a message arrived: ViewportResizeSettledMsg for a terminal resize,
// mouse release for a drag. Neither is guaranteed. Update recovers panics and
// returns a nil command when it does, which drops the settle that the very same
// handler had just armed; the mouse release itself goes missing whenever the
// pointer leaves the surface the events come from mid-drag, which a browser
// client does every time. Either way the flag stayed set for the rest
// of the session, so every retile after it - including the one a new window
// triggers - took the visual-only branch, no pane ever got its real size, and
// the layout was left showing whatever rectangles happened to be current.
//
// So the deferral is keyed on freshness instead. It holds only while a resize
// event has arrived recently; past that it expires by itself and drains the
// work it was holding. Nothing has to arrive for the layout to become correct
// again. The cost of expiring early during a genuine pause mid-gesture is one
// retile taking the full path, which is what it would have done anyway had the
// gesture ended there.

// resizeDeferralTimeout is how long after the last resize event the deferral
// still counts as live.
//
// Comfortably longer than viewportResizeSettleDelay, so the settle message
// remains the normal way a terminal resize ends and this only acts when that
// message never comes. Short enough that a lost mouse release is not something
// the user has to notice: the next retile after half a second is a real one.
const resizeDeferralTimeout = 500 * time.Millisecond

// resizeDeferralActive reports whether a retile happening now should place
// panes directly and defer the expensive half.
//
// It has a side effect on purpose: finding the deferral stale is the only
// reliable moment to end it, so it ends it, and drains what was deferred. Call
// it once per retile rather than per pane.
func (m *OS) resizeDeferralActive() bool {
	if !m.Resizing && !m.viewportResizing {
		return false
	}

	now := time.Now()
	fresh := false
	if m.Resizing && !m.lastPointerAt.IsZero() && now.Sub(m.lastPointerAt) <= resizeDeferralTimeout {
		fresh = true
	}
	if m.viewportResizing && !m.viewportResizeAt.IsZero() && now.Sub(m.viewportResizeAt) <= resizeDeferralTimeout {
		fresh = true
	}
	if fresh {
		return true
	}

	m.endResizeDeferral()
	return false
}

// PendingViewportResize reports the generation of the terminal resize still in
// flight, and whether there is one. It is a pure read, unlike
// resizeDeferralActive above, which ends a stale deferral as it answers.
//
// Exported for the out-of-process fuzz target (internal/fuzz/apptarget), which
// needs both halves: the generation to hand back a ViewportResizeSettledMsg,
// since nothing there runs the command that would carry it, and the flag to know
// that a stale announcement is deferred on purpose rather than wrong.
func (m *OS) PendingViewportResize() (uint64, bool) {
	return m.viewportResizeGen, m.viewportResizing
}

// endResizeDeferral finishes a deferred resize now: the recorded sizes are
// pushed through to the emulators, the PTYs and the daemon, and the next retile
// lays panes out for real.
//
// m.Resizing is left alone. It belongs to the mouse handlers and describes
// whether a button is down, which is not this function's business; with the
// timestamp stale the deferral stays off until a fresh resize step refreshes
// it, and a gesture that resumes simply re-enters the deferral.
func (m *OS) endResizeDeferral() {
	m.settleSizes(func() { m.endResizeDeferralLocked() })
}

// endResizeDeferralLocked is endResizeDeferral with the announcements already held.
func (m *OS) endResizeDeferralLocked() {
	m.viewportResizing = false
	m.renderSkipped = false
	m.ApplyPendingResizes()
}

// noteResizeStep records that a resize event just arrived, which is what keeps
// the deferral alive.
func (m *OS) noteResizeStep(at time.Time) {
	m.viewportResizeAt = at
}

// notePointerEvent records that a mouse event just arrived. A resize drag is
// only live while the pointer is still reporting.
func (m *OS) notePointerEvent(at time.Time) {
	// Zen mode (mouse): a pointer event re-opens the reveal window, so borders
	// that the idle melt hid must come back. The event forces its own frame,
	// but each window's CachedLayer still holds the borderless render and is
	// reused until the window is dirty, so the reveal must mark the affected
	// windows dirty here - otherwise the borders would never be drawn again.
	if m.Settings.ZenMode == config.ZenModeMouse && m.zenHidden && !m.pointerRecentlyMoved() {
		m.markZenDirty()
	}
	m.lastPointerAt = at
}

// requireRealLayout settles every transient geometry mechanism before a
// structural change to the layout - a window opening, closing or splitting,
// tiling being toggled, a layout being loaded. Those are not resize steps, and
// their result is what the user is left looking at, so they must never be laid
// out visually-only and left for a message that may not come.
//
// That means both a deferred resize and a snap still in flight. See
// landSnapAnimations for why a snap cannot simply be dropped. Both settle inside
// one hold: a pane that a landed snap and a drained deferral both touch has one
// size at the end of this, and that is the only one worth sending.
func (m *OS) requireRealLayout() {
	m.settleSizes(func() {
		m.landSnapAnimations()
		if m.viewportResizing || len(m.PendingResizes) > 0 {
			m.endResizeDeferral()
		}
	})
}

// endLostGesture retires a drag or resize whose release never arrived.
//
// It does what mouse release does minus the parts that need the release's own
// coordinates: the gesture is over, the panes keep the geometry the last motion
// gave them, and the deferred resizes are pushed through. The alternative is a
// gesture that never ends, and that is not merely a stuck flag - while it is
// set, MarkTerminalsWithNewContent refuses to look at any pane, so no window
// shows another byte of output for the rest of the session.
func (m *OS) endLostGesture() {
	wasResizing := m.Resizing
	m.Dragging = false
	m.Resizing = false
	m.BorderResizing = false
	m.BorderResizeEdge = BorderEdgeNone
	m.RightClickPending = false
	m.InteractionMode = false
	m.DraggedWindowIndex = -1
	// The scrollbar grab rides on Dragging but is read from its own flag, so
	// clearing only Dragging left the thumb tracking a pointer with nothing held
	// and, because the hover paths yield to it, swallowed hover for good.
	m.ScrollbarDragging = false
	m.ScrollbarDragWindowIndex = -1
	m.ScrollbarGrabOffset = 0

	m.EndPointerGesture()
	m.endResizeDeferral()
	m.clearStaleManipulation()

	// A resize drag is what the BSP tree derives its ratios from; without this
	// the next retile discards everything the drag did.
	if wasResizing && m.AutoTiling && !m.UseScrollingLayout {
		m.SyncBSPTreeFromGeometry()
	}
	m.renderSkipped = false
}

// endGestureWithoutButton is the per-frame backstop: a gesture cannot outlive
// the button that started it.
//
// Every release path ends the gesture, but a release can go missing entirely -
// the pointer leaves the surface the events come from, a recovered panic in
// Update drops the event - and then nothing at all has to arrive for the resize
// to be over. Run once per maintenance tick, so no frame is drawn with the size
// readout up and no button pressed.
func (m *OS) endGestureWithoutButton() {
	if (m.Dragging || m.Resizing) && !m.pointerDown {
		m.endLostGesture()
	}
}

// EndPointerGrabs releases the gestures the chrome holds in flags of its own
// rather than in Dragging: a grabbed overlay panel, the accent picker's grab on
// its grid or hue strip, the rail's width drag and session reorder, and an
// armed ctrl-drag. endLostGesture covers the window layer; nothing covered
// these, so a release that went missing left them running against every motion
// the host reports, button or no button.
//
// They are abandoned rather than committed. A lost release says nothing about
// where the button came up, and a stranded session-row press committed as the
// click it might have been would switch session on a bare hover. The rail's
// width is the one thing kept, because it is already on screen at the width the
// last held motion gave it and only the persist was still outstanding.
func (m *OS) EndPointerGrabs() {
	m.OverlayDrag.Active = false
	m.accentDragging = false
	m.accentDrag = accentHitNone
	m.SidebarDrag = sidebarDragState{}
	m.dockWorkspaceDrag = dockWorkspaceDragState{}
	m.CtrlDragPending = false
	// A capture drag whose release went missing would otherwise leave the
	// marquee tracking a bare hover for ever.
	m.Capture.Dragging = false
	if m.SidebarEdge.Active {
		m.SidebarEdge = sidebarEdgeState{}
		m.saveSidebarState()
	}
}

// EndStrayGesture ends a drag or resize that something other than the window
// layer claimed the release for.
//
// Mouse release can leave down a dozen paths - an overlay, the sidebar band, a
// guest that asked for mouse tracking, the scrollbar, a copy-mode selection -
// and each one used to return before the cleanup at the bottom of the handler.
// A resize that survived one of them kept the size readout on screen and every
// pane in resize borders with nothing pressed. It is idempotent so the normal
// path, which has already finished the gesture properly, finds nothing to do.
func (m *OS) EndStrayGesture() {
	if !m.Dragging && !m.Resizing && !m.BorderResizing {
		return
	}
	m.endLostGesture()
}

// clearStaleManipulation drops IsBeingManipulated from any window still
// carrying it while no drag or resize is in progress.
//
// The flag freezes a pane's content at its last cached frame, which is right
// for a pane the pointer is moving and is silent, total corruption once the
// gesture is over: the pane keeps drawing a screenshot of itself and never
// shows another byte of output. Mouse release clears it, and mouse release is
// exactly the event that is lost when the pointer leaves the surface the events
// come from. Outside a gesture no window should carry it, so this is a safe
// sweep rather than a guess.
func (m *OS) clearStaleManipulation() {
	if m.Dragging || m.Resizing || m.InteractionMode {
		return
	}
	for _, w := range m.Windows {
		if w == nil || !w.IsBeingManipulated {
			continue
		}
		w.IsBeingManipulated = false
		w.MarkContentDirty()
		w.InvalidateCache()
	}
}
