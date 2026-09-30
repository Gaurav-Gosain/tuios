package session

import "fmt"

// Scratch workspaces.
//
// A scratch group (the built-in scratch terminal, or a [[keybindings.command]]
// entry of type scratch) is a workspace of its own: its panes are ordinary
// windows, tiled by the workspace's BSP tree, on a workspace numbered from
// ScratchWorkspaceBase up. A client shows the group by making that workspace
// its current one and drawing it in a box over the workspace the user left;
// hiding the group is going back. So splits, focus, resize, zoom, the layout
// tree ops and the daemon's save and restore all work inside a scratch with no
// code of their own, and a hidden group is simply a workspace nobody is on.
//
// The numbers are outside 1..NumWorkspaces, so nothing that lists, counts,
// cycles or switches workspaces ever reaches one. The few range checks that
// must let one through (a new window on it, a tree op for it) ask
// IsScratchWorkspace. The daemon picks the number when the group is created
// and every pane of the group carries it, with the group's name in
// WindowState.ScratchName, so no table maps one to the other.

// ScratchWorkspaceBase is the first scratch workspace number.
const ScratchWorkspaceBase = 1000

// IsScratchWorkspace reports whether ws is a scratch workspace.
func IsScratchWorkspace(ws int) bool { return ws >= ScratchWorkspaceBase }

// workspaceAccepts reports whether a window may be put on ws: an ordinary
// workspace in range, or a scratch workspace.
func (s *SessionState) workspaceAccepts(ws int) bool {
	return (ws >= 1 && ws <= s.workspaceBound()) || IsScratchWorkspace(ws)
}

// scratchGroupWorkspace is the workspace of the scratch group named name, and
// whether the group has a pane.
func scratchGroupWorkspace(state *SessionState, name string) (int, bool) {
	key := WindowState{ScratchName: name}.ScratchKey()
	for i := range state.Windows {
		if w := &state.Windows[i]; w.Scratch && w.ScratchKey() == key && IsScratchWorkspace(w.Workspace) {
			return w.Workspace, true
		}
	}
	return 0, false
}

// freeScratchWorkspace is the lowest scratch workspace no window is on.
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

// scratchNameOnWorkspace is the name of the group on a scratch workspace.
// ok is false when no pane is on it.
func scratchNameOnWorkspace(state *SessionState, ws int) (string, bool) {
	for i := range state.Windows {
		if w := &state.Windows[i]; w.Workspace == ws && w.Scratch {
			return w.ScratchName, true
		}
	}
	return "", false
}

// errNoScratchGroup refuses a window for a scratch workspace with no group on
// it: the group ended, and its number means nothing now.
func errNoScratchGroup(ws int) error {
	return fmt.Errorf("scratch workspace %d has no panes. Press the scratch key to start it again", ws)
}
