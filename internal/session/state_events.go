package session

// Window lifecycle events are derived, in one place, from the difference between
// two canonical SessionState snapshots. This matters because a mutation reaches
// daemon-owned state by one of two routes: the headless daemon-side ops in
// session_ops.go mutate the state in place, and an attached TUI performs the
// mutation itself and pushes the result back with UpdateState. Both routes funnel
// through the helpers below, so a subscriber sees the same events, with the same
// payloads and the same ordering, no matter which one ran. Emitting from the
// diff (rather than from each op and again from the TUI bridge) is also what
// makes the events exactly-once: state converges once, so it is diffed once.
//
// Events that hang off a PTY rather than off window state (output, bell,
// mode-changed, window-exit, and OSC-driven title changes) are emitted by the
// per-PTY emitter in session.go and are deliberately not derived here, so they
// are not double-counted.

// lifecycleWindow is the subset of a WindowState that window lifecycle events
// are derived from. Geometry, z-order, and alt-screen state are excluded: they
// change on nearly every render and raise no lifecycle event.
type lifecycleWindow struct {
	id         string
	ptyID      string
	title      string
	customName string
	workspace  int
	minimized  bool
}

// lifecycleSnapshot is a copy of the lifecycle-relevant parts of a SessionState.
// It is a copy, not a reference, because the headless ops mutate the state in
// place and the "before" snapshot has to survive that.
type lifecycleSnapshot struct {
	windows   []lifecycleWindow // in state order
	index     map[string]int    // window ID -> index into windows
	focused   string
	workspace int
}

// snapshotLifecycle captures the lifecycle-relevant parts of state. The caller
// must hold the session's state lock.
func snapshotLifecycle(state *SessionState) lifecycleSnapshot {
	snap := lifecycleSnapshot{index: make(map[string]int)}
	if state == nil {
		return snap
	}
	snap.focused = state.FocusedWindowID
	snap.workspace = state.CurrentWorkspace
	snap.windows = make([]lifecycleWindow, 0, len(state.Windows))
	for i := range state.Windows {
		w := &state.Windows[i]
		snap.index[w.ID] = len(snap.windows)
		snap.windows = append(snap.windows, lifecycleWindow{
			id:         w.ID,
			ptyID:      w.PTYID,
			title:      w.Title,
			customName: w.CustomName,
			workspace:  w.Workspace,
			minimized:  w.Minimized,
		})
	}
	return snap
}

// diffLifecycle returns the window lifecycle events implied by the move from
// before to after, in a stable order: closes, then creates, then per-window
// changes (rename, move, minimize/restore) in after-state order, then the
// session-level workspace switch, then the focus change. Focus comes last
// because it is usually a consequence of one of the earlier events, and a
// consumer that reacts to focus wants the window it is focusing to already
// exist in the picture it has built from the stream.
func diffLifecycle(before, after lifecycleSnapshot) []SessionEvent {
	var events []SessionEvent

	for i := range before.windows {
		w := &before.windows[i]
		if _, ok := after.index[w.id]; !ok {
			events = append(events, SessionEvent{
				Type:   EventWindowClosed,
				Window: w.id,
				PTYID:  w.ptyID,
			})
		}
	}

	for i := range after.windows {
		w := &after.windows[i]
		if _, ok := before.index[w.id]; !ok {
			events = append(events, SessionEvent{
				Type:   EventWindowCreated,
				Window: w.id,
				PTYID:  w.ptyID,
				Title:  w.displayTitle(),
			})
		}
	}

	for i := range after.windows {
		w := &after.windows[i]
		prevIdx, ok := before.index[w.id]
		if !ok {
			continue // already reported as created
		}
		prev := &before.windows[prevIdx]

		// Only an explicit rename (CustomName) raises window-retitled from the
		// diff. A Title change is the shell's OSC title sequence, which the
		// per-PTY emitter already reports; deriving it here too would emit the
		// same title change twice.
		if w.customName != prev.customName {
			events = append(events, SessionEvent{
				Type:   EventWindowRetitled,
				Window: w.id,
				Title:  w.customName,
			})
		}
		if w.workspace != prev.workspace {
			events = append(events, SessionEvent{
				Type:      EventWindowMoved,
				Window:    w.id,
				PTYID:     w.ptyID,
				Workspace: w.workspace,
			})
		}
		if w.minimized != prev.minimized {
			evType := EventWindowRestored
			if w.minimized {
				evType = EventWindowMinimized
			}
			events = append(events, SessionEvent{
				Type:   evType,
				Window: w.id,
				PTYID:  w.ptyID,
			})
		}
	}

	if after.workspace != before.workspace && after.workspace > 0 {
		events = append(events, SessionEvent{
			Type:      EventWorkspaceSwitched,
			Workspace: after.workspace,
		})
	}

	// A focus change is only reported for a window that still exists; losing
	// focus because the focused window closed is already implied by
	// window-closed, and an empty focus target has nothing to report.
	if after.focused != before.focused && after.focused != "" {
		ev := SessionEvent{Type: EventWindowFocused, Window: after.focused}
		if idx, ok := after.index[after.focused]; ok {
			ev.PTYID = after.windows[idx].ptyID
		}
		events = append(events, ev)
	}

	return events
}

// displayTitle is the title reported for a window in a lifecycle event: the
// explicit name when one was set, otherwise the shell-reported title.
func (w *lifecycleWindow) displayTitle() string {
	if w.customName != "" {
		return w.customName
	}
	return w.title
}
