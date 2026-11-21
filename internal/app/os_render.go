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
	m.cachedViewContent = ""
	m.sidebarCache.invalidate()
	m.secondarySidebarCache.invalidate()
	m.pip.dirty = true
}

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
	m.cachedViewContent = ""
}

// MarkTerminalsWithNewContent marks terminals that have new content as dirty.
func (m *OS) MarkTerminalsWithNewContent() bool {
	if len(m.Windows) == 0 && !m.sidebarSessionOutputPending() {
		return false
	}

	if m.InteractionMode || m.Dragging || m.Resizing {
		return false
	}

	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()

	hasChanges := false
	activeTerminals := 0
	m.setPaneFrameInterval()
	m.clearViewedWorkspaceMarks()

	for i := range m.Windows {
		window := m.Windows[i]

		if window.Terminal == nil {
			continue
		}
		if window.Pty == nil && !window.DaemonMode {
			continue
		}

		activeTerminals++

		graphics := window.HasGraphicsOutput.Swap(false)

		if window.Minimized || window.Workspace != m.CurrentWorkspace {
			if isRailPane(window) {
				_, m.railPaneView.noted = window.EmulatorWrites()
			}
			out := window.HasNewOutput.Swap(false)
			if out && !window.Minimized && m.noteWorkspaceMark(window.Workspace, workspaceMarkActivity) {
				hasChanges = true
			}
			if out && m.cockpitNoteOutput(window) {
				hasChanges = true
			}
			if out && m.railPaneNoteOutput(window) {
				hasChanges = true
				continue
			}
			if out && window.ID == m.pip.windowID {
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

		if window.IsBeingManipulated {
			continue
		}

		if isRailPane(window) {
			_, m.railPaneView.noted = window.EmulatorWrites()
		}
		newOutput := window.HasNewOutput.Swap(false)
		if !newOutput {
			if graphics && m.KittyPassthrough != nil && m.KittyPassthrough.HasQueued() {
				hasChanges = true
			}
			continue
		}

		window.MarkContentDirty()
		hasChanges = true
		if window.ID == m.pip.windowID {
			m.pip.dirty = true
		}
		m.cockpitNoteOutput(window)
	}

	for edge := range m.sidebarSessions {
		for _, w := range m.sidebarSessions[edge].windows {
			if w.HasNewOutput.Swap(false) {
				w.MarkContentDirty()
				hasChanges = true
				m.cachedViewContent = ""
			}
		}
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

	for i := range m.Windows {
		window := m.Windows[i]
		if window == nil || window.Terminal == nil {
			continue
		}
		if window.Pty == nil && !window.DaemonMode {
			continue
		}

		window.MarkContentDirty()

		window.InvalidateCache()
	}
}
