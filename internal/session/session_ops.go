package session

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// maxDaemonWorkspaces bounds the workspace indices the daemon-side state
// operations accept. It mirrors the TUI's workspace count (config.MaxWorkspaces)
// but is duplicated here to keep the session package free of a config import.
const maxDaemonWorkspaces = 9

// These daemon-side operations mutate a session's canonical SessionState
// directly, so mutating control verbs (create/close/focus/rename/move a window,
// switch workspace, minimize/restore) work with no TUI client attached. When a
// TUI client is attached the daemon keeps routing those verbs to it unchanged;
// these methods are the headless path. The TUI, on its next attach, rebuilds
// from this same state (the resurrection restore path), so a window created
// headless shows up when a client later connects.

// findWindowStateIndex resolves a window target string to an index into
// state.Windows. It matches, in order: an exact window ID, a unique window ID
// prefix, an exact CustomName, then an exact Title. It returns -1 when there is
// no match, and an error when a prefix or name is ambiguous.
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
	pty, err := s.CreatePTY(windowID, ptyWidth, ptyHeight, onExit)
	if err != nil {
		return WindowState{}, err
	}

	s.stateMu.Lock()

	if s.state.WorkspaceFocus == nil {
		s.state.WorkspaceFocus = make(map[int]string)
	}
	workspace := s.state.CurrentWorkspace
	if workspace < 1 {
		workspace = 1
		s.state.CurrentWorkspace = 1
	}

	win := WindowState{
		ID:        windowID,
		Title:     title,
		X:         0,
		Y:         0,
		Width:     width,
		Height:    height,
		Workspace: workspace,
		PTYID:     pty.ID,
	}
	s.state.Windows = append(s.state.Windows, win)
	s.state.FocusedWindowID = windowID
	s.state.WorkspaceFocus[workspace] = windowID
	s.stateMu.Unlock()

	s.emit(SessionEvent{Type: EventWindowCreated, Window: windowID, PTYID: pty.ID, Title: title})
	return win, nil
}

// CloseDaemonWindow removes the window matching target from the session state
// and closes its PTY. It moves focus to another window in the same workspace
// when the closed window was focused. It returns the closed window's ID.
func (s *Session) CloseDaemonWindow(target string) (string, error) {
	s.stateMu.Lock()

	idx, err := findWindowStateIndex(s.state.Windows, target)
	if err != nil {
		s.stateMu.Unlock()
		return "", err
	}

	closed := s.state.Windows[idx]
	workspace := closed.Workspace
	s.state.Windows = append(s.state.Windows[:idx], s.state.Windows[idx+1:]...)

	// Repair focus if we removed the focused window.
	if s.state.FocusedWindowID == closed.ID {
		s.state.FocusedWindowID = ""
		for i := range s.state.Windows {
			if s.state.Windows[i].Workspace == workspace {
				s.state.FocusedWindowID = s.state.Windows[i].ID
				break
			}
		}
	}
	if s.state.WorkspaceFocus != nil && s.state.WorkspaceFocus[workspace] == closed.ID {
		delete(s.state.WorkspaceFocus, workspace)
		if s.state.FocusedWindowID != "" {
			// Only re-point the workspace focus at a window that is actually on it.
			for i := range s.state.Windows {
				if s.state.Windows[i].ID == s.state.FocusedWindowID && s.state.Windows[i].Workspace == workspace {
					s.state.WorkspaceFocus[workspace] = s.state.FocusedWindowID
					break
				}
			}
		}
	}
	s.stateMu.Unlock()

	// Close the PTY outside the state lock.
	if closed.PTYID != "" {
		_ = s.ClosePTY(closed.PTYID)
	}
	s.emit(SessionEvent{Type: EventWindowClosed, Window: closed.ID, PTYID: closed.PTYID})
	return closed.ID, nil
}

// FocusDaemonWindow makes the window matching target the focused window,
// switching the current workspace to that window's workspace.
func (s *Session) FocusDaemonWindow(target string) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	idx, err := findWindowStateIndex(s.state.Windows, target)
	if err != nil {
		return err
	}
	win := s.state.Windows[idx]
	s.state.FocusedWindowID = win.ID
	s.state.CurrentWorkspace = win.Workspace
	if s.state.WorkspaceFocus == nil {
		s.state.WorkspaceFocus = make(map[int]string)
	}
	s.state.WorkspaceFocus[win.Workspace] = win.ID
	return nil
}

// CycleDaemonFocus moves focus to the next (delta > 0) or previous (delta < 0)
// window on the current workspace, wrapping around. It is a no-op when the
// current workspace has fewer than two windows.
func (s *Session) CycleDaemonFocus(delta int) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	// Collect indices of windows on the current workspace, in slice order.
	var order []int
	current := -1
	for i := range s.state.Windows {
		if s.state.Windows[i].Workspace != s.state.CurrentWorkspace {
			continue
		}
		if s.state.Windows[i].ID == s.state.FocusedWindowID {
			current = len(order)
		}
		order = append(order, i)
	}
	if len(order) == 0 {
		return fmt.Errorf("no windows on workspace %d", s.state.CurrentWorkspace)
	}
	if current == -1 {
		current = 0
	}

	step := 1
	if delta < 0 {
		step = -1
	}
	next := ((current+step)%len(order) + len(order)) % len(order)
	win := s.state.Windows[order[next]]
	s.state.FocusedWindowID = win.ID
	if s.state.WorkspaceFocus == nil {
		s.state.WorkspaceFocus = make(map[int]string)
	}
	s.state.WorkspaceFocus[s.state.CurrentWorkspace] = win.ID
	return nil
}

// RenameDaemonWindow sets the CustomName of the window matching target.
func (s *Session) RenameDaemonWindow(target, name string) error {
	s.stateMu.Lock()

	idx, err := findWindowStateIndex(s.state.Windows, target)
	if err != nil {
		s.stateMu.Unlock()
		return err
	}
	s.state.Windows[idx].CustomName = name
	winID := s.state.Windows[idx].ID
	s.stateMu.Unlock()

	s.emit(SessionEvent{Type: EventWindowRetitled, Window: winID, Title: name})
	return nil
}

// MoveDaemonWindowToWorkspace moves the window matching target to workspace ws.
func (s *Session) MoveDaemonWindowToWorkspace(target string, ws int) error {
	if ws < 1 || ws > maxDaemonWorkspaces {
		return fmt.Errorf("workspace %d out of range (1-%d)", ws, maxDaemonWorkspaces)
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	idx, err := findWindowStateIndex(s.state.Windows, target)
	if err != nil {
		return err
	}
	oldWorkspace := s.state.Windows[idx].Workspace
	s.state.Windows[idx].Workspace = ws

	// If the moved window held its old workspace's focus, drop it there.
	if s.state.WorkspaceFocus != nil && s.state.WorkspaceFocus[oldWorkspace] == s.state.Windows[idx].ID {
		delete(s.state.WorkspaceFocus, oldWorkspace)
	}
	return nil
}

// SwitchDaemonWorkspace sets the current workspace, restoring that workspace's
// last-focused window when one is recorded.
func (s *Session) SwitchDaemonWorkspace(ws int) error {
	if ws < 1 || ws > maxDaemonWorkspaces {
		return fmt.Errorf("workspace %d out of range (1-%d)", ws, maxDaemonWorkspaces)
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	s.state.CurrentWorkspace = ws
	if s.state.WorkspaceFocus != nil {
		if focus, ok := s.state.WorkspaceFocus[ws]; ok {
			s.state.FocusedWindowID = focus
		}
	}
	return nil
}

// SetDaemonWindowMinimized sets the minimized flag on the window matching target.
func (s *Session) SetDaemonWindowMinimized(target string, minimized bool) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	idx, err := findWindowStateIndex(s.state.Windows, target)
	if err != nil {
		return err
	}
	s.state.Windows[idx].Minimized = minimized
	return nil
}
