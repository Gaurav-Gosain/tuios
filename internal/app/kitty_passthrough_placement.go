package app

import (
	"bytes"
	"fmt"
	"runtime"
)

// rectsOverlap checks if two rectangles overlap
func rectsOverlap(x1, y1, w1, h1, x2, y2, w2, h2 int) bool {
	return x1 < x2+w2 && x1+w1 > x2 && y1 < y2+h2 && y1+h1 > y2
}

// isOccludedByHigherWindow checks if an image region is fully occluded by a window with higher z-index
func (kp *KittyPassthrough) isOccludedByHigherWindow(
	screenX, screenY, width, height, windowZ int,
	allWindows map[string]*WindowPositionInfo,
	excludeWindowID string,
) bool {
	for id, info := range allWindows {
		// Off-workspace / minimized windows are now present in allWindows with
		// Visible:false (so their own images get hidden rather than deleted).
		// Such a window is not on screen and cannot occlude anything, even if
		// its retained geometry still overlaps the image region.
		if id == excludeWindowID || !info.Visible || info.WindowZ <= windowZ {
			continue
		}
		// Check if higher-z window overlaps the image region
		if rectsOverlap(screenX, screenY, width, height,
			info.WindowX, info.WindowY, info.Width, info.Height) {
			return true
		}
	}
	return false
}

func (kp *KittyPassthrough) RefreshAllPlacements(getAllWindows func() map[string]*WindowPositionInfo) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if !kp.enabled {
		return
	}

	// Note: prior versions short-circuited this loop in web mode because
	// xterm-addon-image could not update placements in place. sip now
	// ships a custom kitty overlay (xterm-kitty-overlay.js) that renders
	// placements as absolutely-positioned DOM canvases with proper
	// update/delete semantics, so the standard refresh path works in
	// both native and web modes.

	// Get all windows upfront for occlusion detection
	allWindows := getAllWindows()

	// Update screen dimensions from any window info
	for _, info := range allWindows {
		if info.ScreenWidth > 0 && info.ScreenHeight > 0 {
			kp.screenWidth = info.ScreenWidth
			kp.screenHeight = info.ScreenHeight
			break
		}
	}

	// Decide, once per window, whether this pass holds the window's placements
	// untouched because an interactive resize is mid-gesture (see the comment
	// on the freeze below). Computed up front and shared by both the regular
	// placement loop and the remote-video loop so that a window carrying both
	// kinds of image freezes them together, and so a window whose only image is
	// a self-placed video (removed from `placements` at handoff) still gets a
	// freeze record at all.
	if kp.frozenThisPass == nil {
		kp.frozenThisPass = make(map[string]bool)
	} else {
		clear(kp.frozenThisPass)
	}
	frozen := kp.frozenThisPass
	markFreeze := func(windowID string) {
		if _, done := frozen[windowID]; done {
			return
		}
		info := allWindows[windowID]
		if info == nil {
			return
		}
		// Coalesce an interactive resize.
		//
		// A drag-resize changes the window size every render tick, and the guest
		// has not redrawn at the new size yet (the PTY resize is deferred until
		// the gesture settles, see resize_deferral.go). Re-clipping and re-placing
		// the same stale image against each intermediate viewport size makes it
		// visibly crop and jitter many times a second: the flicker. So while a
		// window is being manipulated AND its size differs from the size the
		// placement was last laid out at, the existing placement is left exactly
		// where it is - kitty keeps the last a=p on screen - and no delete or
		// re-place is emitted for it.
		//
		// resizeFreezeSize records that last laid-out size. It is refreshed on
		// every pass that is NOT frozen, so a plain window move (manipulated but
		// unchanged size) still re-places and follows the pointer, and the instant
		// the gesture ends the settled size is recorded and the image re-places
		// once at the final geometry. The deferred PTY resize then delivers a
		// fresh frame; nothing is left stale.
		if prev, seen := kp.resizeFreezeSize[windowID]; info.IsBeingManipulated && seen &&
			(prev[0] != info.Width || prev[1] != info.Height) {
			// resizeFreezeSize is deliberately not updated, so every later tick of
			// the same gesture keeps differing from it and stays frozen until the
			// size settles.
			frozen[windowID] = true
			return
		}
		kp.resizeFreezeSize[windowID] = [2]int{info.Width, info.Height}
		frozen[windowID] = false
	}
	for windowID, placements := range kp.placements {
		if len(placements) > 0 {
			markFreeze(windowID)
		}
	}
	for windowID := range kp.remoteVideo {
		markFreeze(windowID)
	}

	// Keep self-placed remote video images in step with their window. These are
	// not in `placements`, so the loop below never sees them; here we (1) delete
	// one whose pane left the screen it was shown on (a browser quitting back to
	// the shell restores the main screen), (2) hide one that went offscreen or
	// under a higher window and re-show it when it returns, and (3) re-place one
	// with a=p when its window moved/resized, from the resident image data with
	// no re-transmit, so the frame follows a drag/resize even while the browser
	// sends no new frame. The desired geometry written here is what the async
	// frame writer reads at write time; this loop is its only writer after the
	// handoff.
	for windowID, ids := range kp.remoteVideo {
		info := allWindows[windowID]
		if info != nil && frozen[windowID] {
			// Mid drag-resize: hold the image exactly where it is, like the
			// regular placement path. The settled geometry re-places it once.
			continue
		}
		var buf bytes.Buffer
		for hostID, st := range ids {
			if info == nil || info.IsAltScreen != st.altScreen {
				fmt.Fprintf(&buf, "\x1b_Ga=d,d=i,i=%d,q=2\x1b\\", hostID)
				delete(ids, hostID)
				continue
			}
			newHostX := info.WindowX + info.ContentOffsetX + st.guestX
			newHostY := info.WindowY + info.ContentOffsetY + st.guestY

			// Clamp the visible cell area to the screen. Like the regular path,
			// keep the final screen row free: an image reaching the last row
			// makes the host terminal scroll and cascades into duplicate frames.
			showCols, showRows := st.cols, st.rows
			visible := info.Visible && newHostX >= 0 && newHostY >= 0 &&
				info.WindowX >= 0 && info.WindowY >= 0
			if visible && info.ScreenWidth > 0 && newHostX+showCols > info.ScreenWidth {
				showCols = info.ScreenWidth - newHostX
			}
			if visible && info.ScreenHeight > 0 && newHostY+showRows > info.ScreenHeight-1 {
				showRows = info.ScreenHeight - 1 - newHostY
			}
			if showCols <= 0 || showRows <= 0 {
				visible = false
			}
			if visible && kp.isOccludedByHigherWindow(
				newHostX, newHostY, showCols, showRows,
				info.WindowZ, allWindows, windowID,
			) {
				visible = false
			}

			// Record the desired geometry BEFORE emitting, so the async frame
			// writer's write-time read and its post-write convergence check
			// always see the same target this pass decided on.
			changed := st.hidden || newHostX != st.hostX || newHostY != st.hostY ||
				showCols != st.showCols || showRows != st.showRows
			st.hostX, st.hostY = newHostX, newHostY
			st.showCols, st.showRows = showCols, showRows

			if !visible {
				if !st.hidden {
					// d=i keeps the image bytes resident so re-showing is a
					// plain a=p, no re-transmit.
					fmt.Fprintf(&buf, "\x1b_Ga=d,d=i,i=%d,q=2\x1b\\", hostID)
					st.hidden = true
				}
				continue
			}
			st.hidden = false
			if changed {
				buf.Write(buildVideoReplace(hostID, st))
			}
		}
		if buf.Len() > 0 {
			kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
		}
		if len(ids) == 0 {
			delete(kp.remoteVideo, windowID)
			if len(kp.placements[windowID]) == 0 {
				delete(kp.resizeFreezeSize, windowID)
			}
		}
	}

	for windowID, placements := range kp.placements {
		if len(placements) == 0 {
			continue
		}

		info := allWindows[windowID]
		kittyPassthroughLog("RefreshAllPlacements: windowID=%s, info=%v, numPlacements=%d", windowID[:min(8, len(windowID))], info != nil, len(placements))
		if info == nil {
			for _, p := range placements {
				if !p.Hidden {
					kp.deleteOnePlacement(p)
				}
			}
			delete(kp.placements, windowID)
			delete(kp.resizeFreezeSize, windowID)
			continue
		}

		kittyPassthroughLog("RefreshAllPlacements: windowID=%s, IsAltScreen=%v, visible=%v", windowID[:min(8, len(windowID))], info.IsAltScreen, info.Visible)

		// Frozen mid drag-resize (see markFreeze above): hold this window's
		// placement untouched for this tick.
		if frozen[windowID] {
			continue
		}

		// During window manipulation (drag/resize), let images reposition
		// with the window. The change detection below (posChanged check)
		// ensures we only re-place if the position actually changed.

		// Calculate viewport dimensions (accounting for window borders).
		// For tiled/borderless windows BorderOffset=0, so content area is full
		// Width×Height. For floating windows with a border, it's 1, so content
		// is (Width-2)×(Height-2).
		viewportTop := info.ScrollbackLen - info.ScrollOffset
		viewportHeight := info.Height - 2*info.ContentOffsetY
		viewportWidth := info.Width - 2*info.ContentOffsetX

		// Collect IDs to delete (for altscreen cleanup)
		var idsToDelete []uint32

		for hostID, p := range placements {
			// Skip placements that are still receiving chunked data
			if p.Streaming {
				continue
			}

			// Handle screen mode mismatch:
			// - Images placed on normal screen should be hidden when altscreen is active
			// - Images placed on altscreen should be DELETED when back to normal screen
			//   (cleanup after TUI apps like yazi exit)
			if info.IsAltScreen != p.PlacedOnAltScreen {
				kittyPassthroughLog("RefreshPlacement: altscreen mismatch (info=%v, placed=%v)",
					info.IsAltScreen, p.PlacedOnAltScreen)
				if !p.Hidden {
					kp.deleteOnePlacement(p)
					p.Hidden = true
				}
				// When exiting altscreen (now on normal screen), delete altscreen placements entirely
				// This cleans up images from TUI apps like yazi when they exit
				if !info.IsAltScreen && p.PlacedOnAltScreen {
					kittyPassthroughLog("RefreshPlacement: cleaning up altscreen placement hostID=%d", hostID)
					idsToDelete = append(idsToDelete, hostID)
				}
				continue
			}

			// Calculate new position (where top-left of image would be)
			relativeY := p.AbsoluteLine - viewportTop

			// Calculate where the FULL image would end (for visibility check)
			fullImageBottom := relativeY + p.Rows
			fullImageRight := p.GuestX + p.Cols

			// Check if ANY part of the image is visible in the viewport
			// Image is visible if: top < viewportHeight AND bottom > 0 AND left < viewportWidth AND right > 0
			anyPartVisible := info.Visible &&
				relativeY < viewportHeight && fullImageBottom > 0 &&
				p.GuestX < viewportWidth && fullImageRight > 0

			// Calculate vertical clipping based on FULL image dimensions
			clipTop := 0
			clipBottom := 0
			if anyPartVisible {
				if relativeY < 0 {
					clipTop = -relativeY // Clip rows above viewport
				}
				if fullImageBottom > viewportHeight {
					clipBottom = fullImageBottom - viewportHeight // Clip rows below viewport
				}
			}

			// Clamp to viewport: rows vertically, cols horizontally
			maxShowableRows := min(p.Rows-clipTop-clipBottom, viewportHeight)
			if maxShowableRows <= 0 {
				maxShowableRows = 1
			}
			maxShowableCols := p.Cols
			if fullImageRight > viewportWidth {
				maxShowableCols = viewportWidth - p.GuestX
				if maxShowableCols <= 0 {
					anyPartVisible = false
				}
			}

			actualRelativeY := relativeY
			if clipTop > 0 {
				actualRelativeY = 0
			}
			newHostX := info.WindowX + info.ContentOffsetX + p.GuestX
			newHostY := info.WindowY + info.ContentOffsetY + actualRelativeY

			imageCellWidth := maxShowableCols
			imageCellHeight := maxShowableRows

			// Check if image is occluded by a higher-z window
			if anyPartVisible && kp.isOccludedByHigherWindow(
				newHostX, newHostY, imageCellWidth, imageCellHeight,
				info.WindowZ, allWindows, windowID,
			) {
				kittyPassthroughLog("RefreshPlacement: image occluded by higher-z window, hiding")
				anyPartVisible = false
			}

			// Hide images when host position is out of bounds.
			if anyPartVisible && (newHostX < 0 || newHostY < 0) {
				anyPartVisible = false
			}
			if anyPartVisible && (info.WindowX < 0 || info.WindowY < 0) {
				anyPartVisible = false
			}
			// In native mode, an image whose bottom reaches the last screen row
			// makes the host terminal scroll to make room, and the next frame
			// then places at the same (now scrolled) Y, cascading into duplicate
			// frames (see 2e288e6). The original guard hid any such image, but
			// that blanks a pane whose content legitimately fills to the screen
			// edge  - exactly what a full-window app like terminal-browser or
			// awrit draws every frame. Instead of hiding, clamp the visible
			// rows/cols so the image stops one row short of the bottom edge
			// (keeping the final row free avoids the scroll) and one column short
			// of the right edge. Hide only when nothing fits at all.
			//
			// Inline-graphics mode (web) skips this: the browser overlay clips
			// via CSS overflow:hidden, and clamping there drops rows unnecessarily.
			if !kp.inlineGraphics && anyPartVisible && info.ScreenHeight > 0 {
				maxBottom := info.ScreenHeight - 1 // leave the final row free
				if newHostY+imageCellHeight > maxBottom {
					fit := maxBottom - newHostY
					if fit <= 0 {
						anyPartVisible = false
					} else {
						clipBottom += imageCellHeight - fit
						imageCellHeight = fit
						maxShowableRows = fit
					}
				}
			}
			if !kp.inlineGraphics && anyPartVisible && info.ScreenWidth > 0 {
				if newHostX+imageCellWidth > info.ScreenWidth {
					fit := info.ScreenWidth - newHostX
					if fit <= 0 {
						anyPartVisible = false
					} else {
						imageCellWidth = fit
						maxShowableCols = fit
					}
				}
			}

			kittyPassthroughLog("RefreshPlacement: winXY=(%d,%d) size=(%d,%d) off=(%d,%d) relY=%d, origRows=%d, origCols=%d, vpH=%d, vpW=%d, clipTop=%d, clipBot=%d, maxRows=%d, newHost=(%d,%d), visible=%v",
				info.WindowX, info.WindowY, info.Width, info.Height, info.ContentOffsetX, info.ContentOffsetY,
				relativeY, p.Rows, p.Cols, viewportHeight, viewportWidth, clipTop, clipBottom, maxShowableRows, newHostX, newHostY, anyPartVisible)

			if !anyPartVisible {
				// Send a delete only if the image was currently visible.
				// deleteOnePlacement sends d=p (placement id, image id) so
				// the image bytes stay in storage and a subsequent scroll
				// back into view can re-place without retransmitting.
				if !p.Hidden {
					kp.deleteOnePlacement(p)
					p.Hidden = true
				}
			} else {
				// Re-place only if position/clipping changed. Real kitty
				// and our sip overlay both treat a=p with the same (i, p)
				// as an in-place update of the existing placement.
				posChanged := p.Hidden || p.HostX != newHostX || p.HostY != newHostY ||
					p.ClipTop != clipTop || p.ClipBottom != clipBottom ||
					p.MaxShowable != maxShowableRows || p.MaxShowableCols != maxShowableCols
				if posChanged {
					p.HostX = newHostX
					p.HostY = newHostY
					p.ClipTop = clipTop
					p.ClipBottom = clipBottom
					p.MaxShowable = maxShowableRows
					p.MaxShowableCols = maxShowableCols
					kp.placeOne(p)
				}
				p.Hidden = false
			}
		}

		// Clean up altscreen placements that are no longer needed
		for _, id := range idsToDelete {
			delete(placements, id)
		}
	}
}

func (kp *KittyPassthrough) HasPlacements() bool {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	for _, placements := range kp.placements {
		if len(placements) > 0 {
			return true
		}
	}
	// Self-placed remote video images are not in `placements` but still need the
	// render loop to run its passes: RefreshAllPlacements clears them when a pane
	// leaves its screen (browser quit), and the overlay path hides them.
	for _, ids := range kp.remoteVideo {
		if len(ids) > 0 {
			return true
		}
	}
	return false
}

// SetOverlayActive is called every frame with whether a full-screen overlay
// (help, command palette, etc.) is showing. Entering an overlay hides the
// self-placed video images (a=d with d=i, which drops the placement but keeps
// the image data resident) so the overlay is not drawn behind a live frame;
// while active, forwardFileTransmitInline drops incoming remote frames. Leaving
// the overlay re-shows each image with a=p from the resident data, so it comes
// back immediately without waiting for the browser to send another frame (a
// static page sends none).
func (kp *KittyPassthrough) SetOverlayActive(active bool) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	if active == kp.overlayActive {
		return
	}
	kp.overlayActive = active

	var buf bytes.Buffer
	for _, ids := range kp.remoteVideo {
		for hostID, st := range ids {
			// Images already hidden for geometry reasons (offscreen/occluded)
			// have no on-screen placement to hide, and must not be re-shown
			// when the overlay closes.
			if st.hidden {
				continue
			}
			if active {
				// Hide but keep the image data so a=p can re-show it.
				fmt.Fprintf(&buf, "\x1b_Ga=d,d=i,i=%d,q=2\x1b\\", hostID)
			} else {
				buf.Write(buildVideoReplace(hostID, st))
			}
		}
	}
	if buf.Len() > 0 {
		kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
		kp.flushToHost()
	}
}

// deleteOnePlacement removes the image and all its placements from graphics memory.
// HideAllPlacements hides all visible image placements. Used during resize
// to prevent stale positions. RefreshAllPlacements will re-place them.
func (kp *KittyPassthrough) HideAllPlacements() {
	// In inline-graphics mode (web), the browser overlay manages
	// placement visibility via CSS. Don't send delete commands that
	// would wipe image data from the overlay's storage.
	if kp.inlineGraphics {
		return
	}
	kp.mu.Lock()
	defer kp.mu.Unlock()
	for _, placements := range kp.placements {
		for _, p := range placements {
			if !p.Hidden {
				kp.deleteOnePlacement(p)
				p.Hidden = true
			}
		}
	}
	kp.flushToHost()
}

func (kp *KittyPassthrough) deleteOnePlacement(p *PassthroughPlacement) {
	var buf bytes.Buffer
	buf.WriteString("\x1b_G")
	fmt.Fprintf(&buf, "a=d,d=i,i=%d,q=2\x1b\\", p.HostImageID)
	// Trace caller for debugging
	var caller string
	if pc, _, line, ok := runtime.Caller(1); ok {
		caller = fmt.Sprintf("%s:%d", runtime.FuncForPC(pc).Name(), line)
	}
	kittyPassthroughLog("deleteOnePlacement: hostID=%d caller=%s", p.HostImageID, caller)
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
}

func (kp *KittyPassthrough) placeOne(p *PassthroughPlacement) {
	caps := GetHostCapabilities()
	cellHeight := caps.CellHeight
	if cellHeight <= 0 {
		cellHeight = 20 // Fallback
	}

	// Use a stable, non-zero placement ID so we can delete the previous
	// placement unambiguously before creating a new one. Kitty's a=p with
	// the same (i, p) replaces  - without p, kitty can stack placements.
	if p.PlacementID == 0 {
		p.PlacementID = 1
	}

	var buf bytes.Buffer
	buf.WriteString("\x1b7") // Save cursor position
	fmt.Fprintf(&buf, "\x1b[%d;%dH", p.HostY+1, p.HostX+1)
	buf.WriteString("\x1b_G")
	fmt.Fprintf(&buf, "a=p,i=%d,p=%d", p.HostImageID, p.PlacementID)

	// MaxShowable is already calculated as: p.Rows - clipTop - clipBottom
	// So it already accounts for clipping and is the number of rows to display
	visibleRows := p.MaxShowable
	if visibleRows <= 0 {
		visibleRows = p.DisplayRows
	}
	if visibleRows <= 0 {
		visibleRows = p.Rows
	}
	if visibleRows <= 0 {
		visibleRows = 1 // Minimum 1 row to avoid issues
	}

	kittyPassthroughLog("placeOne: hostID=%d, pos=(%d,%d), origRows=%d, origCols=%d, clipTop=%d, clipBot=%d, visibleRows=%d, srcXYWH=(%d,%d,%d,%d), cellH=%d",
		p.HostImageID, p.HostX, p.HostY, p.Rows, p.Cols, p.ClipTop, p.ClipBottom, visibleRows,
		p.SourceX, p.SourceY, p.SourceWidth, p.SourceHeight, cellHeight)

	// Use clamped cols if the image extends past the viewport
	visibleCols := p.Cols
	if p.MaxShowableCols > 0 && p.MaxShowableCols < visibleCols {
		visibleCols = p.MaxShowableCols
	}
	if visibleCols > 0 {
		fmt.Fprintf(&buf, ",c=%d", visibleCols)
	}
	if visibleRows > 0 {
		fmt.Fprintf(&buf, ",r=%d", visibleRows)
	}

	// Source clipping parameters. Emit the full x,y,w,h rectangle when
	// clipping is needed so kitty crops the source to exactly the visible
	// slice. When combined with c,r, kitty maps that source pixel rect 1:1
	// onto the cell area, avoiding vertical squash.
	//
	// Derive pixels-per-row from the image's ACTUAL native pixel dimensions
	// (from the s/v transmit params) divided by its native cell rows. This is
	// critical in web/daemon mode where the client's host cell height may
	// differ from the daemon's (e.g. client cellH=22 but image was generated
	// at daemon cellH=20 → 380/19=20). Using the client's cellHeight would
	// produce source regions that overflow the image and xterm-addon-image
	// rejects them.
	isClipping := p.ClipTop > 0 || p.ClipBottom > 0 || visibleCols < p.Cols
	pixelsPerRow := cellHeight
	switch {
	case p.Rows > 0 && p.ImagePixelHeight > 0:
		pixelsPerRow = p.ImagePixelHeight / p.Rows
	case p.Rows > 0 && p.SourceHeight > 0:
		pixelsPerRow = p.SourceHeight / p.Rows
	}
	pixelsPerCol := caps.CellWidth
	switch {
	case p.Cols > 0 && p.ImagePixelWidth > 0:
		pixelsPerCol = p.ImagePixelWidth / p.Cols
	case p.Cols > 0 && p.SourceWidth > 0:
		pixelsPerCol = p.SourceWidth / p.Cols
	}
	switch {
	case isClipping:
		srcX := p.SourceX
		srcY := p.SourceY + p.ClipTop*pixelsPerRow
		srcW := p.SourceWidth
		if srcW == 0 && pixelsPerCol > 0 {
			srcW = p.Cols * pixelsPerCol
		}
		// Horizontal crop: if columns were clamped, crop source width
		if visibleCols < p.Cols && pixelsPerCol > 0 {
			srcW = visibleCols * pixelsPerCol
		}
		srcH := visibleRows * pixelsPerRow
		// Clamp against the image's native pixel height so we never request
		// a source region that overflows the image  - xterm-addon-image rejects
		// such requests (real kitty silently clamps).
		if p.ImagePixelHeight > 0 && srcY+srcH > p.ImagePixelHeight {
			srcH = max(p.ImagePixelHeight-srcY, 0)
		}
		if p.ImagePixelWidth > 0 && srcX+srcW > p.ImagePixelWidth {
			srcW = max(p.ImagePixelWidth-srcX, 0)
		}
		fmt.Fprintf(&buf, ",x=%d,y=%d,w=%d,h=%d", srcX, srcY, srcW, srcH)
	case p.SourceWidth > 0 || p.SourceHeight > 0:
		if p.SourceX > 0 {
			fmt.Fprintf(&buf, ",x=%d", p.SourceX)
		}
		if p.SourceY > 0 {
			fmt.Fprintf(&buf, ",y=%d", p.SourceY)
		}
		if p.SourceWidth > 0 {
			fmt.Fprintf(&buf, ",w=%d", p.SourceWidth)
		}
		if p.SourceHeight > 0 {
			fmt.Fprintf(&buf, ",h=%d", p.SourceHeight)
		}
	}
	if p.XOffset > 0 {
		fmt.Fprintf(&buf, ",X=%d", p.XOffset)
	}
	if p.YOffset > 0 {
		fmt.Fprintf(&buf, ",Y=%d", p.YOffset)
	}
	if p.ZIndex != 0 {
		fmt.Fprintf(&buf, ",z=%d", p.ZIndex)
	}
	// Note: Don't send U=1 to host - TUIOS renders guest content itself
	buf.WriteString(",q=2\x1b\\")
	buf.WriteString("\x1b8") // Restore cursor position
	kittyPassthroughLog("placeOne: emitted kitty cmd: %q", buf.String())
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
}
