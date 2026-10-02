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

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
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
			hint.Command = "tuios attach " + sess.Name()
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
	data := buildSessionInfoData(sess, sess.GetState(), hasClient, d.sessionHostFocus(sess.ID))
	data["type"] = "session_info"
	// Before any client was measured the session has no policy in force, and
	// the one it is set to is the honest answer.
	if data["window_size"] == "" {
		data["window_size"] = d.sessionWindowSize(sess)
	}
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
	addPaneMeta(sess, data)
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
		m := pty.Meta()
		data["history_rows"], data["revision"] = m.HistoryRows, m.Revision
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
		// CloseOnExit closes the window when its process exits, also with no
		// client attached. An attached client closes a command window on
		// exit already. A detached session keeps it until something closes
		// it, which is right for a shell and wrong for tuios xpanes -ss.
		CloseOnExit bool `json:"close_on_exit"`
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
	onExit := func(ptyID string) {
		d.notifyPTYClosed(sess.ID, ptyID)
		if p.CloseOnExit {
			d.closeWindowOfPTY(sess, ptyID)
		}
	}
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
	if errors.Is(err, ErrScratchExists) {
		return nil, invalidParam("scratch", err.Error())
	}
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
	gone := false
	placed := sess.WaitState(limit, func(st *SessionState) bool {
		w, ok := findWindowState(st, windowID)
		gone = !ok
		return gone || !w.Unplaced
	})
	if !placed || gone {
		return false
	}
	// The size is the terminal's, not the session state's, so no state
	// change wakes this short wait.
	settle := time.Now().Add(placeSettle)
	for pty != nil && time.Now().Before(settle) {
		if c, r := pty.Size(); c != cols || r != rows {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
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
		// Scratch opens the session's scratch terminal: a shell when no
		// command is named, marked so toggle_scratch shows and hides it.
		Scratch bool `json:"scratch"`
		// ScratchName names the scratch pane. Empty is the built-in one.
		ScratchName string `json:"scratch_name"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	// The scratch terminal runs the user's shell unless told otherwise, so it
	// is the one popup that needs no command.
	if (len(p.Command) == 0 || p.Command[0] == "") && !p.Scratch {
		return nil, invalidParam("command", "a popup runs one command and closes when it exits, so name the command to run")
	}
	if len(p.Command) > 0 && p.Command[0] == "" {
		return nil, invalidParam("command", "the command's first word is empty. Name the program to run")
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
				Command: "tuios attach " + sess.Name(),
				Detail:  "the daemon has no viewport, so it cannot place a popup nobody is displaying. Attach a client and retry.",
			})
	}

	// One scratch terminal per session. The toggle shows the one there is,
	// so a second is only ever a double press that raced the first. This
	// early check spares a shell; AddDaemonWindowWith makes the check that
	// holds, under the state lock.
	if p.Scratch {
		want := WindowState{ScratchName: p.ScratchName}.ScratchKey()
		for _, w := range sess.GetState().Windows {
			if w.Scratch && w.ScratchKey() == want {
				return nil, invalidParam("scratch", ErrScratchExists.Error())
			}
		}
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
		Scratch:     p.Scratch,
		ScratchName: p.ScratchName,
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
		// A scratch pane whose command ended is closed before the answer
		// goes out, not later when the PTY close is handled. The caller
		// reports the exit and the person presses the key again at once, and
		// a pane still in the state then answered that press with "already
		// has a scratch terminal", or was shown and hidden, dead, instead of
		// starting the command again. An error means it is closed already.
		if p.Scratch {
			_, _ = sess.CloseDaemonWindow(win.ID)
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
	//
	// A link that is down has a code of its own, so a caller can say the
	// machine is unavailable instead of printing the link's state word.
	if down, ok := errors.AsType[*federation.UnreachableError](err); ok {
		msg := down.Host + " is unavailable."
		if down.Reason != "" {
			msg += " " + down.Reason
		}
		return newVerbError(ErrVerbHostUnreachable, msg)
	}
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

// closeWindowOfPTY closes the window whose PTY is ptyID, off the caller's
// goroutine: an exit callback can run where the state lock is held. A window
// that an attached client closed first is gone already, which is fine.
func (d *Daemon) closeWindowOfPTY(sess *Session, ptyID string) {
	d.goTracked(func() {
		for _, w := range sess.GetState().Windows {
			if w.PTYID == ptyID {
				_, _ = sess.CloseDaemonWindow(w.ID)
				return
			}
		}
	})
}

// verbCloseWorkspace closes every pane on one workspace. A scratch pane is
// left alone unless the workspace named is a scratch workspace: a scratch
// group is closed only when it is the target.
func (d *Daemon) verbCloseWorkspace(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string `json:"session"`
		Workspace int    `json:"workspace"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()
	ws := p.Workspace
	if ws == 0 {
		ws = state.CurrentWorkspace
	}
	if !state.workspaceAccepts(ws) {
		return nil, invalidParam("workspace", fmt.Sprintf("workspace %d does not exist. Use a number from 1 to %d", ws, state.workspaceBound()))
	}
	ids := WindowsToCloseOnWorkspace(state.Windows, ws)
	closed := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := sess.CloseDaemonWindow(id); err != nil {
			// A window that closed on its own meanwhile is not a failure.
			if _, gone := findWindowStateIndex(sess.GetState().Windows, id); gone != nil {
				continue
			}
			return nil, mapResolveErr(err, sess)
		}
		closed = append(closed, id)
	}
	return map[string]any{"type": "workspace_closed", "workspace": ws, "closed": closed}, nil
}

// WindowsToCloseOnWorkspace is the windows close-workspace closes on ws, in
// window order: every window there, and scratch panes only on a scratch
// workspace.
func WindowsToCloseOnWorkspace(windows []WindowState, ws int) []string {
	var ids []string
	for _, w := range windows {
		if w.Workspace != ws || (w.Scratch && !IsScratchWorkspace(ws)) {
			continue
		}
		ids = append(ids, w.ID)
	}
	return ids
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

	if paneTypesRaw(cs) && hasPrefixKey(parsed) {
		// The prefix key drives the window manager through the attached
		// client, and a pane's keys never go through the client.
		return nil, hintedVerbError(ErrVerbForbidden, "send-keys with PREFIX is refused from a pane: a pane's keys go to a pane's terminal, never to the window manager", &VerbHint{
			Param:   "keys",
			Verb:    "focus-window",
			Command: "tuios focus-window <window>",
			Detail:  "Nothing was sent. Use the verbs for window-manager actions: focus-window, new-window, close-window, split-window or set-layout. Then send keys without PREFIX, with -w naming the pane.",
		})
	}
	if p.Window == "" && paneTypesRaw(cs) {
		// The keys go to the focused pane's terminal. Pin it now, so the
		// pane checked is the pane written to.
		if id, err := focusedWindowID(sess.GetState()); err == nil {
			p.Window = id
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
		Paste   bool   `json:"paste"`
		Submit  bool   `json:"submit"`
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
	if p.Submit {
		// The prompt path ask-agent types with: one paste, a wait for the
		// program to take it in, and the harness's own submit key.
		window := p.Window
		if window == "" {
			window, _ = focusedWindowID(sess.GetState())
		} else if idx, err := findWindowStateIndex(sess.GetState().Windows, window); err == nil {
			window = sess.GetState().Windows[idx].ID
		}
		if _, err := submitPrompt(d.ctx, pty, p.Text, d.inputProfileFor(sess, window)); err != nil {
			return nil, ptyWriteError(err)
		}
		return map[string]any{"type": "ok"}, nil
	}
	text := p.Text
	if p.Paste {
		// A paste: sanitized as every paste into a pane is, and bracketed
		// when the pane's program asked for bracketed paste.
		text = vt.SanitizePaste(text)
		if text != "" && pty.BracketedPasteOn() {
			text = bracketedPasteStart + text + bracketedPasteEnd
		}
	}
	if _, err := pty.Write([]byte(text)); err != nil {
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
	var meta paneMeta
	if p.Resolved {
		palette, verr := paletteFromParams(p.Palette)
		if verr != nil {
			return nil, verr
		}
		content, meta = pty.CaptureContentResolvedMeta(scrollback, palette)
	} else {
		content, meta = pty.CaptureContentMeta(scrollback, ansi)
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
	out := map[string]any{
		"type":         "pane_content",
		"content":      content,
		"source":       source,
		"styled":       ansi,
		"resolved":     p.Resolved,
		"history_rows": meta.HistoryRows,
		"revision":     meta.Revision,
	}
	// A revision counts from 0 again when the daemon restarts, so it is a
	// cache key only beside the daemon start it came from.
	if d.events != nil {
		out["boot_id"] = d.events.bootIdentity()
	}
	return out, nil
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
		// An old name is never followed to a kill. The caller hears the new
		// name and can kill by it on purpose.
		if renamed, ok := d.manager.ResolveSession(p.Session); ok && renamed != nil {
			return nil, hintedVerbError(ErrVerbSessionNotFound, RenamedSessionMessage(p.Session, renamed.Name()), &VerbHint{
				Param:   "session",
				Command: "tuios kill-session " + renamed.Name(),
				Detail:  "The session has a new name. Kill it by the new name.",
			})
		}
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
	return map[string]any{"type": "session_name_set", "session": sess.Name(), "display_name": name}, nil
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
	return map[string]any{"type": "session_accent_set", "session": sess.Name(), "accent": accent}, nil
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
