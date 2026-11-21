package session

import (
	"slices"
	"strconv"
)

const optionReturnWhenEmpty = "workspaces.return_when_empty"

const workspaceTrailMax = 16

func (s *Session) noteWorkspaceLocked() {
	if s.state == nil {
		return
	}
	ws := s.state.CurrentWorkspace
	if ws < 1 || IsScratchWorkspace(ws) {
		return
	}
	if n := len(s.workspaceTrail); n > 0 && s.workspaceTrail[n-1] == ws {
		return
	}
	s.workspaceTrail = slices.DeleteFunc(s.workspaceTrail, func(w int) bool { return w == ws })
	s.workspaceTrail = append(s.workspaceTrail, ws)
	if over := len(s.workspaceTrail) - workspaceTrailMax; over > 0 {
		s.workspaceTrail = slices.Delete(s.workspaceTrail, 0, over)
	}
}

// SetReturnTo records origin as the workspace to show first when workspace
// loses its last pane. tuios xpanes calls it through select-workspace with
// the workspace that ran it. A zero or equal origin clears it.
func (s *Session) SetReturnTo(workspace, origin int) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if origin < 1 || origin == workspace || IsScratchWorkspace(origin) {
		delete(s.returnTo, workspace)
		return
	}
	if s.returnTo == nil {
		s.returnTo = make(map[int]int)
	}
	s.returnTo[workspace] = origin
}

func (s *Session) returnsWhenEmptyLocked(state *SessionState) bool {
	if v, ok := state.Options[optionReturnWhenEmpty]; ok {
		if on, err := strconv.ParseBool(v); err == nil {
			return on
		}
	}
	if s.config != nil && s.config.ReturnWhenEmpty != nil {
		return s.config.ReturnWhenEmpty()
	}
	return true
}

func hasPanes(state *SessionState, ws int) bool {
	for i := range state.Windows {
		if w := &state.Windows[i]; w.Workspace == ws && !w.Scratch {
			return true
		}
	}
	return false
}

func (s *Session) returnFromEmptyLocked(state *SessionState, emptied int) {
	if IsScratchWorkspace(emptied) || hasPanes(state, emptied) {
		return
	}
	origin := s.returnTo[emptied]
	delete(s.returnTo, emptied)
	s.workspaceTrail = slices.DeleteFunc(s.workspaceTrail, func(w int) bool { return w == emptied })
	if emptied != state.CurrentWorkspace || !s.returnsWhenEmptyLocked(state) {
		return
	}

	target := 0
	if origin != 0 && hasPanes(state, origin) {
		target = origin
	}
	for i := len(s.workspaceTrail) - 1; target == 0 && i >= 0; i-- {
		if hasPanes(state, s.workspaceTrail[i]) {
			target = s.workspaceTrail[i]
		}
	}
	for ws := 1; target == 0 && ws <= state.workspaceBound(); ws++ {
		if hasPanes(state, ws) {
			target = ws
		}
	}
	if target == 0 {
		return
	}

	s.markFocusIntentLocked()
	state.CurrentWorkspace = target
	focus := ""
	if id := state.WorkspaceFocus[target]; id != "" {
		for i := range state.Windows {
			if w := &state.Windows[i]; w.ID == id && w.Workspace == target && !w.Minimized {
				focus = id
				break
			}
		}
	}
	if focus == "" {
		focus = firstVisibleOnWorkspace(state.Windows, target)
	}
	state.FocusedWindowID = focus
	if focus != "" {
		if state.WorkspaceFocus == nil {
			state.WorkspaceFocus = make(map[int]string)
		}
		state.WorkspaceFocus[target] = focus
		state.FocusHistory = RecordFocus(state.FocusHistory, target, focus)
	}
}
