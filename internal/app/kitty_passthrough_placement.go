package app

import (
	"bytes"
	"fmt"
	"math"
	"runtime"
	"slices"
)

func rectsOverlap(x1, y1, w1, h1, x2, y2, w2, h2 int) bool {
	return x1 < x2+w2 && x1+w1 > x2 && y1 < y2+h2 && y1+h1 > y2
}

func (kp *KittyPassthrough) isOccludedByHigherWindow(
	screenX, screenY, width, height, windowZ int,
	allWindows map[string]*WindowPositionInfo,
	excludeWindowID string,
) bool {
	for id, info := range allWindows {
		if id == excludeWindowID || !info.Visible || info.WindowZ <= windowZ {
			continue
		}
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

	kp.releaseDueHeld(true)

	replaceAll := kp.hostScreenErased.Swap(false)

	allWindows := getAllWindows()

	for _, info := range allWindows {
		if info.ScreenWidth > 0 && info.ScreenHeight > 0 {
			kp.screenWidth = info.ScreenWidth
			kp.screenHeight = info.ScreenHeight
			break
		}
	}

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
		cw, chh := paneContentCells(info)
		prev, seen := kp.resizeFreezeSize[windowID]
		kp.resizeFreezeSize[windowID] = [2]int{cw, chh}
		frozen[windowID] = info.IsBeingManipulated && seen &&
			(prev[0] != cw || prev[1] != chh)
	}
	for windowID, placements := range kp.placements {
		if len(placements) > 0 {
			markFreeze(windowID)
		}
	}
	for windowID := range kp.remoteVideo {
		markFreeze(windowID)
	}

	for windowID, ids := range kp.remoteVideo {
		info := allWindows[windowID]
		if info != nil && frozen[windowID] {
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

			boxX0, boxY0, boxX1, boxY1 := info.placementBounds()
			showCols, showRows := st.cols, st.rows
			visible := info.Visible && newHostX >= boxX0 && newHostY >= boxY0 &&
				info.WindowX >= 0 && info.WindowY >= 0
			if visible && newHostX+showCols > boxX1 {
				showCols = boxX1 - newHostX
			}
			if visible && info.ScreenHeight > 0 && newHostY+showRows > info.ScreenHeight-1 {
				showRows = info.ScreenHeight - 1 - newHostY
			}
			if visible && newHostY+showRows > boxY1 {
				showRows = boxY1 - newHostY
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
			if visible && kp.overlapsChrome(cellRect{newHostX, newHostY, showCols, showRows}) {
				visible = false
			}

			changed := replaceAll || st.hidden || newHostX != st.hostX || newHostY != st.hostY ||
				showCols != st.showCols || showRows != st.showRows
			st.hostX, st.hostY = newHostX, newHostY
			st.showCols, st.showRows = showCols, showRows

			if !visible {
				if !st.hidden {
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
		tuiosMoved := false
		if info != nil {
			g := geomOf(info)
			if kp.heldGeom == nil {
				kp.heldGeom = make(map[string]paneGeom)
			}
			prev, seen := kp.heldGeom[windowID]
			tuiosMoved = seen && prev != g
			kp.heldGeom[windowID] = g
		}
		if info == nil {
			kp.deleteWindowImages(windowID, true)
			kp.dropWindowPlacements(windowID)
			delete(kp.resizeFreezeSize, windowID)
			continue
		}

		kittyPassthroughLog("RefreshAllPlacements: windowID=%s, IsAltScreen=%v, visible=%v", windowID[:min(8, len(windowID))], info.IsAltScreen, info.Visible)

		if frozen[windowID] {
			continue
		}

		viewportTop := info.ScrollbackLen - info.ScrollOffset
		viewportWidth, viewportHeight := paneContentCells(info)

		blockers := kp.occludersAboveInto(kp.occluderScratch[:0], info.WindowZ, allWindows, windowID)
		blockers = append(blockers, kp.chromeOccluders...)
		kp.occluderScratch = blockers[:0]
		sliceLimit := min(maxVisibleSlices+3*len(kp.chromeOccluders), int(placementStride))
		pieces := kp.sliceScratch[:0]

		var idsToDelete []placementKey

		for key, p := range placements {
			hostID := key.host
			if key.slot != 0 && p.AbsoluteLine+max(p.Rows, 1) <= 0 {
				kp.evictPlacement(windowID, key)
				continue
			}
			if info.IsAltScreen != p.PlacedOnAltScreen {
				kittyPassthroughLog("RefreshPlacement: altscreen mismatch (info=%v, placed=%v)",
					info.IsAltScreen, p.PlacedOnAltScreen)
				if !p.Hidden {
					kp.deleteOnePlacement(p)
					p.Hidden = true
				}
				if !info.IsAltScreen && p.PlacedOnAltScreen {
					kittyPassthroughLog("RefreshPlacement: cleaning up altscreen placement hostID=%d", hostID)
					idsToDelete = append(idsToDelete, key)
				}
				continue
			}

			relativeY := p.AbsoluteLine - viewportTop

			fullImageBottom := relativeY + p.Rows
			fullImageRight := p.GuestX + p.Cols

			anyPartVisible := info.Visible &&
				relativeY < viewportHeight && fullImageBottom > 0 &&
				p.GuestX < viewportWidth && fullImageRight > 0

			clipTop := 0
			clipBottom := 0
			if anyPartVisible {
				if relativeY < 0 {
					clipTop = -relativeY
				}
				if fullImageBottom > viewportHeight {
					clipBottom = fullImageBottom - viewportHeight
				}
			}

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

			boxX0, boxY0, boxX1, boxY1 := info.placementBounds()
			clipLeft := 0
			if anyPartVisible {
				if crop := boxX0 - newHostX; crop > 0 {
					newHostX += crop
					clipLeft = crop
					imageCellWidth -= crop
					maxShowableCols = imageCellWidth
				}
				if crop := boxY0 - newHostY; crop > 0 {
					newHostY += crop
					clipTop += crop
					imageCellHeight -= crop
					maxShowableRows = imageCellHeight
				}
				if over := newHostX + imageCellWidth - boxX1; over > 0 {
					imageCellWidth -= over
					maxShowableCols = imageCellWidth
				}
				if over := newHostY + imageCellHeight - boxY1; over > 0 {
					clipBottom += over
					imageCellHeight -= over
					maxShowableRows = imageCellHeight
				}
				if imageCellWidth <= 0 || imageCellHeight <= 0 {
					anyPartVisible = false
				}
			}

			if anyPartVisible && (newHostX < 0 || newHostY < 0) {
				anyPartVisible = false
			}
			if anyPartVisible && (info.WindowX < 0 || info.WindowY < 0) {
				anyPartVisible = false
			}
			if !kp.inlineGraphics && anyPartVisible && info.ScreenHeight > 0 {
				maxBottom := info.ScreenHeight - 1
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

			pieces = pieces[:0]
			if anyPartVisible {
				whole := cellRect{newHostX, newHostY, imageCellWidth, imageCellHeight}
				region, exact := clearRegion(kp.regionScratch[:0], whole, blockers, sliceLimit)
				kp.regionScratch = region
				if !exact {
					if one, ok := largestClearRect(whole, blockers); ok {
						region = append(region[:0], one)
					} else {
						region = region[:0]
					}
				}
				if len(region) == 0 {
					kittyPassthroughLog("RefreshPlacement: image fully covered by a higher window, hiding")
					anyPartVisible = false
				} else {
					for _, r := range region {
						pieces = append(pieces, placementSlice{
							HostX:      r.X,
							HostY:      r.Y,
							ClipTop:    clipTop + r.Y - newHostY,
							ClipLeft:   clipLeft + r.X - newHostX,
							ClipBottom: clipBottom + (newHostY + imageCellHeight) - (r.Y + r.H),
							Cols:       r.W,
							Rows:       r.H,
						})
					}
					newHostX, newHostY = pieces[0].HostX, pieces[0].HostY
					clipTop, clipLeft = pieces[0].ClipTop, pieces[0].ClipLeft
					clipBottom = pieces[0].ClipBottom
					maxShowableCols, maxShowableRows = pieces[0].Cols, pieces[0].Rows
				}
			}

			kittyPassthroughLog("RefreshPlacement: winXY=(%d,%d) size=(%d,%d) off=(%d,%d) relY=%d, origRows=%d, origCols=%d, vpH=%d, vpW=%d, box=(%d,%d)-(%d,%d), clipTop=%d, clipBot=%d, clipLeft=%d, maxRows=%d, maxCols=%d, newHost=(%d,%d), visible=%v",
				info.WindowX, info.WindowY, info.Width, info.Height, info.ContentOffsetX, info.ContentOffsetY,
				relativeY, p.Rows, p.Cols, viewportHeight, viewportWidth, boxX0, boxY0, boxX1, boxY1,
				clipTop, clipBottom, clipLeft, maxShowableRows, maxShowableCols, newHostX, newHostY, anyPartVisible)

			if !anyPartVisible {
				kp.dropHeldPlacements(hostID)
				if !p.Hidden {
					kp.deleteOnePlacement(p)
					p.Hidden = true
				}
			} else {
				if p.sentAsIs {
					p.sentAsIs = false
					if !replaceAll && !p.Hidden && !p.DataDirty && len(pieces) == 1 &&
						clipTop == 0 && clipBottom == 0 && clipLeft == 0 &&
						newHostX == p.HostX && newHostY == p.HostY &&
						maxShowableCols == p.Cols && maxShowableRows == p.DisplayRows {
						p.Slices = append(p.Slices[:0], pieces...)
						p.MaxShowable, p.MaxShowableCols = maxShowableRows, maxShowableCols
						p.placedSlices = 1
						continue
					}
				}
				posChanged := replaceAll || p.Hidden || p.DataDirty || p.HostX != newHostX || p.HostY != newHostY ||
					p.ClipTop != clipTop || p.ClipBottom != clipBottom || p.ClipLeft != clipLeft ||
					p.MaxShowable != maxShowableRows || p.MaxShowableCols != maxShowableCols ||
					!slices.Equal(p.Slices, pieces)
				if posChanged {
					p.Slices = append(p.Slices[:0], pieces...)
					p.HostX = newHostX
					p.HostY = newHostY
					p.ClipTop = clipTop
					p.ClipBottom = clipBottom
					p.ClipLeft = clipLeft
					p.MaxShowable = maxShowableRows
					p.MaxShowableCols = maxShowableCols
					start := len(kp.pendingOutput)
					kp.placeSlices(p)
					kp.holdTail(windowID, hostID, start, tuiosMoved)
				}
				p.DataDirty = false
				p.Hidden = false
			}
		}

		for _, id := range idsToDelete {
			kp.dropPlacement(windowID, id)
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
			if st.hidden {
				continue
			}
			if active {
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

// SetChromeOccluders sets the chrome drawn over the panes that images must be
// cropped around, and is called every frame. Nil means none.
//
// This is for chrome that leaves the panes showing, such as capture mode's
// hint strip and marquee. Hiding every image for it, the way an opaque overlay
// does, takes the picture away from the very pane the user is aiming at. A
// kitty image is painted by the host over the finished frame, so an image left
// whole would paint over the chrome instead. Cropping it around the chrome
// rectangles, like around a higher window, keeps both.
func (kp *KittyPassthrough) SetChromeOccluders(rects []cellRect) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	kp.chromeOccluders = append(kp.chromeOccluders[:0], rects...)
}

func (kp *KittyPassthrough) replaceChromeOccluders(rects []cellRect) bool {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	if slices.Equal(kp.chromeOccluders, rects) {
		return false
	}
	kp.chromeOccluders = append(kp.chromeOccluders[:0], rects...)
	return kp.enabled && !kp.overlayActive
}

func (kp *KittyPassthrough) overlapsChrome(r cellRect) bool {
	return slices.ContainsFunc(kp.chromeOccluders, r.overlaps)
}

// HideAllPlacements hides all visible image placements. Used during resize
// to prevent stale positions. RefreshAllPlacements will re-place them.
func (kp *KittyPassthrough) HideAllPlacements() {
	if kp.inlineGraphics {
		return
	}
	kp.mu.Lock()
	defer kp.mu.Unlock()
	for _, placements := range kp.placements {
		for _, p := range placements {
			kp.dropHeldPlacements(p.HostImageID)
			if !p.Hidden {
				kp.deleteOnePlacement(p)
				p.Hidden = true
			}
		}
	}
	kp.flushToHost()
}

func (kp *KittyPassthrough) deleteOnePlacement(p *PassthroughPlacement) {
	if p.slot != 0 || kp.sharesImage(p) {
		kp.deletePlacementIDs(p)
		return
	}
	if !kp.guestCapture {
		kp.dropHeldPlacements(p.HostImageID)
	}
	var buf bytes.Buffer
	buf.WriteString("\x1b_G")
	fmt.Fprintf(&buf, "a=d,d=i,i=%d,q=2\x1b\\", p.HostImageID)
	var caller string
	if pc, _, line, ok := runtime.Caller(1); ok {
		caller = fmt.Sprintf("%s:%d", runtime.FuncForPC(pc).Name(), line)
	}
	kittyPassthroughLog("deleteOnePlacement: hostID=%d caller=%s", p.HostImageID, caller)
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
}

func (kp *KittyPassthrough) placeSlices(p *PassthroughPlacement) {
	if len(p.Slices) == 0 {
		kp.placeOne(p)
		p.placedSlices = 1
		return
	}

	base := kp.placementBaseFor(p)
	saved := placementSlice{
		HostX: p.HostX, HostY: p.HostY,
		ClipTop: p.ClipTop, ClipLeft: p.ClipLeft, ClipBottom: p.ClipBottom,
		Cols: p.MaxShowableCols, Rows: p.MaxShowable,
	}
	for i, sl := range p.Slices {
		p.HostX, p.HostY = sl.HostX, sl.HostY
		p.ClipTop, p.ClipLeft, p.ClipBottom = sl.ClipTop, sl.ClipLeft, sl.ClipBottom
		p.MaxShowableCols, p.MaxShowable = sl.Cols, sl.Rows
		p.PlacementID = base + uint32(i)
		kp.placeOne(p)
	}
	for i := len(p.Slices); i < p.placedSlices; i++ {
		kp.deleteOneSlice(p, base+uint32(i))
	}
	p.placedSlices = len(p.Slices)

	p.PlacementID = base
	p.HostX, p.HostY = saved.HostX, saved.HostY
	p.ClipTop, p.ClipLeft, p.ClipBottom = saved.ClipTop, saved.ClipLeft, saved.ClipBottom
	p.MaxShowableCols, p.MaxShowable = saved.Cols, saved.Rows
}

func (kp *KittyPassthrough) deleteOneSlice(p *PassthroughPlacement, placementID uint32) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "\x1b_Ga=d,d=i,i=%d,p=%d,q=2\x1b\\", p.HostImageID, placementID)
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
}

func (kp *KittyPassthrough) placeOne(p *PassthroughPlacement) {
	caps := kp.hostCaps()
	cellHeight := caps.CellHeight
	if cellHeight <= 0 {
		cellHeight = 20
	}

	kp.placementBaseFor(p)

	var buf bytes.Buffer
	buf.WriteString("\x1b7")
	fmt.Fprintf(&buf, "\x1b[%d;%dH", p.HostY+1, p.HostX+1)
	buf.WriteString("\x1b_G")
	fmt.Fprintf(&buf, "a=p,i=%d,p=%d,C=1", p.HostImageID, p.PlacementID)

	visibleRows := p.MaxShowable
	if visibleRows <= 0 {
		visibleRows = p.DisplayRows
	}
	if visibleRows <= 0 {
		visibleRows = p.Rows
	}
	if visibleRows <= 0 {
		visibleRows = 1
	}

	kittyPassthroughLog("placeOne: hostID=%d, pos=(%d,%d), origRows=%d, origCols=%d, clipTop=%d, clipBot=%d, visibleRows=%d, srcXYWH=(%d,%d,%d,%d), cellH=%d",
		p.HostImageID, p.HostX, p.HostY, p.Rows, p.Cols, p.ClipTop, p.ClipBottom, visibleRows,
		p.SourceX, p.SourceY, p.SourceWidth, p.SourceHeight, cellHeight)

	visibleCols := p.Cols
	if p.MaxShowableCols > 0 && p.MaxShowableCols < visibleCols {
		visibleCols = p.MaxShowableCols
	}
	if visibleCols > 0 && !p.Native {
		fmt.Fprintf(&buf, ",c=%d", visibleCols)
	}
	if visibleRows > 0 && !p.Native {
		fmt.Fprintf(&buf, ",r=%d", visibleRows)
	}

	imageCols := p.ImageCols
	if imageCols <= 0 {
		imageCols = p.Cols
	}
	isClipping := p.ClipTop > 0 || p.ClipBottom > 0 || p.ClipLeft > 0 ||
		visibleCols < imageCols || visibleRows < p.Rows
	spanW := sourceSpan(p.ImagePixelWidth, p.SourceX, p.SourceWidth)
	spanH := sourceSpan(p.ImagePixelHeight, p.SourceY, p.SourceHeight)
	colEdge := func(n int) int {
		if imageCols > 0 && spanW > 0 {
			return int(math.Round(float64(n) * float64(spanW) / float64(imageCols)))
		}
		return n * caps.CellWidth
	}
	rowEdge := func(n int) int {
		if p.Rows > 0 && spanH > 0 {
			return int(math.Round(float64(n) * float64(spanH) / float64(p.Rows)))
		}
		return n * cellHeight
	}
	offsetX, offsetY := p.XOffset, p.YOffset
	switch {
	case isClipping && p.Native:
		left, top := 0, 0
		if p.ClipLeft > 0 {
			left = p.ClipLeft*caps.CellWidth - offsetX
			offsetX = 0
		}
		if p.ClipTop > 0 {
			top = p.ClipTop*cellHeight - offsetY
			offsetY = 0
		}
		srcW := min(visibleCols*caps.CellWidth-offsetX, spanW-left)
		srcH := min(visibleRows*cellHeight-offsetY, spanH-top)
		fmt.Fprintf(&buf, ",x=%d,y=%d,w=%d,h=%d", p.SourceX+left, p.SourceY+top, max(srcW, 0), max(srcH, 0))
	case isClipping:
		srcX := p.SourceX + colEdge(p.ClipLeft)
		srcY := p.SourceY + rowEdge(p.ClipTop)
		srcW := spanW
		if srcW == 0 {
			srcW = colEdge(imageCols)
		}
		if visibleCols < imageCols {
			srcW = colEdge(p.ClipLeft+visibleCols) - colEdge(p.ClipLeft)
		}
		srcH := rowEdge(p.ClipTop+visibleRows) - rowEdge(p.ClipTop)
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
	if offsetX > 0 {
		fmt.Fprintf(&buf, ",X=%d", offsetX)
	}
	if offsetY > 0 {
		fmt.Fprintf(&buf, ",Y=%d", offsetY)
	}
	if p.ZIndex != 0 {
		fmt.Fprintf(&buf, ",z=%d", p.ZIndex)
	}
	buf.WriteString(",q=2\x1b\\")
	buf.WriteString("\x1b8")
	kittyPassthroughLog("placeOne: emitted kitty cmd: %q", buf.String())
	kp.pendingOutput = append(kp.pendingOutput, buf.Bytes()...)
}
