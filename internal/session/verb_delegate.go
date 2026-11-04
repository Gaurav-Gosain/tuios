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

	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// delegate: hand a task to a child agent and get the answer back by mail.
//
// It is a composition of what exists, and adds no channel of its own:
//
//   - start-agent starts the child, which links it to its parent
//     (agent_parent.go) and holds it to the parent's grants and permission
//     mode (child_clamp.go). delegate checks nothing of that again.
//   - The task is typed as the child's first prompt, the way start-agent
//     types one.
//   - The outcome rides the session's state events: the child's next state
//     after the prompt says it finished, or asks. Nothing polls, and nothing
//     runs while no delegated child is waited on: the event sink returns at
//     once while delegates.count is zero.
//   - The answer goes to the parent as mail, from the child, with a subject
//     tuios writes. Mail is read back marked untrusted, so the child's text
//     reaches the parent as data. The existing mail wake (mail_wake.go) then
//     types its one line, which holds no child text, into the parent once
//     it is at rest. delegate never types into the parent itself.
//
// With wait the call itself returns the outcome instead: done, a question or
// an approval, or the timeout. A timeout does not stop the child, and the
// answer is then mailed as in the async case when there is a parent.
//
// A question from the child (needs_input that is not an approval) is mailed
// to the parent, with the prompt as peek-prompt reads it. An approval is left
// in the Inbox for the person. The record on the child (WindowState.Delegation)
// says which child asks and since when, and list-agents says whether the
// parent read the question, for the approval roll-up to build on.
//
// The record is daemon-owned and saved like the parent link, and it is
// carried across a live upgrade. A child that finishes while the daemon is
// handed over keeps running under the keeper, and the new image looks at
// every unfinished record once it serves (resumeDelegations), so the answer
// is still mailed. A daemon restart ends the child's process with the old
// daemon: a restored record reads stopped, and nothing is mailed, since the
// mailbox does not outlive the daemon either.

// Delegation roles. A role names the job in the child's window name and in
// list-agents. research and review default the grants to read. No role adds
// text to the prompt.
var delegateRoles = []string{"implementation", "research", "review", "design", "test", "general"}

// Delegation isolations, modes and states.
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
	// delegateMaxWait bounds wait. A longer task is delegated without one:
	// the answer comes by mail.
	delegateMaxWait = 10 * time.Minute
	// delegateAnswerMax bounds the answer delegate reads from the child. A
	// wait returns all of it. The mail carries what fits in one message, and
	// the whole answer in a stashed file when it does not fit.
	delegateAnswerMax = 16 << 10
	// delegateCaptureLines bounds the lines read from the child's screen
	// when its harness has no transcript.
	delegateCaptureLines = 200
	// delegateTranscriptEntries is how many of the newest transcript
	// entries are read for the answer.
	delegateTranscriptEntries = 60
)

// delegateNext is what the parent of an async delegate is told to do.
const delegateNext = "End your turn now. tuios wakes you when the child agent finishes. Do not wait or poll."

// delegateOutcome is how a delegated task came to an end, or to a stop, as a
// call that waits returns it.
type delegateOutcome struct {
	kind      string // done, errored, question, approval or closed
	answer    string
	source    string // transcript or screen
	truncated bool
	question  *delegateQuestion
}

// delegateQuestion is a prompt the child stopped on, as peek-prompt reads it.
type delegateQuestion struct {
	Kind    string   `json:"kind"`
	Message string   `json:"message,omitempty"`
	Lines   []string `json:"lines"`
}

// delegatePending is one delegated child the daemon still waits on.
type delegatePending struct {
	// look serializes the looks at the child, so two events in a row
	// settle it once.
	look sync.Mutex
	// parent is where the result goes, nil for none. It is copied from the
	// record, so a closed child still knows it.
	parent *PaneRef
	by     string
	// baseline is the child's content lines before the prompt, -1 when not
	// known (after a live upgrade).
	baseline int
	// waiters are the calls that wait for the outcome.
	waiters []chan delegateOutcome
}

// delegates holds the delegated children the daemon waits on. The zero value
// is ready.
type delegates struct {
	mu    sync.Mutex
	panes map[string]*delegatePending
	// count is len(panes), so the event sink returns at once when no
	// delegated child is waited on.
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

// deliver hands o to the calls that wait on window, and reports whether any
// did. The waits end with it.
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

// unwait takes ch off p's waiters, for a wait that ended on its own.
func (t *delegates) unwait(p *delegatePending, ch chan delegateOutcome) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p.waiters = slices.DeleteFunc(p.waiters, func(c chan delegateOutcome) bool { return c == ch })
}

// noteDelegateEvent folds one session event into the delegates: a state
// change or a close of a delegated child gets a look. It runs on the session's
// event sink with the state lock held, so the look runs off it.
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

// delegateLook reads a delegated child's state and acts on it: mails or
// returns its answer once it finished, mails or returns its question once it
// asks one, and notes that it went back to work.
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
			// An approval is the person's, in the Inbox, and the record
			// marks only questions.
			if kind == "question" {
				sess.updateDelegation(window, func(r *Delegation) { r.State, r.AskedAt = delegateAsking, now })
			}
			return
		}
		// The question goes to the parent first, unless the person turned
		// that off. When the parent cannot be told, the Inbox item it holds
		// for the parent is the person's at once. See attention_children.go.
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
	case w.AgentState == AgentStateIdle || w.AgentState == AgentStateDone || w.AgentState == AgentStateErrored:
		if w.AgentReason == AgentReasonSilent || w.AgentStateAt <= rec.StartedAt*int64(time.Millisecond) {
			// The rest the child was in when its prompt was typed.
			return
		}
		state := delegateDone
		if w.AgentState == AgentStateErrored {
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
		// The question was answered, and the child works again.
		sess.updateDelegation(window, func(r *Delegation) { r.State, r.AskedAt, r.QuestionMail = delegateWorking, 0, 0 })
	}
}

// delegateGone settles a child whose window closed before it finished. The
// record went with the window.
func (d *Daemon) delegateGone(window string, p *delegatePending) {
	d.delegates.drop(window, p)
	if d.delegates.deliver(p, delegateOutcome{kind: delegateClosed}) || p.parent == nil {
		return
	}
	d.delegateMail(&Delegation{Parent: p.parent, By: p.by}, nil, WindowState{ID: window},
		"Delegated task closed: window "+shortWindowID(window), "The child window closed before the task finished.", "", delegateClosed)
}

// delegateFinished reports whether a record's state is final.
func delegateFinished(state string) bool {
	switch state {
	case delegateDone, delegateErrored, delegateClosed, delegateStopped:
		return true
	}
	return false
}

// updateDelegation changes window's record. The record is replaced, never
// written in place, since older copies of the state share it.
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

// restoredDelegation is a saved record as a restart brings it back. The
// child's process ended with the daemon that ran it, so an unfinished task
// reads stopped.
func restoredDelegation(saved *Delegation) *Delegation {
	rec := *saved
	if !delegateFinished(rec.State) {
		rec.State = delegateStopped
		rec.FinishedAt = time.Now().UnixMilli()
		rec.AskedAt, rec.QuestionMail = 0, 0
	}
	return &rec
}

// resumeDelegations waits again on every delegated child a live upgrade
// handed over, and looks at each once, for a child that finished or asked
// while the daemon was handed over.
func (d *Daemon) resumeDelegations() {
	var windows []string
	for _, sess := range d.manager.AllSessions() {
		for _, w := range sess.GetState().Windows {
			rec := w.Delegation
			if rec == nil || delegateFinished(rec.State) {
				continue
			}
			// A child still asking was told already, so the look below
			// mails nothing again for the same question. Its next state is
			// what to act on.
			p := &delegatePending{parent: rec.Parent, by: rec.By, baseline: -1}
			d.delegates.add(w.ID, p)
			windows = append(windows, w.ID)
		}
	}
	for _, id := range windows {
		go d.delegateLook(id, false)
	}
}

// delegateQuestionOf reads the prompt the child stopped on.
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

// delegateAnswer reads the child's last answer: from its transcript when its
// harness has one this daemon reads, else what its screen showed after the
// prompt (the reply capture ask-agent uses). It is cut to delegateAnswerMax.
func (d *Daemon) delegateAnswer(sess *Session, w WindowState, baseline int) (answer, source string, truncated bool) {
	if text, ok := d.delegateTranscriptAnswer(sess, w.ID); ok {
		answer, source = text, "transcript"
	} else if pty, err := d.resolvePTYForTarget(sess, w.ID); err == nil {
		content := pty.CaptureContent(true, false)
		if baseline < 0 {
			// After a live upgrade the line count before the prompt is not
			// known: the newest lines are the answer.
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

// delegateTranscriptAnswer is the assistant's text after the last prompt in
// the child's transcript. It reads the newest entries once, through the same
// Clean and secret masking agent-transcript applies, and keeps nothing.
func (d *Daemon) delegateTranscriptAnswer(sess *Session, window string) (string, bool) {
	reg := d.agentMatcher.registry
	path, harnessID, format, session, leaf, joined := sess.transcriptViewOf(window)
	if !joined || reg == nil {
		return "", false
	}
	if tr := reg.TranscriptFor(harnessID); tr == nil || tr.Reader != format || !transcriptview.Known(format) {
		return "", false
	}
	page, err := transcriptview.Read(path, transcriptview.Options{
		Limit:      delegateTranscriptEntries,
		Clean:      transcriptText,
		CleanLines: maskSecretLines,
		Format:     format,
		Session:    session,
		Leaf:       leaf,
	})
	if err != nil {
		return "", false
	}
	var parts []string
	for _, e := range slices.Backward(page.Entries) {
		if e.Role == transcriptview.RoleUser && e.Kind == transcriptview.KindText {
			break
		}
		if e.Role == transcriptview.RoleAssistant && e.Kind == transcriptview.KindText && strings.TrimSpace(e.Text) != "" {
			parts = append(parts, e.Text)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	slices.Reverse(parts)
	return strings.Join(parts, "\n\n"), true
}

// delegateMail mails the parent of rec, from the child, and lets the mail
// wake tell the parent. child is the child's window, sess its session (nil
// for a child that closed). A body longer than one message is cut, and the
// whole body is put in the parent session's stash and attached, named
// stashName; "" sends the cut body alone. finished is the task's final state
// for an answer, which the parent's wake line counts, and "" for a question.
// It returns the message id, 0 when the parent is gone.
func (d *Daemon) delegateMail(rec *Delegation, sess *Session, child WindowState, subject, body, stashName, finished string) uint64 {
	parentSess := d.sessionHoldingWindow(rec.Parent.Window)
	if parentSess == nil {
		return 0
	}
	pw, ok := findWindowState(parentSess.GetState(), rec.Parent.Window)
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
	// The cut leaves room for the line that says where the rest is.
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
	if rec.By != "" {
		origin = queueOrigin{kind: "pane", window: rec.By, session: d.sessionOfWindow(rec.By)}
	}
	d.noteMailSentAs(parentSess.Name(), pw.ID, stored.ID, origin, finished)
	return stored.ID
}

// cutUTF8 cuts s to at most n bytes, at a rune boundary.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// delegateParams are delegate's parameters.
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

	// Worktree isolation: a worktree at the commit of the parent's
	// checkout, in a session of its own.
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
	raw, _ := json.Marshal(start)
	res, verr := d.verbStartAgent(cs, raw)
	if verr != nil {
		wt.undo(d)
		return nil, verr
	}
	out, _ := res.(map[string]any)
	out["type"] = "delegated"
	// outcome is the wait's. How the start went is start_outcome.
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
	// The record and the wait are in place before the prompt is typed, so
	// a child that answers at once is seen.
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
		// An outcome that came while the wait ended is still this call's.
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

// delegateWorktreeMade is the worktree and session delegate made for a child.
type delegateWorktreeMade struct {
	root, path, branch, base, session string
}

// delegateMakeWorktree makes a worktree at the commit checked out in cwd, on a
// new branch, in a session of its own. It is checked against the caller's
// folder first, as start-agent will check the agent's folder, so a refused
// call leaves nothing behind.
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

// undo removes a worktree whose agent did not start: the session, the
// worktree and its branch. Nothing was made in it.
func (wt *delegateWorktreeMade) undo(d *Daemon) {
	if wt == nil {
		return
	}
	_ = d.manager.DeleteSession(wt.session)
	if err := worktree.Remove(wt.root, wt.path, true); err == nil {
		_ = worktree.DeleteBranch(wt.root, wt.branch)
	}
}

// addDelegation puts the delegation record on a list-agents row: the role,
// and the task's state with what a reader of the approval roll-up needs.
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

// messageRead reports whether message id of session was read. A message the
// ring no longer holds reads as read: nobody can read it now.
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
