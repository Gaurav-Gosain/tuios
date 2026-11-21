package session

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
	"github.com/google/uuid"
)

const defaultWorkspaces = 9

func (s *SessionState) workspaceBound() int {
	if s.NumWorkspaces > 0 {
		return s.NumWorkspaces
	}
	return defaultWorkspaces
}

func findWindowStateIndex(windows []WindowState, target string) (int, error) {
	if target == "" {
		return -1, errfmt.Errorf("empty window target")
	}

	for i := range windows {
		if windows[i].ID == target {
			return i, nil
		}
	}

	if idx, ok := WindowIndexTarget(target, len(windows)); ok {
		return idx, nil
	}

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
			return -1, errfmt.Errorf("ambiguous window name %q matches %d windows: %s. Use the id, or rename one with set-window --name", target, nameCount,
				describeWindows(windows, byName))
		}
	}

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
		return -1, errfmt.Errorf("ambiguous window ID prefix %q matches %d windows: %s. Use more of the id", target, prefixCount,
			describeWindows(windows, func(w WindowState) bool { return strings.HasPrefix(w.ID, target) }))
	}

	return -1, errfmt.Errorf("no window found matching %q", target)
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
	// CallerCwd is the folder of the pane process that asked for the window,
	// "" for a caller in no pane. With no Cwd it is inherited in place of the
	// focused pane's folder. See Daemon.callerCwd.
	CallerCwd string
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
	// PopupDim is the optional daemon-owned dim override, including zero.
	PopupDim *config.PopupDim
	// Scratch marks the popup as the session's scratch terminal. See
	// WindowState.Scratch. It means nothing unless Popup is set.
	Scratch bool
	// ScratchName is the scratch pane's name. See WindowState.ScratchName.
	ScratchName string
	// Rail makes the window the session's rail pane, keyed by this command
	// line. See WindowState.Rail. The window goes on a free scratch-range
	// workspace and its process starts at RailWidth by RailHeight cells.
	Rail                  string
	RailWidth, RailHeight int
	stdout                *os.File
	extraFiles            []*os.File
	keepOpen              bool
	modeChecked           bool
	closeOnExit           bool
	// Env is KEY=VALUE pairs the process gets on top of the daemon's own
	// environment, from a caller that passed its own (fan, start-agent). The
	// TUIOS_ variables are set after it, so it cannot change them. It is not
	// saved: a window a restore brings back starts with the daemon's
	// environment. A window on another machine ignores it.
	Env []string
	// FocusIfShown focuses the window only when its workspace is the one the
	// session shows, never changes the current workspace, and does not count
	// as a focus move. See ExecuteCommandPayload.FocusIfShown. It means
	// nothing with Focus set.
	FocusIfShown bool
	focusAskedBy string
	closeAskedBy string
	// TabKey names the workspace by its tab key, which follows it through a
	// renumber, when the caller read the workspace's number before one it
	// may not have seen. Zero names it by Workspace alone. See
	// ExecuteCommandPayload.TabKey.
	TabKey int
	// Grants is what the window's process may do through tuios, nil for the
	// default of [agents.permissions]. It is in force before the process
	// starts, and it is saved with the window. See pane_grants.go.
	Grants *Grants
	// Bounded says a pane without admin asked for the window, and Bound is
	// the folder that pane's files grant reaches ("" for none). The new
	// window starts inside Bound: a folder it inherits from the focused pane
	// that is outside it is replaced by Bound, and with no Bound the window
	// gets no start folder, so its files grant reaches nothing. A caller
	// checks an explicit Cwd against Bound first (Daemon.childWindowBound).
	Bounded bool
	Bound   string
	// Kind and URI make a window a renderer draws natively. See
	// WindowState.Kind. With no Command, its program is the placeholder.
	Kind string
	URI  string
	// Split puts the window beside a pane in the split tree of that pane's
	// workspace, in the same change that adds it, so no client ever holds
	// the window without its place. Workspace must be the pane's.
	Split *SplitPlacement
	// Cols and Rows are the content size the client that asked will give the
	// pane, zero when it did not say. The process starts at that size. A
	// shell started at the session's size and then resized once the client
	// placed the pane took a SIGWINCH while it was still starting, and zsh
	// then drew its prompt twice (#1205). A popup ignores them: it has a
	// size of its own.
	Cols, Rows int
	// ShellIntegration is auto or off for this pane's shell, "" for [shell]
	// integration. It means nothing for a window that runs a command. It is
	// not saved: a restored pane follows the config.
	ShellIntegration string
}

// WindowKinds are the kinds a window can be. Empty is "terminal".
var WindowKinds = []string{"terminal", "view", "web", "app"}

// PlaceholderCommand is the program of a window a renderer draws natively:
// it prints one line that says what the window shows, for a terminal
// client, and then waits without echoing what is typed.
func PlaceholderCommand(kind, uri string) []string {
	return []string{"/bin/sh", "-c", `stty -echo 2>/dev/null; printf '\033[2m%s pane\033[0m  %s\n' "$1" "$2"; exec sleep 2147483647`, "tuios-" + kind, kind, uri}
}

// AddDaemonWindowWith creates a daemon-owned window with explicit placement.
//
// Placement has to happen at creation rather than as a move afterwards: a
// window created on the current workspace and moved is visible on the wrong
// workspace for as long as the two calls take, which an attached client renders.
func (s *Session) AddDaemonWindowWith(opts NewWindowOptions, onExit func(ptyID string)) (WindowState, error) {
	if opts.Kind == "terminal" {
		opts.Kind = ""
	}
	if opts.Kind != "" && len(opts.Command) == 0 {
		opts.Command = PlaceholderCommand(opts.Kind, opts.URI)
	}
	if opts.Scratch {
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

	ptyWidth := max(width-2, 1)
	ptyHeight := max(height-2, 1)
	if opts.Popup {
		ptyWidth, ptyHeight = popupContentSize(width, height, s.LayoutReserve(), opts.PopupWidth, opts.PopupHeight)
		width, height = ptyWidth+2, ptyHeight+2
	} else if opts.Cols > 0 && opts.Rows > 0 {
		ptyWidth, ptyHeight = min(opts.Cols, ptyWidth+2), min(opts.Rows, ptyHeight+2)
		width, height = ptyWidth+2, ptyHeight+2
	}
	if opts.Rail != "" && opts.RailWidth > 0 && opts.RailHeight > 0 {
		ptyWidth, ptyHeight = opts.RailWidth, opts.RailHeight
		width, height = ptyWidth+2, ptyHeight+2
	}

	windowID := uuid.New().String()
	if title == "" {
		title = "Terminal " + windowID[:8]
	}
	if opts.Workspace != 0 {
		if st := s.GetState(); !st.workspaceAccepts(opts.Workspace) {
			return WindowState{}, errfmt.Errorf("workspace %d out of range (1-%d)", opts.Workspace, st.workspaceBound())
		}
	}

	cwd := opts.Cwd
	if cwd == "" && opts.Host == "" {
		cwd = s.inheritedCwd(opts.CallerCwd)
	}
	if cwd == "" && opts.Host == "" {
		cwd = s.StartDir()
	}
	noStartDir := false
	if opts.Bounded && opts.Host == "" {
		switch {
		case opts.Bound == "":
			noStartDir = true
		case cwd == "" || !pathUnder(realPath(cwd), opts.Bound):
			cwd = opts.Bound
		default:
			cwd = realPath(cwd)
		}
	}

	added := make(chan struct{})
	var addedOnce sync.Once
	markAdded := func() { addedOnce.Do(func() { close(added) }) }
	defer markAdded()
	if onExit != nil {
		exit := onExit
		onExit = func(ptyID string) {
			<-added
			exit(ptyID)
		}
	}

	pty, err := s.createPTY(ptyWidth, ptyHeight, ptySpawn{
		windowID: windowID, cwd: cwd, command: opts.Command, env: opts.Env, host: opts.Host,
		onExit: onExit, stdout: opts.stdout, extraFiles: opts.extraFiles, grants: opts.Grants, keepOpen: opts.keepOpen,
		workspace: opts.Workspace, noStartDir: noStartDir, closeOnExit: opts.closeOnExit,
		shellIntegration: opts.ShellIntegration,
	})
	if err != nil {
		return WindowState{}, err
	}
	if s.config != nil && s.config.CellSize != nil {
		if w, h, ok := s.config.CellSize(s.ID); ok {
			_ = pty.UpdatePixelDimensions(w, h)
		}
	}
	if windowSpawnedHook != nil {
		windowSpawnedHook(pty)
	}

	first := len(s.GetState().Windows) == 0
	var detected *WorktreeInfo
	if first {
		detectIn := cwd
		if detectIn == "" {
			detectIn, _ = pty.ProcessCwd()
		}
		if info, main, ok := worktree.DetectEither(detectIn); ok {
			detected = &WorktreeInfo{Info: info, Main: main}
		}
	}

	var win WindowState
	var split *layoutEdit
	var sizes []paneSize
	box := layout.Rect{}
	if opts.Split != nil {
		box = s.layoutBox()
	}
	err = s.mutateState(func(state *SessionState) error {
		scratch, scratchName := false, ""
		if opts.Scratch {
			key := WindowState{ScratchName: opts.ScratchName}.ScratchKey()
			for i := range state.Windows {
				if w := &state.Windows[i]; w.Scratch && w.ScratchKey() == key {
					return ErrScratchExists
				}
			}
			scratch, scratchName = true, scratchNameIf(opts)
			opts.Workspace = freeScratchWorkspace(state)
		} else if IsScratchWorkspace(opts.Workspace) {
			name, ok := scratchNameOnWorkspace(state, opts.Workspace)
			if !ok {
				return errNoScratchGroup(opts.Workspace)
			}
			scratch, scratchName = true, name
		}
		if opts.Rail != "" {
			for i := range state.Windows {
				if state.Windows[i].Rail != "" {
					return ErrRailPaneExists
				}
			}
			opts.Workspace = freeScratchWorkspace(state)
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
		if opts.TabKey > 0 {
			if at := state.WorkspaceForTabKey(opts.TabKey); at > 0 {
				workspace = at
			}
		}
		if opts.FocusIfShown && !scratch && hasPanes(state, workspace) {
			return ErrWorkspaceHasPane
		}

		win = WindowState{
			ID:          windowID,
			Title:       title,
			CustomName:  opts.Name,
			X:           0,
			Y:           0,
			Width:       width,
			Height:      height,
			Workspace:   workspace,
			PTYID:       pty.ID,
			Host:        opts.Host,
			ShellPID:    pty.ShellPID(),
			Unplaced:    true,
			Popup:       opts.Popup,
			IsFloating:  opts.Popup,
			PopupWidth:  opts.PopupWidth,
			PopupHeight: opts.PopupHeight,
			PopupDim:    opts.PopupDim,
			Scratch:     scratch,
			ScratchName: scratchName,
			Rail:        opts.Rail,
			Kind:        opts.Kind,
			URI:         opts.URI,
		}
		if opts.Host == "" {
			win.Grants = grantNamesPtr(opts.Grants)
		}
		if opts.Split != nil {
			split = newLayoutEdit(state, box)
			if !split.tree(workspace).HasWindow(split.leaf(opts.Split.Target)) {
				return errSplitNotTiled
			}
		}
		state.Windows = append(state.Windows, win)
		if split != nil {
			t := split.tree(workspace)
			t.SplitWindowAt(split.leaf(windowID), split.leaf(opts.Split.Target), opts.Split.Side, opts.Split.Share)
			split.commit()
			s.noteTreeOpLocked(state.Version+1, "", workspace)
			state.DaemonLayout = state.Version + 1
			if opts.Split.Place {
				sizes = split.place()
				win = state.Windows[len(state.Windows)-1]
			}
		}
		state.WorkspaceFocus[workspace] = windowID
		if opts.Focus {
			s.markFocusIntentLocked()
			s.focusAskedBy = opts.focusAskedBy
			state.FocusedWindowID = windowID
			if !IsScratchWorkspace(workspace) {
				state.CurrentWorkspace = workspace
			}
			state.FocusHistory = RecordFocus(state.FocusHistory, workspace, windowID)
		} else if opts.FocusIfShown {
			if workspace == state.CurrentWorkspace {
				state.FocusedWindowID = windowID
				state.FocusHistory = RecordFocus(state.FocusHistory, workspace, windowID)
			}
			s.focusNeutral = true
		}
		return nil
	})
	markAdded()
	s.resizePanes(sizes)
	if err != nil {
		_ = s.ClosePTY(pty.ID)
		return WindowState{}, err
	}
	return win, nil
}

var windowSpawnedHook func(*PTY)

// ErrWorkspaceHasPane is the refusal of a FocusIfShown window on a workspace
// that has a pane already. The caller treats it as a success that did
// nothing. See ExecuteCommandPayload.FocusIfShown.
var ErrWorkspaceHasPane = errors.New("the workspace has a pane already")

// ErrRailPaneExists is the refusal of a second rail pane in a session.
var ErrRailPaneExists = errors.New("this session already has a rail pane")

// ErrScratchExists is the refusal of a second scratch terminal in a session.
var ErrScratchExists = errors.New("this session already has a scratch terminal of this name. Press its key to show it")

func scratchNameIf(opts NewWindowOptions) string {
	if !opts.Scratch || opts.ScratchName == "scratch" {
		return ""
	}
	return opts.ScratchName
}

// CloseDaemonWindow removes the window matching target from the session state
// and closes its PTY. It moves focus to another window in the same workspace
// when the closed window was focused. It returns the closed window's ID.
//
// by names who asked, and goes to the log with the window, so a pane that is
// gone is one grep: "closed, asked for by" and its short ID.
func (s *Session) CloseDaemonWindow(target, by string) (string, error) {
	return s.closeDaemonWindow(target, by, s.ClosePTY)
}

func (s *Session) closeDaemonWindow(target, by string, closePTY func(string) error) (string, error) {
	var closed WindowState
	err := s.mutateState(func(state *SessionState) error {
		idx, err := findWindowStateIndex(state.Windows, target)
		if err != nil {
			return err
		}

		closed = s.removeWindowLocked(state, idx)
		s.dropPaneStatesLocked(closed.ID)
		return nil
	})
	if err != nil {
		return "", err
	}
	s.dropPaneStores(closed.ID)

	if by == "" {
		by = "an unnamed caller"
	}
	LogBasic("Window %s %q (PTY %s) in session %s closed, asked for by %s",
		shortID(closed.ID), windowLabelOf(closed), shortID(closed.PTYID), s.Name(), by)
	if closed.PTYID != "" {
		_ = closePTY(closed.PTYID)
	}
	return closed.ID, nil
}

func (s *Session) dropPaneStatesLocked(id string) {
	delete(s.agentClaims, id)
	delete(s.agentHarnessPIDs, id)
	delete(s.agentSharedGuards, id)
	delete(s.askShows, id)
	delete(s.agentTurns, id)
	delete(s.agentSubagents, id)
	delete(s.completionSeen, id)
}

func (s *Session) dropPaneStores(id string) {
	s.dropAgentHold(id)
	s.idle.forget(id)
	s.startDirs.Delete(id)
	s.agentDirsMu.Lock()
	delete(s.agentDirs, id)
	s.agentDirsMu.Unlock()
}

func (s *Session) removeWindowLocked(state *SessionState, idx int) WindowState {
	gone := state.Windows[idx]
	workspace := gone.Workspace
	state.Windows = append(state.Windows[:idx], state.Windows[idx+1:]...)
	defer func() {
		if !gone.Zoomed || gone.Popup || state.FocusedWindowID == "" {
			return
		}
		for i := range state.Windows {
			if w := &state.Windows[i]; w.ID == state.FocusedWindowID && w.Workspace == workspace && !w.Popup {
				w.Zoomed = true
			}
		}
	}()
	state.FocusHistory = RemoveFocus(state.FocusHistory, gone.ID)
	if _, ok := state.WindowToBSPID[gone.ID]; ok {
		ids := maps.Clone(state.WindowToBSPID)
		delete(ids, gone.ID)
		state.WindowToBSPID = ids
	}

	if state.FocusedWindowID == gone.ID {
		state.FocusedWindowID = focusAfterClose(state, workspace, gone.ID)
	}
	if state.WorkspaceFocus != nil && state.WorkspaceFocus[workspace] == gone.ID {
		delete(state.WorkspaceFocus, workspace)
		if state.FocusedWindowID != "" {
			for i := range state.Windows {
				if state.Windows[i].ID == state.FocusedWindowID && state.Windows[i].Workspace == workspace {
					state.WorkspaceFocus[workspace] = state.FocusedWindowID
					break
				}
			}
		}
	}
	s.returnFromEmptyLocked(state, workspace)
	return gone
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
		s.markFocusIntentLocked()
		state.FocusedWindowID = win.ID
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
		var order []int
		current := -1
		for i := range state.Windows {
			if state.Windows[i].Workspace != state.CurrentWorkspace {
				continue
			}
			if state.Windows[i].Scratch && state.Windows[i].Minimized {
				continue
			}
			if state.Windows[i].ID == state.FocusedWindowID {
				current = len(order)
			}
			order = append(order, i)
		}
		if len(order) == 0 {
			return errfmt.Errorf("no windows on workspace %d", state.CurrentWorkspace)
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

func (s *Session) setStartedBy(window string, ref *PaneRef) {
	_ = s.mutateState(func(state *SessionState) error {
		idx, err := findWindowStateIndex(state.Windows, window)
		if err != nil {
			return err
		}
		state.Windows[idx].StartedBy = ref
		return nil
	})
}

func (s *Session) renameStartedBy(old, name string) {
	hit := false
	for _, w := range s.GetState().Windows {
		if w.StartedBy != nil && w.StartedBy.Session == old || w.AgentLink != nil && w.AgentLink.Root.Session == old {
			hit = true
			break
		}
	}
	if !hit {
		return
	}
	_ = s.mutateState(func(state *SessionState) error {
		for i := range state.Windows {
			if ref := state.Windows[i].StartedBy; ref != nil && ref.Session == old {
				state.Windows[i].StartedBy = &PaneRef{Session: name, Window: ref.Window}
			}
			if l := state.Windows[i].AgentLink; l != nil && l.Root.Session == old {
				moved := *l
				moved.Root.Session = name
				state.Windows[i].AgentLink = &moved
			}
		}
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
			return errfmt.Errorf("workspace %d out of range (1-%d)", ws, state.workspaceBound())
		}
		idx, err := findWindowStateIndex(state.Windows, target)
		if err != nil {
			return err
		}
		if state.Windows[idx].Scratch {
			return errScratchPaneStays
		}
		oldWorkspace := state.Windows[idx].Workspace
		state.Windows[idx].Workspace = ws

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
			return errfmt.Errorf("workspace %d out of range (1-%d)", ws, state.workspaceBound())
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

// SwitchDaemonWorkspace sets the current workspace, restoring that workspace's
// last-focused window when one is recorded.
func (s *Session) SwitchDaemonWorkspace(ws int) error {
	return s.mutateState(func(state *SessionState) error {
		if ws < 1 || ws > state.workspaceBound() {
			return errfmt.Errorf("workspace %d out of range (1-%d)", ws, state.workspaceBound())
		}
		s.markFocusIntentLocked()
		state.CurrentWorkspace = ws
		if state.WorkspaceFocus != nil {
			if focus, ok := state.WorkspaceFocus[ws]; ok {
				state.FocusedWindowID = focus
				state.FocusHistory = RecordFocus(state.FocusHistory, ws, focus)
			}
		}
		unfocusScratchLocked(state)
		return nil
	})
}

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
			if state.FocusedWindowID == w.ID {
				state.FocusedWindowID = ""
			}
		}
		return nil
	})
}

func (s *Session) markRestoredViews(views map[string][2]string) {
	_ = s.mutateState(func(state *SessionState) error {
		for i := range state.Windows {
			w := &state.Windows[i]
			if v, ok := views[w.ID]; ok {
				w.Kind, w.URI = v[0], v[1]
			}
		}
		return nil
	})
}

// RestoreFocusedDaemonWindow clears the minimized flag of the focused
// window. A stack's title row keeps its flag: it says which member is open.
func (s *Session) RestoreFocusedDaemonWindow() {
	_ = s.mutateState(func(state *SessionState) error {
		for i := range state.Windows {
			w := &state.Windows[i]
			if w.ID == state.FocusedWindowID && w.Minimized && w.Stack == "" {
				w.Minimized = false
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
