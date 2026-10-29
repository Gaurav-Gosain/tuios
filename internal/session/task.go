package session

import (
	"slices"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Task cards: one unit of work each, kept by the daemon in the session it
// belongs to, so a conductor hands work out from here rather than from a file
// that dies with its pane.
//
// A card has a title, an optional prompt (what the pane that takes it is to
// do), a stage, and at most one holder: the window id of the pane working on
// it. claim-task takes a card for a pane, finish-task moves it to done and
// lets it go, release-task lets it go and keeps its stage, block-task marks it
// stuck, and set-task-stage moves it by hand. Every change is appended to the
// card's history with who made it and when.
//
// The cards live on SessionState (Tasks, NextTaskID), written only through
// mutateState, so they are saved with the session and come back after a
// restart. A holder does not come back: the restore releases every card with
// a restart entry, since no agent survives a restart (clearLiveAgent). A card
// is also released when its pane closes or its agent leaves the pane, inside
// the state change that closed it (releasedTaskEvents), so no claim lands
// between the pane going and the card being let go.
//
// The verbs are in verb_task.go. Times are Unix nanoseconds, as list-queued
// and the Inbox give them.

// The stages, in the order a card usually moves through them. They are wire
// values. claim-task moves a card in todo or plan to doing, and finish-task
// moves it to done; every other move is set-task-stage's.
// They live in config, which validates [tasks] stages without this package.
const (
	TaskStageTodo    = config.TaskStageTodo
	TaskStagePlan    = config.TaskStagePlan
	TaskStageDoing   = config.TaskStageDoing
	TaskStageVerify  = config.TaskStageVerify
	TaskStageReview  = config.TaskStageReview
	TaskStageShipped = config.TaskStageShipped
	TaskStageDone    = config.TaskStageDone
)

// TaskStages is every stage, in order.
var TaskStages = config.TaskStages

// The actions a card's history records. They are wire values, and the ones
// task-changed carries in action.
const (
	TaskActionAdded     = "added"
	TaskActionClaimed   = "claimed"
	TaskActionFinished  = "finished"
	TaskActionStaged    = "staged"
	TaskActionBlocked   = "blocked"
	TaskActionUnblocked = "unblocked"
	TaskActionReleased  = "released"
	TaskActionRemoved   = "removed"
)

// The reasons a card is released without its holder asking.
const (
	taskReleasePaneClosed = "pane closed"
	taskReleaseAgentLeft  = "agent left"
	taskReleaseRestart    = "restart"
)

// taskByDaemon is the by of a change the daemon makes on its own.
const taskByDaemon = "tuios"

// Error codes of the task verbs.
const (
	// ErrVerbTaskHeld reports a claim of a card another pane holds.
	ErrVerbTaskHeld = "task_held"
	// ErrVerbNoTask reports a card id the session does not hold, or a
	// claim-task next that found nothing to take.
	ErrVerbNoTask = "no_task"
)

// Bounds on what one session keeps.
const (
	taskMaxTitle   = 200
	taskMaxPrompt  = queueMaxText
	taskMaxReason  = 500
	taskMaxCards   = 1000
	taskMaxHistory = 100
)

// TaskState is one card.
type TaskState struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Prompt string `json:"prompt,omitempty"`
	Stage  string `json:"stage"`
	// StageAt is when the card entered its stage.
	StageAt int64 `json:"stage_at"`
	// Holder is the window id of the pane that holds the card, and HeldAt
	// when it took it. Empty when no pane holds it.
	Holder string `json:"holder,omitempty"`
	HeldAt int64  `json:"held_at,omitempty"`
	// By is who added the card (see taskCaller), and At when.
	By      string     `json:"by"`
	At      int64      `json:"at"`
	Blocked *TaskBlock `json:"blocked,omitempty"`
	// Fan and FanID name the fan the card was added in: the session's
	// WorktreeInfo Group and FanID, empty outside a fan.
	Fan   string `json:"fan,omitempty"`
	FanID string `json:"fan_id,omitempty"`
	// History is every change to the card, oldest first, at most
	// taskMaxHistory entries: the first is kept and the oldest after it go.
	History []TaskEvent `json:"history,omitempty"`
}

// TaskBlock says why a card is stuck, who said so and when.
type TaskBlock struct {
	Reason string `json:"reason"`
	By     string `json:"by"`
	At     int64  `json:"at"`
}

// TaskEvent is one entry of a card's history.
type TaskEvent struct {
	Action string `json:"action"`
	By     string `json:"by"`
	At     int64  `json:"at"`
	Stage  string `json:"stage,omitempty"`
	Holder string `json:"holder,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// note appends ev to the card's history, keeping the first entry and the
// newest after it within taskMaxHistory. It builds a new slice, and Blocked is
// only ever replaced, so a snapshot shares a card's history and block with the
// state safely and copies the cards with slices.Clone alone.
func (tk *TaskState) note(ev TaskEvent) {
	first, rest := tk.History, []TaskEvent(nil)
	if n := len(tk.History); n >= taskMaxHistory {
		first, rest = tk.History[:1], tk.History[n-taskMaxHistory+2:]
	}
	tk.History = slices.Concat(first, rest, []TaskEvent{ev})
}

// setStage moves the card to stage at now.
func (tk *TaskState) setStage(stage string, now int64) {
	if tk.Stage != stage {
		tk.Stage, tk.StageAt = stage, now
	}
}

// finish moves the card to done and lets it go, lifting any block. It returns
// the holder it had, and false when the card was done already and nothing
// changed.
func (tk *TaskState) finish(by string, now int64) (string, bool) {
	if tk.Stage == TaskStageDone && tk.Holder == "" && tk.Blocked == nil {
		return "", false
	}
	holder := tk.Holder
	tk.setStage(TaskStageDone, now)
	tk.Holder, tk.HeldAt, tk.Blocked = "", 0, nil
	tk.note(TaskEvent{Action: TaskActionFinished, By: by, At: now, Stage: tk.Stage, Holder: holder})
	return holder, true
}

// release lets the card go, recording why.
func (tk *TaskState) release(by, reason string, now int64) {
	holder := tk.Holder
	tk.Holder, tk.HeldAt = "", 0
	tk.note(TaskEvent{Action: TaskActionReleased, By: by, At: now, Holder: holder, Stage: tk.Stage, Reason: reason})
}

// workable refuses a card no pane may start on: one held by a pane other
// than window, one that is done, and one that is blocked.
func (tk *TaskState) workable(window string) *verbError {
	switch {
	case tk.Holder != "" && tk.Holder != window:
		return hintedVerbError(ErrVerbTaskHeld, "task card "+tk.ID+" is held by window "+shortWindowID(tk.Holder), &VerbHint{
			Command: "tuios task claim --next",
			Detail:  "Nothing was claimed. Take another card with next, or wait for task-changed released.",
		})
	case tk.Stage == TaskStageDone:
		return invalidParam("id", "task card "+tk.ID+" is done, so there is nothing to claim")
	case tk.Blocked != nil:
		return hintedVerbError(ErrVerbInvalidParams, "task card "+tk.ID+" is blocked: "+tk.Blocked.Reason, &VerbHint{
			Param:   "id",
			Command: "tuios task block " + tk.ID + " --clear",
			Detail:  "Nothing was claimed. Clear the block first.",
		})
	}
	return nil
}

// take gives the card to window, moving it from todo or plan to doing. It
// reports false when window holds it already, and the refusal when the card
// is not there to take (workable).
func (tk *TaskState) take(window, by string, now int64) (bool, *verbError) {
	if tk.Holder == window {
		return false, nil
	}
	if verr := tk.workable(window); verr != nil {
		return false, verr
	}
	tk.Holder, tk.HeldAt = window, now
	if tk.Stage == TaskStageTodo || tk.Stage == TaskStagePlan {
		tk.setStage(TaskStageDoing, now)
	}
	tk.note(TaskEvent{Action: TaskActionClaimed, By: by, At: now, Stage: tk.Stage, Holder: window})
	return true, nil
}

// taskIndex is the index of the card id in tasks, or -1.
func taskIndex(tasks []TaskState, id string) int {
	return slices.IndexFunc(tasks, func(tk TaskState) bool { return tk.ID == id })
}

// nextFreeTask is the index of the card claim-task next takes: the oldest
// one no pane holds, not blocked and not done. A card a live fan works on
// from outside it (fan task=) is skipped too: its attempts hold it, though no
// one pane does. liveFans names the fans that still have a session. Cards are
// appended as they are added, so the first free one is the oldest. -1 when
// there is none.
func nextFreeTask(st *SessionState, liveFans map[string]bool) int {
	own := ""
	if st.Worktree != nil {
		own = st.Worktree.FanID
	}
	return slices.IndexFunc(st.Tasks, func(tk TaskState) bool {
		if tk.FanID != "" && tk.FanID != own && liveFans[tk.FanID] {
			return false
		}
		return tk.Holder == "" && tk.Blocked == nil && tk.Stage != TaskStageDone
	})
}

// newTaskID numbers the session's next card, t1 first. NextTaskID is saved
// with the cards, so an id is never handed out twice.
func newTaskID(st *SessionState) string {
	st.NextTaskID++
	return "t" + strconv.FormatUint(st.NextTaskID, 10)
}

// heldTasks maps each window that holds a card to the cards it holds, oldest
// first.
func heldTasks(tasks []TaskState) map[string][]TaskState {
	var out map[string][]TaskState
	for _, tk := range tasks {
		if tk.Holder == "" {
			continue
		}
		if out == nil {
			out = map[string][]TaskState{}
		}
		out[tk.Holder] = append(out[tk.Holder], tk)
	}
	return out
}

// releaseHolders releases every card whose holder held reports true, and
// returns the cards released as they are now.
func releaseHolders(st *SessionState, held func(holder string) bool, reason string, now int64) []TaskState {
	var out []TaskState
	for i := range st.Tasks {
		tk := &st.Tasks[i]
		if tk.Holder == "" || !held(tk.Holder) {
			continue
		}
		tk.release(taskByDaemon, reason, now)
		out = append(out, *tk)
	}
	return out
}

// releasedTaskEvents releases the cards of st held by a pane that events say
// closed or whose agent reported none, and returns one eventTaskReleased per
// card for the daemon's sink to publish. It runs inside the state change that
// raised events, with the state lock held, so a claim cannot land between the
// pane going and the card being let go. A pane that moved to another session
// left this one, so its cards are let go as closed.
func releasedTaskEvents(sess *Session, st *SessionState, events []SessionEvent, now int64) []SessionEvent {
	if len(st.Tasks) == 0 {
		return nil
	}
	var out []SessionEvent
	for _, ev := range events {
		var reason string
		switch {
		case ev.Type == EventWindowClosed:
			reason = taskReleasePaneClosed
		case ev.Type == EventAgentState && ev.State == AgentStateNone.Name():
			reason = taskReleaseAgentLeft
		default:
			continue
		}
		for _, tk := range releaseHolders(st, func(holder string) bool { return holder == ev.Window }, reason, now) {
			row := taskRow(sess, st, &tk)
			out = append(out, SessionEvent{Type: eventTaskReleased, Window: ev.Window, taskRow: &row, taskReason: reason})
		}
	}
	return out
}
