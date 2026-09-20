package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// MinimizeWindow minimizes the window at the specified index.
//
// A popup cannot be minimized. Minimizing puts a pane on the dock as a pill the
// user picks up again later, and a popup has no later: it holds one command and
// closes when the command exits, which would leave a pill for a pane that is
// gone. Close it instead.
func (m *OS) MinimizeWindow(i int) {
	if i >= 0 && i < len(m.Windows) && m.Windows[i].IsPopup {
		return
	}
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
		camera := m.zoomUsesLayout(fw)
		fw.Zoomed = false
		if camera {
			// The camera comes back to the layout's own size, which the tiler
			// does for every pane at once. Nothing here to put back by hand,
			// and the same slide on the way out as on the way in.
			m.zoomRelayout = true
			m.tileAllWindows()
			m.FlushPTYBuffersAfterResize()
			m.MarkAllDirty()
			return
		}
		// The slide back to the tile, when it is on. The pane is left where it
		// is and the snap walks it home, landing it and resizing the guest once
		// at the destination, exactly as the way in does.
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
		retireRetile := m.zoomPane(fw)
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
	return m.zoomRectFor(nil)
}

// zoomRectFor is zoomRect for a named pane.
//
// win is unused and kept so the two callers read the same. A zoom of part of
// the screen is a camera over the layout rather than a box for one pane, so the
// only zoom that comes through here is the zoom of the whole screen, which is
// the same rectangle whichever pane asked for it. See zoom_canvas.go and
// zoomUsesLayout.
func (m *OS) zoomRectFor(win *terminal.Window) (x, y, w, h int) {
	_ = win
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
	m.applyZoomRectAnimated(w, deferring, false)
}

// applyZoomRectAnimated is applyZoomRect with a say in whether the move is
// animated.
//
// Only a real transition animates. This also runs on every sync and every
// resize to keep the box correct, and sliding the pane on those would put the
// whole region in motion whenever anything at all changed.
func (m *OS) applyZoomRectAnimated(w *terminal.Window, deferring, animate bool) {
	// A snap still in flight owns this pane's rectangle and stamps its own back
	// on the next tick, so it is retired before the box is set - the same thing
	// toggleZoom does before it zooms, and the same thing ApplyBSPLayout does
	// before it places a pane. Retired even when the box already matches: the
	// snap is heading somewhere else regardless.
	m.CancelSnapAnimation(w)
	x, y, width, height := m.zoomRectFor(w)
	if w.X == x && w.Y == y && w.Width == width && w.Height == height {
		return
	}
	if animate && !deferring {
		if anim := m.zoomAnimation(w, x, y, width, height); anim != nil {
			m.Animations = append(m.Animations, anim)
			w.InvalidateCache()
			return
		}
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

// zoomPane gives w the workspace's zoom, taking it from whichever pane held it,
// and reports whether a pane was handed back to the layout and so needs a
// retile.
//
// It is the zoom half of toggleZoom, and the second half of a handover. One
// body, so the two ways of taking the zoom cannot come to mean different
// things.
func (m *OS) zoomPane(w *terminal.Window) bool {
	// One pane is zoomed per workspace. It was not, while the flag was
	// client-local and only the focused pane was drawn: zooming a second
	// pane left the first one flagged and still holding the whole box,
	// invisible until the focus came back to it and the layout was wrong
	// when it did. Shared, that ambiguity is a divergence rather than a
	// latent mess - each client picks a pane to blow up and they need not
	// pick the same one - so the previous zoom is retired here.
	retireRetile := m.retireOtherZooms(w)

	// Save current position and zoom to fullscreen
	w.PreZoomX = w.X
	w.PreZoomY = w.Y
	w.PreZoomWidth = w.Width
	w.PreZoomHeight = w.Height
	w.Zoomed = true

	if m.zoomUsesLayout(w) {
		// The layout places this pane along with every other. Putting it in a
		// box here first would be a rectangle the very next retile throws away.
		//
		// The retile it is waiting for slides rather than places: the whole
		// layout is going somewhere new, and a cut between two arrangements
		// says nothing about which pane was zoomed.
		m.zoomRelayout = true
		//
		// On the strip the widened column also has to be brought on screen:
		// growing a column that is half off the edge leaves the pane you asked
		// for further off it than before.
		if m.UseScrollingLayout {
			m.scrollingSetPositions()
			m.RevealFocusedColumn()
		}
		return true
	}
	m.applyZoomRectAnimated(w, false, true)
	return retireRetile
}

// zoomUsesLayout reports whether zooming w is the layout's job rather than a
// box of w's own.
//
// A zoom of part of the screen is, in all three tilers. BSP and master-stack
// get a camera over the whole arrangement; the scrolling strip is already a
// camera, so there it is the zoomed column's own width. Either way the tiler
// places the pane and nothing hands it a rectangle beside that.
//
// A zoom of the whole screen is not: there is nothing to show around the pane,
// so it takes the region the way it always has. Nor is a floating pane, which
// is not in a layout to be zoomed inside of.
func (m *OS) zoomUsesLayout(w *terminal.Window) bool {
	return m.Settings.GetZoomSize() < 100 &&
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
func (m *OS) ZoomFollowsFocus(i int) {
	if !m.Settings.ZoomFollowsFocus || i < 0 || i >= len(m.Windows) {
		return
	}
	w := m.Windows[i]
	// A popup is drawn over the zoom and focused in front of it, so focusing
	// one is not a request to see it filling the region.
	if w == nil || w.IsPopup || w.Minimized || w.Minimizing || w.Workspace != m.CurrentWorkspace {
		return
	}
	zw := m.zoomedWindow()
	if zw == nil || zw == w {
		return
	}
	m.settleSizes(func() {
		// The same two retirements toggleZoom makes, for the same reasons: a
		// deferred resize replayed over the box would shrink the pane back to
		// its tile, and a snap in flight owns the rectangle the pre-zoom record
		// is about to be read from.
		m.requireRealLayout()
		m.CancelSnapAnimation(w)
		m.handOverZoom(zw, w)
		m.FlushPTYBuffersAfterResize()
		m.MarkAllDirty()
	})
}

// handOverZoom moves the box from one pane to another, sliding both.
//
// The outgoing pane is put back in the layout first, which is what settles the
// rectangle it is going home to, and only then is it returned to the box it was
// holding so the slide has somewhere to start. Arming the animation before the
// retile would be arming it against a destination the tiler had not chosen yet.
func (m *OS) handOverZoom(from, to *terminal.Window) {
	// Under a camera there is no box to hand over. The zoom is which pane the
	// layout is aimed at, so moving it is moving the mark and letting the tiler
	// aim again, and every pane slides because the tiler animates what it
	// places.
	//
	// The retile at the end is the whole of it. Marking the new pane without
	// one left the layout drawn at the arrangement it already had, so the
	// focus moved and the zoom appeared to come off: the mark had moved and
	// nothing had been redrawn against it.
	if m.zoomUsesLayout(to) {
		m.zoomPane(to)
		m.tileAllWindows()
		return
	}

	boxX, boxY, boxW, boxH := from.X, from.Y, from.Width, from.Height

	// Back into the layout. Under a tiling layout the tiler owns where it
	// goes, and it can only place a pane that is no longer zoomed.
	from.Zoomed = false
	if m.AutoTiling && !from.IsFloating {
		m.tileAllWindows()
	} else if from.PreZoomWidth > 0 && from.PreZoomHeight > 0 {
		from.X, from.Y = from.PreZoomX, from.PreZoomY
		from.Width, from.Height = from.PreZoomWidth, from.PreZoomHeight
		from.Resize(from.PreZoomWidth, from.PreZoomHeight)
	}

	// Where it ended up is where the slide is going.
	homeX, homeY, homeW, homeH := from.X, from.Y, from.Width, from.Height
	from.X, from.Y, from.Width, from.Height = boxX, boxY, boxW, boxH
	if !m.animateToRect(from, homeX, homeY, homeW, homeH) {
		from.X, from.Y, from.Width, from.Height = homeX, homeY, homeW, homeH
		from.InvalidateCache()
	}

	// And the pane the focus reached takes the box, sliding into it from the
	// tile the retile above just gave it.
	m.zoomPane(to)
}

// zoomCoversRegion reports whether the zoomed pane's rectangle fills the content
// region, so nothing behind it could show through.
//
// It is false for a zoom sized under 100 percent, which leaves the layout
// showing at the edges on purpose, and false while a zoom is sliding, because
// the pane has not reached the box yet. The render draws the rest of the layout
// in both cases. A nil pane is not a zoom and covers nothing.
func (m *OS) zoomCoversRegion(w *terminal.Window) bool {
	if w == nil {
		return false
	}
	for _, a := range m.Animations {
		if a != nil && a.Window == w && !a.Complete {
			return false
		}
	}
	return w.X <= m.GetLeftMargin() && w.Y <= m.GetTopMargin() &&
		w.Width >= m.GetContentWidth() && w.Height >= m.GetUsableHeight()
}

// zoomAnimation is the slide between a pane's tile and a zoom box, or nil when
// the pane should simply be put there.
//
// Zoom used to be a cut: the pane was at its tile in one frame and filling the
// region in the next, with nothing to say which pane had grown. That reads
// worst exactly where it matters most, which is a zoom moving from one pane to
// another: two panes change at once and neither says which way.
//
// It is the same snap every other pane movement uses, so it lands the same way,
// resizes the guest once at the destination, and is retired by the same paths
// that retire any other.
func (m *OS) zoomAnimation(w *terminal.Window, x, y, width, height int) *ui.Animation {
	if !m.Settings.ZoomAnimation {
		return nil
	}
	dur := m.Settings.GetFastAnimationDuration()
	if dur <= 0 {
		return nil
	}
	return ui.NewSnapAnimation(w, x, y, width, height, dur)
}

// animateToRect slides a pane to a rectangle it is being put back at, which is
// the unzoom half. It reports whether it armed an animation; when it did not,
// the caller places the pane itself.
func (m *OS) animateToRect(w *terminal.Window, x, y, width, height int) bool {
	if w.X == x && w.Y == y && w.Width == width && w.Height == height {
		return false
	}
	anim := m.zoomAnimation(w, x, y, width, height)
	if anim == nil {
		return false
	}
	m.Animations = append(m.Animations, anim)
	w.InvalidateCache()
	return true
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
