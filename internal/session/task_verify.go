package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/charmbracelet/x/ansi"
)

// The card lifecycle after the work: verify, review, shipped.
//
// An agent that is done with its card (finish-task from the pane that holds
// it, here or on another machine, or its agent going to done on a pull or
// auto board) does not close the card. The card is let go, which frees the
// agent for its next card, and goes to verify when its board has a verify
// command, or to review when it has none. A finish by the person, a shell, a
// plugin or an admin pane, and set-task-stage done, close the card to done
// as before. [tasks] auto_stage = false, or [tasks] stages without review,
// keeps every finish going to done.
//
// The verify command is a board setting the person makes (set-task-dispatch
// verify, refused from every pane): no card, prompt or agent supplies it. The
// daemon runs it through childproc as `sh -c COMMAND` in the card's worktree,
// in a process group of its own, with a timeout that ends the whole group and
// a tail buffer that keeps only the end of the output. It never runs in a
// pane. A card whose agent worked on another machine is verified there: the
// owner calls run-task-verify over the link, and that daemon finds the pane's
// worktree and runs the same code.
//
// A pass moves the card to review. A fail (an exit other than 0, a timeout,
// a worktree that is gone) sends it back to todo with the attempts count one
// higher and the last line of the output as its note. An agent that errors
// while it holds a dispatched card sends it back the same way, with its last
// line as the note. A verify that could not reach the machine leaves the card
// in verify, since the work may well be fine: moving it to verify again runs
// the command again.
//
// One run per card. Its token is the record's StartedAt, and a result is
// applied only to a card still in verify whose record has that token, in one
// state change, so a card moved by hand mid-run, or verified again, drops the
// old result. A move out of verify also ends the old run.

// TaskActionVerified is the history action of a verify that ended.
const TaskActionVerified = "verified"

// Bounds on the verify command and its output.
const (
	verifyDefaultTimeout = 600
	verifyMaxTimeout     = 3600
	// verifyKeepBytes is the output a run holds while it runs; the rest is
	// counted and dropped. verifyCardBytes is what the card keeps.
	verifyKeepBytes = 16 << 10
	verifyCardBytes = 2 << 10
	// verifyLinkSlack is how much longer than the command the owner waits
	// on a far machine's answer.
	verifyLinkSlack = 30 * time.Second
	// verifyWaitDelay bounds the wait for output pipes a killed command's
	// children held open.
	verifyWaitDelay = 2 * time.Second
)

// TaskWork is where a card's agent did its work: the machine (empty for
// this one), the session and pane, and the worktree root. A far card names
// only the machine and the pane; that machine finds the folder.
type TaskWork struct {
	Host    string `json:"host,omitempty"`
	Session string `json:"session,omitempty"`
	Window  string `json:"window,omitempty"`
	Dir     string `json:"dir,omitempty"`
}

// TaskVerify is one run of the verify command on a card: running, passed or
// failed, the exit status when the command ended, why it failed otherwise,
// and the end of its output.
type TaskVerify struct {
	State      string `json:"state"`
	Exit       *int   `json:"exit,omitempty"`
	Note       string `json:"note,omitempty"`
	Tail       string `json:"tail,omitempty"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at,omitempty"`
}

// verifyResult is how one run ended. Unreached says the command did not run
// because the machine could not be asked, which sends no card back.
type verifyResult struct {
	Type      string `json:"type,omitempty"`
	Exit      *int   `json:"exit,omitempty"`
	Note      string `json:"note,omitempty"`
	Tail      string `json:"tail,omitempty"`
	unreached bool
}

// verifyTimeoutOf is a board's timeout in seconds, the default for 0.
func verifyTimeoutOf(sec int) int {
	if sec <= 0 {
		return verifyDefaultTimeout
	}
	return min(sec, verifyMaxTimeout)
}

// verifyCommandParam checks a verify command and its timeout. off clears it.
func verifyCommandParam(command string, timeout int) (string, *verbError) {
	command = strings.TrimSpace(command)
	if command == "off" {
		command = ""
	}
	switch {
	case len(command) > fanVerifyMaxCommand || strings.ContainsRune(command, 0):
		return "", invalidParam("verify", "verify is one shell line of at most 4096 bytes")
	case timeout < 0 || timeout > verifyMaxTimeout:
		return "", invalidParam("verify_timeout", "verify_timeout is 0 to 3600 seconds")
	}
	return command, nil
}

// verifyOf is board's verify command and its timeout, "" when it has none.
func (ds *dispatchStore) verifyOf(board string) (string, time.Duration) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	b := ds.f.Boards[board]
	if b == nil || b.Verify == "" {
		return "", 0
	}
	return b.Verify, time.Duration(verifyTimeoutOf(b.VerifyTimeout)) * time.Second
}

// loaded reports whether the saved settings were read.
func (ds *dispatchStore) loaded() bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.path != ""
}

// handIn lets the card go as its agent is done with it, and moves it to
// stage: verify, with a running record, or review. It returns the holder it
// had.
func (tk *TaskState) handIn(by, stage string, work *TaskWork, now int64) string {
	holder := tk.Holder
	tk.Holder, tk.HeldAt, tk.HolderName, tk.HolderHarness, tk.Blocked = "", 0, "", "", nil
	tk.setStage(stage, now)
	if work != nil {
		tk.Work = work
	}
	if stage == TaskStageVerify {
		tk.Verify = &TaskVerify{State: VerifyRunning, StartedAt: now}
	}
	tk.note(TaskEvent{Action: TaskActionFinished, By: by, At: now, Stage: tk.Stage, Holder: holder})
	return holder
}

// handInStage is where a card of board goes when its agent is done with it:
// verify when the board has a verify command, review when it has none, and
// "" when the lifecycle is off and the card is done.
func (d *Daemon) handInStage(board string) string {
	if d.lifecycleOff.Load() {
		return ""
	}
	if cmd, _ := d.dispatch.verifyOf(board); cmd != "" {
		return TaskStageVerify
	}
	return TaskStageReview
}

// workOf is where the pane window does its work: the machine and pane of a
// holder on another machine, or the session, pane and worktree root of one
// here. nil when the pane is gone. It runs git, so never under a state lock.
func (d *Daemon) workOf(window string) *TaskWork {
	if host, w, ok := splitRemoteHolder(window); ok {
		return &TaskWork{Host: host, Window: w}
	}
	sess := d.sessionHoldingWindow(window)
	if sess == nil {
		return nil
	}
	w, ok := findWindowState(sess.GetState(), window)
	if !ok {
		return nil
	}
	work := &TaskWork{Session: sess.Name(), Window: window}
	if repo, verr := d.paneRepo(sess, w); verr == nil {
		work.Dir = repo.root
	}
	return work
}

// taskVerifies holds the verify running for each card, by session/id. The
// zero value is ready.
type taskVerifies struct {
	mu   sync.Mutex
	runs map[string]taskVerifyRun
	// held is set while a live upgrade is prepared: no run starts, and the
	// card keeps its running record for the image that comes next.
	held bool
}

type taskVerifyRun struct {
	token  int64
	cancel context.CancelFunc
	done   chan struct{}
}

// put records the run for key, ending the one it replaces. It reports false,
// and records nothing, while the runs are held.
func (t *taskVerifies) put(key string, token int64, cancel context.CancelFunc, done chan struct{}) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.held {
		return false
	}
	if old, ok := t.runs[key]; ok {
		old.cancel()
	}
	if t.runs == nil {
		t.runs = map[string]taskVerifyRun{}
	}
	t.runs[key] = taskVerifyRun{token: token, cancel: cancel, done: done}
	return true
}

// hold ends every run and waits, up to limit, until each has ended, so no
// command outlives this image as a child that nothing waits for. A run ended
// this way lands no result: its card stays in verify with a running record,
// which resumeVerifies runs again. No run starts until release.
func (t *taskVerifies) hold(limit time.Duration) {
	t.mu.Lock()
	t.held = true
	var waits []chan struct{}
	for _, r := range t.runs {
		r.cancel()
		waits = append(waits, r.done)
	}
	t.mu.Unlock()
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	for _, done := range waits {
		select {
		case <-done:
		case <-deadline.C:
			return
		}
	}
}

// release lets runs start again after hold.
func (t *taskVerifies) release() {
	t.mu.Lock()
	t.held = false
	t.mu.Unlock()
}

// stop ends the run of key and takes it out: the one with token, or any
// one for token 0.
func (t *taskVerifies) stop(key string, token int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r, ok := t.runs[key]; ok && (token == 0 || r.token == token) {
		r.cancel()
		delete(t.runs, key)
	}
}

// verifyKey is the key of a card in taskVerifies.
func verifyKey(session, id string) string { return session + "/" + id }

// startTaskVerify runs the board's verify command on the card id of sess,
// when the card is in verify with a running record. The result lands on the
// card when the command ends.
func (d *Daemon) startTaskVerify(sess *Session, id string) {
	tk, ok := sess.taskByID(id)
	if !ok || tk.Stage != TaskStageVerify || tk.Verify == nil || tk.Verify.State != VerifyRunning {
		return
	}
	work := tk.Work
	if work == nil {
		work = &TaskWork{}
	}
	token := tk.Verify.StartedAt
	command, timeout := d.dispatch.verifyOf(dispatchBoardKey(sess.Name(), tk.Project))
	ctx, cancel := context.WithCancel(d.ctx)
	key := verifyKey(sess.Name(), id)
	done := make(chan struct{})
	if !d.verifies.put(key, token, cancel, done) {
		cancel()
		return
	}
	go func() {
		defer close(done)
		defer d.verifies.stop(key, token)
		res := verifyResult{Note: "no verify command", unreached: true}
		switch {
		case command == "":
		case work.Host != "":
			res = d.farVerify(ctx, work, command, timeout)
		default:
			res = runVerify(ctx, work.Dir, command, timeout)
		}
		if ctx.Err() != nil {
			// Moved, verified again, or the daemon is stopping.
			return
		}
		d.applyVerify(sess, id, token, res)
	}()
}

// farVerify asks the machine of work to run command in the pane's
// worktree. A far holder is named by the name its machine gives itself,
// which need not be its [hosts] name here, so a name that is not a host is
// found by asking each linked machine whether it has the pane, and kept.
func (d *Daemon) farVerify(ctx context.Context, work *TaskWork, command string, timeout time.Duration) verifyResult {
	res := verifyResult{Note: "machine " + work.Host + " is not reachable", unreached: true}
	host, _ := d.verifyPeers.Load(work.Host)
	hosts := d.farHosts()
	if slices.Contains(hosts, work.Host) {
		host, hosts = work.Host, nil
	}
	params := runTaskVerifyParams{Window: work.Window, Probe: true}
	for _, h := range hosts {
		if host != nil {
			break
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		raw, err := d.federation.Call(cctx, h, "run-task-verify", params)
		cancel()
		var ans struct{ Here bool }
		if err == nil && jsonx.Unmarshal(raw, &ans) == nil && ans.Here {
			host = h
			d.verifyPeers.Store(work.Host, h)
		}
	}
	name, _ := host.(string)
	if name == "" {
		return res
	}
	res.Note = "machine " + name + " is not reachable"
	params.Probe, params.Command, params.TimeoutMS = false, command, timeout.Milliseconds()
	cctx, cancel := context.WithTimeout(ctx, timeout+verifyLinkSlack)
	defer cancel()
	raw, err := d.federation.Call(cctx, name, "run-task-verify", params)
	var got verifyResult
	if err != nil || jsonx.Unmarshal(raw, &got) != nil {
		return res
	}
	// The far daemon is untrusted: its words are cut and cleaned.
	got.Note, got.Tail = printableClaim(got.Note, taskMaxReason), cleanTail(got.Tail)
	return got
}

// runVerify runs command in dir with the limit, and reports how it ended.
// The command never runs in another folder: a dir that is gone is a fail.
func runVerify(ctx context.Context, dir, command string, timeout time.Duration) verifyResult {
	if fi, err := os.Stat(dir); dir == "" || err != nil || !fi.IsDir() {
		return verifyResult{Note: "the worktree is gone"}
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = childproc.CommandContext(rctx, "cmd.exe", "/c", command)
	} else {
		cmd = childproc.CommandContext(rctx, "/bin/sh", "-c", command)
	}
	cmd.Dir = dir
	childproc.OwnGroup(cmd)
	cmd.WaitDelay = verifyWaitDelay
	out := &tailBuffer{max: verifyKeepBytes}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	res := verifyResult{Tail: cleanTail(string(out.buf[max(len(out.buf)-out.max, 0):]))}
	var exitErr *exec.ExitError
	errors.As(err, &exitErr)
	switch {
	case errors.Is(rctx.Err(), context.DeadlineExceeded):
		res.Note = "timed out after " + timeout.String()
	case ctx.Err() != nil:
		res.Note = "stopped"
	case err == nil:
		code := 0
		res.Exit = &code
	case exitErr != nil:
		code := exitErr.ExitCode()
		res.Exit = &code
	default:
		res.Note = "could not run"
	}
	return res
}

// applyVerify lands a run's result on the card, when the card is still in
// verify with the run's token: a pass to review, a fail back to todo, and a
// run that could not reach its machine stays in verify.
func (d *Daemon) applyVerify(sess *Session, id string, token int64, res verifyResult) {
	// The outcome is decided before the state change: the stage it goes to,
	// the record and the reason.
	v := &TaskVerify{State: VerifyFailed, Exit: res.Exit, Note: res.Note, Tail: res.Tail, StartedAt: token}
	stage, note, reason := TaskStageTodo, verifyFailNote(res), ""
	switch {
	case res.Exit != nil && *res.Exit == 0 && res.Note == "":
		v.State, stage, reason = VerifyPassed, TaskStageReview, "verify passed"
	case res.unreached:
		stage, reason = TaskStageVerify, "verify did not run: "+res.Note
	default:
		reason = "verify failed: " + note
	}
	var row TaskRow
	if sess.mutateState(func(st *SessionState) error {
		i := taskIndex(st.Tasks, id)
		if i < 0 || st.Tasks[i].Stage != TaskStageVerify || st.Tasks[i].Verify == nil || st.Tasks[i].Verify.StartedAt != token {
			return errNoTaskMove
		}
		tk := &st.Tasks[i]
		now := time.Now().UnixNano()
		v.FinishedAt = now
		if stage == TaskStageTodo {
			tk.Attempts++
			tk.Note = note
		}
		tk.setStage(stage, now)
		tk.Verify = v
		tk.note(TaskEvent{Action: TaskActionVerified, By: taskByDaemon, At: now, Stage: stage, Reason: reason})
		row = taskRow(sess, st, tk)
		return nil
	}) != nil {
		return
	}
	LogBasic("Task %s in session %s: %s", id, sess.Name(), reason)
	d.publishTask(sess, EventTaskChanged, TaskActionVerified, "", reason, row)
}

// verifyFailNote is a failed run as a card's note: the exit status or why it
// did not end, and the last line of its output.
func verifyFailNote(res verifyResult) string {
	note := res.Note
	if res.Exit != nil {
		note = "exit " + strconv.Itoa(*res.Exit)
	}
	if line := lastOutputLine(res.Tail); line != "" {
		note += ": " + line
	}
	return cutUTF8(note, taskMaxReason)
}

// lastOutputLine is the last line of out that is not blank.
func lastOutputLine(out string) string {
	for out != "" {
		var line string
		if i := strings.LastIndexByte(out, '\n'); i >= 0 {
			out, line = out[:i], out[i+1:]
		} else {
			out, line = "", out
		}
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// cleanTail is the end of a run's output as a card keeps it: no escape
// sequences or other control characters but newlines and tabs, at most
// verifyCardBytes, from the start of a line where it can.
func cleanTail(out string) string {
	out = ansi.Strip(out)
	out = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			return -1
		}
		return r
	}, out)
	out = strings.TrimRight(out, "\n\t ")
	if len(out) > verifyCardBytes {
		out = out[len(out)-verifyCardBytes:]
		if i := strings.IndexByte(out, '\n'); i >= 0 && i < len(out)-1 {
			out = out[i+1:]
		}
		for len(out) > 0 && out[0]&0xC0 == 0x80 {
			out = out[1:]
		}
	}
	return out
}

// tailBuffer keeps the last max bytes written to it. exec writes to it from
// one goroutine, since the command's stdout and stderr are the same writer,
// and it is read after the command ended, so it needs no lock.
type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if n := len(t.buf); n > 2*t.max {
		t.buf = append(t.buf[:0], t.buf[n-t.max:]...)
	}
	return len(p), nil
}

// taskByID is the card id of the session, read under the state lock.
func (s *Session) taskByID(id string) (TaskState, bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if i := taskIndex(s.state.Tasks, id); i >= 0 {
		return s.state.Tasks[i], true
	}
	return TaskState{}, false
}

// resumeVerifies runs again the verifies a restart ended: every card of sess
// in verify whose record says running.
func (d *Daemon) resumeVerifies(sess *Session) {
	var ids []string
	sess.anyTask(func(t *TaskState) bool {
		if t.Stage == TaskStageVerify && t.Verify != nil && t.Verify.State == VerifyRunning {
			ids = append(ids, t.ID)
		}
		return false
	})
	for _, id := range ids {
		d.startTaskVerify(sess, id)
	}
}

// runTaskVerifyParams are what run-task-verify takes.
type runTaskVerifyParams struct {
	// Probe only answers whether the pane is here.
	Probe     bool   `json:"probe,omitempty"`
	Window    string `json:"window"`
	Command   string `json:"command,omitempty"`
	TimeoutMS int64  `json:"timeout_ms,omitempty"`
}

// verbRunTaskVerify answers run-task-verify: the machine that owns a card
// asks this one to run the board's verify command in the worktree of the
// pane here that did the card's work. Only over the link: a caller here, in
// a pane or not, has no business running a command outside its grants by it.
func (d *Daemon) verbRunTaskVerify(cs *connState, params json.RawMessage) (any, *verbError) {
	if cs == nil || !cs.viaLink {
		return nil, newVerbError(ErrVerbForbidden, "run-task-verify is only for a link")
	}
	var p runTaskVerifyParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Probe {
		return map[string]any{"type": "task_verify", "here": d.sessionHoldingWindow(p.Window) != nil}, nil
	}
	command, verr := verifyCommandParam(p.Command, 0)
	if verr != nil || command == "" {
		return nil, invalidParam("command", "command is one shell line")
	}
	dir := ""
	if work := d.workOf(p.Window); work != nil {
		dir = work.Dir
	}
	LogBasic("Verifying window %s for a linked machine", shortWindowID(p.Window))
	res := runVerify(d.ctx, dir, command, time.Duration(verifyTimeoutOf(int(p.TimeoutMS/1000)))*time.Second)
	res.Type = "task_verify"
	return res, nil
}

// taskVerifyVerbs are the registry entries of the verify verb.
func taskVerifyVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"run-task-verify": {
			scope:       scopeLaunch,
			link:        needOpen,
			description: "Run a board's verify command in the worktree of a pane here, for the card's owner. Only over a link.",
			params: []verbParam{
				{Name: "window", Type: "string", Required: true, Description: "The pane."},
				{Name: "command", Type: "string", Description: "The shell line."},
				{Name: "probe", Type: "bool", Description: "Only say if the pane is here.", Default: "false"},
				{Name: "timeout_ms", Type: "int", Description: "The limit.", Default: "600000"},
			},
			returns: []verbParam{
				{Name: "here", Type: "bool", Description: "With probe: the pane is here."},
				{Name: "exit", Type: "int", Description: "The exit status."},
				{Name: "note", Type: "string", Description: "Why it did not end."},
				{Name: "tail", Type: "string", Description: "The end of the output."},
			},
			examples: []string{`{"id":1,"verb":"run-task-verify","params":{"window":"a1b2c3d4","command":"go test ./...","timeout_ms":600000}}`},
			handler:  (*Daemon).verbRunTaskVerify,
		},
	}
}
