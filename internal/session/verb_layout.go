package session

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

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

// verbFocusWindow moves the focus, by target, by position, or by direction.
//
// One verb rather than three because the caller's question is always "focus
// this" and only the way it names the pane changes.
func (d *Daemon) verbFocusWindow(_ *connState, params json.RawMessage) (any, *verbError) {
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
		return nil, invalidParam("window", "window, relative and direction are three ways to name one pane; pass exactly one")
	}

	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	switch {
	case p.Window != "":
		if err := sess.FocusDaemonWindow(p.Window); err != nil {
			return nil, mapResolveErr(err, sess)
		}

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

	default:
		if !slices.Contains(focusDirections, p.Direction) {
			return nil, invalidParam("direction", "unknown direction "+echoName(p.Direction), focusDirections...)
		}
		// A direction is a question about the viewport, so it only has an answer
		// where there is one.
		if verr := d.routeTape(sess, "FocusDirection", []string{p.Direction}); verr != nil {
			return nil, verr
		}
	}

	return focusResult(sess), nil
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
//
// The tape command behind this only ever moved the focused window, so moving a
// named one meant focusing it first, which is a visible change to the session
// the caller did not ask for and cannot undo without knowing what was focused
// before.
func (d *Daemon) verbMoveWindow(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string `json:"session"`
		Window    string `json:"window"`
		Workspace int    `json:"workspace"`
		Follow    bool   `json:"follow"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Workspace == 0 {
		return nil, invalidParam("workspace", "workspace is required and is the workspace number to move the window to, e.g. 2")
	}
	sess, verr := d.resolveVerbSession(p.Session)
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
		"from_workspace":    from,
		"workspace":         p.Workspace,
		"current_workspace": sess.GetState().CurrentWorkspace,
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
			Detail: fmt.Sprintf("this session has workspaces 1 to %d; %d is outside that.", sess.GetState().workspaceBound(), ws),
		})
	}
	return mapResolveErr(err, sess)
}

// verbSelectWorkspace shows a workspace.
//
// Named select- rather than set- because the two set-workspace verbs that
// already exist change a workspace's label and its order, and this changes
// neither: it changes which one you are looking at.
func (d *Daemon) verbSelectWorkspace(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string `json:"session"`
		Workspace int    `json:"workspace"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Workspace == 0 {
		return nil, invalidParam("workspace", "workspace is required and is the workspace number to show, e.g. 2")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	if err := sess.SwitchDaemonWorkspace(p.Workspace); err != nil {
		return nil, moveErr(err, sess, p.Workspace)
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
func (d *Daemon) verbSetWindow(_ *connState, params json.RawMessage) (any, *verbError) {
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
	sess, verr := d.resolveVerbSession(p.Session)
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
	for k, v := range windowStateToData(state, idx) {
		out[k] = v
	}
	return out, nil
}

// verbListWorkspaces reports every workspace with what is on it.
//
// The facts were spread across two verbs and neither answered the question: a
// caller wanting to know where to put a pane had to read the workspace count and
// the names from session-info, the per-workspace tallies from list-windows, and
// join them itself.
func (d *Daemon) verbListWorkspaces(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
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
func (d *Daemon) verbSetLayout(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session  string `json:"session"`
		Tiling   *bool  `json:"tiling"`
		Equalize bool   `json:"equalize"`
		Rotate   bool   `json:"rotate"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Tiling == nil && !p.Equalize && !p.Rotate {
		return nil, invalidParam("tiling", "nothing to set: pass tiling, equalize or rotate")
	}
	sess, verr := d.resolveVerbSession(p.Session)
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
		if verr := d.routeTape(sess, cmd, nil); verr != nil {
			return nil, verr
		}
	}
	if p.Rotate {
		if verr := d.routeTape(sess, "RotateSplit", nil); verr != nil {
			return nil, verr
		}
	}
	if p.Equalize {
		if verr := d.routeTape(sess, "EqualizeSplits", nil); verr != nil {
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
	return map[string]any{
		"type":         "layout",
		"tiling_mode":  mode,
		"layout_mode":  layoutMode,
		"master_ratio": state.MasterRatio,
	}
}

// verbSplitWindow divides a pane, putting a new one beside it.
//
// This is the placement question in the form the tiling code can answer: the
// new pane takes half of a pane that already exists, so the caller names which
// pane and which way to cut rather than trying to describe a box. It is routed,
// because the division is a geometry, and it goes through the renderer's own
// splitting path so the new pane lands in the tree the same way one opened from
// the keyboard does.
func (d *Daemon) verbSplitWindow(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string `json:"session"`
		Window    string `json:"window"`
		Direction string `json:"direction"`
		Name      string `json:"name"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Direction == "" {
		return nil, invalidParam("direction", "direction is required and is the axis to cut on", splitDirections...)
	}
	if !slices.Contains(splitDirections, p.Direction) {
		return nil, invalidParam("direction", "unknown split direction "+echoName(p.Direction), splitDirections...)
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	// Splitting divides whichever pane has the focus, so a named target is
	// focused first. Doing it here rather than leaving it to the caller keeps
	// the two steps under one round trip, which matters because a pane focused
	// and not yet split is a visible flicker in someone's session.
	if p.Window != "" {
		if err := sess.FocusDaemonWindow(p.Window); err != nil {
			return nil, mapResolveErr(err, sess)
		}
	}

	before := windowIDSet(sess.GetState())
	if verr := d.routeTape(sess, "Split", []string{p.Direction}); verr != nil {
		return nil, verr
	}

	// The new pane's id is the whole point of the call: without it the caller
	// has to diff two list-windows and guess which one it just made.
	created := newWindowID(before, sess.GetState())
	out := map[string]any{
		"type":      "window_split",
		"direction": p.Direction,
	}
	if created == "" {
		// The split reported success but no window appeared. Saying so is better
		// than returning an empty id that reads as a valid target.
		out["window_id"] = ""
		out["note"] = "the split ran but no new window has reached daemon state yet; call list-windows to find it"
		return out, nil
	}
	out["window_id"] = created
	if p.Name != "" {
		if err := sess.RenameDaemonWindow(created, p.Name); err != nil {
			return nil, mapResolveErr(err, sess)
		}
		out["name"] = p.Name
	}
	return out, nil
}

// windowIDSet snapshots the window ids in a session.
func windowIDSet(state *SessionState) map[string]bool {
	ids := make(map[string]bool, len(state.Windows))
	for i := range state.Windows {
		ids[state.Windows[i].ID] = true
	}
	return ids
}

// newWindowID returns the one id in state that before did not hold.
func newWindowID(before map[string]bool, state *SessionState) string {
	for i := range state.Windows {
		if !before[state.Windows[i].ID] {
			return state.Windows[i].ID
		}
	}
	return ""
}

// verbRunCommand runs one tape command, which is the vocabulary the keybindings
// are written in.
//
// It is the escape hatch, and it is here so the JSON protocol is never a subset
// of what the CLI can do: a binding that has no verb of its own is still
// reachable by the name the keymap gives it. Prefer a verb where one exists,
// because a verb reports what it changed and this reports only that the command
// ran.
func (d *Daemon) verbRunCommand(_ *connState, params json.RawMessage) (any, *verbError) {
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
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
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
		for k, v := range data {
			out[k] = v
		}
		return out, nil
	}

	if verr := d.routeTape(sess, p.Command, p.Args); verr != nil {
		return nil, verr
	}
	return map[string]any{"type": "command_result", "command": p.Command, "routed": true}, nil
}

// routeTape sends one tape command to the session's attached client and waits
// for its result, reporting needs_client when nobody is attached.
func (d *Daemon) routeTape(sess *Session, command string, args []string) *verbError {
	tui := d.findTUIClient(sess.ID)
	if tui == nil {
		return hintedVerbError(ErrVerbNeedsClient,
			command+" changes what is drawn on screen, so it needs an attached client",
			&VerbHint{
				Command: "tuios attach " + sess.Name,
				Detail:  "the daemon has no viewport, so it cannot compute a geometry nobody is displaying. Attach a client and retry.",
			})
	}
	res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
		CommandType: "tape_command",
		TapeCommand: command,
		TapeArgs:    args,
	}, routedVerbTimeout)
	if err != nil {
		return newVerbError(ErrVerbCommandFailed, err.Error())
	}
	if !res.Success {
		return newVerbError(ErrVerbCommandFailed, res.Message)
	}
	return nil
}

// hasTUIClient reports whether a renderer is attached to the session.
func (d *Daemon) hasTUIClient(sess *Session) bool { return d.findTUIClient(sess.ID) != nil }
