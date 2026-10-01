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

// EnterCopyMode enters vim-style copy/scrollback mode with the copy cursor on
// the terminal cursor, clamped to the viewport. That is where tmux puts it, and
// it is usually the prompt line, so a search starts from where the person was
// typing rather than from an arbitrary row.
func (w *Window) EnterCopyMode() {
	x, y := w.copyEntryCursor()
	w.enterCopyMode(x, y)
}

// EnterCopyModeCentered enters copy mode with the copy cursor at column 0 of
// the middle row, the vim-style entry tuios used before the cursor entry. It is
// what appearance.selection.copy_entry = "center" asks for.
func (w *Window) EnterCopyModeCentered() {
	w.enterCopyMode(0, w.ContentHeight()/2)
}

// copyEntryCursor is the terminal cursor clamped to the pane's content area.
//
// The lock is a try, not a wait, for the reason cursor rendering gives: a pane
// in an output burst holds the exclusive side almost continuously, and entering
// copy mode must not stall behind it. When the lock is busy the cursor the last
// frame saw is used, which is at most a frame stale. The try also means a
// caller that already holds the read lock cannot deadlock here.
func (w *Window) copyEntryCursor() (int, int) {
	pos, _, _ := w.GuestCursor()
	maxX := max(w.ContentWidth()-1, 0)
	maxY := max(w.ContentHeight()-1, 0)
	return max(min(pos.X, maxX), 0), max(min(pos.Y, maxY), 0)
}

func (w *Window) enterCopyMode(x, y int) {
	if w.CopyMode == nil {
		w.CopyMode = &CopyMode{}
	}

	w.CopyMode.Active = true
	w.CopyMode.State = CopyModeNormal
	w.CopyMode.CursorX = x
	w.CopyMode.CursorY = y
	w.CopyMode.ScrollOffset = 0 // Start at live content
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
// The cursor is hidden in an implicit session and the gesture places it, so
// the entry row is the old middle row and no terminal lock is taken.
//
// See CopyMode.Implicit for what the flag changes.
func (w *Window) EnterCopyModeImplicit() {
	w.enterCopyMode(0, w.Height/2)
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
