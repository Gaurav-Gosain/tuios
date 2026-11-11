package session

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
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
	// Project is the repository the card is for, its normalised origin
	// URL, empty for a card of its session only. Host is the machine that
	// owns the card, set on a row another machine gave; empty is this one.
	Project string `json:"project,omitempty"`
	Host    string `json:"host,omitempty"`
	// Harness is the agent the card is for, AskFirst marks a card that
	// waits for a person, and Dispatched says dispatch made its last claim
	// (task_dispatch.go).
	Harness    string `json:"harness,omitempty"`
	AskFirst   bool   `json:"ask_first,omitempty"`
	Dispatched bool   `json:"dispatched,omitempty"`
	// Machine is where the card's agent starts: empty for the owner,
	// "any" for the machine placement picks, or a host name.
	Machine string `json:"machine,omitempty"`
	// Attempts, Note, Work and Verify are the card's lifecycle: see
	// task_verify.go.
	Attempts int         `json:"attempts,omitempty"`
	Note     string      `json:"note,omitempty"`
	Work     *TaskWork   `json:"work,omitempty"`
	Verify   *TaskVerify `json:"verify,omitempty"`
	// AttachmentCount is how many attachments the card has. get-task
	// lists them.
	AttachmentCount int `json:"attachment_count,omitempty"`
}

// TaskHolder is the pane that holds a card. Name, Harness and State are read
// from the pane now, and left out when the session no longer has it.
type TaskHolder struct {
	Window string `json:"window"`
	// Host is the machine the holding pane is on, empty for this one.
	Host    string `json:"host,omitempty"`
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

// taskDetail is get-task's result: the row, the prompt, the history and the
// attachments.
type taskDetail struct {
	TaskRow
	Prompt      string           `json:"prompt"`
	History     []TaskEvent      `json:"history"`
	Attachments []TaskAttachment `json:"attachments"`
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
	row.Project = tk.Project
	row.Harness, row.AskFirst, row.Dispatched = tk.Harness, tk.AskFirst, tk.Dispatched
	row.Machine = tk.Machine
	row.Attempts, row.Note, row.Work, row.Verify = tk.Attempts, tk.Note, tk.Work, tk.Verify
	row.AttachmentCount = len(tk.Attachments)
	if host, window, ok := splitRemoteHolder(tk.Holder); ok {
		row.Holder = &TaskHolder{Window: window, Host: host, HeldAt: tk.HeldAt, Name: tk.HolderName, Harness: tk.HolderHarness}
	} else if tk.Holder != "" {
		row.Holder = &TaskHolder{Window: tk.Holder, HeldAt: tk.HeldAt}
		if w, ok := findWindowState(st, tk.Holder); ok {
			row.Holder.Name, row.Holder.Harness, row.Holder.State = windowLabelOf(w), w.AgentHarness, w.AgentState.Name()
		} else {
			// A pane of another session (start-agent task_session).
			row.Holder.Name, row.Holder.Harness = tk.HolderName, tk.HolderHarness
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
// publish, "" for none, or the refusal. event names the task event the action
// stands for, and is called once the row is worked out, in the same change: the
// event leaves under the state lock, so events leave in the order of the
// changes that made them. The card's row after the change is returned with the
// action and the event published.
func (d *Daemon) changeTask(sess *Session, id string, fn func(st *SessionState, tk *TaskState, now int64) (string, *verbError), event func(action string) taskEventSpec) (TaskRow, string, taskEventSpec, *verbError) {
	var (
		verr   *verbError
		action string
		row    TaskRow
		spec   taskEventSpec
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
		if spec = event(action); spec.typ != "" {
			d.taskEvent(sess, spec.typ, spec.action, spec.window, spec.reason, row)
		}
		return nil
	})
	if verr != nil {
		return TaskRow{}, "", taskEventSpec{}, verr
	}
	if err != nil {
		return TaskRow{}, "", taskEventSpec{}, newVerbError(ErrVerbInternal, "could not change the card: "+err.Error())
	}
	return row, action, spec, nil
}

// taskEventSpec is a task event a change publishes: its type, action, window
// and reason. The zero value is no event.
type taskEventSpec struct{ typ, action, window, reason string }

// taskEvent puts a task event for row on the event stream and does nothing
// else. It is called inside the state change that made the card's row, as that
// change's last step, so a session's task events leave in the order of its
// changes. The hub only takes its own lock and does non-blocking sends, so it
// is safe under the state lock. What dispatch does with the event is
// afterTaskEvent, after the change.
func (d *Daemon) taskEvent(sess *Session, typ, action, window, reason string, row TaskRow) {
	d.events.publish(streamEvent{Type: typ, Session: sess.Name(), Window: window, Action: action, Reason: reason, Task: &row})
}

// afterTaskSpec runs afterTaskEvent for the event spec of a change that
// published one.
func (d *Daemon) afterTaskSpec(sess *Session, spec taskEventSpec, row TaskRow) {
	if spec.typ != "" {
		d.afterTaskEvent(sess, spec.action, spec.window, spec.reason, row)
	}
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
	Project    string `json:"project"`
	Harness    string `json:"harness"`
	AskFirst   bool   `json:"ask_first"`
	Machine    string `json:"machine"`
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
	project, verr := projectParam("project", p.Project)
	if verr != nil {
		return nil, verr
	}
	harnessName := printableClaim(p.Harness, 64)
	if harnessName != p.Harness || strings.ContainsAny(harnessName, " /") {
		return nil, invalidParam("harness", "harness is one agent's id, such as claude-code")
	}
	machine := printableClaim(p.Machine, 64)
	if machine != p.Machine || strings.ContainsAny(machine, " /:") {
		return nil, invalidParam("machine", "machine is any, or one machine's [hosts] name. Omit it for this machine")
	}
	if machine == federation.LocalHostName {
		machine = ""
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
			ID:       newTaskID(st),
			Title:    title,
			Prompt:   p.Prompt,
			Stage:    firstNonEmpty(p.Stage, TaskStageTodo),
			StageAt:  now,
			By:       caller.by,
			At:       now,
			Project:  project,
			Harness:  harnessName,
			AskFirst: p.AskFirst,
			Machine:  machine,
		}
		if wt := st.Worktree; wt != nil && wt.Group != "" {
			tk.Fan, tk.FanID = wt.Group, wt.FanID
		}
		tk.note(TaskEvent{Action: TaskActionAdded, By: caller.by, At: now, Stage: tk.Stage})
		st.Tasks = append(st.Tasks, tk)
		row = taskRow(sess, st, &st.Tasks[len(st.Tasks)-1])
		d.taskEvent(sess, EventTaskChanged, TaskActionAdded, "", "", row)
		return nil
	})
	if verr != nil {
		return nil, verr
	}
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not add the card: "+err.Error())
	}
	LogBasic("Added task %s in session %s", row.ID, sess.Name())
	d.afterTaskEvent(sess, TaskActionAdded, "", "", row)
	return row.typed("task_added"), nil
}

// listTasksParams are what list-tasks takes.
type listTasksParams struct {
	Session     string `json:"session"`
	Stage       string `json:"stage"`
	Holder      string `json:"holder"`
	Open        bool   `json:"open"`
	AllSessions bool   `json:"all_sessions"`
	// Project lists the cards of one repository from every session; Hosts
	// also asks each linked machine for its own (task_project.go).
	Project string `json:"project"`
	Hosts   bool   `json:"hosts"`
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
	project, verr := projectParam("project", p.Project)
	if verr != nil {
		return nil, verr
	}
	p.Project = project
	if p.Hosts && p.Project == "" && !p.AllSessions {
		return nil, invalidParam("hosts", "hosts asks the linked machines for project cards: pass project or all_sessions too")
	}
	if p.Project != "" {
		if p.Session != "" || p.Holder != "" {
			return nil, invalidParam("project", "project lists the cards of every session of the repository, so it takes no session or holder")
		}
		res := map[string]any{"type": "task_list", "project": p.Project, "tasks": d.projectRows(p)}
		// A call that came over the link lists this machine's cards only.
		if p.Hosts && (cs == nil || !cs.viaLink) {
			rows, down := d.hostTaskRows(p)
			res["tasks"] = append(res["tasks"].([]TaskRow), rows...)
			if len(down) > 0 {
				res["hosts_down"] = down
			}
		}
		return res, nil
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
		res := map[string]any{"type": "task_list", "all_sessions": true}
		// With hosts, every linked machine's project cards too. A call
		// that came over the link lists this machine's cards only.
		if p.Hosts && (cs == nil || !cs.viaLink) {
			far, down := d.hostTaskRows(p)
			for _, r := range far {
				if r.Project != "" {
					rows = append(rows, r)
				}
			}
			if len(down) > 0 {
				res["hosts_down"] = down
			}
		}
		res["tasks"] = rows
		return res, nil
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
	// Host is the machine that owns the card, for release-task and
	// finish-task from the pane that holds it there (task_project.go).
	Host string `json:"host"`
	remoteClaim
}

// taskCallerFor is taskCallerOf, or the pane of a linked machine a call over
// the link names with remote_window.
func (d *Daemon) taskCallerFor(cs *connState, verb string, sess *Session, nonce string, rc remoteClaim) (taskCaller, *verbError) {
	if rc.RemoteWindow != "" {
		c, _, verr := remoteCaller(cs, verb, rc)
		return c, verr
	}
	return d.taskCallerOf(cs, verb, sess, nonce)
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
	return taskDetail{TaskRow: taskRow(sess, st, tk).typed("task"), Prompt: tk.Prompt, History: tk.History,
		Attachments: append([]TaskAttachment{}, tk.Attachments...)}, nil
}

// claimTaskParams are what claim-task takes.
type claimTaskParams struct {
	Session    string `json:"session"`
	ID         string `json:"id"`
	Next       bool   `json:"next"`
	Window     string `json:"window"`
	HumanNonce string `json:"human_nonce"`
	// Host is the machine that owns the card: the claim is passed on to it
	// for the calling pane (task_project.go).
	Host string `json:"host"`
	remoteClaim
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
	if p.Host != "" {
		if p.Next {
			return nil, invalidParam("next", "a card on another machine is claimed by its id: tuios task ls --project lists them")
		}
		return d.forwardTask(cs, "claim-task", p.Host, p.Session, p.ID)
	}
	if p.RemoteWindow != "" {
		return d.claimForRemote(cs, p)
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
		row     TaskRow
		window  string
		missing error
	)
	var live map[string]bool
	if p.Next {
		live = d.liveFans()
	}
	err := sess.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, name)
		if err != nil {
			// Mapped once mutateState returns: mapResolveErr reads the
			// session's state for its hint, and this function holds the state
			// lock for writing. Mapped here, the daemon waited on itself and
			// stopped answering every client (#1477).
			missing = err
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
		if _, _, verr = sess.moveCard(tk, cardMove{kind: moveClaim, by: caller.by, window: window}, time.Now().UnixNano()); verr != nil {
			return errTaskRefused
		}
		row = taskRow(sess, st, tk)
		d.taskEvent(sess, EventTaskClaimed, TaskActionClaimed, window, "", row)
		return nil
	})
	if missing != nil {
		return nil, mapResolveErr(missing, sess)
	}
	if verr != nil {
		return nil, verr
	}
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not claim the card: "+err.Error())
	}
	LogBasic("Window %s claimed task %s in session %s", shortWindowID(window), row.ID, sess.Name())
	d.afterTaskEvent(sess, TaskActionClaimed, window, "", row)
	return row.typed("task_claimed"), nil
}

// verbFinishTask answers finish-task: the card moves to done and its holder
// lets it go.
func (d *Daemon) verbFinishTask(cs *connState, params json.RawMessage) (any, *verbError) {
	var p taskIDParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Host != "" {
		return d.forwardTask(cs, "finish-task", p.Host, p.Session, p.ID)
	}
	sess, verr := d.sessionForTaskID(cs, p.Session, p.ID)
	if verr != nil {
		return nil, verr
	}
	caller, verr := d.taskCallerFor(cs, "finish-task", sess, p.HumanNonce, p.remoteClaim)
	if verr != nil {
		return nil, verr
	}
	// The pane that holds the card is its agent, done with it: the card
	// goes on to verify or review (task_verify.go). Where it did the work is
	// read before the change, since that runs git.
	var work *TaskWork
	if caller.window != "" && !d.lifecycleOff.Load() {
		work = d.workOf(caller.window)
	}
	var holder string
	row, action, spec, verr := d.changeTask(sess, p.ID, func(_ *SessionState, tk *TaskState, now int64) (string, *verbError) {
		if verr := caller.mayTouch("finish-task", tk); verr != nil {
			return "", verr
		}
		move := cardMove{kind: moveFinish, by: caller.by}
		if caller.window != "" && tk.Holder == caller.window {
			if stage := d.handInStage(dispatchBoardKey(sess.Name(), tk.Project)); stage != "" {
				move = cardMove{kind: moveHandIn, by: caller.by, stage: stage, work: work}
			}
		}
		var action string
		action, holder, _ = sess.moveCard(tk, move, now)
		return action, nil
	}, func(action string) taskEventSpec {
		if action == "" {
			return taskEventSpec{}
		}
		return taskEventSpec{EventTaskFinished, TaskActionFinished, holder, ""}
	})
	if verr != nil {
		return nil, verr
	}
	if action != "" {
		d.afterTaskSpec(sess, spec, row)
		d.startTaskVerify(sess, row.ID)
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
	// With a verify command, a pane moves its card past verify only by
	// finishing it, and a move to verify runs the command again, on the
	// work recorded or on the pane that holds the card.
	var work *TaskWork
	cmd := ""
	if tk, ok := sess.taskByID(p.ID); ok && !d.lifecycleOff.Load() {
		cmd, _ = d.dispatch.verifyOf(dispatchBoardKey(sess.Name(), tk.Project))
		if cmd != "" && p.Stage == TaskStageVerify && tk.Holder != "" {
			work = d.workOf(tk.Holder)
		}
	}
	var holder string
	row, action, spec, verr := d.changeTask(sess, p.ID, func(_ *SessionState, tk *TaskState, now int64) (string, *verbError) {
		if verr := caller.mayTouch("set-task-stage", tk); verr != nil {
			return "", verr
		}
		if cmd != "" && caller.window != "" && (p.Stage == TaskStageReview || p.Stage == TaskStageShipped) {
			return "", newVerbError(ErrVerbForbidden, "the board verifies its cards: a pane moves its card on with tuios task done")
		}
		move := cardMove{kind: moveStage, by: caller.by, stage: p.Stage}
		switch {
		case p.Stage == TaskStageDone:
			move = cardMove{kind: moveFinish, by: caller.by}
		case cmd != "" && p.Stage == TaskStageVerify && (work != nil || tk.Work != nil):
			move.verifyRun, move.work = true, work
		}
		var action string
		action, holder, _ = sess.moveCard(tk, move, now)
		return action, nil
	}, func(action string) taskEventSpec {
		switch action {
		case TaskActionFinished:
			return taskEventSpec{EventTaskFinished, TaskActionFinished, holder, ""}
		case TaskActionStaged:
			return taskEventSpec{EventTaskChanged, TaskActionStaged, "", ""}
		}
		return taskEventSpec{}
	})
	if verr != nil {
		return nil, verr
	}
	d.afterTaskSpec(sess, spec, row)
	if action == TaskActionStaged {
		d.startTaskVerify(sess, row.ID)
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
	row, _, spec, verr := d.changeTask(sess, p.ID, func(_ *SessionState, tk *TaskState, now int64) (string, *verbError) {
		if verr := caller.mayTouch("block-task", tk); verr != nil {
			return "", verr
		}
		move := cardMove{kind: moveBlock, by: caller.by, reason: reason}
		if p.Clear {
			move = cardMove{kind: moveUnblock, by: caller.by}
		}
		action, _, _ := sess.moveCard(tk, move, now)
		return action, nil
	}, func(action string) taskEventSpec {
		if action == "" {
			return taskEventSpec{}
		}
		return taskEventSpec{EventTaskChanged, action, "", reason}
	})
	if verr != nil {
		return nil, verr
	}
	d.afterTaskSpec(sess, spec, row)
	return row.typed("task_changed"), nil
}

// verbReleaseTask answers release-task: the holder lets the card go and it
// keeps its stage, so another pane can take it.
func (d *Daemon) verbReleaseTask(cs *connState, params json.RawMessage) (any, *verbError) {
	var p taskIDParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Host != "" {
		return d.forwardTask(cs, "release-task", p.Host, p.Session, p.ID)
	}
	sess, verr := d.sessionForTaskID(cs, p.Session, p.ID)
	if verr != nil {
		return nil, verr
	}
	caller, verr := d.taskCallerFor(cs, "release-task", sess, p.HumanNonce, p.remoteClaim)
	if verr != nil {
		return nil, verr
	}
	row, _, spec, verr := d.changeTask(sess, p.ID, func(_ *SessionState, tk *TaskState, now int64) (string, *verbError) {
		if verr := caller.mayTouch("release-task", tk); verr != nil {
			return "", verr
		}
		if tk.Holder == "" {
			return "", invalidParam("id", "task card "+tk.ID+" is held by no pane, so there is nothing to release")
		}
		action, _, _ := sess.moveCard(tk, cardMove{kind: moveRelease, by: caller.by}, now)
		return action, nil
	}, func(string) taskEventSpec {
		return taskEventSpec{EventTaskChanged, TaskActionReleased, "", ""}
	})
	if verr != nil {
		return nil, verr
	}
	d.afterTaskSpec(sess, spec, row)
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
	var hashes []string
	err := sess.mutateState(func(st *SessionState) error {
		i := taskIndex(st.Tasks, p.ID)
		if i < 0 {
			verr = noTask(sess, p.ID)
			return errTaskRefused
		}
		row = taskRow(sess, st, &st.Tasks[i])
		for _, a := range st.Tasks[i].Attachments {
			if a.Hash != "" {
				hashes = append(hashes, a.Hash)
			}
		}
		st.Tasks = slices.Delete(st.Tasks, i, i+1)
		d.taskEvent(sess, EventTaskChanged, TaskActionRemoved, "", "", row)
		return nil
	})
	if verr != nil {
		return nil, verr
	}
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not remove the card: "+err.Error())
	}
	LogBasic("Removed task %s from session %s", p.ID, sess.Name())
	if len(hashes) > 0 {
		d.sweepAttachments(hashes...)
	}
	d.afterTaskEvent(sess, TaskActionRemoved, "", "", row)
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
		for _, row := range rows {
			d.taskEvent(sess, EventTaskChanged, TaskActionReleased, "", taskReleaseRestart, row)
		}
		return nil
	}); err != nil {
		LogBasic("Could not restore the task cards of session %s: %v", sess.Name(), err)
		return
	}
	d.afterReleased(sess, rows, taskReleaseRestart)
	if d.dispatch.loaded() {
		// A session resurrected later; at the start the daemon resumes
		// them once the boards are read.
		d.resumeVerifies(sess)
	}
}

// releasedRows releases the cards of st whose holder held reports true, and
// returns their rows.
func releasedRows(sess *Session, st *SessionState, held func(holder string) bool, reason string) []TaskRow {
	released := releaseHolders(sess, st, held, reason, time.Now().UnixNano())
	rows := make([]TaskRow, len(released))
	for i := range released {
		rows[i] = taskRow(sess, st, &released[i])
	}
	return rows
}

// afterReleased is what follows the release events of rows, published inside
// the change that released them: the log line and afterTaskEvent for each.
func (d *Daemon) afterReleased(sess *Session, rows []TaskRow, reason string) {
	for _, row := range rows {
		LogBasic("Released task %s in session %s: %s", row.ID, sess.Name(), reason)
		d.afterTaskEvent(sess, TaskActionReleased, "", reason, row)
	}
}

// publishReleased says on the event stream that the cards of rows were
// released for reason. It is called only from the session's event sink, which
// runs under the state lock of the change that released them.
func (d *Daemon) publishReleased(sess *Session, rows []TaskRow, reason string) {
	for _, row := range rows {
		LogBasic("Released task %s in session %s: %s", row.ID, sess.Name(), reason)
		d.taskEvent(sess, EventTaskChanged, TaskActionReleased, "", reason, row)
		d.afterTaskEvent(sess, TaskActionReleased, "", reason, row)
	}
}
