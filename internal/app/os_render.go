package app

// MarkAllDirty marks all windows as dirty for re-rendering. It goes through
// MarkContentDirty so ContentDirty always implies the cached content string is
// dropped; otherwise renderTerminal's unfocused early return would hand back
// the stale cache and ClearDirtyFlags would then discard the repaint request.
func (m *OS) MarkAllDirty() {
	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()
	for i := range m.Windows {
		if m.Windows[i] != nil {
			m.Windows[i].MarkContentDirty()
		}
	}
	m.cachedViewContent = "" // Invalidate view cache
}

// MarkTerminalsWithNewContent marks terminals that have new content as dirty.
func (m *OS) MarkTerminalsWithNewContent() bool {
	// Fast path: no windows
	if len(m.Windows) == 0 {
		m.HasActiveTerminals = false
		return false
	}

	// Skip all terminal updates if we're actively dragging/resizing ANY window
	// This prevents content updates from interfering with mouse coordinate calculations
	if m.InteractionMode || m.Dragging || m.Resizing {
		return false
	}

	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()

	hasChanges := false
	activeTerminals := 0
	focusedWindowIndex := m.FocusedWindow

	for i := range m.Windows {
		window := m.Windows[i]

		// Skip invalid terminals
		// For daemon-mode windows, we don't have a local PTY but still need to update
		if window.Terminal == nil {
			continue
		}
		if window.Pty == nil && !window.DaemonMode {
			continue
		}

		activeTerminals++

		// Skip content checking for minimized windows or windows on a different workspace.
		// Their PTY data is still consumed (preventing buffer overflow), but we avoid
		// marking them dirty and triggering unnecessary rendering work.
		if window.Minimized || window.Workspace != m.CurrentWorkspace {
			// Drain the new-output flag so it doesn't accumulate
			window.HasNewOutput.Swap(false)
			continue
		}

		// Skip content checking for windows that are being moved/resized
		// This prevents btop and other rapidly-updating programs from interfering
		if window.IsBeingManipulated {
			continue
		}

		// Only mark dirty when the terminal actually received new output.
		// This avoids the old unconditional dirty-marking that defeated frame skipping.
		newOutput := window.HasNewOutput.Swap(false)
		if !newOutput {
			continue
		}

		// Mark window as dirty. Focused windows always update immediately.
		// Background windows update every 3rd cycle to reduce CPU, but
		// keep HasNewOutput set so they update when focused.
		isFocused := i == focusedWindowIndex
		if isFocused {
			window.MarkContentDirty()
			hasChanges = true
		} else {
			window.UpdateCounter++
			if window.UpdateCounter%3 == 0 {
				window.MarkContentDirty()
				hasChanges = true
			} else {
				// Don't clear the flag  - let it stay set so the window
				// updates on the next cycle or when focused
				window.HasNewOutput.Store(true)
			}
		}
	}

	m.HasActiveTerminals = activeTerminals > 0
	return hasChanges
}

// ApplyPendingResizes performs the deferred half of every resize recorded while
// a drag or a terminal-resize storm was in progress: the emulator's real
// resize, the PTY's TIOCSWINSZ and SIGWINCH, the daemon notification and the
// backend's own resize, followed by a full repaint so the guests' answers are
// picked up.
//
// The visual half already happened, per step, through ResizeVisual. Everything
// here is the part whose cost scales with the number of panes and with what is
// running in them, which is why it waits for the size to stop moving.
func (m *OS) ApplyPendingResizes() {
	if len(m.PendingResizes) == 0 {
		m.FlushPTYBuffersAfterResize()
		return
	}
	for i := range m.Windows {
		win := m.Windows[i]
		if win == nil {
			continue
		}
		if dims, ok := m.PendingResizes[win.ID]; ok {
			win.Resize(dims[0], dims[1])
		}
	}
	m.PendingResizes = make(map[string][2]int)
	m.FlushPTYBuffersAfterResize()
}

// FlushPTYBuffersAfterResize flushes buffered PTY content and forces content polling
// after a resize operation completes. This ensures that shell prompt redraws in response
// to SIGWINCH are properly processed and displayed.
func (m *OS) FlushPTYBuffersAfterResize() {
	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()

	// Mark all windows as dirty to force full redraw
	for i := range m.Windows {
		window := m.Windows[i]
		if window == nil || window.Terminal == nil {
			continue
		}
		// For daemon-mode windows, we don't have a local PTY but still need to update
		if window.Pty == nil && !window.DaemonMode {
			continue
		}

		// Mark content as dirty to trigger re-rendering
		window.MarkContentDirty()

		// Invalidate cache to force fresh render
		window.InvalidateCache()
	}
}
