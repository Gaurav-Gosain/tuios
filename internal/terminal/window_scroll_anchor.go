package terminal

// ApplyScrollAnchor puts the viewport back on its anchored line, and reports
// whether it had to move the offset to do it.
//
// It is the derive half and it runs on every message, so the live case is a
// single field read and no lock.
//
// One case it cannot hold, and does not claim to: a scrollback ring at its
// maximum evicts a line for every line it takes, so the history stops getting
// longer while the anchored line is destroyed, and nothing either emulator
// backend reports says how many lines went by. Past that point the view drifts
// again, at the default depth after ten thousand lines printed under a pane the
// user is still reading. The offset stays a row the ring holds, so nothing
// misreads; the pane is simply no longer where it was left.
func (w *Window) ApplyScrollAnchor() bool {
	if w == nil || !w.scrollAnchored {
		return false
	}
	sbLen := w.ScrollbackLenSync()
	want := w.anchoredOffset(sbLen)
	if want == w.ScrollbackOffset {
		w.scrollOffsetSeen = want
		return false
	}
	w.ScrollbackOffset = want
	if w.CopyMode != nil {
		w.CopyMode.ScrollOffset = want
	}
	w.scrollOffsetSeen = want
	if want == 0 {
		w.scrollAnchored = false
		if w.InImplicitCopyMode() && w.CopyMode.State == CopyModeNormal {
			w.ExitCopyMode()
			return true
		}
	}
	w.InvalidateCache()
	w.MarkContentDirty()
	return true
}

func (w *Window) anchoredOffset(sbLen int) int {
	return min(max(sbLen-w.scrollAnchorLine, 0), sbLen)
}

// HoldScrollAnchorAt puts the viewport back on its anchored line for the
// frame being drawn. sbLen is the history length the frame reads, under the
// I/O lock the render holds.
//
// ApplyScrollAnchor runs in OS.Update, but the reader goroutine writes output
// into the emulator at any time. A line that lands after the last Update and
// before the frame lengthens the history the frame draws from, and the
// offset derived before it no longer matches. Drawn at that offset, the view
// slides forward by the lines that just arrived, until the next message puts
// it back. ApplyScrollAnchor can also derive from a stale length, when the
// writer holds the lock and ScrollbackLenSync falls back to its last reading.
//
// An anchor that the history caught up with is left to ApplyScrollAnchor,
// which ends the implicit copy mode session on the UI goroutine's next
// message. This frame draws the stored offset, as before.
func (w *Window) HoldScrollAnchorAt(sbLen int) {
	if w == nil || !w.scrollAnchored {
		return
	}
	want := w.anchoredOffset(sbLen)
	if want == 0 || want == w.ScrollbackOffset {
		return
	}
	w.ScrollbackOffset = want
	if w.CopyMode != nil {
		w.CopyMode.ScrollOffset = want
	}
	w.scrollOffsetSeen = want
}

// RecordScrollAnchor takes the pane's current offset as the place the user
// meant, and remembers it as a line rather than as a distance.
//
// It is the record half. It acts only on an offset that changed since the
// derive half last looked, so output arriving between the two cannot be
// mistaken for the user scrolling and shift the anchor by the amount this whole
// file exists to cancel.
func (w *Window) RecordScrollAnchor() {
	if w == nil || w.ScrollbackOffset == w.scrollOffsetSeen {
		return
	}
	w.scrollOffsetSeen = w.ScrollbackOffset
	if w.ScrollbackOffset <= 0 {
		w.scrollAnchored = false
		return
	}
	w.scrollAnchorLine = w.ScrollbackLenSync() - w.ScrollbackOffset
	w.scrollAnchored = true
}

// ScrollAnchorLine is the anchored line, and whether there is one. It exists
// for the tests that pin the anchor rule; nothing in the app reads it.
func (w *Window) ScrollAnchorLine() (int, bool) {
	if w == nil {
		return 0, false
	}
	return w.scrollAnchorLine, w.scrollAnchored
}
