package session

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
	"github.com/google/uuid"
	"tuios.dev/vt"
)

const routedVerbTimeout = 10 * time.Second

func decodeParams(params json.RawMessage, v any) *verbError {
	if len(params) == 0 {
		return nil
	}
	if err := jsonx.Unmarshal(params, v); err != nil {
		return hintedVerbError(ErrVerbInvalidParams, "could not decode params: "+err.Error(), &VerbHint{
			Verb:   "list-verbs",
			Detail: "Call list-verbs to get this verb's parameter schema, including each parameter's type.",
		})
	}
	return nil
}

func (d *Daemon) resolveVerbSession(cs *connState, name string) (*Session, *verbError) {
	sess := d.findTargetSession(cs, name)
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

func mapResolveErr(err error, sess *Session) *verbError {
	msg := err.Error()

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
			Command: "tuios list-windows",
			Detail:  "the window target matched no window. A window is addressable by its id, a unique id prefix, the index list-windows prints, or its exact name.",
		}
		if strings.Contains(msg, "ambiguous window") {
			hint.Detail = "the window target matched more than one window, listed in the message. Pass the id, or give the windows different names with set-window --name."
		}
		if sess != nil {
			state := sess.GetState()
			hint.Command = "tuios list-windows " + sessionArg(sess)
			hint.Available = windowChoices(state)
			hint.DidYouMean = closestMatch(targetFromError(msg), windowTargets(state))
		}
		return hintedVerbError(ErrVerbWindowNotFound, msg, hint)
	}
}

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

func (d *Daemon) verbListClients(_ *connState, _ json.RawMessage) (any, *verbError) {
	return map[string]any{
		"type":    "client_list",
		"clients": d.listClients(),
	}, nil
}

func (d *Daemon) verbSessionInfo(cs *connState, params json.RawMessage) (any, *verbError) {
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
	hasClient := d.findTUIClient(sess.ID) != nil
	data := buildSessionInfoData(sess, sess.GetState(), hasClient, d.sessionHostFocus(sess.ID))
	data["type"] = "session_info"
	if data["window_size"] == "" {
		data["window_size"] = d.sessionWindowSize(sess)
	}
	return data, nil
}

func (d *Daemon) verbListWindows(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Rail    bool   `json:"rail"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()
	data := buildWindowListData(state)
	data["type"] = "window_list"
	if sh := sess.getShell(); sh != "" {
		data["shell"] = filepath.Base(sh)
	}
	addShellFacts(sess, data)
	addPaneMeta(sess, data)
	addResumable(d, state, data)
	if !p.Rail {
		dropRailPane(data)
	}
	return data, nil
}

func dropRailPane(data map[string]any) {
	windows, _ := data["windows"].([]map[string]any)
	kept := windows[:0]
	for _, w := range windows {
		if _, rail := w["rail"]; !rail {
			kept = append(kept, w)
		}
	}
	if len(kept) == len(windows) {
		return
	}
	data["windows"] = kept
	data["total"] = len(kept)
}

func addResumable(d *Daemon, state *SessionState, data map[string]any) {
	windows, _ := data["windows"].([]map[string]any)
	for i := range min(len(windows), len(state.Windows)) {
		if o, ok := d.resumeOfferFor(state.Name, state.Windows[i]); ok {
			windows[i]["agent_resumable"] = o.harness
		}
	}
}

func (d *Daemon) verbGetWindow(cs *connState, params json.RawMessage) (any, *verbError) {
	var p commonParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
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
	}
	data := windowStateToData(state, idx)
	if pty := sess.GetPTY(state.Windows[idx].PTYID); pty != nil {
		maps.Copy(data, shellFactsData(pty.ShellFacts()))
		addOnePaneMeta(pty, data)
	}
	data["type"] = "window"
	return data, nil
}

func (d *Daemon) verbNewWindow(cs *connState, params json.RawMessage) (any, *verbError) {
	var p newWindowParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	return d.openWindow(cs, &p, nil)
}

type newWindowParams struct {
	Session          string            `json:"session"`
	Name             string            `json:"name"`
	Workspace        int               `json:"workspace"`
	Cwd              string            `json:"cwd"`
	Focus            *bool             `json:"focus"`
	Command          []string          `json:"command"`
	Host             string            `json:"host"`
	Grants           []string          `json:"grants"`
	Env              map[string]string `json:"env"`
	CloseOnExit      bool              `json:"close_on_exit"`
	KeepOpen         bool              `json:"keep_open"`
	Wait             bool              `json:"wait"`
	Timeout          int               `json:"timeout"`
	Kind             string            `json:"kind"`
	URI              string            `json:"uri"`
	ShellIntegration string            `json:"shell_integration"`
}

func (d *Daemon) openWindow(cs *connState, p *newWindowParams, split *splitAsk) (any, *verbError) {
	if p.Kind != "" && !slices.Contains(WindowKinds, p.Kind) {
		return nil, invalidParam("kind", "kind is terminal, view, web or app")
	}
	if p.Kind != "" && p.Kind != "terminal" && p.URI == "" {
		return nil, invalidParam("uri", "a "+p.Kind+" window needs uri, what it shows")
	}
	if p.KeepOpen && p.CloseOnExit {
		return nil, invalidParam("keep_open", "keep_open and close_on_exit cannot be used together")
	}
	if p.Timeout < 0 {
		return nil, invalidParam("timeout", "timeout is milliseconds, 0 for no limit")
	}
	if p.ShellIntegration != "" && !slices.Contains(config.ShellIntegrationModes, p.ShellIntegration) {
		return nil, invalidParam("shell_integration", "shell_integration is auto or off")
	}
	if p.Wait && p.Host != "" && p.Host != "local" {
		return nil, invalidParam("wait", "wait reads the exit status, and a window on another machine does not report one")
	}
	grants, verr := d.launchGrants(cs, p.Grants)
	if verr != nil {
		return nil, verr
	}
	env, _, verr := callerEnv(cs, p.Env)
	if verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if p.Workspace < 0 {
		return nil, invalidParam("workspace", "workspace is a workspace number, e.g. 2. Omit it for the calling pane's workspace, else the one the session shows")
	}
	var placement *SplitPlacement
	var cols, rows int
	if split != nil {
		if placement, cols, rows, verr = d.splitTarget(cs, sess, p, split); verr != nil {
			return nil, verr
		}
	}
	if p.Workspace == 0 {
		p.Workspace = d.callerWorkspaceIn(cs, sess)
	}
	if verr := checkWindowHost(d, &p.Host); verr != nil {
		return nil, verr
	}
	if len(env) > 0 && p.Host != "" {
		return nil, invalidParam("env", "env names variables of this machine, so it cannot go to a window on another machine")
	}
	if p.Host == "" {
		if verr := checkWindowCwd(p.Cwd); verr != nil {
			return nil, verr
		}
	}
	if len(p.Command) > 0 && p.Command[0] == "" {
		return nil, invalidParam("command", "command[0] is the program to exec and cannot be empty")
	}
	focus := p.Focus == nil || *p.Focus

	exitStatus := newPopupExit()
	onExit := func(ptyID string) {
		if pty := sess.GetPTY(ptyID); pty != nil {
			exitStatus.recordPTY(pty)
		}
		if p.KeepOpen {
			return
		}
		d.notifyPTYClosed(sess.ID, ptyID)
		if p.CloseOnExit {
			d.closeWindowOfPTY(sess, ptyID, false)
		}
	}
	win, bverr, err := d.addWindowFor(cs, "new-window", sess, NewWindowOptions{
		keepOpen:    p.KeepOpen,
		closeOnExit: p.CloseOnExit,
		Title:       p.Name,
		Cwd:         p.Cwd,
		CallerCwd:   d.callerCwd(cs),
		Workspace:   p.Workspace,
		Focus:       focus,
		Command:     p.Command,
		Name:        p.Name,
		Host:        p.Host,
		Grants:      grants,
		Env:         env,
		Kind:        p.Kind,
		URI:         p.URI,
		Split:       placement,
		Cols:        cols,
		Rows:        rows,

		ShellIntegration: p.ShellIntegration,
	}, onExit)
	if bverr != nil {
		return nil, bverr
	}
	if errors.Is(err, errSplitNotTiled) {
		return nil, invalidParam("window", err.Error())
	}
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

	d.notePaneCreator(cs, win.ID)

	if win.Unplaced && d.findTUIClient(sess.ID) != nil {
		win.Unplaced = !d.awaitPlacement(sess, win.ID, win.PTYID, newWindowPlaceWait)
	}

	if p.Wait {
		pty := sess.GetPTY(win.PTYID)
		code, exited := d.waitPopupExit(sess, win, time.Duration(p.Timeout)*time.Millisecond, exitStatus)
		if !exited {
			if d.ctx.Err() != nil {
				return nil, newVerbError(ErrVerbInternal, "daemon is shutting down")
			}
			return nil, hintedVerbError(ErrVerbTimeout, "the window's program still runs after the wait ended", &VerbHint{
				Command: "tuios wait-for window-exit -w " + win.ID,
				Detail:  "The program still runs in the window. Wait for it with the command shown, or raise timeout.",
			})
		}
		pty.awaitExitNote()
		res := map[string]any{
			"type":      "window_exited",
			"window_id": win.ID,
			"name":      displayName,
			"workspace": win.Workspace,
			"pty_id":    win.PTYID,
			"exit_code": code,
		}
		sig := exitStatus.exitSignal()
		if pty != nil {
			sig = pty.ExitSignal()
		}
		if sig > 0 {
			res["signal"] = sig
		}
		if p.KeepOpen {
			res["keep_open"] = true
		}
		return res, nil
	}

	out := map[string]any{
		"type":      "window_created",
		"window_id": win.ID,
		"name":      displayName,
		"workspace": win.Workspace,
		"pty_id":    win.PTYID,
		"focused":   focus,
		"unplaced":  win.Unplaced,
		"host":      win.Host,
	}
	if p.KeepOpen {
		out["keep_open"] = true
	}
	if split != nil {
		d.splitDone(sess, out, placement, win.ID, split)
	}
	return out, nil
}

const newWindowPlaceWait = time.Second

const placeSettle = 250 * time.Millisecond

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
	settle := time.Now().Add(placeSettle)
	for pty != nil && time.Now().Before(settle) {
		if c, r := pty.Size(); c != cols || r != rows {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

func (d *Daemon) verbPopup(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session       string           `json:"session"`
		Name          string           `json:"name"`
		Cwd           string           `json:"cwd"`
		Width         string           `json:"width"`
		Height        string           `json:"height"`
		Dim           *config.PopupDim `json:"dim"`
		Command       []string         `json:"command"`
		Workspace     int              `json:"workspace"`
		Wait          bool             `json:"wait"`
		CaptureStdout bool             `json:"capture_stdout"`
		Timeout       int              `json:"timeout"`
		Scratch       bool             `json:"scratch"`
		ScratchName   string           `json:"scratch_name"`
		CwdFrom       string           `json:"cwd_from"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
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
	if p.Dim != nil && (p.Dim.Percent < 0 || p.Dim.Percent > config.PopupDimMax) {
		return nil, invalidParam("dim", "dim must be an integer from 0 to 90")
	}
	if p.Workspace < 0 {
		return nil, invalidParam("workspace", "workspace is a workspace number, e.g. 2. Omit it for the current one")
	}
	if p.CwdFrom != "" {
		if win, ok := findWindowState(sess.GetState(), p.CwdFrom); ok && win.Host == "" {
			if cwd := sess.windowCwd(p.CwdFrom); cwd != "" {
				p.Cwd = cwd
			}
		}
	}
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

	if p.Scratch {
		want := WindowState{ScratchName: p.ScratchName}.ScratchKey()
		for _, w := range sess.GetState().Windows {
			if w.Scratch && w.ScratchKey() == want {
				return nil, invalidParam("scratch", ErrScratchExists.Error())
			}
		}
	}

	exitStatus := newPopupExit()
	onExit := func(ptyID string) {
		d.notifyPTYClosed(sess.ID, ptyID)
		if pty := sess.GetPTY(ptyID); pty != nil {
			if code, ok := pty.ExitStatus(); ok {
				exitStatus.record(code)
			}
		}
		d.closeWindowOfPTY(sess, ptyID, true)
	}
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
		PopupDim:    p.Dim,
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
		release := d.pipes.hold("a popup in session " + sess.Name() + " is capturing its command's output")
		go func() {
			<-capture.done
			release()
		}()
	}
	win, bverr, err := d.addWindowFor(cs, "popup", sess, opts, onExit)
	if bverr != nil {
		if opts.stdout != nil {
			_ = opts.stdout.Close()
		}
		return nil, bverr
	}
	if opts.stdout != nil {
		_ = opts.stdout.Close()
	}
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
	if p.Wait {
		if popupBeforeWaitHook != nil {
			popupBeforeWaitHook(sess, win)
		}
		code, exited := d.waitPopupExit(sess, win, time.Duration(p.Timeout)*time.Millisecond, exitStatus)
		if !exited {
			return nil, hintedVerbError(ErrVerbTimeout, "the popup was still open when the wait ended", &VerbHint{
				Command: "tuios wait-for window-exit -w " + win.ID,
				Detail:  "The popup is still on the screen and its command still runs. Wait for it with the command shown, or raise timeout.",
			})
		}
		if p.Scratch {
			_, _ = sess.CloseDaemonWindow(win.ID, "the daemon, as the scratch popup's command ended")
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
		"width":     cmp.Or(win.PopupWidth, PopupDefaultWidth),
		"height":    cmp.Or(win.PopupHeight, PopupDefaultHeight),
	}, nil
}

func newWindowErr(err error, sess *Session, ws int) *verbError {
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

func (d *Daemon) verbCloseWindow(cs *connState, params json.RawMessage) (any, *verbError) {
	var p commonParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	var args []string
	if p.Window != "" {
		args = []string{p.Window}
	}

	onExit := func(ptyID string) { d.notifyPTYClosed(sess.ID, ptyID) }
	by := d.askerOf(cs) + " (close-window)"
	if _, err := d.executeDaemonCommandEnv(sess, "CloseWindow", args, NewWindowOptions{closeAskedBy: by}, onExit); err != nil {
		return nil, mapResolveErr(err, sess)
	}
	return map[string]any{"type": "ok"}, nil
}

func (d *Daemon) closeWindowOfPTY(sess *Session, ptyID string, popupOnly bool) {
	d.goTracked(func() {
		if holder := d.sessionHoldingPTY(ptyID); holder != nil {
			sess = holder
		}
		for _, w := range sess.GetState().Windows {
			if w.PTYID == ptyID {
				if popupOnly && !w.Popup {
					return
				}
				_, _ = sess.CloseDaemonWindow(w.ID, "the daemon, as the pane's process exited")
				return
			}
		}
	})
}

func (d *Daemon) verbCloseWorkspace(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string `json:"session"`
		Workspace int    `json:"workspace"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
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
	by := d.askerOf(cs) + " (close-workspace)"
	for _, id := range ids {
		if _, err := sess.CloseDaemonWindow(id, by); err != nil {
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
		Session      string `json:"session"`
		Window       string `json:"window"`
		Keys         string `json:"keys"`
		Literal      bool   `json:"literal"`
		Raw          bool   `json:"raw"`
		Repeat       int    `json:"repeat"`
		AllowBlocked bool   `json:"allow_blocked"`
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
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	var parsed []sendKey
	if !p.Literal && !p.Raw {
		var err error
		if parsed, err = parseSendKeys(p.Keys, repeat); err != nil {
			return nil, sendKeysParseError(err, sess, p.Window)
		}
	}

	if paneTypesRaw(cs) && hasPrefixKey(parsed) {
		return nil, hintedVerbError(ErrVerbForbidden, "send-keys with PREFIX is refused from a pane: a pane's keys go to a pane's terminal, never to the window manager", &VerbHint{
			Param:   "keys",
			Verb:    "focus-window",
			Command: "tuios focus-window <window>",
			Detail:  "Nothing was sent. Use the verbs for window-manager actions: focus-window, new-window, close-window, split-window or set-layout. Then send keys without PREFIX, with -w naming the pane.",
		})
	}
	if p.Window == "" && paneTypesRaw(cs) {
		if id, err := focusedWindowID(sess.GetState()); err == nil {
			p.Window = id
		}
	}
	if verr := d.recheckTyping(cs, "send-keys", sess, p.Window); verr != nil {
		return nil, verr
	}
	if verr := d.recheckBlocked(cs, "send-keys", sess, p.Window, p.AllowBlocked); verr != nil {
		return nil, verr
	}

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

func windowDisplayName(w WindowState) string {
	if w.CustomName != "" {
		return w.CustomName
	}
	return w.Title
}

func (d *Daemon) verbSendText(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session      string `json:"session"`
		Window       string `json:"window"`
		Text         string `json:"text"`
		Paste        bool   `json:"paste"`
		Submit       bool   `json:"submit"`
		AllowBlocked bool   `json:"allow_blocked"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	pty, err := d.resolvePTYForTarget(sess, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	if verr := d.recheckTyping(cs, "send-text", sess, p.Window); verr != nil {
		return nil, verr
	}
	if verr := d.recheckBlocked(cs, "send-text", sess, p.Window, p.AllowBlocked); verr != nil {
		return nil, verr
	}
	if p.Submit {
		window := p.Window
		if window == "" {
			window, _ = focusedWindowID(sess.GetState())
		} else if idx, err := findWindowStateIndex(sess.GetState().Windows, window); err == nil {
			window = sess.GetState().Windows[idx].ID
		}
		ctx := withSubmitGuard(d.ctx, func() *verbError {
			if verr := d.recheckTyping(cs, "send-text", sess, window); verr != nil {
				return verr
			}
			return d.recheckBlocked(cs, "send-text", sess, window, p.AllowBlocked)
		})
		if _, err := submitPrompt(ctx, pty, p.Text, d.inputProfileFor(sess, window)); err != nil {
			return nil, ptyWriteError(err)
		}
		return map[string]any{"type": "ok"}, nil
	}
	text := p.Text
	if p.Paste {
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

func paneBusyError(err error) *verbError {
	return newVerbError(ErrVerbBusy, err.Error()+". Nothing was typed. Try again when the pane reads its input.")
}

func promptWriteError(err error) *verbError {
	if ve := (*verbError)(nil); errors.As(err, &ve) {
		return ve
	}
	if errors.Is(err, errPaneInputBusy) {
		return paneBusyError(err)
	}
	return newVerbError(ErrVerbInternal, err.Error())
}

func ptyWriteError(err error) *verbError {
	if ve := (*verbError)(nil); errors.As(err, &ve) {
		return ve
	}
	if errors.Is(err, errPaneInputBusy) {
		return paneBusyError(err)
	}
	if ended := (*remotePaneEndedError)(nil); errors.As(err, &ended) {
		return paneEndedError(ended)
	}
	if errors.Is(err, errPaneReconnecting) {
		return hintedVerbError(ErrVerbHostUnreachable, err.Error(), &VerbHint{
			Command: "tuios list-windows",
			Detail:  "Nothing was typed. The window's process is still running on the other machine; host_link and host_link_until in list-windows say until when it waits for the link.",
		})
	}
	return newVerbError(ErrVerbInternal, err.Error())
}

func (d *Daemon) verbCapturePane(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session    string   `json:"session"`
		Window     string   `json:"window"`
		Source     string   `json:"source"`
		Styled     bool     `json:"styled"`
		Scrollback bool     `json:"scrollback"`
		ANSI       bool     `json:"ansi"`
		Lines      int      `json:"lines"`
		Start      int      `json:"start"`
		End        int      `json:"end"`
		Resolved   bool     `json:"resolved"`
		Palette    []string `json:"palette"`
		Unwrap     bool     `json:"unwrap"`
		Trim       bool     `json:"trim"`
		Commands   int      `json:"commands"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := validateCaptureSource(p.Source); verr != nil {
		return nil, verr
	}
	if p.Commands < 0 {
		return nil, invalidParam("commands", "commands is how many of the last shell commands to capture, 1 or more")
	}
	if p.Commands > 0 {
		switch {
		case p.Source != "" && p.Source != "recent":
			return nil, invalidParam("commands", "commands reads the scrollback, so it takes no source other than recent")
		case p.Start > 0 || p.End > 0:
			return nil, invalidParam("commands", "commands takes no start or end. Use lines for the most lines to keep")
		case p.Unwrap:
			return nil, invalidParam("commands", "commands takes no unwrap")
		}
		p.Scrollback = true
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	win, pty, verr := d.resolveWindowPTY(sess, p.Window)
	if verr != nil {
		return nil, verr
	}

	if win.Kind == WindowKindTML && p.Source != captureLastCommand {
		if out, verr := d.captureTMLPane(win, pty, p.Styled || p.ANSI || p.Resolved, p.Resolved, p.Palette, p.Trim, p.Start, p.End, p.Lines); verr != nil || out != nil {
			return out, verr
		}
	}

	if p.Source == captureLastCommand {
		return captureLastCommandOutput(sess, pty, p.Window, p.Styled || p.ANSI || p.Resolved, p.Unwrap, p.Trim, p.Start, p.End, p.Lines)
	}

	scrollback := p.Scrollback || p.Source == "recent"
	ansi := p.Styled || p.ANSI || p.Resolved
	var palette [16]color.Color
	if p.Resolved {
		var verr *verbError
		if palette, verr = paletteFromParams(p.Palette); verr != nil {
			return nil, verr
		}
	}
	var content string
	var meta paneMeta
	var commandMarks *bool
	var commandList []map[string]any
	commandsAvailable := 0
	omitted := -1
	if p.Commands > 0 {
		ceiling := p.Lines
		if ceiling <= 0 {
			ceiling = defaultCommandCeiling
		}
		rows, blocks, m, ok := pty.captureCommandRows(ansi)
		commandMarks = &ok
		if ok {
			meta, commandsAvailable = m, len(blocks)
			content, omitted, commandList = captureCommands(rows, blocks, p.Commands, ceiling)
			if p.Resolved {
				content = ResolveSGR(content, palette)
			}
		} else {
			p.Lines = ceiling
		}
	}
	byCommands := commandMarks != nil && *commandMarks
	switch {
	case byCommands:
	case p.Resolved:
		content, meta = pty.CaptureContentResolvedMeta(scrollback, palette, p.Unwrap)
	default:
		content, meta = pty.CaptureContentMeta(scrollback, ansi, p.Unwrap)
	}
	if p.Trim {
		content = trimCapture(content)
	}
	if !byCommands {
		if p.Lines > 0 && p.Start <= 0 && p.End <= 0 {
			omitted = max(captureLineCount(content)-p.Lines, 0)
		}
		content = sliceCaptureLines(content, p.Start, p.End, p.Lines)
	}

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
	if p.Unwrap {
		out["unwrapped"] = true
	}
	if p.Trim {
		out["trimmed"] = true
	}
	if omitted >= 0 {
		out["lines_omitted"] = omitted
	}
	if commandMarks != nil {
		out["command_marks"] = *commandMarks
		if *commandMarks {
			out["commands"] = commandList
			out["commands_available"] = commandsAvailable
		}
	}
	if d.events != nil {
		out["boot_id"] = d.events.bootIdentity()
	}
	return out, nil
}

func (d *Daemon) verbResize(cs *connState, params json.RawMessage) (any, *verbError) {
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
	sess, verr := d.resolveVerbSession(cs, p.Session)
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

func (d *Daemon) verbSetSessionName(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Name    string `json:"name"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	name := strings.TrimSpace(p.Name)
	if err := sess.SetDisplayName(name); err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not set session name: "+err.Error())
	}
	return map[string]any{"type": "session_name_set", "session": sess.Name(), "display_name": name}, nil
}

func (d *Daemon) verbSetSessionAccent(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Accent  string `json:"accent"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	accent := strings.TrimSpace(p.Accent)
	if err := sess.SetAccent(accent); err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not set session accent: "+err.Error())
	}
	return map[string]any{"type": "session_accent_set", "session": sess.Name(), "accent": accent}, nil
}

func (d *Daemon) verbSetWorkspaceName(cs *connState, params json.RawMessage) (any, *verbError) {
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
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	name := strings.TrimSpace(p.Name)
	if err := sess.SetDaemonWorkspaceName(p.Workspace, name); err != nil {
		return nil, invalidParam("workspace", err.Error())
	}
	return map[string]any{"type": "workspace_name_set", "workspace": p.Workspace, "name": name}, nil
}

func (d *Daemon) verbSetWorkspaceOrder(cs *connState, params json.RawMessage) (any, *verbError) {
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
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	moved, err := sess.RenumberWorkspaces(p.Order)
	if err != nil {
		return nil, invalidParam("order", err.Error())
	}
	renumbered := make(map[string]int, len(moved))
	for from, to := range moved {
		renumbered[strconv.Itoa(from)] = to
	}
	return map[string]any{"type": "workspace_order_set", "workspace_order": nil, "renumbered": renumbered}, nil
}

func (d *Daemon) verbSetAgentState(cs *connState, params json.RawMessage) (any, *verbError) {
	res, verr := d.setAgentState(cs, params, false)
	d.noteSharedServerHook(params, verr)
	return res, verr
}

func (d *Daemon) setAgentState(cs *connState, params json.RawMessage, ownPane bool) (any, *verbError) {
	if !d.agentsEnabled() {
		return nil, agentsDisabledError()
	}
	var p struct {
		Session        string               `json:"session"`
		Window         string               `json:"window"`
		State          string               `json:"state"`
		Message        string               `json:"message"`
		Source         string               `json:"source"`
		Harness        string               `json:"harness"`
		Kind           string               `json:"kind"`
		AgentSessionID string               `json:"agent_session_id"`
		TranscriptPath string               `json:"transcript_path"`
		TranscriptLeaf *string              `json:"transcript_leaf"`
		IfState        string               `json:"if_state"`
		SubagentID     string               `json:"subagent_id"`
		IfSubagentID   string               `json:"if_subagent_id"`
		HarnessPID     int                  `json:"harness_pid"`
		SessionStart   bool                 `json:"session_start"`
		Notification   bool                 `json:"notification"`
		AgentCwd       string               `json:"agent_cwd"`
		Activity       *AgentActivityReport `json:"activity"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := checkActivityReport(p.Activity, activityEvents); verr != nil {
		return nil, verr
	}
	if verr := d.checkTranscriptSelection(p.Harness, p.TranscriptPath, p.TranscriptLeaf); verr != nil {
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
	source, ok := ParseAgentSource(p.Source)
	if !ok {
		return nil, hintedVerbError(ErrVerbInvalidParams, "unknown agent state source "+echoName(p.Source), &VerbHint{
			Param:      "source",
			DidYouMean: closestMatch(p.Source, AgentSourceNames),
			Available:  AgentSourceNames,
			Detail:     "source says where the state came from and decides which of two competing reports wins. Omit it to report for yourself.",
		})
	}
	if p.Kind == AgentKindUsageLimit {
		if state != AgentStateErrored && state != AgentStateLimitReached {
			return nil, invalidParam("kind", "kind usage_limit describes a limit_reached or errored state, and this report is "+state.Name())
		}
		state, p.Kind = AgentStateLimitReached, ""
	}
	if p.Kind != "" && p.Kind != AgentKindUsageLimit {
		if p.Kind != harness.PromptKindApproval && p.Kind != harness.PromptKindQuestion && p.Kind != harness.PromptKindAuth {
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
	if p.SubagentID != "" {
		if !integration.ValidSubagentID(p.SubagentID) {
			return nil, invalidParam("subagent_id", "subagent_id is 1 to "+strconv.Itoa(integration.SubagentIDMax)+" letters, digits, '_', '.', ':', '@' or '-'")
		}
		if state != AgentStateNeedsInput {
			return nil, invalidParam("subagent_id", "subagent_id names the subagent a needs_input state waits on, and this report is "+state.Name())
		}
	}
	if p.IfSubagentID != "" && !integration.ValidSubagentID(p.IfSubagentID) {
		return nil, invalidParam("if_subagent_id", "if_subagent_id is 1 to "+strconv.Itoa(integration.SubagentIDMax)+" letters, digits, '_', '.', ':', '@' or '-'")
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
	route, routed := d.routeSharedServerReport(cs, p.Session, p.Window, p.Harness, p.AgentSessionID, p.TranscriptPath)
	if routed {
		p.Session, p.Window = route.sess.Name(), route.windowID
	}
	sess, target, verr := d.reportTarget(cs, p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	var treeWindow, previousSession string
	if p.AgentSessionID != "" && d.transcriptIsTree(p.Harness) {
		st := sess.GetState()
		if idx, err := findWindowStateIndex(st.Windows, target); err == nil {
			treeWindow, previousSession = st.Windows[idx].ID, st.Windows[idx].AgentSessionID
		}
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
		Subagent:   p.SubagentID,
		IfSubagent: p.IfSubagentID,

		HarnessIsCaller: p.AgentSessionID != "" && callerRunsHarness(cs, p.HarnessPID),

		SessionStart: p.SessionStart,
		SharedServer: d.agentMatcher.registry.SharedServer(p.Harness),

		event: p.Notification,
	}
	report.foreignPane = d.harnessOutsidePane(cs, sess, target, p.HarnessPID)
	report.harnessGone = p.AgentSessionID != "" && d.harnessLeftPane(cs, sess, target, p.HarnessPID)
	report.fromPane = ownPane || d.reportFromOwnPane(cs, sess, target)
	if routed {
		placeRoutedReport(&report, route)
	}
	var windowID, guard string
	if p.Activity != nil {
		windowID, guard = sess.agentReportGuard(target, report)
	}
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
	recorded := false
	if p.Activity != nil && windowID != "" && guard == "" && !refusedAsForeign(reason) {
		d.recordAgentActivity(sess, windowID, p.Activity, effective)
		recorded = true
	} else if created {
		d.activity.forgetIfEmpty(sess.ID, windowID)
	}
	if p.AgentCwd != "" && p.AgentSessionID != "" && !refusedAsForeign(reason) {
		d.noteReportedAgentDir(sess, target, p.AgentSessionID, p.AgentCwd)
	}
	if refusedAsForeign(reason) {
		debugLog("[DEBUG] set-agent-state: %s refused session %q of %s", reason, p.AgentSessionID, p.Harness)
	}
	if applied && p.AgentSessionID != "" && report.State != AgentStateNone {
		d.attention.closeResumesOf(p.Harness, p.AgentSessionID)
	}
	transcriptRefused := ""
	switch {
	case applied && p.TranscriptPath != "":
		transcriptRefused = d.joinReportedTranscript(cs, sess, target, p.Harness, p.TranscriptPath, "", p.TranscriptLeaf)
	case applied && cs != nil && p.TranscriptPath == "" && p.AgentSessionID != "":
		sid := strings.TrimSpace(p.AgentSessionID)
		if path := d.storeTranscriptPath(p.Harness); path != "" {
			transcriptRefused = d.joinReportedTranscript(cs, sess, target, p.Harness, path, sid, nil)
		} else if path := d.sessionTranscriptPath(p.Harness, sid); path != "" {
			transcriptRefused = d.joinReportedTranscript(cs, sess, target, p.Harness, path, "", nil)
		}
	}
	if applied && treeWindow != "" && previousSession != p.AgentSessionID && (p.TranscriptPath == "" || transcriptRefused != "") {
		sess.DropAgentTranscript(treeWindow)
		sess.emit(SessionEvent{Type: EventTranscript, Window: treeWindow})
	}
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
	if transcriptRefused != "" {
		out["transcript_refused"] = transcriptRefused
	}
	if routed {
		out["session"], out["window"] = sess.Name(), route.windowID
	}
	return out, nil
}

func (d *Daemon) reportTarget(cs *connState, sessionName, window string) (*Session, string, *verbError) {
	var own *paneAuth
	if window == "" {
		if pa := d.paneAuthority(cs); pa != nil && !pa.hosted && pa.window != "" && pa.window != unplacedWindow && pa.session != "" {
			own = pa
			if sessionName == "" {
				sessionName = pa.session
			}
		}
	}
	sess, verr := d.resolveVerbSession(cs, sessionName)
	if verr != nil {
		return nil, "", verr
	}
	target := window
	if target == "" && own != nil && sess.Name() == own.session {
		target = own.window
	}
	if target == "" {
		id, err := focusedWindowID(sess.GetState())
		if err != nil {
			return nil, "", mapResolveErr(err, sess)
		}
		target = id
	}
	return sess, target, nil
}

func (d *Daemon) reportFromOwnPane(cs *connState, sess *Session, target string) bool {
	if cs == nil {
		return false
	}
	w, ok := findWindowStateByTarget(sess, target)
	if !ok {
		return false
	}
	if cs.hostedWindow != "" {
		return cs.hostedWindow == w.ID
	}
	pa := d.paneAuthority(cs)
	return pa != nil && pa.plugin == "" && !pa.hosted && pa.window == w.ID
}

func findWindowStateByTarget(sess *Session, target string) (WindowState, bool) {
	wins := sess.GetState().Windows
	idx, err := findWindowStateIndex(wins, target)
	if err != nil {
		return WindowState{}, false
	}
	return wins[idx], true
}

const (
	agentActivityRate  = 10.0
	agentActivityBurst = float64(subagentsMax)
)

func (d *Daemon) verbReportAgentActivity(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session        string               `json:"session"`
		Window         string               `json:"window"`
		Harness        string               `json:"harness"`
		AgentSessionID string               `json:"agent_session_id"`
		HarnessPID     int                  `json:"harness_pid"`
		Activity       *AgentActivityReport `json:"activity"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Activity == nil {
		return nil, invalidParam("activity", "activity is required: the hook event to record, with its event")
	}
	if verr := checkActivityReport(p.Activity, reportActivityEvents); verr != nil {
		return nil, verr
	}
	route, routed := d.routeSharedServerReport(cs, p.Session, p.Window, p.Harness, p.AgentSessionID, "")
	if routed {
		p.Session, p.Window = route.sess.Name(), route.windowID
	}
	sess, target, verr := d.reportTarget(cs, p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	guarded := AgentReport{
		Harness:      p.Harness,
		SessionID:    p.AgentSessionID,
		HarnessPID:   p.HarnessPID,
		SharedServer: d.agentMatcher.registry.SharedServer(p.Harness),
		foreignPane:  d.harnessOutsidePane(cs, sess, target, p.HarnessPID),
	}
	if routed {
		placeRoutedReport(&guarded, route)
	}
	windowID, state, reason, err := sess.activityReportGuard(target, guarded)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	if !d.activityReports.take(windowID, time.Now(), agentActivityRate, agentActivityBurst) {
		return nil, hintedVerbError(ErrVerbRateLimited, "this pane reports activity too fast", &VerbHint{
			Param:  "activity",
			Detail: fmt.Sprintf("a pane may report a burst of %d events, then %g a second. Nothing was recorded; report again later.", int(agentActivityBurst), agentActivityRate),
		})
	}
	recorded := false
	if reason == "" {
		recorded = d.recordAgentActivity(sess, windowID, p.Activity, state)
	}
	out := map[string]any{
		"type":      "agent_activity_reported",
		"window_id": windowID,
		"state":     state.Name(),
		"recorded":  recorded,
		"subagents": sess.subagentCount(windowID),
	}
	if reason != "" {
		out["reason"] = reason
	}
	return out, nil
}

func (d *Daemon) verbSetAgentSession(cs *connState, params json.RawMessage) (any, *verbError) {
	if !d.agentsEnabled() {
		return nil, agentsDisabledError()
	}
	var p struct {
		Session        string  `json:"session"`
		Window         string  `json:"window"`
		Harness        string  `json:"harness"`
		AgentSessionID string  `json:"agent_session_id"`
		HarnessPID     int     `json:"harness_pid"`
		TranscriptPath string  `json:"transcript_path"`
		TranscriptLeaf *string `json:"transcript_leaf"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	harnessID := strings.TrimSpace(p.Harness)
	if verr := d.checkTranscriptSelection(harnessID, p.TranscriptPath, p.TranscriptLeaf); verr != nil {
		return nil, verr
	}
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
	var treeWindow, previousSession string
	if d.transcriptIsTree(harnessID) {
		st := sess.GetState()
		if idx, err := findWindowStateIndex(st.Windows, target); err == nil {
			treeWindow, previousSession = st.Windows[idx].ID, st.Windows[idx].AgentSessionID
		}
	}
	stored, applied, reason, err := sess.applyAgentSession(target, AgentSessionReport{
		Harness:     harnessID,
		SessionID:   sid,
		HarnessPID:  p.HarnessPID,
		foreignPane: d.harnessOutsidePane(cs, sess, target, p.HarnessPID),

		HarnessIsCaller: callerRunsHarness(cs, p.HarnessPID),
		TranscriptOnly:  p.TranscriptPath != "" || p.TranscriptLeaf != nil,
		harnessGone:     d.harnessLeftPane(cs, sess, target, p.HarnessPID),
	})
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	if applied {
		d.attention.closeResumesOf(harnessID, stored)
	}
	out := map[string]any{
		"type":             "agent_session_set",
		"agent_session_id": stored,
		"applied":          applied,
	}
	if reason != "" {
		out["reason"] = reason
	}
	transcriptRefused := ""
	if applied && p.TranscriptPath != "" {
		transcriptRefused = d.joinReportedTranscript(cs, sess, target, harnessID, p.TranscriptPath, "", p.TranscriptLeaf)
		if transcriptRefused != "" {
			out["transcript_refused"] = transcriptRefused
		}
	}
	if applied && treeWindow != "" && previousSession != stored && (p.TranscriptPath == "" || transcriptRefused != "") {
		sess.DropAgentTranscript(treeWindow)
		sess.emit(SessionEvent{Type: EventTranscript, Window: treeWindow})
	}
	return out, nil
}

func (d *Daemon) checkTranscriptSelection(harnessID, path string, leaf *string) *verbError {
	if leaf == nil {
		return nil
	}
	if path == "" || !d.transcriptIsTree(strings.TrimSpace(harnessID)) {
		return invalidParam("transcript_leaf", "a selected leaf needs a harness whose transcript is a tree, and its transcript_path")
	}
	if len(*leaf) > 128 {
		return invalidParam("transcript_leaf", "the selected entry id exceeds 128 bytes")
	}
	for _, c := range *leaf {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return invalidParam("transcript_leaf", "the selected entry id must contain only letters, digits, '-' or '_'")
		}
	}
	return nil
}

const maxAgentSessionIDLen = 256

var agentKindNames = []string{harness.PromptKindApproval, harness.PromptKindQuestion, harness.PromptKindAuth, AgentKindUsageLimit}

func (d *Daemon) transcriptIsTree(harnessID string) bool {
	reg := d.agentMatcher.registry
	if harnessID == "" || reg == nil {
		return false
	}
	tr := reg.TranscriptFor(harnessID)
	return tr != nil && transcriptview.IsTree(tr.Reader)
}

func (d *Daemon) joinReportedTranscript(cs *connState, sess *Session, target, harnessID, path, session string, leaf *string) string {
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return ""
	}
	w := state.Windows[idx]
	if harnessID == "" {
		harnessID = w.AgentHarness
	}
	reg := d.agentMatcher.registry
	if harnessID == "" || reg == nil || reg.TranscriptFor(harnessID) == nil {
		return ""
	}
	if pa := d.paneAuthority(cs); pa != nil && (pa.hosted || pa.window != w.ID || !paneInSession(pa, sess)) {
		why := "a pane may name a transcript only for its own pane"
		LogBasic("transcript_path refused for window %s: %s", shortWindowID(w.ID), why)
		return why
	}
	if cs != nil && (cs.viaLink || cs.paneOnly) {
		why := "a transcript is named only from this machine"
		LogBasic("transcript_path refused for window %s: %s", shortWindowID(w.ID), why)
		return why
	}
	resolved, why := transcriptPathAllowed(reg.TranscriptFor(harnessID), path, peerGetenv(cs))
	if why != "" {
		LogBasic("transcript_path refused for window %s: %s", shortWindowID(w.ID), why)
		return why
	}
	tr := reg.TranscriptFor(harnessID)
	if tr.BySession {
		if !validReportedSession(session) {
			return "the session id is not one a store names"
		}
		sess.JoinAgentSession(w.ID, harnessID, tr.Reader, resolved, session)
		return ""
	}
	_ = sess.joinAgentTranscriptLeaf(w.ID, harnessID, tr.Reader, resolved, true, leaf)
	return ""
}

func validReportedSession(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func (d *Daemon) storeTranscriptPath(harnessID string) string {
	reg := d.agentMatcher.registry
	if reg == nil || harnessID == "" {
		return ""
	}
	if tr := reg.TranscriptFor(harnessID); tr != nil {
		return tr.StorePath()
	}
	return ""
}

func (d *Daemon) sessionTranscriptPath(harnessID, sessionID string) string {
	reg := d.agentMatcher.registry
	if reg == nil || harnessID == "" {
		return ""
	}
	tr := reg.TranscriptFor(harnessID)
	if tr == nil {
		return ""
	}
	path := tr.SessionPath(strings.TrimSpace(sessionID))
	if path == "" {
		return ""
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return path
}

func paneInSession(pa *paneAuth, sess *Session) bool {
	if pa.sessionID != "" {
		return pa.sessionID == sess.ID
	}
	return pa.session == sess.Name()
}

func transcriptPathAllowed(tr *harness.Transcript, path string, peer func(string) string) (string, string) {
	roots := []string{tr.Root()}
	if peer != nil {
		if r := tr.RootIn(peer); r != roots[0] {
			roots = append(roots, r)
		}
	}
	if !slices.ContainsFunc(roots, func(r string) bool { return r != "" }) {
		return "", "the harness has no transcript folder"
	}
	if !filepath.IsAbs(path) {
		return "", "the transcript path is not absolute"
	}
	resolved := resolveExisting(filepath.Clean(path))
	realRoot := ""
	for i, root := range roots {
		if root == "" {
			continue
		}
		r := resolveExisting(root)
		if i > 0 && filepath.Base(r) != filepath.Base(root) {
			continue
		}
		rel, err := filepath.Rel(r, resolved)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			realRoot = r
			break
		}
	}
	if realRoot == "" {
		return "", "the transcript is not in the harness's transcript folder"
	}
	if !tr.MatchesGlob(realRoot, resolved) {
		return "", "the transcript file name does not match the harness's pattern"
	}
	if info, err := os.Lstat(resolved); err == nil && !info.Mode().IsRegular() {
		return "", "the transcript is not a regular file"
	}
	return resolved, ""
}

func peerGetenv(cs *connState) func(string) string {
	if cs == nil || !cs.peerEnvOK {
		return nil
	}
	return func(name string) string {
		v, _ := cs.peerEnv.Get(name)
		return v
	}
}

func resolveExisting(path string) string {
	rest := ""
	for p := path; ; {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return path
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

func (d *Daemon) verbGetAgentState(cs *connState, params json.RawMessage) (any, *verbError) {
	var p commonParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
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
		"type":             "agent_state",
		"window_id":        w.ID,
		"state":            w.ShownAgentState().Name(),
		"reason":           w.AgentReason,
		"message":          w.AgentMessage,
		"agent_state_at":   w.AgentStateAt,
		"source":           claim.source.Name(),
		"harness_id":       claim.harness,
		"identity":         string(claim.identity),
		"confidence":       claim.identity.confidence(),
		"evidence_age_ms":  evidenceAgeMS(sess.evidenceStamp(w, claim), d.evidenceNow()),
		"needs_you":        w.AgentState.NeedsYou(),
		"quiet":            w.AgentQuiet,
		"activity":         w.ShownAgentState().Activity(),
		"ready":            d.agentReady(w, agentRestStates),
		"blocked_by":       agentBlockedBy(w),
		"waits_on":         agentWaitsOn(w),
		"agent_session_id": w.AgentSessionID,
		"meta":             agentMetaMap(w.AgentMeta, time.Now().UnixNano()),
		"queued":           w.AgentQueued,
		"draft_held":       w.AgentDraftHeld,
		"settled":          w.AgentSettled,
		"subagents":        w.AgentSubagents,
		"subagent_list":    subagentListOut(w.AgentSubagentList),
		"program_status":   programStatusList(w.ProgramStatus),
	}, nil
}

func (d *Daemon) verbExplainAgentDetect(cs *connState, params json.RawMessage) (any, *verbError) {
	var p commonParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
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
	source := ""
	if isAgentWindow(w) {
		source = claim.source.Name()
	}

	out := map[string]any{
		"type":            "agent_detect",
		"window_id":       w.ID,
		"state":           w.AgentState.Name(),
		"source":          source,
		"harness_id":      w.AgentHarness,
		"auto_detected":   claim.auto,
		"identity":        string(claim.identity),
		"confidence":      claim.identity.confidence(),
		"evidence_age_ms": evidenceAgeMS(sess.evidenceStamp(w, claim), d.evidenceNow()),
		"needs_you":       w.AgentState.NeedsYou(),
		"activity":        w.AgentState.Activity(),
		"running":         false,
		"matched":         false,
	}
	var evidence []string
	if claim.identity == identityReport && claim.harness != "" {
		evidence = append(evidence, "The harness "+claim.harness+" named itself in a report. That is certain.")
	}

	info, running := d.foregroundResolver(sess)(w.PTYID)
	out["running"] = running
	if !running {
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

func describeRule(det detection) string {
	if det.harness != "" {
		return "the manifest " + det.harness + " on " + det.rule + "."
	}
	return "the name list on " + strings.TrimPrefix(det.rule, "name ") + "."
}

func (d *Daemon) verbExplainAgentScreen(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		commonParams
		Harness string `json:"harness"`
		Lines   int    `json:"lines"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
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

	lines := p.Lines
	if lines <= 0 && m != nil {
		lines = m.Screen.Lines
	}
	if lines <= 0 {
		lines = harness.DefaultScreenLines
	}

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
		return out, nil
	}
	out["enabled"] = m.Screen.Enabled
	source, replaced := m.Source()
	out["manifest_source"] = source
	out["replaces_bundled"] = replaced

	matchedState, rule, reports := reg.Explain(hid, tail)
	out["rules"] = reports
	out["rule"] = rule
	out["matched"] = rule >= 0
	out["rule_state"] = matchedState

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

func captureLineCount(content string) int {
	split := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	n := len(split)
	for n > 0 && strings.TrimSpace(split[n-1]) == "" {
		n--
	}
	return n
}

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

func paneEndedError(e *remotePaneEndedError) *verbError {
	return hintedVerbError(ErrVerbPaneEnded, e.Error(), &VerbHint{
		Command: "tuios list-windows",
		Detail:  "Nothing was sent. The window stays until it is closed; list-windows shows it as ended, with host_ended saying why. Close it, or open a new window on " + e.host + ".",
	})
}
