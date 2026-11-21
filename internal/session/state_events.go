package session

import (
	"maps"
	"slices"
)

type lifecycleWindow struct {
	id                     string
	ptyID                  string
	title                  string
	customName             string
	workspace              int
	minimized              bool
	agentState             AgentState
	shownState             AgentState
	agentReason            string
	agentQuiet             bool
	agentHarness           string
	agentMessage           string
	agentStateAt           int64
	agentKind              string
	completionSeq          uint64
	program                bool
	worktree, agentSession string
	link                   childLink
	x, y, w, h             int
	zoomed, floating       bool
}

type lifecycleSnapshot struct {
	windows       []lifecycleWindow
	index         map[string]int
	focused       string
	workspace     int
	wsNames       map[int]string
	displayName   string
	width, height int
}

func snapshotLifecycle(state *SessionState) lifecycleSnapshot {
	snap := lifecycleSnapshot{index: make(map[string]int)}
	if state == nil {
		return snap
	}
	snap.focused = state.FocusedWindowID
	snap.displayName = state.DisplayName
	snap.workspace = state.CurrentWorkspace
	snap.width, snap.height = state.Width, state.Height
	if len(state.WorkspaceNames) > 0 {
		snap.wsNames = maps.Clone(state.WorkspaceNames)
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
			shownState:    w.ShownAgentState(),
			agentReason:   w.AgentReason,
			agentQuiet:    w.AgentQuiet,
			agentHarness:  w.AgentHarness,
			agentMessage:  w.AgentMessage,
			agentStateAt:  w.AgentStateAt,
			agentKind:     agentBlockedBy(*w),
			completionSeq: w.CompletionSeq,
			program:       len(w.ProgramStatus) > 0,
			worktree:      worktree,
			agentSession:  w.AgentSessionID,
			link:          childLinkOf(w),
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
			continue
		}
		prev := &before.windows[prevIdx]

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
		if w.agentState != prev.agentState || w.shownState != prev.shownState {
			events = append(events, SessionEvent{
				Type:          EventAgentState,
				Window:        w.id,
				PTYID:         w.ptyID,
				State:         w.shownState.Name(),
				Reason:        w.agentReason,
				hookTitle:     w.displayTitle(),
				hookWorkspace: w.workspace,
				hookPrevState: prev.agentState.Name(),
				hookHarness:   w.agentHarness,
				hookMessage:   w.agentMessage,

				hookKind:          w.agentKind,
				hookWorktree:      w.worktree,
				hookAgentSession:  w.agentSession,
				link:              w.link,
				hookProgram:       w.program,
				agentQuiet:        w.agentQuiet,
				completionSeq:     w.completionSeq,
				prevCompletionSeq: prev.completionSeq,
			})
		} else if attentionDetailChanged(w, prev) {
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
				link:              w.link,
				hookProgram:       w.program,
				agentQuiet:        w.agentQuiet,
				completionSeq:     w.completionSeq,
				prevCompletionSeq: w.completionSeq,
			})
		}
	}

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

	if after.displayName != before.displayName {
		events = append(events, SessionEvent{Type: EventSessionLabelled, Title: after.displayName})
	}

	events = append(events, layoutChanges(before, after)...)

	if after.workspace != before.workspace && after.workspace > 0 && !IsScratchWorkspace(after.workspace) {
		events = append(events, SessionEvent{
			Type:              EventWorkspaceSwitched,
			Workspace:         after.workspace,
			hookPrevWorkspace: before.workspace,
		})
	}

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

func layoutChanges(before, after lifecycleSnapshot) []SessionEvent {
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

func attentionDetailChanged(w, prev *lifecycleWindow) bool {
	if w.agentState != AgentStateNeedsInput && w.agentState != AgentStateErrored {
		return false
	}
	return w.agentKind != prev.agentKind || w.agentMessage != prev.agentMessage ||
		w.agentHarness != prev.agentHarness || w.workspace != prev.workspace ||
		w.displayTitle() != prev.displayTitle() || w.program != prev.program
}

func (w *lifecycleWindow) displayTitle() string {
	if w.customName != "" {
		return w.customName
	}
	return w.title
}
