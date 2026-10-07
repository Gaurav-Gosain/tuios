package terminal

import (
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
)

// PixelInsets is the room a pixel renderer keeps around each pane's text for
// its own chrome: a header above, and padding on the other sides. Each inset
// is in device pixels and is measured from the pane's slot, which is the
// pane's cells plus the gap after it on each axis (half a gap column on each
// side, and the gap row above). The renderer draws its splits on the slot
// edges, so the insets are the distance from a split to the text.
type PixelInsets struct {
	CellWidth, CellHeight    int
	Top, Left, Right, Bottom int
}

var pixelInsets atomic.Pointer[PixelInsets]

// SetPixelInsets makes every pane's guest size the room the insets leave
// inside its slot. Nil, or a zero cell size, turns it off. Only a process
// that serves a pixel renderer (tuios gui-bridge) sets it.
func SetPixelInsets(in *PixelInsets) {
	if in != nil && (in.CellWidth <= 0 || in.CellHeight <= 0) {
		in = nil
	}
	pixelInsets.Store(in)
}

// InsetCells is the number of whole cells left in a slot of n cells plus
// one gap cell once the two insets are taken off.
func InsetCells(n, cell, before, after int) int {
	return max(((n+1)*cell-before-after)/cell, 1)
}

// contentSize is the drawable box inside an outer rectangle of the given
// dimensions. Every consumer of a pane's inner size derives it from here: the
// renderer's clip (ContentWidth/ContentHeight), the emulator grid and the PTY
// announcement (Resize, ResizeVisual). One formula, so what the guest is told
// and what it gets to draw in cannot drift apart.
func (w *Window) contentSize(width, height int) (int, int) {
	d := 2 * w.BorderOffset()
	cw, ch := max(width-d, 1), max(height-d, 1)
	if in := pixelInsets.Load(); in != nil {
		cw = InsetCells(cw, in.CellWidth, in.Left, in.Right)
		ch = InsetCells(ch, in.CellHeight, in.Top, in.Bottom)
	}
	return cw, ch
}

// ContentWidth returns the usable content width (excluding borders if not tiled).
func (w *Window) ContentWidth() int {
	cw, _ := w.contentSize(w.Width, w.Height)
	return cw
}

// ContentHeight returns the usable content height (excluding borders if not tiled).
func (w *Window) ContentHeight() int {
	_, ch := w.contentSize(w.Width, w.Height)
	return ch
}

// BorderOffset returns the number of cells used by each border edge.
// Returns 0 for tiled windows (no individual borders), 1 otherwise.
func (w *Window) BorderOffset() int {
	if w.Tiled {
		return 0
	}
	return 1
}

// GeometrySnapshot is a self-consistent copy of the window's on-screen
// geometry, published by the goroutine that owns window layout (the update
// loop) and read by PTY-reader callbacks (the kitty/sixel passthroughs) that
// must not touch the live fields while the update loop mutates them.
type GeometrySnapshot struct {
	X, Y, Width, Height, BorderOffset int
	// ContentW/ContentH are the cells the guest has actually been told it has,
	// which is not always Width and Height less the border allowance. Those two
	// move apart whenever the layout changes a pane's drawable area before
	// telling it: a live resize drag moves the rectangle every frame and defers
	// the announcement to the gesture's end, and a pane dragged out of a
	// shared-border layout drops its borderless allowance without moving the
	// rectangle at all.
	//
	// Anything measuring an image against its pane wants this pair and not the
	// other one. The guest draws to the size it was given, so a bitmap can only
	// ever match this rectangle; measuring against a rectangle the guest has
	// never heard of is measuring the bitmap against a box it was not drawn for,
	// and the difference is the scale factor kitty applies to it.
	ContentW, ContentH int
}

// PublishGeometry records the current geometry for cross-goroutine readers.
// It must be called from the goroutine that mutates geometry (the update
// loop); the render path republishes every frame, so a snapshot is never more
// than a frame stale, and every placement is re-laid out per frame anyway.
func (w *Window) PublishGeometry() {
	cw, ch := w.AnnouncedSize()
	if cw <= 0 || ch <= 0 {
		// Nothing announced yet, so the rectangle is the best answer available.
		cw, ch = w.contentSize(w.Width, w.Height)
	}
	cur := w.geomSnap.Load()
	if cur != nil && cur.X == w.X && cur.Y == w.Y &&
		cur.Width == w.Width && cur.Height == w.Height &&
		cur.BorderOffset == w.BorderOffset() &&
		cur.ContentW == cw && cur.ContentH == ch {
		return
	}
	w.geomSnap.Store(&GeometrySnapshot{
		X: w.X, Y: w.Y, Width: w.Width, Height: w.Height,
		BorderOffset: w.BorderOffset(),
		ContentW:     cw, ContentH: ch,
	})
}

// LastGeometry returns the most recently published geometry snapshot. The
// zero snapshot is returned if none has been published yet; NewWindow
// publishes before the PTY reader starts, so callbacks always see real
// geometry.
func (w *Window) LastGeometry() GeometrySnapshot {
	if g := w.geomSnap.Load(); g != nil {
		return *g
	}
	return GeometrySnapshot{}
}

// ScreenToTerminal converts screen coordinates (X, Y) to terminal-relative coordinates.
// Returns the terminal X, Y and whether the coordinates are within the content area.
func (w *Window) ScreenToTerminal(screenX, screenY int) (termX, termY int, ok bool) {
	off := w.BorderOffset()
	termX = screenX - w.X - off
	termY = screenY - w.Y - off
	ok = termX >= 0 && termY >= 0 && termX < w.ContentWidth() && termY < w.ContentHeight()
	return
}

// SeedAnnouncedSize records the emulator size the guest already believes it has,
// so a later Resize to that same size does not re-announce it. On reattach the
// daemon PTY is already at this size; re-announcing it SIGWINCHes the shell and
// makes it repaint its prompt over the restored screen.
//
// Only call this when the real PTY is known to already carry the size being
// recorded. Seeding a size the PTY does not have is indistinguishable from
// having announced it, and Resize will skip the announcement that would have
// made it true.
func (w *Window) SeedAnnouncedSize(width, height int) {
	w.announcedW, w.announcedH = width, height
	w.toldW, w.toldH = width, height
}

// AnnouncedSize returns the emulator size last handed downstream.
func (w *Window) AnnouncedSize() (int, int) {
	return w.announcedW, w.announcedH
}

// HoldAnnouncements stops Resize from telling the guest anything until the
// matching ReleaseAnnouncements, which then sends the settled size if it
// differs from what the guest already has.
//
// One layout update is several steps: settle the border allowance, place the
// rectangle, reclaim the columns a divider was holding, drain a deferred
// resize. Each step used to reach the guest as its own SIGWINCH, and a
// full-screen program repaints on every one, so a switch that left a pane
// exactly the size it started at still cost it two full repaints. Only the
// size the pane settles at was ever real.
//
// Holds nest, so a caller must pair every Hold with exactly one Release. A
// mouse gesture holds for its whole length and every retile inside it holds
// again; only the last release sends anything.
func (w *Window) HoldAnnouncements() { w.announceHolds++ }

// ReleaseAnnouncements ends one hold. The size the pane settled at is sent
// when the last hold ends; an inner release only drops the depth.
func (w *Window) ReleaseAnnouncements() {
	if w.announceHolds == 0 {
		return
	}
	w.announceHolds--
	if w.announceHolds > 0 {
		return
	}
	if w.announcedW != w.toldW || w.announcedH != w.toldH {
		w.tellGuest(w.announcedW, w.announcedH)
	}
}

// AnnounceTrace, when set, is called with every size handed to a guest, from
// the goroutine that hands it over. It is a diagnostic hook for the client's
// render trace, which records the size and the code path that announced it,
// so a pane whose shell repaints on a focus move can say what resized it. Nil
// in a normal run, where tellGuest pays one nil check per real resize.
var AnnounceTrace func(w *Window, cols, rows int)

// tellGuest sends one size downstream. Both the local PTY and the daemon turn
// it into a SIGWINCH, so it is only ever called for a size the guest does not
// already have. Re-sending an unchanged size makes the shell repaint its
// prompt for nothing, which is what stacked prompts on a same-size session
// switch were.
func (w *Window) tellGuest(termWidth, termHeight int) {
	w.toldW, w.toldH = termWidth, termHeight
	if AnnounceTrace != nil {
		AnnounceTrace(w, termWidth, termHeight)
	}
	if w.Pty != nil {
		// Cells and pixels in one winsize write, so the kernel signals the
		// guest once for it. See ptyspawn.SetWinsize.
		var xpixel, ypixel int
		if w.CellPixelWidth > 0 && w.CellPixelHeight > 0 {
			xpixel = termWidth * w.CellPixelWidth
			ypixel = termHeight * w.CellPixelHeight
		}
		_ = ptyspawn.SetWinsize(w.Pty, termWidth, termHeight, xpixel, ypixel)
		// The kernel signals the tty's foreground process group. This also
		// signals the shell itself, which the kernel does not while a job
		// holds the foreground.
		w.TriggerRedraw()
	} else if w.DaemonMode && w.DaemonResizeFunc != nil {
		// In daemon mode, use the resize callback to notify the daemon
		if err := w.DaemonResizeFunc(termWidth, termHeight); err != nil {
			_ = err // Acknowledge error but don't break functionality
		}
	}
}

func (w *Window) Resize(width, height int) {
	if w.Terminal == nil {
		return
	}

	termWidth, termHeight := w.contentSize(width, height)

	// Did anything downstream actually change? Measured against what was last
	// announced, not against Width and Height: a deferred resize has already
	// applied those visually, and comparing with them makes the announcement
	// look redundant exactly when it is not.
	sizeChanged := termWidth != w.announcedW || termHeight != w.announcedH
	w.announcedW, w.announcedH = termWidth, termHeight

	// A pane fed by a daemon subscription is sized by that stream, so the grid
	// changes width at the byte the daemon's own emulator changed width at.
	// Resizing it here instead laid out everything the guest produced between
	// this client asking and the daemon hearing at a width the daemon never
	// used, and a line that wrapped differently stays in the scrollback.
	// See docs/REHYDRATION.md.
	if !w.streamOwnsSize.Load() {
		// ioMu serializes the emulator buffer reallocation against the render
		// reader (RLockIO) and the PTY writers; Terminal has no lock of its own.
		// TriggerRedraw below takes ioMu.RLock, so the lock is scoped to the resize.
		w.ioMu.Lock()
		// Re-check under the lock: the guard at the top of Resize runs unlocked
		// and Close() nils Terminal while holding this lock.
		if w.Terminal != nil {
			w.Terminal.Resize(termWidth, termHeight)
		}
		w.ioMu.Unlock()
	}
	if sizeChanged && w.announceHolds == 0 {
		w.tellGuest(termWidth, termHeight)
	}
	w.Width = width
	w.Height = height

	// Mark both position and content dirty for resize operations
	w.MarkPositionDirty()
	w.MarkContentDirty()
}

// ResizeVisual updates the window dimensions without triggering PTY resize.
// This is used during mouse drag to provide immediate visual feedback while
// deferring expensive PTY resize operations until the drag completes.
//
// A pane fed by a daemon subscription keeps its emulator at the size the
// stream gave it, as Resize does: the renderer clips or pads the grid to the
// pane, and the emulator changes size when the daemon's own does, at the same
// byte. Resizing it at every motion step reflowed the client through widths
// the daemon never had, so a drag left the client's screen and history laid
// out differently from the daemon's, most visibly under a program that parks
// its cursor above a footer. See docs/REHYDRATION.md. A local pane has no
// other copy, and its emulator follows the drag.
func (w *Window) ResizeVisual(width, height int) {
	w.Width = width
	w.Height = height

	if w.Terminal != nil && !w.streamOwnsSize.Load() {
		termWidth, termHeight := w.contentSize(width, height)
		// ioMu serializes the buffer reallocation with the render reader and
		// PTY writers; Terminal has no lock of its own.
		w.ioMu.Lock()
		// Re-check under the lock; Close() nils Terminal while holding it.
		if w.Terminal != nil {
			w.Terminal.Resize(termWidth, termHeight)
		}
		w.ioMu.Unlock()
	}

	w.MarkPositionDirty()
	// Note: NOT marking ContentDirty to preserve cached content during drag
	// This improves responsiveness during resize operations
}

// SetCellPixelDimensions sets the cell pixel dimensions for the window.
// This is used to report accurate pixel dimensions to child processes via TIOCGWINSZ.
// Call this after window creation with the host terminal's cell dimensions.
func (w *Window) SetCellPixelDimensions(cellWidth, cellHeight int) {
	w.CellPixelWidth = cellWidth
	w.CellPixelHeight = cellHeight

	w.Terminal.SetCellSize(cellWidth, cellHeight)

	// Only a pty that can carry pixels is written. Anything else would only be
	// resized to the size it already has.
	if ws, ok := w.Pty.(ptyspawn.WinsizeSetter); ok && cellWidth > 0 && cellHeight > 0 {
		termWidth := w.ContentWidth()
		termHeight := w.ContentHeight()
		_ = ws.SetWinsize(termWidth, termHeight, termWidth*cellWidth, termHeight*cellHeight)
	}
}

// MarkPositionDirty marks the window position as dirty.
func (w *Window) MarkPositionDirty() {
	w.Dirty = true
	w.PositionDirty = true
	// Position changes invalidate the cached layer but NOT the content cache
	// This allows us to keep the expensive terminal content rendering
	w.CachedLayer = nil
	// Don't clear w.CachedContent here. Keep it for performance.
}

// SetCachedContentDim records the dim percentage CachedContent was rendered
// at, and CachedContentDim reads it back. Zero is an undimmed frame, which is
// what a focused pane and an unconfigured dim both produce.
//
// It sits beside the cached size for the same reason: the cache is only usable
// by a caller wanting the frame it actually holds, and focus is now one of the
// things that decides what that frame looks like.
func (w *Window) SetCachedContentDim(dim int) { w.cachedContentDim = dim }

// CachedContentDim is the dim CachedContent was rendered at.
func (w *Window) CachedContentDim() int { return w.cachedContentDim }

// MarkContentDirty marks the window content as dirty.
func (w *Window) MarkContentDirty() {
	w.Dirty = true
	w.ContentDirty = true
	// Invalidate the content cache so the next render re-reads the emulator.
	// ContentDirty already forces that re-render, so CachedLayer is deliberately
	// kept: retaining the last complete layer lets the renderer hold it while the
	// guest is mid-frame in a synchronized update (DEC 2026), instead of showing
	// a half-drawn buffer. It is replaced on the next render and invalidated by
	// MarkPositionDirty, retiling, and close.
	w.CachedContent = ""
	w.CachedContentCols, w.CachedContentRows = 0, 0
}

// ClearDirtyFlags clears all dirty flags.
func (w *Window) ClearDirtyFlags() {
	w.Dirty = false
	w.ContentDirty = false
	w.PositionDirty = false
}

// InvalidateCache invalidates the cached content.
func (w *Window) InvalidateCache() {
	w.CachedLayer = nil
	w.CachedContent = ""
	w.CachedContentCols, w.CachedContentRows = 0, 0
}

// LastContentRow is the bottom row of the content area, the lowest row a copy
// mode cursor can sit on. It is ContentHeight()-1 and not Height-3: a
// borderless tiled pane draws on every row of its Height.
func (w *Window) LastContentRow() int {
	return max(w.ContentHeight()-1, 0)
}

// LastContentCol is the rightmost column of the content area, the furthest a
// copy mode cursor can go. See LastContentRow.
func (w *Window) LastContentCol() int {
	return max(w.ContentWidth()-1, 0)
}
