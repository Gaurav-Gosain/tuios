package session

import (
	"errors"
	"fmt"
	"os"
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
// list-windows prints when the target is all digits and in range, an exact
// CustomName, an exact Title, then a unique window ID prefix. It returns -1
// when there is no match, and an error when a name or prefix is ambiguous.
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

	// Exact name, before the id prefix. A window id is a uuid, so any name
	// made only of hex digits ("db", "cafe", "a1") is also a prefix of an id
	// some of the time, about once in 256 windows for a two letter name. With
	// the prefix tried first, a target naming one window reached whichever
	// other window's id happened to start with it, and set-agent-state on
	// "db" reported success for a pane that was never touched. A name is what
	// a person typed on purpose; an id prefix that is also somebody's name is
	// a coincidence.
	//
	// A name given with set-window or new-window wins over a title, which the
	// program in the window sets and can change at any time, so a window's own
	// name cannot be shadowed by another window's title.
	for _, byName := range []func(w WindowState) bool{
		func(w WindowState) bool { return w.CustomName == target },
		func(w WindowState) bool { return w.Title == target },
	} {
		nameIdx, nameCount := -1, 0
		for i := range windows {
			if byName(windows[i]) {
				nameIdx = i
				nameCount++
			}
		}
		if nameCount == 1 {
			return nameIdx, nil
		}
		if nameCount > 1 {
			return -1, fmt.Errorf("ambiguous window name %q matches %d windows: %s. Use the id, or rename one with set-window --name", target, nameCount,
				describeWindows(windows, byName))
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
		return -1, fmt.Errorf("ambiguous window ID prefix %q matches %d windows: %s. Use more of the id", target, prefixCount,
			describeWindows(windows, func(w WindowState) bool { return strings.HasPrefix(w.ID, target) }))
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

func focusAfterClose(state *SessionState, workspace int, closed string) string {
	if state.FocusHistory != nil {
		history := state.FocusHistory[workspace]
		kept := history[:0]
		for _, id := range history {
			if id == closed {
				continue
			}
			for _, w := range state.Windows {
				if w.ID == id && w.Workspace == workspace && !w.Minimized {
					kept = append(kept, id)
					break
				}
			}
		}
		if len(kept) > 0 {
			state.FocusHistory[workspace] = kept
			return kept[0]
		}
		delete(state.FocusHistory, workspace)
	}
	return firstVisibleOnWorkspace(state.Windows, workspace)
}

// AddDaemonWindow spawns a fresh PTY and appends a canonical window for it to
// the session state, focusing it on the current workspace. onExit (may be nil)
// is invoked with the PTY ID when the shell process exits. It returns a copy of
// the created window state. This is the headless equivalent of the TUI creating
// a new window; geometry is a nominal full-size box that a client re-tiles on
// attach.
func (s *Session) AddDaemonWindow(title string, onExit func(ptyID string)) (WindowState, error) {
	return s.AddDaemonWindowWith(NewWindowOptions{Title: title, Focus: true}, onExit)
}

// NewWindowOptions says where a daemon-created window goes and what its shell
// starts in. The zero value is the historical behaviour of AddDaemonWindow
// except for Focus, which every existing caller wants and passes explicitly.
type NewWindowOptions struct {
	// Title is the window's name. Empty takes the generated default.
	Title string
	// Cwd is the directory the shell starts in. Empty inherits the daemon's,
	// which is what every window did before this existed.
	Cwd string
	// Workspace is the workspace to place the window on. Zero means whichever
	// workspace is current, so a caller that does not care keeps the old
	// behaviour.
	Workspace int
	// Focus says whether to focus the new window. A caller opening a pane to
	// work in later wants it created without stealing the focus from the pane
	// the user is in.
	Focus bool
	// Command, when non-empty, is an argv exec'd as the window's process in
	// place of a shell. The daemon execs it itself because it is the side that
	// spawns the PTY; sending bytes for a shell to re-parse instead would make
	// the command's meaning depend on which shell answered.
	Command []string
	// Host asks for the window's process on another machine, named as it is in
	// the [hosts] config table. Empty is this machine, which is every window
	// unless someone asked otherwise. See hosted_pane.go.
	Host string
	// Name is the window's custom name, the one the dock and the rail show.
	//
	// It is set here rather than by a rename afterwards for the same reason the
	// workspace is: two mutations are two state pushes, and a client that adopts
	// the window on the first one sees it unnamed. That is not only a frame of
	// the wrong title. The launcher's type-it-out path waits for a pane by the
	// name it asked for, and adopting it unnamed meant the match never happened
	// on any later push either, because a window is only ever new once.
	Name string
	// Popup makes the window a popup: a floating pane that closes when its
	// command exits. It is set at creation for the reason the workspace is. A
	// window created plain and marked afterwards is an ordinary pane for as long
	// as the two calls take, and an attached client tiles it in that gap.
	Popup bool
	// PopupWidth and PopupHeight are the size the caller asked for, as written.
	// See WindowState.PopupWidth. They mean nothing unless Popup is set.
	PopupWidth  string
	PopupHeight string
	// Scratch marks the popup as the session's scratch terminal. See
	// WindowState.Scratch. It means nothing unless Popup is set.
	Scratch bool
	// ScratchName is the scratch pane's name. See WindowState.ScratchName.
	ScratchName string
	// stdout, when set, is the process's standard output in place of the PTY.
	// See createPTY. It is unexported: only the popup verb's capture sets it.
	stdout *os.File
	// extraFiles, when set, are open files the process inherits as fd 3 and
	// up. It is unexported: only verify-fan's status pipe sets it, and only
	// for a local process on a platform that passes them (not Windows).
	extraFiles []*os.File
	// Env is KEY=VALUE pairs the process gets on top of the daemon's own
	// environment, from a caller that passed its own (fan, start-agent). The
	// TUIOS_ variables are set after it, so it cannot change them. It is not
	// saved: a window a restore brings back starts with the daemon's
	// environment. A window on another machine ignores it.
	Env []string
	// Grants is what the window's process may do through tuios, nil for the
	// default of [agents.permissions]. It is in force before the process
	// starts, and it is saved with the window. See pane_grants.go.
	Grants *Grants
}

// AddDaemonWindowWith creates a daemon-owned window with explicit placement.
//
// Placement has to happen at creation rather than as a move afterwards: a
// window created on the current workspace and moved is visible on the wrong
// workspace for as long as the two calls take, which an attached client renders.
func (s *Session) AddDaemonWindowWith(opts NewWindowOptions, onExit func(ptyID string)) (WindowState, error) {
	// A scratch pane is an ordinary tiled window on its group's workspace, not
	// a popup. See scratch_workspace.go. While a client too old for that is
	// attached it is a popup on the current workspace, as that client
	// expects.
	legacyScratch := opts.Scratch && !s.scratchWorkspacesOn()
	if opts.Scratch && !legacyScratch {
		opts.Popup = false
	}
	title := opts.Title
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
	// A popup starts at the box a client will give it, so its command reads
	// its real size when it starts. A program that reads its size once and
	// then acts on it (a nested tuios client, a pager) otherwise starts at
	// the whole session's size and is told the real one only after a client
	// has placed the popup.
	//
	// The window's box is the popup's box too: the daemon sizes a PTY by
	// its window's box, and the session's box would undo this at once.
	if opts.Popup {
		ptyWidth, ptyHeight = popupContentSize(width, height, s.LayoutReserve(), opts.PopupWidth, opts.PopupHeight)
		width, height = ptyWidth+2, ptyHeight+2
	}

	windowID := uuid.New().String()
	if title == "" {
		// The same default the renderer used when it still created windows
		// itself, so a window looks the same however it was asked for.
		title = "Terminal " + windowID[:8]
	}
	// Reject the workspace before spawning anything: a PTY created for a window
	// that then fails to be placed is a process nothing owns.
	if opts.Workspace != 0 {
		if st := s.GetState(); !st.workspaceAccepts(opts.Workspace) {
			return WindowState{}, fmt.Errorf("workspace %d out of range (1-%d)", opts.Workspace, st.workspaceBound())
		}
	}

	// A caller that named a directory gets it. One that did not inherits the
	// focused pane's, which is what appearance.new_window_inherit_cwd asks for;
	// with the setting off, or with nothing to inherit, this stays empty and
	// the shell starts where the daemon did.
	cwd := opts.Cwd
	// Inheriting is the focused pane's directory, and that is a path on this
	// machine. Handing it to another machine would start the shell somewhere
	// unrelated on the rare occasion the path happens to exist there, so a
	// window with a host inherits nothing and starts where its own shell would.
	if cwd == "" && opts.Host == "" {
		cwd = s.inheritedCwd()
	}

	pty, err := s.createPTY(ptyWidth, ptyHeight, ptySpawn{
		windowID: windowID, cwd: cwd, command: opts.Command, env: opts.Env, host: opts.Host,
		onExit: onExit, stdout: opts.stdout, extraFiles: opts.extraFiles, grants: opts.Grants,
		workspace: opts.Workspace,
	})
	if err != nil {
		return WindowState{}, err
	}

	// The session's directory is its first window's, so the first window is
	// the one that can make this a worktree session. Detection runs here,
	// before the mutation, so the window and the record reach clients in one
	// push rather than two.
	first := len(s.GetState().Windows) == 0
	var detected *WorktreeInfo
	if first {
		detectIn := cwd
		if detectIn == "" {
			detectIn, _ = pty.ProcessCwd()
		}
		detected = detectWorktree(detectIn)
	}

	var win WindowState
	err = s.mutateState(func(state *SessionState) error {
		// One scratch terminal per session, checked under the state lock so
		// two calls that race cannot both add one.
		scratch, scratchName := false, ""
		if opts.Scratch {
			key := WindowState{ScratchName: opts.ScratchName}.ScratchKey()
			for i := range state.Windows {
				if w := &state.Windows[i]; w.Scratch && w.ScratchKey() == key {
					return ErrScratchExists
				}
			}
			scratch, scratchName = true, scratchNameIf(opts)
			if !legacyScratch {
				opts.Workspace = freeScratchWorkspace(state)
			}
		} else if IsScratchWorkspace(opts.Workspace) {
			// A split inside a scratch group joins the group.
			name, ok := scratchNameOnWorkspace(state, opts.Workspace)
			if !ok {
				return errNoScratchGroup(opts.Workspace)
			}
			scratch, scratchName = true, name
		}
		if first && (state.Worktree == nil || !state.Worktree.Managed) {
			state.Worktree = detected
		}
		if state.WorkspaceFocus == nil {
			state.WorkspaceFocus = make(map[int]string)
		}
		workspace := state.CurrentWorkspace
		if workspace < 1 {
			workspace = 1
			state.CurrentWorkspace = 1
		}
		if opts.Workspace != 0 {
			workspace = opts.Workspace
		}

		win = WindowState{
			ID:         windowID,
			Title:      title,
			CustomName: opts.Name,
			X:          0,
			Y:          0,
			Width:      width,
			Height:     height,
			Workspace:  workspace,
			PTYID:      pty.ID,
			// The machine the process is on. Empty for a window of this
			// daemon's own, which is what makes the field free for every
			// session that is not global.
			Host: opts.Host,
			// Stamped here as well as on the detector's poll, because the pid is
			// already in hand and a pane that waits a poll for it is a pane the
			// rail cannot check for two seconds. The poll stays the authority: it
			// corrects this and clears it when the shell goes.
			ShellPID: pty.ShellPID(),
			// The daemon has no viewport, so this box is a placeholder that keeps
			// the PTY a usable size until a client places the window properly.
			Unplaced: true,
			// A popup is always floating. The daemon marks it here rather than
			// leaving the client to infer it, because every peer reads the float
			// as layout intent and a peer that missed it tiles the popup away.
			Popup:       opts.Popup,
			IsFloating:  opts.Popup,
			PopupWidth:  opts.PopupWidth,
			PopupHeight: opts.PopupHeight,
			Scratch:     scratch,
			ScratchName: scratchName,
		}
		// A window on another machine holds what that machine gives it.
		if opts.Host == "" {
			win.Grants = grantNamesPtr(opts.Grants)
		}
		state.Windows = append(state.Windows, win)
		// The workspace's own focus points at the new window either way: it is
		// the only sensible thing to focus when that workspace is next shown.
		// Session focus moves only when the caller asked for it, so a pane opened
		// to work in later does not pull the user out of what they are doing.
		state.WorkspaceFocus[workspace] = windowID
		if opts.Focus {
			s.markFocusIntentLocked()
			state.FocusedWindowID = windowID
			// A scratch workspace is never the session's current one: a
			// client shows it over the workspace it is on.
			if !IsScratchWorkspace(workspace) {
				state.CurrentWorkspace = workspace
			}
			state.FocusHistory = RecordFocus(state.FocusHistory, workspace, windowID)
		}
		return nil
	})
	if err != nil {
		// The shell started for a window that was refused has no owner.
		_ = s.ClosePTY(pty.ID)
		return WindowState{}, err
	}
	return win, nil
}

// ErrScratchExists is the refusal of a second scratch terminal in a session.
var ErrScratchExists = errors.New("this session already has a scratch terminal of this name. Press its key to show it")

// scratchNameIf is the name a new window keeps, empty for any window that is
// not a scratch pane and for the built-in scratch terminal.
func scratchNameIf(opts NewWindowOptions) string {
	if !opts.Scratch || opts.ScratchName == "scratch" {
		return ""
	}
	return opts.ScratchName
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
		state.FocusHistory = RemoveFocus(state.FocusHistory, closed.ID)
		// The window is gone, so nothing owns its agent state any more. The
		// detector sweeps stale claims on its own tick too, but only when it is
		// running, and a claim can now come from a source that is not the detector.
		s.forgetAgentClaimLocked(closed.ID)

		// Repair focus if we removed the focused window.
		if state.FocusedWindowID == closed.ID {
			state.FocusedWindowID = focusAfterClose(state, workspace, closed.ID)
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
		// A scratch popup, from a session in which a client too old for
		// scratch workspaces is attached, is shown on the current workspace
		// before it takes the focus.
		if w := &state.Windows[idx]; w.Scratch && w.Popup && w.Minimized {
			w.Minimized = false
			w.Workspace = state.CurrentWorkspace
		}
		win := state.Windows[idx]
		// No focus goes to a scratch workspace a client cannot show.
		if IsScratchWorkspace(win.Workspace) && s.scratchWSOff {
			return fmt.Errorf("an older tuios client is attached to this session, so a scratch pane cannot take the focus")
		}
		s.markFocusIntentLocked()
		state.FocusedWindowID = win.ID
		// A scratch pane takes the focus without its workspace becoming the
		// session's: a client that sees the focus on a scratch pane shows the
		// group over the workspace it is on (OS.syncScratchView).
		if !IsScratchWorkspace(win.Workspace) {
			state.CurrentWorkspace = win.Workspace
		}
		if state.WorkspaceFocus == nil {
			state.WorkspaceFocus = make(map[int]string)
		}
		state.WorkspaceFocus[win.Workspace] = win.ID
		state.FocusHistory = RecordFocus(state.FocusHistory, win.Workspace, win.ID)
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
			// The cycle never lands on a hidden scratch terminal.
			if state.Windows[i].Scratch && state.Windows[i].Minimized {
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
		s.markFocusIntentLocked()
		state.FocusedWindowID = win.ID
		if state.WorkspaceFocus == nil {
			state.WorkspaceFocus = make(map[int]string)
		}
		state.WorkspaceFocus[state.CurrentWorkspace] = win.ID
		state.FocusHistory = RecordFocus(state.FocusHistory, state.CurrentWorkspace, win.ID)
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
		state.Windows[idx].CustomName = ClampDisplayText(name)
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
		// A scratch pane stays in its group: moved off its workspace, the
		// group would lose it.
		if state.Windows[idx].Scratch {
			return errScratchPaneStays
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
		s.markFocusIntentLocked()
		state.CurrentWorkspace = ws
		if state.WorkspaceFocus != nil {
			if focus, ok := state.WorkspaceFocus[ws]; ok {
				state.FocusedWindowID = focus
				state.FocusHistory = RecordFocus(state.FocusHistory, ws, focus)
			}
		}
		// A focus left on a scratch pane would show its group over the
		// workspace just selected: the selection means the workspace.
		unfocusScratchLocked(state)
		return nil
	})
}

// markRestoredScratch marks the window id as the session's scratch terminal,
// hidden. A restore calls it: a push cannot set the mark, so the restore sets
// it on canonical state after the push. See daemon_resurrect.go.
func (s *Session) markRestoredScratch(panes map[string]scratchPane) {
	_ = s.mutateState(func(state *SessionState) error {
		for i := range state.Windows {
			w := &state.Windows[i]
			p, ok := panes[w.ID]
			if !ok {
				continue
			}
			w.Scratch, w.ScratchName, w.Workspace = true, p.name, p.workspace
			w.Popup, w.IsFloating, w.Minimized = false, false, false
			// A hidden group holds no focus, or the first client would show it.
			if state.FocusedWindowID == w.ID {
				state.FocusedWindowID = ""
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

// describeWindows lists the windows match accepts as index, short id and name,
// for an error that has to say which windows a target could have meant.
func describeWindows(windows []WindowState, match func(WindowState) bool) string {
	var parts []string
	for i := range windows {
		if !match(windows[i]) {
			continue
		}
		name := windows[i].CustomName
		if name == "" {
			name = windows[i].Title
		}
		parts = append(parts, fmt.Sprintf("%d %s (%s)", i, shortWindowID(windows[i].ID), name))
	}
	return strings.Join(parts, ", ")
}
