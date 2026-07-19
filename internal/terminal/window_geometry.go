package terminal

// ContentWidth returns the usable content width (excluding borders if not tiled).
func (w *Window) ContentWidth() int {
	if w.Tiled {
		return max(w.Width, 1)
	}
	return max(w.Width-2, 1)
}

// ContentHeight returns the usable content height (excluding borders if not tiled).
func (w *Window) ContentHeight() int {
	if w.Tiled {
		return max(w.Height, 1)
	}
	return max(w.Height-2, 1)
}

// BorderOffset returns the number of cells used by each border edge.
// Returns 0 for tiled windows (no individual borders), 1 otherwise.
func (w *Window) BorderOffset() int {
	if w.Tiled {
		return 0
	}
	return 1
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

func (w *Window) Resize(width, height int) {
	if w.Terminal == nil {
		return
	}

	borderDeduct := 2
	if w.Tiled {
		borderDeduct = 0
	}
	termWidth := max(width-borderDeduct, 1)
	termHeight := max(height-borderDeduct, 1)

	// Check if size actually changed
	sizeChanged := w.Width != width || w.Height != height

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
	if w.Pty != nil {
		if err := w.Pty.Resize(termWidth, termHeight); err != nil {
			_ = err
		}
		if w.CellPixelWidth > 0 && w.CellPixelHeight > 0 {
			xpixel := termWidth * w.CellPixelWidth
			ypixel := termHeight * w.CellPixelHeight
			_ = w.SetPtyPixelSize(termWidth, termHeight, xpixel, ypixel)
		}
	} else if w.DaemonMode && w.DaemonResizeFunc != nil {
		// In daemon mode, use the resize callback to notify the daemon
		if err := w.DaemonResizeFunc(termWidth, termHeight); err != nil {
			_ = err // Acknowledge error but don't break functionality
		}
	}
	w.Width = width
	w.Height = height

	// Mark both position and content dirty for resize operations
	w.MarkPositionDirty()
	w.MarkContentDirty()

	// Trigger redraw if size changed to force applications to adapt
	if sizeChanged && w.Pty != nil {
		w.TriggerRedraw()
	}
}

// ResizeVisual updates the window dimensions without triggering PTY resize.
// This is used during mouse drag to provide immediate visual feedback while
// deferring expensive PTY resize operations until the drag completes.
// The terminal emulator dimensions are updated to ensure correct rendering.
func (w *Window) ResizeVisual(width, height int) {
	w.Width = width
	w.Height = height

	// Critical: Update terminal emulator dimensions so rendering uses correct bounds.
	// This prevents the "stuck" height and dimension mismatch issues during drag.
	// PTY resize is still deferred until mouse release (via pending resizes).
	if w.Terminal != nil {
		borderDeduct := 2
		if w.Tiled {
			borderDeduct = 0
		}
		termWidth := max(width-borderDeduct, 1)
		termHeight := max(height-borderDeduct, 1)
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

	if w.Pty != nil && cellWidth > 0 && cellHeight > 0 {
		termWidth := w.ContentWidth()
		termHeight := w.ContentHeight()
		xpixel := termWidth * cellWidth
		ypixel := termHeight * cellHeight
		_ = w.SetPtyPixelSize(termWidth, termHeight, xpixel, ypixel)
	}
}

// MarkPositionDirty marks the window position as dirty.
func (w *Window) MarkPositionDirty() {
	w.Dirty = true
	w.PositionDirty = true
	// Position changes invalidate the cached layer but NOT the content cache
	// This allows us to keep the expensive terminal content rendering
	w.CachedLayer = nil
	// DON'T clear w.CachedContent here - keep it for performance
}

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
}
