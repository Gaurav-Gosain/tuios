package session

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/ghpr"
)

// Cards that move on their own, and the cards a fan works on.
//
// The daemon moves a card when it records a fact about the card's work: a
// plan held for the person and the person approving it, a fan check starting
// or failing everywhere, a pull request opening, merging or closing. It never
// reads pane output for it. Every move is a taskMove, and fires only when the
// card is in one of the move's from stages, so a card moved by hand to a stage
// no move starts from stays where it was put. A blocked card and a done card
// are never moved: the block is a decision someone made, and done is the
// person's or an agent's to set (finish-task). Each move writes a staged
// entry to the card's history with by tuios and the reason, and sends the
// task-changed staged event set-task-stage sends.
//
// A pane fact reaches the cards the pane holds: the plan hold is applied right
// after the hold opens, and the approval in the reply that gave it. A plan
// the person keeps planning, or a hold that ends with no answer through tuios
// (it timed out, or its caller went), leaves the card in plan: the daemon
// does not know what the agent does next. Its pull request is proof enough,
// so opening and merging move a card out of plan too. A fan fact reaches
// every card stamped with the fan's FanID, in any session: the cards added in
// the fan's sessions, and the card fan task= named. A pull request also
// reaches the cards held by the panes of its worktree's session.
//
// [tasks] auto_stage = false turns every move off. The fan stamp is still
// written, since it records what the fan is working on rather than moving
// anything.

// taskMove is one automatic move: the stages a card must be in for it to
// fire, and the stage it goes to.
type taskMove struct {
	from []string
	to   string
}

// The moves, by the fact behind each.
var (
	movePlanHeld      = taskMove{from: []string{TaskStageTodo, TaskStageDoing}, to: TaskStagePlan}
	movePlanApproved  = taskMove{from: []string{TaskStagePlan}, to: TaskStageDoing}
	moveFanned        = taskMove{from: []string{TaskStageTodo, TaskStagePlan}, to: TaskStageDoing}
	moveVerifyStarted = taskMove{from: []string{TaskStageDoing}, to: TaskStageVerify}
	moveVerifyFailed  = taskMove{from: []string{TaskStageVerify}, to: TaskStageDoing}
	movePROpened      = taskMove{from: []string{TaskStagePlan, TaskStageDoing, TaskStageVerify}, to: TaskStageReview}
	movePRMerged      = taskMove{from: []string{TaskStagePlan, TaskStageDoing, TaskStageVerify, TaskStageReview}, to: TaskStageShipped}
	movePRClosed      = taskMove{from: []string{TaskStageReview}, to: TaskStageDoing}
)

// The reasons a move records that are not built from the fact.
const (
	taskReasonPlanHeld      = "plan held for approval"
	taskReasonPlanApproved  = "plan approved"
	taskReasonVerifyStarted = "fan check started"
	taskReasonVerifyFailed  = "fan check failed in every attempt"
)

// applies reports whether mv moves tk.
func (mv taskMove) applies(tk *TaskState) bool {
	return tk.Blocked == nil && tk.Stage != TaskStageDone && tk.Stage != mv.to && slices.Contains(mv.from, tk.Stage)
}

// autoStageLocked moves every card of st that match picks and mv applies to,
// and returns the cards moved as they are now. The caller holds the state
// lock.
func autoStageLocked(st *SessionState, match func(*TaskState) bool, mv taskMove, reason string, now int64) []TaskState {
	var out []TaskState
	for i := range st.Tasks {
		tk := &st.Tasks[i]
		if !match(tk) || !mv.applies(tk) {
			continue
		}
		tk.setStage(mv.to, now)
		tk.note(TaskEvent{Action: TaskActionStaged, By: taskByDaemon, At: now, Stage: tk.Stage, Holder: tk.Holder, Reason: reason})
		out = append(out, *tk)
	}
	return out
}

// SetTaskAutoProbe installs what reports whether cards move on their own.
// The daemon installs it on every session.
func (s *Session) SetTaskAutoProbe(fn func() bool) {
	s.taskAuto.Store(&fn)
}

// taskAutoOn reports whether cards move on their own. A session no daemon
// wired moves none.
func (s *Session) taskAutoOn() bool {
	probe := s.taskAuto.Load()
	return probe != nil && (*probe)()
}

// errNoTaskMove ends a state change that moved no card, so it costs no state
// version and no broadcast.
var errNoTaskMove = errors.New("no task card moved")

// anyTask reports whether a card of the session passes fn, under the read
// lock, so a fact that reaches no card costs no state change.
func (s *Session) anyTask(fn func(*TaskState) bool) bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for i := range s.state.Tasks {
		if fn(&s.state.Tasks[i]) {
			return true
		}
	}
	return false
}

// autoStage applies mv to the cards of sess that match picks, in a state
// change of its own, and publishes a task-changed staged for each card moved.
// It takes the session's state lock, so it is never called under one.
func (d *Daemon) autoStage(sess *Session, match func(*TaskState) bool, mv taskMove, reason string) {
	if d.taskAutoOff.Load() {
		return
	}
	picked := func(tk *TaskState) bool { return match(tk) && mv.applies(tk) }
	if !sess.anyTask(picked) {
		return
	}
	var rows []TaskRow
	_ = sess.mutateState(func(st *SessionState) error {
		moved := autoStageLocked(st, match, mv, reason, time.Now().UnixNano())
		if len(moved) == 0 {
			return errNoTaskMove
		}
		for i := range moved {
			rows = append(rows, taskRow(sess, st, &moved[i]))
		}
		return nil
	})
	for _, row := range rows {
		LogBasic("Moved task %s in session %s to %s: %s", row.ID, sess.Name(), row.Stage, reason)
		d.publishTask(sess, EventTaskChanged, TaskActionStaged, "", reason, row)
	}
}

// heldBy picks the cards window holds.
func heldBy(window string) func(*TaskState) bool {
	return func(tk *TaskState) bool { return tk.Holder == window }
}

// notePlanAnswered moves the cards held by the pane of a plan hold out of
// plan when the answer let the plan through. Kept planning, they stay.
func (d *Daemon) notePlanAnswered(sess *Session, window, decision string) {
	if decision == ApprovalDeny {
		return
	}
	d.autoStage(sess, heldBy(window), movePlanApproved, taskReasonPlanApproved)
}

// autoStageFan applies mv to the cards stamped with the fan fanID, in every
// session.
func (d *Daemon) autoStageFan(fanID string, mv taskMove, reason string) {
	if fanID == "" {
		return
	}
	for _, s := range d.manager.AllSessions() {
		d.autoStage(s, func(tk *TaskState) bool { return tk.FanID == fanID }, mv, reason)
	}
}

// noteFanVerifyEnded moves the cards of sess's fan back to doing once every
// attempt's check has ended and none passed. While one runs nothing moves,
// and a pass anywhere leaves the cards in verify.
func (d *Daemon) noteFanVerifyEnded(sess *Session) {
	info := sess.Worktree()
	if info == nil || info.FanID == "" || d.taskAutoOff.Load() {
		return
	}
	for _, s := range d.fanSiblings(info) {
		wt := s.Worktree()
		if wt == nil {
			continue
		}
		v := d.fanVerifyReport(s, wt.Verify)
		if v != nil && (v.State == VerifyRunning || v.State == VerifyPassed) {
			return
		}
	}
	d.autoStageFan(info.FanID, moveVerifyFailed, taskReasonVerifyFailed)
}

// notePR keeps pr on the session's worktree record, as setWorktreePR does,
// and moves the cards the pull request reaches when its state changed. It
// reports whether the record changed.
func (d *Daemon) notePR(sess *Session, root string, pr *ghpr.PR) bool {
	changed, was := sess.setWorktreePR(root, pr)
	if !changed || pr == nil || pr.State == was {
		return changed
	}
	var (
		mv     taskMove
		reason = "pull request #" + strconv.Itoa(pr.Number) + " "
	)
	switch pr.State {
	case ghpr.StateOpen:
		mv, reason = movePROpened, reason+"opened"
	case ghpr.StateMerged:
		mv, reason = movePRMerged, reason+"merged"
	case ghpr.StateClosed:
		mv, reason = movePRClosed, reason+"closed without a merge"
	default:
		return changed
	}
	fanID := ""
	if wt := sess.Worktree(); wt != nil {
		fanID = wt.FanID
	}
	// The cards held in the worktree's own session, whichever fan they name.
	d.autoStage(sess, func(tk *TaskState) bool {
		return tk.Holder != "" || (fanID != "" && tk.FanID == fanID)
	}, mv, reason)
	if fanID == "" {
		return changed
	}
	for _, s := range d.manager.AllSessions() {
		if s != sess {
			d.autoStage(s, func(tk *TaskState) bool { return tk.FanID == fanID }, mv, reason)
		}
	}
	return changed
}

// liveFans is the FanID of every fan that still has a session. claim-task
// next skips a card a fan works on from outside it (nextFreeTask). It is read
// before the claim's state change, since it reads every session's state.
func (d *Daemon) liveFans() map[string]bool {
	var out map[string]bool
	for _, s := range d.manager.AllSessions() {
		if wt := s.Worktree(); wt != nil && wt.FanID != "" {
			if out == nil {
				out = map[string]bool{}
			}
			out[wt.FanID] = true
		}
	}
	return out
}

// taskParam is a card a verb that starts agents was asked to work on: fan
// stamps it with the fan it makes, and start-agent claims it for the pane it
// opens.
type taskParam struct {
	sess   *Session
	id     string
	caller taskCaller
}

// admitTaskParam checks the task a verb that starts agents was given, before
// the verb makes anything: the caller may change the cards of session, as
// claim-task would let it, and the card is there to work on. The card may be
// held by no pane, or with callerMayHold by the calling pane: fan leaves the
// holder alone, and start-agent hands the card to the pane it opens.
func (d *Daemon) admitTaskParam(cs *connState, verb, session, id, nonce string, callerMayHold bool) (*taskParam, *verbError) {
	refused := func(verr *verbError) *verbError {
		verr.Message = verb + " with task: " + verr.Message
		if verr.Hint == nil {
			verr.Hint = &VerbHint{}
		}
		verr.Hint.Param = "task"
		return verr
	}
	if verr := d.checkLinkVerb(cs, "claim-task"); verr != nil {
		return nil, refused(verr)
	}
	raw, err := json.Marshal(map[string]string{"session": session})
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode the task's session")
	}
	granted, verr := d.checkGrants(cs, "claim-task", raw)
	if verr != nil {
		return nil, refused(verr)
	}
	scoped, verr := d.checkScope(cs, "claim-task", granted)
	if verr != nil {
		return nil, refused(verr)
	}
	var sp struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(scoped, &sp); err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not read the task's session")
	}
	sess, verr := d.resolveVerbSession(cs, sp.Session)
	if verr != nil {
		return nil, refused(verr)
	}
	caller, verr := d.taskCallerOf(cs, verb, sess, nonce)
	if verr != nil {
		return nil, refused(verr)
	}
	var tk TaskState
	if !sess.anyTask(func(t *TaskState) bool {
		if t.ID != id {
			return false
		}
		tk = *t
		return true
	}) {
		return nil, refused(noTask(sess, id))
	}
	holder := ""
	if callerMayHold {
		holder = caller.window
	}
	if verr := tk.workable(holder); verr != nil {
		return nil, refused(verr)
	}
	return &taskParam{sess: sess, id: id, caller: caller}, nil
}

// stampFan writes the fan stem and fanID onto the card, and moves it from
// todo or plan to doing. It returns the card's row, or false when the card
// was removed while the fan was made.
func (d *Daemon) stampFan(tp *taskParam, stem, fanID string) (TaskRow, bool) {
	var (
		row   TaskRow
		found bool
		moved bool
	)
	reason := "fanned out to " + stem
	_ = tp.sess.mutateState(func(st *SessionState) error {
		i := taskIndex(st.Tasks, tp.id)
		if i < 0 {
			return errTaskRefused
		}
		found = true
		tk := &st.Tasks[i]
		tk.Fan, tk.FanID = stem, fanID
		if tp.sess.taskAutoOn() && moveFanned.applies(tk) {
			now := time.Now().UnixNano()
			tk.setStage(moveFanned.to, now)
			tk.note(TaskEvent{Action: TaskActionStaged, By: tp.caller.by, At: now, Stage: tk.Stage, Holder: tk.Holder, Reason: reason})
			moved = true
		}
		row = taskRow(tp.sess, st, tk)
		return nil
	})
	if moved {
		d.publishTask(tp.sess, EventTaskChanged, TaskActionStaged, "", reason, row)
	}
	return row, found
}

// claimForPane gives the card to window, the pane start-agent opened for it.
func (d *Daemon) claimForPane(tp *taskParam, window string) (TaskRow, *verbError) {
	row, action, verr := d.changeTask(tp.sess, tp.id, func(_ *SessionState, tk *TaskState, now int64) (string, *verbError) {
		taken, verr := tk.take(window, tp.caller.by, now)
		if verr != nil || !taken {
			return "", verr
		}
		return TaskActionClaimed, nil
	})
	if verr != nil {
		return TaskRow{}, verr
	}
	if action != "" {
		LogBasic("Window %s claimed task %s in session %s as it started", shortWindowID(window), row.ID, tp.sess.Name())
		d.publishTask(tp.sess, EventTaskClaimed, TaskActionClaimed, window, "", row)
	}
	return row, nil
}
