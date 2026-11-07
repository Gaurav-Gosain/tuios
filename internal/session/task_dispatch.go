package session

import (
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
)

// DispatchModes are the modes set-task-dispatch takes.
var DispatchModes = []string{DispatchManual, DispatchPull}

// The actions set-task-dispatch takes.
const (
	dispatchActionMode   = "mode"
	dispatchActionPause  = "pause"
	dispatchActionResume = "resume"
	dispatchActionQuiet  = "quiet"
)

// dispatchActions are the actions set-task-dispatch takes.
var dispatchActions = []string{dispatchActionMode, dispatchActionPause, dispatchActionResume, dispatchActionQuiet}

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
}

// dispatchStore holds the dispatch settings. The daemon keeps one.
type dispatchStore struct {
	mu   sync.Mutex
	path string
	f    dispatchFile
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

// blocked says why dispatch makes no offer on board now, "" when it may.
func (ds *dispatchStore) blocked(board string, now time.Time) string {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	b := ds.f.Boards[board]
	switch {
	case b == nil || b.Mode != DispatchPull:
		return "the board is manual"
	case ds.f.Paused:
		return "dispatch is paused"
	case ds.f.Stopped != "":
		return "dispatch stopped: " + ds.f.Stopped
	case b.Stopped != "":
		return "the board stopped: " + b.Stopped
	case inQuietHours(ds.f.Quiet, now):
		return "quiet hours"
	}
	return ""
}

// isPull reports whether board is in pull mode.
func (ds *dispatchStore) isPull(board string) bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	b := ds.f.Boards[board]
	return b != nil && b.Mode == DispatchPull
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

// anyPull reports whether a board is in pull mode, so an event that could
// start a pull costs nothing on a daemon with none.
func (ds *dispatchStore) anyPull() bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	for _, b := range ds.f.Boards {
		if b.Mode == DispatchPull {
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
	d.events.publish(streamEvent{Type: EventTaskDispatch, Action: action, Reason: reason, Dispatch: &st})
}

// noteDispatchedOutcome counts how a dispatched card ended on its board. row
// is the card after the change; reason is why it was let go.
func (d *Daemon) noteDispatchedOutcome(action, reason string, row TaskRow) {
	if !row.Dispatched || reason == taskReleaseRestart {
		return
	}
	var finished bool
	switch action {
	case TaskActionFinished:
		finished = true
	case TaskActionReleased, TaskActionBlocked:
	default:
		return
	}
	board := dispatchBoardKey(row.Session, row.Project)
	if d.dispatch.noteOutcome(board, finished) {
		LogBasic("Task dispatch stopped on board %s: %s", board, dispatchStopFailures)
		d.publishDispatch("stopped", dispatchStopFailures)
	}
}

// afterTaskEvent is what dispatch does with a task event: it counts a
// dispatched card's outcome, and after a finish offers the pane that held
// the card its next one. It runs off the caller's goroutine, since a
// release is published from the session's event sink under the state lock.
func (d *Daemon) afterTaskEvent(sess *Session, action, window, reason string, row TaskRow) {
	if row.Dispatched || (action == TaskActionFinished && d.dispatch.anyPull()) {
		go func() {
			d.noteDispatchedOutcome(action, reason, row)
			if action == TaskActionFinished && window != "" && !strings.HasPrefix(window, remoteHolderPrefix) {
				d.pullNext(sess, window, dispatchBoardKey(row.Session, row.Project), row.Project)
			}
		}()
	}
}

// noteDispatchAgentState is what dispatch does with an agent state change of
// window: done finishes the card the pane holds on a pull board, which then
// pulls the next; errored puts back a card dispatch gave it, and a usage
// limit stops all dispatch. It runs off the sink.
func (d *Daemon) noteDispatchAgentState(sess *Session, window, state string) {
	if state != AgentStateDone.Name() && state != AgentStateErrored.Name() {
		return
	}
	if !d.dispatch.anyPull() {
		return
	}
	go func() {
		w, ok := findWindowState(sess.GetState(), window)
		if !ok {
			return
		}
		if state == AgentStateErrored.Name() {
			if usageLimited(w) {
				harness := firstNonEmpty(w.AgentHarness, "an agent")
				if d.dispatch.stopAll(dispatchStopLimit) {
					LogBasic("Task dispatch stopped: %s reported a usage limit in window %s", harness, shortWindowID(window))
					d.publishDispatch("stopped", dispatchStopLimit+" reported by "+harness)
				}
			}
			d.putBackDispatched(sess, window)
			return
		}
		d.finishOnDone(sess, window)
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

// putBackDispatched lets go, back to todo, the cards dispatch gave window,
// after its agent errored.
func (d *Daemon) putBackDispatched(sess *Session, window string) {
	const reason = "agent errored"
	var rows []TaskRow
	_ = sess.mutateState(func(st *SessionState) error {
		now := time.Now().UnixNano()
		for i := range st.Tasks {
			tk := &st.Tasks[i]
			if tk.Holder != window || !tk.Dispatched {
				continue
			}
			tk.release(taskByDaemon, reason, now)
			tk.setStage(TaskStageTodo, now)
			rows = append(rows, taskRow(sess, st, tk))
		}
		if len(rows) == 0 {
			return errNoTaskMove
		}
		return nil
	})
	d.publishReleased(sess, rows, reason)
}

// finishOnDone finishes the cards window holds on pull boards, after its
// agent went to done. Each finish publishes task-finished, which pulls the
// next card.
func (d *Daemon) finishOnDone(sess *Session, window string) {
	var rows []TaskRow
	_ = sess.mutateState(func(st *SessionState) error {
		now := time.Now().UnixNano()
		for i := range st.Tasks {
			tk := &st.Tasks[i]
			if tk.Holder != window || !d.dispatch.isPull(dispatchBoardKey(sess.Name(), tk.Project)) {
				continue
			}
			if _, changed := tk.finish(taskByDaemon, now); changed {
				rows = append(rows, taskRow(sess, st, tk))
			}
		}
		if len(rows) == 0 {
			return errNoTaskMove
		}
		return nil
	})
	for _, row := range rows {
		LogBasic("Task %s in session %s finished: its agent is done", row.ID, sess.Name())
		d.publishTask(sess, EventTaskFinished, TaskActionFinished, window, "agent done", row)
	}
}

// pullNext offers window the next card of board, when the board is in pull
// mode and nothing holds dispatch. The claim and every check of the pane are
// made in one state change of the session.
func (d *Daemon) pullNext(sess *Session, window, board, project string) {
	if why := d.dispatch.blocked(board, time.Now()); why != "" {
		if d.dispatch.isPull(board) {
			LogBasic("Task dispatch held for window %s: %s", shortWindowID(window), why)
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
		if d.dispatch.blocked(board, time.Now()) != "" {
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
				(tk.Stage == TaskStageTodo || tk.Stage == TaskStagePlan || tk.Stage == TaskStageDoing) &&
				(tk.Harness == "" || tk.Harness == w.AgentHarness)
		})
		if i < 0 {
			return errNoTaskMove
		}
		tk := &st.Tasks[i]
		if _, verr := tk.take(window, taskByDispatch, time.Now().UnixNano()); verr != nil {
			return errNoTaskMove
		}
		tk.Dispatched = true
		row, prompt, label = taskRow(sess, st, tk), tk.Prompt, windowLabelOf(w)
		return nil
	})
	if err != nil {
		return
	}
	LogBasic("Task dispatch gave task %s in session %s to window %s", row.ID, sess.Name(), shortWindowID(window))
	d.publishTask(sess, EventTaskClaimed, TaskActionClaimed, window, "dispatch", row)
	d.mailDispatched(sess, window, label, row, prompt)
}

// mailDispatched tells window, as mail from tuios, which card it now holds.
func (d *Daemon) mailDispatched(sess *Session, window, label string, row TaskRow, prompt string) {
	text := "You now hold task card " + row.ID + ": " + row.Title + "\n\n"
	if strings.TrimSpace(prompt) != "" {
		text += prompt + "\n\n"
	}
	text += "When it is done, run: tuios task done " + row.ID + "\nIf you cannot do it, run: tuios task release " + row.ID
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
