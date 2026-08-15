package session

import (
	"encoding/json"
	"errors"
	"maps"
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
		Detail:     "the name matches no live session. A session that was killed is gone; one that was never started may still have saved state ('tuios resurrect').",
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

	// Creating runs against daemon state whether or not a client is attached: the
	// PTY and the window set are the daemon's. An attached renderer learns of the
	// window from the state push and places it, so there is no round trip to the
	// client that can time out and no second creation path to keep in step.
	onExit := func(ptyID string) { d.notifyPTYClosed(sess.ID, ptyID) }
	data, err := d.executeDaemonCommand(sess, "NewWindow", args, onExit)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	out := map[string]any{"type": "window_created"}
	maps.Copy(out, data)
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
		Session    string `json:"session"`
		Window     string `json:"window"`
		Source     string `json:"source"`     // visible | recent
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
		return nil, invalidParam("key", `key is required, e.g. "appearance.dockbar_position"`)
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
		return nil, invalidParam("key", `key is required, e.g. "appearance.dockbar_position"`)
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	value, ok := sess.GetOption(p.Key)
	if !ok {
		available := sess.OptionKeys()
		return nil, hintedVerbError(ErrVerbOptionNotFound, "option "+p.Key+" is not set on session "+sess.Name, &VerbHint{
			Param:      "key",
			Verb:       "set-option",
			Command:    "tuios set-config " + p.Key + " <value>",
			DidYouMean: closestMatch(p.Key, available),
			Available:  available,
			Detail:     "get-option only reads options previously set through set-option on this session; it does not read the config file.",
		})
	}
	return map[string]any{"type": "option", "key": p.Key, "value": value}, nil
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
			Detail:     "state names the pane's semantic agent state; use none to clear it.",
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
			Detail:     "source says where the state came from and decides which of two competing reports wins; omit it to report for yourself.",
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
				Detail:     "harness names a manifest in the registry; drop a file in the user manifest directory to add one.",
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
