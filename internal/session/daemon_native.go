package session

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

type errNeedsClient struct{ verb string }

func (e errNeedsClient) Error() string {
	return fmt.Sprintf("command %q requires an attached client (the headless daemon has no renderer)", e.verb)
}

func focusedWindowID(state *SessionState) (string, error) {
	if state.FocusedWindowID != "" {
		return state.FocusedWindowID, nil
	}
	for i := range state.Windows {
		if state.Windows[i].Workspace == state.CurrentWorkspace {
			return state.Windows[i].ID, nil
		}
	}
	if len(state.Windows) > 0 {
		return state.Windows[0].ID, nil
	}
	return "", errfmt.Errorf("session has no windows")
}

func (d *Daemon) executeDaemonCommand(sess *Session, commandType string, args []string, onExit func(ptyID string)) (map[string]any, error) {
	return d.executeDaemonCommandEnv(sess, commandType, args, NewWindowOptions{}, onExit)
}

func (d *Daemon) executeDaemonCommandEnv(sess *Session, commandType string, args []string, base NewWindowOptions, onExit func(ptyID string)) (map[string]any, error) {
	switch commandType {
	case "NewWindow":
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		var command []string
		if len(args) > 1 {
			command = args[1:]
		}
		opts := base
		opts.Focus, opts.Command, opts.Name = true, command, name
		win, err := sess.AddDaemonWindowWith(opts, onExit)
		if err != nil {
			return nil, err
		}
		displayName := win.Title
		if name != "" {
			displayName = name
		}
		return map[string]any{"window_id": win.ID, "name": displayName}, nil

	case "CloseWindow":
		target := ""
		if len(args) > 0 {
			target = args[0]
		} else {
			id, err := focusedWindowID(sess.GetState())
			if err != nil {
				return nil, err
			}
			target = id
		}
		if _, err := sess.CloseDaemonWindow(target, base.closeAskedBy); err != nil {
			return nil, err
		}
		return nil, nil

	case "NextWindow":
		return nil, sess.CycleDaemonFocus(1)

	case "PrevWindow":
		return nil, sess.CycleDaemonFocus(-1)

	case "FocusWindow":
		if len(args) < 1 {
			return nil, errfmt.Errorf("FocusWindow requires a window name or ID")
		}
		return nil, sess.FocusDaemonWindow(args[0])

	case "RenameWindow":
		switch len(args) {
		case 1:
			id, err := focusedWindowID(sess.GetState())
			if err != nil {
				return nil, err
			}
			return nil, sess.RenameDaemonWindow(id, args[0])
		case 2:
			return nil, sess.RenameDaemonWindow(args[0], args[1])
		default:
			return nil, errfmt.Errorf("RenameWindow requires <new-name> or <target> <new-name>")
		}

	case "MinimizeWindow", "RestoreWindow":
		minimize := commandType == "MinimizeWindow"
		target := ""
		if len(args) > 0 {
			target = args[0]
		} else {
			id, err := focusedWindowID(sess.GetState())
			if err != nil {
				return nil, err
			}
			target = id
		}
		return nil, sess.SetDaemonWindowMinimized(target, minimize)

	case "SwitchWorkspace":
		ws, err := parseWorkspaceArg(args)
		if err != nil {
			return nil, err
		}
		return nil, sess.SwitchDaemonWorkspace(ws)

	case "MoveToWorkspace", "MoveAndFollowWorkspace":
		ws, err := parseWorkspaceArg(args)
		if err != nil {
			return nil, err
		}
		id, err := focusedWindowID(sess.GetState())
		if err != nil {
			return nil, err
		}
		if err := sess.MoveDaemonWindowToWorkspace(id, ws); err != nil {
			return nil, err
		}
		if commandType == "MoveAndFollowWorkspace" {
			return nil, sess.SwitchDaemonWorkspace(ws)
		}
		return nil, nil

	case "ListWindows":
		data := buildWindowListData(sess.GetState())
		dropRailPane(data)
		return data, nil
	case "GetSessionInfo":
		return buildSessionInfoData(sess, sess.GetState(), false, HostFocusUnknown), nil
	case "GetWindow":
		state := sess.GetState()
		target := ""
		if len(args) > 0 {
			target = args[0]
		} else {
			id, err := focusedWindowID(state)
			if err != nil {
				return nil, err
			}
			target = id
		}
		idx, err := findWindowStateIndex(state.Windows, target)
		if err != nil {
			return nil, err
		}
		return windowStateToData(state, idx), nil

	default:
		return nil, errNeedsClient{verb: commandType}
	}
}

func parseWorkspaceArg(args []string) (int, error) {
	if len(args) < 1 {
		return 0, errfmt.Errorf("workspace number required")
	}
	ws, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil {
		return 0, errfmt.Errorf("invalid workspace number %q", args[0])
	}
	return ws, nil
}

func keysToBytes(keys string, literal, raw bool) ([]byte, error) {
	if literal || raw {
		return []byte(keys), nil
	}
	parsed, err := parseSendKeys(keys, 1)
	if err != nil {
		return nil, err
	}
	return sendKeysBytes(parsed, paneKeyModes{})
}

func (d *Daemon) resolvePTYForTarget(sess *Session, target string) (*PTY, error) {
	state := sess.GetState()
	if target == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return nil, err
		}
		target = id
	}
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return nil, err
	}
	ptyID := state.Windows[idx].PTYID
	if ptyID == "" {
		return nil, errfmt.Errorf("window %q has no PTY", target)
	}
	pty := sess.GetPTY(ptyID)
	if pty == nil {
		return nil, errfmt.Errorf("PTY for window %q is gone", target)
	}
	return pty, nil
}

func (d *Daemon) writeKeysToWindow(sess *Session, target, keys string, literal, raw bool, parsed []sendKey) (WindowState, error) {
	state := sess.GetState()
	resolved := target
	if resolved == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return WindowState{}, err
		}
		resolved = id
	}
	idx, err := findWindowStateIndex(state.Windows, resolved)
	if err != nil {
		return WindowState{}, err
	}
	win := state.Windows[idx]
	pty, err := d.resolvePTYForTarget(sess, win.ID)
	if err != nil {
		return win, err
	}
	var data []byte
	switch {
	case literal || raw:
		data = []byte(keys)
	default:
		if parsed == nil {
			if parsed, err = parseSendKeys(keys, 1); err != nil {
				return win, err
			}
		}
		if data, err = sendKeysBytes(parsed, pty.keyModes()); err != nil {
			return win, err
		}
	}
	_, err = pty.Write(data)
	return win, err
}

func buildWindowListData(state *SessionState) map[string]any {
	windows := make([]map[string]any, 0, len(state.Windows))
	for i := range state.Windows {
		windows = append(windows, windowStateToData(state, i))
	}

	workspaceWindows := make([]int, state.workspaceBound())
	for i := range state.Windows {
		ws := state.Windows[i].Workspace
		if ws >= 1 && ws <= state.workspaceBound() {
			workspaceWindows[ws-1]++
		}
	}

	focusedIndex := -1
	for i := range state.Windows {
		if state.Windows[i].ID == state.FocusedWindowID {
			focusedIndex = i
			break
		}
	}

	return map[string]any{
		"windows":           windows,
		"total":             len(state.Windows),
		"focused_index":     focusedIndex,
		"focused_window_id": state.FocusedWindowID,
		"current_workspace": state.CurrentWorkspace,
		"workspace_windows": workspaceWindows,
	}
}

func windowStateToData(state *SessionState, idx int) map[string]any {
	w := state.Windows[idx]
	displayName := w.Title
	if w.CustomName != "" {
		displayName = w.CustomName
	}
	info := map[string]any{
		"window_id":    w.ID,
		"index":        idx,
		"title":        w.Title,
		"display_name": displayName,
		"workspace":    w.Workspace,
		"minimized":    w.Minimized,
		"minimized_at": w.MinimizedAt,
		"focused":      w.ID == state.FocusedWindowID,
		"x":            w.X,
		"y":            w.Y,
		"width":        w.Width,
		"height":       w.Height,
		"pty_id":       w.PTYID,
	}
	if w.CustomName != "" {
		info["custom_name"] = w.CustomName
	}
	if name := state.IconNames[w.ID]; name != "" {
		info["icon_name"] = name
	}
	if w.Zoomed {
		info["zoomed"] = true
	}
	if w.IsFloating {
		info["floating"] = true
	}
	if w.Scratch {
		info["scratch"] = true
		info["scratch_name"] = w.ScratchKey()
	}
	if w.Rail != "" {
		info["rail"] = w.Rail
	}
	if w.Kind != "" {
		info["kind"] = w.Kind
		info["uri"] = w.URI
		if id := TMLSurfaceID(w.URI); w.Kind == WindowKindTML && id != "" {
			info["surface"] = id
		}
	}
	if w.Cwd != "" {
		info["cwd"] = w.Cwd
	}
	if w.Host != "" {
		info["host"] = w.Host
	}
	if w.ForegroundCmd != "" {
		info["foreground_cmd"] = w.ForegroundCmd
	}
	if w.ScreenStopped {
		info["screen_stopped"] = true
	}
	if w.Recording {
		info["recording"] = true
	}
	if w.HostLink != "" {
		info["host_link"] = w.HostLink
		if w.HostLinkUntil > 0 {
			info["host_link_until"] = w.HostLinkUntil
		}
		if w.HostEnded != "" {
			info["exited"] = true
			info["host_ended"] = w.HostEnded
		}
	}
	info["agent_state"] = w.AgentState.Name()
	if w.AgentReason != "" {
		info["agent_reason"] = w.AgentReason
	}
	if w.AgentMessage != "" {
		info["agent_message"] = w.AgentMessage
	}
	if w.AgentStateAt != 0 {
		info["agent_state_at"] = w.AgentStateAt
	}
	if w.StartedBy != nil {
		info["started_by"] = w.StartedBy
	}
	if w.Grants != nil {
		info["grants"] = w.Grants
	}
	return info
}

func buildSessionInfoData(sess *Session, state *SessionState, hasClient bool, hostFocus string) map[string]any {
	tilingMode := "floating"
	if state.AutoTiling {
		tilingMode = "tiling"
	}
	layoutMode := state.LayoutMode
	if layoutMode == "" {
		layoutMode = "unknown"
	}
	workspaceNames := make(map[string]string, len(state.WorkspaceNames))
	for ws, name := range state.WorkspaceNames {
		workspaceNames[strconv.Itoa(ws)] = name
	}
	sessionWidth, sessionHeight := sess.Size()
	return map[string]any{
		"window_size":       sess.WindowSizePolicy(),
		"session_width":     sessionWidth,
		"session_height":    sessionHeight,
		"session_name":      state.Name,
		"session_id":        sess.ID,
		"display_name":      state.DisplayName,
		"accent":            state.Accent,
		"mode":              "unknown",
		"current_workspace": state.CurrentWorkspace,
		"num_workspaces":    state.workspaceBound(),
		"workspace_names":   workspaceNames,
		"workspace_order":   state.WorkspaceOrder,
		"layout_mode":       layoutMode,
		"window_count":      len(state.Windows),
		"tiling_mode":       tilingMode,
		"master_ratio":      state.MasterRatio,
		"width":             state.Width,
		"height":            state.Height,
		"tui_attached":      hasClient,
		"host_focus":        hostFocus,
	}
}
