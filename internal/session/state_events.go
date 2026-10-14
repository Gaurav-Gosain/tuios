package session

import (
	"maps"
	"slices"
)

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
	agentState AgentState
	// agentHarness and agentMessage ride the snapshot only so the hook a
	// transition raises can name them. The lifecycle events themselves do not
	// report them and neither field alone raises an event.
	agentHarness string
	agentMessage string
	// agentStateAt rides along for the turn count, which needs to know when a
	// working phase began if nothing recorded it. See agent_turns.go.
	agentStateAt int64
	// agentKind and completionSeq ride along for the attention queue: what a
	// needs_input window is blocked by, and how many turns it has finished.
	agentKind     string
	completionSeq uint64
	// program rides along for the attention queue too: a pane whose state
	// comes from OSC 7501 is named by its id as well as its title.
	program bool
	// worktree and agentSession ride along for the risk rules: the
	// session's worktree when tuios made it, and the window's agent session
	// id. The sink turns them into the approval root (windowApprovalRoot),
	// off this snapshot, since that reads the file system.
	worktree, agentSession string
	// The rectangle and how the pane sits in it, for layout-changed only.
	// They change while a person drags a split, so they raise no event of
	// their own and only mark the workspace's layout as changed.
	x, y, w, h       int
	zoomed, floating bool
}

// lifecycleSnapshot is a copy of the lifecycle-relevant parts of a SessionState.
// It is a copy, not a reference, because the headless ops mutate the state in
// place and the "before" snapshot has to survive that.
type lifecycleSnapshot struct {
	windows   []lifecycleWindow // in state order
	index     map[string]int    // window ID -> index into windows
	focused   string
	workspace int
	// wsNames is a copy of the workspace names, for workspace-renamed.
	wsNames map[int]string
	// width and height are the session's size in cells, the box the
	// windows' rectangles are laid out in. For layout-changed.
	width, height int
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
	snap.width, snap.height = state.Width, state.Height
	if len(state.WorkspaceNames) > 0 {
		snap.wsNames = maps.Clone(state.WorkspaceNames)
		// A scratch group's workspace is not a workspace a person names or
		// switches to, so no workspace event ever reports one. See
		// scratch_workspace.go.
		maps.DeleteFunc(snap.wsNames, func(ws int, _ string) bool { return IsScratchWorkspace(ws) })
	}
	snap.windows = make([]lifecycleWindow, 0, len(state.Windows))
	worktree := managedWorktreePath(state.Worktree)
	for i := range state.Windows {
		w := &state.Windows[i]
		snap.index[w.ID] = len(snap.windows)
		snap.windows = append(snap.windows, lifecycleWindow{
			id:            w.ID,
			ptyID:         w.PTYID,
			title:         w.Title,
			customName:    w.CustomName,
			workspace:     w.Workspace,
			minimized:     w.Minimized,
			agentState:    w.AgentState,
			agentHarness:  w.AgentHarness,
			agentMessage:  w.AgentMessage,
			agentStateAt:  w.AgentStateAt,
			agentKind:     agentBlockedBy(*w),
			completionSeq: w.CompletionSeq,
			program:       len(w.ProgramStatus) > 0,
			worktree:      worktree,
			agentSession:  w.AgentSessionID,
			x:             w.X,
			y:             w.Y,
			w:             w.Width,
			h:             w.Height,
			zoomed:        w.Zoomed,
			floating:      w.IsFloating,
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
				Type:          EventWindowClosed,
				Window:        w.id,
				PTYID:         w.ptyID,
				hookTitle:     w.displayTitle(),
				hookWorkspace: w.workspace,
			})
		}
	}

	for i := range after.windows {
		w := &after.windows[i]
		if _, ok := before.index[w.id]; !ok {
			events = append(events, SessionEvent{
				Type:          EventWindowCreated,
				Window:        w.id,
				PTYID:         w.ptyID,
				Title:         w.displayTitle(),
				hookTitle:     w.displayTitle(),
				hookWorkspace: w.workspace,
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
		// Diffed here rather than emitted by each writer because agent state has
		// several: explicit reports, the screen tier, the stall heuristic and
		// the foreground detector all set it, and every one already runs inside
		// a state mutation. The event carries the wire spelling, so a pane
		// ceasing to be an agent says "none" rather than vanishing silently.
		if w.agentState != prev.agentState {
			events = append(events, SessionEvent{
				Type:          EventAgentState,
				Window:        w.id,
				PTYID:         w.ptyID,
				State:         w.agentState.Name(),
				hookTitle:     w.displayTitle(),
				hookWorkspace: w.workspace,
				hookPrevState: prev.agentState.Name(),
				hookHarness:   w.agentHarness,
				hookMessage:   w.agentMessage,

				hookKind:          w.agentKind,
				hookWorktree:      w.worktree,
				hookAgentSession:  w.agentSession,
				hookProgram:       w.program,
				completionSeq:     w.completionSeq,
				prevCompletionSeq: prev.completionSeq,
			})
		} else if attentionDetailChanged(w, prev) {
			// The same state with a new kind, message or name. Only the Inbox
			// hears it; prevCompletionSeq is the current count so it can
			// never be taken for a finished turn.
			events = append(events, SessionEvent{
				Type:          eventAttentionDetail,
				Window:        w.id,
				PTYID:         w.ptyID,
				State:         w.agentState.Name(),
				hookTitle:     w.displayTitle(),
				hookWorkspace: w.workspace,
				hookPrevState: prev.agentState.Name(),
				hookHarness:   w.agentHarness,
				hookMessage:   w.agentMessage,

				hookKind:          w.agentKind,
				hookWorktree:      w.worktree,
				hookAgentSession:  w.agentSession,
				hookProgram:       w.program,
				completionSeq:     w.completionSeq,
				prevCompletionSeq: w.completionSeq,
			})
		}
	}

	// A workspace whose name was set, changed or cleared. Title is the new
	// name, empty when it was cleared. Ascending, so the order is stable.
	var renamed []int
	for ws, name := range after.wsNames {
		if before.wsNames[ws] != name {
			renamed = append(renamed, ws)
		}
	}
	for ws := range before.wsNames {
		if _, ok := after.wsNames[ws]; !ok {
			renamed = append(renamed, ws)
		}
	}
	slices.Sort(renamed)
	for _, ws := range renamed {
		events = append(events, SessionEvent{Type: EventWorkspaceRenamed, Workspace: ws, Title: after.wsNames[ws]})
	}

	events = append(events, layoutChanges(before, after)...)

	if after.workspace != before.workspace && after.workspace > 0 && !IsScratchWorkspace(after.workspace) {
		events = append(events, SessionEvent{
			Type:              EventWorkspaceSwitched,
			Workspace:         after.workspace,
			hookPrevWorkspace: before.workspace,
		})
	}

	// A focus change is only reported for a window that still exists; losing
	// focus because the focused window closed is already implied by
	// window-closed, and an empty focus target has nothing to report.
	if after.focused != before.focused && after.focused != "" {
		ev := SessionEvent{Type: EventWindowFocused, Window: after.focused}
		if idx, ok := after.index[after.focused]; ok {
			ev.PTYID = after.windows[idx].ptyID
			ev.hookTitle = after.windows[idx].displayTitle()
			ev.hookWorkspace = after.windows[idx].workspace
		}
		events = append(events, ev)
	}

	return events
}

// layoutChanges returns one layout-changed event for each workspace whose
// panes now sit differently: a pane on it opened, closed, moved in or out,
// was minimized or restored, zoomed, floated, or changed its rectangle, or the
// session itself changed size. A client that mirrors a session's layout reads
// list-windows again on it. One event per workspace, however many panes moved,
// so a split that reshapes five panes is one event and not five. Ascending by
// workspace, so the order is stable.
func layoutChanges(before, after lifecycleSnapshot) []SessionEvent {
	// Allocated on the first change, so a push that moves nothing, which is
	// most of them, costs no allocation.
	var changed map[int]bool
	mark := func(ws int) {
		if ws > 0 && !IsScratchWorkspace(ws) {
			if changed == nil {
				changed = map[int]bool{}
			}
			changed[ws] = true
		}
	}
	for i := range before.windows {
		w := &before.windows[i]
		j, ok := after.index[w.id]
		if !ok {
			mark(w.workspace)
			continue
		}
		n := &after.windows[j]
		if n.workspace != w.workspace {
			mark(w.workspace)
			mark(n.workspace)
			continue
		}
		if n.x != w.x || n.y != w.y || n.w != w.w || n.h != w.h ||
			n.zoomed != w.zoomed || n.floating != w.floating || n.minimized != w.minimized {
			mark(n.workspace)
		}
	}
	for i := range after.windows {
		if _, ok := before.index[after.windows[i].id]; !ok {
			mark(after.windows[i].workspace)
		}
	}
	if (before.width != after.width || before.height != after.height) && after.width > 0 && after.height > 0 {
		// Every workspace is laid out in the same box, so a new box is a new
		// layout on each one that holds a pane.
		for i := range after.windows {
			mark(after.windows[i].workspace)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	wss := slices.Sorted(maps.Keys(changed))
	events := make([]SessionEvent, 0, len(wss))
	for _, ws := range wss {
		events = append(events, SessionEvent{Type: EventLayoutChanged, Workspace: ws})
	}
	return events
}

// attentionDetailChanged reports whether a window that kept its agent state
// changed something its Inbox item shows. Only needs_input and errored windows
// have an item that follows the current report; a finished item describes the
// turn that finished and is left as it was.
func attentionDetailChanged(w, prev *lifecycleWindow) bool {
	if w.agentState != AgentStateNeedsInput && w.agentState != AgentStateErrored {
		return false
	}
	return w.agentKind != prev.agentKind || w.agentMessage != prev.agentMessage ||
		w.agentHarness != prev.agentHarness || w.workspace != prev.workspace ||
		w.displayTitle() != prev.displayTitle() || w.program != prev.program
}

// displayTitle is the title reported for a window in a lifecycle event: the
// explicit name when one was set, otherwise the shell-reported title.
func (w *lifecycleWindow) displayTitle() string {
	if w.customName != "" {
		return w.customName
	}
	return w.title
}
