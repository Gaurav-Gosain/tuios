package session

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/google/uuid"
)

// routedVerbTimeout bounds how long a verb routed to an attached TUI waits for
// that client's result before failing with command_failed.
const routedVerbTimeout = 10 * time.Second

// decodeParams unmarshals a request's params into v, returning an invalid_params
// error on failure. Empty params decode to the zero value of v.
func decodeParams(params json.RawMessage, v any) *verbError {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return hintedVerbError(ErrVerbInvalidParams, "could not decode params: "+err.Error(), &VerbHint{
			Verb:   "list-verbs",
			Detail: "Call list-verbs to get this verb's parameter schema, including each parameter's type.",
		})
	}
	return nil
}

// resolveVerbSession resolves a session name (empty means most recently active)
// to a live session, or a session_not_found error whose hint lists the sessions
// that do exist and suggests the closest name.
func (d *Daemon) resolveVerbSession(name string) (*Session, *verbError) {
	sess := d.findTargetSession(name)
	if sess != nil {
		return sess, nil
	}

	available := d.sessionNames()
	if name == "" {
		return nil, hintedVerbError(ErrVerbSessionNotFound, "no sessions exist", &VerbHint{
			Param:   "session",
			Command: "tuios new --detach",
			Detail:  "The daemon is running but holds no sessions. Create one, or restore a saved one with 'tuios resurrect'.",
		})
	}
	return nil, hintedVerbError(ErrVerbSessionNotFound, "session "+name+" not found", &VerbHint{
		Param:      "session",
		Command:    "tuios ls",
		DidYouMean: closestMatch(name, available),
		Available:  available,
		Detail:     "the name matches no live session. A session that was killed is gone. One that was never started may still have saved state ('tuios resurrect').",
	})
}

// mapResolveErr classifies a window/PTY resolution error into a stable code and
// attaches the remedy for that class. sess may be nil when the caller has no
// session context, in which case the available-window list is omitted.
func mapResolveErr(err error, sess *Session) *verbError {
	msg := err.Error()

	// A command that genuinely needs a renderer is its own class: the caller has
	// to attach a client, not fix a parameter.
	var needsClient errNeedsClient
	if errors.As(err, &needsClient) {
		hint := &VerbHint{
			Command: "tuios attach",
			Detail:  "This command changes what is drawn on screen, so it only runs with a client attached. Attach to the session, then retry.",
		}
		if sess != nil {
			hint.Command = "tuios attach " + sess.Name
		}
		return hintedVerbError(ErrVerbNeedsClient, msg, hint)
	}

	switch {
	case strings.Contains(msg, "no windows"):
		return hintedVerbError(ErrVerbNoWindows, msg, &VerbHint{
			Verb:    "new-window",
			Command: "tuios run-command NewWindow",
			Detail:  "The session exists but holds no windows. Create one before addressing a window.",
		})
	case strings.Contains(msg, "has no PTY"), strings.Contains(msg, "is gone"):
		return hintedVerbError(ErrVerbPTYNotFound, msg, &VerbHint{
			Verb:   "list-windows",
			Detail: "The window exists but its shell has already exited, so there is nothing to write to. Close it or create a new window.",
		})
	default:
		hint := &VerbHint{
			Param:   "window",
			Verb:    "list-windows",
			Command: "tuios list-windows --json",
			Detail:  "the window target matched no window. A window is addressable by its id, a unique id prefix, the index list-windows prints, or its exact name.",
		}
		if sess != nil {
			hint.Available = windowTargets(sess.GetState())
			hint.DidYouMean = closestMatch(targetFromError(msg), hint.Available)
		}
		return hintedVerbError(ErrVerbWindowNotFound, msg, hint)
	}
}

// targetFromError extracts the window target from a resolution error message so
// a did-you-mean suggestion can be computed. Every resolution error quotes the
// target it failed on (`no window found matching "build"`), so the first quoted
// run is the target. A message without one yields no target and therefore no
// suggestion, which is the safe outcome.
func targetFromError(msg string) string {
	_, rest, ok := strings.Cut(msg, `"`)
	if !ok {
		return ""
	}
	target, _, ok := strings.Cut(rest, `"`)
	if !ok {
		return ""
	}
	return target
}

// commonParams are the fields shared by session/window-targeted verbs.
type commonParams struct {
	Session string `json:"session"`
	Window  string `json:"window"`
}

func (d *Daemon) verbListSessions(_ *connState, _ json.RawMessage) (any, *verbError) {
	return map[string]any{
		"type":     "session_list",
		"sessions": d.listSessions(),
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
		Session   string   `json:"session"`
		Name      string   `json:"name"`
		Workspace int      `json:"workspace"`
		Cwd       string   `json:"cwd"`
		Focus     *bool    `json:"focus"`
		Command   []string `json:"command"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	if p.Workspace < 0 {
		return nil, invalidParam("workspace", "workspace is a workspace number, e.g. 2. Omit it for the current one")
	}
	// A directory that cannot be entered is refused rather than quietly ignored.
	if verr := checkWindowCwd(p.Cwd); verr != nil {
		return nil, verr
	}
	// An empty argv head would only fail later inside exec with a message that
	// names nothing; refuse it as the parameter mistake it is.
	if len(p.Command) > 0 && p.Command[0] == "" {
		return nil, invalidParam("command", "command[0] is the program to exec and cannot be empty")
	}
	// Focusing is the historical behaviour and stays the default, because a
	// caller opening a pane usually means to use it. Passing false is how an
	// agent opens one to work in later without pulling the user out of the pane
	// they are in.
	focus := p.Focus == nil || *p.Focus

	// Creating runs against daemon state whether or not a client is attached: the
	// PTY and the window set are the daemon's. An attached renderer learns of the
	// window from the state push and places it, so there is no round trip to the
	// client that can time out and no second creation path to keep in step.
	onExit := func(ptyID string) { d.notifyPTYClosed(sess.ID, ptyID) }
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Title:     p.Name,
		Cwd:       p.Cwd,
		Workspace: p.Workspace,
		Focus:     focus,
		Command:   p.Command,
		Name:      p.Name,
	}, onExit)
	if err != nil {
		return nil, newWindowErr(err, sess, p.Workspace)
	}

	displayName := win.Title
	if p.Name != "" {
		displayName = p.Name
	}

	// The result says where the window went, not just that one was made. A
	// caller that asked for a workspace has to be able to confirm it without a
	// second call, and unplaced is the honest answer to "what size is it": the
	// box is a placeholder until a client with a viewport places it.
	return map[string]any{
		"type":      "window_created",
		"window_id": win.ID,
		"name":      displayName,
		"workspace": win.Workspace,
		"pty_id":    win.PTYID,
		"focused":   focus,
		"unplaced":  win.Unplaced,
	}, nil
}

// verbPopup opens a popup: a floating pane that runs one command and closes
// when the command exits.
//
// Creation goes through the same daemon-side path new-window uses, because a
// popup is a window and there is no second way to make one. What the daemon
// adds is the mark, the float and the size the caller asked for; where the box
// lands is the attached client's answer, exactly as it is for any window the
// daemon creates (see WindowState.Unplaced).
//
// It needs an attached client, which new-window does not. The difference is what
// a popup is for: it is a thing on a screen for the length of one command, and
// opening one on a session nobody is looking at runs a program in a box no one
// can see or type into. Refusing says so while the caller can still do something
// about it.
func (d *Daemon) verbPopup(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string   `json:"session"`
		Name      string   `json:"name"`
		Cwd       string   `json:"cwd"`
		Width     string   `json:"width"`
		Height    string   `json:"height"`
		Command   []string `json:"command"`
		Workspace int      `json:"workspace"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	if len(p.Command) == 0 || p.Command[0] == "" {
		return nil, invalidParam("command", "a popup runs one command and closes when it exits, so name the command to run")
	}
	if err := ValidatePopupSize(p.Width); err != nil {
		return nil, invalidParam("width", err.Error())
	}
	if err := ValidatePopupSize(p.Height); err != nil {
		return nil, invalidParam("height", err.Error())
	}
	if p.Workspace < 0 {
		return nil, invalidParam("workspace", "workspace is a workspace number, e.g. 2. Omit it for the current one")
	}
	// The same refusal new-window makes, for the same reason: a directory that
	// cannot be entered would leave the command running in the wrong place with
	// nothing in the reply to say so.
	if p.Cwd != "" {
		info, err := os.Stat(p.Cwd)
		switch {
		case err != nil:
			return nil, invalidParam("cwd", "cannot start a popup in "+echoName(p.Cwd)+": "+err.Error())
		case !info.IsDir():
			return nil, invalidParam("cwd", echoName(p.Cwd)+" is not a directory")
		}
	}
	if !d.hasTUIClient(sess) {
		return nil, hintedVerbError(ErrVerbNeedsClient,
			"a popup is drawn on a screen, so it needs an attached client",
			&VerbHint{
				Command: "tuios attach " + sess.Name,
				Detail:  "the daemon has no viewport, so it cannot place a popup nobody is displaying. Attach a client and retry.",
			})
	}

	onExit := func(ptyID string) { d.notifyPTYClosed(sess.ID, ptyID) }
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Title:       p.Name,
		Cwd:         p.Cwd,
		Workspace:   p.Workspace,
		Focus:       true,
		Command:     p.Command,
		Name:        p.Name,
		Popup:       true,
		PopupWidth:  p.Width,
		PopupHeight: p.Height,
	}, onExit)
	if err != nil {
		return nil, newWindowErr(err, sess, p.Workspace)
	}

	displayName := win.Title
	if p.Name != "" {
		displayName = p.Name
	}
	return map[string]any{
		"type":      "popup_opened",
		"window_id": win.ID,
		"name":      displayName,
		"workspace": win.Workspace,
		"pty_id":    win.PTYID,
		// The size the popup will use, with the default filled in, so a caller
		// that named none learns what it got instead of reading back its own
		// silence.
		"width":  cmp.Or(win.PopupWidth, PopupDefaultWidth),
		"height": cmp.Or(win.PopupHeight, PopupDefaultHeight),
	}, nil
}

// newWindowErr classifies a creation failure. An out-of-range workspace is a bad
// parameter the caller can correct; anything else came from spawning the shell.
func newWindowErr(err error, sess *Session, ws int) *verbError {
	if strings.Contains(err.Error(), "out of range") {
		return hintedVerbError(ErrVerbInvalidParams, err.Error(), &VerbHint{
			Param:  "workspace",
			Verb:   "list-workspaces",
			Detail: fmt.Sprintf("this session has workspaces 1 to %d. %d is outside that range.", sess.GetState().workspaceBound(), ws),
		})
	}
	return mapResolveErr(err, sess)
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

	// Closing runs against daemon state whether or not a client is attached: the
	// window set and the PTY are the daemon's, and an attached renderer is told
	// through the state push that the mutation raises. There is no second
	// implementation to keep in step and no round trip to the client to fail.
	onExit := func(ptyID string) { d.notifyPTYClosed(sess.ID, ptyID) }
	if _, err := d.executeDaemonCommand(sess, "CloseWindow", args, onExit); err != nil {
		return nil, mapResolveErr(err, sess)
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
		return nil, invalidParam("keys", `keys is required, e.g. "ls,Enter" or "ctrl+c"`)
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
		return nil, mapResolveErr(err, sess)
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
		return nil, mapResolveErr(err, sess)
	}
	if _, err := pty.Write([]byte(p.Text)); err != nil {
		return nil, newVerbError(ErrVerbInternal, err.Error())
	}
	return map[string]any{"type": "ok"}, nil
}

func (d *Daemon) verbCapturePane(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session    string   `json:"session"`
		Window     string   `json:"window"`
		Source     string   `json:"source"`     // visible | recent
		Styled     bool     `json:"styled"`     // include ANSI styling
		Scrollback bool     `json:"scrollback"` // alias for source=recent
		ANSI       bool     `json:"ansi"`       // alias for styled
		Lines      int      `json:"lines"`      // if >0, keep only the last N lines
		Start      int      `json:"start"`      // 1-based inclusive region start
		End        int      `json:"end"`        // 1-based inclusive region end
		Resolved   bool     `json:"resolved"`   // resolve SGR index colours to 24-bit RGB
		Palette    []string `json:"palette"`    // 16 hex colours to resolve against (default: xterm)
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := validateCaptureSource(p.Source); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	pty, err := d.resolvePTYForTarget(sess, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}

	scrollback := p.Scrollback || p.Source == "recent"
	// Resolved implies styling: resolving has nothing to act on without the
	// escape sequences, and the reply must admit what the content carries
	// instead of reporting styled=false beside a rewritten capture.
	ansi := p.Styled || p.ANSI || p.Resolved
	var content string
	if p.Resolved {
		palette, verr := paletteFromParams(p.Palette)
		if verr != nil {
			return nil, verr
		}
		content = pty.CaptureContentResolved(scrollback, palette)
	} else {
		content = pty.CaptureContent(scrollback, ansi)
	}
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
		"type":     "pane_content",
		"content":  content,
		"source":   source,
		"styled":   ansi,
		"resolved": p.Resolved,
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
		return nil, invalidParam("width", "width and height must both be positive")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
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
		return nil, hintedVerbError(ErrVerbInvalidParams,
			"session is required (kill-session never guesses which session to destroy)",
			&VerbHint{Param: "session", Command: "tuios ls", Available: d.sessionNames()})
	}
	if err := d.manager.DeleteSession(p.Session); err != nil {
		available := d.sessionNames()
		return nil, hintedVerbError(ErrVerbSessionNotFound, err.Error(), &VerbHint{
			Param:      "session",
			Command:    "tuios ls",
			DidYouMean: closestMatch(p.Session, available),
			Available:  available,
		})
	}
	return map[string]any{"type": "ok"}, nil
}

func (d *Daemon) verbSetSessionName(_ *connState, params json.RawMessage) (any, *verbError) {
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
	name := strings.TrimSpace(p.Name)
	if err := sess.SetDisplayName(name); err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not set session name: "+err.Error())
	}
	// session is the identity the caller addressed and keeps addressing; the
	// rename only changed display_name.
	return map[string]any{"type": "session_name_set", "session": sess.Name, "display_name": name}, nil
}

func (d *Daemon) verbSetSessionAccent(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Accent  string `json:"accent"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	accent := strings.TrimSpace(p.Accent)
	if err := sess.SetAccent(accent); err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not set session accent: "+err.Error())
	}
	return map[string]any{"type": "session_accent_set", "session": sess.Name, "accent": accent}, nil
}

func (d *Daemon) verbSetWorkspaceName(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string `json:"session"`
		Workspace int    `json:"workspace"`
		Name      string `json:"name"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Workspace == 0 {
		return nil, invalidParam("workspace", "workspace is required and is the workspace number, e.g. 1")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	name := strings.TrimSpace(p.Name)
	if err := sess.SetDaemonWorkspaceName(p.Workspace, name); err != nil {
		return nil, invalidParam("workspace", err.Error())
	}
	return map[string]any{"type": "workspace_name_set", "workspace": p.Workspace, "name": name}, nil
}

func (d *Daemon) verbSetWorkspaceOrder(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Order   []int  `json:"order"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if len(p.Order) == 0 {
		return nil, invalidParam("order", "order is required and is the workspace numbers in the order to show them, e.g. [3,1,2]")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	if err := sess.SetDaemonWorkspaceOrder(p.Order); err != nil {
		return nil, invalidParam("order", err.Error())
	}
	// The stored order is what was kept after sanitising, which is what the
	// caller has to see: a drag that named a workspace this session no longer
	// has should read back without it rather than as accepted verbatim.
	return map[string]any{"type": "workspace_order_set", "workspace_order": sess.GetState().WorkspaceOrder}, nil
}

func (d *Daemon) verbSetAgentState(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		State   string `json:"state"`
		Message string `json:"message"`
		Source  string `json:"source"`
		Harness string `json:"harness"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.State == "" {
		return nil, invalidParam("state", "state is required, one of: "+strings.Join(AgentStateNames, ", "))
	}
	state, ok := ParseAgentState(p.State)
	if !ok {
		return nil, hintedVerbError(ErrVerbInvalidParams, "unknown agent state "+echoName(p.State), &VerbHint{
			Param:      "state",
			DidYouMean: closestMatch(p.State, AgentStateNames),
			Available:  AgentStateNames,
			Detail:     "state names the pane's agent state. Use none to clear it.",
		})
	}
	// An omitted source is a report, so a caller written before sources existed
	// keeps the authority it had.
	source, ok := ParseAgentSource(p.Source)
	if !ok {
		return nil, hintedVerbError(ErrVerbInvalidParams, "unknown agent state source "+echoName(p.Source), &VerbHint{
			Param:      "source",
			DidYouMean: closestMatch(p.Source, AgentSourceNames),
			Available:  AgentSourceNames,
			Detail:     "source says where the state came from and decides which of two competing reports wins. Omit it to report for yourself.",
		})
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

	effective, applied, err := sess.ApplyAgentReport(target, AgentReport{
		State:   state,
		Message: p.Message,
		Source:  source,
		Harness: p.Harness,
	})
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	// state is the effective state, so a report a higher-ranked source outranked
	// reports what the pane actually shows rather than what was asked for.
	// applied says which of the two happened.
	return map[string]any{
		"type":    "agent_state_set",
		"state":   effective.Name(),
		"message": p.Message,
		"source":  source.Name(),
		"applied": applied,
	}, nil
}

func (d *Daemon) verbGetAgentState(_ *connState, params json.RawMessage) (any, *verbError) {
	var p commonParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	state := sess.GetState()
	target := p.Window
	if target == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := state.Windows[idx]
	claim := sess.agentClaimFor(w.ID)
	return map[string]any{
		"type":           "agent_state",
		"window_id":      w.ID,
		"state":          w.AgentState.Name(),
		"message":        w.AgentMessage,
		"agent_state_at": w.AgentStateAt,
		// source and harness_id are what make a shown state explainable: which
		// tier put it there and, once the harness registry lands, which harness.
		// harness_id is empty until something names one.
		"source":     claim.source.Name(),
		"harness_id": claim.harness,
	}, nil
}

// verbExplainAgentDetect says what the foreground-process detector sees in a
// pane and what every manifest makes of it.
//
// Detection was unfalsifiable from outside. A pane was an agent or it was not,
// with no way to ask which of comm, argv0, argv_path or exe_glob decided it, or
// even what the daemon had read. That is why a registry in which every exe_glob
// matched nothing shipped, and why a rule that matched any process with an
// agent's name anywhere in its arguments went unnoticed until users found
// unrelated panes turning into agents. This is the counterpart to
// explain-agent-screen: that one explains a state, this one explains the name.
func (d *Daemon) verbExplainAgentDetect(_ *connState, params json.RawMessage) (any, *verbError) {
	var p commonParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	state := sess.GetState()
	target := p.Window
	if target == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := state.Windows[idx]
	claim := sess.agentClaimFor(w.ID)

	out := map[string]any{
		"type":          "agent_detect",
		"window_id":     w.ID,
		"state":         w.AgentState.Name(),
		"source":        claim.source.Name(),
		"harness_id":    w.AgentHarness,
		"auto_detected": claim.auto,
		"running":       false,
		"matched":       false,
	}

	// Read the process now rather than reporting what the last poll happened to
	// see. A diagnostic that shows a cached answer cannot be used to check a rule
	// against a pane the user is looking at.
	info, running := d.foregroundResolver(sess)(w.PTYID)
	out["running"] = running
	if !running {
		// Not an error: a pane sitting at its shell prompt with nothing running is
		// the ordinary case, and saying so is the answer.
		out["reason"] = "no foreground process could be read for this pane"
		return out, nil
	}

	proc := info.proc()
	out["process"] = harness.Describe(proc)
	if reg := d.agentMatcher.registry; reg != nil {
		out["manifests"] = reg.ExplainDetect(proc)
	}
	if rule, ok := d.agentMatcher.nameRule(proc); ok {
		out["name_list"] = rule
	}
	if id, rule, ok := d.agentMatcher.identifyDetail(info); ok {
		out["matched"] = true
		out["matched_rule"] = rule
		if id != "" {
			out["matched_harness"] = id
		} else {
			// The flat name list matched. It names no harness, so the pane gets no
			// screen rules, which is worth stating rather than leaving to be
			// inferred from an empty field.
			out["matched_harness"] = ""
			out["note"] = "matched the built-in name list, not a manifest: no harness is named, so no screen rules run"
		}
	}
	return out, nil
}

// verbExplainAgentScreen dumps a pane's tail exactly as the screen tier reads
// it, with what every rule of its harness made of it.
//
// Writing a screen rule was otherwise guesswork in both directions: the text is
// matched inside the daemon against a pane that has moved on by the time anyone
// looks, and a rule that fails says nothing about which of its strings was the
// reason. This answers both, so adding a harness is an edit and a re-run rather
// than an experiment.
func (d *Daemon) verbExplainAgentScreen(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		commonParams
		Harness string `json:"harness"`
		Lines   int    `json:"lines"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	state := sess.GetState()
	target := p.Window
	if target == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := state.Windows[idx]
	claim := sess.agentClaimFor(w.ID)

	// A harness named on the call rather than on the pane is how a rule is tried
	// against a pane the detector has not attributed yet, which is every pane
	// while the rule that would attribute it is still being written.
	hid := strings.TrimSpace(p.Harness)
	if hid == "" {
		hid = w.AgentHarness
	}

	reg := d.agentMatcher.registry
	var m *harness.Manifest
	if reg != nil && hid != "" {
		if m = reg.Lookup(hid); m == nil {
			return nil, hintedVerbError(ErrVerbInvalidParams, "unknown harness "+echoName(hid), &VerbHint{
				Param:      "harness",
				DidYouMean: closestMatch(hid, reg.IDs()),
				Available:  reg.IDs(),
				Detail:     "harness names a manifest in the registry. Drop a file in the user manifest directory to add one.",
			})
		}
	}

	// How far up the rules would read, so what is dumped is what would be
	// matched. An explicit lines wins, for checking whether a rule needs to see
	// further up than its manifest lets it.
	lines := p.Lines
	if lines <= 0 && m != nil {
		lines = m.Screen.Lines
	}
	if lines <= 0 {
		lines = harness.DefaultScreenLines
	}

	// The tail is read whether or not a harness was resolved. Writing the first
	// rule for a harness tuios does not know yet means looking at a pane nothing
	// has claimed, so refusing to dump it there would withhold the diagnostic
	// from the case it is most needed in.
	var tail []string
	if w.PTYID != "" {
		if pty := sess.GetPTY(w.PTYID); pty != nil {
			tail = pty.tailText(lines)
		}
	}

	out := map[string]any{
		"type":       "agent_screen",
		"window_id":  w.ID,
		"harness_id": hid,
		"state":      w.AgentState.Name(),
		"source":     claim.source.Name(),
		"lines":      lines,
		"tail":       tail,
		"rules":      []harness.RuleReport{},
		"matched":    false,
		"rule":       -1,
	}
	if m == nil {
		// No harness means no rules to run, which is a fact worth returning
		// rather than an error: it is the answer for most panes.
		return out, nil
	}
	out["enabled"] = m.Screen.Enabled

	matchedState, rule, reports := reg.Explain(hid, tail)
	out["rules"] = reports
	out["rule"] = rule
	out["matched"] = rule >= 0
	out["rule_state"] = matchedState
	return out, nil
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
	case lines > 0:
		// A capture ends at the bottom of the pane, so below the cursor there is
		// always a run of empty rows. Counting those as lines makes "the last 20
		// lines" of a quiet pane twenty blanks, which is never what was wanted.
		// start and end stay row-exact for callers who need the geometry.
		body := split
		for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
			body = body[:len(body)-1]
		}
		if lines < len(body) {
			body = body[len(body)-lines:]
		}
		selected = body
	default:
		selected = split
	}

	out := strings.Join(selected, "\n")
	if trailing && out != "" {
		out += "\n"
	}
	return out
}
