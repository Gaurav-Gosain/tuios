package session

import (
	"slices"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

type cardMoveKind uint8

const (
	moveClaim cardMoveKind = iota + 1
	moveRelease
	moveBlock
	moveUnblock
	moveBlockErrored
	moveUnblockErrored
	moveHandIn
	moveFinish
	moveStage
	moveAuto
	moveVerified
	moveAttach
	moveDetach
)

type cardMove struct {
	kind              cardMoveKind
	by, reason        string
	window            string
	dispatched, names bool
	name, harness     string
	refresh           bool
	stage             string
	work              *TaskWork
	verifyRun         bool
	auto              taskMove
	token             int64
	verify            *TaskVerify
	note              string
	attachments       []TaskAttachment
}

func (tk *TaskState) apply(m cardMove, now int64) (action, holder string, verr *verbError) {
	holder = tk.Holder
	to := tk.Stage
	letGo := func() {
		tk.Holder, tk.HeldAt, tk.HolderName, tk.HolderHarness = "", 0, "", ""
	}
	evHolder := holder
	switch m.kind {
	case moveClaim:
		if tk.Holder == m.window {
			if m.refresh {
				tk.Dispatched = m.dispatched
				if m.names {
					tk.HolderName, tk.HolderHarness = m.name, m.harness
				}
			}
			return "", holder, nil
		}
		if verr := tk.workable(m.window); verr != nil {
			return "", holder, verr
		}
		tk.Holder, tk.HeldAt, tk.Dispatched = m.window, now, m.dispatched
		if m.names {
			tk.HolderName, tk.HolderHarness = m.name, m.harness
		}
		if to == TaskStageTodo || to == TaskStagePlan {
			to = TaskStageDoing
		}
		action, evHolder = TaskActionClaimed, m.window

	case moveRelease:
		letGo()
		if tk.blockedOnErrored() {
			tk.Blocked, to = nil, TaskStageTodo
		}
		action = TaskActionReleased

	case moveBlock, moveBlockErrored:
		if m.kind == moveBlockErrored {
			if tk.Holder == "" || !tk.Dispatched || tk.Blocked != nil {
				return "", holder, nil
			}
			tk.Attempts++
			tk.Note = m.note
		}
		tk.Blocked = &TaskBlock{Reason: m.reason, By: m.by, At: now}
		action = TaskActionBlocked

	case moveUnblock, moveUnblockErrored:
		if tk.Blocked == nil || m.kind == moveUnblockErrored && !tk.blockedOnErrored() {
			return "", holder, nil
		}
		tk.Blocked = nil
		action = TaskActionUnblocked

	case moveHandIn:
		letGo()
		tk.Blocked, to = nil, m.stage
		if m.work != nil {
			tk.Work = m.work
		}
		if to == TaskStageVerify {
			tk.Verify = &TaskVerify{State: VerifyRunning, StartedAt: now}
		}
		action = TaskActionFinished

	case moveFinish:
		if to == TaskStageDone && tk.Holder == "" && tk.Blocked == nil {
			return "", holder, nil
		}
		letGo()
		tk.Blocked, to = nil, TaskStageDone
		action = TaskActionFinished

	case moveStage:
		if to == m.stage {
			return "", holder, nil
		}
		if m.verifyRun {
			if m.work != nil {
				tk.Work = m.work
			}
			tk.Verify = &TaskVerify{State: VerifyRunning, StartedAt: now}
			letGo()
		}
		to, action = m.stage, TaskActionStaged

	case moveAuto:
		if !m.auto.applies(tk) {
			return "", holder, nil
		}
		to, action = m.auto.to, TaskActionStaged

	case moveVerified:
		if to != TaskStageVerify || tk.Verify == nil || tk.Verify.StartedAt != m.token {
			return "", holder, nil
		}
		if m.stage == TaskStageTodo {
			tk.Attempts++
			tk.Note = m.note
		}
		letGo()
		tk.Verify, to = m.verify, m.stage
		action, evHolder = TaskActionVerified, ""

	case moveAttach:
		list := slices.Clone(tk.Attachments)
		for _, a := range m.attachments {
			list = slices.DeleteFunc(list, func(b TaskAttachment) bool { return b.Name == a.Name })
			list = append(list, a)
		}
		tk.Attachments = list
		action = TaskActionAttached

	case moveDetach:
		i := slices.IndexFunc(tk.Attachments, func(a TaskAttachment) bool { return a.Name == m.reason })
		if i < 0 {
			return "", holder, nil
		}
		tk.Attachments = slices.Delete(slices.Clone(tk.Attachments), i, i+1)
		action = TaskActionDetached

	default:
		return "", holder, nil
	}
	if tk.Stage != to {
		tk.Stage, tk.StageAt = to, now
	}
	tk.note(TaskEvent{Action: action, By: m.by, At: now, Stage: to, Holder: evHolder, Reason: m.reason})
	return action, holder, nil
}

func (s *Session) moveCard(tk *TaskState, m cardMove, now int64) (action, holder string, verr *verbError) {
	from := tk.Stage
	action, holder, verr = tk.apply(m, now)
	if action == "" || verr != nil {
		return action, holder, verr
	}
	if debugEnabled() {
		debugLog("[DEBUG] Task %s in session %s: %s %s -> %s, holder %q -> %q, by %s", tk.ID, s.Name(), action, from, tk.Stage, holder, tk.Holder, m.by)
	}
	if finished, counts := dispatchOutcome(m, action, tk); counts {
		if fn := s.taskOutcome.Load(); fn != nil {
			(*fn)(dispatchBoardKey(s.Name(), tk.Project), finished)
		}
	}
	if cardCheckOn() {
		for _, broken := range cardRulesBroken(tk, m) {
			LogBasic("Task invariant broken: card %s in session %s after %s by %s: %s", tk.ID, s.Name(), action, m.by, broken)
		}
	}
	return action, holder, nil
}

func dispatchOutcome(m cardMove, action string, tk *TaskState) (finished, counts bool) {
	if !tk.Dispatched || m.reason == taskReleaseRestart {
		return false, false
	}
	switch action {
	case TaskActionFinished, TaskActionVerified:
		if tk.Stage == TaskStageVerify {
			return false, false
		}
		return tk.Stage != TaskStageTodo, true
	case TaskActionReleased:
		return false, true
	case TaskActionBlocked:
		return false, m.kind != moveBlockErrored
	}
	return false, false
}

// SetTaskOutcomeSink installs what counts a dispatched card's outcome on
// its board. The daemon installs it on every session.
func (s *Session) SetTaskOutcomeSink(fn func(board string, finished bool)) {
	s.taskOutcome.Store(&fn)
}

var cardCheckOn = sync.OnceValue(func() bool { return testknob.String("card_check") != "" })

func cardRulesBroken(tk *TaskState, m cardMove) []string {
	var out []string
	if !slices.Contains(TaskStages, tk.Stage) {
		out = append(out, "stage "+tk.Stage+" is not a stage")
	}
	if tk.Stage == TaskStageDone && (tk.Holder != "" || tk.Blocked != nil) {
		out = append(out, "a done card is held or blocked")
	}
	if tk.Holder != "" && (m.kind == moveHandIn || m.kind == moveVerified || m.kind == moveStage && m.verifyRun) {
		out = append(out, "the card is still held after a move that lets it go")
	}
	if tk.Holder == "" && (tk.HeldAt != 0 || tk.HolderName != "" || tk.HolderHarness != "") {
		out = append(out, "a card with no holder keeps its holder's time or name")
	}
	if tk.blockedOnErrored() && tk.Holder == "" {
		out = append(out, "a card blocked on its errored agent has no holder")
	}
	return out
}

const taskBlockErrored = "agent errored"

func (tk *TaskState) blockedOnErrored() bool {
	return tk.Blocked != nil && tk.Blocked.By == taskByDaemon && strings.HasPrefix(tk.Blocked.Reason, taskBlockErrored)
}

func (tk TaskState) pushCopy() TaskState {
	tk.Prompt = ""
	tk.History = nil
	tk.Attachments = nil
	if tk.Verify != nil {
		v := *tk.Verify
		v.Tail = ""
		tk.Verify = &v
	}
	return tk
}
