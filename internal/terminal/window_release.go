package terminal

// reflowScratchReleaser is an emulator that keeps buffers from one reflow to
// the next. The pure Go emulator is one. libghostty-vt manages its own memory
// and is not.
type reflowScratchReleaser interface {
	ReleaseReflowScratch()
}

// altScreenReleaser is an emulator that can drop its alternate screen's cells
// while the main screen is shown. The pure Go emulator is one. libghostty-vt
// manages its own memory and is not.
type altScreenReleaser interface {
	ReleaseAltScreen()
}

// TryReleaseEmulatorScratch drops the emulator's buffers that nothing on
// screen reads: the reflow scratch a resize keeps for the next one, and the
// alternate screen's cells while the main screen is shown. Neither changes
// what the pane draws, so the pane is not marked dirty.
//
// It takes ioMu only if it is free. The caller is the UI goroutine, which
// must not wait behind a PTY writer in the middle of a burst; a pane that is
// busy now is released on a later call. It reports false for such a pane.
func (w *Window) TryReleaseEmulatorScratch() bool {
	if !w.ioMu.TryLock() {
		return false
	}
	defer w.ioMu.Unlock()
	if r, ok := w.Terminal.(reflowScratchReleaser); ok {
		r.ReleaseReflowScratch()
	}
	if r, ok := w.Terminal.(altScreenReleaser); ok {
		r.ReleaseAltScreen()
	}
	return true
}
