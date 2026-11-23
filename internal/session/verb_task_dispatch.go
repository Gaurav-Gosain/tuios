package session

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// taskDispatchParams are what get-task-dispatch and set-task-dispatch take.
type taskDispatchParams struct {
	Action     string `json:"action"`
	Mode       string `json:"mode"`
	Session    string `json:"session"`
	Project    string `json:"project"`
	Quiet      string `json:"quiet"`
	HumanNonce string `json:"human_nonce"`
	// With preset, or mode auto: the board's preset.
	Agent          string `json:"agent"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permission_mode"`
	Machine        string `json:"machine"`
	// With cap: auto's caps, 0 for the default.
	Cap        *int `json:"cap"`
	MachineCap *int `json:"machine_cap"`
	// With verify: the board's verify command ("" or off clears it), and
	// its limit in seconds, 0 for the default.
	Verify        string `json:"verify"`
	VerifyTimeout int    `json:"verify_timeout"`
}

// preset checks the preset p names, nil when it names no agent.
func (d *Daemon) presetParam(p taskDispatchParams) (*DispatchPreset, *verbError) {
	agent := strings.TrimSpace(p.Agent)
	if agent == "" {
		return nil, nil
	}
	if !harness.LaunchValueOK(agent) {
		return nil, invalidParam("agent", "agent is one harness id or program name, such as claude")
	}
	for _, v := range []string{p.Model, p.Effort, p.PermissionMode} {
		if v != "" && !harness.LaunchValueOK(v) {
			return nil, invalidParam("agent", "model, effort and permission_mode are one word each")
		}
	}
	machine := strings.TrimSpace(p.Machine)
	if machine != "" && machine != TaskMachineAny && machine != dispatchMachineHere && !slices.Contains(d.farHosts(), machine) {
		return nil, invalidParam("machine", "machine is here, any, or a linked machine")
	}
	return &DispatchPreset{Agent: agent, Model: p.Model, Effort: p.Effort, PermissionMode: p.PermissionMode, Machine: machine}, nil
}

// dispatchBoardParam is the board a call names: its project, or the session's
// own cards. "" when it names none, which only get-task-dispatch allows.
func (d *Daemon) dispatchBoardParam(cs *connState, p taskDispatchParams, need bool) (string, *Session, *verbError) {
	project, verr := projectParam("project", p.Project)
	if verr != nil {
		return "", nil, verr
	}
	if project == "" && p.Session == "" && !need {
		return "", nil, nil
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return "", nil, verr
	}
	return dispatchBoardKey(sess.Name(), project), sess, nil
}

// verbGetTaskDispatch answers get-task-dispatch.
func (d *Daemon) verbGetTaskDispatch(cs *connState, params json.RawMessage) (any, *verbError) {
	var p taskDispatchParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	board, _, verr := d.dispatchBoardParam(cs, p, false)
	if verr != nil {
		return nil, verr
	}
	st := d.dispatch.status(board, time.Now())
	d.autoStatus(&st)
	st.Type = "task_dispatch"
	return st, nil
}

// verbSetTaskDispatch answers set-task-dispatch. Anyone may pause. A mode,
// a resume and quiet hours are the person's, a shell's outside every pane, a
// plugin's, or a pane's that holds admin: an agent must not turn on the
// dispatch that feeds it, or start it again after a safety switch stopped it.
func (d *Daemon) verbSetTaskDispatch(cs *connState, params json.RawMessage) (any, *verbError) {
	var p taskDispatchParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if !slices.Contains(dispatchActions, p.Action) {
		return nil, invalidParam("action", "action is one of mode, pause, resume, quiet, preset, cap or verify", dispatchActions...)
	}
	if cs != nil && cs.viaLink {
		return nil, newVerbError(ErrVerbForbidden, "set-task-dispatch is refused over the link: each machine's dispatch is set on that machine")
	}
	board, sess, verr := d.dispatchBoardParam(cs, p, p.Action == dispatchActionMode || p.Action == dispatchActionPreset || p.Action == dispatchActionVerify)
	if verr != nil {
		return nil, verr
	}
	if sess == nil {
		if sess, verr = d.resolveVerbSession(cs, p.Session); verr != nil {
			return nil, verr
		}
	}
	caller, verr := d.taskCallerOf(cs, "set-task-dispatch", sess, p.HumanNonce)
	if verr != nil {
		return nil, verr
	}
	if p.Action != dispatchActionPause && caller.window != "" && !caller.admin {
		return nil, hintedVerbError(ErrVerbForbidden, "set-task-dispatch "+p.Action+" is refused from a pane: only pause is open to every pane", &VerbHint{
			Command: "tuios task dispatch pause",
			Detail:  "Nothing changed. The person changes a board's mode, quiet hours, and starts dispatch again, from the agents view or a shell outside the panes.",
		})
	}
	if p.Action == dispatchActionVerify && caller.window != "" {
		// Admin too: the command is what judges an agent's work, so no
		// agent writes it.
		return nil, newVerbError(ErrVerbForbidden, "set-task-dispatch verify is refused from every pane")
	}
	verify, verr := verifyCommandParam(p.Verify, p.VerifyTimeout)
	if verr != nil {
		return nil, verr
	}
	preset, verr := d.presetParam(p)
	if verr != nil {
		return nil, verr
	}
	if p.Action == dispatchActionPreset && preset == nil {
		return nil, invalidParam("agent", "preset needs agent: the harness auto starts, such as claude")
	}
	for _, v := range []*int{p.Cap, p.MachineCap} {
		if v != nil && (*v < 0 || *v > dispatchCapMax) {
			return nil, invalidParam("cap", "cap and machine_cap are 0 (the default) to 64")
		}
	}
	ds := &d.dispatch
	ds.mu.Lock()
	reason := ""
	switch p.Action {
	case dispatchActionMode:
		if !slices.Contains(DispatchModes, p.Mode) {
			ds.mu.Unlock()
			return nil, invalidParam("mode", "mode is manual, pull or auto", DispatchModes...)
		}
		b := ds.boardLocked(board, true)
		if preset != nil {
			b.Preset = preset
		}
		if p.Mode == DispatchAuto && b.Preset == nil {
			ds.mu.Unlock()
			return nil, invalidParam("agent", "auto needs a preset: name the agent it starts, such as claude")
		}
		b.Mode = p.Mode
		if !b.keep() {
			delete(ds.f.Boards, board)
		}
		reason = board + " is " + p.Mode
	case dispatchActionPause:
		if !ds.f.Paused {
			ds.f.Paused, ds.f.PausedBy, ds.f.PausedAt = true, caller.by, time.Now().UnixNano()
		}
	case dispatchActionResume:
		// Resume lifts every switch a person must lift: the pause, a stop for
		// a usage limit, and each board's stop and failure count.
		ds.f.Paused, ds.f.PausedBy, ds.f.PausedAt, ds.f.Stopped = false, "", 0, ""
		for k, b := range ds.f.Boards {
			b.Failures, b.Stopped = 0, ""
			if !b.keep() {
				delete(ds.f.Boards, k)
			}
		}
	case dispatchActionPreset:
		ds.boardLocked(board, true).Preset = preset
		reason = board + " preset " + preset.Agent
	case dispatchActionCap:
		if p.Cap != nil {
			ds.f.Cap = *p.Cap
		}
		if p.MachineCap != nil {
			ds.f.MachineCap = *p.MachineCap
		}
	case dispatchActionVerify:
		b := ds.boardLocked(board, true)
		b.Verify, b.VerifyTimeout = verify, p.VerifyTimeout
		if verify == "" {
			b.VerifyTimeout = 0
		}
		if !b.keep() {
			delete(ds.f.Boards, board)
		}
		reason = board + " verify"
	case dispatchActionQuiet:
		if p.Quiet != "" && p.Quiet != "off" {
			if _, _, ok := parseQuietHours(p.Quiet); !ok {
				ds.mu.Unlock()
				return nil, invalidParam("quiet", "quiet is a span of local time such as 22:00-07:00, or off")
			}
			ds.f.Quiet = p.Quiet
		} else {
			ds.f.Quiet = ""
		}
	}
	ds.saveLocked()
	ds.mu.Unlock()
	LogBasic("Task dispatch %s by %s", p.Action, caller.by)
	d.publishDispatch(p.Action, reason)
	d.kickAuto()
	go d.offerPull()
	st := ds.status(board, time.Now())
	d.autoStatus(&st)
	st.Type = "task_dispatch"
	return st, nil
}

// taskDispatchVerbs are the registry entries of the dispatch verbs.
func taskDispatchVerbs(humanNonce verbParam) map[string]verbEntry {
	status := []verbParam{
		{Name: "paused", Type: "bool"},
		{Name: "paused_by", Type: "string"},
		{Name: "stopped", Type: "string"},
		{Name: "quiet", Type: "string"},
		{Name: "quiet_now", Type: "bool"},
		{Name: "boards", Type: "[]object"},
		{Name: "cap", Type: "int"},
		{Name: "machine_cap", Type: "int"},
		{Name: "running", Type: "int"},
		{Name: "board", Type: "object"},
	}
	board := []verbParam{
		sessionParam,
		{Name: "project", Type: "string"},
	}
	return map[string]verbEntry{
		"get-task-dispatch": {
			scope:   scopeRead,
			link:    needList,
			params:  board,
			returns: status,
			handler: (*Daemon).verbGetTaskDispatch,
		},
		"set-task-dispatch": {
			scope: scopeTask,
			link:  needWrite,
			params: append(slices.Clone(board),
				verbParam{Name: "action", Type: "string", Required: true, Accepted: dispatchActions},
				verbParam{Name: "mode", Type: "string", Accepted: DispatchModes},
				verbParam{Name: "quiet", Type: "string"},
				verbParam{Name: "agent", Type: "string"},
				verbParam{Name: "model", Type: "string"},
				verbParam{Name: "effort", Type: "string"},
				verbParam{Name: "permission_mode", Type: "string"},
				verbParam{Name: "machine", Type: "string"},
				verbParam{Name: "cap", Type: "int"},
				verbParam{Name: "machine_cap", Type: "int"},
				verbParam{Name: "verify", Type: "string"},
				verbParam{Name: "verify_timeout", Type: "int"},
				humanNonce,
			),
			returns: status,
			handler: (*Daemon).verbSetTaskDispatch,
		},
	}
}
