package session

import (
	"fmt"
	"strconv"
	"strings"
)

// This file implements the headless execution path for the mutating control
// verbs. When a session has a TUI client attached the daemon keeps routing verbs
// to it (unchanged behavior); when none is attached these functions act directly
// on the daemon-owned canonical state so control works with no client.

// errNeedsClient is returned for verbs that genuinely require a live renderer
// (tiling geometry, animations, theming) and so cannot run headless.
type errNeedsClient struct{ verb string }

func (e errNeedsClient) Error() string {
	return fmt.Sprintf("command %q requires an attached client (the headless daemon has no renderer)", e.verb)
}

// focusedWindowID returns the focused window's ID, falling back to the first
// window on the current workspace, or an error when the session has no windows.
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
	return "", fmt.Errorf("session has no windows")
}

// executeDaemonCommand runs a structural tape command against daemon-owned
// session state with no TUI client. onExit is wired into any PTY it spawns so
// the daemon can notify clients when that shell exits. It returns result data
// (for read verbs and NewWindow) or an error. Rendering-dependent verbs return
// errNeedsClient.
func (d *Daemon) executeDaemonCommand(sess *Session, commandType string, args []string, onExit func(ptyID string)) (map[string]any, error) {
	switch commandType {
	case "NewWindow":
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		win, err := sess.AddDaemonWindow("", onExit)
		if err != nil {
			return nil, err
		}
		displayName := win.Title
		if name != "" {
			if err := sess.RenameDaemonWindow(win.ID, name); err != nil {
				return nil, err
			}
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
		if _, err := sess.CloseDaemonWindow(target); err != nil {
			return nil, err
		}
		return nil, nil

	case "NextWindow":
		return nil, sess.CycleDaemonFocus(1)

	case "PrevWindow":
		return nil, sess.CycleDaemonFocus(-1)

	case "FocusWindow":
		if len(args) < 1 {
			return nil, fmt.Errorf("FocusWindow requires a window name or ID")
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
			return nil, fmt.Errorf("RenameWindow requires <new-name> or <target> <new-name>")
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

	// Read-only verbs answerable from state without a client.
	case "ListWindows":
		return buildWindowListData(sess.GetState()), nil
	case "GetSessionInfo":
		return buildSessionInfoData(sess, sess.GetState(), false), nil
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

// parseWorkspaceArg extracts a 1-9 workspace number from a command's args.
func parseWorkspaceArg(args []string) (int, error) {
	if len(args) < 1 {
		return 0, fmt.Errorf("workspace number required")
	}
	ws, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil {
		return 0, fmt.Errorf("invalid workspace number %q", args[0])
	}
	return ws, nil
}

// keysToBytes translates a send-keys request into the raw bytes to write to a
// PTY. literal or raw modes pass the text through unchanged. Otherwise the input
// is split on spaces/commas and each token is mapped to its terminal byte
// sequence (named keys, ctrl+X, alt+X, or a literal character). WM-level tokens
// (the prefix) have no headless meaning and produce an error.
func keysToBytes(keys string, literal, raw bool) ([]byte, error) {
	if literal || raw {
		return []byte(keys), nil
	}

	normalized := strings.ReplaceAll(keys, ",", " ")
	tokens := strings.Fields(normalized)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("no valid keys in sequence: %s", keys)
	}

	var out []byte
	for _, tok := range tokens {
		b, err := keyTokenToBytes(tok)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

// namedKeyBytes maps a special key name (case-insensitive) to its byte sequence.
var namedKeyBytes = map[string]string{
	"enter":     "\r",
	"return":    "\r",
	"space":     " ",
	"tab":       "\t",
	"escape":    "\x1b",
	"esc":       "\x1b",
	"backspace": "\x7f",
	"delete":    "\x1b[3~",
	"del":       "\x1b[3~",
	"up":        "\x1b[A",
	"down":      "\x1b[B",
	"right":     "\x1b[C",
	"left":      "\x1b[D",
	"home":      "\x1b[H",
	"end":       "\x1b[F",
	"pageup":    "\x1b[5~",
	"pagedown":  "\x1b[6~",
	"insert":    "\x1b[2~",
	"f1":        "\x1bOP",
	"f2":        "\x1bOQ",
	"f3":        "\x1bOR",
	"f4":        "\x1bOS",
	"f5":        "\x1b[15~",
	"f6":        "\x1b[17~",
	"f7":        "\x1b[18~",
	"f8":        "\x1b[19~",
	"f9":        "\x1b[20~",
	"f10":       "\x1b[21~",
	"f11":       "\x1b[23~",
	"f12":       "\x1b[24~",
}

// keyTokenToBytes converts one send-keys token to its terminal byte sequence.
func keyTokenToBytes(tok string) ([]byte, error) {
	lower := strings.ToLower(tok)

	if tok == "PREFIX" || tok == "$PREFIX" {
		return nil, fmt.Errorf("the prefix key is a window-manager concept and has no meaning headless; attach a client")
	}

	if b, ok := namedKeyBytes[lower]; ok {
		return []byte(b), nil
	}

	// ctrl+X: control byte for letters, and common punctuation.
	if after, ok := strings.CutPrefix(lower, "ctrl+"); ok {
		if len(after) == 1 {
			c := after[0]
			switch {
			case c >= 'a' && c <= 'z':
				return []byte{c & 0x1f}, nil
			case c == ' ' || c == '@':
				return []byte{0x00}, nil
			case c == '[':
				return []byte{0x1b}, nil
			case c == '\\':
				return []byte{0x1c}, nil
			case c == ']':
				return []byte{0x1d}, nil
			}
		}
		return nil, fmt.Errorf("unsupported ctrl combination %q", tok)
	}

	// alt+X: ESC followed by the key/character.
	if after, ok := strings.CutPrefix(lower, "alt+"); ok {
		if b, ok := namedKeyBytes[after]; ok {
			return append([]byte{0x1b}, []byte(b)...), nil
		}
		if len(after) >= 1 {
			return append([]byte{0x1b}, []byte(after)...), nil
		}
		return nil, fmt.Errorf("unsupported alt combination %q", tok)
	}

	// Any other single-token string is sent as literal characters.
	return []byte(tok), nil
}

// resolvePTYForTarget resolves a window target (name/ID, or empty for the
// focused window) to its live PTY within the session.
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
		return nil, fmt.Errorf("window %q has no PTY", target)
	}
	pty := sess.GetPTY(ptyID)
	if pty == nil {
		return nil, fmt.Errorf("PTY for window %q is gone", target)
	}
	return pty, nil
}

// sendKeysDaemonSide writes a send-keys request straight to the target window's
// PTY, with no TUI client involved.
func (d *Daemon) sendKeysDaemonSide(sess *Session, payload *SendKeysPayload) error {
	pty, err := d.resolvePTYForTarget(sess, payload.WindowTarget)
	if err != nil {
		return err
	}
	data, err := keysToBytes(payload.Keys, payload.Literal, payload.Raw)
	if err != nil {
		return err
	}
	_, err = pty.Write(data)
	return err
}

// capturePaneDaemonSide renders the target pane from the daemon-side VT
// emulator, with no TUI client involved.
func (d *Daemon) capturePaneDaemonSide(sess *Session, payload *CapturePanePayload) (string, error) {
	pty, err := d.resolvePTYForTarget(sess, payload.WindowTarget)
	if err != nil {
		return "", err
	}
	return pty.CaptureContent(payload.Scrollback, payload.ANSI), nil
}

// buildWindowListData builds the window-list result map from session state. It
// is shared by handleQueryWindows and the headless ListWindows verb.
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

// windowStateToData renders one window (by index) to a result map.
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
	return info
}

// buildSessionInfoData builds the session-info result map from session state.
// It is shared by handleQuerySession and the headless GetSessionInfo verb.
func buildSessionInfoData(sess *Session, state *SessionState, hasClient bool) map[string]any {
	tilingMode := "floating"
	if state.AutoTiling {
		tilingMode = "tiling"
	}
	// layout_mode names which tiling layout is in use, which tiling_mode cannot
	// say: it reports only whether tiling is on at all, and has to keep doing so
	// because callers already dispatch on its two values.
	layoutMode := state.LayoutMode
	if layoutMode == "" {
		layoutMode = "unknown"
	}
	return map[string]any{
		"session_name":      state.Name,
		"session_id":        sess.ID,
		"mode":              "unknown",
		"current_workspace": state.CurrentWorkspace,
		"num_workspaces":    state.workspaceBound(),
		"layout_mode":       layoutMode,
		"window_count":      len(state.Windows),
		"tiling_mode":       tilingMode,
		"master_ratio":      state.MasterRatio,
		"width":             state.Width,
		"height":            state.Height,
		"tui_attached":      hasClient,
	}
}
