package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// MinimizeWindow minimizes the window at the specified index.
func (m *OS) MinimizeWindow(i int) {
	if i >= 0 && i < len(m.Windows) && !m.Windows[i].Minimized && !m.Windows[i].Minimizing {
		// Get pointer to the actual window (not a copy)
		window := m.Windows[i]

		// Store current position before minimizing
		window.PreMinimizeX = window.X
		window.PreMinimizeY = window.Y
		window.PreMinimizeWidth = window.Width
		window.PreMinimizeHeight = window.Height

		// Immediately minimize without animation
		now := time.Now()
		window.Minimized = true
		window.Minimizing = false
		window.MinimizeOrder = now.UnixNano() // Track order for dock sorting

		// Set highlight timestamp for dock tab
		window.MinimizeHighlightUntil = now.Add(1 * time.Second)

		// Change focus to next visible window
		if i == m.FocusedWindow {
			m.FocusNextVisibleWindow()
		}

		// Retile remaining windows if in tiling mode
		if m.AutoTiling {
			if m.UseScrollingLayout {
				// Remove from scrolling layout and retile
				intID := m.getWindowIntID(window.ID)
				sl := m.GetOrCreateScrollingLayout()
				sl.RemoveWindow(intID)
				sl.EnsureFocusedVisible(m.ScrollingViewWidth())
				m.scrollingSetPositions()
			} else if m.UseBSPLayout {
				// Remove from the BSP tree and reflow the remaining panes,
				// mirroring the close path (DeleteWindow). Using the
				// master-stack tiler here would ignore the tree and leave a
				// stale window ID behind, discarding custom split ratios.
				m.RemoveWindowFromBSPTree(window)
				m.ApplyBSPLayout()
			} else {
				m.TileRemainingWindows(i)
			}
		}
	}
}

// RestoreWindow restores a minimized window at the specified index.
func (m *OS) RestoreWindow(i int) {
	if i >= 0 && i < len(m.Windows) && m.Windows[i].Minimized {
		window := m.Windows[i]

		// In tiling mode, skip animation and let TileAllWindows() handle positioning
		// This prevents incorrect tiling calculations when restoring multiple windows
		if m.AutoTiling {
			window.Minimized = false

			if m.UseScrollingLayout {
				// Re-add to scrolling layout
				intID := m.getWindowIntID(window.ID)
				sl := m.GetOrCreateScrollingLayout()
				if !sl.HasWindow(intID) {
					sl.AddColumn(intID)
				}
			}

			// Bring the window to front and focus it
			m.FocusWindow(i)
			m.TileAllWindows()
			return
		}

		// Non-tiling mode: create smooth animation to PreMinimize position
		// Create and start animation
		anim := m.CreateRestoreAnimation(i)
		if anim != nil {
			// Set window to animation start position (dock position) to avoid flashing
			window.X = anim.StartX
			window.Y = anim.StartY
			window.Width = anim.StartWidth
			window.Height = anim.StartHeight

			m.Animations = append(m.Animations, anim)
		}

		// Mark as not minimized after setting position so it shows during animation
		window.Minimized = false

		// Bring the window to front and focus it
		m.FocusWindow(i)
		// Enter window management mode to interact with the restored window
		m.Mode = WindowManagementMode
	}
}

// ToggleZoom toggles the focused window between zoomed (fullscreen) and normal state.
// When zoomed, the window fills the entire viewport (minus dock). When unzoomed, it
// returns to its previous size and position. Other windows are hidden while zoomed.
func (m *OS) ToggleZoom() {
	m.settleSizes(func() { m.toggleZoom() })
}

// toggleZoom is ToggleZoom with the announcements already held.
func (m *OS) toggleZoom() {
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}

	// Zooming is structural, the same way switching tiling mode is: the
	// rectangle it lands on is final, not a step on the way to a size the user
	// is still choosing. A resize recorded before the zoom and drained after it
	// was replayed over the zoomed rectangle, so the pane shrank back to its
	// tile a tick later with the rest of the region left blank, and the guest
	// took a second announcement for a size it never had.
	m.requireRealLayout()

	// Zoom sets the pane's rectangle directly, and a snap still in flight owns
	// that rectangle: zooming while the scrolling strip was mid-slide put the
	// pane back in its column one tick later, with the emulator still at the
	// zoomed size. Retiring it also keeps the pre-zoom rectangle honest, since it
	// is read off the window a line below.
	m.CancelSnapAnimation(fw)

	if fw.Zoomed {
		// Restore from zoom
		fw.Zoomed = false
		fw.X = fw.PreZoomX
		fw.Y = fw.PreZoomY
		fw.Width = fw.PreZoomWidth
		fw.Height = fw.PreZoomHeight
		fw.InvalidateCache()
		// Route the resize through the shared path so a daemon-hosted pane is told
		// its new size too; resizing the local emulator alone leaves the app
		// unreflowed at the old size.
		fw.Resize(fw.Width, fw.Height)
		m.FlushPTYBuffersAfterResize()
		// If tiling, retile all
		if m.AutoTiling {
			m.TileAllWindows()
		}
		m.MarkAllDirty()
	} else {
		// One pane is zoomed per workspace. It was not, while the flag was
		// client-local and only the focused pane was drawn: zooming a second
		// pane left the first one flagged and still holding the whole box,
		// invisible until the focus came back to it and the layout was wrong
		// when it did. Shared, that ambiguity is a divergence rather than a
		// latent mess - each client picks a pane to blow up and they need not
		// pick the same one - so the previous zoom is retired here.
		retireRetile := m.retireOtherZooms(fw)

		// Save current position and zoom to fullscreen
		fw.PreZoomX = fw.X
		fw.PreZoomY = fw.Y
		fw.PreZoomWidth = fw.Width
		fw.PreZoomHeight = fw.Height
		fw.Zoomed = true

		m.applyZoomRect(fw, false)
		if retireRetile {
			// A pane the retirement above handed back to the layout. Retiling
			// while a pane is zoomed is safe now and was not before: the tiler
			// skips the zoomed pane's rectangle and hands it the zoom box.
			m.tileAllWindows()
		}
		m.FlushPTYBuffersAfterResize()
		m.MarkAllDirty()
	}
}

// zoomedWindow is the pane the session has zoomed on the workspace this client
// is showing, or nil when nothing on it is zoomed.
//
// Asked of the workspace, never of the focused pane. While zoom was
// client-local the two questions had one answer, because the only client that
// could hold the flag was the one that had just pressed the key on its own
// focused pane. They come apart the moment the flag is shared: focus and zoom
// travel in the same broadcast but a client applies them a step apart, a client
// whose focused id is not in its window list holds -1, and a peer can be sitting
// in its sidebar. Every reader that asked the focused window read those moments
// as "nothing is zoomed" - which retiles the zoom away and drags every other
// client's shell back to its tile with it.
func (m *OS) zoomedWindow() *terminal.Window {
	for _, w := range m.Windows {
		if w == nil || !w.Zoomed {
			continue
		}
		if w.Workspace != m.CurrentWorkspace || w.Minimized || w.Minimizing {
			continue
		}
		return w
	}
	return nil
}

// zoomRect is the box a zoomed pane fills on this client: the content region,
// which is the box the session agreed the panes go in, beside a reserved
// sidebar band and clear of the dock rows. The margins come from the negotiated
// reserve rather than this client's own dock config, so the rectangle sits
// inside the box every client tiles against.
//
// It is this client's box, computed from this client's size. The flag that says
// a pane is zoomed is shared; this is not, and a peer recomputes it here rather
// than adopting the rectangle a differently sized client arrived at.
//
// ZoomMaxWidth is the one term in it that is a per-client setting rather than a
// session-agreed one, so two clients that have set it differently will hand the
// same shell two widths. It is off by default and it was already the width the
// zooming client pushed, so nothing regressed here - but it is the one input to
// this box that the session does not settle, and settling it is a job of its
// own.
func (m *OS) zoomRect() (x, y, w, h int) {
	topMargin := m.GetTopMargin()
	leftMargin := m.GetLeftMargin()
	contentWidth := m.GetContentWidth()
	zoomWidth := contentWidth
	// If ZoomMaxWidth is set, cap width and center horizontally
	if m.Settings.ZoomMaxWidth > 0 && m.Settings.ZoomMaxWidth < contentWidth {
		zoomWidth = m.Settings.ZoomMaxWidth
	}
	return leftMargin + (contentWidth-zoomWidth)/2, topMargin, zoomWidth, m.GetUsableHeight()
}

// applyZoomRect puts a zoomed pane in this client's zoom box and tells its
// guest, through the shared path so a daemon-hosted pane is told its new size
// too; resizing the local emulator alone leaves the app unreflowed at the old
// size. It is idempotent, so it can be run on any sync that might have moved
// the box.
//
// deferring is the tiler's own answer to resizeDeferralActive, threaded through
// rather than asked again: while the pointer is dragging an edge the pane is
// given the box visually and the real announcement is left for the release, the
// same bargain every tiled pane gets.
func (m *OS) applyZoomRect(w *terminal.Window, deferring bool) {
	// A snap still in flight owns this pane's rectangle and stamps its own back
	// on the next tick, so it is retired before the box is set - the same thing
	// toggleZoom does before it zooms, and the same thing ApplyBSPLayout does
	// before it places a pane. Retired even when the box already matches: the
	// snap is heading somewhere else regardless.
	m.CancelSnapAnimation(w)
	x, y, width, height := m.zoomRect()
	if w.X == x && w.Y == y && w.Width == width && w.Height == height {
		return
	}
	w.X, w.Y = x, y
	w.InvalidateCache()
	if deferring {
		w.ResizeVisual(width, height)
		m.PendingResizes[w.ID] = [2]int{width, height}
		w.MarkPositionDirty()
		return
	}
	w.Resize(width, height)
}

// applyZoomState puts this client's own geometry behind the zoom flags a sync
// delivered, and reports whether the layout owes anybody a retile.
//
// Zoom travels as a flag; the rectangle it implies does not. A zoomed pane
// covers the content region of the client that zoomed it, which is that
// client's render size less the agreed reserve, so adopting the box would leave
// a narrower peer drawing a shell wider than its screen and a wider one drawing
// it in a corner. The flag is adopted, the box is computed here.
//
// unzoomed are the panes this sync took out of zoom. They are holding a zoom box
// and nothing in the sync says what should replace it: under a tiling layout the
// answer is the layout's, which is what the returned bool asks for, and outside
// one it is the rectangle the pane had before it was zoomed, which travels with
// the flag exactly as the pre-minimize rectangle does.
func (m *OS) applyZoomState(unzoomed []*terminal.Window) bool {
	retile := false
	for _, w := range unzoomed {
		retile = m.unzoomPane(w) || retile
	}
	if zw := m.zoomedWindow(); zw != nil {
		// Unconditional, not only when the flag changed: the box also moves when
		// the session resizes or its reserve is renegotiated, and while a pane is
		// zoomed nothing else looks at that pane's rectangle.
		m.applyZoomRect(zw, false)
	}
	return retile
}

// retireOtherZooms takes every pane but keep out of zoom on keep's workspace,
// reporting whether the layout owes any of them a retile.
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

// unzoomPane takes one pane out of zoom and gives it a rectangle again,
// reporting whether the answer is a retile the caller still owes it.
//
// Under a tiling layout the pre-zoom rectangle is not the answer. It is a record
// of where the pane sat at the moment it was zoomed, and the box has had every
// chance to move since - a client resized, a peer joined narrower, the reserve
// was renegotiated - so restoring it puts the pane at a size the layout does not
// agree with and the shell at a width no client is drawing. The layout knows
// where the pane goes; the caller is told to ask it.
//
// Outside a tiling layout nothing else will ever place the pane, so the pre-zoom
// rectangle is the only record there is and it is restored.
func (m *OS) unzoomPane(w *terminal.Window) bool {
	w.Zoomed = false
	if m.AutoTiling && !w.IsFloating {
		return true
	}
	if w.PreZoomWidth <= 0 || w.PreZoomHeight <= 0 {
		// A peer that predates the pre-zoom fields, or a pane zoomed before it
		// had a rectangle. Nothing to go back to, so the box it holds stands.
		return false
	}
	w.X, w.Y = w.PreZoomX, w.PreZoomY
	w.InvalidateCache()
	w.Resize(w.PreZoomWidth, w.PreZoomHeight)
	return false
}

// RestoreMinimizedByIndex restores a minimized window by its minimized index.
func (m *OS) RestoreMinimizedByIndex(index int) {
	// Find the nth minimized window in current workspace
	minimizedCount := 0
	for i, window := range m.Windows {
		if window.Workspace == m.CurrentWorkspace && window.Minimized {
			if minimizedCount == index {
				m.RestoreWindow(i)
				return
			}
			minimizedCount++
		}
	}
}

// FocusNextVisibleWindow focuses the next visible window in the current workspace.
func (m *OS) FocusNextVisibleWindow() {
	// Find the next non-minimized and non-minimizing window to focus in current workspace
	// Start from the beginning to find any visible window

	// First pass: find any visible window in current workspace
	for i := range len(m.Windows) {
		if m.Windows[i].Workspace == m.CurrentWorkspace && !m.Windows[i].Minimized && !m.Windows[i].Minimizing {
			m.FocusWindow(i)
			return
		}
	}

	// No visible windows in workspace, set focus to -1
	m.FocusedWindow = -1
}

// HasMinimizedWindows returns true if there are any minimized windows.
func (m *OS) HasMinimizedWindows() bool {
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && w.Minimized {
			return true
		}
	}
	return false
}
