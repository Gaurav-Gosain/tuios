package session

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// Task dispatch: agents that pick work.
//
// Each board has a dispatch mode. A board is a project's cards (the card's
// project URL) or a session's own cards ("session:NAME"). Manual, the
// default, starts nothing. Pull offers an agent that finishes its card the
// next open card of its board: the oldest card in todo, plan or doing (one
// let go mid-work) that no pane holds, that is not blocked, not marked ask_first, and names no harness or
// the agent's own. The card is claimed for the agent's pane in one state
// change of its session, so two agents that finish at once never take one
// card, and the agent hears of it as mail from tuios, which the hook notice
// or the mail wake tells it about. Dispatch types nothing into a pane, and it
// never offers a card to a pane that waits on the person, so a card can never
// read as the answer to an approval.
//
// An agent finishes its card with finish-task (tuios task done), set-task-stage
// done, or, on a pull board, by going to done while it holds the card: the
// daemon then finishes the card for it.
//
// The safety switches stop dispatch, and only a person (or a shell outside
// every pane, a plugin, or a pane holding admin) starts it again:
//   - Pause stops all dispatch at once. Anyone may pause.
//   - Quiet hours ("22:00-07:00", local time) hold every offer while they run.
//   - A board stops after dispatchMaxFailures dispatched cards in a row end
//     without being finished: let go (pane closed, agent left, released),
//     blocked, or put back because the agent errored.
//   - All dispatch stops when an agent reports a usage limit: set-agent-state
//     errored with kind usage_limit, or an errored message that says so.
//
// v1 limits: the agent and the card are on this daemon and in one session. A
// card is let go by its own session when its pane closes, so a pane never
// holds a card of another session.
//
// The modes and switches are saved in tasks/dispatch.json, so they survive a
// restart.

// The dispatch modes of a board.
const (
	DispatchManual = "manual"
	DispatchPull   = "pull"
	DispatchAuto   = "auto"
)

// DispatchModes are the modes set-task-dispatch takes.
var DispatchModes = []string{DispatchManual, DispatchPull, DispatchAuto}

// The actions set-task-dispatch takes.
const (
	dispatchActionMode   = "mode"
	dispatchActionPause  = "pause"
	dispatchActionResume = "resume"
	dispatchActionQuiet  = "quiet"
	dispatchActionPreset = "preset"
	dispatchActionCap    = "cap"
	dispatchActionVerify = "verify"
)

// dispatchActions are the actions set-task-dispatch takes.
var dispatchActions = []string{dispatchActionMode, dispatchActionPause, dispatchActionResume, dispatchActionQuiet, dispatchActionPreset, dispatchActionCap, dispatchActionVerify}

// taskByDispatch is the by of a claim dispatch makes.
const taskByDispatch = "dispatch"

// dispatchMaxFailures is how many dispatched cards in a row may end without
// being finished before the board stops.
const dispatchMaxFailures = 3

// AgentKindUsageLimit is the kind of an errored report that says the agent
// hit its harness's usage limit.
const AgentKindUsageLimit = "usage_limit"

// EventTaskDispatch says a dispatch mode or switch changed. Dispatch carries
// the whole status, and Reason why dispatch stopped on its own.
const EventTaskDispatch = "task-dispatch"

// The reasons dispatch stops on its own.
const (
	dispatchStopFailures = "3 failures in a row"
	dispatchStopLimit    = "usage limit"
)

// sessionBoardPrefix starts the board key of a session's own cards.
const sessionBoardPrefix = "session:"

// dispatchBoard is one board's dispatch settings.
type dispatchBoard struct {
	Mode     string `json:"mode,omitempty"`
	Failures int    `json:"failures,omitempty"`
	Stopped  string `json:"stopped,omitempty"`
	// Preset is what auto starts each card's agent with.
	Preset *DispatchPreset `json:"preset,omitempty"`
	// Verify is the shell line the daemon runs in a card's worktree when
	// its agent is done with it, and VerifyTimeout its limit in seconds,
	// 0 for the default (task_verify.go). Only the person sets them.
	Verify        string `json:"verify,omitempty"`
	VerifyTimeout int    `json:"verify_timeout,omitempty"`
}

// keep reports whether the board holds a setting, so a board back to
// manual with none is dropped from the file.
func (b *dispatchBoard) keep() bool {
	return (b.Mode != "" && b.Mode != DispatchManual) || b.Failures > 0 || b.Stopped != "" || b.Preset != nil || b.Verify != ""
}

// DispatchPreset is how auto starts a board's agents: the agent, its
// choices, and the machine. An empty permission mode is the harness's own
// default, so auto turns approvals off only when the person named a mode
// that does.
type DispatchPreset struct {
	Agent          string `json:"agent"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	// Machine is "here" (the default), "any" (placement picks), or a
	// [hosts] name. A card's own machine comes first.
	Machine string `json:"machine,omitempty"`
}

// dispatchFile is the saved form of the dispatch settings.
type dispatchFile struct {
	Version  int                       `json:"version"`
	Paused   bool                      `json:"paused,omitempty"`
	PausedBy string                    `json:"paused_by,omitempty"`
	PausedAt int64                     `json:"paused_at,omitempty"`
	Stopped  string                    `json:"stopped,omitempty"`
	Quiet    string                    `json:"quiet,omitempty"`
	Boards   map[string]*dispatchBoard `json:"boards,omitempty"`
	// Cap and MachineCap bound the agents auto runs at once, in total and
	// on one machine. 0 is the default.
	Cap        int `json:"cap,omitempty"`
	MachineCap int `json:"machine_cap,omitempty"`
}

// dispatchStore holds the dispatch settings. The daemon keeps one.
type dispatchStore struct {
	mu   sync.Mutex
	path string
	f    dispatchFile
	// pullQuiet runs offerPull when the quiet hours end. It is armed only
	// while quiet hours hold an offer of a pull board.
	pullQuiet *time.Timer
}

// DispatchStatus is what get-task-dispatch and the task-dispatch event give.
type DispatchStatus struct {
	Type     string `json:"type,omitempty"`
	Paused   bool   `json:"paused"`
	PausedBy string `json:"paused_by,omitempty"`
	PausedAt int64  `json:"paused_at,omitempty"`
	// Stopped is why all dispatch stopped on its own, empty when it did not.
	Stopped  string `json:"stopped,omitempty"`
	Quiet    string `json:"quiet,omitempty"`
	QuietNow bool   `json:"quiet_now"`
	// Cap and MachineCap bound the agents auto runs at once, in total and
	// on one machine, and Running is how many it runs now.
	Cap        int `json:"cap"`
	MachineCap int `json:"machine_cap"`
	Running    int `json:"running"`
	// Boards are the boards with a mode or a stop, in key order. A board
	// not listed is manual.
	Boards []DispatchBoardRow `json:"boards"`
	// Board is the board the call named, also when it is manual.
	Board *DispatchBoardRow `json:"board,omitempty"`
}

// DispatchBoardRow is one board's dispatch settings.
type DispatchBoardRow struct {
	Board    string `json:"board"`
	Project  string `json:"project,omitempty"`
	Session  string `json:"session,omitempty"`
	Mode     string `json:"mode"`
	Failures int    `json:"failures,omitempty"`
	Stopped  string `json:"stopped,omitempty"`
	// Preset is an auto board's preset. Running is the agents auto runs for
	// the board, and Waiting the open cards that wait for a free machine;
	// Note says why the last start did not happen, when one did not.
	Preset  *DispatchPreset `json:"preset,omitempty"`
	Running int             `json:"running,omitempty"`
	Waiting int             `json:"waiting,omitempty"`
	Note    string          `json:"note,omitempty"`
	// Verify is the board's verify command and VerifyTimeout its limit in
	// seconds.
	Verify        string `json:"verify,omitempty"`
	VerifyTimeout int    `json:"verify_timeout,omitempty"`
}

// The default caps of auto.
const (
	dispatchDefaultCap        = 3
	dispatchDefaultMachineCap = 2
	dispatchCapMax            = 64
)

// caps is the total and per-machine cap. mu is held.
func (ds *dispatchStore) capsLocked() (int, int) {
	return cmp.Or(ds.f.Cap, dispatchDefaultCap), cmp.Or(ds.f.MachineCap, dispatchDefaultMachineCap)
}

// dispatchPath is where the settings are saved.
func dispatchPath() string {
	return filepath.Join(getResurrectionDir(), "tasks", "dispatch.json")
}

// dispatchBoardKey is the board of a card of project in sessionName.
func dispatchBoardKey(sessionName, project string) string {
	if project != "" {
		return project
	}
	return sessionBoardPrefix + sessionName
}

// load reads the saved settings. A missing or broken file leaves every board
// manual and dispatch running.
func (ds *dispatchStore) load(path string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.path = path
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var f dispatchFile
	if err := jsonx.Unmarshal(data, &f); err != nil {
		LogError("Failed to read the task dispatch settings: %v", err)
		return
	}
	ds.f = f
}

// saveLocked writes the settings. It holds mu.
func (ds *dispatchStore) saveLocked() {
	if ds.path == "" {
		return
	}
	ds.f.Version = 1
	data, err := jsonx.Marshal(ds.f)
	if err != nil {
		return
	}
	if err := atomicfile.WritePrivate(ds.path, data, false); err != nil {
		LogError("Failed to save the task dispatch settings: %v", err)
	}
}

// boardLocked is the board key's settings, made when make is set. It holds mu.
func (ds *dispatchStore) boardLocked(key string, make bool) *dispatchBoard {
	if b := ds.f.Boards[key]; b != nil || !make {
		return b
	}
	if ds.f.Boards == nil {
		ds.f.Boards = map[string]*dispatchBoard{}
	}
	b := &dispatchBoard{}
	ds.f.Boards[key] = b
	return b
}

// rowOf is board key's row.
func rowOf(key string, b *dispatchBoard) DispatchBoardRow {
	row := DispatchBoardRow{Board: key, Mode: DispatchManual}
	if s, ok := strings.CutPrefix(key, sessionBoardPrefix); ok {
		row.Session = s
	} else {
		row.Project = key
	}
	if b != nil {
		row.Mode = firstNonEmpty(b.Mode, DispatchManual)
		row.Failures, row.Stopped = b.Failures, b.Stopped
		if b.Mode == DispatchAuto {
			row.Preset = b.Preset
		}
		if b.Verify != "" {
			row.Verify, row.VerifyTimeout = b.Verify, verifyTimeoutOf(b.VerifyTimeout)
		}
	}
	return row
}

// status is the settings as get-task-dispatch gives them, with board's row
// when board is not empty.
func (ds *dispatchStore) status(board string, now time.Time) DispatchStatus {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	st := DispatchStatus{
		Paused: ds.f.Paused, PausedBy: ds.f.PausedBy, PausedAt: ds.f.PausedAt,
		Stopped: ds.f.Stopped, Quiet: ds.f.Quiet, QuietNow: inQuietHours(ds.f.Quiet, now),
		Boards: []DispatchBoardRow{},
	}
	st.Cap, st.MachineCap = ds.capsLocked()
	keys := make([]string, 0, len(ds.f.Boards))
	for k := range ds.f.Boards {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		st.Boards = append(st.Boards, rowOf(k, ds.f.Boards[k]))
	}
	if board != "" {
		row := rowOf(board, ds.f.Boards[board])
		st.Board = &row
	}
	return st
}

// dispatchQuietHours is why blocked holds an offer in the quiet hours.
const dispatchQuietHours = "quiet hours"

// blocked says why dispatch in mode makes no offer on board now, "" when
// it may.
func (ds *dispatchStore) blocked(board, mode string, now time.Time) string {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	b := ds.f.Boards[board]
	switch {
	case b == nil || b.Mode != mode:
		return "the board is not in " + mode
	case ds.f.Paused:
		return "dispatch is paused"
	case ds.f.Stopped != "":
		return "dispatch stopped: " + ds.f.Stopped
	case b.Stopped != "":
		return "the board stopped: " + b.Stopped
	case inQuietHours(ds.f.Quiet, now):
		return dispatchQuietHours
	}
	return ""
}

// isMode reports whether board is in one of modes.
func (ds *dispatchStore) isMode(board string, modes ...string) bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	b := ds.f.Boards[board]
	return b != nil && slices.Contains(modes, b.Mode)
}

// noteOutcome records how a dispatched card of board ended: finished resets
// the count, anything else adds one. It returns true when this stopped the
// board.
func (ds *dispatchStore) noteOutcome(board string, finished bool) bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	b := ds.boardLocked(board, !finished)
	if b == nil {
		return false
	}
	if finished {
		if b.Failures == 0 {
			return false
		}
		b.Failures = 0
		ds.saveLocked()
		return false
	}
	b.Failures++
	stopped := b.Failures >= dispatchMaxFailures && b.Stopped == ""
	if stopped {
		b.Stopped = dispatchStopFailures
	}
	ds.saveLocked()
	return stopped
}

// stopAll stops all dispatch for reason. It returns false when it was
// stopped already.
func (ds *dispatchStore) stopAll(reason string) bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.f.Stopped != "" {
		return false
	}
	ds.f.Stopped = reason
	ds.saveLocked()
	return true
}

// anyMode reports whether a board is in one of modes, so an event that
// could start dispatch costs nothing on a daemon with none.
func (ds *dispatchStore) anyMode(modes ...string) bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	for _, b := range ds.f.Boards {
		if slices.Contains(modes, b.Mode) {
			return true
		}
	}
	return false
}

// parseQuietHours checks a quiet hours span, "HH:MM-HH:MM".
func parseQuietHours(s string) (from, to int, ok bool) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, false
	}
	from, ok1 := parseClock(a)
	to, ok2 := parseClock(b)
	return from, to, ok1 && ok2 && from != to
}

// parseClock is "HH:MM" as minutes after midnight.
func parseClock(s string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || len(h) == 0 || len(h) > 2 || len(m) != 2 {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// inQuietHours reports whether now is inside span. A span that ends before
// it starts runs across midnight.
func inQuietHours(span string, now time.Time) bool {
	from, to, ok := parseQuietHours(span)
	if !ok {
		return false
	}
	m := now.Hour()*60 + now.Minute()
	if from < to {
		return m >= from && m < to
	}
	return m >= from || m < to
}

// publishDispatch sends the task-dispatch event with the status now.
func (d *Daemon) publishDispatch(action, reason string) {
	st := d.dispatch.status("", time.Now())
	d.autoStatus(&st)
	d.events.publish(streamEvent{Type: EventTaskDispatch, Action: action, Reason: reason, Dispatch: &st})
}

// afterTaskEvent is what dispatch does with a task event: after a finish it
// offers the pane that held the card its next one. A dispatched card's
// outcome counted in the move itself (task_move.go). It runs off the caller's goroutine, since a
// release is published from the session's event sink under the state lock.
func (d *Daemon) afterTaskEvent(sess *Session, action, window, reason string, row TaskRow) {
	d.kickAuto()
	if row.Stage != TaskStageVerify {
		// A card moved out of verify ends the verify that ran on it.
		d.verifies.stop(verifyKey(row.Session, row.ID), 0)
	}
	switch action {
	case TaskActionAdded, TaskActionReleased, TaskActionStaged, TaskActionUnblocked:
		// A card that came free is offered to an agent at rest.
		if row.Holder == nil && row.Blocked == nil && pullable(row.Stage) && d.dispatch.anyMode(DispatchPull) {
			go d.offerPull()
		}
	}
	if action == TaskActionFinished && window != "" && !strings.HasPrefix(window, remoteHolderPrefix) && d.dispatch.anyMode(DispatchPull) {
		go d.pullNext(sess, window, dispatchBoardKey(row.Session, row.Project), row.Project, false)
	}
}

// noteDispatchAgentState is what dispatch does with an agent state change of
// window: errored blocks a card dispatch gave it (blockErrored), working
// lifts that block, and a usage limit stops all dispatch. It runs off the
// sink.
//
// Done is not read here. Every turn end reports done (the Claude Code Stop
// hook, Codex's turn notice, the transcript's turn end), also a turn that
// ends on a question or part way, so done does not say the card is done. A
// card leaves doing only by an act that names it: tuios task done, a stage
// move, a release, or its verify result.
func (d *Daemon) noteDispatchAgentState(sess *Session, window, state string) {
	if state != AgentStateErrored.Name() && state != AgentStateWorking.Name() {
		return
	}
	if !d.dispatch.anyMode(DispatchPull, DispatchAuto) {
		return
	}
	go func() {
		w, ok := findWindowState(sess.GetState(), window)
		if !ok {
			return
		}
		if state == AgentStateWorking.Name() {
			// The agent goes on after an error: its card is no longer
			// blocked on it.
			for _, cs := range d.cardSessions(sess, window) {
				d.unblockErrored(cs, window)
			}
			return
		}
		if usageLimited(w) {
			harness := firstNonEmpty(w.AgentHarness, "an agent")
			if d.dispatch.stopAll(dispatchStopLimit) {
				LogBasic("Task dispatch stopped: %s reported a usage limit in window %s", harness, shortWindowID(window))
				d.publishDispatch("stopped", dispatchStopLimit+" reported by "+harness)
			}
		}
		for _, cs := range d.cardSessions(sess, window) {
			d.blockErrored(cs, window, w.AgentMessage)
		}
	}()
}

// usageLimited reports whether w's errored state says the agent hit a usage
// limit.
func usageLimited(w WindowState) bool {
	if w.AgentKind == AgentKindUsageLimit {
		return true
	}
	msg := strings.ToLower(w.AgentMessage)
	return strings.Contains(msg, "usage limit") || strings.Contains(msg, "rate limit")
}

// blockErrored marks blocked the cards dispatch gave window, after its agent
// errored: one attempt more, with the agent's last line as the note and in
// the block. The pane keeps them. An error is often passing and the agent
// lives on, so a card put back now would start a second agent on the same
// work beside it. The card goes back to todo once the pane lets it go (its
// agent leaves, the pane closes, a person releases it; release), and the
// block lifts if the agent works again (unblockErrored). The block counts
// one failure on the board.
func (d *Daemon) blockErrored(sess *Session, window, last string) {
	line := cutUTF8(firstNonEmpty(printableClaim(last, taskMaxReason), taskBlockErrored), taskMaxReason)
	reason := cutUTF8(taskBlockErrored+": "+line, taskMaxReason)
	var rows []TaskRow
	_ = sess.mutateState(func(st *SessionState) error {
		now := time.Now().UnixNano()
		for i := range st.Tasks {
			tk := &st.Tasks[i]
			if tk.Holder != window {
				continue
			}
			if action, _, _ := sess.moveCard(tk, cardMove{kind: moveBlockErrored, by: taskByDaemon, reason: reason, note: line}, now); action != "" {
				rows = append(rows, taskRow(sess, st, tk))
			}
		}
		if len(rows) == 0 {
			return errNoTaskMove
		}
		return nil
	})
	for _, row := range rows {
		LogBasic("Task %s in session %s blocked: %s", row.ID, sess.Name(), reason)
		d.publishTask(sess, EventTaskChanged, TaskActionBlocked, "", reason, row)
	}
}

// unblockErrored lifts the block blockErrored put on the cards window holds.
func (d *Daemon) unblockErrored(sess *Session, window string) {
	// Every turn starts working: most find nothing, under a read lock.
	if !sess.anyTask(func(tk *TaskState) bool { return tk.Holder == window && tk.blockedOnErrored() }) {
		return
	}
	var rows []TaskRow
	_ = sess.mutateState(func(st *SessionState) error {
		now := time.Now().UnixNano()
		for i := range st.Tasks {
			tk := &st.Tasks[i]
			if tk.Holder != window {
				continue
			}
			if action, _, _ := sess.moveCard(tk, cardMove{kind: moveUnblockErrored, by: taskByDaemon}, now); action != "" {
				rows = append(rows, taskRow(sess, st, tk))
			}
		}
		if len(rows) == 0 {
			return errNoTaskMove
		}
		return nil
	})
	for _, row := range rows {
		d.publishTask(sess, EventTaskChanged, TaskActionUnblocked, "", "the agent works again", row)
	}
}

// pullNext offers window the next card of board, when the board is in pull
// mode and nothing holds dispatch. The claim and every check of the pane are
// made in one state change of the session. notOwn leaves out a card window
// let go last: an offer the pane did not ask for by a finish never hands it
// back the card it gave up.
func (d *Daemon) pullNext(sess *Session, window, board, project string, notOwn bool) {
	if why := d.dispatch.blocked(board, DispatchPull, time.Now()); why != "" {
		if d.dispatch.isMode(board, DispatchPull) {
			LogBasic("Task dispatch held for window %s: %s", shortWindowID(window), why)
		}
		if why == dispatchQuietHours {
			d.armPullQuiet()
		}
		return
	}
	var (
		row    TaskRow
		prompt string
		label  string
	)
	err := sess.mutateState(func(st *SessionState) error {
		w, ok := findWindowState(st, window)
		if !ok || w.AgentState == AgentStateNone || w.AgentState == AgentStateNeedsInput {
			return errNoTaskMove
		}
		// The switches are read again under the state lock, so a pause
		// that landed after the first look still holds this claim.
		if d.dispatch.blocked(board, DispatchPull, time.Now()) != "" {
			return errNoTaskMove
		}
		for _, tk := range st.Tasks {
			if tk.Holder == window && tk.Stage != TaskStageDone {
				// It still holds work.
				return errNoTaskMove
			}
		}
		i := slices.IndexFunc(st.Tasks, func(tk TaskState) bool {
			return tk.Project == project && tk.Holder == "" && tk.Blocked == nil && !tk.AskFirst &&
				pullable(tk.Stage) && (tk.Harness == "" || tk.Harness == w.AgentHarness) &&
				!(notOwn && releasedBy(tk, window))
		})
		if i < 0 {
			return errNoTaskMove
		}
		tk := &st.Tasks[i]
		if action, _, _ := sess.moveCard(tk, cardMove{kind: moveClaim, by: taskByDispatch, window: window, dispatched: true}, time.Now().UnixNano()); action == "" {
			return errNoTaskMove
		}
		row, prompt, label = taskRow(sess, st, tk), tk.Prompt, windowLabelOf(w)
		return nil
	})
	if err != nil {
		return
	}
	LogBasic("Task dispatch gave task %s in session %s to window %s", row.ID, sess.Name(), shortWindowID(window))
	d.publishTask(sess, EventTaskClaimed, TaskActionClaimed, window, "dispatch", row)
	d.mailDispatched(sess, window, label, row, prompt, "")
}

// pullable reports whether pull offers a free card in stage.
func pullable(stage string) bool {
	return stage == TaskStageTodo || stage == TaskStagePlan || stage == TaskStageDoing
}

// releasedBy reports whether the last change to tk was window letting it
// go: pull does not offer a pane the card it just gave up.
func releasedBy(tk TaskState, window string) bool {
	n := len(tk.History)
	return n > 0 && tk.History[n-1].Action == TaskActionReleased && tk.History[n-1].Holder == window
}

// offerPull offers a card of each pull board to each agent at rest that
// holds none, as a finish would. It runs when a switch that holds offers
// lifts or a board is set to pull (set-task-dispatch), when quiet hours
// end, and when a card comes free (added, let go, moved back, unblocked):
// otherwise only a finish asks for an offer.
func (d *Daemon) offerPull() {
	if !d.dispatch.anyMode(DispatchPull) {
		return
	}
	now := time.Now()
	for _, sess := range d.manager.AllSessions() {
		st := sess.GetState()
		if st == nil {
			continue
		}
		// The boards with a free card in this session, by board key.
		boards := map[string]string{}
		for _, tk := range st.Tasks {
			if tk.Holder == "" {
				boards[dispatchBoardKey(sess.Name(), tk.Project)] = tk.Project
			}
		}
		for board, project := range boards {
			if why := d.dispatch.blocked(board, DispatchPull, now); why != "" {
				if why == dispatchQuietHours {
					d.armPullQuiet()
				}
				continue
			}
			for _, w := range st.Windows {
				if w.AgentState == AgentStateIdle || w.AgentState == AgentStateDone {
					d.pullNext(sess, w.ID, board, project, true)
				}
			}
		}
	}
}

// armPullQuiet runs offerPull once the quiet hours end, unless that is armed
// already. A span changed meanwhile is read again then.
func (d *Daemon) armPullQuiet() {
	wait := d.dispatch.untilQuietEnds(time.Now())
	ds := &d.dispatch
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.pullQuiet != nil {
		return
	}
	ds.pullQuiet = time.AfterFunc(wait, func() {
		ds.mu.Lock()
		ds.pullQuiet = nil
		ds.mu.Unlock()
		d.offerPull()
	})
}

// mailHeldCard tells win, which start-agent opened and which now holds the
// card row, about the card as mail from tuios: the prompt is read from the
// card's owner, here or on host over the link.
func (d *Daemon) mailHeldCard(sess *Session, win WindowState, row TaskRow, host string) {
	prompt, where := "", ""
	if host == "" {
		if cs := d.manager.GetSession(row.Session); cs != nil {
			if st := cs.GetState(); st != nil {
				if i := taskIndex(st.Tasks, row.ID); i >= 0 {
					prompt = st.Tasks[i].Prompt
				}
			}
			if cs != sess {
				where = " -s " + row.Session
			}
		}
	} else {
		ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
		raw, err := d.federation.Call(ctx, host, "get-task", map[string]string{"session": row.Session, "id": row.ID})
		cancel()
		var detail struct {
			Prompt string `json:"prompt"`
		}
		if err == nil && jsonx.Unmarshal(raw, &detail) == nil {
			prompt = detail.Prompt
		}
		where = " -s " + host + ":" + row.Session
	}
	d.mailDispatched(sess, win.ID, windowLabelOf(win), row, prompt, where)
}

// mailDispatched tells window, as mail from tuios, which card it now holds.
// where is the -s flag that names the card's session, when it is not the
// pane's own.
func (d *Daemon) mailDispatched(sess *Session, window, label string, row TaskRow, prompt, where string) {
	text := "You now hold task card " + row.ID + ": " + row.Title + "\n\n"
	if strings.TrimSpace(prompt) != "" {
		text += prompt + "\n\n"
	}
	text += "When it is done, run: tuios task done " + row.ID + where + " (ending your turn does not finish it)\nIf you cannot do it, run: tuios task release " + row.ID + where
	if limit := agentMsgMaxText; len(text) > limit {
		text = cutUTF8(text, limit)
	}
	stored := d.agents.send(sess.Name(), AgentMessage{
		Kind:      agentMsgDirect,
		From:      taskByDaemon,
		FromLabel: taskByDaemon,
		To:        window,
		ToLabel:   label,
		Subject:   cutUTF8("Next task: "+row.Title, agentMsgMaxSubject),
		Text:      text,
	})
	d.broadcastToSession(sess.ID, MsgAgentMail, &AgentMailPayload{Message: stored}, "")
	d.events.publish(streamEvent{Type: EventAgentMessage, Session: sess.Name(), Window: stored.To})
	d.noteMailSentAs(sess.Name(), window, stored.ID, queueOrigin{kind: queueByShell}, "")
}
