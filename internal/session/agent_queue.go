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
)

// The delivery queue: messages for the agent in a pane, typed as a prompt the
// moment the agent comes to rest.
//
// A message is queued with queue-prompt (verb_queue.go), or by another part of
// the daemon through queuePrompt, which is how send-review hands over its
// notes. Nothing polls. The queue acts on the agent-state events the session
// sink already produces (noteQueueEvent, called beside the Inbox's own
// noteSessionEvent), and a daemon with nothing queued and nothing typed costs
// two atomic loads per event. A timer exists only for a pane that has
// something queued.
//
// Delivery, one entry at a time per pane:
//
//   - The pane must be ready the way fan judges a fresh agent ready
//     (agentReady with fanReadyStates): idle or done, and unknown only for a
//     harness whose rules can never show idle. It must have been in that state
//     for queueRest, and, after an earlier delivery, have reached it after that
//     delivery was typed, so the next entry waits for the next rest. When the
//     last entry was typed is kept per pane (typedStamp), not per queue, so an
//     entry queued after the queue emptied still waits for that rest.
//   - A pane whose harness cannot show working (the prompt gate counts its
//     output as taking a prompt) may never change state after taking one: an
//     unknown pane stays unknown. For it a rest is also output printed after
//     the last entry was typed, followed by queueQuiet of silence. Only such a
//     pane with something queued is looked at on a timer.
//   - The entry's origin is checked again (queueOriginRefusal): a pane's
//     grants as they are now, a link's policy as it is now. A refusal drops
//     the entry and logs it; nothing is typed.
//   - refuseBlockedAgent runs right before typing, for every entry, so queued
//     text never answers a prompt.
//   - The text is typed by typeFirstPrompt, the path fan uses: paste, Enter,
//     then the prompt gate (prompt_gate.go) waits for evidence the agent took
//     it.
//   - Taken: the entry is removed. Stalled: the entry is kept, marked stalled,
//     and never typed again, and the Inbox gets a question on the pane's
//     blocking key. A stalled entry holds the entries behind it until the pane
//     next shows working (the agent took it late, or the person dealt with the
//     pane), which drops it, or until someone cancels it. Either way the next
//     entry still waits for a rest reached after the stalled one was typed.
//
// A queue is memory only. It is dropped when the daemon stops, when its pane
// closes, when the agent leaves the pane (state none), and when its session
// ends. An entry a pane queued is dropped when that pane closes.

// Entry states, as list-queued reports them.
const (
	queueWaiting    = "waiting"
	queueDelivering = "delivering"
	queueStalled    = "stalled"
)

// Origins of an entry: who queued it, which decides what is checked again
// when it is typed.
const (
	// queueByHuman is the person, proved by a live human_nonce. Nothing more
	// is checked at delivery: the person consented, as when they type.
	queueByHuman = "human"
	// queueByShell is a caller outside every pane of this daemon, such as the
	// person's own shell or a script, without the nonce. It is held to
	// nothing new, now or at delivery.
	queueByShell = "shell"
	// queueByLinkPrefix starts the by of an entry a linked machine queued.
	queueByLinkPrefix = "link:"
)

// queueRestDefault is how long a pane must have been at rest before a queued
// entry is typed into it. It is what keeps a message out of the moment an
// agent flickers to idle between two tool calls.
const queueRestDefault = time.Second

// queueQuietDefault is how long a pane whose harness cannot show working must
// have been silent, after printing something since the last entry was typed,
// before the next entry is typed into it. It is longer than the rest, since
// silence is weaker evidence than a state: an agent may pause while it waits
// on its model.
const queueQuietDefault = 5 * time.Second

// queuePreviewLen bounds the preview list-queued shows, in characters.
const queuePreviewLen = 80

// queueOrigin is who queued an entry.
type queueOrigin struct {
	// kind is queueByHuman, queueByShell, "pane" or "link".
	kind string
	// window and session are the pane that queued it, for kind pane.
	window  string
	session string
	// hosted marks a pane this machine runs for another machine. It holds
	// this machine's default grants and no session here.
	hosted bool
	// peer is the linked machine, for kind link, as its link-peer handshake
	// named it; empty for a link that gave no name.
	peer string
	// conn is the client id of the link connection that queued it, for kind
	// link. Two machines that gave no name share the empty peer, so only
	// the connection tells their entries apart.
	conn string
}

// queueEntry is one queued message.
type queueEntry struct {
	id   string
	text string
	// by is who queued it, as list-queued shows it: human, shell, the
	// queueing pane's window id, or link:HOST.
	by string
	// from is the label the caller gave for the sender, empty for none.
	from   string
	origin queueOrigin
	at     int64
	state  string
}

// paneQueue is one pane's queue.
type paneQueue struct {
	// session is the pane's session as the last event named it.
	session string
	entries []*queueEntry
	// delivering is set while an entry is being typed.
	delivering bool
	// timer is the pending look at the pane, nil when none is armed.
	timer *time.Timer
}

// typedStamp is when the last queued entry was typed into a pane, in Unix
// nanoseconds, so the next one waits for a rest reached after it. It outlives
// the pane's queue: a queue that empties is deleted, and an entry queued a
// moment later must still wait. It goes when the pane closes, when the agent
// leaves it, and when its session ends.
type typedStamp struct {
	at      int64
	session string
}

// agentQueues holds every pane's queue. The zero value is ready.
//
// Lock order: a session's state lock, then mu. The session sink calls
// noteQueueEvent with the state lock held, and publishQueued reads the count
// inside mutateState. Nothing calls into a session while holding mu.
type agentQueues struct {
	mu    sync.Mutex
	panes map[string]*paneQueue
	seq   uint64
	// typed holds each pane's typedStamp, and stamped its length.
	typed map[string]typedStamp
	// total counts entries across every pane, and stamped the panes with a
	// typedStamp, so the event sink returns at once on a daemon where nothing
	// is queued and nothing was typed.
	total   atomic.Int64
	stamped atomic.Int64
	// max is [agents.queue] max; zero reads as the default.
	max atomic.Int64
	// rest replaces queueRestDefault, and quiet queueQuietDefault, when set.
	// Only tests set them.
	rest  time.Duration
	quiet time.Duration
	// beforeType, when set, runs right before an entry is typed, after the
	// look found the pane ready. Only tests set it.
	beforeType func(window string)
}

// SetQueueMax applies [agents.queue] max. A queue already longer keeps its
// entries; the next queue-prompt is held to the new bound.
func (d *Daemon) SetQueueMax(n int) {
	d.queue.max.Store(int64(config.QueueConfig{Max: n}.MaxEntries()))
}

// maxEntries is the bound per pane.
func (q *agentQueues) maxEntries() int {
	if n := q.max.Load(); n > 0 {
		return int(n)
	}
	return config.DefaultQueueMax
}

// restFor is how long a pane rests before it is typed into.
func (q *agentQueues) restFor() time.Duration {
	if q.rest > 0 {
		return q.rest
	}
	return queueRestDefault
}

// quietFor is how long a pane that cannot show working must be silent.
func (q *agentQueues) quietFor() time.Duration {
	if q.quiet > 0 {
		return q.quiet
	}
	return queueQuietDefault
}

// stampLocked records that an entry was typed into window at at. It holds mu.
func (q *agentQueues) stampLocked(window, session string, at int64) {
	if q.typed == nil {
		q.typed = make(map[string]typedStamp)
	}
	if _, ok := q.typed[window]; !ok {
		q.stamped.Add(1)
	}
	q.typed[window] = typedStamp{at: at, session: session}
}

// unstampLocked forgets when an entry was last typed into window. It holds mu.
func (q *agentQueues) unstampLocked(window string) {
	if _, ok := q.typed[window]; ok {
		delete(q.typed, window)
		q.stamped.Add(-1)
	}
}

// paneRest is what a look at a pane reads before it takes mu, since reading
// it may take the session's locks.
type paneRest struct {
	// ready is agentReady with fanReadyStates.
	ready bool
	// stateAt is when the pane's agent state last changed.
	stateAt int64
	// outputRest says output then silence counts as a rest: the pane's
	// harness cannot show working. lastOutput is the pane's output clock.
	outputRest bool
	lastOutput int64
}

// readPaneRest reads what restLocked decides on.
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

// restLocked decides whether a pane may take its next entry now. When it may
// not, look says whether a look should be armed, after wait; false leaves it
// to the pane's next state change. stamp is when the last entry was typed, 0
// for none. It holds mu.
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
	// The pane has not changed state since the last entry was typed.
	if !r.outputRest {
		return false, 0, false
	}
	if r.lastOutput <= stamp {
		// Nothing printed since. No event says when it does, so look again
		// after the quiet window while something waits.
		return false, q.quietFor(), true
	}
	silent := now.Sub(time.Unix(0, r.lastOutput))
	if silent >= q.quietFor() {
		return true, 0, true
	}
	return false, q.quietFor() - silent, true
}

// queuedResult is what queuePrompt reports about an entry it queued.
type queuedResult struct {
	ID       string
	Position int
	Queued   int
	// Delivering is true when the entry is next and the agent is at rest
	// now, so it is typed within about a second rather than after a turn.
	Delivering bool
}

// queueFullError is the refusal for a pane whose queue is at its bound.
func queueFullError(target WindowState, limit int) *verbError {
	return hintedVerbError(ErrVerbQueueFull, "the queue for window "+shortWindowID(target.ID)+" holds "+strconv.Itoa(limit)+" messages, as many as [agents.queue] max allows", &VerbHint{
		Verb:    "list-queued",
		Command: "tuios queue ls -w " + shortWindowID(target.ID),
		Detail:  "Nothing was queued. Wait for the agent to take what is queued, drop an entry with tuios queue rm, or raise max under [agents.queue] in config.toml.",
	})
}

// queuePrompt puts e at the back of its pane's queue and, when the agent is at
// rest, arms the look that types it. sess and target are the pane's session
// and window as the caller resolved them. It is the entry point for every
// part of the daemon that queues a message: queue-prompt, and send-review
// with the text it composed. The caller has checked the caller's authority
// (newQueueEntry); this checks only the bound.
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
	pq.session = sess.Name
	if len(pq.entries) >= limit {
		if len(pq.entries) == 0 {
			delete(q.panes, target.ID)
		}
		q.mu.Unlock()
		return queuedResult{}, queueFullError(target, limit)
	}
	q.seq++
	e.id = "q" + strconv.FormatUint(q.seq, 10)
	e.at = now.UnixNano()
	e.state = queueWaiting
	pq.entries = append(pq.entries, e)
	q.total.Add(1)
	res := queuedResult{ID: e.id, Position: len(pq.entries), Queued: len(pq.entries)}
	stamp := q.typed[target.ID].at
	if res.Position == 1 && !pq.delivering && rest.ready && (stamp == 0 || rest.stateAt > stamp) {
		// At a rest reached since the last entry was typed, so it goes
		// once the rest is a second old.
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

// armLocked schedules a look at the pane after wait, replacing one already
// armed. It holds mu.
func (q *agentQueues) armLocked(d *Daemon, window string, pq *paneQueue, wait time.Duration) {
	if pq.timer != nil {
		pq.timer.Stop()
	}
	pq.timer = time.AfterFunc(wait, func() { d.deliverQueued(window) })
}

// removeLocked takes entries matching drop out of pq and returns them. It
// holds mu, and deletes the pane's queue when it empties.
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
	q.total.Add(-int64(len(gone)))
	if len(pq.entries) == 0 && !pq.delivering {
		if pq.timer != nil {
			pq.timer.Stop()
		}
		if q.panes[window] == pq {
			delete(q.panes, window)
		}
	}
	return gone
}

// count is how many entries the pane's queue holds.
func (q *agentQueues) count(window string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	if pq := q.panes[window]; pq != nil {
		return len(pq.entries)
	}
	return 0
}

// errQueueUnchanged tells mutateState a count that did not change, so nothing
// is pushed.
var errQueueUnchanged = errors.New("queue count unchanged")

// publishQueued writes the pane's queue length to its window state, which
// every attached client and get-agent-state read. The count is read inside the
// state lock, so two updates racing each other still leave the latest count.
// It must not be called with a session's state lock held.
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
			if st.Windows[idx].AgentQueued == n {
				return errQueueUnchanged
			}
			st.Windows[idx].AgentQueued = n
			return nil
		})
	}
}

// sessionHolding is the session a window is in now, nil for none.
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

// noteQueueEvent folds one session event into the queues. It runs on the
// session's event sink with the session's state lock held, so it reads
// nothing but the event, and whatever it has to push runs on its own
// goroutine.
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
			// The agent left the pane, and the conversation the messages
			// were for went with it.
			gone := q.removeLocked(ev.Window, pq, func(*queueEntry) bool { return true })
			if len(gone) > 0 {
				LogBasic("Dropped %d queued for window %s: the agent left the pane", len(gone), shortWindowID(ev.Window))
				go d.publishQueued(ev.Window)
			}
			return
		case AgentStateWorking.Name():
			// A stalled entry is settled by the pane working: the agent took
			// it late, or the person dealt with the pane. It is never typed
			// again either way.
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

// dropWindow drops the queue of a pane that closed, and every entry that pane
// queued elsewhere. It returns the panes whose count changed.
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

// forgetQueuedSession drops the queues of a session that ended, and every
// entry a pane of it queued. The session's windows are gone from every
// lookup by now, so the session name recorded on each queue and origin is
// what names them.
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

// deliverQueued is the armed look at a pane: it types the pane's next entry
// when the pane is ready for it, and otherwise leaves it for the next event.
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

	q.mu.Lock()
	pq := q.panes[window]
	if pq == nil || pq.delivering || len(pq.entries) == 0 || pq.entries[0].state != queueWaiting {
		q.mu.Unlock()
		return
	}
	if ready, wait, look := q.restLocked(rest, q.typed[window].at, time.Now()); !ready {
		// Not at rest, or not at a rest reached since the last entry was
		// typed. Either a look is due later, or the pane's next state
		// change arms one.
		if look {
			q.armLocked(d, window, pq, wait)
		}
		q.mu.Unlock()
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
	status, note, at := d.typeQueued(sess, target, e)
	if at == 0 {
		at = time.Now().UnixNano()
	}

	q.mu.Lock()
	pq.delivering = false
	// A pane that closed, or whose session ended or agent left, while the
	// gate waited has dropped its queue, and it gets no stamp.
	live := q.panes[window] == pq
	var stalled bool
	switch status {
	case PromptSent:
		// Stamped with when the Enter went out, not now: a fast turn can
		// reach its rest while the gate waits, and that rest counts.
		if live {
			q.stampLocked(window, sess.Name, at)
		}
		q.removeLocked(window, pq, func(x *queueEntry) bool { return x == e })
		LogBasic("Typed queued %s into window %s", e.id, shortWindowID(window))
	case PromptStalled:
		if live {
			q.stampLocked(window, sess.Name, at)
		}
		e.state = queueStalled
		// A pane that closed while the gate waited has dropped its queue,
		// and there is no pane left to look at.
		stalled = live
		LogBasic("Queued %s was typed into window %s and not taken: %s", e.id, shortWindowID(window), note)
	case queueRefused:
		q.removeLocked(window, pq, func(x *queueEntry) bool { return x == e })
		LogBasic("Dropped queued %s for window %s: %s", e.id, shortWindowID(window), note)
		// The next entry may come from someone else, and nothing was typed,
		// so it is looked at now rather than at the next rest.
		if len(pq.entries) > 0 && pq.entries[0].state == queueWaiting {
			q.armLocked(d, window, pq, 0)
		}
	default:
		// Not typed: the pane came to a prompt or could not be written.
		// It waits for the pane's next state change.
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
		// A fast turn can come and go while the gate waited, so its rest
		// armed nothing. Look again; the look types only after a rest
		// reached since this entry was typed.
		q.armLocked(d, window, pq, q.restFor())
	}
	q.mu.Unlock()

	if stalled {
		d.attention.openQueueStalled(sess.Name, target)
	}
	d.publishQueued(window)
}

// queueRefused is typeQueued's status for an entry whose origin may no longer
// type into the pane. It is dropped, and nothing was typed.
const queueRefused = "refused"

// typeQueued checks e once more and types it: the origin's authority as it is
// now, then that the pane is not on a prompt, then the prompt path fan uses.
// It returns the prompt status, or queueRefused, a note, and when the Enter
// went out (0 when nothing was typed).
func (d *Daemon) typeQueued(sess *Session, target WindowState, e *queueEntry) (string, string, int64) {
	if why := d.queueOriginRefusal(e, sess, target); why != "" {
		return queueRefused, why, 0
	}
	if verr := d.refuseBlockedAgent(sess, target.ID); verr != nil {
		return PromptNotSent, verr.Message, 0
	}
	return d.typeFirstPrompt(sess, target.ID, e.text)
}

// queueOriginRefusal says why the entry's origin may no longer type into
// target, or "" when it may. The person and a caller outside every pane are
// held to nothing new. A pane is held to its grants as they are now: the
// write reach of its session and fan group, and a target that holds nothing it
// does not. A pane that closed has dropped its entries already, and one that
// cannot be found is refused. A link is held to its machine's policy as it is
// now.
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
		return ""
	case "pane":
		if o.hosted {
			if !d.manager.grants.defaults().Has(GrantAdmin) {
				return "the pane that queued it runs here for another machine, and the default grants no longer reach any session"
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
		if why := d.paneWriteReach(pa, sess.Name); why != "" {
			return "window " + shortWindowID(o.window) + " that queued it holds " + g.String() + " now: " + why
		}
		if why := d.typingRefusal(pa, target, true); why != "" {
			return "window " + shortWindowID(o.window) + " that queued it holds " + g.String() + " now: " + why
		}
		return ""
	}
	return "the entry names no origin"
}

// queueStalledSummary is the Inbox question for an entry that was typed and
// not taken.
func queueStalledSummary(w WindowState) string {
	name := windowLabelOf(w)
	if name == "" {
		name = "the agent"
	}
	return "your queued message was typed but " + name + " did not take it: look at the pane"
}

// openQueueStalled opens a question on the pane's blocking key for an entry
// that was typed and not taken. Like the held-screen question, the pane's
// next state change closes it, and a needs_input the pane reaches replaces
// it.
func (a *attentionStore) openQueueStalled(sessionName string, w WindowState) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.upsertLocked(AttentionItem{
		Kind:      AttentionQuestion,
		Session:   sessionName,
		Window:    w.ID,
		Workspace: w.Workspace,
		Harness:   w.AgentHarness,
		Name:      attentionText(windowLabelOf(w), attentionMaxSummary),
		Summary:   attentionText(queueStalledSummary(w), attentionMaxSummary),
	})
}

// queuePreview is the first line of a message, cut to queuePreviewLen
// characters with its control characters left out.
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
