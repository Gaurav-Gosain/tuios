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
	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// The dispatch modes of a board.
const (
	DispatchManual = "manual"
	DispatchPull   = "pull"
	DispatchAuto   = "auto"
)

// DispatchModes are the modes set-task-dispatch takes.
var DispatchModes = []string{DispatchManual, DispatchPull, DispatchAuto}

const (
	dispatchActionMode   = "mode"
	dispatchActionPause  = "pause"
	dispatchActionResume = "resume"
	dispatchActionQuiet  = "quiet"
	dispatchActionPreset = "preset"
	dispatchActionCap    = "cap"
	dispatchActionVerify = "verify"
)

var dispatchActions = []string{dispatchActionMode, dispatchActionPause, dispatchActionResume, dispatchActionQuiet, dispatchActionPreset, dispatchActionCap, dispatchActionVerify}

const taskByDispatch = "dispatch"

const dispatchMaxFailures = 3

// AgentKindUsageLimit is the kind of an errored report that says the agent
// hit its harness's usage limit: the form from before limit_reached was a
// state. set-agent-state turns such a report into limit_reached.
const AgentKindUsageLimit = "usage_limit"

// EventTaskDispatch says a dispatch mode or switch changed. Dispatch carries
// the whole status, and Reason why dispatch stopped on its own.
const EventTaskDispatch = "task-dispatch"

const (
	dispatchStopFailures = "3 failures in a row"
	dispatchStopLimit    = "usage limit"
)

const sessionBoardPrefix = "session:"

type dispatchBoard struct {
	Mode          string          `json:"mode,omitempty"`
	Failures      int             `json:"failures,omitempty"`
	Stopped       string          `json:"stopped,omitempty"`
	Preset        *DispatchPreset `json:"preset,omitempty"`
	Verify        string          `json:"verify,omitempty"`
	VerifyTimeout int             `json:"verify_timeout,omitempty"`
}

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

type dispatchFile struct {
	Version    int                       `json:"version"`
	Paused     bool                      `json:"paused,omitempty"`
	PausedBy   string                    `json:"paused_by,omitempty"`
	PausedAt   int64                     `json:"paused_at,omitempty"`
	Stopped    string                    `json:"stopped,omitempty"`
	Quiet      string                    `json:"quiet,omitempty"`
	Boards     map[string]*dispatchBoard `json:"boards,omitempty"`
	Cap        int                       `json:"cap,omitempty"`
	MachineCap int                       `json:"machine_cap,omitempty"`
}

type dispatchStore struct {
	mu        sync.Mutex
	path      string
	f         dispatchFile
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

const (
	dispatchDefaultCap        = 3
	dispatchDefaultMachineCap = 2
	dispatchCapMax            = 64
)

func (ds *dispatchStore) capsLocked() (int, int) {
	return cmp.Or(ds.f.Cap, dispatchDefaultCap), cmp.Or(ds.f.MachineCap, dispatchDefaultMachineCap)
}

func dispatchPath() string {
	return filepath.Join(getResurrectionDir(), "tasks", "dispatch.json")
}

func dispatchBoardKey(sessionName, project string) string {
	if project != "" {
		return project
	}
	return sessionBoardPrefix + sessionName
}

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

const dispatchQuietHours = "quiet hours"

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

func (ds *dispatchStore) isMode(board string, modes ...string) bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	b := ds.f.Boards[board]
	return b != nil && slices.Contains(modes, b.Mode)
}

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

func parseQuietHours(s string) (from, to int, ok bool) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, false
	}
	from, ok1 := parseClock(a)
	to, ok2 := parseClock(b)
	return from, to, ok1 && ok2 && from != to
}

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

func (d *Daemon) publishDispatch(action, reason string) {
	st := d.dispatch.status("", time.Now())
	d.autoStatus(&st)
	d.events.publish(streamEvent{Type: EventTaskDispatch, Action: action, Reason: reason, Dispatch: &st})
}

func (d *Daemon) afterTaskEvent(sess *Session, action, window, reason string, row TaskRow) {
	d.kickAuto()
	if row.Stage != TaskStageVerify {
		d.verifies.stop(verifyKey(row.Session, row.ID), 0)
	}
	switch action {
	case TaskActionAdded, TaskActionReleased, TaskActionStaged, TaskActionUnblocked:
		if row.Holder == nil && row.Blocked == nil && pullable(row.Stage) && d.dispatch.anyMode(DispatchPull) {
			go d.offerPull()
		}
	}
	if action == TaskActionFinished && window != "" && !strings.HasPrefix(window, remoteHolderPrefix) && d.dispatch.anyMode(DispatchPull) {
		go d.pullNext(sess, window, dispatchBoardKey(row.Session, row.Project), row.Project, false)
	}
}

func (d *Daemon) noteDispatchAgentState(sess *Session, window, state string) {
	if state != AgentStateErrored.Name() && state != AgentStateLimitReached.Name() && state != AgentStateWorking.Name() {
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

func usageLimited(w WindowState) bool {
	if w.AgentState == AgentStateLimitReached {
		return true
	}
	if w.AgentState != AgentStateErrored {
		return false
	}
	msg := strings.ToLower(w.AgentMessage)
	return strings.Contains(msg, "usage limit") || strings.Contains(msg, "rate limit")
}

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
		for _, row := range rows {
			d.taskEvent(sess, EventTaskChanged, TaskActionBlocked, "", reason, row)
		}
		return nil
	})
	for _, row := range rows {
		LogBasic("Task %s in session %s blocked: %s", row.ID, sess.Name(), reason)
		d.afterTaskEvent(sess, TaskActionBlocked, "", reason, row)
	}
}

func (d *Daemon) unblockErrored(sess *Session, window string) {
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
		for _, row := range rows {
			d.taskEvent(sess, EventTaskChanged, TaskActionUnblocked, "", "the agent works again", row)
		}
		return nil
	})
	for _, row := range rows {
		d.afterTaskEvent(sess, TaskActionUnblocked, "", "the agent works again", row)
	}
}

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
		if d.dispatch.blocked(board, DispatchPull, time.Now()) != "" {
			return errNoTaskMove
		}
		for _, tk := range st.Tasks {
			if tk.Holder == window && tk.Stage != TaskStageDone {
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
		d.taskEvent(sess, EventTaskClaimed, TaskActionClaimed, window, "dispatch", row)
		return nil
	})
	if err != nil {
		return
	}
	LogBasic("Task dispatch gave task %s in session %s to window %s", row.ID, sess.Name(), shortWindowID(window))
	d.afterTaskEvent(sess, TaskActionClaimed, window, "dispatch", row)
	d.mailDispatched(sess, window, label, row, prompt, "")
}

func pullable(stage string) bool {
	return stage == TaskStageTodo || stage == TaskStagePlan || stage == TaskStageDoing
}

func releasedBy(tk TaskState, window string) bool {
	n := len(tk.History)
	return n > 0 && tk.History[n-1].Action == TaskActionReleased && tk.History[n-1].Holder == window
}

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

func (d *Daemon) mailDispatched(sess *Session, window, label string, row TaskRow, prompt, where string) {
	prompt, _ = invisible.StripHidden(prompt)
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
