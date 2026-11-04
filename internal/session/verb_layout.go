package session

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/google/uuid"
)

// This file holds the verbs that arrange a session: where a window is, which
// one has the focus, which workspace is showing, and how the panes are laid
// out. Every one of them was reachable only by sending the keybinding that
// triggers it, which meant a caller had to know the user's keymap, had to have
// a client attached for the keys to land anywhere, and got back nothing but
// "ok" whether or not the arrangement changed.
//
// The split between what runs here and what is routed to an attached client is
// not arbitrary. The daemon owns the window set, so which workspace a window is
// on and which window has the focus are its facts and it answers them itself,
// attached or not. Geometry is the client's: only a renderer knows the viewport,
// so anything that needs to measure it (a direction, a tiling scheme, a split
// ratio) is routed, and says needs_client when nobody is there to measure.

// focusRelatives are the positional focus moves that need no viewport, so the
// daemon can serve them on a detached session.
var focusRelatives = []string{"next", "prev"}

// focusDirections are the spatial focus moves. They need a renderer, because
// "the pane to the left" is a question about geometry the daemon does not have.
var focusDirections = []string{"left", "right", "up", "down"}

// splitDirections are the two ways a pane can be divided. They are the axis of
// the cut, which is the vocabulary the tiling code and the keybindings both
// already use; left/right/up/down name a neighbour, which is focus-window's
// question, not this one.
var splitDirections = []string{"horizontal", "vertical"}

// splitSides are the sides of the split pane the new pane can take. A side
// is a direction and a placement in one: left and right cut the vertical
// axis, up and down the horizontal one.
var splitSides = []string{"left", "right", "up", "down"}

// verbFocusWindow moves the focus, by target, by position, or by direction.
//
// One verb rather than three because the caller's question is always "focus
// this" and only the way it names the pane changes.
func (d *Daemon) verbFocusWindow(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string `json:"session"`
		Window    string `json:"window"`
		Relative  string `json:"relative"`
		Direction string `json:"direction"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	given := 0
	for _, v := range []string{p.Window, p.Relative, p.Direction} {
		if v != "" {
			given++
		}
	}
	if given == 0 {
		return nil, invalidParam("window", "name the pane to focus with one of window, relative or direction")
	}
	if given > 1 {
		return nil, invalidParam("window", "window, relative and direction are three ways to name one pane. Pass exactly one")
	}

	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	switch {
	case p.Window != "":
		if err := sess.FocusDaemonWindow(p.Window); err != nil {
			return nil, mapResolveErr(err, sess)
		}
		d.revealFocus(sess)

	case p.Relative != "":
		delta := 0
		switch p.Relative {
		case "next":
			delta = 1
		case "prev":
			delta = -1
		default:
			return nil, invalidParam("relative", "unknown relative move "+echoName(p.Relative), focusRelatives...)
		}
		if err := sess.CycleDaemonFocus(delta); err != nil {
			return nil, mapResolveErr(err, sess)
		}
		d.revealFocus(sess)

	default:
		if !slices.Contains(focusDirections, p.Direction) {
			return nil, invalidParam("direction", "unknown direction "+echoName(p.Direction), focusDirections...)
		}
		// A direction is a question about the viewport, so it only has an answer
		// where there is one.
		if verr := d.routeTape(sess, "tape_command", "FocusDirection", []string{p.Direction}); verr != nil {
			return nil, verr
		}
	}

	return focusResult(sess), nil
}

// revealFocus asks the attached client to focus the pane the session's focus
// is on, so the client shows it as a pane the user asked for: in the
// scrolling layout that is the whole column, as after a focus key. The state
// push alone reaches the client as a focus moved for some other reason, and
// a column partly on screen then stays cut off at the edge.
//
// It is best effort. The focus is already the session's, and a session with
// no client attached has no screen to show it on.
func (d *Daemon) revealFocus(sess *Session) {
	st := sess.GetState()
	if st == nil || st.FocusedWindowID == "" || d.findTUIClient(sess.ID) == nil {
		return
	}
	_ = d.routeTape(sess, "tape_command", "FocusWindow", []string{st.FocusedWindowID})
}

// focusResult reports where the focus ended up. Naming the pane rather than
// acknowledging the call is what lets a caller confirm a relative or directional
// move did what it meant, since neither says in advance which pane it lands on.
func focusResult(sess *Session) map[string]any {
	state := sess.GetState()
	out := map[string]any{
		"type":              "focus",
		"focused_window_id": state.FocusedWindowID,
		"current_workspace": state.CurrentWorkspace,
	}
	if idx, err := findWindowStateIndex(state.Windows, state.FocusedWindowID); err == nil {
		out["window"] = windowStateToData(state, idx)
	}
	return out
}

// verbMoveWindow moves a window to another workspace, optionally following it.
// With to_session it moves the window to another session.
//
// The tape command behind this only ever moved the focused window, so moving a
// named one meant focusing it first, which is a visible change to the session
// the caller did not ask for and cannot undo without knowing what was focused
// before.
func (d *Daemon) verbMoveWindow(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string `json:"session"`
		Window    string `json:"window"`
		Workspace int    `json:"workspace"`
		Follow    bool   `json:"follow"`
		ToSession string `json:"to_session"`
		Beside    string `json:"beside"`
		Side      string `json:"side"`
		Ratio     int    `json:"ratio"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Beside != "" {
		if p.Workspace != 0 || p.ToSession != "" || p.Follow {
			return nil, invalidParam("beside", "beside moves the window next to a pane of its session, on that pane's workspace. Leave out workspace, to_session and follow")
		}
		sess, verr := d.resolveVerbSession(cs, p.Session)
		if verr != nil {
			return nil, verr
		}
		return d.movePaneBeside(cs, sess, p.Window, p.Beside, p.Side, p.Ratio)
	}
	if p.Side != "" || p.Ratio != 0 {
		return nil, invalidParam("side", "side and ratio go with beside")
	}
	if p.Workspace == 0 && p.ToSession == "" {
		return nil, invalidParam("workspace", "workspace is required and is the workspace number to move the window to, e.g. 2")
	}
	if p.Workspace < 0 {
		return nil, invalidParam("workspace", "workspace is a workspace number, e.g. 2")
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if p.ToSession != "" {
		return d.moveWindowAcross(cs, sess, p.Window, p.ToSession, p.Workspace, p.Follow)
	}

	target := p.Window
	if target == "" {
		id, err := focusedWindowID(sess.GetState())
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	// Resolve to an id first so the result can name the window that moved even
	// when the caller addressed it by index or by a prefix.
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	windowID := state.Windows[idx].ID
	from := state.Windows[idx].Workspace

	if err := sess.MoveDaemonWindowToWorkspace(windowID, p.Workspace); err != nil {
		return nil, moveErr(err, sess, p.Workspace)
	}
	if p.Follow {
		if err := sess.SwitchDaemonWorkspace(p.Workspace); err != nil {
			return nil, moveErr(err, sess, p.Workspace)
		}
	}

	return map[string]any{
		"type":              "window_moved",
		"window_id":         windowID,
		"session":           sess.Name(),
		"from_workspace":    from,
		"workspace":         p.Workspace,
		"current_workspace": sess.GetState().CurrentWorkspace,
	}, nil
}

// moveWindowAcross is move-window with to_session: the window leaves sess and
// joins the session toName names, with its process still running.
func (d *Daemon) moveWindowAcross(cs *connState, sess *Session, target, toName string, workspace int, follow bool) (any, *verbError) {
	dst := d.findTargetSession(cs, toName)
	if dst == nil {
		dst = d.manager.GetSessionByID(toName)
	}
	if dst == nil {
		available := d.sessionNames()
		return nil, hintedVerbError(ErrVerbSessionNotFound, "session "+echoName(toName)+" not found", &VerbHint{
			Param:      "to_session",
			Command:    "tuios ls",
			DidYouMean: closestMatch(toName, available),
			Available:  available,
			Detail:     "to_session names the session to move the window to. Create it first with 'tuios new " + toName + " --detach'.",
		})
	}
	if target == "" {
		id, err := focusedWindowID(sess.GetState())
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	win := state.Windows[idx]
	if dst == sess {
		return nil, hintedVerbError(ErrVerbSameSession, "the window is already in session "+sess.Name(), &VerbHint{
			Param:  "to_session",
			Detail: "Name another session in to_session. To move the window in its own session, leave out to_session and give workspace.",
		})
	}
	if why := movableRefusal(win); why != "" {
		return nil, hintedVerbError(ErrVerbPaneNotMovable, "the window cannot move to another session: "+why, &VerbHint{
			Param:  "window",
			Detail: "Only a local pane that is not a popup or a scratch pane can move to another session.",
		})
	}
	if workspace != 0 {
		if st := dst.GetState(); !st.workspaceAccepts(workspace) || IsScratchWorkspace(workspace) {
			return nil, hintedVerbError(ErrVerbInvalidParams, fmt.Sprintf("workspace %d does not exist in session %s", workspace, dst.Name()), &VerbHint{
				Param:  "workspace",
				Verb:   "list-workspaces",
				Detail: fmt.Sprintf("session %s has workspaces 1 to %d. Leave out workspace to use the workspace it shows.", dst.Name(), st.workspaceBound()),
			})
		}
	}

	moved, err := d.moveWindowToSession(sess, dst, win.ID, workspace, follow)
	if err != nil {
		if why, ok := isPaneNotMovable(err); ok {
			return nil, newVerbError(ErrVerbPaneNotMovable, "the window cannot move to another session: "+why)
		}
		return nil, moveErr(err, dst, workspace)
	}
	return map[string]any{
		"type":              "window_moved",
		"window_id":         moved.ID,
		"session":           dst.Name(),
		"from_session":      sess.Name(),
		"from_workspace":    win.Workspace,
		"workspace":         moved.Workspace,
		"current_workspace": dst.GetState().CurrentWorkspace,
	}, nil
}

// moveErr classifies a workspace mutation failure. An out-of-range workspace is
// a bad parameter with a knowable bound, not an unresolvable target, and a
// caller has to be able to tell those apart to know whether retrying with a
// different number could work.
func moveErr(err error, sess *Session, ws int) *verbError {
	if strings.Contains(err.Error(), "out of range") {
		return hintedVerbError(ErrVerbInvalidParams, err.Error(), &VerbHint{
			Param:  "workspace",
			Verb:   "list-workspaces",
			Detail: fmt.Sprintf("this session has workspaces 1 to %d. %d is outside that range.", sess.GetState().workspaceBound(), ws),
		})
	}
	return mapResolveErr(err, sess)
}

// verbSelectWorkspace shows a workspace.
//
// Named select- rather than set- because the two set-workspace verbs that
// already exist change a workspace's label and its order, and this changes
// neither: it changes which one you are looking at.
func (d *Daemon) verbSelectWorkspace(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string `json:"session"`
		Workspace int    `json:"workspace"`
		ReturnTo  int    `json:"return_to"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Workspace == 0 {
		return nil, invalidParam("workspace", "workspace is required and is the workspace number to show, e.g. 2")
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if p.ReturnTo != 0 && !sess.GetState().workspaceAccepts(p.ReturnTo) {
		return nil, invalidParam("return_to", fmt.Sprintf("workspace %d does not exist", p.ReturnTo))
	}
	if err := sess.SwitchDaemonWorkspace(p.Workspace); err != nil {
		return nil, moveErr(err, sess, p.Workspace)
	}
	if p.ReturnTo != 0 {
		sess.SetReturnTo(p.Workspace, p.ReturnTo)
	}

	state := sess.GetState()
	return map[string]any{
		"type":              "workspace_selected",
		"current_workspace": state.CurrentWorkspace,
		"focused_window_id": state.FocusedWindowID,
		"window_count":      countOnWorkspace(state, state.CurrentWorkspace),
	}, nil
}

// verbSetWindow changes a window's own properties: what it is called and whether
// it is minimized.
//
// One verb with optional fields rather than rename-window plus minimize-window
// plus restore-window, which would be three verbs that differ by a word and
// three round trips to apply together.
func (d *Daemon) verbSetWindow(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string  `json:"session"`
		Window    string  `json:"window"`
		Name      *string `json:"name"`
		Minimized *bool   `json:"minimized"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Name == nil && p.Minimized == nil {
		return nil, invalidParam("name", "nothing to set: pass name, minimized, or both")
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	target := p.Window
	if target == "" {
		id, err := focusedWindowID(sess.GetState())
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	windowID := state.Windows[idx].ID

	if p.Name != nil {
		if err := sess.RenameDaemonWindow(windowID, strings.TrimSpace(*p.Name)); err != nil {
			return nil, mapResolveErr(err, sess)
		}
	}
	if p.Minimized != nil {
		if err := sess.SetDaemonWindowMinimized(windowID, *p.Minimized); err != nil {
			return nil, mapResolveErr(err, sess)
		}
	}

	// Report the window as it now stands rather than echoing the request, so a
	// caller that cleared a name sees what it fell back to.
	state = sess.GetState()
	idx, err = findWindowStateIndex(state.Windows, windowID)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	out := map[string]any{"type": "window_set"}
	maps.Copy(out, windowStateToData(state, idx))
	return out, nil
}

// verbListWorkspaces reports every workspace with what is on it.
//
// The facts were spread across two verbs and neither answered the question: a
// caller wanting to know where to put a pane had to read the workspace count and
// the names from session-info, the per-workspace tallies from list-windows, and
// join them itself.
func (d *Daemon) verbListWorkspaces(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	state := sess.GetState()
	bound := state.workspaceBound()
	spaces := make([]map[string]any, 0, bound)
	for ws := 1; ws <= bound; ws++ {
		entry := map[string]any{
			"workspace":    ws,
			"name":         state.WorkspaceNames[ws],
			"window_count": countOnWorkspace(state, ws),
			"current":      ws == state.CurrentWorkspace,
		}
		if state.WorkspaceFocus != nil {
			entry["focused_window_id"] = state.WorkspaceFocus[ws]
		}
		// The windows focused there, newest first, so a caller can name the
		// one before the current (tmux's last-pane).
		if history := state.FocusHistory[ws]; len(history) > 0 {
			entry["focus_history"] = history
		}
		spaces = append(spaces, entry)
	}

	return map[string]any{
		"type":              "workspace_list",
		"workspaces":        spaces,
		"current_workspace": state.CurrentWorkspace,
		"order":             state.WorkspaceOrder,
	}, nil
}

// countOnWorkspace counts the windows sitting on one workspace.
func countOnWorkspace(state *SessionState, ws int) int {
	n := 0
	for i := range state.Windows {
		if state.Windows[i].Workspace == ws {
			n++
		}
	}
	return n
}

// verbSetLayout turns tiling on or off and evens out the splits.
//
// Both are routed. Tiling is a geometry the renderer computes, and a daemon
// that recorded the flag without one would report a layout the session is not
// actually in, which is worse than refusing.
func (d *Daemon) verbSetLayout(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session  string `json:"session"`
		Tiling   *bool  `json:"tiling"`
		Equalize bool   `json:"equalize"`
		Rotate   bool   `json:"rotate"`
		// The master-stack shape of the current workspace.
		MasterPosition string `json:"master_position"`
		MasterCount    int    `json:"master_count"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Tiling == nil && !p.Equalize && !p.Rotate && p.MasterPosition == "" && p.MasterCount == 0 {
		return nil, invalidParam("tiling", "nothing to set: pass tiling, equalize, rotate, master_position or master_count")
	}
	if p.MasterPosition != "" && !slices.Contains(config.MasterPositions, p.MasterPosition) {
		return nil, invalidParam("master_position", "use "+strings.Join(config.MasterPositions, ", "))
	}
	if p.MasterCount != 0 && (p.MasterCount < config.MasterCountMin || p.MasterCount > config.MasterCountMax) {
		return nil, invalidParam("master_count", fmt.Sprintf("use a number from %d to %d", config.MasterCountMin, config.MasterCountMax))
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	// Tiling first: evening out or rotating splits means nothing until the panes
	// are tiled, so the other order would work on a layout that is about to be
	// replaced.
	if p.Tiling != nil {
		cmd := "DisableTiling"
		if *p.Tiling {
			cmd = "EnableTiling"
		}
		if verr := d.routeTape(sess, "tape_command", cmd, nil); verr != nil {
			return nil, verr
		}
	}
	if p.Rotate {
		if verr := d.routeTape(sess, "tape_command", "RotateSplit", nil); verr != nil {
			return nil, verr
		}
	}
	if p.Equalize {
		if verr := d.routeTape(sess, "tape_command", "EqualizeSplits", nil); verr != nil {
			return nil, verr
		}
	}
	if p.MasterPosition != "" {
		if verr := d.routeTape(sess, "tape_command", "SetMasterPosition", []string{p.MasterPosition}); verr != nil {
			return nil, verr
		}
	}
	if p.MasterCount != 0 {
		if verr := d.routeTape(sess, "tape_command", "SetMasterCount", []string{strconv.Itoa(p.MasterCount)}); verr != nil {
			return nil, verr
		}
	}

	return layoutResult(sess), nil
}

// layoutResult reports the arrangement as it now stands.
func layoutResult(sess *Session) map[string]any {
	state := sess.GetState()
	mode := "floating"
	if state.AutoTiling {
		mode = "tiling"
	}
	layoutMode := state.LayoutMode
	if layoutMode == "" {
		layoutMode = "unknown"
	}
	// The daemon holds a shape only for a workspace somebody changed. The
	// others use the attached client's configured one, which the daemon does
	// not know.
	masterPosition, masterCount := "default", 0
	if ml, ok := state.WorkspaceMasterLayout[state.CurrentWorkspace]; ok {
		masterPosition, masterCount = ml.Position, ml.Count
	}
	return map[string]any{
		"type":            "layout",
		"tiling_mode":     mode,
		"layout_mode":     layoutMode,
		"master_ratio":    state.MasterRatio,
		"master_position": masterPosition,
		"master_count":    masterCount,
	}
}

// verbSplitWindow divides a pane, putting a new one beside it.
//
// The new pane is a window like any new-window makes, and takes new-window's
// params: a command, a directory, the focus. The daemon puts it in the split
// tree beside the pane in the same change that adds it, so the split works
// with no client attached, and a client that is attached lays the panes out
// from the tree like any other change to it.
func (d *Daemon) verbSplitWindow(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		newWindowParams
		Window    string `json:"window"`
		Direction string `json:"direction"`
		Side      string `json:"side"`
		Ratio     int    `json:"ratio"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	switch {
	case p.Direction != "" && p.Side != "":
		return nil, invalidParam("side", "pass direction or side, not both")
	case p.Side != "":
		if !slices.Contains(splitSides, p.Side) {
			return nil, invalidParam("side", "unknown split side "+echoName(p.Side), splitSides...)
		}
	case p.Direction == "":
		return nil, invalidParam("direction", "direction or side is required. direction is the axis to cut on", splitDirections...)
	case !slices.Contains(splitDirections, p.Direction):
		return nil, invalidParam("direction", "unknown split direction "+echoName(p.Direction), splitDirections...)
	}
	if p.Ratio < 0 || p.Ratio > 99 {
		return nil, invalidParam("ratio", "ratio is the new pane's share in percent, from 1 to 99")
	}
	ask := &splitAsk{window: p.Window, side: p.Side, share: float64(p.Ratio) / 100}
	if ask.side == "" {
		ask.side = map[string]string{"vertical": "right", "horizontal": "down"}[p.Direction]
	}
	return d.openWindow(cs, &p.newWindowParams, ask)
}

// splitAsk is what split-window asks of new-window: the pane to cut, the side
// the new pane takes, and its share (0 for the tree's default).
type splitAsk struct {
	window, side string
	share        float64
}

// splitTarget resolves the pane a split cuts, and puts the new window on its
// workspace. cols and rows are the size the new pane's terminal starts at.
func (d *Daemon) splitTarget(cs *connState, sess *Session, p *newWindowParams, ask *splitAsk) (*SplitPlacement, int, int, *verbError) {
	w, verr := d.layoutWindow(cs, sess, ask.window)
	if verr != nil {
		return nil, 0, 0, verr
	}
	if !tiledPane(&w) {
		return nil, 0, 0, invalidParam("window", errSplitNotTiled.Error())
	}
	if p.Workspace != 0 && p.Workspace != w.Workspace {
		return nil, 0, 0, invalidParam("workspace", "a split opens on the workspace of the pane it cuts. Leave out workspace")
	}
	p.Workspace = w.Workspace
	side, _ := layout.ParseSide(ask.side)
	share := ask.share
	if share == 0 {
		share = 0.5
	}
	cols, rows := w.Width-2, w.Height-2
	if ask.side == "left" || ask.side == "right" {
		cols = int(float64(w.Width)*share) - 2
	} else {
		rows = int(float64(w.Height)*share) - 2
	}
	if cols < 1 || rows < 1 {
		cols, rows = 0, 0
	}
	return &SplitPlacement{Target: w.ID, Side: side, Share: ask.share, Place: !d.hasTUIClient(sess)}, cols, rows, nil
}

// splitDone finishes split-window's answer. With a client attached it waits,
// bounded, until the state holds the client's rectangles for the two panes:
// a caller that reads the layout straight after the answer (herdr's pane
// swap) otherwise reads the old one.
func (d *Daemon) splitDone(sess *Session, out map[string]any, placed *SplitPlacement, created string, ask *splitAsk) {
	out["type"] = "window_split"
	out["direction"] = splitAxis(ask.side)
	out["side"] = ask.side
	if !placed.Place {
		sess.WaitState(routedVerbTimeout, func(st *SessionState) bool { return splitSettled(st, placed.Target, created) })
	}
}

// layoutWindow is the pane a layout verb acts on: the one named, else the
// caller's own pane when the caller runs in a pane of sess, else the focused
// one. An agent that splits or zooms without naming a pane means its own, not
// whichever pane the person has in front of them.
func (d *Daemon) layoutWindow(cs *connState, sess *Session, target string) (WindowState, *verbError) {
	st := sess.GetState()
	if target == "" {
		if ref := d.callerPaneRef(cs); ref != nil && ref.Session == sess.Name() {
			target = ref.Window
		} else {
			id, err := focusedWindowID(st)
			if err != nil {
				return WindowState{}, mapResolveErr(err, sess)
			}
			target = id
		}
	}
	i, err := findWindowStateIndex(st.Windows, target)
	if err != nil {
		return WindowState{}, mapResolveErr(err, sess)
	}
	return st.Windows[i], nil
}

// daemonArranges reports whether the daemon edits the layout of ws itself.
// It does for a session no client shows, and for a tiled workspace in the
// split layout. Another layout (master-stack, scrolling) lays panes out by
// order, not by the tree, so an attached client does that edit.
func (d *Daemon) daemonArranges(sess *Session, ws int) bool {
	if !d.hasTUIClient(sess) {
		return true
	}
	st := sess.GetState()
	mode := st.LayoutMode
	if m, ok := st.WorkspaceLayoutModes[ws]; ok {
		mode = m
	}
	return st.AutoTiling && (mode == "" || mode == "bsp")
}

// errFloating refuses a layout edit in a session that floats its panes.
func errFloating(param string) *verbError {
	return invalidParam(param, "the session floats its panes, so there is no layout to change. split-window turns tiling on")
}

// errLayoutUnchanged is an edit that found nothing to change. The state is
// left alone, so no client is woken for it.
var errLayoutUnchanged = errors.New("layout unchanged")

// settleRects waits, bounded, for an attached client to lay out the panes
// again after a layout edit: until moved reports that the client's rectangles
// show it.
func (d *Daemon) settleRects(sess *Session, moved func(st *SessionState) bool) {
	if d.hasTUIClient(sess) {
		sess.WaitState(2*time.Second, moved)
	}
}

// rectOf is the rectangle of window id in st, and whether it is there.
func rectOf(st *SessionState, id string) (layout.Rect, bool) {
	w, ok := findWindowState(st, id)
	return layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height}, ok
}

// layoutParams are the params of zoom-window, swap-window and
// resize-window.
type layoutParams struct {
	Session   string `json:"session"`
	Window    string `json:"window"`
	Zoom      *bool  `json:"zoom"`
	With      string `json:"with"`
	Direction string `json:"direction"`
	Amount    int    `json:"amount"`
}

func (d *Daemon) verbZoomWindow(cs *connState, params json.RawMessage) (any, *verbError) {
	return d.layoutVerb(cs, params, 'z')
}

func (d *Daemon) verbSwapWindow(cs *connState, params json.RawMessage) (any, *verbError) {
	return d.layoutVerb(cs, params, 's')
}

func (d *Daemon) verbResizeWindow(cs *connState, params json.RawMessage) (any, *verbError) {
	return d.layoutVerb(cs, params, 'r')
}

// layoutVerb is zoom-window (z), swap-window (s) and resize-window (r). Each
// acts on the pane layoutWindow names.
func (d *Daemon) layoutVerb(cs *connState, params json.RawMessage, verb byte) (any, *verbError) {
	var p layoutParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	switch {
	case verb == 's' && p.With == "":
		return nil, invalidParam("with", "with is required and names the pane to swap with")
	case verb == 'r' && !slices.Contains(focusDirections, p.Direction):
		return nil, invalidParam("direction", "direction is required and is the way the border moves", focusDirections...)
	case verb == 'r' && (p.Amount < 0 || p.Amount > 50):
		return nil, invalidParam("amount", "amount is a share of the layout in percent, from 1 to 50")
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	w, verr := d.layoutWindow(cs, sess, p.Window)
	if verr != nil {
		return nil, verr
	}
	out := map[string]any{"window_id": w.ID}
	switch verb {
	case 'z':
		want := !w.Zoomed
		if p.Zoom != nil {
			want = *p.Zoom
		}
		changed, verr := d.zoomPane(sess, w, want)
		out["type"], out["zoomed"], out["changed"] = "window_zoomed", want, changed
		return out, verr
	case 's':
		other, verr := d.layoutWindow(cs, sess, p.With)
		if verr == nil {
			verr = d.swapPanes(sess, w, other)
		}
		out["type"], out["with"] = "windows_swapped", other.ID
		return out, verr
	}
	changed, verr := d.resizePane(sess, w, p.Direction, float64(cmp.Or(p.Amount, 5))/100)
	out["type"], out["changed"] = "window_resized", changed
	return out, verr
}

// zoomPane sets the zoom of w's workspace: on w, or off. A zoomed workspace
// shows one pane, so zooming a pane on the workspace the session shows
// focuses it, and the person never types into a pane they cannot see.
func (d *Daemon) zoomPane(sess *Session, w WindowState, on bool) (bool, *verbError) {
	if w.Minimized || w.Popup {
		return false, invalidParam("window", "a minimized pane or a popup cannot be zoomed")
	}
	changed := false
	err := sess.arrangeLayout(!d.hasTUIClient(sess), func(state *SessionState, _ *layoutEdit) (int, error) {
		for i := range state.Windows {
			if x := &state.Windows[i]; x.Workspace == w.Workspace {
				z := on && x.ID == w.ID
				changed = changed || x.Zoomed != z
				x.Zoomed = z
			}
		}
		if !changed {
			return 0, errLayoutUnchanged
		}
		if on && w.Workspace == state.CurrentWorkspace && state.FocusedWindowID != w.ID {
			sess.markFocusIntentLocked()
			state.FocusedWindowID = w.ID
			state.FocusHistory = RecordFocus(state.FocusHistory, w.Workspace, w.ID)
		}
		if on {
			if state.WorkspaceFocus == nil {
				state.WorkspaceFocus = map[int]string{}
			}
			state.WorkspaceFocus[w.Workspace] = w.ID
		}
		return w.Workspace, nil
	})
	if err != nil && !errors.Is(err, errLayoutUnchanged) {
		return false, mapResolveErr(err, sess)
	}
	return changed, nil
}

// swapPanes swaps a and b in the split tree. Two panes of one workspace in
// another layout are swapped by the attached client, which orders them.
func (d *Daemon) swapPanes(sess *Session, a, b WindowState) *verbError {
	if a.ID == b.ID {
		return invalidParam("with", "name another pane to swap with")
	}
	if !tiledPane(&a) || !tiledPane(&b) {
		return invalidParam("with", "both panes must be tiled. A floating, minimized or popup pane has no place in the layout to swap")
	}
	if a.Workspace != b.Workspace {
		return invalidParam("with", "the panes are on different workspaces. Move one with move-window --beside")
	}
	if !sess.GetState().AutoTiling {
		return errFloating("with")
	}
	if !d.daemonArranges(sess, a.Workspace) {
		return d.routeTape(sess, "swap_windows", "", []string{a.ID, b.ID})
	}
	err := sess.arrangeLayout(!d.hasTUIClient(sess), func(_ *SessionState, e *layoutEdit) (int, error) {
		e.tree(a.Workspace).SwapWindows(e.leaf(a.ID), e.leaf(b.ID))
		return a.Workspace, nil
	})
	if err != nil {
		return mapResolveErr(err, sess)
	}
	if a.X != b.X || a.Y != b.Y {
		d.settleRects(sess, func(st *SessionState) bool {
			r, _ := rectOf(st, a.ID)
			return r.X == b.X && r.Y == b.Y
		})
	}
	return nil
}

// resizePane moves a border of w by amount, a share of the layout. In
// another layout than the split one the attached client moves it.
func (d *Daemon) resizePane(sess *Session, w WindowState, dir string, amount float64) (bool, *verbError) {
	if !d.daemonArranges(sess, w.Workspace) {
		data, verr := d.routeData(sess, "resize_window", []string{w.ID, dir, strconv.FormatFloat(amount, 'f', -1, 64)})
		changed, _ := data["changed"].(bool)
		return changed, verr
	}
	if !tiledPane(&w) || !sess.GetState().AutoTiling {
		return false, nil
	}
	side, _ := layout.ParseSide(dir)
	err := sess.arrangeLayout(!d.hasTUIClient(sess), func(_ *SessionState, e *layoutEdit) (int, error) {
		if !e.tree(w.Workspace).NudgeBorder(e.leaf(w.ID), side, amount) {
			return 0, errLayoutUnchanged
		}
		return w.Workspace, nil
	})
	if errors.Is(err, errLayoutUnchanged) {
		return false, nil
	}
	if err != nil {
		return false, mapResolveErr(err, sess)
	}
	d.settleRects(sess, func(st *SessionState) bool {
		r, _ := rectOf(st, w.ID)
		return r != layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height}
	})
	return true, nil
}

// movePaneBeside is move-window with beside: the pane leaves its place and
// splits the pane beside names, toward side, on that pane's workspace.
func (d *Daemon) movePaneBeside(cs *connState, sess *Session, window, beside, sideName string, ratio int) (any, *verbError) {
	side, ok := layout.ParseSide(cmp.Or(sideName, "right"))
	if !ok {
		return nil, invalidParam("side", "unknown side "+echoName(sideName), splitSides...)
	}
	if ratio < 0 || ratio > 99 {
		return nil, invalidParam("ratio", "ratio is the moved pane's share in percent, from 1 to 99")
	}
	src, verr := d.layoutWindow(cs, sess, window)
	if verr != nil {
		return nil, verr
	}
	dst, verr := d.layoutWindow(cs, sess, beside)
	if verr != nil {
		return nil, verr
	}
	switch {
	case src.ID == dst.ID:
		return nil, invalidParam("beside", "name another pane to move beside")
	case !tiledPane(&src) || !tiledPane(&dst):
		return nil, invalidParam("beside", "both panes must be tiled. A floating, minimized or popup pane has no place in the layout")
	case src.Scratch && src.Workspace != dst.Workspace:
		return nil, invalidParam("window", errScratchPaneStays.Error())
	case !sess.GetState().AutoTiling:
		return nil, errFloating("beside")
	}
	// Across workspaces the window moves first. The tree of its new
	// workspace then holds it, and the edit below puts it beside dst.
	if src.Workspace != dst.Workspace {
		if err := sess.MoveDaemonWindowToWorkspace(src.ID, dst.Workspace); err != nil {
			return nil, mapResolveErr(err, sess)
		}
	}
	err := sess.arrangeLayout(!d.hasTUIClient(sess), func(_ *SessionState, e *layoutEdit) (int, error) {
		if !e.tree(dst.Workspace).MoveWindowBeside(e.leaf(src.ID), e.leaf(dst.ID), side, float64(ratio)/100) && src.Workspace == dst.Workspace {
			return 0, errLayoutUnchanged
		}
		return dst.Workspace, nil
	})
	changed := !errors.Is(err, errLayoutUnchanged)
	if err != nil && changed {
		return nil, mapResolveErr(err, sess)
	}
	if changed {
		d.settleRects(sess, func(st *SessionState) bool { return splitSettled(st, dst.ID, src.ID) })
	}
	return map[string]any{"type": "window_moved", "window_id": src.ID, "from_workspace": src.Workspace, "workspace": dst.Workspace, "beside": dst.ID, "changed": changed}, nil
}

// splitAxis is the axis a split cut: a side names its axis too.
func splitAxis(cut string) string {
	switch cut {
	case "left", "right":
		return "vertical"
	case "up", "down":
		return "horizontal"
	}
	return cut
}

// verbRunCommand runs one tape command, which is the vocabulary the keybindings
// are written in.
//
// It is the escape hatch, and it is here so the JSON protocol is never a subset
// of what the CLI can do: a binding that has no verb of its own is still
// reachable by the name the keymap gives it. Prefer a verb where one exists,
// because a verb reports what it changed and this reports only that the command
// ran.
func (d *Daemon) verbRunCommand(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string   `json:"session"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Command == "" {
		return nil, invalidParam("command", `command is required, e.g. "SwitchWorkspace"`)
	}
	// The keymap's name for an action reaches the same command as the tape's
	// name, and a name that is neither is an error here rather than a success
	// the client reports for running nothing. See resolveCommandName.
	canonical, args, ok := resolveCommandName(p.Command, p.Args)
	if !ok {
		return nil, invalidParam("command", unknownCommandMessage(p.Command))
	}
	p.Command, p.Args = canonical, args
	// The client runs the command in whatever pane is focused, as the
	// person. A pane without respond may not type or press keys that way:
	// see refuseTapeTyping, which the client protocol's run-command has too.
	if why := d.refuseTapeTyping(cs, &ExecuteCommandPayload{CommandType: p.Command}); why != "" {
		return nil, hintedVerbError(ErrVerbForbidden, "run-command "+p.Command+" is refused for this pane: "+why, &VerbHint{
			Verb:    "send-keys",
			Command: "tuios send-keys -w <window> <keys>",
			Detail:  "Nothing was run. Name the pane to type into with send-keys or send-text.",
		})
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if why := d.refuseMultifocusInto(cs, sess, p.Command, p.Args); why != "" {
		return nil, newVerbError(ErrVerbForbidden, "run-command "+p.Command+" is refused for this pane: "+why)
	}

	// The daemon-owned commands run here whether or not a client is attached, the
	// same rule the CLI path follows, so the two cannot disagree about what a
	// detached session can do.
	if daemonOwnedCommands[p.Command] || !d.hasTUIClient(sess) {
		onExit := func(ptyID string) { d.notifyPTYClosed(sess.ID, ptyID) }
		data, err := d.executeDaemonCommand(sess, p.Command, p.Args, onExit)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		out := map[string]any{"type": "command_result", "command": p.Command, "routed": false}
		maps.Copy(out, data)
		return out, nil
	}

	if verr := d.routeTape(sess, "tape_command", p.Command, p.Args); verr != nil {
		return nil, verr
	}
	return map[string]any{"type": "command_result", "command": p.Command, "routed": true}, nil
}

// routeTape sends one command to the session's attached client and waits for
// its result, reporting needs_client when nobody is attached. commandType is
// the routed command's type: "tape_command" with command the tape command's
// name, or another type the client runs from args alone.
func (d *Daemon) routeTape(sess *Session, commandType, command string, args []string) *verbError {
	tui := d.findTUIClient(sess.ID)
	if tui == nil {
		what := command
		if what == "" {
			what = "this"
		}
		return hintedVerbError(ErrVerbNeedsClient,
			what+" changes what is drawn on screen, so it needs an attached client",
			&VerbHint{
				Command: "tuios attach " + sess.Name(),
				Detail:  "the daemon has no viewport, so it cannot compute a geometry nobody is displaying. Attach a client and retry.",
			})
	}
	res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
		CommandType: commandType,
		TapeCommand: command,
		TapeArgs:    args,
	}, routedVerbTimeout)
	if err != nil {
		return newVerbError(ErrVerbCommandFailed, err.Error())
	}
	if !res.Success {
		msg := res.Message
		if msg == "" {
			msg = "the attached client refused the request"
		}
		return newVerbError(ErrVerbCommandFailed, msg)
	}
	return nil
}

// routeData is routeTape for a command whose answer carries data.
func (d *Daemon) routeData(sess *Session, command string, args []string) (map[string]any, *verbError) {
	tui := d.findTUIClient(sess.ID)
	if tui == nil {
		return nil, d.routeTape(sess, command, "", args)
	}
	res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{CommandType: command, TapeArgs: args}, routedVerbTimeout)
	if err != nil {
		return nil, newVerbError(ErrVerbCommandFailed, err.Error())
	}
	if !res.Success {
		return nil, newVerbError(ErrVerbCommandFailed, cmp.Or(res.Message, "the attached client refused the request"))
	}
	return res.Data, nil
}

// hasTUIClient reports whether a renderer is attached to the session.
func (d *Daemon) hasTUIClient(sess *Session) bool { return d.findTUIClient(sess.ID) != nil }

// splitSettled reports whether the state shows the new pane and the pane it
// was cut from side by side: both with a size, and overlapping by no more
// than the one shared border cell a layout with shared borders gives them.
func splitSettled(st *SessionState, from, created string) bool {
	var a, b *WindowState
	for i := range st.Windows {
		switch st.Windows[i].ID {
		case from:
			a = &st.Windows[i]
		case created:
			b = &st.Windows[i]
		}
	}
	if b == nil || b.Width <= 0 || b.Height <= 0 {
		return false
	}
	if a == nil {
		return true
	}
	overX := min(a.X+a.Width, b.X+b.Width) - max(a.X, b.X)
	overY := min(a.Y+a.Height, b.Y+b.Height) - max(a.Y, b.Y)
	return a.Width > 0 && a.Height > 0 && (overX <= 1 || overY <= 1)
}
