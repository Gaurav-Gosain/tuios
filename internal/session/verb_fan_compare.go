package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Fan compare: the attempts of a fan side by side, one check run in all of
// them, and keeping one.
//
// compare-fan reads counts and states, not file contents, so it is scopeRead
// and a link needs only list. verify-fan starts a window in each sibling, so it
// is scopeLaunch (the fan grant, reaching the caller's fan group) and a link
// needs open and write. The command is always the caller's and the verify
// window is opened with no grants, so a check cannot call tuios and cloning a
// repository cannot make fan run code. keep-fan removes worktrees, so it is
// the person's or admin's (scopeDeny), like remove-worktree, and a link needs
// write.
//
// The grant and scope checks hold the named session to the caller's reach.
// compare-fan and verify-fan then act on every sibling of that session, so
// each sibling is held to the same reach again here (callerReachesSession): a
// sibling the caller could not name itself is left out of the rows and gets
// no check.

// fanGitTimeout bounds the git calls one sibling's row costs. A repository
// slow enough to pass it reports the row without counts rather than holding
// the verb.
const fanGitTimeout = 10 * time.Second

// fanVerifyMaxCommand bounds a verify command. It is a shell line a person or
// an agent typed, not a script.
const fanVerifyMaxCommand = 4096

// fanVerifyWindow is the name of the window a check runs in.
const fanVerifyWindow = "verify"

// fanTarget resolves a session of a fan: its session and record, or the
// refusal for a session that is not a worktree or not part of a fan.
func (d *Daemon) fanTarget(name string) (*Session, *WorktreeInfo, *verbError) {
	sess, info, verr := d.worktreeTarget(name)
	if verr != nil {
		return nil, nil, verr
	}
	if info.Group == "" {
		var fans []string
		for _, s := range d.manager.AllSessions() {
			if wt := s.Worktree(); wt != nil && wt.Group != "" {
				fans = append(fans, s.Name)
			}
		}
		return nil, nil, hintedVerbError(ErrVerbInvalidParams, "session "+sess.Name+" is a worktree session that is not part of a fan", &VerbHint{
			Param:     "session",
			Command:   "tuios worktree ls",
			Available: fans,
			Detail:    "Name any session a fan started. A worktree made on its own has no attempts to compare.",
		})
	}
	return sess, info, nil
}

// fanSiblings lists the sessions of info's fan, in the order fan made them:
// the same group in the same repository.
func (d *Daemon) fanSiblings(info *WorktreeInfo) []*Session {
	var out []*Session
	for _, s := range d.manager.AllSessions() {
		wt := s.Worktree()
		if wt != nil && wt.Group == info.Group && wt.RepoRoot == info.RepoRoot {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b *Session) int { return fanOrder(a.Name, b.Name) })
	return out
}

// fanOrder sorts fan sessions the way fan numbers them: the stem, then -2,
// -3 and on, with -10 after -9.
func fanOrder(a, b string) int {
	stemA, nA := fanSuffix(a)
	stemB, nB := fanSuffix(b)
	if stemA != stemB {
		return strings.Compare(a, b)
	}
	return nA - nB
}

// fanSuffix splits "api-retry-3" into "api-retry" and 3, and gives a name
// with no number 1.
func fanSuffix(name string) (string, int) {
	if i := strings.LastIndexByte(name, '-'); i > 0 {
		if n, err := strconv.Atoi(name[i+1:]); err == nil && n > 1 {
			return name[:i], n
		}
	}
	return name, 1
}

// callerReachesSession reports whether the caller on cs may reach session
// target: the restriction of a restricted connection, and the reach of a
// pane without admin, as checkScope and checkGrants held the named session.
// A caller held to neither reaches every session.
func (d *Daemon) callerReachesSession(cs *connState, target string) bool {
	if cs == nil {
		return true
	}
	if sc := cs.scope.Load(); sc != nil && sc.own && !d.sessionInScope(sc.session, target) {
		return false
	}
	if pa := cs.paneView.Load(); pa != nil && !pa.grants.Has(GrantAdmin) && !d.sessionInScope(pa.session, target) {
		return false
	}
	return true
}

// fanCompareParams are what compare-fan takes.
type fanCompareParams struct {
	Session string `json:"session"`
	Changes *bool  `json:"changes"`
}

// fanCompareRow is one attempt as compare-fan reports it. The counts are nil
// when they were not asked for or git could not give them.
type fanCompareRow struct {
	Session      string          `json:"session"`
	Branch       string          `json:"branch"`
	Path         string          `json:"path"`
	Agent        string          `json:"agent"`
	Harness      string          `json:"harness"`
	State        string          `json:"state"`
	Files        *int            `json:"files,omitempty"`
	Added        *int            `json:"added,omitempty"`
	Removed      *int            `json:"removed,omitempty"`
	Ahead        *int            `json:"ahead,omitempty"`
	Dirty        *bool           `json:"dirty,omitempty"`
	BaseSHA      string          `json:"base_sha,omitempty"`
	PromptStatus string          `json:"prompt_status"`
	Verify       *FanVerify      `json:"verify,omitempty"`
	LastCommand  *fanLastCommand `json:"last_command,omitempty"`
	Gone         bool            `json:"gone,omitempty"`
	// Note says why the counts are missing when git failed.
	Note string `json:"note,omitempty"`
}

// fanLastCommand is the newest command a shell in the session finished, from
// its OSC 133 marks.
type fanLastCommand struct {
	Cmdline string `json:"cmdline"`
	Exit    *int   `json:"exit,omitempty"`
	At      int64  `json:"at"`
	Window  string `json:"window"`
}

// verbCompareFan answers compare-fan.
func (d *Daemon) verbCompareFan(cs *connState, params json.RawMessage) (any, *verbError) {
	var p fanCompareParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	_, info, verr := d.fanTarget(p.Session)
	if verr != nil {
		return nil, verr
	}
	changes := p.Changes == nil || *p.Changes
	var siblings []*Session
	for _, s := range d.fanSiblings(info) {
		if d.callerReachesSession(cs, s.Name) {
			siblings = append(siblings, s)
		}
	}

	rows := make([]fanCompareRow, len(siblings))
	var wg sync.WaitGroup
	for i, s := range siblings {
		rows[i] = d.fanRow(s)
		if !changes || rows[i].Gone {
			continue
		}
		wg.Add(1)
		go func(row *fanCompareRow, wt *WorktreeInfo) {
			defer wg.Done()
			fanRowChanges(row, wt)
		}(&rows[i], s.Worktree())
	}
	wg.Wait()

	base := info.Base
	if base == "" {
		// A fan with no base started from the main checkout's HEAD; the rows
		// are counted from where each branch left it.
		if b, err := worktree.CurrentBranch(info.RepoRoot); err == nil && b != "" {
			base = b
		} else {
			base = "HEAD"
		}
	}
	return map[string]any{
		"type":      "fan_compare",
		"group":     info.Group,
		"repo":      info.Repo,
		"repo_root": info.RepoRoot,
		"base":      base,
		"rows":      rows,
		"total":     len(rows),
	}, nil
}

// fanRow is one sibling's row without the git counts.
func (d *Daemon) fanRow(s *Session) fanCompareRow {
	wt := s.worktreeListing()
	if wt == nil {
		wt = &WorktreeInfo{}
	}
	row := fanCompareRow{
		Session:      s.Name,
		Branch:       wt.Branch,
		Path:         wt.Path,
		Agent:        wt.Agent,
		PromptStatus: wt.PromptStatus,
		Verify:       d.fanVerifyReport(s, wt.Verify),
		Gone:         wt.Gone,
	}
	st := s.GetState()
	states := make([]string, 0, len(st.Windows))
	var newest *fanLastCommand
	for _, w := range st.Windows {
		states = append(states, string(w.AgentState))
		if row.Harness == "" {
			row.Harness = w.AgentHarness
		}
		pty := s.GetPTY(w.PTYID)
		if pty == nil {
			continue
		}
		f := pty.ShellFacts()
		if f.CommandSeq == 0 || f.LastAt.IsZero() {
			continue
		}
		if newest == nil || f.LastAt.UnixNano() > newest.At {
			newest = &fanLastCommand{Cmdline: f.LastCmdline, Exit: f.LastExit, At: f.LastAt.UnixNano(), Window: w.ID}
		}
	}
	row.LastCommand = newest
	row.State = sessiontree.RollUpState(states)
	if row.State == "" {
		row.State = AgentStateNone.Name()
	}
	return row
}

// fanRowChanges fills in a row's counts against the sibling's base: its
// recorded base, or where its branch left the main checkout's HEAD.
func fanRowChanges(row *fanCompareRow, wt *WorktreeInfo) {
	if wt == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), fanGitTimeout)
	defer cancel()
	base := wt.Base
	if base == "" {
		head, err := worktree.HeadCommit(wt.RepoRoot)
		if err == nil {
			base, err = worktree.MergeBase(wt.Path, "HEAD", head)
		}
		if err != nil {
			row.Note = "no base to count from: " + err.Error()
			return
		}
	}
	if sha, err := worktree.MergeBase(wt.Path, "HEAD", base); err == nil {
		row.BaseSHA = sha
	}
	n, err := worktree.WorkingNumstat(ctx, wt.Path, base)
	if err != nil {
		row.Note = err.Error()
		return
	}
	row.Files, row.Added, row.Removed = &n.Files, &n.Added, &n.Removed
	if ahead, err := worktree.Ahead(wt.Path, base); err == nil {
		row.Ahead = &ahead
	}
	if c, err := worktree.Changes(wt.Path); err == nil {
		dirty := c > 0
		row.Dirty = &dirty
	}
}

// fanVerifyReport is a session's recorded check as it is reported: a check
// recorded as running that no watcher of this daemon runs was ended by a
// restart, and reads as failed with no exit status.
func (d *Daemon) fanVerifyReport(s *Session, v *FanVerify) *FanVerify {
	if v == nil || v.State != VerifyRunning || d.fanVerifies.running(s.ID) {
		return v
	}
	cp := *v
	cp.State = VerifyFailed
	cp.Note = "the daemon restarted before the check finished"
	return &cp
}

// verifyFanParams are what verify-fan takes.
type verifyFanParams struct {
	Session   string            `json:"session"`
	Command   string            `json:"command"`
	TimeoutMS int               `json:"timeout_ms"`
	Env       map[string]string `json:"env"`
}

// verbVerifyFan answers verify-fan.
func (d *Daemon) verbVerifyFan(cs *connState, params json.RawMessage) (any, *verbError) {
	var p verifyFanParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Command) == "" {
		return nil, invalidParam("command", "command is required: the check to run in every attempt, as a shell line")
	}
	if len(p.Command) > fanVerifyMaxCommand {
		return nil, invalidParam("command", fmt.Sprintf("command is at most %d bytes: a shell line, not a script", fanVerifyMaxCommand))
	}
	if strings.ContainsRune(p.Command, 0) {
		return nil, invalidParam("command", "command holds a NUL byte")
	}
	if p.TimeoutMS < 0 {
		return nil, invalidParam("timeout_ms", "timeout_ms is a number of milliseconds, 0 for no limit")
	}
	env, _, verr := callerEnv(cs, p.Env)
	if verr != nil {
		return nil, verr
	}
	_, info, verr := d.fanTarget(p.Session)
	if verr != nil {
		return nil, verr
	}
	timeout := time.Duration(p.TimeoutMS) * time.Millisecond

	started := []string{}
	skipped := []map[string]any{}
	for _, s := range d.fanSiblings(info) {
		if !d.callerReachesSession(cs, s.Name) {
			continue
		}
		wt := s.worktreeListing()
		if wt == nil || wt.Gone {
			skipped = append(skipped, map[string]any{"session": s.Name, "reason": "its worktree directory is gone"})
			continue
		}
		if err := d.startFanVerify(s, wt.Path, p.Command, env, timeout); err != nil {
			skipped = append(skipped, map[string]any{"session": s.Name, "reason": err.Error()})
			continue
		}
		started = append(started, s.Name)
	}
	if len(started) == 0 {
		return nil, hintedVerbError(ErrVerbInternal, "no check was started in fan "+info.Group, &VerbHint{
			Detail: skippedDetail(skipped),
		})
	}
	out := map[string]any{
		"type":     "fan_verify_started",
		"group":    info.Group,
		"command":  p.Command,
		"sessions": started,
	}
	if len(skipped) > 0 {
		out["skipped"] = skipped
	}
	return out, nil
}

func skippedDetail(skipped []map[string]any) string {
	if len(skipped) == 0 {
		return "The caller reaches no session of the fan."
	}
	parts := make([]string, 0, len(skipped))
	for _, s := range skipped {
		parts = append(parts, fmt.Sprintf("%v: %v", s["session"], s["reason"]))
	}
	return strings.Join(parts, "; ")
}

// fanVerifyScript runs the check and reports how it ended on fd 3, which the
// check itself does not inherit, so its output cannot be taken for the
// status. A check that passed ends the window. One that failed keeps it open,
// so the output can be read, until the person presses enter.
const fanVerifyScript = `printf 'tuios verify: %s\n\n' "$1"
sh -c "$1" 3>&-
s=$?
printf '%s\n' "$s" >&3
exec 3>&-
if [ "$s" -eq 0 ]; then exit 0; fi
printf '\nThe check exited %s. This window stays open so the output can be read. Press enter to close it.\n' "$s"
read -r _
exit "$s"`

// startFanVerify opens the verify window in one sibling and watches it. A
// check still running there from before is stopped and its window closed.
func (d *Daemon) startFanVerify(sess *Session, dir, command string, env []string, timeout time.Duration) error {
	if prev := d.fanVerifies.take(sess.ID); prev != nil {
		prev.halt()
		_, _ = sess.CloseDaemonWindow(prev.window)
	}

	var (
		argv   []string
		r, w   *os.File
		extras []*os.File
	)
	if runtime.GOOS == "windows" {
		// No descriptor passes to a console process, so the window's own exit
		// status is the check's, and the window closes either way.
		argv = []string{"cmd.exe", "/c", command}
	} else {
		var err error
		if r, w, err = os.Pipe(); err != nil {
			return err
		}
		extras = []*os.File{w}
		argv = []string{"/bin/sh", "-c", fanVerifyScript, "tuios-verify", command}
	}
	none := Grants(0)
	sessionID := sess.ID
	onExit := func(ptyID string) { d.notifyPTYClosed(sessionID, ptyID) }
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Name:       fanVerifyWindow,
		Cwd:        dir,
		Command:    argv,
		Env:        env,
		Grants:     &none,
		extraFiles: extras,
	}, onExit)
	if w != nil {
		// The child holds its own copy. Closing this one is what lets the
		// reader see the end of the pipe when the child is gone.
		_ = w.Close()
	}
	if err != nil {
		if r != nil {
			_ = r.Close()
		}
		return err
	}

	run := &fanVerifyRun{window: win.ID, stop: make(chan struct{})}
	d.fanVerifies.put(sess.ID, run)
	rec := &FanVerify{Command: command, State: VerifyRunning, StartedAt: time.Now().UnixNano()}
	sess.setFanVerify(rec)

	status := make(chan *int, 1)
	if r != nil {
		go func() { status <- readVerifyStatus(r, sess, win.PTYID) }()
	} else {
		go func() {
			code, exited := d.waitPopupExit(sess, win, 0)
			if !exited {
				status <- nil
				return
			}
			status <- &code
		}()
	}
	go d.watchFanVerify(sess, run, rec, status, timeout)
	return nil
}

// readVerifyStatus reads the status the script writes on fd 3. A pipe that
// ends with no number in it is a script that was killed, and the window's
// exit status stands for it when there is one.
func readVerifyStatus(r *os.File, sess *Session, ptyID string) *int {
	defer func() { _ = r.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(r, 64))
	if code, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
		return &code
	}
	if pty := sess.GetPTY(ptyID); pty != nil {
		// The pipe closes as the process exits; the status lands a moment
		// after.
		for range 20 {
			if code, exited := pty.ExitStatus(); exited {
				if code < 0 {
					return nil
				}
				return &code
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return nil
}

// watchFanVerify records how one check ended. A check that passed closes its
// window; one that failed leaves it open. A check that runs past its timeout
// is failed and its window closed. A check stopped by a newer one records
// nothing, since the newer one owns the record.
func (d *Daemon) watchFanVerify(sess *Session, run *fanVerifyRun, rec *FanVerify, status <-chan *int, timeout time.Duration) {
	var deadline <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		deadline = t.C
	}
	var code *int
	note := ""
	select {
	case code = <-status:
	case <-deadline:
		note = "timed out after " + timeout.String()
	case <-run.stop:
		return
	case <-d.ctx.Done():
		return
	}
	if !d.fanVerifies.finish(sess.ID, run) {
		return
	}
	done := *rec
	done.Exit = code
	done.FinishedAt = time.Now().UnixNano()
	done.Note = note
	done.State = VerifyFailed
	if code != nil && *code == 0 && note == "" {
		done.State = VerifyPassed
	}
	sess.setFanVerify(&done)
	if done.State == VerifyPassed || note != "" {
		// A passed check's window has already ended on its own; closing it
		// here takes it out of a session no client is attached to. A timed
		// out one is ended here.
		_, _ = sess.CloseDaemonWindow(run.window)
	}
}

// setFanVerify replaces the session's verify record.
func (s *Session) setFanVerify(v *FanVerify) {
	_ = s.mutateState(func(st *SessionState) error {
		if st.Worktree != nil {
			st.Worktree.Verify = v
		}
		return nil
	})
}

// fanVerifyRuns holds the check running in each fan session, by session id.
// The zero value is ready.
type fanVerifyRuns struct {
	mu   sync.Mutex
	runs map[string]*fanVerifyRun
}

// fanVerifyRun is one running check: its window, and the channel that stops
// its watcher.
type fanVerifyRun struct {
	window string
	stop   chan struct{}
	once   sync.Once
}

func (r *fanVerifyRun) halt() { r.once.Do(func() { close(r.stop) }) }

// put records run as the session's check.
func (t *fanVerifyRuns) put(session string, run *fanVerifyRun) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runs == nil {
		t.runs = make(map[string]*fanVerifyRun)
	}
	t.runs[session] = run
}

// take removes and returns the session's check, nil for none.
func (t *fanVerifyRuns) take(session string) *fanVerifyRun {
	t.mu.Lock()
	defer t.mu.Unlock()
	run := t.runs[session]
	delete(t.runs, session)
	return run
}

// finish removes run when it is still the session's check, and reports
// whether it was.
func (t *fanVerifyRuns) finish(session string, run *fanVerifyRun) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runs[session] != run {
		return false
	}
	delete(t.runs, session)
	return true
}

// running reports whether a check runs in the session now.
func (t *fanVerifyRuns) running(session string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runs[session] != nil
}

// keepFanParams are what keep-fan takes.
type keepFanParams struct {
	Session string `json:"session"`
	Stash   bool   `json:"stash"`
	Force   bool   `json:"force"`
}

// verbKeepFan answers keep-fan: every sibling of the kept session is removed
// the way remove-worktree removes it, each on its own, so one sibling with
// uncommitted work is left in place and the rest still go. Branches are never
// deleted.
func (d *Daemon) verbKeepFan(cs *connState, params json.RawMessage) (any, *verbError) {
	var p keepFanParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Session == "" {
		return nil, invalidParam("session", "session is required: the attempt to keep")
	}
	kept, info, verr := d.worktreeTarget(p.Session)
	if verr != nil {
		return nil, verr
	}
	if info.Group == "" {
		return nil, hintedVerbError(ErrVerbInvalidParams, "session "+kept.Name+" is not part of a fan, so it has no siblings to remove", &VerbHint{
			Param:   "session",
			Verb:    "remove-worktree",
			Command: "tuios worktree rm " + kept.Name,
			Detail:  "Nothing was removed. remove-worktree removes one worktree.",
		})
	}
	removed := []map[string]any{}
	left := 0
	for _, s := range d.fanSiblings(info) {
		if s == kept {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"session": s.Name, "stash": p.Stash, "force": p.Force})
		res, verr := d.verbRemoveWorktree(cs, raw)
		if verr != nil {
			left++
			out := map[string]any{"session": s.Name, "removed": false, "note": verr.Message, "code": verr.Code}
			removed = append(removed, out)
			continue
		}
		// The session and its verify window are gone with the worktree, so a
		// check still running there has nothing left to record into. One in a
		// sibling left in place runs on.
		if run := d.fanVerifies.take(s.ID); run != nil {
			run.halt()
		}
		out, _ := res.(map[string]any)
		if out == nil {
			out = map[string]any{"session": s.Name}
		}
		delete(out, "type")
		out["removed"] = true
		removed = append(removed, out)
	}
	return map[string]any{
		"type":    "fan_kept",
		"kept":    kept.Name,
		"branch":  info.Branch,
		"group":   info.Group,
		"repo":    info.Repo,
		"removed": removed,
		"left":    left,
	}, nil
}
