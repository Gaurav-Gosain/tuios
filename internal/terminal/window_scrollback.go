package terminal

import (
	uv "github.com/charmbracelet/ultraviolet"
)

// ScrollbackLenSync returns the scrollback length without blocking on the
// window's I/O lock. Callers on the render loop must use this rather than
// ScrollbackLen, because the PTY reader and the daemon outputWriter push
// scrollback lines from background goroutines. ScrollbackLen itself stays
// lock-free: renderTerminal calls it while already holding RLockIO, and RWMutex
// read locks do not nest safely against a waiting writer.
//
// The acquisition is a try, not a wait. This is only ever a scrollbar's
// dimensions, and a pane under a heavy burst holds the exclusive lock almost
// continuously, so waiting here blocked the compositor for the entire screen on
// one background pane's output. When the lock is busy the last observed length
// is returned instead: it is at most a few frames stale, which a scrollbar
// cannot show, and the true value is republished on the next frame that
// acquires.
func (w *Window) ScrollbackLenSync() int {
	if !w.ioMu.TryRLock() {
		return int(w.lastScrollbackLen.Load())
	}
	defer w.ioMu.RUnlock()
	if w.Terminal == nil {
		return 0
	}
	n := w.Terminal.ScrollbackLen()
	w.lastScrollbackLen.Store(int64(n))
	return n
}

// ScrollbackLen returns the number of lines in the scrollback buffer.
func (w *Window) ScrollbackLen() int {
	if w.Terminal == nil {
		return 0
	}
	return w.Terminal.ScrollbackLen()
}

// ScrollbackLine returns a line from the scrollback buffer at the given index.
// Index 0 is the oldest line. Returns nil if index is out of bounds.
func (w *Window) ScrollbackLine(index int) uv.Line {
	if w.Terminal == nil {
		return nil
	}
	return w.Terminal.ScrollbackLine(index)
}

// ClearScrollback clears the scrollback buffer.
func (w *Window) ClearScrollback() {
	if w.Terminal != nil {
		w.Terminal.ClearScrollback()
	}
}

// SetScrollbackMaxLines sets the maximum number of lines for the scrollback buffer.
func (w *Window) SetScrollbackMaxLines(maxLines int) {
	if w.Terminal != nil {
		w.Terminal.SetScrollbackMaxLines(maxLines)
	}
}

// EnterScrollbackMode enters scrollback viewing mode.
func (w *Window) EnterScrollbackMode() {
	w.ScrollbackMode = true
	w.ScrollbackOffset = 0 // Start at the bottom (most recent scrollback)
	w.InvalidateCache()
}

// ExitScrollbackMode exits scrollback viewing mode.
func (w *Window) ExitScrollbackMode() {
	w.ScrollbackMode = false
	w.ScrollbackOffset = 0
	w.InvalidateCache()
}

// ScrollUp scrolls up in the scrollback buffer.
func (w *Window) ScrollUp(lines int) {
	if !w.ScrollbackMode || w.Terminal == nil {
		return
	}

	maxOffset := w.ScrollbackLen()
	w.ScrollbackOffset = min(w.ScrollbackOffset+lines, maxOffset)
	w.InvalidateCache()
}

// ScrollDown scrolls down in the scrollback buffer.
func (w *Window) ScrollDown(lines int) {
	if !w.ScrollbackMode {
		return
	}

	w.ScrollbackOffset = max(w.ScrollbackOffset-lines, 0)
	if w.ScrollbackOffset == 0 {
		// If we scrolled all the way down, exit scrollback mode
		w.ExitScrollbackMode()
	} else {
		w.InvalidateCache()
	}
}

// EnterCopyMode enters vim-style copy/scrollback mode.
// This replaces both ScrollbackMode and SelectionMode with a unified vim interface.
func (w *Window) EnterCopyMode() {
	if w.CopyMode == nil {
		w.CopyMode = &CopyMode{}
	}

	w.CopyMode.Active = true
	w.CopyMode.State = CopyModeNormal
	w.CopyMode.CursorX = 0
	w.CopyMode.CursorY = w.Height / 2 // Start in MIDDLE (vim-style)
	w.CopyMode.ScrollOffset = 0       // Start at live content
	w.CopyMode.SearchQuery = ""
	w.CopyMode.SearchMatches = nil
	w.CopyMode.CurrentMatch = 0
	w.CopyMode.CaseSensitive = false
	w.CopyMode.PendingGCount = false
	w.CopyMode.Implicit = false

	// Sync with window scrollback
	w.ScrollbackOffset = 0

	w.InvalidateCache()
}

// EnterCopyModeImplicit turns copy mode on as a mechanism rather than as a
// mode: it is how a mouse wheel, a scrollbar drag, or a drag-selection gets
// scrollback on screen at all. Nothing about the session is announced and the
// dock keeps showing terminal mode, so scrolling looks like scrolling.
//
// See CopyMode.Implicit for what the flag changes.
func (w *Window) EnterCopyModeImplicit() {
	w.EnterCopyMode()
	if w.CopyMode != nil {
		w.CopyMode.Implicit = true
	}
}

// InCopyMode reports whether copy mode is active at all, implicit sessions
// included. Use it for anything that reads or writes copy-mode state.
//
// This and the two predicates below tolerate a nil window: the render and dock
// paths ask about the focused window, and there is not always one.
func (w *Window) InCopyMode() bool {
	return w != nil && w.CopyMode != nil && w.CopyMode.Active
}

// CopyModeVisible reports whether copy mode should present itself as a mode:
// the dock's copy-mode pill and key hints, and the block cursor. An implicit
// session is a scrolled view, so it reports false.
func (w *Window) CopyModeVisible() bool {
	return w.InCopyMode() && !w.CopyMode.Implicit
}

// InImplicitCopyMode reports whether copy mode is active only because a scroll
// or drag gesture needed it.
func (w *Window) InImplicitCopyMode() bool {
	return w.InCopyMode() && w.CopyMode.Implicit
}

// HasSelection reports whether the pane is holding text a copy action could act
// on. A visual selection is copy mode's whole representation of one, whether it
// came from v/V or from a mouse gesture, so this is the single question every
// consumer asks: the context menu deciding whether to offer "Copy selection",
// the right-click that opens the selection menu, and the copy action itself.
//
// It exists because the answer used to be read off Window.SelectedText, a field
// the mouse path deliberately never wrote, so a plainly visible selection
// presented as no selection at all.
func (w *Window) HasSelection() bool {
	return w.InCopyMode() &&
		(w.CopyMode.State == CopyModeVisualChar || w.CopyMode.State == CopyModeVisualLine)
}

// ExitCopyMode exits copy mode and returns to normal terminal mode.
func (w *Window) ExitCopyMode() {
	if w.CopyMode != nil {
		w.CopyMode.Active = false
		w.CopyMode.State = CopyModeNormal
		w.CopyMode.ScrollOffset = 0
		w.CopyMode.Implicit = false
		// Clear search state
		w.CopyMode.SearchQuery = ""
		w.CopyMode.SearchMatches = nil
		w.CopyMode.SearchCache.Valid = false
	}

	// CRITICAL: Return to live content (bottom of scrollback)
	w.ScrollbackOffset = 0
	w.InvalidateCache()
}

// EnableCallbacks re-enables VT emulator callbacks after state restoration.
// This is used to prevent race conditions where buffered PTY output overwrites
// restored state during daemon session reattachment.
func (w *Window) EnableCallbacks() {
	w.suppressCallbacks.Store(false)
}

// DisableCallbacks temporarily disables VT emulator callbacks.
// This is used during state restoration to prevent race conditions.
func (w *Window) DisableCallbacks() {
	w.suppressCallbacks.Store(true)
}
