package session

import (
	"cmp"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/ghpr"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

type taskMove struct {
	from []string
	to   string
}

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

const (
	taskReasonPlanHeld      = "plan held for approval"
	taskReasonPlanApproved  = "plan approved"
	taskReasonVerifyStarted = "fan check started"
	taskReasonVerifyFailed  = "fan check failed in every attempt"
)

func (mv taskMove) applies(tk *TaskState) bool {
	return tk.Blocked == nil && tk.Stage != TaskStageDone && tk.Stage != mv.to && slices.Contains(mv.from, tk.Stage)
}

func autoStageLocked(sess *Session, st *SessionState, match func(*TaskState) bool, mv taskMove, reason string, now int64) []TaskState {
	var out []TaskState
	for i := range st.Tasks {
		tk := &st.Tasks[i]
		if !match(tk) {
			continue
		}
		if action, _, _ := sess.moveCard(tk, cardMove{kind: moveAuto, by: taskByDaemon, auto: mv, reason: reason}, now); action != "" {
			out = append(out, *tk)
		}
	}
	return out
}

// SetTaskAutoProbe installs what reports whether cards move on their own.
// The daemon installs it on every session.
func (s *Session) SetTaskAutoProbe(fn func() bool) {
	s.taskAuto.Store(&fn)
}

func (s *Session) taskAutoOn() bool {
	probe := s.taskAuto.Load()
	return probe != nil && (*probe)()
}

var errNoTaskMove = errors.New("no task card moved")

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
		moved := autoStageLocked(sess, st, match, mv, reason, time.Now().UnixNano())
		if len(moved) == 0 {
			return errNoTaskMove
		}
		for i := range moved {
			rows = append(rows, taskRow(sess, st, &moved[i]))
		}
		for _, row := range rows {
			d.taskEvent(sess, EventTaskChanged, TaskActionStaged, "", reason, row)
		}
		return nil
	})
	for _, row := range rows {
		LogBasic("Moved task %s in session %s to %s: %s", row.ID, sess.Name(), row.Stage, reason)
		d.afterTaskEvent(sess, TaskActionStaged, "", reason, row)
	}
}

func heldBy(window string) func(*TaskState) bool {
	return func(tk *TaskState) bool { return tk.Holder == window }
}

func (d *Daemon) notePlanAnswered(sess *Session, window, decision string) {
	if decision == ApprovalDeny {
		return
	}
	d.autoStage(sess, heldBy(window), movePlanApproved, taskReasonPlanApproved)
}

func (d *Daemon) autoStageFan(fanID string, mv taskMove, reason string) {
	if fanID == "" {
		return
	}
	for _, s := range d.manager.AllSessions() {
		d.autoStage(s, func(tk *TaskState) bool { return tk.FanID == fanID }, mv, reason)
	}
}

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
	d.autoStage(sess, func(tk *TaskState) bool {
		return tk.Holder != "" || (fanID != "" && tk.FanID == fanID)
	}, mv, reason)
	name := sess.Name()
	for _, s := range d.manager.AllSessions() {
		d.autoStage(s, func(tk *TaskState) bool {
			return (s != sess && fanID != "" && tk.FanID == fanID) ||
				(tk.Holder == "" && tk.Work != nil && tk.Work.Host == "" && tk.Work.Session == name)
		}, mv, reason)
	}
	return changed
}

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

type taskParam struct {
	sess   *Session
	id     string
	caller taskCaller
}

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
	raw, err := jsonx.Marshal(map[string]string{"session": session})
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
	if err := jsonx.Unmarshal(scoped, &sp); err != nil {
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
		if tp.sess.taskAutoOn() {
			action, _, _ := tp.sess.moveCard(tk, cardMove{kind: moveAuto, by: tp.caller.by, auto: moveFanned, reason: reason}, time.Now().UnixNano())
			moved = action != ""
		}
		row = taskRow(tp.sess, st, tk)
		if moved {
			d.taskEvent(tp.sess, EventTaskChanged, TaskActionStaged, "", reason, row)
		}
		return nil
	})
	if moved {
		d.afterTaskEvent(tp.sess, TaskActionStaged, "", reason, row)
	}
	return row, found
}

func (d *Daemon) claimForPane(tp *taskParam, window, name, harness string, cross bool) (TaskRow, *verbError) {
	auto := d.auto.expects(tp.sess.Name(), tp.id, false)
	row, action, spec, verr := d.changeTask(tp.sess, tp.id, func(_ *SessionState, tk *TaskState, now int64) (string, *verbError) {
		action, _, verr := tp.sess.moveCard(tk, cardMove{kind: moveClaim, by: cmp.Or(auto, tp.caller.by), window: window,
			dispatched: auto != "", names: cross, name: name, harness: harness}, now)
		return action, verr
	}, func(action string) taskEventSpec {
		if action == "" {
			return taskEventSpec{}
		}
		return taskEventSpec{EventTaskClaimed, TaskActionClaimed, window, ""}
	})
	if verr == nil && cross {
		d.remoteHolds.note(window, remoteHold{session: tp.sess.Name(), id: tp.id}, true)
	}
	if verr != nil {
		return TaskRow{}, verr
	}
	if action != "" {
		LogBasic("Window %s claimed task %s in session %s as it started", shortWindowID(window), row.ID, tp.sess.Name())
		d.afterTaskSpec(tp.sess, spec, row)
	}
	return row, nil
}
