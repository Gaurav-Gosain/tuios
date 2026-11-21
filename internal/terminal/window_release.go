package terminal

type reflowScratchReleaser interface {
	ReleaseReflowScratch()
}

type altScreenReleaser interface {
	ReleaseAltScreen()
}

type scrollbackCacheReleaser interface {
	ReleaseScrollbackCache()
}

// TryReleaseEmulatorScratch drops the emulator's buffers that nothing on
// screen reads: the reflow scratch a resize keeps for the next one, the
// alternate screen's cells while the main screen is shown, and the decoded
// history lines a scrolled pane left in the scrollback cache. None changes
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
	if r, ok := w.Terminal.(scrollbackCacheReleaser); ok {
		r.ReleaseScrollbackCache()
	}
	return true
}
