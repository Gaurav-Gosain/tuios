package session

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
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
	if dir, ok := directionOf(name, "focus_", "terminal_focus_"); ok {
		return nil, d.headlessFocusSide(sess, dir)
	}
	if dir, ok := directionOf(name, "swap_"); ok {
		return nil, d.headlessSwapSide(sess, dir)
	}
	if dir, cells, ok := resizeStepOf(name); ok {
		return nil, d.headlessResizeStep(sess, dir, cells)
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

// directionOf reads the left, right, up or down at the end of an action that
// starts with one of prefixes, such as focus_up or swap_left.
func directionOf(name string, prefixes ...string) (layout.Side, bool) {
	for _, pre := range prefixes {
		if rest, ok := strings.CutPrefix(name, pre); ok {
			return layout.ParseSide(rest)
		}
	}
	return 0, false
}

// resizeStepOf reads resize_pane_DIR_N: the way and the cells.
func resizeStepOf(name string) (layout.Side, int, bool) {
	rest, ok := strings.CutPrefix(name, "resize_pane_")
	if !ok {
		return 0, 0, false
	}
	dir, n, ok := strings.Cut(rest, "_")
	if !ok {
		return 0, 0, false
	}
	side, ok := layout.ParseSide(dir)
	cells, err := strconv.Atoi(n)
	if !ok || err != nil || cells <= 0 {
		return 0, 0, false
	}
	return side, cells, true
}

// neighbourOf is the window toward side of the focused one on the current
// workspace, by layout.Neighbour, the rule the TUI's FocusDirection uses. A
// tiled workspace is laid out from its tree in the daemon's box now, since a
// session no client shows may hold no rectangles or old ones; a floating one
// takes the windows' recorded rectangles, with the floating fallback.
func (d *Daemon) neighbourOf(sess *Session, side layout.Side) (WindowState, WindowState, error) {
	state := sess.GetState()
	id, err := focusedWindowID(state)
	if err != nil {
		return WindowState{}, WindowState{}, err
	}
	i, err := findWindowStateIndex(state.Windows, id)
	if err != nil {
		return WindowState{}, WindowState{}, err
	}
	from := state.Windows[i]
	if from.Workspace != state.CurrentWorkspace || from.Minimized {
		return WindowState{}, WindowState{}, errfmt.Errorf("the focused pane is not shown on the current workspace")
	}
	rects := map[string]layout.Rect{}
	tiled := state.AutoTiling && tiledPane(&from) && d.daemonArranges(sess, from.Workspace)
	if tiled {
		box := sess.layoutBox()
		e := newLayoutEdit(state, box)
		byLeaf := e.tree(from.Workspace).ApplyLayout(box, 0)
		for wid, leaf := range e.ids {
			if r, ok := byLeaf[leaf]; ok {
				rects[wid] = r
			}
		}
	}
	var cands []layout.Rect
	var wins []WindowState
	for _, w := range state.Windows {
		if w.ID == from.ID || w.Workspace != from.Workspace || w.Minimized {
			continue
		}
		r := layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height}
		if tiled {
			var ok bool
			if r, ok = rects[w.ID]; !ok {
				continue
			}
		}
		cands, wins = append(cands, r), append(wins, w)
	}
	origin := layout.Rect{X: from.X, Y: from.Y, W: from.Width, H: from.Height}
	if tiled {
		origin = rects[from.ID]
	}
	n := layout.Neighbour(origin, cands, side, !tiled)
	if n < 0 {
		return from, WindowState{}, errfmt.Errorf("no pane %s of the focused one", sideName(side))
	}
	return from, wins[n], nil
}

func sideName(s layout.Side) string {
	return [...]string{"left", "right", "up", "down"}[s]
}

// headlessFocusSide focuses the pane toward side.
func (d *Daemon) headlessFocusSide(sess *Session, side layout.Side) error {
	_, to, err := d.neighbourOf(sess, side)
	if err != nil {
		return err
	}
	return sess.FocusDaemonWindow(to.ID)
}

// headlessSwapSide swaps the focused pane with the one toward side. Where
// the daemon arranges the workspace it swaps the two in the tree itself and
// does not wait for a client to lay them out: a swap key from the TUI runs
// on the TUI's own connection, and waiting there for that client's next
// push (swapPanes' settleRects) held the push back for its whole timeout,
// so the swap reached the screen two seconds late. Elsewhere swapPanes
// hands the swap to the client.
func (d *Daemon) headlessSwapSide(sess *Session, side layout.Side) error {
	from, to, err := d.neighbourOf(sess, side)
	if err != nil {
		return err
	}
	if !d.daemonArranges(sess, from.Workspace) || !sess.GetState().AutoTiling || !tiledPane(&from) || !tiledPane(&to) || from.Workspace != to.Workspace {
		if verr := d.swapPanes(sess, from, to); verr != nil {
			return errors.New(verr.Message)
		}
		return nil
	}
	return sess.arrangeLayout(!d.hasTUIClient(sess), func(_ *SessionState, e *layoutEdit) (int, error) {
		e.tree(from.Workspace).SwapWindows(e.leaf(from.ID), e.leaf(to.ID))
		return from.Workspace, nil
	})
}

// headlessResizeStep makes the focused pane cells wider (right), narrower
// (left), taller (down) or shorter (up), as the TUI's resize_pane keys: the
// cells turn into a share of the daemon's box, and the tree moves the pane's
// far border, or its near one at the far edge (layout.GrowPane).
func (d *Daemon) headlessResizeStep(sess *Session, side layout.Side, cells int) error {
	state := sess.GetState()
	id, err := focusedWindowID(state)
	if err != nil {
		return err
	}
	i, err := findWindowStateIndex(state.Windows, id)
	if err != nil {
		return err
	}
	w := state.Windows[i]
	if !state.AutoTiling || !tiledPane(&w) {
		return errfmt.Errorf("the focused pane is not tiled, so there is no layout to resize it in")
	}
	box := sess.layoutBox()
	horizontal := side == layout.SideLeft || side == layout.SideRight
	extent := box.H
	if horizontal {
		extent = box.W
	}
	delta := float64(cells) / float64(max(extent, 1))
	if side == layout.SideLeft || side == layout.SideUp {
		delta = -delta
	}
	// With a client showing the session it lays the new tree out and sends
	// its rectangles (DaemonLayout), as for the resize-window verb.
	err = sess.arrangeLayout(!d.hasTUIClient(sess), func(_ *SessionState, e *layoutEdit) (int, error) {
		if !e.tree(w.Workspace).GrowPane(e.leaf(w.ID), horizontal, delta) {
			return 0, errLayoutUnchanged
		}
		return w.Workspace, nil
	})
	if errors.Is(err, errLayoutUnchanged) {
		return nil
	}
	return err
}
