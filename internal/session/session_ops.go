package session

import (
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// defaultWorkspaces bounds the workspace indices the daemon-side state
// operations accept when the session state does not say how many it has. State
// written by a client that reports its workspace count uses that instead, so the
// bound is the session's own rather than a number this package guesses.
const defaultWorkspaces = 9

// workspaceBound returns how many workspaces this state has, for the range check
// on the operations that take a workspace index.
func (s *SessionState) workspaceBound() int {
	if s.NumWorkspaces > 0 {
		return s.NumWorkspaces
	}
	return defaultWorkspaces
}

// These daemon-side operations mutate a session's canonical SessionState
// directly, so mutating control verbs (create/close/focus/rename/move a window,
// switch workspace, minimize/restore) work with no TUI client attached. When a
// TUI client is attached the daemon keeps routing those verbs to it unchanged;
// these methods are the headless path. The TUI, on its next attach, rebuilds
// from this same state (the resurrection restore path), so a window created
// headless shows up when a client later connects.
//
// Every mutation here runs inside Session.mutateState, which diffs the state
// before and after and raises the window lifecycle events. None of these
// operations emits an event itself: the diff is the single emit site shared with
// the TUI's UpdateState path, which is what makes the events fire exactly once
// and identically on both paths.

// findWindowStateIndex resolves a window target string to an index into
// state.Windows. It matches, in order: an exact window ID, the position that
// list-windows prints when the target is all digits and in range, a unique
// window ID prefix, an exact CustomName, then an exact Title. It returns -1
// when there is no match, and an error when a prefix or name is ambiguous.
func findWindowStateIndex(windows []WindowState, target string) (int, error) {
	if target == "" {
		return -1, fmt.Errorf("empty window target")
	}

	// Exact ID.
	for i := range windows {
		if windows[i].ID == target {
			return i, nil
		}
	}

	// The index list-windows prints. It is the position in the slice, so it is
	// what a caller reading that output will reach for first. An out-of-range
	// number falls through: a long digit run can still be a valid id prefix.
	if idx, ok := WindowIndexTarget(target, len(windows)); ok {
		return idx, nil
	}

	// Unique ID prefix.
	prefixIdx, prefixCount := -1, 0
	for i := range windows {
		if strings.HasPrefix(windows[i].ID, target) {
			prefixIdx = i
			prefixCount++
		}
	}
	if prefixCount == 1 {
		return prefixIdx, nil
	}
	if prefixCount > 1 {
		return -1, fmt.Errorf("ambiguous window ID prefix %q matches %d windows", target, prefixCount)
	}

	// Exact CustomName, then Title.
	nameIdx, nameCount := -1, 0
	for i := range windows {
		if windows[i].CustomName == target || windows[i].Title == target {
			nameIdx = i
			nameCount++
		}
	}
	if nameCount == 1 {
		return nameIdx, nil
	}
	if nameCount > 1 {
		return -1, fmt.Errorf("ambiguous window name %q matches %d windows", target, nameCount)
	}

	return -1, fmt.Errorf("no window found matching %q", target)
}

// WindowIndexTarget reports whether a window target is an all-digit position
// into a window list of the given length, and which position. Both resolvers
// share it so an index means the same thing attached and detached.
func WindowIndexTarget(target string, count int) (int, bool) {
	if target == "" || len(target) > 4 {
		return -1, false
	}
	idx := 0
	for _, r := range target {
		if r < '0' || r > '9' {
			return -1, false
		}
		idx = idx*10 + int(r-'0')
	}
	if idx >= count {
		return -1, false
	}
	return idx, true
}

// firstVisibleOnWorkspace returns the ID of the first window in slice order that
// sits on the given workspace and is not minimized, or "" when the workspace has
// no such window.
//
// This is the focus-repair rule, and it is deliberately the same rule the
// renderer applies (OS.FocusNextVisibleWindow): first in order, minimized
// windows skipped, no focus at all when nothing visible remains. The daemon used
// to take the first window on the workspace whether or not it was minimized,
// which put focus on a window sitting in the dock while a visible one went
// unfocused. TestDaemonFocusRepairAfterClose pins every case.
func firstVisibleOnWorkspace(windows []WindowState, workspace int) string {
	for i := range windows {
		if windows[i].Workspace == workspace && !windows[i].Minimized {
			return windows[i].ID
		}
	}
	return ""
}

// AddDaemonWindow spawns a fresh PTY and appends a canonical window for it to
// the session state, focusing it on the current workspace. onExit (may be nil)
// is invoked with the PTY ID when the shell process exits. It returns a copy of
// the created window state. This is the headless equivalent of the TUI creating
// a new window; geometry is a nominal full-size box that a client re-tiles on
// attach.
func (s *Session) AddDaemonWindow(title string, onExit func(ptyID string)) (WindowState, error) {
	width, height := s.Size()
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	// WindowState dimensions are the outer window box (including the border);
	// the shell gets the inner content size, matching restoreSession.
	ptyWidth := max(width-2, 1)
	ptyHeight := max(height-2, 1)

	windowID := uuid.New().String()
	if title == "" {
		// The same default the renderer used when it still created windows
		// itself, so a window looks the same however it was asked for.
		title = "Terminal " + windowID[:8]
	}
	pty, err := s.CreatePTY(windowID, ptyWidth, ptyHeight, onExit)
	if err != nil {
		return WindowState{}, err
	}

	var win WindowState
	_ = s.mutateState(func(state *SessionState) error {
		if state.WorkspaceFocus == nil {
			state.WorkspaceFocus = make(map[int]string)
		}
		workspace := state.CurrentWorkspace
		if workspace < 1 {
			workspace = 1
			state.CurrentWorkspace = 1
		}

		win = WindowState{
			ID:        windowID,
			Title:     title,
			X:         0,
			Y:         0,
			Width:     width,
			Height:    height,
			Workspace: workspace,
			PTYID:     pty.ID,
			// The daemon has no viewport, so this box is a placeholder that keeps
			// the PTY a usable size until a client places the window properly.
			Unplaced: true,
		}
		state.Windows = append(state.Windows, win)
		state.FocusedWindowID = windowID
		state.WorkspaceFocus[workspace] = windowID
		return nil
	})
	return win, nil
}

// CloseDaemonWindow removes the window matching target from the session state
// and closes its PTY. It moves focus to another window in the same workspace
// when the closed window was focused. It returns the closed window's ID.
func (s *Session) CloseDaemonWindow(target string) (string, error) {
	var closed WindowState
	err := s.mutateState(func(state *SessionState) error {
		idx, err := findWindowStateIndex(state.Windows, target)
		if err != nil {
			return err
		}

		closed = state.Windows[idx]
		workspace := closed.Workspace
		state.Windows = append(state.Windows[:idx], state.Windows[idx+1:]...)
		// The window is gone, so nothing owns its agent state any more. The
		// detector sweeps stale claims on its own tick too, but only when it is
		// running, and a claim can now come from a source that is not the detector.
		delete(s.agentClaims, closed.ID)

		// Repair focus if we removed the focused window.
		if state.FocusedWindowID == closed.ID {
			state.FocusedWindowID = firstVisibleOnWorkspace(state.Windows, workspace)
		}
		if state.WorkspaceFocus != nil && state.WorkspaceFocus[workspace] == closed.ID {
			delete(state.WorkspaceFocus, workspace)
			if state.FocusedWindowID != "" {
				// Only re-point the workspace focus at a window that is actually on it.
				for i := range state.Windows {
					if state.Windows[i].ID == state.FocusedWindowID && state.Windows[i].Workspace == workspace {
						state.WorkspaceFocus[workspace] = state.FocusedWindowID
						break
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	// Close the PTY outside the state lock.
	if closed.PTYID != "" {
		_ = s.ClosePTY(closed.PTYID)
	}
	return closed.ID, nil
}

// FocusDaemonWindow makes the window matching target the focused window,
// switching the current workspace to that window's workspace.
func (s *Session) FocusDaemonWindow(target string) error {
	return s.mutateState(func(state *SessionState) error {
		idx, err := findWindowStateIndex(state.Windows, target)
		if err != nil {
			return err
		}
		win := state.Windows[idx]
		state.FocusedWindowID = win.ID
		state.CurrentWorkspace = win.Workspace
		if state.WorkspaceFocus == nil {
			state.WorkspaceFocus = make(map[int]string)
		}
		state.WorkspaceFocus[win.Workspace] = win.ID
		return nil
	})
}

// CycleDaemonFocus moves focus to the next (delta > 0) or previous (delta < 0)
// window on the current workspace, wrapping around. It is a no-op when the
// current workspace has fewer than two windows.
func (s *Session) CycleDaemonFocus(delta int) error {
	return s.mutateState(func(state *SessionState) error {
		// Collect indices of windows on the current workspace, in slice order.
		var order []int
		current := -1
		for i := range state.Windows {
			if state.Windows[i].Workspace != state.CurrentWorkspace {
				continue
			}
			if state.Windows[i].ID == state.FocusedWindowID {
				current = len(order)
			}
			order = append(order, i)
		}
		if len(order) == 0 {
			return fmt.Errorf("no windows on workspace %d", state.CurrentWorkspace)
		}
		if current == -1 {
			current = 0
		}

		step := 1
		if delta < 0 {
			step = -1
		}
		next := ((current+step)%len(order) + len(order)) % len(order)
		win := state.Windows[order[next]]
		state.FocusedWindowID = win.ID
		if state.WorkspaceFocus == nil {
			state.WorkspaceFocus = make(map[int]string)
		}
		state.WorkspaceFocus[state.CurrentWorkspace] = win.ID
		return nil
	})
}

// RenameDaemonWindow sets the CustomName of the window matching target.
func (s *Session) RenameDaemonWindow(target, name string) error {
	return s.mutateState(func(state *SessionState) error {
		idx, err := findWindowStateIndex(state.Windows, target)
		if err != nil {
			return err
		}
		state.Windows[idx].CustomName = name
		return nil
	})
}

// MoveDaemonWindowToWorkspace moves the window matching target to workspace ws.
func (s *Session) MoveDaemonWindowToWorkspace(target string, ws int) error {
	return s.mutateState(func(state *SessionState) error {
		if ws < 1 || ws > state.workspaceBound() {
			return fmt.Errorf("workspace %d out of range (1-%d)", ws, state.workspaceBound())
		}
		idx, err := findWindowStateIndex(state.Windows, target)
		if err != nil {
			return err
		}
		oldWorkspace := state.Windows[idx].Workspace
		state.Windows[idx].Workspace = ws

		// If the moved window held its old workspace's focus, drop it there.
		if state.WorkspaceFocus != nil && state.WorkspaceFocus[oldWorkspace] == state.Windows[idx].ID {
			delete(state.WorkspaceFocus, oldWorkspace)
		}
		return nil
	})
}

// SetDaemonWorkspaceName sets workspace ws's optional label, or clears it when
// name is empty. Clearing deletes the entry rather than storing an empty string,
// so an unnamed workspace leaves no trace in serialized state and reads back the
// way it did before workspaces could be named.
func (s *Session) SetDaemonWorkspaceName(ws int, name string) error {
	return s.mutateState(func(state *SessionState) error {
		if ws < 1 || ws > state.workspaceBound() {
			return fmt.Errorf("workspace %d out of range (1-%d)", ws, state.workspaceBound())
		}
		if name == "" {
			delete(state.WorkspaceNames, ws)
			return nil
		}
		if state.WorkspaceNames == nil {
			state.WorkspaceNames = make(map[int]string)
		}
		state.WorkspaceNames[ws] = name
		return nil
	})
}

// SetDaemonWorkspaceOrder records the order the workspaces are shown in. It
// moves no workspace: every number keeps everything it owns, and the windows,
// the focus map, the trees and the verbs all go on addressing by number.
//
// The order is sanitised rather than trusted, because it arrives from a drag
// that reads a list the client rendered and the daemon's own list may since
// have changed: an entry outside the session's range or naming a workspace
// already placed cannot mean anything, so it is dropped instead of stored.
//
// An ascending order is stored as nil. That is the arrangement a session with
// no order already has, so recording it would leave a trace in serialized state
// that says nothing, the way an empty name would.
func (s *Session) SetDaemonWorkspaceOrder(order []int) error {
	return s.mutateState(func(state *SessionState) error {
		bound := state.workspaceBound()
		clean := make([]int, 0, len(order))
		seen := make(map[int]bool, len(order))
		for _, ws := range order {
			if ws < 1 || ws > bound || seen[ws] {
				continue
			}
			seen[ws] = true
			clean = append(clean, ws)
		}
		if len(clean) == 0 && len(order) > 0 {
			return fmt.Errorf("no workspace in the order is in range (1-%d)", bound)
		}
		if slices.IsSorted(clean) {
			state.WorkspaceOrder = nil
			return nil
		}
		state.WorkspaceOrder = clean
		return nil
	})
}

// SwitchDaemonWorkspace sets the current workspace, restoring that workspace's
// last-focused window when one is recorded.
func (s *Session) SwitchDaemonWorkspace(ws int) error {
	return s.mutateState(func(state *SessionState) error {
		if ws < 1 || ws > state.workspaceBound() {
			return fmt.Errorf("workspace %d out of range (1-%d)", ws, state.workspaceBound())
		}
		state.CurrentWorkspace = ws
		if state.WorkspaceFocus != nil {
			if focus, ok := state.WorkspaceFocus[ws]; ok {
				state.FocusedWindowID = focus
			}
		}
		return nil
	})
}

// SetDaemonWindowMinimized sets the minimized flag on the window matching target.
func (s *Session) SetDaemonWindowMinimized(target string, minimized bool) error {
	return s.mutateState(func(state *SessionState) error {
		idx, err := findWindowStateIndex(state.Windows, target)
		if err != nil {
			return err
		}
		state.Windows[idx].Minimized = minimized
		return nil
	})
}
