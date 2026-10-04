package session

import (
	"slices"
	"strconv"
)

// Going back from a workspace that lost its last pane (discussion #273).
//
// A workspace someone opened for a job, such as the panes of tuios xpanes,
// empties when the job ends. Before this the session stayed on it, and every
// client showed the splash screen with the work the person came from one
// workspace away. Now the session shows the workspace they came from.
//
// It is the daemon's to do because the daemon owns the window set and the
// workspace on screen: a pane exits, -ss closes one, close-workspace closes
// them all, with or without a client attached, and every close lands in
// CloseDaemonWindow. The workspace on screen is session state, so every
// attached client follows the switch, as it follows any other.
//
// Moving a pane to another workspace is not a close and does not come here:
// a move that follows the pane already switches, and one that does not
// leaves the person where they asked to be.

// optionReturnWhenEmpty is the option path. set-config records it on the
// session, and that value wins over the daemon's config for that session.
const optionReturnWhenEmpty = "workspaces.return_when_empty"

// workspaceTrailMax bounds the trail. A chain of empty workspaces longer than
// this falls back to the lowest workspace with panes.
const workspaceTrailMax = 16

// noteWorkspaceLocked puts the workspace on screen at the end of the trail.
// The caller holds stateMu. A scratch workspace is never the session's
// current one, and is left out in case one is.
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

// returnsWhenEmptyLocked reports whether this session goes back from an
// emptied workspace: the session's own option when set-config set one, else
// the daemon's config. The caller holds stateMu.
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

// hasPanes reports whether a workspace holds a pane. A scratch pane does not
// count: it is shown over a workspace, and is not one of its panes.
func hasPanes(state *SessionState, ws int) bool {
	for i := range state.Windows {
		if w := &state.Windows[i]; w.Workspace == ws && !w.Scratch {
			return true
		}
	}
	return false
}

// returnFromEmptyLocked runs after a close took a pane off emptied. When
// emptied is the workspace on screen and has no pane left, it shows another
// one: the workspace recorded by SetReturnTo, else the most recent one on the
// trail that has panes, else the lowest workspace with panes. When no
// workspace has a pane, the session stays, and clients show the splash
// screen. The caller holds stateMu, inside mutateState.
func (s *Session) returnFromEmptyLocked(state *SessionState, emptied int) {
	if IsScratchWorkspace(emptied) || hasPanes(state, emptied) {
		return
	}
	// The record is for the job that filled the workspace, and that job is
	// over, wherever the session is.
	origin := s.returnTo[emptied]
	delete(s.returnTo, emptied)
	// The emptied workspace leaves the trail, so the next empty one in a
	// chain goes back past it.
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
