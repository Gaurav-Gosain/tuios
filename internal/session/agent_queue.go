package session

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

const (
	queueWaiting    = "waiting"
	queueDelivering = "delivering"
	queueStalled    = "stalled"
)

const (
	queueByHuman      = "human"
	queueByShell      = "shell"
	queueByLinkPrefix = "link:"
)

const queueRestDefault = time.Second

const queueQuietDefault = 5 * time.Second

const queuePreviewLen = 80

type queueOrigin struct {
	kind    string
	window  string
	session string
	hosted  bool
	outside bool
	peer    string
	conn    string
}

type queueEntry struct {
	id      string
	text    string
	by      string
	from    string
	origin  queueOrigin
	at      int64
	state   string
	parts   []queuePart
	size    int
	typed   []string
	recheck func() (string, bool)
	done    func()
	unwatch chan struct{}
}

type paneQueue struct {
	session    string
	entries    []*queueEntry
	delivering bool
	bytes      int
	timer      *time.Timer
	draftSince int64
	surface    *time.Timer
	watched    *PTY
}

type typedStamp struct {
	at      int64
	session string
}

type agentQueues struct {
	mu         sync.Mutex
	panes      map[string]*paneQueue
	seq        uint64
	bytes      int
	typed      map[string]typedStamp
	total      atomic.Int64
	stamped    atomic.Int64
	max        atomic.Int64
	rest       time.Duration
	quiet      time.Duration
	beforeType func(window string)
}

// SetQueueMax applies [agents.queue] max. A queue already longer keeps its
// entries; the next queue-prompt is held to the new bound.
func (d *Daemon) SetQueueMax(n int) {
	d.queue.max.Store(int64(config.QueueConfig{Max: n}.MaxEntries()))
}

func (q *agentQueues) maxEntries() int {
	if n := q.max.Load(); n > 0 {
		return int(n)
	}
	return config.DefaultQueueMax
}

func (q *agentQueues) restFor() time.Duration {
	if q.rest > 0 {
		return q.rest
	}
	return queueRestDefault
}

func (q *agentQueues) quietFor() time.Duration {
	if q.quiet > 0 {
		return q.quiet
	}
	return queueQuietDefault
}

func (q *agentQueues) stampLocked(window, session string, at int64) {
	if q.typed == nil {
		q.typed = make(map[string]typedStamp)
	}
	if _, ok := q.typed[window]; !ok {
		q.stamped.Add(1)
	}
	q.typed[window] = typedStamp{at: at, session: session}
}

func (q *agentQueues) unstampLocked(window string) {
	if _, ok := q.typed[window]; ok {
		delete(q.typed, window)
		q.stamped.Add(-1)
	}
}

type paneRest struct {
	ready      bool
	stateAt    int64
	outputRest bool
	lastOutput int64
}

func (d *Daemon) readPaneRest(sess *Session, target WindowState) paneRest {
	r := paneRest{ready: d.agentReady(target, fanReadyStates), stateAt: target.AgentStateAt}
	if r.ready && d.outputShowsTaking(sess, target) {
		r.outputRest = true
		if pty, err := d.resolvePTYForTarget(sess, target.ID); err == nil {
			r.lastOutput = pty.LastOutput()
		}
	}
	return r
}

func (q *agentQueues) restLocked(r paneRest, stamp int64, now time.Time) (ready bool, wait time.Duration, look bool) {
	if !r.ready {
		return false, 0, false
	}
	if stamp == 0 || r.stateAt > stamp {
		rested := now.Sub(time.Unix(0, r.stateAt))
		if rested >= q.restFor() {
			return true, 0, true
		}
		return false, q.restFor() - rested, true
	}
	if !r.outputRest {
		return false, 0, false
	}
	if r.lastOutput <= stamp {
		return false, q.quietFor(), true
	}
	silent := now.Sub(time.Unix(0, r.lastOutput))
	if silent >= q.quietFor() {
		return true, 0, true
	}
	return false, q.quietFor() - silent, true
}

type queuedResult struct {
	ID         string
	Position   int
	Queued     int
	Delivering bool
}

func queueFullError(sess *Session, target WindowState, limit int) *verbError {
	return hintedVerbError(ErrVerbQueueFull, "the queue for window "+shortWindowID(target.ID)+" holds "+strconv.Itoa(limit)+" messages, as many as [agents.queue] max allows", &VerbHint{
		Verb:    "list-queued",
		Command: "tuios queue ls " + paneArgs(sess, target.ID),
		Detail:  "Nothing was queued. Wait for the agent to take what is queued, drop an entry with tuios queue rm, or raise max under [agents.queue] in config.toml.",
	})
}

func queueBytesError(sess *Session, target WindowState) *verbError {
	return hintedVerbError(ErrVerbQueueFull, "the queue for window "+shortWindowID(target.ID)+" holds as many bytes as it may: "+units.Bytes(int64(queuePaneMaxBytes))+" a pane and "+units.Bytes(int64(queueDaemonMaxBytes))+" in all", &VerbHint{
		Verb:    "list-queued",
		Command: "tuios queue ls " + paneArgs(sess, target.ID),
		Detail:  "Nothing was queued. Wait for the agents to take what is queued, or drop an entry with tuios queue rm.",
	})
}

func (d *Daemon) queuePrompt(sess *Session, target WindowState, e *queueEntry) (queuedResult, *verbError) {
	q := &d.queue
	limit := q.maxEntries()
	rest := d.readPaneRest(sess, target)
	now := time.Now()
	q.mu.Lock()
	if q.panes == nil {
		q.panes = make(map[string]*paneQueue)
	}
	pq := q.panes[target.ID]
	if pq == nil {
		pq = &paneQueue{}
		q.panes[target.ID] = pq
	}
	pq.session = sess.Name()
	if e.size == 0 {
		e.size = len(e.text)
	}
	if pq.bytes+e.size > queuePaneMaxBytes || q.bytes+e.size > queueDaemonMaxBytes {
		if len(pq.entries) == 0 {
			delete(q.panes, target.ID)
		}
		q.mu.Unlock()
		return queuedResult{}, queueBytesError(sess, target)
	}
	if len(pq.entries) >= limit {
		if len(pq.entries) == 0 {
			delete(q.panes, target.ID)
		}
		q.mu.Unlock()
		return queuedResult{}, queueFullError(sess, target, limit)
	}
	q.seq++
	e.id = "q" + strconv.FormatUint(q.seq, 10)
	e.at = now.UnixNano()
	e.state = queueWaiting
	pq.entries = append(pq.entries, e)
	pq.bytes += e.size
	q.bytes += e.size
	q.total.Add(1)
	res := queuedResult{ID: e.id, Position: len(pq.entries), Queued: len(pq.entries)}
	stamp := q.typed[target.ID].at
	if res.Position == 1 && !pq.delivering && rest.ready && (stamp == 0 || rest.stateAt > stamp) {
		res.Delivering = true
	}
	if !pq.delivering && pq.entries[0].state == queueWaiting {
		if _, wait, look := q.restLocked(rest, stamp, now); look {
			q.armLocked(d, target.ID, pq, wait)
		}
	}
	q.mu.Unlock()
	LogBasic("Queued %s for window %s (by %s, %d queued)", e.id, shortWindowID(target.ID), e.by, res.Queued)
	d.publishQueued(target.ID)
	return res, nil
}

func (q *agentQueues) armLocked(d *Daemon, window string, pq *paneQueue, wait time.Duration) {
	if pq.timer != nil {
		pq.timer.Reset(wait)
		return
	}
	pq.timer = time.AfterFunc(wait, func() { d.deliverQueued(window) })
}

func (q *agentQueues) removeLocked(window string, pq *paneQueue, drop func(*queueEntry) bool) []*queueEntry {
	var gone []*queueEntry
	kept := pq.entries[:0]
	for _, e := range pq.entries {
		if drop(e) {
			gone = append(gone, e)
			continue
		}
		kept = append(kept, e)
	}
	clear(pq.entries[len(kept):])
	pq.entries = kept
	for _, e := range gone {
		pq.bytes -= e.size
		q.bytes -= e.size
		if e.unwatch != nil {
			close(e.unwatch)
			e.unwatch = nil
		}
	}
	q.total.Add(-int64(len(gone)))
	if len(pq.entries) == 0 && !pq.delivering {
		if pq.timer != nil {
			pq.timer.Stop()
		}
		q.unholdLocked(pq)
		if q.panes[window] == pq {
			delete(q.panes, window)
		}
	}
	return gone
}

func (q *agentQueues) count(window string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	if pq := q.panes[window]; pq != nil {
		return len(pq.entries)
	}
	return 0
}

var errQueueUnchanged = errors.New("queue count unchanged")

func (d *Daemon) publishQueued(windows ...string) {
	for _, window := range windows {
		sess := d.sessionHolding(window)
		if sess == nil {
			continue
		}
		_ = sess.mutateState(func(st *SessionState) error {
			idx, err := findWindowStateIndex(st.Windows, window)
			if err != nil {
				return err
			}
			n := d.queue.count(window)
			held := d.queue.draftHeld(window)
			if st.Windows[idx].AgentQueued == n && st.Windows[idx].AgentDraftHeld == held {
				return errQueueUnchanged
			}
			st.Windows[idx].AgentQueued = n
			st.Windows[idx].AgentDraftHeld = held
			return nil
		})
		if !d.queue.draftHeld(window) {
			d.attention.closeHeldPrompt(sess.Name(), window, DraftHeldSummary)
		}
	}
}

func (d *Daemon) sessionHolding(window string) *Session {
	if window == "" {
		return nil
	}
	for _, sess := range d.manager.AllSessions() {
		if _, ok := findWindowState(sess.GetState(), window); ok {
			return sess
		}
	}
	return nil
}

func (d *Daemon) noteQueueEvent(sessionName string, ev SessionEvent) {
	q := &d.queue
	if q.total.Load() == 0 && q.stamped.Load() == 0 {
		return
	}
	switch ev.Type {
	case EventWindowClosed:
		touched := q.dropWindow(ev.Window)
		if len(touched) > 0 {
			go d.publishQueued(touched...)
		}
	case EventAgentState:
		q.mu.Lock()
		defer q.mu.Unlock()
		if ev.State == AgentStateNone.Name() {
			q.unstampLocked(ev.Window)
		}
		pq := q.panes[ev.Window]
		if pq == nil {
			return
		}
		pq.session = sessionName
		switch ev.State {
		case AgentStateNone.Name():
			pq.delivering = false
			gone := q.removeLocked(ev.Window, pq, func(*queueEntry) bool { return true })
			if len(gone) > 0 {
				LogBasic("Dropped %d queued for window %s: the agent left the pane", len(gone), shortWindowID(ev.Window))
				go d.publishQueued(ev.Window)
			}
			return
		case AgentStateWorking.Name():
			if !pq.delivering && len(pq.entries) > 0 && pq.entries[0].state == queueStalled {
				q.removeLocked(ev.Window, pq, func(e *queueEntry) bool { return e.state == queueStalled })
				go d.publishQueued(ev.Window)
			}
		}
		if len(pq.entries) > 0 && !pq.delivering && pq.entries[0].state == queueWaiting {
			q.armLocked(d, ev.Window, pq, q.restFor())
		}
	}
}

func (q *agentQueues) dropWindow(window string) []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.unstampLocked(window)
	var touched []string
	if pq := q.panes[window]; pq != nil {
		pq.delivering = false
		q.removeLocked(window, pq, func(*queueEntry) bool { return true })
		touched = append(touched, window)
	}
	for id, pq := range q.panes {
		gone := q.removeLocked(id, pq, func(e *queueEntry) bool { return e.origin.kind == "pane" && e.origin.window == window })
		if len(gone) > 0 {
			LogBasic("Dropped %d queued for window %s: window %s that queued them closed", len(gone), shortWindowID(id), shortWindowID(window))
			touched = append(touched, id)
		}
	}
	return touched
}

func (d *Daemon) forgetQueuedSession(name string) {
	q := &d.queue
	if q.total.Load() == 0 && q.stamped.Load() == 0 {
		return
	}
	var touched []string
	q.mu.Lock()
	for id, st := range q.typed {
		if st.session == name {
			q.unstampLocked(id)
		}
	}
	for id, pq := range q.panes {
		if pq.session == name {
			pq.delivering = false
			q.removeLocked(id, pq, func(*queueEntry) bool { return true })
			continue
		}
		if gone := q.removeLocked(id, pq, func(e *queueEntry) bool { return e.origin.kind == "pane" && e.origin.session == name }); len(gone) > 0 {
			touched = append(touched, id)
		}
	}
	q.mu.Unlock()
	if len(touched) > 0 {
		go d.publishQueued(touched...)
	}
}

func (d *Daemon) deliverQueued(window string) {
	if d.ctx.Err() != nil {
		return
	}
	q := &d.queue
	sess := d.sessionHolding(window)
	if sess == nil {
		return
	}
	target, ok := findWindowState(sess.GetState(), window)
	if !ok {
		return
	}
	rest := d.readPaneRest(sess, target)
	var gate draftGate
	if rest.ready {
		gate = d.readDraftGate(sess, target)
	}

	q.mu.Lock()
	pq := q.panes[window]
	if d.agentsOff.Load() || pq == nil || pq.delivering || len(pq.entries) == 0 || pq.entries[0].state != queueWaiting {
		q.mu.Unlock()
		return
	}
	if d.askTurns.busy(window) {
		q.armLocked(d, window, pq, q.restFor())
		q.mu.Unlock()
		return
	}
	if ready, wait, look := q.restLocked(rest, q.typed[window].at, time.Now()); !ready {
		if look {
			q.armLocked(d, window, pq, wait)
		}
		q.mu.Unlock()
		return
	}
	held, changed := q.holdLocked(d, window, pq, gate, time.Now())
	if held {
		q.mu.Unlock()
		if changed {
			d.publishQueued(window)
		}
		return
	}
	e := pq.entries[0]
	e.state = queueDelivering
	pq.delivering = true
	hook := q.beforeType
	q.mu.Unlock()

	if hook != nil {
		hook(window)
	}
	status, note, at, prompt := d.typeQueued(sess, target, e)
	if at == 0 {
		at = time.Now().UnixNano()
	}

	q.mu.Lock()
	pq.delivering = false
	live := q.panes[window] == pq
	var stalled bool
	var unwatch chan struct{}
	switch status {
	case PromptSent:
		if live {
			q.stampLocked(window, sess.Name(), at)
		}
		q.removeLocked(window, pq, func(x *queueEntry) bool { return x == e })
		LogBasic("Typed queued %s into window %s", e.id, shortWindowID(window))
	case PromptStalled:
		if live {
			q.stampLocked(window, sess.Name(), at)
		}
		e.state = queueStalled
		if live {
			unwatch = make(chan struct{})
			e.unwatch = unwatch
		}
		stalled = live
		LogBasic("Queued %s was typed into window %s and not taken: %s", e.id, shortWindowID(window), note)
	case queueRefused:
		q.removeLocked(window, pq, func(x *queueEntry) bool { return x == e })
		LogBasic("Dropped queued %s for window %s: %s", e.id, shortWindowID(window), note)
		if len(pq.entries) > 0 && pq.entries[0].state == queueWaiting {
			q.armLocked(d, window, pq, 0)
		}
	default:
		if e.state == queueDelivering {
			e.state = queueWaiting
		}
	}
	if len(pq.entries) == 0 {
		if pq.timer != nil {
			pq.timer.Stop()
		}
		if q.panes[window] == pq {
			delete(q.panes, window)
		}
	} else if status == PromptSent && pq.entries[0].state == queueWaiting {
		q.armLocked(d, window, pq, q.restFor())
	}
	q.mu.Unlock()

	if stalled {
		summary := d.attention.openQueueStalled(sess.Name(), target)
		go d.watchStalledQueue(sess, window, e, unwatch, prompt, summary)
	}
	if e.done != nil && (status == PromptSent || status == queueRefused || status == PromptStalled) {
		e.done()
	}
	d.publishQueued(window)
}

const queueRefused = "refused"

func (d *Daemon) typeQueued(sess *Session, target WindowState, e *queueEntry) (string, string, int64, *promptGate) {
	if why := d.queueOriginRefusal(e, sess, target); why != "" {
		return queueRefused, why, 0, nil
	}
	if verr := d.refuseBlockedAgent(sess, target.ID); verr != nil {
		return PromptNotSent, verr.Message, 0, nil
	}
	if e.recheck != nil {
		text, ok := e.recheck()
		if !ok {
			return queueRefused, "nothing is left for it to say", 0, nil
		}
		return d.typePromptGated(sess, target.ID, []string{text})
	}
	pastes, err := d.composeQueued(sess, target, e)
	if err != nil {
		d.attention.openQueueFailed(sess.Name(), target, err.Error())
		return queueRefused, "a pasted block could not be saved: " + err.Error(), 0, nil
	}
	return d.typePromptGated(sess, target.ID, pastes)
}

func (d *Daemon) queueOriginRefusal(e *queueEntry, sess *Session, target WindowState) string {
	o := e.origin
	switch o.kind {
	case queueByHuman, queueByShell:
		return ""
	case "link":
		var hosts linkPolicyTable
		if t := d.linkPolicies.Load(); t != nil {
			hosts = *t
		}
		if !config.LinkPolicyFor(hosts, o.peer).Allows(config.LinkAllowWrite) {
			from := "a machine that gave no name"
			if o.peer != "" {
				from = o.peer
			}
			return "the link from " + from + " may no longer write here"
		}
		if hasFileParts(e.parts) && !d.linkAllowsFiles(o.peer) {
			return "the link from " + firstNonEmpty(o.peer, "a machine that gave no name") + " may no longer put files here"
		}
		return ""
	case "pane":
		if o.hosted {
			if !d.manager.grants.defaults().Has(GrantAdmin) {
				return "the pane that queued it runs here for another machine, and the default grants no longer reach any session"
			}
			return ""
		}
		if o.outside {
			if !d.manager.grants.defaults().Has(GrantAdmin) {
				return "the process outside every pane that queued it holds the default grants, and they no longer reach any session"
			}
			return ""
		}
		session := d.sessionOfWindow(o.window)
		if session == "" {
			return "window " + shortWindowID(o.window) + " that queued it is gone"
		}
		g, explicit := d.manager.grants.effective(o.window)
		pa := &paneAuth{window: o.window, session: session, grants: g, explicit: explicit}
		if g.Has(GrantAdmin) {
			return ""
		}
		if why := d.paneWriteReach(pa, sess.Name()); why != "" {
			return "window " + shortWindowID(o.window) + " that queued it holds " + g.String() + " now: " + why
		}
		if why := d.typingRefusal(pa, target, true); why != "" {
			return "window " + shortWindowID(o.window) + " that queued it holds " + g.String() + " now: " + why
		}
		return ""
	}
	return "the entry names no origin"
}

func queueStalledSummary(w WindowState) string {
	name := windowLabelOf(w)
	if name == "" {
		name = "the agent"
	}
	return "your queued message was typed but " + name + " did not take it: look at the pane"
}

func (a *attentionStore) openQueueStalled(sessionName string, w WindowState) string {
	if a == nil {
		return ""
	}
	summary := attentionText(queueStalledSummary(w), attentionMaxSummary)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.upsertLocked(AttentionItem{
		Kind:      AttentionQuestion,
		Session:   sessionName,
		Window:    w.ID,
		Workspace: w.Workspace,
		Harness:   w.AgentHarness,
		Name:      attentionText(windowLabelOf(w), attentionMaxSummary),
		Summary:   summary,
	})
	return summary
}

const (
	queueLateTake = 10 * time.Minute
	queueLatePoll = time.Second
)

func (d *Daemon) watchStalledQueue(sess *Session, window string, e *queueEntry, unwatch <-chan struct{}, gate *promptGate, summary string) {
	if gate == nil {
		return
	}
	gate.late = true
	if gate.pty == nil {
		if !gate.check(sess, nil) {
			return
		}
	} else {
		taken, gone, stopped := d.watchPromptGate(sess, gate.pty, gate, queueLateTake, queueLatePoll, unwatch)
		if stopped {
			LogBasic("Stopped watching window %s for its stalled queued %s: it left the queue", shortWindowID(window), e.id)
			return
		}
		if !taken || gone {
			return
		}
	}
	q := &d.queue
	q.mu.Lock()
	settled := false
	if pq := q.panes[window]; pq != nil && !pq.delivering && len(pq.entries) > 0 && pq.entries[0] == e && e.state == queueStalled {
		q.removeLocked(window, pq, func(x *queueEntry) bool { return x == e })
		settled = true
		if len(pq.entries) > 0 && pq.entries[0].state == queueWaiting {
			q.armLocked(d, window, pq, q.restFor())
		}
	}
	q.mu.Unlock()
	if !settled {
		return
	}
	LogBasic("Window %s took its stalled queued %s late, by %s", shortWindowID(window), e.id, gate.taken)
	d.attention.closeHeldPrompt(sess.Name(), window, summary)
	d.publishQueued(window)
}

func queuePreview(text string) string {
	line, _, cut := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.TrimSpace(line)
	var b strings.Builder
	n := 0
	for _, r := range line {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) || r == utf8.RuneError {
			continue
		}
		if n == queuePreviewLen {
			cut = true
			break
		}
		b.WriteRune(r)
		n++
	}
	if cut {
		return b.String() + "..."
	}
	return b.String()
}

func (d *Daemon) renameQueuedSession(old, newName string) {
	q := &d.queue
	if old == newName || (q.total.Load() == 0 && q.stamped.Load() == 0) {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for id, st := range q.typed {
		if st.session == old {
			st.session = newName
			q.typed[id] = st
		}
	}
	for _, pq := range q.panes {
		if pq.session == old {
			pq.session = newName
		}
		for _, e := range pq.entries {
			if e.origin.session == old {
				e.origin.session = newName
			}
		}
	}
}
