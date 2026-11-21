package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

var delegateRoles = []string{"implementation", "research", "review", "design", "test", "general"}

const (
	delegateShared   = "shared"
	delegateWorktree = "worktree"

	delegateAsync = "async"
	delegateWait  = "wait"

	delegateWorking = "working"
	delegateAsking  = "asking"
	delegateDone    = "done"
	delegateErrored = "errored"
	delegateClosed  = "closed"
	delegateStopped = "stopped"
)

const (
	delegateMaxWait           = 10 * time.Minute
	delegateAnswerMax         = 16 << 10
	delegateCaptureLines      = 200
	delegateTranscriptEntries = 60
)

const delegateNext = "End your turn now. tuios wakes you when the child agent finishes. Do not wait or poll."

type delegateOutcome struct {
	kind      string
	answer    string
	source    string
	truncated bool
	question  *delegateQuestion
}

type delegateQuestion struct {
	Kind    string   `json:"kind"`
	Message string   `json:"message,omitempty"`
	Lines   []string `json:"lines"`
}

type delegatePending struct {
	look     sync.Mutex
	parent   *PaneRef
	by       string
	baseline int
	waiters  []chan delegateOutcome
}

type delegates struct {
	mu    sync.Mutex
	panes map[string]*delegatePending
	count atomic.Int64
}

func (t *delegates) add(window string, p *delegatePending) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.panes == nil {
		t.panes = map[string]*delegatePending{}
	}
	if _, ok := t.panes[window]; !ok {
		t.count.Add(1)
	}
	t.panes[window] = p
}

func (t *delegates) get(window string) *delegatePending {
	if t.count.Load() == 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.panes[window]
}

func (t *delegates) drop(window string, p *delegatePending) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.panes[window] == p {
		delete(t.panes, window)
		t.count.Add(-1)
	}
}

func (t *delegates) deliver(p *delegatePending, o delegateOutcome) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(p.waiters) == 0 {
		return false
	}
	for _, ch := range p.waiters {
		ch <- o
	}
	p.waiters = nil
	return true
}

func (t *delegates) unwait(p *delegatePending, ch chan delegateOutcome) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p.waiters = slices.DeleteFunc(p.waiters, func(c chan delegateOutcome) bool { return c == ch })
}

func (d *Daemon) noteDelegateEvent(ev SessionEvent) {
	if d.delegates.count.Load() == 0 {
		return
	}
	if ev.Type != EventAgentState && ev.Type != EventWindowClosed {
		return
	}
	if d.delegates.get(ev.Window) == nil {
		return
	}
	go d.delegateLook(ev.Window, ev.Type == EventWindowClosed)
}

func (d *Daemon) delegateLook(window string, closedEv bool) {
	p := d.delegates.get(window)
	if p == nil {
		return
	}
	p.look.Lock()
	defer p.look.Unlock()
	if d.delegates.get(window) != p {
		return
	}
	sess := d.sessionHoldingWindow(window)
	var w WindowState
	ok := false
	if sess != nil && !closedEv {
		w, ok = findWindowState(sess.GetState(), window)
	}
	if !ok {
		d.delegateGone(window, p)
		return
	}
	rec := w.Delegation
	if rec == nil || delegateFinished(rec.State) {
		d.delegates.drop(window, p)
		return
	}
	now := time.Now().UnixMilli()
	switch {
	case w.AgentState == AgentStateNeedsInput:
		if rec.State == delegateAsking {
			return
		}
		q := d.delegateQuestionOf(sess, w)
		kind := "question"
		if q.Kind == "approval" {
			kind = "approval"
		}
		if d.delegates.deliver(p, delegateOutcome{kind: kind, question: q}) || kind == "approval" {
			if kind == "question" {
				sess.updateDelegation(window, func(r *Delegation) { r.State, r.AskedAt = delegateAsking, now })
			}
			return
		}
		var mail uint64
		if rec.Parent != nil && d.attention.parentFirst.Load() {
			mail = d.delegateMail(rec, sess, w, "Delegated task asks a question: "+rec.Role, strings.Join(q.Lines, "\n"), "", "")
		}
		if mail == 0 {
			d.attention.releaseParentHold(sess.Name(), window)
		}
		sess.updateDelegation(window, func(r *Delegation) {
			r.State, r.AskedAt, r.QuestionMail = delegateAsking, now, mail
		})
	case w.AgentState == AgentStateIdle || w.AgentState == AgentStateDone || w.AgentState == AgentStateErrored || w.AgentState == AgentStateLimitReached:
		if w.AgentReason == AgentReasonSilent || w.AgentStateAt <= rec.StartedAt*int64(time.Millisecond) {
			return
		}
		state := delegateDone
		if w.AgentState == AgentStateErrored || w.AgentState == AgentStateLimitReached {
			state = delegateErrored
		}
		answer, source, truncated := d.delegateAnswer(sess, w, p.baseline)
		o := delegateOutcome{kind: state, answer: answer, source: source, truncated: truncated}
		var mail uint64
		if !d.delegates.deliver(p, o) && rec.Parent != nil {
			subject := "Delegated task done: " + rec.Role
			if state == delegateErrored {
				subject = "Delegated task stopped on an error: " + rec.Role
			}
			mail = d.delegateMail(rec, sess, w, subject, answer, "delegate-answer-"+shortWindowID(window)+".txt", state)
		}
		sess.updateDelegation(window, func(r *Delegation) {
			r.State, r.FinishedAt, r.AskedAt, r.QuestionMail, r.ResultMail = state, now, 0, 0, mail
		})
		d.delegates.drop(window, p)
	case rec.State == delegateAsking:
		sess.updateDelegation(window, func(r *Delegation) { r.State, r.AskedAt, r.QuestionMail = delegateWorking, 0, 0 })
	}
}

func (d *Daemon) delegateGone(window string, p *delegatePending) {
	d.delegates.drop(window, p)
	if d.delegates.deliver(p, delegateOutcome{kind: delegateClosed}) || p.parent == nil {
		return
	}
	d.delegateMail(&Delegation{Parent: p.parent, By: p.by}, nil, WindowState{ID: window},
		"Delegated task closed: window "+shortWindowID(window), "The child window closed before the task finished.", "", delegateClosed)
}

func delegateFinished(state string) bool {
	switch state {
	case delegateDone, delegateErrored, delegateClosed, delegateStopped:
		return true
	}
	return false
}

func (s *Session) updateDelegation(window string, fn func(*Delegation)) {
	_ = s.mutateState(func(state *SessionState) error {
		idx, err := findWindowStateIndex(state.Windows, window)
		if err != nil {
			return err
		}
		old := state.Windows[idx].Delegation
		if old == nil {
			return nil
		}
		rec := *old
		fn(&rec)
		state.Windows[idx].Delegation = &rec
		return nil
	})
}

func restoredDelegation(saved *Delegation) *Delegation {
	rec := *saved
	if !delegateFinished(rec.State) {
		rec.State = delegateStopped
		rec.FinishedAt = time.Now().UnixMilli()
		rec.AskedAt, rec.QuestionMail = 0, 0
	}
	return &rec
}

func (d *Daemon) resumeDelegations() {
	var windows []string
	for _, sess := range d.manager.AllSessions() {
		for _, w := range sess.GetState().Windows {
			rec := w.Delegation
			if rec == nil || delegateFinished(rec.State) {
				continue
			}
			p := &delegatePending{parent: rec.Parent, by: rec.By, baseline: -1}
			d.delegates.add(w.ID, p)
			windows = append(windows, w.ID)
		}
	}
	for _, id := range windows {
		go d.delegateLook(id, false)
	}
}

func (d *Daemon) delegateQuestionOf(sess *Session, w WindowState) *delegateQuestion {
	q := &delegateQuestion{Kind: w.AgentKind, Message: printableLine(w.AgentMessage), Lines: []string{}}
	if look, verr := d.lookAtPrompt(sess, w.ID); verr == nil && look.found {
		for _, l := range look.prompt.Lines {
			q.Lines = append(q.Lines, printableLine(l))
		}
	}
	if len(q.Lines) == 0 && q.Message != "" {
		q.Lines = append(q.Lines, q.Message)
	}
	return q
}

func (d *Daemon) delegateAnswer(sess *Session, w WindowState, baseline int) (answer, source string, truncated bool) {
	if text, ok := d.delegateTranscriptAnswer(sess, w.ID); ok {
		answer, source = text, "transcript"
	} else if pty, err := d.resolvePTYForTarget(sess, w.ID); err == nil {
		content := pty.CaptureContent(true, false)
		if baseline < 0 {
			baseline = max(contentLines(content)-delegateCaptureLines, 0)
		}
		answer, truncated = tailLines(content, baseline, delegateCaptureLines)
		source = "screen"
	}
	if len(answer) > delegateAnswerMax {
		answer, truncated = cutUTF8(answer, delegateAnswerMax), true
	}
	return answer, source, truncated
}

func (d *Daemon) delegateTranscriptAnswer(sess *Session, window string) (string, bool) {
	path, opts, ok := d.transcriptOptions(sess, window)
	if !ok {
		return "", false
	}
	opts.Limit = delegateTranscriptEntries
	page, err := transcriptview.Read(path, opts)
	if err != nil {
		return "", false
	}
	entries := page.Entries
	if i := lastIndexFunc(entries, func(e transcriptview.Entry) bool {
		return e.Role == transcriptview.RoleUser && e.Kind == transcriptview.KindText
	}); i >= 0 {
		entries = entries[i+1:]
	}
	return assistantText(entries)
}

func (d *Daemon) delegateMail(rec *Delegation, sess *Session, child WindowState, subject, body, stashName, finished string) uint64 {
	return d.mailParent(*rec.Parent, rec.By, sess, child, subject, body, stashName, finished)
}

func (d *Daemon) mailParent(parent PaneRef, by string, sess *Session, child WindowState, subject, body, stashName, finished string) uint64 {
	parentSess := d.sessionHoldingWindow(parent.Window)
	if parentSess == nil {
		return 0
	}
	pw, ok := findWindowState(parentSess.GetState(), parent.Window)
	if !ok {
		return 0
	}
	if len(subject) > agentMsgMaxSubject {
		subject = cutUTF8(subject, agentMsgMaxSubject)
	}
	msg := AgentMessage{
		Kind:    agentMsgDirect,
		From:    child.ID,
		To:      pw.ID,
		ToLabel: windowLabelOf(pw),
		Subject: subject,
		Text:    body,
	}
	if sess != nil {
		msg.FromLabel = windowLabelOf(child)
	}
	if limit := agentMsgMaxText - 256; len(body) > limit {
		msg.Text = cutUTF8(body, limit)
		if stashName != "" {
			referenced := func() map[string]bool { return d.agents.referencedPaths(parentSess.Name()) }
			if res, err := d.stash.putFrom(parentSess.ID, stashName, strings.NewReader(body), referenced); err == nil {
				if att, err := classifyAttachment(res.Entry.Path); err == nil {
					att.Stashed = true
					msg.Attachments = append(msg.Attachments, att)
					msg.Text += "\n\n[tuios: the answer is longer than one message. The whole answer is in the attached file.]"
				}
			}
		}
	}
	stored := d.agents.send(parentSess.Name(), msg)
	d.broadcastToSession(parentSess.ID, MsgAgentMail, &AgentMailPayload{Message: stored}, "")
	d.events.publish(streamEvent{Type: EventAgentMessage, Session: parentSess.Name(), Window: stored.To})
	origin := queueOrigin{kind: queueByShell}
	if by != "" {
		origin = queueOrigin{kind: "pane", window: by, session: d.sessionOfWindow(by)}
	}
	d.noteMailSentAs(parentSess.Name(), pw.ID, stored.ID, origin, finished)
	return stored.ID
}

func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

type delegateParams struct {
	Session      string            `json:"session"`
	Agent        string            `json:"agent"`
	Args         []string          `json:"args"`
	Prompt       string            `json:"prompt"`
	Role         string            `json:"role"`
	Isolation    string            `json:"isolation"`
	Name         string            `json:"name"`
	Cwd          string            `json:"cwd"`
	Parent       string            `json:"parent"`
	Grants       []string          `json:"grants"`
	Env          map[string]string `json:"env"`
	Wait         int               `json:"wait"`
	ReadyTimeout int               `json:"ready_timeout"`
}

func (d *Daemon) verbDelegate(cs *connState, params json.RawMessage) (any, *verbError) {
	var p delegateParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	var hidden int
	p.Prompt, hidden = invisible.StripHidden(p.Prompt)
	if strings.TrimSpace(p.Prompt) == "" {
		return nil, invalidParam("prompt", "prompt is required: the task for the child agent")
	}
	role := strings.ToLower(strings.TrimSpace(p.Role))
	if role == "" {
		role = "general"
	}
	if !slices.Contains(delegateRoles, role) {
		return nil, invalidParam("role", "role "+echoName(p.Role)+" is not a role. The roles are "+strings.Join(delegateRoles, ", "))
	}
	isolation := strings.ToLower(strings.TrimSpace(p.Isolation))
	if isolation == "" {
		isolation = delegateShared
	}
	if isolation != delegateShared && isolation != delegateWorktree {
		return nil, invalidParam("isolation", "isolation is shared or worktree")
	}
	if p.Wait < 0 || time.Duration(p.Wait)*time.Millisecond > delegateMaxWait {
		return nil, invalidParam("wait", "wait is a time in milliseconds, at most 600000 (10 minutes). For a longer task, omit it: the answer comes by mail")
	}
	parent, verr := d.resolveAgentParent(cs, "delegate", p.Parent)
	if verr != nil {
		return nil, verr
	}
	if parent == nil && p.Wait == 0 {
		return nil, hintedVerbError(ErrVerbInvalidParams, "delegate needs an agent to send the answer to", &VerbHint{
			Param:  "parent",
			Detail: "Nothing was started. Run delegate from an agent pane, name the agent pane with parent, or pass wait to get the answer in the reply.",
		})
	}
	agent := strings.TrimSpace(p.Agent)
	if agent == "" && parent != nil {
		agent = parent.link.Harness
	}
	if agent == "" {
		return nil, invalidParam("agent", "agent is required here: the parent runs no harness tuios knows. Name the agent, such as claude or codex")
	}
	grants := p.Grants
	if grants == nil && (role == "research" || role == "review") {
		grants = []string{"read"}
	}
	name := strings.TrimSpace(p.Name)
	if name == "" && role != "general" {
		name = role
	}
	session := strings.TrimSpace(p.Session)
	cwd := p.Cwd
	if parent != nil {
		if session == "" {
			session = parent.ref.Session
		}
		if cwd == "" {
			if s := d.sessionHoldingWindow(parent.ref.Window); s != nil {
				if pw, ok := findWindowState(s.GetState(), parent.ref.Window); ok {
					cwd = pw.Cwd
				}
			}
		}
	}

	var wt *delegateWorktreeMade
	if isolation == delegateWorktree {
		if wt, verr = d.delegateMakeWorktree(cs, cwd, role); verr != nil {
			return nil, verr
		}
		session, cwd = wt.session, wt.path
	}

	start := map[string]any{"agent": agent, "cwd": cwd}
	for k, v := range map[string]string{"session": session, "name": name, "parent": p.Parent} {
		if v != "" {
			start[k] = v
		}
	}
	if len(p.Args) > 0 {
		start["args"] = p.Args
	}
	if grants != nil {
		start["grants"] = grants
	}
	if len(p.Env) > 0 {
		start["env"] = p.Env
	}
	if p.ReadyTimeout > 0 {
		start["ready_timeout"] = p.ReadyTimeout
	}
	raw, _ := jsonx.Marshal(start)
	res, verr := d.verbStartAgent(cs, raw)
	if verr != nil {
		wt.undo(d)
		return nil, verr
	}
	out, _ := res.(map[string]any)
	out["type"] = "delegated"
	out["invisible_removed"] = hidden
	out["start_outcome"] = out["outcome"]
	delete(out, "outcome")
	out["role"] = role
	out["isolation"] = isolation
	mode := delegateAsync
	if p.Wait > 0 {
		mode = delegateWait
	}
	out["mode"] = mode
	if wt != nil {
		out["worktree"], out["branch"], out["base"] = wt.path, wt.branch, wt.base
		out["worktree_note"] = "The worktree starts at commit " + shortCommit(wt.base) + " of the parent's checkout. It does not have uncommitted changes."
	}
	if out["ready"] != true {
		out["delegated"] = false
		out["prompt_status"] = PromptNotSent
		out["prompt_note"] = "The child agent was not ready, so the task was not typed. The pane is kept."
		return out, nil
	}
	window, _ := out["window_id"].(string)
	childSess := d.sessionHoldingWindow(window)
	if childSess == nil {
		out["delegated"] = false
		return out, nil
	}

	rec := &Delegation{Role: role, Isolation: isolation, Mode: mode, State: delegateWorking}
	if parent != nil {
		ref := parent.ref
		rec.Parent = &ref
	}
	if origin, ok := d.mailOriginOf(cs, false); ok && origin.kind == "pane" {
		rec.By = origin.window
	}
	if wt != nil {
		rec.Worktree, rec.Base = wt.path, wt.base
	}
	baseline := -1
	if pty, err := d.resolvePTYForTarget(childSess, window); err == nil {
		baseline = contentLines(pty.CaptureContent(true, false))
	}
	pend := &delegatePending{parent: rec.Parent, by: rec.By, baseline: baseline}
	var ch chan delegateOutcome
	if mode == delegateWait {
		ch = make(chan delegateOutcome, 1)
		pend.waiters = []chan delegateOutcome{ch}
	}
	rec.StartedAt = time.Now().UnixMilli()
	_ = childSess.mutateState(func(state *SessionState) error {
		idx, err := findWindowStateIndex(state.Windows, window)
		if err != nil {
			return err
		}
		state.Windows[idx].Delegation = rec
		return nil
	})
	d.delegates.add(window, pend)
	status, note, _ := d.typeFirstPrompt(childSess, window, p.Prompt)
	out["prompt_status"] = status
	if note != "" {
		out["prompt_note"] = note
	}
	if status == PromptNotSent {
		d.delegates.drop(window, pend)
		childSess.updateDelegation(window, func(r *Delegation) { r.State, r.FinishedAt = delegateStopped, time.Now().UnixMilli() })
		out["delegated"] = false
		return out, nil
	}
	out["delegated"] = true
	if mode == delegateAsync {
		out["next"] = delegateNext
		return out, nil
	}

	gone, stop := askCancel(cs)
	defer stop()
	timer := time.NewTimer(time.Duration(p.Wait) * time.Millisecond)
	defer timer.Stop()
	var o delegateOutcome
	select {
	case o = <-ch:
	case <-timer.C:
	case <-gone:
	case <-d.ctx.Done():
	}
	if o.kind == "" {
		d.delegates.unwait(pend, ch)
		select {
		case o = <-ch:
		default:
		}
	}
	switch o.kind {
	case "":
		out["outcome"] = "timeout"
		note := "The wait ended, and the child agent still works. It was not stopped."
		if rec.Parent != nil {
			note += " tuios mails the answer to the parent when the child finishes."
		} else {
			note += " Read its pane with capture-pane."
		}
		out["wait_note"] = note
	case delegateDone, delegateErrored:
		out["outcome"] = o.kind
		out["answer"], out["answer_source"], out["truncated"] = o.answer, o.source, o.truncated
		out["untrusted"] = true
	case "question", "approval":
		out["outcome"] = o.kind
		out["question"] = o.question
		out["untrusted"] = true
	default:
		out["outcome"] = o.kind
	}
	return out, nil
}

type delegateWorktreeMade struct {
	root, path, branch, base, session string
}

func (d *Daemon) delegateMakeWorktree(cs *connState, cwd, role string) (*delegateWorktreeMade, *verbError) {
	if cwd == "" {
		return nil, invalidParam("cwd", "worktree isolation needs the folder of a git checkout: pass cwd")
	}
	root, err := worktree.Root(cwd)
	if err != nil {
		return nil, invalidParam("cwd", echoName(cwd)+" is not in a git checkout, so no worktree can be made from it")
	}
	base, err := worktree.HeadCommit(cwd)
	if err != nil {
		return nil, invalidParam("cwd", echoName(cwd)+" has no commit to start a worktree from")
	}
	var b [3]byte
	_, _ = rand.Read(b[:])
	branch := "delegate/" + role + "-" + hex.EncodeToString(b[:])
	path, verr := worktreePathFor(d.worktreeTemplate(), root, branch)
	if verr != nil {
		return nil, verr
	}
	if _, _, verr := d.childWindowBound(cs, "delegate", path); verr != nil {
		return nil, verr
	}
	sess, _, _, _, verr := d.makeWorktreeSession(cs, root, path, branch, base, "", nil)
	if verr != nil {
		return nil, verr
	}
	return &delegateWorktreeMade{root: root, path: path, branch: branch, base: base, session: sess.Name()}, nil
}

func (wt *delegateWorktreeMade) undo(d *Daemon) {
	if wt == nil {
		return
	}
	_ = d.manager.DeleteSession(wt.session)
	if err := worktree.Remove(wt.root, wt.path, true); err == nil {
		d.worktreeRemoved(wt.session, wt.path)
		_ = worktree.DeleteBranch(wt.root, wt.branch)
	}
}

func (d *Daemon) addDelegation(row map[string]any, w WindowState) {
	rec := w.Delegation
	if rec == nil {
		return
	}
	out := map[string]any{
		"role":       rec.Role,
		"isolation":  rec.Isolation,
		"mode":       rec.Mode,
		"state":      rec.State,
		"started_at": rec.StartedAt,
	}
	if rec.FinishedAt > 0 {
		out["finished_at"] = rec.FinishedAt
	}
	if rec.Worktree != "" {
		out["worktree"], out["base"] = rec.Worktree, rec.Base
	}
	if rec.AskedAt > 0 {
		out["asked_at"] = rec.AskedAt
	}
	if rec.QuestionMail > 0 && rec.Parent != nil {
		out["question_mail"] = rec.QuestionMail
		out["question_read"] = d.agents.messageRead(d.sessionOfWindow(rec.Parent.Window), rec.QuestionMail)
	}
	if rec.ResultMail > 0 {
		out["result_mail"] = rec.ResultMail
	}
	row["role"] = rec.Role
	row["delegation"] = out
}

func (b *agentBus) messageRead(session string, id uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	mb := b.boxes[session]
	if mb == nil {
		return true
	}
	m := mb.find(id)
	return m == nil || m.ReadAt != 0
}
