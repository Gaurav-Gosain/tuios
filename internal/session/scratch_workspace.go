package session

import (
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// ScratchWorkspaceBase is the first scratch workspace number.
const ScratchWorkspaceBase = 1000

// IsScratchWorkspace reports whether ws is a scratch workspace.
func IsScratchWorkspace(ws int) bool { return ws >= ScratchWorkspaceBase }

func (s *SessionState) workspaceAccepts(ws int) bool {
	return (ws >= 1 && ws <= s.workspaceBound()) || IsScratchWorkspace(ws)
}

func freeScratchWorkspace(state *SessionState) int {
	used := map[int]bool{}
	for i := range state.Windows {
		used[state.Windows[i].Workspace] = true
	}
	ws := ScratchWorkspaceBase
	for used[ws] {
		ws++
	}
	return ws
}

func scratchNameOnWorkspace(state *SessionState, ws int) (string, bool) {
	for i := range state.Windows {
		if w := &state.Windows[i]; w.Workspace == ws && w.Scratch {
			return w.ScratchName, true
		}
	}
	return "", false
}

func errNoScratchGroup(ws int) error {
	return errfmt.Errorf("scratch workspace %d has no panes. Press the scratch key to start it again", ws)
}

func unfocusScratchLocked(state *SessionState) {
	for i := range state.Windows {
		w := &state.Windows[i]
		if w.ID != state.FocusedWindowID || !IsScratchWorkspace(w.Workspace) {
			continue
		}
		state.FocusedWindowID = ""
		if id := state.WorkspaceFocus[state.CurrentWorkspace]; id != "" {
			state.FocusedWindowID = id
			return
		}
		for j := range state.Windows {
			if state.Windows[j].Workspace == state.CurrentWorkspace {
				state.FocusedWindowID = state.Windows[j].ID
				return
			}
		}
		return
	}
}

var errScratchPaneStays = errfmt.Errorf("a scratch pane stays in its scratch group. Move a different pane")
