package session

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The task verbs: add-task, list-tasks, get-task, claim-task, finish-task,
// set-task-stage, block-task, release-task and remove-task. The store is
// task.go.
//
// list-tasks and get-task read the session (scopeRead). Every other verb
// writes the cards (scopeTask): a pane needs the task grant, and reaches only
// its own session. Over a link the listing needs list and a change needs
// write.
//
// Who made a change (by) is decided here, once, from the connection, as the
// queue decides it (newQueueEntry): human only with a live human_nonce,
// link:HOST over a link, the pane's window id for a process in a pane,
// plugin:ID for a native plugin, and shell for anything else.
//
// Who may do what: a pane claims only for itself, and finishes, stages,
// blocks or releases only the card it holds, unless it holds admin. The
// person, a shell, a link and a plugin name the window on a claim and may act
// on any card. remove-task from a pane needs admin.

// taskCaller is who calls a task verb.
type taskCaller struct {
	by string
	// window is the caller's pane, empty for a caller in no pane.
	window string
	// any is set for a caller that may claim for another pane and act on a
	// card it does not hold.
	any bool
	// admin is set for a pane that holds admin, which remove-task needs.
	admin bool
}

// taskCallerOf places the caller on cs for a change to the cards of sess.
func (d *Daemon) taskCallerOf(cs *connState, verb string, sess *Session, nonce string) (taskCaller, *verbError) {
	if verr := refuseForwardedPane(cs, verb); verr != nil {
		return taskCaller{}, verr
	}
	if nonce != "" {
		if _, human := d.humanNonceFor(nonce, sess.ID, cs); !human {
			return taskCaller{}, hintedVerbError(ErrVerbNotHuman, "human_nonce does not belong to a client attached right now", &VerbHint{
				Param:  "human_nonce",
				Detail: "Nothing changed. Only the person's attached client holds a nonce, and a process inside a pane cannot use one. Leave human_nonce out to act as yourself.",
			})
		}
		return taskCaller{by: queueByHuman, any: true}, nil
	}
	if cs != nil && cs.viaLink {
		cs.mu.Lock()
		peer := cs.linkPeer
		cs.mu.Unlock()
		return taskCaller{by: queueByLinkPrefix + firstNonEmpty(peer, "*"), any: true}, nil
	}
	pa := d.paneAuthority(cs)
	switch {
	case pa == nil:
		return taskCaller{by: queueByShell, any: true}, nil
	case pa.plugin != "":
		return taskCaller{by: "plugin:" + pa.plugin, any: true}, nil
	case pa.hosted || pa.window == unplacedWindow:
		return taskCaller{}, hintedVerbError(ErrVerbForbidden, verb+" is refused: the caller's pane could not be placed in a session of this daemon", &VerbHint{
			Detail: "Nothing changed. A card is held by a pane of its session, and this caller is in none here.",
		})
	}
	admin := pa.grants.Has(GrantAdmin)
	return taskCaller{by: pa.window, window: pa.window, any: admin, admin: admin}, nil
}

// mayTouch refuses a change to tk by a caller that does not hold it and may
// not act on any card.
func (c taskCaller) mayTouch(verb string, tk *TaskState) *verbError {
	if c.any || tk.Holder == c.window {
		return nil
	}
	who := "another pane"
	if tk.Holder == "" {
		who = "no pane"
	}
	return hintedVerbError(ErrVerbForbidden, verb+" of "+tk.ID+" is refused: "+who+" holds it, and a pane changes only the card it holds", &VerbHint{
		Command: "tuios task claim " + tk.ID,
		Detail:  "Nothing changed. Claim a card first, then finish, stage, block or release it. Only the person, a shell outside every pane, or a pane holding admin changes any card.",
	})
}

// TaskRow is a card as the task verbs and the task events give it. Type is
// set on a verb's result and left empty in a list and an event.
type TaskRow struct {
	Type    string      `json:"type,omitempty"`
	ID      string      `json:"id"`
	Session string      `json:"session"`
	Title   string      `json:"title"`
	Preview string      `json:"preview"`
	Stage   string      `json:"stage"`
	StageAt int64       `json:"stage_at"`
	Holder  *TaskHolder `json:"holder"`
	By      string      `json:"by"`
	At      int64       `json:"at"`
	Blocked *TaskBlock  `json:"blocked"`
	Fan     *TaskFan    `json:"fan,omitempty"`
}

// TaskHolder is the pane that holds a card. Name, Harness and State are read
// from the pane now, and left out when the session no longer has it.
type TaskHolder struct {
	Window  string `json:"window"`
	Name    string `json:"name,omitempty"`
	Harness string `json:"harness,omitempty"`
	State   string `json:"state,omitempty"`
	HeldAt  int64  `json:"held_at"`
}

// TaskFan names the fan a card was added in.
type TaskFan struct {
	Fan   string `json:"fan"`
	FanID string `json:"fan_id"`
}

// taskDetail is get-task's result: the row, the prompt and the history.
type taskDetail struct {
	TaskRow
	Prompt  string      `json:"prompt"`
	History []TaskEvent `json:"history"`
}

// taskRow is tk as list-tasks gives it, its holder read from st.
func taskRow(sess *Session, st *SessionState, tk *TaskState) TaskRow {
	row := TaskRow{
		ID:      tk.ID,
		Session: sess.Name(),
		Title:   tk.Title,
		Preview: queuePreview(tk.Prompt),
		Stage:   tk.Stage,
		StageAt: tk.StageAt,
		By:      tk.By,
		At:      tk.At,
		Blocked: tk.Blocked,
	}
	if tk.Holder != "" {
		row.Holder = &TaskHolder{Window: tk.Holder, HeldAt: tk.HeldAt}
		if w, ok := findWindowState(st, tk.Holder); ok {
			row.Holder.Name, row.Holder.Harness, row.Holder.State = windowLabelOf(w), w.AgentHarness, w.AgentState.Name()
		}
	}
	if tk.Fan != "" {
		row.Fan = &TaskFan{Fan: tk.Fan, FanID: tk.FanID}
	}
	return row
}

// typed is row as a verb's result of type typ.
func (row TaskRow) typed(typ string) TaskRow {
	row.Type = typ
	return row
}

// noTask is the refusal for a card id the session does not hold.
func noTask(sess *Session, id string) *verbError {
	return hintedVerbError(ErrVerbNoTask, "no task card "+echoName(id)+" in session "+echoName(sess.Name()), &VerbHint{
		Param:   "id",
		Verb:    "list-tasks",
		Command: "tuios task ls",
		Detail:  "Nothing changed. The card may have been removed. tuios task ls lists the session's cards.",
	})
}

// errTaskRefused ends a task mutation whose refusal is in a *verbError the
// closure holds.
var errTaskRefused = errors.New("task change refused")

// changeTask runs fn on the card id of sess inside one mutateState, so two
// callers cannot both act on the same card at once. fn returns the action to
// publish, "" for none, or the refusal. The card's row after the change is
// returned with the session state it was read from.
func (d *Daemon) changeTask(sess *Session, id string, fn func(st *SessionState, tk *TaskState, now int64) (string, *verbError)) (TaskRow, string, *verbError) {
	var (
		verr   *verbError
		action string
		row    TaskRow
	)
	err := sess.mutateState(func(st *SessionState) error {
		i := taskIndex(st.Tasks, id)
		if i < 0 {
			verr = noTask(sess, id)
			return errTaskRefused
		}
		tk := &st.Tasks[i]
		action, verr = fn(st, tk, time.Now().UnixNano())
		if verr != nil {
			return errTaskRefused
		}
		row = taskRow(sess, st, tk)
		return nil
	})
	if verr != nil {
		return TaskRow{}, "", verr
	}
	if err != nil {
		return TaskRow{}, "", newVerbError(ErrVerbInternal, "could not change the card: "+err.Error())
	}
	return row, action, nil
}

// publishTask sends a task event for row.
func (d *Daemon) publishTask(sess *Session, typ, action, window, reason string, row TaskRow) {
	d.events.publish(streamEvent{Type: typ, Session: sess.Name(), Window: window, Action: action, Reason: reason, Task: &row})
}

// taskStageParam checks a stage parameter.
func taskStageParam(name, stage string) *verbError {
	if stage == "" || slices.Contains(TaskStages, stage) {
		return nil
	}
	return hintedVerbError(ErrVerbInvalidParams, "unknown stage "+echoName(stage), &VerbHint{
		Param:      name,
		Accepted:   TaskStages,
		DidYouMean: closestMatch(stage, TaskStages),
	})
}

// addTaskParams are what add-task takes.
type addTaskParams struct {
	Session    string `json:"session"`
	Title      string `json:"title"`
	Prompt     string `json:"prompt"`
	Stage      string `json:"stage"`
	HumanNonce string `json:"human_nonce"`
}

// verbAddTask answers add-task.
func (d *Daemon) verbAddTask(cs *connState, params json.RawMessage) (any, *verbError) {
	var p addTaskParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	title := printableClaim(p.Title, taskMaxTitle)
	if title == "" {
		return nil, invalidParam("title", "title is required: one line that says what the card is")
	}
	if len(p.Prompt) > taskMaxPrompt {
		return nil, invalidParam("prompt", "prompt is longer than "+strconv.Itoa(taskMaxPrompt)+" bytes")
	}
	if verr := taskStageParam("stage", p.Stage); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	caller, verr := d.taskCallerOf(cs, "add-task", sess, p.HumanNonce)
	if verr != nil {
		return nil, verr
	}
	var row TaskRow
	err := sess.mutateState(func(st *SessionState) error {
		if len(st.Tasks) >= taskMaxCards {
			verr = hintedVerbError(ErrVerbInvalidParams, "session "+echoName(sess.Name())+" holds "+strconv.Itoa(taskMaxCards)+" task cards, as many as it may", &VerbHint{
				Command: "tuios task rm <id>",
				Detail:  "Nothing was added. Remove the cards that are done.",
			})
			return errTaskRefused
		}
		now := time.Now().UnixNano()
		tk := TaskState{
			ID:      newTaskID(st),
			Title:   title,
			Prompt:  p.Prompt,
			Stage:   firstNonEmpty(p.Stage, TaskStageTodo),
			StageAt: now,
			By:      caller.by,
			At:      now,
		}
		if wt := st.Worktree; wt != nil && wt.Group != "" {
			tk.Fan, tk.FanID = wt.Group, wt.FanID
		}
		tk.note(TaskEvent{Action: TaskActionAdded, By: caller.by, At: now, Stage: tk.Stage})
		st.Tasks = append(st.Tasks, tk)
		row = taskRow(sess, st, &st.Tasks[len(st.Tasks)-1])
		return nil
	})
	if verr != nil {
		return nil, verr
	}
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not add the card: "+err.Error())
	}
	LogBasic("Added task %s in session %s", row.ID, sess.Name())
	d.publishTask(sess, EventTaskChanged, TaskActionAdded, "", "", row)
	return row.typed("task_added"), nil
}

// listTasksParams are what list-tasks takes.
type listTasksParams struct {
	Session     string `json:"session"`
	Stage       string `json:"stage"`
	Holder      string `json:"holder"`
	Open        bool   `json:"open"`
	AllSessions bool   `json:"all_sessions"`
}

// verbListTasks answers list-tasks: the session's cards, oldest first. With
// all_sessions it lists every session's, in session name order.
func (d *Daemon) verbListTasks(cs *connState, params json.RawMessage) (any, *verbError) {
	var p listTasksParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := taskStageParam("stage", p.Stage); verr != nil {
		return nil, verr
	}
	if p.AllSessions {
		if p.Session != "" {
			return nil, invalidParam("all_sessions", "all_sessions lists every session, so it takes no session. Drop one or the other")
		}
		if p.Holder != "" {
			return nil, invalidParam("holder", "a window means nothing across sessions, so all_sessions takes no holder")
		}
		sessions := d.manager.AllSessions()
		slices.SortFunc(sessions, func(a, b *Session) int { return strings.Compare(a.Name(), b.Name()) })
		rows := []TaskRow{}
		for _, sess := range sessions {
			rows = append(rows, listedTasks(sess, sess.GetState(), p, "")...)
		}
		return map[string]any{
			"type":         "task_list",
			"all_sessions": true,
			"tasks":        rows,
		}, nil
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	st := sess.GetState()
	holder := ""
	if p.Holder != "" {
		idx, err := findWindowStateIndex(st.Windows, p.Holder)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		holder = st.Windows[idx].ID
	}
	return map[string]any{
		"type":    "task_list",
		"session": sess.Name(),
		"tasks":   listedTasks(sess, st, p, holder),
	}, nil
}

// listedTasks is one session's rows of a list-tasks answer.
func listedTasks(sess *Session, st *SessionState, p listTasksParams, holder string) []TaskRow {
	rows := []TaskRow{}
	for i := range st.Tasks {
		tk := &st.Tasks[i]
		if p.Stage != "" && tk.Stage != p.Stage {
			continue
		}
		if p.Open && tk.Stage == TaskStageDone {
			continue
		}
		if holder != "" && tk.Holder != holder {
			continue
		}
		rows = append(rows, taskRow(sess, st, tk))
	}
	return rows
}

// taskIDParams are what get-task, finish-task, release-task and remove-task
// take.
type taskIDParams struct {
	Session    string `json:"session"`
	ID         string `json:"id"`
	HumanNonce string `json:"human_nonce"`
}

// sessionForTaskID checks the card id a verb names and resolves the session
// it is looked up in.
func (d *Daemon) sessionForTaskID(cs *connState, session, id string) (*Session, *verbError) {
	if strings.TrimSpace(id) == "" {
		return nil, invalidParam("id", "id is required: the card, such as t1")
	}
	return d.resolveVerbSession(cs, session)
}

// verbGetTask answers get-task: one card, with its prompt and history.
func (d *Daemon) verbGetTask(cs *connState, params json.RawMessage) (any, *verbError) {
	var p taskIDParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.sessionForTaskID(cs, p.Session, p.ID)
	if verr != nil {
		return nil, verr
	}
	st := sess.GetState()
	i := taskIndex(st.Tasks, p.ID)
	if i < 0 {
		return nil, noTask(sess, p.ID)
	}
	tk := &st.Tasks[i]
	return taskDetail{TaskRow: taskRow(sess, st, tk).typed("task"), Prompt: tk.Prompt, History: tk.History}, nil
}

// claimTaskParams are what claim-task takes.
type claimTaskParams struct {
	Session    string `json:"session"`
	ID         string `json:"id"`
	Next       bool   `json:"next"`
	Window     string `json:"window"`
	HumanNonce string `json:"human_nonce"`
}

// verbClaimTask answers claim-task: the card id, or with next the oldest card
// nobody holds, taken for a pane. The pick and the take happen in one
// mutateState, so two panes claiming at once take two cards.
func (d *Daemon) verbClaimTask(cs *connState, params json.RawMessage) (any, *verbError) {
	var p claimTaskParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Next == (p.ID != "") {
		return nil, invalidParam("id", "name the card with id, or pass next to take the oldest open card, not both")
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	caller, verr := d.taskCallerOf(cs, "claim-task", sess, p.HumanNonce)
	if verr != nil {
		return nil, verr
	}
	name := firstNonEmpty(p.Window, caller.window)
	if name == "" {
		return nil, hintedVerbError(ErrVerbInvalidParams, "window is required outside a pane: the pane that takes the card", &VerbHint{
			Param:   "window",
			Command: "tuios task claim --next -w <pane>",
			Detail:  "Nothing was claimed. A card is held by a pane. From inside a pane, leave window out to claim for that pane.",
		})
	}

	var (
		row    TaskRow
		window string
	)
	var live map[string]bool
	if p.Next {
		live = d.liveFans()
	}
	err := sess.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, name)
		if err != nil {
			verr = mapResolveErr(err, sess)
			return errTaskRefused
		}
		if window = st.Windows[idx].ID; window != caller.window && !caller.any {
			verr = hintedVerbError(ErrVerbForbidden, "claim-task for window "+shortWindowID(window)+" is refused: a pane claims only for itself", &VerbHint{
				Param:  "window",
				Detail: "Nothing was claimed. Leave window out, or pass $TUIOS_PANE_ID.",
			})
			return errTaskRefused
		}
		var i int
		if p.Next {
			if i = nextFreeTask(st, live); i < 0 {
				verr = hintedVerbError(ErrVerbNoTask, "no open task card to take in session "+echoName(sess.Name()), &VerbHint{
					Verb:    "list-tasks",
					Command: "tuios task ls --open",
					Detail:  "Nothing was claimed. Every card is held, blocked or done. Subscribe to task-changed to hear when one is added or released.",
				})
				return errTaskRefused
			}
		} else if i = taskIndex(st.Tasks, p.ID); i < 0 {
			verr = noTask(sess, p.ID)
			return errTaskRefused
		}
		tk := &st.Tasks[i]
		if _, verr = tk.take(window, caller.by, time.Now().UnixNano()); verr != nil {
			return errTaskRefused
		}
		row = taskRow(sess, st, tk)
		return nil
	})
	if verr != nil {
		return nil, verr
	}
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not claim the card: "+err.Error())
	}
	LogBasic("Window %s claimed task %s in session %s", shortWindowID(window), row.ID, sess.Name())
	d.publishTask(sess, EventTaskClaimed, TaskActionClaimed, window, "", row)
	return row.typed("task_claimed"), nil
}

// verbFinishTask answers finish-task: the card moves to done and its holder
// lets it go.
func (d *Daemon) verbFinishTask(cs *connState, params json.RawMessage) (any, *verbError) {
	var p taskIDParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.sessionForTaskID(cs, p.Session, p.ID)
	if verr != nil {
		return nil, verr
	}
	caller, verr := d.taskCallerOf(cs, "finish-task", sess, p.HumanNonce)
	if verr != nil {
		return nil, verr
	}
	var holder string
	row, action, verr := d.changeTask(sess, p.ID, func(_ *SessionState, tk *TaskState, now int64) (string, *verbError) {
		if verr := caller.mayTouch("finish-task", tk); verr != nil {
			return "", verr
		}
		var changed bool
		if holder, changed = tk.finish(caller.by, now); !changed {
			return "", nil
		}
		return TaskActionFinished, nil
	})
	if verr != nil {
		return nil, verr
	}
	if action != "" {
		d.publishTask(sess, EventTaskFinished, TaskActionFinished, holder, "", row)
	}
	return row.typed("task_finished"), nil
}

// setTaskStageParams are what set-task-stage takes.
type setTaskStageParams struct {
	Session    string `json:"session"`
	ID         string `json:"id"`
	Stage      string `json:"stage"`
	HumanNonce string `json:"human_nonce"`
}

// verbSetTaskStage answers set-task-stage. A move to done is a finish: the
// holder lets the card go, and task-finished is sent, as finish-task sends it.
func (d *Daemon) verbSetTaskStage(cs *connState, params json.RawMessage) (any, *verbError) {
	var p setTaskStageParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.sessionForTaskID(cs, p.Session, p.ID)
	if verr != nil {
		return nil, verr
	}
	if p.Stage == "" {
		return nil, invalidParam("stage", "stage is required", TaskStages...)
	}
	if verr := taskStageParam("stage", p.Stage); verr != nil {
		return nil, verr
	}
	caller, verr := d.taskCallerOf(cs, "set-task-stage", sess, p.HumanNonce)
	if verr != nil {
		return nil, verr
	}
	var holder string
	row, action, verr := d.changeTask(sess, p.ID, func(_ *SessionState, tk *TaskState, now int64) (string, *verbError) {
		if verr := caller.mayTouch("set-task-stage", tk); verr != nil {
			return "", verr
		}
		if p.Stage == TaskStageDone {
			var changed bool
			if holder, changed = tk.finish(caller.by, now); !changed {
				return "", nil
			}
			return TaskActionFinished, nil
		}
		if tk.Stage == p.Stage {
			return "", nil
		}
		tk.setStage(p.Stage, now)
		tk.note(TaskEvent{Action: TaskActionStaged, By: caller.by, At: now, Stage: tk.Stage, Holder: tk.Holder})
		return TaskActionStaged, nil
	})
	if verr != nil {
		return nil, verr
	}
	switch action {
	case TaskActionFinished:
		d.publishTask(sess, EventTaskFinished, TaskActionFinished, holder, "", row)
	case TaskActionStaged:
		d.publishTask(sess, EventTaskChanged, TaskActionStaged, "", "", row)
	}
	return row.typed("task_changed"), nil
}

// blockTaskParams are what block-task takes.
type blockTaskParams struct {
	Session    string `json:"session"`
	ID         string `json:"id"`
	Reason     string `json:"reason"`
	Clear      bool   `json:"clear"`
	HumanNonce string `json:"human_nonce"`
}

// verbBlockTask answers block-task: the card is marked stuck, with a reason,
// or with clear the mark is lifted. A block keeps the holder: release-task
// lets a card go.
func (d *Daemon) verbBlockTask(cs *connState, params json.RawMessage) (any, *verbError) {
	var p blockTaskParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.sessionForTaskID(cs, p.Session, p.ID)
	if verr != nil {
		return nil, verr
	}
	reason := printableClaim(p.Reason, taskMaxReason)
	if p.Clear && reason != "" {
		return nil, invalidParam("clear", "pass reason or clear, not both")
	}
	if !p.Clear && reason == "" {
		return nil, invalidParam("reason", "reason is required: what the card is waiting on. Pass clear to lift a block")
	}
	caller, verr := d.taskCallerOf(cs, "block-task", sess, p.HumanNonce)
	if verr != nil {
		return nil, verr
	}
	row, action, verr := d.changeTask(sess, p.ID, func(_ *SessionState, tk *TaskState, now int64) (string, *verbError) {
		if verr := caller.mayTouch("block-task", tk); verr != nil {
			return "", verr
		}
		if p.Clear {
			if tk.Blocked == nil {
				return "", nil
			}
			tk.Blocked = nil
			tk.note(TaskEvent{Action: TaskActionUnblocked, By: caller.by, At: now, Stage: tk.Stage, Holder: tk.Holder})
			return TaskActionUnblocked, nil
		}
		tk.Blocked = &TaskBlock{Reason: reason, By: caller.by, At: now}
		tk.note(TaskEvent{Action: TaskActionBlocked, By: caller.by, At: now, Stage: tk.Stage, Holder: tk.Holder, Reason: reason})
		return TaskActionBlocked, nil
	})
	if verr != nil {
		return nil, verr
	}
	if action != "" {
		d.publishTask(sess, EventTaskChanged, action, "", reason, row)
	}
	return row.typed("task_changed"), nil
}

// verbReleaseTask answers release-task: the holder lets the card go and it
// keeps its stage, so another pane can take it.
func (d *Daemon) verbReleaseTask(cs *connState, params json.RawMessage) (any, *verbError) {
	var p taskIDParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.sessionForTaskID(cs, p.Session, p.ID)
	if verr != nil {
		return nil, verr
	}
	caller, verr := d.taskCallerOf(cs, "release-task", sess, p.HumanNonce)
	if verr != nil {
		return nil, verr
	}
	row, _, verr := d.changeTask(sess, p.ID, func(_ *SessionState, tk *TaskState, now int64) (string, *verbError) {
		if verr := caller.mayTouch("release-task", tk); verr != nil {
			return "", verr
		}
		if tk.Holder == "" {
			return "", invalidParam("id", "task card "+tk.ID+" is held by no pane, so there is nothing to release")
		}
		tk.release(caller.by, "", now)
		return TaskActionReleased, nil
	})
	if verr != nil {
		return nil, verr
	}
	d.publishTask(sess, EventTaskChanged, TaskActionReleased, "", "", row)
	return row.typed("task_changed"), nil
}

// verbRemoveTask answers remove-task: the card and its history go.
func (d *Daemon) verbRemoveTask(cs *connState, params json.RawMessage) (any, *verbError) {
	var p taskIDParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.sessionForTaskID(cs, p.Session, p.ID)
	if verr != nil {
		return nil, verr
	}
	caller, verr := d.taskCallerOf(cs, "remove-task", sess, p.HumanNonce)
	if verr != nil {
		return nil, verr
	}
	if caller.window != "" && !caller.admin {
		return nil, hintedVerbError(ErrVerbForbidden, "remove-task is refused for this pane: removing a card needs the admin grant", &VerbHint{
			Command: "tuios task done " + p.ID,
			Detail:  "Nothing was removed. A pane finishes the card it holds. The person removes cards.",
		})
	}
	var row TaskRow
	err := sess.mutateState(func(st *SessionState) error {
		i := taskIndex(st.Tasks, p.ID)
		if i < 0 {
			verr = noTask(sess, p.ID)
			return errTaskRefused
		}
		row = taskRow(sess, st, &st.Tasks[i])
		st.Tasks = slices.Delete(st.Tasks, i, i+1)
		return nil
	})
	if verr != nil {
		return nil, verr
	}
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not remove the card: "+err.Error())
	}
	LogBasic("Removed task %s from session %s", p.ID, sess.Name())
	d.publishTask(sess, EventTaskChanged, TaskActionRemoved, "", "", row)
	return map[string]any{"type": "task_removed", "id": p.ID}, nil
}

// restoreTasks puts a saved session's cards back after the restore, every
// holder released, in one change: no agent comes back from a restart, and a
// holder's pane may not have either.
func (d *Daemon) restoreTasks(sess *Session, tasks []TaskState, next uint64) {
	if len(tasks) == 0 && next == 0 {
		return
	}
	var rows []TaskRow
	if err := sess.mutateState(func(st *SessionState) error {
		st.Tasks, st.NextTaskID = slices.Clone(tasks), next
		rows = releasedRows(sess, st, func(string) bool { return true }, taskReleaseRestart)
		return nil
	}); err != nil {
		LogBasic("Could not restore the task cards of session %s: %v", sess.Name(), err)
		return
	}
	d.publishReleased(sess, rows, taskReleaseRestart)
}

// releasedRows releases the cards of st whose holder held reports true, and
// returns their rows.
func releasedRows(sess *Session, st *SessionState, held func(holder string) bool, reason string) []TaskRow {
	released := releaseHolders(st, held, reason, time.Now().UnixNano())
	rows := make([]TaskRow, len(released))
	for i := range released {
		rows[i] = taskRow(sess, st, &released[i])
	}
	return rows
}

// publishReleased says on the event stream that the cards of rows were
// released for reason.
func (d *Daemon) publishReleased(sess *Session, rows []TaskRow, reason string) {
	for _, row := range rows {
		LogBasic("Released task %s in session %s: %s", row.ID, sess.Name(), reason)
		d.publishTask(sess, EventTaskChanged, TaskActionReleased, "", reason, row)
	}
}
