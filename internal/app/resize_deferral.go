package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const resizeDeferralTimeout = 500 * time.Millisecond

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

func (m *OS) resizePane(win *terminal.Window, width, height int, deferring bool) {
	if deferring {
		win.ResizeVisual(width, height)
		m.PendingResizes[win.ID] = [2]int{width, height}
		return
	}
	m.resizeNow(win, width, height)
}

func (m *OS) resizeNow(win *terminal.Window, width, height int) {
	delete(m.PendingResizes, win.ID)
	win.Resize(width, height)
}

func (m *OS) placePaneAt(win *terminal.Window, x, y, width, height int, deferring bool) {
	win.X, win.Y = x, y
	win.InvalidateCache()
	m.resizePane(win, width, height, deferring)
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

func (m *OS) endResizeDeferral() {
	m.settleSizes(func() { m.endResizeDeferralLocked() })
}

func (m *OS) endResizeDeferralLocked() {
	m.viewportResizing = false
	m.renderSkipped = false
	m.ApplyPendingResizes()
}

func (m *OS) noteResizeStep(at time.Time) {
	m.viewportResizeAt = at
}

func (m *OS) notePointerEvent(at time.Time) {
	if m.Settings.ZenMode == config.ZenModeMouse && m.zenHidden && !m.pointerRecentlyMoved() {
		m.markZenDirty()
	}
	m.lastPointerAt = at
}

func (m *OS) requireRealLayout() {
	m.settleSizes(func() {
		m.landSnapAnimations()
		m.endResizeDeferralIfAny()
	})
}

func (m *OS) requireRealRelayout() {
	m.settleSizes(m.endResizeDeferralIfAny)
}

func (m *OS) endResizeDeferralIfAny() {
	if m.viewportResizing || len(m.PendingResizes) > 0 {
		m.endResizeDeferral()
	}
}

func (m *OS) endLostGesture() {
	wasResizing := m.Resizing
	m.Dragging = false
	m.Resizing = false
	m.BorderResizing = false
	m.BorderResizeEdge = BorderEdgeNone
	m.RightClickPending = false
	m.InteractionMode = false
	m.DraggedWindowIndex = -1
	m.ClearDropZone()
	m.ScrollbarDragging = false
	m.ScrollbarDragWindowIndex = -1
	m.ScrollbarGrabOffset = 0

	m.EndPointerGesture()
	m.endResizeDeferral()
	m.releaseGestureAnnouncements()
	m.clearStaleManipulation()

	if wasResizing && m.AutoTiling && !m.UseScrollingLayout {
		m.SyncMasterStackFromGeometry()
		m.SyncBSPTreeFromGeometry()
	}
	m.renderSkipped = false
}

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
	m.fileDrag = fileDragState{}
	m.dockWorkspaceDrag = dockWorkspaceDragState{}
	m.CtrlDragPending = false
	m.CtrlClickLink = ""
	m.Capture.Dragging = false
	if m.SidebarEdge.Active {
		m.SidebarEdge = sidebarEdgeState{}
		m.saveSidebarState()
	}
	if m.sidebarSplit.Active {
		m.sidebarSplit.Active = false
		m.saveSidebarState()
	}
}

// EndStrayGesture ends a drag or resize that something other than the window
// layer claimed the release for.
//
// Mouse release can leave down a dozen paths (an overlay, the sidebar band, a
// guest that asked for mouse tracking, the scrollbar, a copy-mode selection),
// and each one returns before the cleanup at the bottom of the handler. A
// resize that survived one of them would keep the size readout on screen and
// every pane in resize borders with nothing pressed. It is idempotent so the normal
// path, which has already finished the gesture properly, finds nothing to do.
func (m *OS) EndStrayGesture() {
	if !m.Dragging && !m.Resizing && !m.BorderResizing {
		return
	}
	m.endLostGesture()
}

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
