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
	st := ds.status(board, time.Now())
	d.autoStatus(&st)
	st.Type = "task_dispatch"
	return st, nil
}

// taskDispatchVerbs are the registry entries of the dispatch verbs.
func taskDispatchVerbs(humanNonce verbParam) map[string]verbEntry {
	status := []verbParam{
		{Name: "paused", Type: "bool", Description: "True while dispatch is paused."},
		{Name: "paused_by", Type: "string", Description: "Who paused it."},
		{Name: "stopped", Type: "string", Description: "Why all dispatch stopped on its own, such as usage limit. Empty when it did not."},
		{Name: "quiet", Type: "string", Description: "The quiet hours, local time, such as 22:00-07:00."},
		{Name: "quiet_now", Type: "bool", Description: "True inside the quiet hours."},
		{Name: "boards", Type: "[]object", Description: "The boards with a mode or a stop: board, project or session, mode, failures (dispatched cards in a row that did not finish), stopped, verify and verify_timeout."},
		{Name: "cap", Type: "int", Description: "Auto's agents at once, in total."},
		{Name: "machine_cap", Type: "int", Description: "Auto's agents at once on one machine."},
		{Name: "running", Type: "int", Description: "Auto's agents now."},
		{Name: "board", Type: "object", Description: "The board the call named, also when it is manual. An auto board adds preset, running, waiting and note."},
	}
	board := []verbParam{
		sessionParam,
		{Name: "project", Type: "string", Description: "The project board, by origin URL. Omit for the session's own cards."},
	}
	return map[string]verbEntry{
		"get-task-dispatch": {
			scope:       scopeRead,
			link:        needList,
			description: "Read the task dispatch settings: each board's mode (manual or pull) and the switches that stop dispatch (pause, quiet hours, a board stopped after 3 failures in a row, a usage limit).",
			params:      board,
			returns:     status,
			examples:    []string{`{"id":1,"verb":"get-task-dispatch","params":{"project":"git@github.com:acme/api.git"}}`},
			handler:     (*Daemon).verbGetTaskDispatch,
		},
		"set-task-dispatch": {
			scope:       scopeTask,
			link:        needWrite,
			description: "Change task dispatch. mode sets a board to manual, pull or auto: in pull, an agent that finishes its card is given the board's next open card, as mail from tuios; in auto, the daemon starts the board's preset agent for each open card, in a new worktree, and mails it the card. preset sets that agent; auto needs one. cap bounds the agents at once. verify sets the command run in a card's worktree when its agent is done: a pass moves the card to review, a fail to todo. pause stops all dispatch at once, and any pane may pause. resume lifts the pause and every stop. quiet sets the hours when dispatch gives no card. A pane needs admin for every action but pause, and verify is refused from every pane. Refused over a link.",
			params: append(slices.Clone(board),
				verbParam{Name: "action", Type: "string", Required: true, Description: "What to change.", Accepted: dispatchActions},
				verbParam{Name: "mode", Type: "string", Description: "With mode: the board's new mode.", Accepted: DispatchModes},
				verbParam{Name: "quiet", Type: "string", Description: "With quiet: a span of local time such as 22:00-07:00, or off."},
				verbParam{Name: "agent", Type: "string", Description: "With preset or mode auto: the agent auto starts. A card's own agent comes first."},
				verbParam{Name: "model", Type: "string", Description: "With preset: as start-agent takes it."},
				verbParam{Name: "effort", Type: "string", Description: "With preset: as start-agent takes it."},
				verbParam{Name: "permission_mode", Type: "string", Description: "With preset: as start-agent takes it. Omit for the agent's own default."},
				verbParam{Name: "machine", Type: "string", Description: "With preset: here (the default), any, or a linked machine."},
				verbParam{Name: "cap", Type: "int", Description: "With cap: agents at once in total, 0 for 3."},
				verbParam{Name: "machine_cap", Type: "int", Description: "With cap: on one machine, 0 for 2."},
				verbParam{Name: "verify", Type: "string", Description: "With verify: a shell line, or off."},
				verbParam{Name: "verify_timeout", Type: "int", Description: "With verify: seconds, 0 for 600."},
				humanNonce,
			),
			returns: status,
			examples: []string{
				`{"id":1,"verb":"set-task-dispatch","params":{"action":"mode","mode":"pull","project":"git@github.com:acme/api.git"}}`,
				`{"id":1,"verb":"set-task-dispatch","params":{"action":"pause"}}`,
			},
			handler: (*Daemon).verbSetTaskDispatch,
		},
	}
}
