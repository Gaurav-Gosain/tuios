package session

import (
	"os"
	"slices"
	"strings"
	"sync"
)

// The task card lifecycle as one transition function. A card's lifecycle
// fields (Stage, StageAt, Holder, HeldAt, HolderName, HolderHarness,
// Dispatched, Blocked, Attempts, Note, Work, Verify and Attachments) are
// written here and
// nowhere else: internal/lint TestOnlyTaskMoveWritesACardsLifecycle holds it.
// A verb keeps its own checks (who may touch the card, the pane refusals, the
// verify command rules) and asks for a move; Session.moveCard makes it,
// counts a dispatched card's outcome on its board in the same state change,
// logs it at debug level, and under TUIOS_TEST_CARD_CHECK checks the card's
// rules after it (cardRulesBroken).

// cardMoveKind is what a move does.
type cardMoveKind uint8

const (
	// moveClaim gives the card to window: todo and plan go to doing.
	moveClaim cardMoveKind = iota + 1
	// moveRelease lets the card go. A card blocked on its errored agent
	// goes back to todo with the block lifted.
	moveRelease
	// moveBlock marks the card stuck, keeping its holder.
	moveBlock
	// moveUnblock lifts any block.
	moveUnblock
	// moveBlockErrored blocks a dispatched card whose agent errored: one
	// attempt more, the agent's line as the note.
	moveBlockErrored
	// moveUnblockErrored lifts only the block moveBlockErrored put.
	moveUnblockErrored
	// moveHandIn lets the card go as its agent is done with it, to verify
	// (with a running record) or review.
	moveHandIn
	// moveFinish closes the card: done, no holder, no block.
	moveFinish
	// moveStage is a person's move to a stage other than done. A move to
	// verify that runs the board's command lets the holder go: the card
	// waits on the command, not on a pane.
	moveStage
	// moveAuto is an automatic move (task_auto.go) when it applies.
	moveAuto
	// moveVerified lands a verify result on a card still in verify with
	// the run's token.
	moveVerified
	// moveAttach puts attachment on the card, in the place of one with its
	// name.
	moveAttach
	// moveDetach takes the attachment named reason off the card.
	moveDetach
)

// cardMove is one move and what it needs.
type cardMove struct {
	kind       cardMoveKind
	by, reason string
	// Claim: the pane, whether dispatch made the claim, and the name and
	// harness of a holder the card's session cannot read (names). refresh
	// updates dispatched and the names on a claim the same holder makes again.
	window            string
	dispatched, names bool
	name, harness     string
	refresh           bool
	stage             string    // HandIn, Stage, Verified: where the card goes
	work              *TaskWork // HandIn, Stage
	verifyRun         bool      // Stage: run the board's verify command
	auto              taskMove  // Auto
	token             int64     // Verified
	verify            *TaskVerify
	note              string          // BlockErrored, Verified: the card's note
	attachment        *TaskAttachment // Attach
}

// apply makes m on the card at now. It returns the action the history and
// the event name, "" when nothing changed, the holder the card had before,
// and the refusal of a claim. Each move that changes the card notes one
// history entry: the action, m.by, the stage after, the holder (the new one
// for a claim, none for a verify result, else the one before) and m.reason.
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
		// A pane that took the card in verify lets it go with the result.
		letGo()
		tk.Verify, to = m.verify, m.stage
		action, evHolder = TaskActionVerified, ""

	case moveAttach:
		// A new slice: a snapshot shares the old one.
		list := slices.DeleteFunc(slices.Clone(tk.Attachments), func(a TaskAttachment) bool { return a.Name == m.attachment.Name })
		tk.Attachments = append(list, *m.attachment)
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

// moveCard makes m on tk, a card of s, inside the caller's state change,
// and does what follows from it in the same change: the outcome of a
// dispatched card counts on its board, so two cards of one board count in
// the order their moves land; a debug line names the move; and under
// TUIOS_TEST_CARD_CHECK the card's rules are checked.
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

// dispatchOutcome is how a move ends a dispatched card on its board: counts
// says it ends it, and finished says well. A finish into verify waits for the
// verify. A restart, and a block on an errored agent (which may go on),
// count nothing; the errored card counts when it is let go.
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

// cardCheckOn reports whether TUIOS_TEST_CARD_CHECK asks for the checks.
var cardCheckOn = sync.OnceValue(func() bool { return os.Getenv("TUIOS_TEST_CARD_CHECK") != "" })

// cardRulesBroken is every rule of the lifecycle tk breaks after m.
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

// taskBlockErrored starts the reason of a block dispatch puts on a card
// whose agent errored.
const taskBlockErrored = "agent errored"

// blockedOnErrored reports whether the card's block is the one dispatch put
// on it when its agent errored.
func (tk *TaskState) blockedOnErrored() bool {
	return tk.Blocked != nil && tk.Blocked.By == taskByDaemon && strings.HasPrefix(tk.Blocked.Reason, taskBlockErrored)
}

// pushCopy is the card as a state push sends it: a copy with its prompt and
// history cleared and, when a verify record is set, a copy of that record with
// its output tail cleared. It is a copy for a push and moves no card. Verify
// points into the daemon's state, so the record is copied, never written
// through; tk and everything it points to stay as they were.
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
