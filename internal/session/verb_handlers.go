package session

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

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
	if _, ok := errors.AsType[errNeedsClient](err); ok {
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
		if strings.Contains(msg, "ambiguous window") {
			// The code stays window_not_found, which callers already handle:
			// the target did not name one window. The detail says why.
			hint.Detail = "the window target matched more than one window, listed in the message. Pass the id, or give the windows different names with set-window --name."
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
	addShellFacts(sess, data)
	return data, nil
}

// verbGetWindow describes one window. It is what tuios get-window calls, so
// the command is a read like list-windows rather than a message of the client
// protocol, which only admin may send. It answers the way the client
// protocol's GetWindow did: an attached client describes the window, with its
// cursor and process fields, and with none attached the daemon gives the
// window's list-windows entry. The window is resolved here either way, so a
// target that matches nothing is the same error attached or not.
func (d *Daemon) verbGetWindow(_ *connState, params json.RawMessage) (any, *verbError) {
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
	if tui := d.findTUIClient(sess.ID); tui != nil {
		res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
			CommandType: "tape_command",
			TapeCommand: "GetWindow",
			TapeArgs:    []string{state.Windows[idx].ID},
		}, routedVerbTimeout)
		if err == nil && res.Success && res.Data != nil {
			data := maps.Clone(res.Data)
			data["type"] = "window"
			return data, nil
		}
		// A client that does not answer does not stop the read: the
		// daemon's own record of the window follows.
	}
	data := windowStateToData(state, idx)
	if pty := sess.GetPTY(state.Windows[idx].PTYID); pty != nil {
		maps.Copy(data, shellFactsData(pty.ShellFacts()))
	}
	data["type"] = "window"
	return data, nil
}

func (d *Daemon) verbNewWindow(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string   `json:"session"`
		Name      string   `json:"name"`
		Workspace int      `json:"workspace"`
		Cwd       string   `json:"cwd"`
		Focus     *bool    `json:"focus"`
		Command   []string `json:"command"`
		Host      string   `json:"host"`
		Grants    []string `json:"grants"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	grants, verr := d.launchGrants(cs, p.Grants)
	if verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	if p.Workspace < 0 {
		return nil, invalidParam("workspace", "workspace is a workspace number, e.g. 2. Omit it for the current one")
	}
	// A window on another machine. The name is checked against the [hosts]
	// table here rather than at the spawn, so a typo is a parameter error that
	// names the machines available instead of a link failure twenty seconds
	// later. "local" is accepted and means this machine, which is the default.
	if verr := checkWindowHost(d, &p.Host); verr != nil {
		return nil, verr
	}
	// A directory that cannot be entered is refused rather than quietly ignored.
	// A window on another machine is the exception: the path is that machine's
	// to judge, and checking it against this filesystem would refuse a
	// directory that exists there and accept one that does not.
	if p.Host == "" {
		if verr := checkWindowCwd(p.Cwd); verr != nil {
			return nil, verr
		}
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
		Host:      p.Host,
		Grants:    grants,
	}, onExit)
	if err != nil {
		return nil, newWindowErr(err, sess, p.Workspace)
	}

	displayName := win.Title
	if p.Name != "" {
		displayName = p.Name
	}

	// With a client attached, answer once the client has placed the window
	// and sized its terminal. A program started in the pane straight after
	// the call otherwise starts at the nominal size and gets a resize while
	// it draws, which some programs (glow's pager) never recover from.
	if win.Unplaced && d.findTUIClient(sess.ID) != nil {
		win.Unplaced = !d.awaitPlacement(sess, win.ID, win.PTYID, newWindowPlaceWait)
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
		// Omitted for a window on this machine, so the ordinary result keeps
		// the shape it has always had.
		"host": win.Host,
	}, nil
}

// newWindowPlaceWait bounds how long new-window waits for an attached client
// to place the window it made. A client places a window on its next frame, so
// the limit is only reached when the client is stuck; the call then answers
// unplaced, as it did before it waited at all.
const newWindowPlaceWait = time.Second

// placeSettle is how long, after the client placed a window, new-window waits
// for the pane's terminal to take the new size. The client sends the size
// after the geometry, so the two land a moment apart.
const placeSettle = 250 * time.Millisecond

// awaitPlacement waits until an attached client has placed a window (cleared
// Unplaced) and then, briefly, until the pane's terminal size changes from the
// nominal one it was created with. It reports whether the window was placed.
func (d *Daemon) awaitPlacement(sess *Session, windowID, ptyID string, limit time.Duration) bool {
	var pty *PTY
	var cols, rows int
	if ptyID != "" {
		if pty = sess.GetPTY(ptyID); pty != nil {
			cols, rows = pty.Size()
		}
	}
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		w, ok := findWindowState(sess.GetState(), windowID)
		if !ok {
			return false
		}
		if w.Unplaced {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		settle := time.Now().Add(placeSettle)
		for pty != nil && time.Now().Before(settle) {
			if c, r := pty.Size(); c != cols || r != rows {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		return true
	}
	return false
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
		// Wait keeps the call open until the command exits; CaptureStdout
		// returns what it printed to standard output. See popup_wait.go.
		Wait          bool `json:"wait"`
		CaptureStdout bool `json:"capture_stdout"`
		Timeout       int  `json:"timeout"`
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
	if p.CaptureStdout && !p.Wait {
		return nil, invalidParam("capture_stdout", "capture_stdout returns the output when the command exits, so it needs wait")
	}
	if p.CaptureStdout && runtime.GOOS == "windows" {
		return nil, invalidParam("capture_stdout", "capture_stdout is not supported on Windows, where the console carries the output. Redirect inside the popup instead")
	}
	if p.Timeout < 0 {
		return nil, invalidParam("timeout", "timeout is milliseconds and cannot be negative")
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
	opts := NewWindowOptions{
		Title:       p.Name,
		Cwd:         p.Cwd,
		Workspace:   p.Workspace,
		Focus:       true,
		Command:     p.Command,
		Name:        p.Name,
		Popup:       true,
		PopupWidth:  p.Width,
		PopupHeight: p.Height,
	}
	var capture *popupCapture
	if p.CaptureStdout {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, newVerbError(ErrVerbInternal, "cannot make a pipe for the popup's output: "+err.Error())
		}
		opts.stdout = w
		capture = newPopupCapture(r)
	}
	win, err := sess.AddDaemonWindowWith(opts, onExit)
	// The process holds its own copy of the write end once it has started,
	// so the daemon's copy is closed here, and the read ends when the process
	// and its children have closed theirs. On a failed start this also ends
	// the read.
	if opts.stdout != nil {
		_ = opts.stdout.Close()
	}
	if err != nil {
		return nil, newWindowErr(err, sess, p.Workspace)
	}

	displayName := win.Title
	if p.Name != "" {
		displayName = p.Name
	}
	if p.Wait {
		code, exited := d.waitPopupExit(sess, win, time.Duration(p.Timeout)*time.Millisecond)
		if !exited {
			return nil, hintedVerbError(ErrVerbTimeout, "the popup was still open when the wait ended", &VerbHint{
				Command: "tuios wait-for window-exit -w " + win.ID,
				Detail:  "The popup is still on the screen and its command still runs. Wait for it with the command shown, or raise timeout.",
			})
		}
		res := map[string]any{
			"type":      "popup_result",
			"window_id": win.ID,
			"name":      displayName,
			"exit_code": code,
		}
		if capture != nil {
			out, truncated := capture.finish()
			res["stdout"] = out
			res["stdout_truncated"] = truncated
		}
		return res, nil
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
	// A window that could not be opened on another machine has nothing to do
	// with window targets. mapResolveErr below is for the failures of naming a
	// window, and its fallback hint says the target matched nothing and lists
	// the windows that exist, which on a link failure is advice about the
	// wrong problem printed under a message about the right one.
	if msg := err.Error(); strings.Contains(msg, "tuios on ") || strings.Contains(msg, "host ") {
		return newVerbError(ErrVerbInternal, msg)
	}
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

func (d *Daemon) verbSendKeys(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Keys    string `json:"keys"`
		Literal bool   `json:"literal"`
		Raw     bool   `json:"raw"`
		Repeat  int    `json:"repeat"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Keys == "" {
		return nil, invalidParam("keys", `keys is required, e.g. "Down" or "ctrl+c"`)
	}
	if p.Repeat < 0 || p.Repeat > maxSendKeysRepeat {
		return nil, invalidParam("repeat", fmt.Sprintf("repeat must be between 1 and %d, got %d", maxSendKeysRepeat, p.Repeat))
	}
	repeat := max(p.Repeat, 1)
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	// Parse before anything is sent, so a misspelled key fails whole on either
	// route instead of arriving as its letters.
	var parsed []sendKey
	if !p.Literal && !p.Raw {
		var err error
		if parsed, err = parseSendKeys(p.Keys, repeat); err != nil {
			return nil, sendKeysParseError(err, sess, p.Window)
		}
	}

	if verr := d.recheckTyping(cs, "send-keys", sess, p.Window); verr != nil {
		return nil, verr
	}

	// Where the keys go. Keys for a named window go to that window's
	// terminal, attached or not: the attached client reads keys as the
	// person's and hands them to the focused window, which is not the one
	// the caller named. With no window, an attached client gets them, so the
	// prefix and the window manager's keys work the way the person's do. A
	// pane without admin always writes to a terminal: window-manager keys
	// would let it do what only admin may (paneTypesRaw).
	tui := d.findTUIClient(sess.ID)
	if p.Window != "" && hasPrefixKey(parsed) && tui != nil && !paneTypesRaw(cs) {
		return nil, hintedVerbError(ErrVerbInvalidParams,
			fmt.Sprintf("PREFIX goes to the window manager, which acts on the focused window, not on window %q", p.Window),
			&VerbHint{
				Param:   "window",
				Command: "tuios focus-window " + p.Window,
				Detail:  "Leave out the window to send window-manager keys, after focus-window if they should act on a particular window. Keys for a program in a window take no PREFIX.",
			})
	}
	if p.Window == "" && !p.Literal && tui != nil && !paneTypesRaw(cs) {
		var keys string
		count := len(parsed)
		if p.Raw {
			keys = strings.Repeat(p.Keys, repeat)
			count = utf8.RuneCountInString(keys)
		} else {
			canonical, err := sendKeysCanonical(parsed)
			if err != nil {
				return nil, invalidParam("keys", err.Error())
			}
			keys = canonical
		}
		res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
			CommandType: "send_keys",
			Keys:        keys,
			Raw:         p.Raw,
		}, routedVerbTimeout)
		if err != nil {
			return nil, newVerbError(ErrVerbCommandFailed, err.Error())
		}
		if !res.Success {
			return nil, newVerbError(ErrVerbCommandFailed, res.Message)
		}
		return map[string]any{"type": "ok", "sent_to": "client", "keys": count}, nil
	}

	keys := p.Keys
	if (p.Literal || p.Raw) && repeat > 1 {
		keys = strings.Repeat(p.Keys, repeat)
	}
	win, err := d.writeKeysToWindow(sess, p.Window, keys, p.Literal, p.Raw, parsed)
	if err != nil {
		var unknown errUnknownKey
		if errors.As(err, &unknown) || strings.Contains(err.Error(), "prefix key") || strings.Contains(err.Error(), "unsupported") {
			return nil, sendKeysParseError(err, sess, p.Window)
		}
		if errors.Is(err, errPaneReconnecting) {
			return nil, ptyWriteError(err)
		}
		return nil, mapResolveErr(err, sess)
	}
	count := len(parsed)
	if parsed == nil {
		count = utf8.RuneCountInString(keys)
	}
	return map[string]any{
		"type":      "ok",
		"sent_to":   "window",
		"window_id": win.ID,
		"window":    windowDisplayName(win),
		"keys":      count,
	}, nil
}

// sendKeysParseError is the verb error for keys that do not parse, naming the
// window they were meant for so a caller driving several can tell which call
// failed.
func sendKeysParseError(err error, sess *Session, target string) *verbError {
	msg := err.Error()
	if target != "" {
		msg = fmt.Sprintf("send-keys to window %q: %s", target, msg)
	}
	hint := &VerbHint{
		Param:    "keys",
		Accepted: KeyNames(),
		Command:  "tuios send-keys --help",
		Detail:   "A key is one of the names listed, a single character, or either of those after ctrl+, alt+ or shift+. Keys are split on spaces and commas; text to type goes through send-text.",
	}
	if unknown, ok := errors.AsType[errUnknownKey](err); ok {
		hint.DidYouMean = unknown.didYouMean
	}
	return hintedVerbError(ErrVerbInvalidParams, msg, hint)
}

// windowDisplayName is the name a window shows: its custom name, else its title.
func windowDisplayName(w WindowState) string {
	if w.CustomName != "" {
		return w.CustomName
	}
	return w.Title
}

func (d *Daemon) verbSendText(cs *connState, params json.RawMessage) (any, *verbError) {
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
	if verr := d.recheckTyping(cs, "send-text", sess, p.Window); verr != nil {
		return nil, verr
	}
	if _, err := pty.Write([]byte(p.Text)); err != nil {
		return nil, ptyWriteError(err)
	}
	return map[string]any{"type": "ok"}, nil
}

// ptyWriteError is the verb error for a write a pane refused. A pane on
// another machine whose link is being restored refuses writes, which is
// host_unreachable: the text was not typed, and waiting is the remedy.
func ptyWriteError(err error) *verbError {
	if errors.Is(err, errPaneReconnecting) {
		return hintedVerbError(ErrVerbHostUnreachable, err.Error(), &VerbHint{
			Command: "tuios list-windows",
			Detail:  "Nothing was typed. The window's process is still running on the other machine; host_link and host_link_until in list-windows say until when it waits for the link.",
		})
	}
	return newVerbError(ErrVerbInternal, err.Error())
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

	if p.Source == captureLastCommand {
		return captureLastCommandOutput(pty, p.Window, p.Styled || p.ANSI || p.Resolved, p.Start, p.End, p.Lines)
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
		// The fields below are what a hook reporter adds. Every one is
		// optional, and a caller that sends none of them is handled exactly as
		// before they existed.
		Kind           string `json:"kind"`
		AgentSessionID string `json:"agent_session_id"`
		TranscriptPath string `json:"transcript_path"`
		IfState        string `json:"if_state"`
		HarnessPID     int    `json:"harness_pid"`
		// Activity is one hook event for the pane's activity ring. It is
		// recorded when the report passes the identity guard, whether or not
		// the state part applies. See agent_activity.go.
		Activity *AgentActivityReport `json:"activity"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := checkActivityReport(p.Activity); verr != nil {
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
	if p.Kind != "" {
		if p.Kind != harness.PromptKindApproval && p.Kind != harness.PromptKindQuestion {
			return nil, hintedVerbError(ErrVerbInvalidParams, "unknown kind "+echoName(p.Kind), &VerbHint{
				Param:     "kind",
				Available: agentKindNames,
				Detail:    "kind says what a needs_input state waits for.",
			})
		}
		if state != AgentStateNeedsInput {
			return nil, invalidParam("kind", "kind describes a needs_input state, and this report is "+state.Name())
		}
	}
	var ifState []AgentState
	for name := range strings.SplitSeq(p.IfState, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		st, ok := ParseAgentState(name)
		if !ok {
			return nil, hintedVerbError(ErrVerbInvalidParams, "unknown agent state "+echoName(name)+" in if_state", &VerbHint{
				Param:      "if_state",
				DidYouMean: closestMatch(name, AgentStateNames),
				Available:  AgentStateNames,
				Detail:     "if_state lists the states the window must be in for the report to apply.",
			})
		}
		ifState = append(ifState, st)
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

	report := AgentReport{
		State:      state,
		Message:    p.Message,
		Source:     source,
		Harness:    p.Harness,
		Kind:       p.Kind,
		SessionID:  p.AgentSessionID,
		HarnessPID: p.HarnessPID,
		IfState:    ifState,
	}
	// The identity guard is read before the report applies, because it is
	// about the pane as the report found it, and applyAgentReport checks
	// if_state first and so may never reach it.
	var windowID, guard string
	if p.Activity != nil {
		windowID, guard = sess.agentReportGuard(target, report)
	}
	// A pane's first activity gets its ring before the report applies, so
	// the state change the report makes reaches the ring through the session
	// event sink, completion_seq step included, the same as for a pane that
	// already had one. Without this a first report that finished a turn, a
	// Stop after the hooks were installed mid-session or after a daemon
	// restart, kept its turn_end entry but did not count the turn.
	created := false
	if p.Activity != nil && windowID != "" && guard == "" {
		created = d.activity.ensure(sess.ID, windowID)
	}
	effective, applied, reason, err := sess.applyAgentReport(target, report)
	if err != nil {
		if created {
			d.activity.forgetIfEmpty(sess.ID, windowID)
		}
		return nil, mapResolveErr(err, sess)
	}
	// Activity is recorded for a report from the pane's own agent, applied or
	// not: a PostToolUse refused by if_state still finished a tool call. A
	// report the identity guard refuses is a nested run's, and its activity
	// is not the pane's.
	recorded := false
	if p.Activity != nil && windowID != "" && guard == "" &&
		reason != agentRefusedForeignSession && reason != agentRefusedForeignHarness {
		d.recordAgentActivity(sess, windowID, p.Activity, effective)
		recorded = true
	} else if created {
		// The guard read before the report let it through and the report
		// then found a nested run: the ring made for it holds nothing of the
		// pane's agent.
		d.activity.forgetIfEmpty(sess.ID, windowID)
	}
	if applied && p.TranscriptPath != "" {
		d.joinReportedTranscript(sess, target, p.Harness, p.TranscriptPath)
	}
	// state is the effective state, so a report a higher-ranked source outranked
	// reports what the pane actually shows rather than what was asked for.
	// applied says which of the two happened, and reason says why not.
	out := map[string]any{
		"type":    "agent_state_set",
		"state":   effective.Name(),
		"message": p.Message,
		"source":  source.Name(),
		"applied": applied,
	}
	if reason != "" {
		out["reason"] = reason
	}
	if p.Activity != nil {
		out["activity_recorded"] = recorded
	}
	return out, nil
}

// verbSetAgentSession stores the conversation id a harness reports for a pane
// without touching the pane's state. See applyAgentSession.
func (d *Daemon) verbSetAgentSession(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session        string `json:"session"`
		Window         string `json:"window"`
		Harness        string `json:"harness"`
		AgentSessionID string `json:"agent_session_id"`
		HarnessPID     int    `json:"harness_pid"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	harnessID := strings.TrimSpace(p.Harness)
	if harnessID == "" {
		return nil, invalidParam("harness", "harness is required: the id of the harness the conversation belongs to, e.g. qwen")
	}
	sid := strings.TrimSpace(p.AgentSessionID)
	if sid == "" {
		return nil, invalidParam("agent_session_id", "agent_session_id is required: the harness's own id for the conversation")
	}
	if len(sid) > maxAgentSessionIDLen {
		return nil, invalidParam("agent_session_id", fmt.Sprintf("agent_session_id is %d bytes; the limit is %d", len(sid), maxAgentSessionIDLen))
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
	stored, applied, reason, err := sess.applyAgentSession(target, AgentSessionReport{
		Harness:    harnessID,
		SessionID:  sid,
		HarnessPID: p.HarnessPID,
	})
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	out := map[string]any{
		"type":             "agent_session_set",
		"agent_session_id": stored,
		"applied":          applied,
	}
	if reason != "" {
		out["reason"] = reason
	}
	return out, nil
}

// maxAgentSessionIDLen bounds a reported conversation id. Every harness uses a
// uuid or a short token; the cap only stops a caller parking a blob on the
// window, which is synced to every client and persisted.
const maxAgentSessionIDLen = 256

// agentKindNames are the values set-agent-state accepts for kind. They are
// the manifest rule kinds, so a hook and a screen rule describe a block in the
// same words.
var agentKindNames = []string{harness.PromptKindApproval, harness.PromptKindQuestion}

// joinReportedTranscript binds a window to the transcript file its harness
// named in a hook. This is the exact join the transcript source was built for:
// the searched join refuses whenever two files could be the pane's, and the
// harness naming its own file settles that. Only a harness whose manifest has
// a transcript reader is joined, since nothing else could read the file, and
// a failure leaves the pane on whatever join it had.
func (d *Daemon) joinReportedTranscript(sess *Session, target, harnessID, path string) {
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return
	}
	w := state.Windows[idx]
	if harnessID == "" {
		harnessID = w.AgentHarness
	}
	reg := d.agentMatcher.registry
	if harnessID == "" || reg == nil || reg.TranscriptFor(harnessID) == nil {
		return
	}
	_ = sess.JoinAgentTranscript(w.ID, harnessID, path, true)
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
		// identity and confidence say what kind of evidence named the harness:
		// a report from the harness itself is certain, a process name is strong,
		// and a pane nothing has named has none.
		"identity":   string(claim.identity),
		"confidence": claim.identity.confidence(),
		// needs_you is the one question a person asks of a pane, answered as a
		// bool so a consumer does not have to know which states mean it.
		"needs_you": w.AgentState.NeedsYou(),
		"activity":  w.AgentState.Activity(),
		// ready and blocked_by are the same answers list-agents gives: whether
		// ask-agent would type at the pane now, and, for a pane on needs_input,
		// whether it waits on an approval or a question. blocked_by is also
		// where the kind a hook reported with set-agent-state reads back.
		"ready":      d.agentReady(w, agentRestStates),
		"blocked_by": agentBlockedBy(w),
		// The harness's own conversation id, empty until a hook reports one.
		"agent_session_id": w.AgentSessionID,
		// meta is what set-agent-meta recorded, key to value.
		"meta": agentMetaMap(w.AgentMeta, time.Now().UnixNano()),
		// queued is how many messages wait in the pane's delivery queue.
		"queued": w.AgentQueued,
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
//
// The answer leads with a verdict in plain words and the evidence it rests on,
// then lists every word on the command line that looks like an agent's name and
// was not counted. A person who reports a false positive should be able to read
// the answer once and see either the rule that fired or the word they took for
// one.
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
	// A pane nothing has claimed has no source. The zero claim names itself
	// report, and printing that would say a shell prompt reported for itself.
	source := ""
	if isAgentWindow(w) {
		source = claim.source.Name()
	}

	out := map[string]any{
		"type":          "agent_detect",
		"window_id":     w.ID,
		"state":         w.AgentState.Name(),
		"source":        source,
		"harness_id":    w.AgentHarness,
		"auto_detected": claim.auto,
		"identity":      string(claim.identity),
		"confidence":    claim.identity.confidence(),
		"needs_you":     w.AgentState.NeedsYou(),
		"activity":      w.AgentState.Activity(),
		"running":       false,
		"matched":       false,
	}
	var evidence []string
	if claim.identity == identityReport && claim.harness != "" {
		evidence = append(evidence, "The harness "+claim.harness+" named itself in a report. That is certain.")
	}

	// Read the process now rather than reporting what the last poll happened to
	// see. A diagnostic that shows a cached answer cannot be used to check a rule
	// against a pane the user is looking at.
	info, running := d.foregroundResolver(sess)(w.PTYID)
	out["running"] = running
	if !running {
		// Not an error: a pane with no live process is the ordinary case, and
		// saying so is the answer.
		out["reason"] = "No foreground process can be read for this pane."
		out["verdict"] = "This pane runs no process that tuios can read."
		out["evidence"] = evidence
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
	det, ok := d.agentMatcher.identifyDetail(info)
	if len(det.visited) > 0 {
		group := make([]map[string]any, 0, len(det.visited))
		for _, member := range det.visited {
			_, matched := d.agentMatcher.matchProc(member)
			group = append(group, map[string]any{
				"pid":     member.pid,
				"depth":   member.depth,
				"comm":    member.comm,
				"argv":    member.argv,
				"exe":     member.exe,
				"matched": matched,
			})
		}
		out["group"] = group
	}
	label := processLabel(info)
	switch {
	case ok:
		out["matched"] = true
		out["matched_rule"] = det.rule
		out["matched_harness"] = det.harness
		out["matched_via"] = det.via
		if claim.identity != identityReport {
			out["confidence"] = det.tier.confidence()
			out["identity"] = string(det.tier)
		}
		name := det.harness
		if name == "" {
			name = "an agent named " + processLabel(det.proc)
			out["note"] = "The name list matched, not a manifest. No harness is named, so no screen rules run."
		}
		if det.tier == identityHint {
			// The process itself was not recognised: its environment named
			// the harness. Said as that, so nobody reads it as a name match.
			out["verdict"] = "This pane runs " + name + ", as named by " + AgentHintEnv + "."
			evidence = append(evidence, fmt.Sprintf("Named by %s on pid %d (%s). The process itself is not a known agent.",
				det.rule, det.proc.pid, processLabel(det.proc)))
			break
		}
		what := "The process " + processLabel(det.proc) + " matched " + describeRule(det)
		if len(det.via) > 0 {
			out["verdict"] = "This pane runs " + name + " behind " + strings.Join(det.via, ", ") + "."
			evidence = append(evidence, "The foreground process "+label+" is a wrapper, so tuios read the processes behind it.")
		} else {
			out["verdict"] = "This pane runs " + name + "."
		}
		evidence = append(evidence, what)
		if det.harness == "" {
			evidence = append(evidence, "A process name is strong evidence. It names no harness.")
		} else {
			evidence = append(evidence, "A process name is strong evidence.")
		}
	case info.atShell():
		out["verdict"] = "This pane is at its shell prompt. It runs no agent."
	default:
		out["verdict"] = "This pane does not run an agent. The foreground process is " + label + "."
		if proc.Wraps() {
			if len(det.visited) == 0 {
				evidence = append(evidence, "The process "+label+" is a wrapper. Nothing runs behind it.")
			} else {
				evidence = append(evidence, fmt.Sprintf("The process %s is a wrapper. None of the %d processes behind it is an agent.",
					label, len(det.visited)))
			}
		}
		if info.pid > 0 && info.group == nil && proc.Wraps() {
			evidence = append(evidence, "This platform cannot list the processes behind a wrapper.")
		}
		out["ignored"] = d.agentMatcher.mentions(info)
	}
	out["evidence"] = evidence
	return out, nil
}

// describeRule spells a detection's rule as a sentence fragment: which manifest
// or list it came from, and the predicate.
func describeRule(det detection) string {
	if det.harness != "" {
		return "the manifest " + det.harness + " on " + det.rule + "."
	}
	return "the name list on " + strings.TrimPrefix(det.rule, "name ") + "."
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
	// The pane title is read here, in the same look as the tail, so the
	// explanation and the classification below run against one reading rather
	// than two of a value that moves.
	var tail []string
	var paneTitle, paneProgress string
	if w.PTYID != "" {
		if pty := sess.GetPTY(w.PTYID); pty != nil {
			tail = pty.tailText(lines)
			paneTitle = pty.Title()
			paneProgress = pty.ProgressText()
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
		"title":      paneTitle,
	}
	if m == nil {
		// No harness means no rules to run, which is a fact worth returning
		// rather than an error: it is the answer for most panes.
		return out, nil
	}
	out["enabled"] = m.Screen.Enabled
	// Which file is in force, so a user override that shadows a bundled
	// manifest is visible where its rules are being read.
	source, replaced := m.Source()
	out["manifest_source"] = source
	out["replaces_bundled"] = replaced

	matchedState, rule, reports := reg.Explain(hid, tail)
	out["rules"] = reports
	out["rule"] = rule
	out["matched"] = rule >= 0
	out["rule_state"] = matchedState

	// The title tier, reported beside the screen tier rather than in a verb of
	// its own. Someone asking why a pane reads the way it does is asking about
	// the pane, not about one channel, and a title rule that fired is exactly
	// the thing they would otherwise have no way to see: the string it matched
	// is gone from the screen by the time anyone looks.
	out["title"] = paneTitle
	out["title_enabled"] = m.Title.Enabled
	out["progress"] = paneProgress
	titleState, titleRule, titleReports := reg.ExplainOSC(hid, paneTitle, paneProgress)
	out["title_rules"] = titleReports
	out["title_rule"] = titleRule
	out["title_matched"] = titleRule >= 0
	out["title_rule_state"] = titleState
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
