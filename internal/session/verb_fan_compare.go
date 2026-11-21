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

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

const fanGitTimeout = 10 * time.Second

const fanVerifyMaxCommand = 4096

const fanVerifyWindow = "verify"

func (d *Daemon) fanTarget(cs *connState, name string) (*Session, *WorktreeInfo, *verbError) {
	sess, info, verr := d.worktreeTarget(cs, name)
	if verr != nil {
		return nil, nil, verr
	}
	if info.Group == "" {
		var fans []string
		for _, s := range d.manager.AllSessions() {
			if wt := s.Worktree(); wt != nil && wt.Group != "" && d.callerReachesSession(cs, s.Name()) {
				fans = append(fans, s.Name())
			}
		}
		return nil, nil, hintedVerbError(ErrVerbInvalidParams, "session "+sess.Name()+" is a worktree session that is not part of a fan", &VerbHint{
			Param:     "session",
			Command:   "tuios worktree ls",
			Available: fans,
			Detail:    "Name any session a fan started. A worktree made on its own has no attempts to compare.",
		})
	}
	return sess, info, nil
}

func (d *Daemon) fanSiblings(info *WorktreeInfo) []*Session {
	var out []*Session
	for _, s := range d.manager.AllSessions() {
		wt := s.Worktree()
		if sameFan(wt, info) {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b *Session) int { return fanOrder(a.Name(), b.Name()) })
	return out
}

func fanOrder(a, b string) int {
	stemA, nA := fanSuffix(a)
	stemB, nB := fanSuffix(b)
	if stemA != stemB {
		return strings.Compare(a, b)
	}
	return nA - nB
}

func fanSuffix(name string) (string, int) {
	if i := strings.LastIndexByte(name, '-'); i > 0 {
		if n, err := strconv.Atoi(name[i+1:]); err == nil && n > 1 {
			return name[:i], n
		}
	}
	return name, 1
}

func (d *Daemon) callerReachesSession(cs *connState, target string) bool {
	if cs == nil {
		return true
	}
	if sc := cs.scope.Load(); sc != nil && sc.own && !d.sessionInScope(d.sessionNameByID(sc.sessionID), target) {
		return false
	}
	if pa := cs.paneView.Load(); pa != nil && !pa.grants.Has(GrantAdmin) && !d.sessionInScope(pa.session, target) {
		return false
	}
	return true
}

type fanCompareParams struct {
	Session string `json:"session"`
	Changes *bool  `json:"changes"`
	Detail  bool   `json:"detail"`
}

const (
	fanDetailFiles = 8
	fanDetailLines = 6
	fanIgnoredMax  = 200
)

type fanCompareRow struct {
	Session      string              `json:"session"`
	Branch       string              `json:"branch"`
	Path         string              `json:"path"`
	Agent        string              `json:"agent"`
	Harness      string              `json:"harness"`
	State        string              `json:"state"`
	Files        *int                `json:"files,omitempty"`
	Added        *int                `json:"added,omitempty"`
	Removed      *int                `json:"removed,omitempty"`
	Ahead        *int                `json:"ahead,omitempty"`
	Dirty        *bool               `json:"dirty,omitempty"`
	BaseSHA      string              `json:"base_sha,omitempty"`
	PromptStatus string              `json:"prompt_status"`
	Verify       *FanVerify          `json:"verify,omitempty"`
	LastCommand  *fanLastCommand     `json:"last_command,omitempty"`
	Gone         bool                `json:"gone,omitempty"`
	Note         string              `json:"note,omitempty"`
	FileList     []worktree.FileStat `json:"file_list,omitempty"`
	Lines        []worktree.DiffLine `json:"lines,omitempty"`
	Notes        []string            `json:"notes,omitempty"`
	Ignored      []string            `json:"ignored,omitempty"`
	IgnoredCount int                 `json:"ignored_count,omitempty"`
	IgnoredError string              `json:"ignored_error,omitempty"`
	IgnoredHash  string              `json:"ignored_hash,omitempty"`
	Cost         string              `json:"cost,omitempty"`
	StartedAt    int64               `json:"started_at,omitempty"`
	DoneAt       int64               `json:"done_at,omitempty"`
}

type fanLastCommand struct {
	Cmdline string `json:"cmdline"`
	Exit    *int   `json:"exit,omitempty"`
	At      int64  `json:"at"`
	Window  string `json:"window"`
}

func (d *Daemon) verbCompareFan(cs *connState, params json.RawMessage) (any, *verbError) {
	var p fanCompareParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Detail {
		if verr := d.checkLinkPolicy(cs, linkCapReadOut, "compare-fan with detail"); verr != nil {
			return nil, verr
		}
	}
	_, info, verr := d.fanTarget(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	changes := p.Changes == nil || *p.Changes
	var siblings []*Session
	pa := cs.paneViewOrNil()
	for _, s := range d.fanSiblings(info) {
		if d.callerReachesSession(cs, s.Name()) || d.judgeReaches(pa, s.Name()) {
			siblings = append(siblings, s)
		}
	}

	rows := make([]fanCompareRow, len(siblings))
	maxUntracked := d.checkpoints.maxUntracked.Load()
	var wg sync.WaitGroup
	for i, s := range siblings {
		rows[i] = d.fanRow(s)
		if !changes || rows[i].Gone {
			continue
		}
		wg.Add(1)
		go func(row *fanCompareRow, wt *WorktreeInfo) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(d.ctx, fanGitTimeout)
			defer cancel()
			fanRowChanges(ctx, row, wt, maxUntracked)
			if p.Detail && row.BaseSHA != "" {
				row.FileList, row.Lines, _ = worktree.WorkingDetail(ctx, wt.Path, row.BaseSHA, fanDetailFiles, fanDetailLines, maxUntracked)
			}
			if p.Detail {
				var err error
				row.Ignored, row.IgnoredCount, err = worktree.IgnoredFiles(ctx, wt.Path, fanIgnoredMax)
				if err != nil {
					row.IgnoredError = err.Error()
				} else {
					row.IgnoredHash = worktree.IgnoredHash(row.Ignored, row.IgnoredCount)
				}
			}
		}(&rows[i], s.Worktree())
	}
	wg.Wait()

	base := info.Base
	if base == "" {
		if b, err := worktree.CurrentBranch(info.RepoRoot); err == nil && b != "" {
			base = b
		} else {
			base = "HEAD"
		}
	}
	out := map[string]any{
		"type":      "fan_compare",
		"group":     info.Group,
		"fan_id":    info.FanID,
		"repo":      info.Repo,
		"repo_root": info.RepoRoot,
		"base":      base,
		"prompt":    info.Prompt,
		"rows":      rows,
		"total":     len(rows),
	}
	if v := info.Verdict; v != nil {
		out["verdict"] = map[string]any{"pick": v.Pick, "summary": v.Summary, "reasons": v.Reasons, "by": v.By, "judge": v.Judge, "at": v.At}
	}
	return out, nil
}

func (d *Daemon) fanRow(s *Session) fanCompareRow {
	wt := s.worktreeListing()
	if wt == nil {
		wt = &WorktreeInfo{}
	}
	row := fanCompareRow{
		Session:      s.Name(),
		Branch:       wt.Branch,
		Path:         wt.Path,
		Agent:        wt.Agent,
		PromptStatus: wt.PromptStatus,
		Verify:       d.fanVerifyReport(s, wt.Verify),
		Gone:         wt.Gone,
		StartedAt:    wt.PromptAt,
	}
	if wt.Verdict != nil {
		row.Notes = wt.Verdict.Notes
	}
	st := s.GetState()
	states := make([]string, 0, len(st.Windows))
	var newest *fanLastCommand
	for _, w := range st.Windows {
		states = append(states, string(w.AgentState))
		if row.Harness == "" {
			row.Harness = w.AgentHarness
		}
		for _, t := range w.AgentMeta {
			if t.Key == "cost" && row.Cost == "" {
				row.Cost = t.Value
			}
		}
		if w.AgentState == AgentStateDone && w.AgentStateAt > row.DoneAt {
			row.DoneAt = w.AgentStateAt
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

func fanRowChanges(ctx context.Context, row *fanCompareRow, wt *WorktreeInfo, maxUntracked int64) {
	if wt == nil {
		return
	}
	base := wt.Base
	if base == "" {
		head, err := worktree.HeadCommitCtx(ctx, wt.RepoRoot)
		if err != nil {
			row.Note = "no base to count from: " + err.Error()
			return
		}
		base = head
	}
	head, base, err := worktree.WorkRevs(ctx, wt.Path, base)
	if err != nil {
		row.Note = err.Error()
		return
	}
	from := base
	if sha, err := worktree.MergeBaseCtx(ctx, wt.Path, head, base); err == nil && sha != "" {
		row.BaseSHA = sha
		from = sha
	} else if ctx.Err() != nil {
		row.Note = err.Error()
		return
	}
	n, err := worktree.WorkingNumstat(ctx, wt.Path, from, maxUntracked)
	if err != nil {
		row.Note = err.Error()
		return
	}
	row.Files, row.Added, row.Removed = &n.Files, &n.Added, &n.Removed
	if ahead, err := worktree.AheadCtx(ctx, wt.Path, from); err == nil {
		row.Ahead = &ahead
	}
	if c, err := worktree.ChangesCtx(ctx, wt.Path); err == nil {
		dirty := c > 0
		row.Dirty = &dirty
	}
}

func (d *Daemon) fanVerifyReport(s *Session, v *FanVerify) *FanVerify {
	if v == nil || v.State != VerifyRunning || d.fanVerifies.running(s.ID) {
		return v
	}
	if wt := s.worktreeListing(); wt != nil && wt.Verify != nil {
		now := wt.Verify
		if now.State != VerifyRunning || (now.StartedAt != v.StartedAt && d.fanVerifies.running(s.ID)) {
			return now
		}
		v = now
	}
	cp := *v
	cp.State = VerifyFailed
	cp.Note = "the daemon restarted before the check finished"
	return &cp
}

type verifyFanParams struct {
	Session   string            `json:"session"`
	Command   string            `json:"command"`
	TimeoutMS int               `json:"timeout_ms"`
	Env       map[string]string `json:"env"`
}

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
	_, info, verr := d.fanTarget(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	timeout := time.Duration(p.TimeoutMS) * time.Millisecond

	started := []string{}
	skipped := []map[string]any{}
	for _, s := range d.fanSiblings(info) {
		if !d.callerReachesSession(cs, s.Name()) {
			continue
		}
		wt := s.worktreeListing()
		if wt == nil || wt.Gone {
			skipped = append(skipped, map[string]any{"session": s.Name(), "reason": "its worktree directory is gone"})
			continue
		}
		if err := d.startFanVerify(s, wt.Path, p.Command, env, timeout); err != nil {
			skipped = append(skipped, map[string]any{"session": s.Name(), "reason": err.Error()})
			continue
		}
		started = append(started, s.Name())
	}
	if len(started) == 0 {
		return nil, hintedVerbError(ErrVerbInternal, "no check was started in fan "+info.Group, &VerbHint{
			Detail: skippedDetail(skipped),
		})
	}
	d.autoStageFan(info.FanID, moveVerifyStarted, taskReasonVerifyStarted)
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

const fanVerifyScript = `printf 'tuios verify: %s\n\n' "$1"
sh -c "$1" 3>&-
s=$?
printf '%s\n' "$s" >&3
exec 3>&-
if [ "$s" -eq 0 ]; then exit 0; fi
printf '\nThe check exited %s. This window stays open so the output can be read. Press enter to close it.\n' "$s"
read -r _
exit "$s"`

func (d *Daemon) startFanVerify(sess *Session, dir, command string, env []string, timeout time.Duration) error {
	prev, failedWindow := d.fanVerifies.take(sess.ID)
	if prev != nil {
		prev.halt()
		_, _ = sess.CloseDaemonWindow(prev.window, "the daemon, as a new fan check replaced it")
	}
	if failedWindow != "" {
		_, _ = sess.CloseDaemonWindow(failedWindow, "the daemon, as a new fan check replaced the failed one")
	}

	var (
		argv    []string
		r, w    *os.File
		extras  []*os.File
		release = func() {}
	)
	if runtime.GOOS == "windows" {
		argv = []string{"cmd.exe", "/c", command}
	} else {
		var err error
		if r, w, err = os.Pipe(); err != nil {
			return err
		}
		extras = []*os.File{w}
		argv = []string{"/bin/sh", "-c", fanVerifyScript, "tuios-verify", command}
		release = d.pipes.hold("a fan check is running in session " + sess.Name())
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
		_ = w.Close()
	}
	if err != nil {
		release()
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
		go func() {
			defer release()
			status <- readVerifyStatus(r, sess, win.PTYID)
		}()
	} else {
		go func() {
			code, exited := d.waitPopupExit(sess, win, 0, nil)
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

func readVerifyStatus(r *os.File, sess *Session, ptyID string) *int {
	defer func() { _ = r.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(r, 64))
	if code, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
		return &code
	}
	if pty := sess.GetPTY(ptyID); pty != nil {
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
	done := *rec
	done.Exit = code
	done.FinishedAt = time.Now().UnixNano()
	done.Note = note
	done.State = VerifyFailed
	if code != nil && *code == 0 && note == "" {
		done.State = VerifyPassed
	}
	keptOpen := done.State == VerifyFailed && note == ""
	current := false
	_ = sess.mutateState(func(st *SessionState) error {
		current = d.fanVerifies.finish(sess.ID, run, keptOpen)
		if current && st.Worktree != nil {
			st.Worktree.Verify = &done
		}
		return nil
	})
	if !current {
		return
	}
	d.noteFanVerifyEnded(sess)
	if !keptOpen {
		_, _ = sess.CloseDaemonWindow(run.window, "the daemon, as its fan check ended")
	}
}

func (s *Session) setFanVerify(v *FanVerify) {
	_ = s.mutateState(func(st *SessionState) error {
		if st.Worktree != nil {
			st.Worktree.Verify = v
		}
		return nil
	})
}

type fanVerifyRuns struct {
	mu     sync.Mutex
	runs   map[string]*fanVerifyRun
	failed map[string]string
}

type fanVerifyRun struct {
	window string
	stop   chan struct{}
	once   sync.Once
}

func (r *fanVerifyRun) halt() { r.once.Do(func() { close(r.stop) }) }

func (t *fanVerifyRuns) put(session string, run *fanVerifyRun) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runs == nil {
		t.runs = make(map[string]*fanVerifyRun)
	}
	t.runs[session] = run
}

func (t *fanVerifyRuns) take(session string) (*fanVerifyRun, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	run := t.runs[session]
	delete(t.runs, session)
	window := t.failed[session]
	delete(t.failed, session)
	return run, window
}

func (t *fanVerifyRuns) finish(session string, run *fanVerifyRun, keptOpen bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runs[session] != run {
		return false
	}
	delete(t.runs, session)
	if keptOpen {
		if t.failed == nil {
			t.failed = make(map[string]string)
		}
		t.failed[session] = run.window
	}
	return true
}

func (t *fanVerifyRuns) running(session string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runs[session] != nil
}

type keepFanParams struct {
	Session   string            `json:"session"`
	Stash     bool              `json:"stash"`
	Force     bool              `json:"force"`
	Merge     bool              `json:"merge"`
	MergeMode string            `json:"merge_mode"`
	Into      string            `json:"into"`
	Group     string            `json:"group"`
	RepoRoot  string            `json:"repo_root"`
	FanID     *string           `json:"fan_id"`
	Siblings  []string          `json:"siblings"`
	Ignored   map[string]string `json:"ignored"`
}

func (d *Daemon) verbKeepFan(cs *connState, params json.RawMessage) (any, *verbError) {
	var p keepFanParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Session == "" {
		return nil, invalidParam("session", "session is required: the attempt to keep")
	}
	kept, info, verr := d.worktreeTarget(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if info.Group == "" {
		return nil, hintedVerbError(ErrVerbInvalidParams, "session "+kept.Name()+" is not part of a fan, so it has no siblings to remove", &VerbHint{
			Param:   "session",
			Verb:    "remove-worktree",
			Command: "tuios worktree rm " + kept.Name(),
			Detail:  "Nothing was removed. remove-worktree removes one worktree.",
		})
	}
	if (p.Group != "" && p.Group != info.Group) || (p.RepoRoot != "" && canonRoot(p.RepoRoot) != canonRoot(info.RepoRoot)) || (p.FanID != nil && *p.FanID != info.FanID) {
		return nil, hintedVerbError(ErrVerbInvalidParams, "session "+kept.Name()+" is not an attempt of the fan named by group and repo_root", &VerbHint{
			Param:  "session",
			Detail: "Nothing was removed. The session may have been renamed. Read the fan again with compare-fan.",
		})
	}
	if !p.Merge && (p.MergeMode != "" || p.Into != "") {
		return nil, invalidParam("merge", "merge_mode and into apply only with merge")
	}
	if p.MergeMode != "" && !slices.Contains(shipMergeModes, p.MergeMode) {
		return nil, invalidParam("merge_mode", "merge_mode is merge, squash or ff-only", shipMergeModes...)
	}
	if p.Into != "" {
		if err := worktree.ValidBranch(p.Into); err != nil {
			return nil, invalidParam("into", err.Error())
		}
	}
	var merged map[string]any
	if p.Merge {
		recorded := info.Base
		if recorded == "" && info.Managed {
			if b, err := worktree.CurrentBranch(info.RepoRoot); err == nil && b != info.Branch {
				recorded = b
			}
		}
		res, verr := d.shipMerge(d.shipCallerIsPerson(cs), kept.Name(), info.Path, info.RepoRoot, recorded, p.Into, p.MergeMode, "")
		if verr != nil {
			if verr.Hint != nil {
				verr.Hint.Detail += " No sibling was removed."
			}
			return nil, verr
		}
		merged = res
	}
	if verr := d.checkKeepAsShown(kept, info, p.Siblings, p.Ignored); verr != nil {
		return nil, verr
	}
	removed := []map[string]any{}
	left := 0
	for _, s := range d.fanSiblings(info) {
		if s == kept {
			continue
		}
		raw, _ := jsonx.Marshal(map[string]any{"session": s.Name(), "stash": p.Stash, "force": p.Force})
		res, verr := d.verbRemoveWorktree(cs, raw)
		if verr != nil {
			left++
			out := map[string]any{"session": s.Name(), "removed": false, "note": verr.Message, "code": verr.Code}
			removed = append(removed, out)
			continue
		}
		if run, _ := d.fanVerifies.take(s.ID); run != nil {
			run.halt()
		}
		out, _ := res.(map[string]any)
		if out == nil {
			out = map[string]any{"session": s.Name()}
		}
		delete(out, "type")
		out["removed"] = true
		removed = append(removed, out)
	}
	out := map[string]any{
		"type":    "fan_kept",
		"kept":    kept.Name(),
		"branch":  info.Branch,
		"group":   info.Group,
		"repo":    info.Repo,
		"removed": removed,
		"left":    left,
	}
	if merged != nil {
		out["merge"] = merged
	}
	return out, nil
}

func (d *Daemon) checkKeepAsShown(kept *Session, info *WorktreeInfo, siblings []string, ignored map[string]string) *verbError {
	if siblings == nil && ignored == nil {
		return nil
	}
	var others []string
	for _, s := range d.fanSiblings(info) {
		if s != kept {
			others = append(others, s.Name())
		}
	}
	if siblings != nil {
		a, b := slices.Clone(others), slices.Clone(siblings)
		slices.Sort(a)
		slices.Sort(b)
		if !slices.Equal(a, b) {
			return hintedVerbError(ErrVerbInvalidParams, "the fan's other attempts are not the ones the caller showed", &VerbHint{
				Param: "siblings", Available: others,
				Detail: "Nothing was removed. Read the fan again with compare-fan.",
			})
		}
	}
	for _, name := range others {
		want, ok := ignored[name]
		if !ok {
			if ignored == nil {
				continue
			}
			return hintedVerbError(ErrVerbInvalidParams, "ignored names no list for "+name+", which a keep removes", &VerbHint{
				Param: "ignored", Detail: "Nothing was removed. Send the ignored_hash of every attempt to remove.",
			})
		}
		s := d.manager.GetSession(name)
		wt := s.Worktree()
		if wt == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(d.ctx, fanGitTimeout)
		names, n, err := worktree.IgnoredFiles(ctx, wt.Path, fanIgnoredMax)
		cancel()
		if err != nil {
			return newVerbError(ErrVerbCommandFailed, "could not list the files git ignores in "+name+", so nothing was removed: "+err.Error())
		}
		if worktree.IgnoredHash(names, n) != want {
			return hintedVerbError(ErrVerbInvalidParams, "the files git ignores in "+name+" are not the ones the caller showed", &VerbHint{
				Param:  "ignored",
				Detail: "Nothing was removed. Read the fan again with compare-fan, and show the files a keep deletes.",
			})
		}
	}
	return nil
}
