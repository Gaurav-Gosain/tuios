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
	m.sidebarCache.invalidate()
	m.secondarySidebarCache.invalidate()
	m.pip.dirty = true
}

// markZenDirty marks the windows whose zen-mode border visibility is about to
// change as dirty, so their CachedLayer is rebuilt with (or without) the
// frame. The focused window always keeps its border, so only the unfocused
// windows need the repaint. Marking them dirty is also what makes View()
// compose a fresh frame instead of serving the cached one; without it the
// melt/reveal would never be drawn even when the tick knows it crossed the
// idle threshold.
func (m *OS) markZenDirty() {
	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()
	for i := range m.Windows {
		w := m.Windows[i]
		if w == nil || i == m.FocusedWindow {
			continue
		}
		w.MarkContentDirty()
	}
	m.cachedViewContent = "" // Invalidate view cache so View() re-composes
}

// MarkTerminalsWithNewContent marks terminals that have new content as dirty.
func (m *OS) MarkTerminalsWithNewContent() bool {
	// A docked session's pane is deliberately not in m.Windows, but its
	// subscription uses the same output signal and frame budget.
	if len(m.Windows) == 0 && !m.sidebarSessionOutputPending() {
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
	// A pane made since the last pass takes this client's frame rate.
	m.setPaneFrameInterval()

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

		// A write of kitty graphics alone changed no cell (see
		// terminal.graphicsOnly). The frame is needed only to draw what the
		// passthrough queued for the next frame; a frame it already wrote
		// itself needs nothing.
		graphics := window.HasGraphicsOutput.Swap(false)

		// Skip content checking for minimized windows or windows on a different workspace.
		// Their PTY data is still consumed (preventing buffer overflow), but we avoid
		// marking them dirty and triggering unnecessary rendering work.
		if window.Minimized || window.Workspace != m.CurrentWorkspace {
			// Drain the new-output flag so it doesn't accumulate. The one
			// hidden pane whose output is drawn is the pinned one, in the
			// picture-in-picture view, and it is paced like an unfocused
			// pane below: every third pass, with the flag kept between so
			// the last output is still drawn once the pane goes quiet.
			if window.HasNewOutput.Swap(false) && window.ID == m.pip.windowID {
				window.UpdateCounter++
				if window.UpdateCounter%3 == 0 {
					m.pip.dirty = true
					hasChanges = true
				} else {
					window.HasNewOutput.Store(true)
				}
			}
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
			if graphics && m.KittyPassthrough != nil && m.KittyPassthrough.HasQueued() {
				hasChanges = true
			}
			continue
		}

		// Mark the window dirty. Every visible pane with output is drawn in
		// the frame: output frames are spaced a frame period apart (see
		// paneFrameWait), so this runs at most once a frame.
		//
		// Unfocused panes used to be drawn on every third pass only, to
		// save CPU when every signal from every pane composed a frame. With
		// passes bounded by the frame rate that rule only made them uneven:
		// nine animating panes at 120 frames a second showed their guests at
		// 72 to 100.
		window.MarkContentDirty()
		hasChanges = true
		if window.ID == m.pip.windowID {
			m.pip.dirty = true
		}
	}

	for edge := range m.sidebarSessions {
		for _, w := range m.sidebarSessions[edge].windows {
			if w.HasNewOutput.Swap(false) {
				w.MarkContentDirty()
				hasChanges = true
			}
		}
	}
	if hasChanges {
		m.cachedViewContent = ""
	}
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
