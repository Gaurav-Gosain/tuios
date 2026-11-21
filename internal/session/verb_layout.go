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

var focusRelatives = []string{"next", "prev"}

var focusDirections = []string{"left", "right", "up", "down"}

var splitDirections = []string{"horizontal", "vertical"}

var splitSides = []string{"left", "right", "up", "down"}

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
		sess.RestoreFocusedDaemonWindow()
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
		if verr := d.routeTape(sess, "tape_command", "FocusDirection", []string{p.Direction}); verr != nil {
			return nil, verr
		}
	}

	return focusResult(sess), nil
}

func (d *Daemon) revealFocus(sess *Session) {
	st := sess.GetState()
	if st == nil || st.FocusedWindowID == "" || d.findTUIClient(sess.ID) == nil {
		return
	}
	_ = d.routeTape(sess, "tape_command", "FocusWindow", []string{st.FocusedWindowID})
}

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

	state = sess.GetState()
	idx, err = findWindowStateIndex(state.Windows, windowID)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	out := map[string]any{"type": "window_set"}
	maps.Copy(out, windowStateToData(state, idx))
	return out, nil
}

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

func countOnWorkspace(state *SessionState, ws int) int {
	n := 0
	for i := range state.Windows {
		if state.Windows[i].Workspace == ws {
			n++
		}
	}
	return n
}

func (d *Daemon) verbSetLayout(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session        string `json:"session"`
		Tiling         *bool  `json:"tiling"`
		Equalize       bool   `json:"equalize"`
		Rotate         bool   `json:"rotate"`
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

type splitAsk struct {
	window, side string
	share        float64
}

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

func (d *Daemon) splitDone(sess *Session, out map[string]any, placed *SplitPlacement, created string, ask *splitAsk) {
	out["type"] = "window_split"
	out["direction"] = splitAxis(ask.side)
	out["side"] = ask.side
	if !placed.Place {
		sess.waitChange(routedVerbTimeout, func() bool {
			if !d.hasTUIClient(sess) {
				return true
			}
			sess.stateMu.RLock()
			defer sess.stateMu.RUnlock()
			return splitShown(sess.state, placed.Target, created)
		})
	}
}

func splitShown(st *SessionState, from, created string) bool {
	b, ok := findWindowState(st, created)
	if !ok {
		return true
	}
	if workspaceZoomed(st, b.Workspace) {
		return !b.Unplaced && b.Width > 0 && b.Height > 0
	}
	return splitSettled(st, from, created)
}

func workspaceZoomed(st *SessionState, ws int) bool {
	for i := range st.Windows {
		if w := &st.Windows[i]; w.Workspace == ws && w.Zoomed {
			return true
		}
	}
	return false
}

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

func errFloating(param string) *verbError {
	return invalidParam(param, "the session floats its panes, so there is no layout to change. split-window turns tiling on")
}

var errLayoutUnchanged = errors.New("layout unchanged")

func (d *Daemon) settleRects(sess *Session, moved func(st *SessionState) bool) {
	if d.hasTUIClient(sess) {
		sess.WaitState(2*time.Second, moved)
	}
}

func rectOf(st *SessionState, id string) (layout.Rect, bool) {
	w, ok := findWindowState(st, id)
	return layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height}, ok
}

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
		sess.zoomOpAt = state.Version + 1
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

func splitAxis(cut string) string {
	switch cut {
	case "left", "right":
		return "vertical"
	case "up", "down":
		return "horizontal"
	}
	return cut
}

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
	canonical, args, ok := resolveCommandName(p.Command, p.Args)
	if !ok {
		return nil, invalidParam("command", unknownCommandMessage(p.Command))
	}
	p.Command, p.Args = canonical, args
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

func (d *Daemon) hasTUIClient(sess *Session) bool { return d.findTUIClient(sess.ID) != nil }

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
