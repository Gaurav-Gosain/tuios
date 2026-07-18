package session

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// routedVerbTimeout bounds how long a verb routed to an attached TUI waits for
// that client's result before failing with command_failed.
const routedVerbTimeout = 10 * time.Second

// sortVerbEntries sorts the list-verbs output by verb name for deterministic
// output.
func sortVerbEntries(verbs []map[string]string) {
	sort.Slice(verbs, func(i, j int) bool { return verbs[i]["verb"] < verbs[j]["verb"] })
}

// decodeParams unmarshals a request's params into v, returning an invalid_params
// error on failure. Empty params decode to the zero value of v.
func decodeParams(params json.RawMessage, v any) *verbError {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return newVerbError(ErrVerbInvalidParams, "could not decode params: "+err.Error())
	}
	return nil
}

// resolveVerbSession resolves a session name (empty means most recently active)
// to a live session, or a session_not_found error.
func (d *Daemon) resolveVerbSession(name string) (*Session, *verbError) {
	sess := d.findTargetSession(name)
	if sess == nil {
		if name == "" {
			return nil, newVerbError(ErrVerbSessionNotFound, "no sessions exist")
		}
		return nil, newVerbError(ErrVerbSessionNotFound, "session "+name+" not found")
	}
	return sess, nil
}

// mapResolveErr classifies a window/PTY resolution error into a stable code.
func mapResolveErr(err error) *verbError {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no windows"):
		return newVerbError(ErrVerbNoWindows, msg)
	case strings.Contains(msg, "has no PTY"), strings.Contains(msg, "is gone"):
		return newVerbError(ErrVerbPTYNotFound, msg)
	default:
		return newVerbError(ErrVerbWindowNotFound, msg)
	}
}

// commonParams are the fields shared by session/window-targeted verbs.
type commonParams struct {
	Session string `json:"session"`
	Window  string `json:"window"`
}

func (d *Daemon) verbListSessions(_ *connState, _ json.RawMessage) (any, *verbError) {
	return map[string]any{
		"type":     "session_list",
		"sessions": d.manager.ListSessions(),
	}, nil
}

func (d *Daemon) verbSessionInfo(_ *connState, params json.RawMessage) (any, *verbError) {
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
	hasClient := d.findTUIClient(sess.ID) != nil
	data := buildSessionInfoData(sess, sess.GetState(), hasClient)
	data["type"] = "session_info"
	return data, nil
}

func (d *Daemon) verbListWindows(_ *connState, params json.RawMessage) (any, *verbError) {
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
	data := buildWindowListData(sess.GetState())
	data["type"] = "window_list"
	return data, nil
}

func (d *Daemon) verbNewWindow(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Name    string `json:"name"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	var args []string
	if p.Name != "" {
		args = []string{p.Name}
	}

	// With a TUI attached, route so the daemon and the live renderer stay in
	// sync (the TUI overwrites daemon state on its next sync). Headless, create
	// the window directly against daemon-owned state.
	if tui := d.findTUIClient(sess.ID); tui != nil {
		res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
			CommandType: "tape_command",
			TapeCommand: "NewWindow",
			TapeArgs:    args,
		}, routedVerbTimeout)
		if err != nil {
			return nil, newVerbError(ErrVerbCommandFailed, err.Error())
		}
		if !res.Success {
			return nil, newVerbError(ErrVerbCommandFailed, res.Message)
		}
		out := map[string]any{"type": "window_created"}
		for k, v := range res.Data {
			out[k] = v
		}
		return out, nil
	}

	onExit := func(ptyID string) { d.notifyPTYClosed(sess.ID, ptyID) }
	data, err := d.executeDaemonCommand(sess, "NewWindow", args, onExit)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, err.Error())
	}
	out := map[string]any{"type": "window_created"}
	for k, v := range data {
		out[k] = v
	}
	return out, nil
}

func (d *Daemon) verbCloseWindow(_ *connState, params json.RawMessage) (any, *verbError) {
	var p commonParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	var args []string
	if p.Window != "" {
		args = []string{p.Window}
	}

	if tui := d.findTUIClient(sess.ID); tui != nil {
		res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
			CommandType: "tape_command",
			TapeCommand: "CloseWindow",
			TapeArgs:    args,
		}, routedVerbTimeout)
		if err != nil {
			return nil, newVerbError(ErrVerbCommandFailed, err.Error())
		}
		if !res.Success {
			return nil, newVerbError(ErrVerbCommandFailed, res.Message)
		}
		return map[string]any{"type": "ok"}, nil
	}

	onExit := func(ptyID string) { d.notifyPTYClosed(sess.ID, ptyID) }
	if _, err := d.executeDaemonCommand(sess, "CloseWindow", args, onExit); err != nil {
		return nil, mapResolveErr(err)
	}
	return map[string]any{"type": "ok"}, nil
}

func (d *Daemon) verbSendKeys(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Keys    string `json:"keys"`
		Literal bool   `json:"literal"`
		Raw     bool   `json:"raw"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Keys == "" {
		return nil, newVerbError(ErrVerbInvalidParams, "keys is required")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	payload := &SendKeysPayload{
		Keys:         p.Keys,
		Literal:      p.Literal,
		Raw:          p.Raw,
		WindowTarget: p.Window,
	}

	// Route to the TUI when attached so window-manager keys (the prefix) are
	// honored; otherwise write the parsed bytes straight to the target PTY.
	if tui := d.findTUIClient(sess.ID); tui != nil {
		res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
			CommandType:  "send_keys",
			Keys:         p.Keys,
			Literal:      p.Literal,
			Raw:          p.Raw,
			WindowTarget: p.Window,
		}, routedVerbTimeout)
		if err != nil {
			return nil, newVerbError(ErrVerbCommandFailed, err.Error())
		}
		if !res.Success {
			return nil, newVerbError(ErrVerbCommandFailed, res.Message)
		}
		return map[string]any{"type": "ok"}, nil
	}

	if err := d.sendKeysDaemonSide(sess, payload); err != nil {
		return nil, mapResolveErr(err)
	}
	return map[string]any{"type": "ok"}, nil
}

func (d *Daemon) verbSendText(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Text    string `json:"text"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	// Literal text is always safe to write to a PTY whether or not a TUI is
	// attached (the TUI just renders the PTY's output), so send-text goes
	// straight to the daemon-owned PTY.
	pty, err := d.resolvePTYForTarget(sess, p.Window)
	if err != nil {
		return nil, mapResolveErr(err)
	}
	if _, err := pty.Write([]byte(p.Text)); err != nil {
		return nil, newVerbError(ErrVerbInternal, err.Error())
	}
	return map[string]any{"type": "ok"}, nil
}

func (d *Daemon) verbCapturePane(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session    string `json:"session"`
		Window     string `json:"window"`
		Source     string `json:"source"`     // visible | recent | recent-unwrapped
		Styled     bool   `json:"styled"`     // include ANSI styling
		Scrollback bool   `json:"scrollback"` // alias for source=recent
		ANSI       bool   `json:"ansi"`       // alias for styled
		Lines      int    `json:"lines"`      // if >0, keep only the last N lines
		Start      int    `json:"start"`      // 1-based inclusive region start
		End        int    `json:"end"`        // 1-based inclusive region end
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	pty, err := d.resolvePTYForTarget(sess, p.Window)
	if err != nil {
		return nil, mapResolveErr(err)
	}

	scrollback := p.Scrollback || p.Source == "recent" || p.Source == "recent-unwrapped"
	ansi := p.Styled || p.ANSI
	content := pty.CaptureContent(scrollback, ansi)
	content = sliceCaptureLines(content, p.Start, p.End, p.Lines)

	source := p.Source
	if source == "" {
		if scrollback {
			source = "recent"
		} else {
			source = "visible"
		}
	}
	return map[string]any{
		"type":    "pane_content",
		"content": content,
		"source":  source,
		"styled":  ansi,
	}, nil
}

func (d *Daemon) verbResize(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Width   int    `json:"width"`
		Height  int    `json:"height"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Width <= 0 || p.Height <= 0 {
		return nil, newVerbError(ErrVerbInvalidParams, "width and height must be positive")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, p.Window)
	if err != nil {
		return nil, mapResolveErr(err)
	}
	if err := pty.Resize(p.Width, p.Height); err != nil {
		return nil, newVerbError(ErrVerbInternal, err.Error())
	}
	return map[string]any{"type": "resized", "width": p.Width, "height": p.Height}, nil
}

func (d *Daemon) verbKillSession(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Session == "" {
		return nil, newVerbError(ErrVerbInvalidParams, "session is required")
	}
	if err := d.manager.DeleteSession(p.Session); err != nil {
		return nil, newVerbError(ErrVerbSessionNotFound, err.Error())
	}
	return map[string]any{"type": "ok"}, nil
}

func (d *Daemon) verbSetOption(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Key     string `json:"key"`
		Value   string `json:"value"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Key == "" {
		return nil, newVerbError(ErrVerbInvalidParams, "key is required")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	// Record the option in daemon-owned state so get-option can read it back.
	sess.SetOption(p.Key, p.Value)

	// When a TUI is attached, also route the change so options it understands
	// apply to the live renderer. A routing failure is not fatal: the option is
	// still recorded, so applied reflects only whether the live apply succeeded.
	applied := false
	if tui := d.findTUIClient(sess.ID); tui != nil {
		res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
			CommandType: "set_config",
			ConfigPath:  p.Key,
			ConfigValue: p.Value,
		}, routedVerbTimeout)
		applied = err == nil && res != nil && res.Success
	}

	return map[string]any{"type": "option_set", "key": p.Key, "value": p.Value, "applied": applied}, nil
}

func (d *Daemon) verbGetOption(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Key     string `json:"key"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Key == "" {
		return nil, newVerbError(ErrVerbInvalidParams, "key is required")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	value, ok := sess.GetOption(p.Key)
	if !ok {
		return nil, newVerbError(ErrVerbOptionNotFound, "option "+p.Key+" is not set")
	}
	return map[string]any{"type": "option", "key": p.Key, "value": value}, nil
}

// sliceCaptureLines applies the optional region/lines selection to captured
// content. start/end are 1-based inclusive line numbers; when both are zero the
// region is ignored. lines, when > 0 and no region is given, keeps only the last
// N lines. It preserves a trailing newline when the input had one.
func sliceCaptureLines(content string, start, end, lines int) string {
	if start <= 0 && end <= 0 && lines <= 0 {
		return content
	}

	trailing := strings.HasSuffix(content, "\n")
	body := content
	if trailing {
		body = strings.TrimSuffix(body, "\n")
	}
	split := strings.Split(body, "\n")

	var selected []string
	switch {
	case start > 0 || end > 0:
		lo := start
		if lo <= 0 {
			lo = 1
		}
		hi := end
		if hi <= 0 || hi > len(split) {
			hi = len(split)
		}
		if lo > len(split) || lo > hi {
			return ""
		}
		selected = split[lo-1 : hi]
	case lines > 0 && lines < len(split):
		selected = split[len(split)-lines:]
	default:
		selected = split
	}

	out := strings.Join(selected, "\n")
	if trailing && out != "" {
		out += "\n"
	}
	return out
}
