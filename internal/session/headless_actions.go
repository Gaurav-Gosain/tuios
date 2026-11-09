package session

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// Keymap actions with no TUI client (parity gap 1, step 1).
//
// run-command names any keymap action as the tape's Action command, and a
// session with a TUI routes it there, where the TUI runs it in its own state.
// With no TUI the daemon ran only its few tape commands and answered every
// action that it needs an attached client, so guios, the CLI and an agent got
// "this action needs the tuios TUI" for a focus or a workspace move the daemon
// can do itself. These are the actions that are only an op on session state
// the daemon owns already: it runs them here, through the same session ops the
// verbs use, so every client gets the change in the next state push.
//
// What a client shows stays the client's (issue #162): the workspace a client
// was on before, which last_workspace goes back to, is not here. Neither is
// kill_session, which waits for a step of its own behind kill-session's
// checks.

// runHeadlessAction runs keymap action name on sess with no client, or
// reports errNeedsClient for one it has no headless form of.
func (d *Daemon) runHeadlessAction(sess *Session, name string, base NewWindowOptions) (map[string]any, error) {
	if n, ok := actionDigit(name, "select_window_"); ok {
		return nil, headlessSelectWindow(sess, n)
	}
	if n, ok := actionDigit(name, "send_to_workspace_"); ok {
		return nil, headlessSendToWorkspace(sess, n, false)
	}
	if n, ok := actionDigit(name, "move_and_follow_"); ok {
		return nil, headlessSendToWorkspace(sess, n, true)
	}
	switch name {
	case "last_pane":
		return nil, headlessLastPane(sess)
	case "break_pane":
		return headlessBreakPane(sess)
	case "close_workspace":
		return headlessCloseWorkspace(sess, base.closeAskedBy)
	case "move_workspace_left":
		return nil, headlessMoveWorkspace(sess, -1)
	case "move_workspace_right":
		return nil, headlessMoveWorkspace(sess, 1)
	case "minimize_window":
		id, err := focusedWindowID(sess.GetState())
		if err != nil {
			return nil, err
		}
		return nil, sess.SetDaemonWindowMinimized(id, true)
	case "restore_all":
		return nil, headlessRestoreAll(sess)
	}
	return nil, errNeedsClient{verb: "Action " + name}
}

// actionDigit reads the 1 to 9 at the end of an action such as
// select_window_3.
func actionDigit(name, prefix string) (int, bool) {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok || len(rest) != 1 {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n >= 1 && n <= 9
}

// headlessSelectWindow focuses the nth window of the current workspace,
// counted as the TUI counts them (selectWindowByIndex): in window order, and
// past the minimized ones while the workspace tiles.
func headlessSelectWindow(sess *Session, n int) error {
	state := sess.GetState()
	count := 0
	for _, w := range state.Windows {
		if w.Workspace != state.CurrentWorkspace || state.AutoTiling && w.Minimized {
			continue
		}
		count++
		if count == n {
			return sess.FocusDaemonWindow(w.ID)
		}
	}
	return errfmt.Errorf("workspace %d has %d windows, so there is no window %d", state.CurrentWorkspace, count, n)
}

// headlessLastPane focuses the pane focused before the current one on this
// workspace, from the session's focus history (newest first).
func headlessLastPane(sess *Session) error {
	state := sess.GetState()
	cur, _ := focusedWindowID(state)
	for _, id := range state.FocusHistory[state.CurrentWorkspace] {
		if id == cur {
			continue
		}
		if i, err := findWindowStateIndex(state.Windows, id); err == nil && state.Windows[i].Workspace == state.CurrentWorkspace {
			return sess.FocusDaemonWindow(id)
		}
	}
	return errfmt.Errorf("no pane to go back to on workspace %d", state.CurrentWorkspace)
}

// headlessSendToWorkspace moves the focused window to workspace ws. With
// follow it switches to where the window went: the daemon numbers its
// workspaces with no gap, so a move past an empty workspace lands on a lower
// number than ws. Without follow the focus stays on the current workspace,
// on the pane focused there before, as the TUI's MoveWindowToWorkspace does,
// not on a window now out of view.
func headlessSendToWorkspace(sess *Session, ws int, follow bool) error {
	state := sess.GetState()
	id, err := focusedWindowID(state)
	if err != nil {
		return err
	}
	from := state.CurrentWorkspace
	if err := sess.MoveDaemonWindowToWorkspace(id, ws); err != nil {
		return err
	}
	after := sess.GetState()
	if _, err := findWindowStateIndex(after.Windows, id); err != nil {
		return nil // it closed meanwhile
	}
	if follow {
		return sess.FollowDaemonWindow(id)
	}
	if after.FocusedWindowID != id {
		return nil
	}
	next := ""
	for _, h := range after.FocusHistory[from] {
		if j, err := findWindowStateIndex(after.Windows, h); err == nil && h != id && after.Windows[j].Workspace == from {
			next = h
			break
		}
	}
	if next == "" {
		for _, w := range after.Windows {
			if w.Workspace == from && w.ID != id {
				next = w.ID
				break
			}
		}
	}
	if next == "" {
		return nil
	}
	return sess.FocusDaemonWindow(next)
}

// headlessBreakPane moves the focused window to the first workspace with no
// window and follows it there, as the TUI's BreakPane does. The scratch
// workspace is never the target.
func headlessBreakPane(sess *Session) (map[string]any, error) {
	state := sess.GetState()
	id, err := focusedWindowID(state)
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	for _, w := range state.Windows {
		used[w.Workspace] = true
	}
	ws := 0
	for n := 1; n <= state.workspaceBound(); n++ {
		if !used[n] && !IsScratchWorkspace(n) {
			ws = n
			break
		}
	}
	if ws == 0 {
		return nil, errfmt.Errorf("every workspace has a pane, so there is none to break the pane out to")
	}
	if err := headlessSendToWorkspace(sess, ws, true); err != nil {
		return nil, err
	}
	return map[string]any{"window_id": id, "workspace": ws}, nil
}

// headlessCloseWorkspace closes every window of the current workspace, as
// close-workspace does.
func headlessCloseWorkspace(sess *Session, by string) (map[string]any, error) {
	state := sess.GetState()
	ws := state.CurrentWorkspace
	closed := []string{}
	for _, id := range WindowsToCloseOnWorkspace(state.Windows, ws) {
		if _, err := sess.CloseDaemonWindow(id, by); err != nil {
			// A window that closed on its own meanwhile is not a failure.
			if _, gone := findWindowStateIndex(sess.GetState().Windows, id); gone != nil {
				continue
			}
			return nil, err
		}
		closed = append(closed, id)
	}
	return map[string]any{"workspace": ws, "closed": closed}, nil
}

// headlessMoveWorkspace swaps the current workspace with its neighbour,
// delta -1 the one before and 1 the one after, as the TUI's MoveWorkspaceBy:
// nothing at either end, past the last workspace with a window, or from the
// scratch workspace.
func headlessMoveWorkspace(sess *Session, delta int) error {
	state := sess.GetState()
	from := state.CurrentWorkspace
	to := from + delta
	if IsScratchWorkspace(from) || to < 1 || to > state.workspaceBound() {
		return nil
	}
	used := []int{}
	for _, w := range state.Windows {
		used = append(used, w.Workspace)
	}
	if !slices.Contains(used, to) {
		return nil
	}
	order := []int{to, from}
	if delta < 0 {
		order = []int{from, to}
	}
	_, err := sess.RenumberWorkspaces(order)
	return err
}

// headlessRestoreAll restores every minimized window of the current
// workspace.
func headlessRestoreAll(sess *Session) error {
	state := sess.GetState()
	for _, w := range state.Windows {
		if w.Workspace == state.CurrentWorkspace && w.Minimized {
			if err := sess.SetDaemonWindowMinimized(w.ID, false); err != nil {
				return err
			}
		}
	}
	return nil
}
