package terminal

// A scrolled pane is parked at a place in its history, not at a distance from
// the end of it.
//
// ScrollbackOffset counts lines back from the newest line, and the render path
// reads a row as scrollbackLen-ScrollbackOffset+y. Both halves of that move:
// scrollbackLen grows every time the guest prints a line, and it grows in jumps
// when a workspace switch primes the pane from the daemon's copy of the
// history. Held at a fixed offset, the pane therefore slides forward under a
// user who has not touched it, one line per line printed and hundreds of lines
// per workspace round trip. That is the "scrolling breaks randomly" report:
// nothing about the keys the user pressed decides it, only whether output
// happened to arrive.
//
// The fix is to say the same viewport top the other way round, as an absolute
// line in the history, and to derive the offset from it whenever the history
// changes length. Two calls do it, both on the UI goroutine and both from
// OS.Update:
//
//   - ApplyScrollAnchor, before the message is handled, turns the anchor back
//     into an offset against however long the history is now.
//   - RecordScrollAnchor, after it, turns a scroll the user just made into a
//     new anchor.
//
// Saying it as an anchor rather than as a count of lines pushed is what makes
// the rehydration path correct without being told about it: ApplyTerminalState
// merges rows that never passed through the emulator's scroll path, and a
// counter would miss every one of them.

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
	// Clamped at both ends, and only the lower end ever binds: a history that
	// shrank below the anchored line, which is what ED 3 and the alternate
	// screen do, leaves no line to hold and the pane goes back to live output.
	// The upper end is defense. An anchor is recorded from an offset every
	// caller already clamps to the length, so it is never negative and this can
	// never ask for a row past the oldest the ring holds.
	want := min(max(sbLen-w.scrollAnchorLine, 0), sbLen)
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
		// The end of the history caught up with the anchor, so there is nothing
		// left to hold and the pane is on live output again. An implicit copy
		// mode session exists only to render scrollback, so it ends here for
		// the same reason the wheel ends it on reaching the bottom; an explicit
		// one the user asked for stays up.
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
