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
	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/charmbracelet/x/ansi"
)

// TaskActionVerified is the history action of a verify that ended.
const TaskActionVerified = "verified"

const (
	verifyDefaultTimeout = 600
	verifyMaxTimeout     = 3600
	verifyKeepBytes      = 16 << 10
	verifyCardBytes      = 2 << 10
	verifyLinkSlack      = 30 * time.Second
	verifyWaitDelay      = 2 * time.Second
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

type verifyResult struct {
	Type      string `json:"type,omitempty"`
	Exit      *int   `json:"exit,omitempty"`
	Note      string `json:"note,omitempty"`
	Tail      string `json:"tail,omitempty"`
	unreached bool
}

func verifyTimeoutOf(sec int) int {
	if sec <= 0 {
		return verifyDefaultTimeout
	}
	return min(sec, verifyMaxTimeout)
}

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

func (ds *dispatchStore) verifyOf(board string) (string, time.Duration) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	b := ds.f.Boards[board]
	if b == nil || b.Verify == "" {
		return "", 0
	}
	return b.Verify, time.Duration(verifyTimeoutOf(b.VerifyTimeout)) * time.Second
}

func (ds *dispatchStore) loaded() bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.path != ""
}

func (d *Daemon) handInStage(board string) string {
	if d.lifecycleOff.Load() {
		return ""
	}
	if cmd, _ := d.dispatch.verifyOf(board); cmd != "" {
		return TaskStageVerify
	}
	return TaskStageReview
}

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

type taskVerifies struct {
	mu   sync.Mutex
	runs map[string]taskVerifyRun
	held bool
}

type taskVerifyRun struct {
	token  int64
	cancel context.CancelFunc
	done   chan struct{}
}

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

func (t *taskVerifies) release() {
	t.mu.Lock()
	t.held = false
	t.mu.Unlock()
}

func (t *taskVerifies) stop(key string, token int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r, ok := t.runs[key]; ok && (token == 0 || r.token == token) {
		r.cancel()
		delete(t.runs, key)
	}
}

func verifyKey(session, id string) string { return session + "/" + id }

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
			return
		}
		d.applyVerify(sess, id, token, res)
	}()
}

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
	got.Note, got.Tail = printableClaim(got.Note, taskMaxReason), cleanTail(got.Tail)
	return got
}

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
	cmd.Env = guestenv.DaemonChild()
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

func (d *Daemon) applyVerify(sess *Session, id string, token int64, res verifyResult) {
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
		if i < 0 {
			return errNoTaskMove
		}
		tk := &st.Tasks[i]
		now := time.Now().UnixNano()
		v.FinishedAt = now
		if action, _, _ := sess.moveCard(tk, cardMove{kind: moveVerified, by: taskByDaemon, token: token, stage: stage, verify: v, note: note, reason: reason}, now); action == "" {
			return errNoTaskMove
		}
		row = taskRow(sess, st, tk)
		d.taskEvent(sess, EventTaskChanged, TaskActionVerified, "", reason, row)
		return nil
	}) != nil {
		return
	}
	LogBasic("Task %s in session %s: %s", id, sess.Name(), reason)
	d.afterTaskEvent(sess, TaskActionVerified, "", reason, row)
}

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

func (s *Session) taskByID(id string) (TaskState, bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if i := taskIndex(s.state.Tasks, id); i >= 0 {
		return s.state.Tasks[i], true
	}
	return TaskState{}, false
}

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

type runTaskVerifyParams struct {
	Probe     bool   `json:"probe,omitempty"`
	Window    string `json:"window"`
	Command   string `json:"command,omitempty"`
	TimeoutMS int64  `json:"timeout_ms,omitempty"`
}

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
