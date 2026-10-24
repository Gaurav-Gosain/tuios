package session

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Waking an agent at rest that has mail.
//
// A hook tells an agent about its mail during a turn (mail_notice.go). An agent
// at rest runs no hooks, and an agent whose harness has none never hears at
// all. So when mail for a pane stays unannounced and unread for
// mailWakeGrace, and the pane's agent is at rest, the daemon types one line
// into it: the count of unread messages and the command that reads them.
//
// The line goes through the delivery queue (agent_queue.go), the same path
// queue-prompt takes, so everything the queue promises holds: it is typed
// only once the agent has rested for a second, never into a pane on
// needs_input, never twice, and with the sender's authority checked again
// right before typing. The entry's origin is the caller that sent the mail,
// as the connection proved it, not the from the message claims. A pane that
// may not type into the recipient cannot wake it either; its mail waits for
// the recipient's next hook or read. A connection restricted to read_only,
// and a pane on another machine, which queue-prompt refuses, wake nothing.
//
// The line is written by tuios alone, and holds no text a sender chose: not
// the subject, not the body, not even the sender's name. Text typed into an
// agent is read as its user's prompt, and mail must stay data the recipient
// reads on purpose. The hook notice may name senders, since it goes into the
// agent's context, not its user's turn.
//
// The line is written again right before it is typed: a wake whose mail was
// read while it waited is dropped, and one that waited while more mail came
// says the count at that moment.
//
// Each message is announced once, by a hook or by a wake, whichever comes
// first: both mark the bus's notice mark, and a wake claims it only if no hook
// moved it since the wake looked (claimNotice). A burst of mail gives one wake,
// because the grace timer is armed once and a wake already queued for the pane
// covers mail that arrives after it. Wakes of one pane are also at least
// mailWakeGap apart. Once a wake is typed its record goes, so a daemon whose
// mail was all announced is back on the sink's fast path.
//
// [agents.mail] wake = false turns it off. So does turning the agent features
// off, since the queue then types nothing.

// mailWakeGrace is how long new mail stays unannounced before a wake is
// typed. It leaves room for an agent mid-turn to hear of it from its hook, or
// to read its mail.
const mailWakeGrace = 5 * time.Second

// mailWakeGap is the least time between two wakes of one pane.
const mailWakeGap = 30 * time.Second

// mailWakeBy is the by of a wake entry, as list-queued shows it.
const mailWakeBy = "tuios mail"

// mailWakeSender is one message that may wake its recipient, and the origin of
// the call that sent it.
type mailWakeSender struct {
	id     uint64
	origin queueOrigin
}

// mailWakePane is what the waker holds for one pane.
type mailWakePane struct {
	session string
	senders []mailWakeSender
	timer   *time.Timer
	// entry is the wake waiting in the pane's queue, nil when none.
	entry *queueEntry
}

// mailWakeMaxSenders bounds the senders kept for one pane. Only the newest
// are needed: a wake picks the newest sender that may type into the pane.
const mailWakeMaxSenders = 16

// mailWaker holds the panes that have mail which may wake them. The zero
// value is ready and off.
type mailWaker struct {
	on    atomic.Bool
	mu    sync.Mutex
	panes map[string]*mailWakePane
	// count is len(panes), so the event sink returns at once when no pane
	// waits on mail.
	count atomic.Int64
	// recent is when a wake was last typed into each pane, for the gap. An
	// entry older than the gap is pruned when mail arrives.
	recent map[string]time.Time
	// grace and gap replace mailWakeGrace and mailWakeGap when set. Only
	// tests set them.
	grace time.Duration
	gap   time.Duration
}

// SetMailWake applies [agents.mail] wake. Turning it off drops what waits.
func (d *Daemon) SetMailWake(on bool) {
	w := &d.mailWake
	w.on.Store(on)
	if on {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, p := range w.panes {
		if p.timer != nil {
			p.timer.Stop()
		}
		delete(w.panes, id)
	}
	w.count.Store(0)
}

func (w *mailWaker) graceFor() time.Duration {
	if w.grace > 0 {
		return w.grace
	}
	return mailWakeGrace
}

func (w *mailWaker) gapFor() time.Duration {
	if w.gap > 0 {
		return w.gap
	}
	return mailWakeGap
}

// paneLocked returns the pane's record, making one. It holds mu.
func (w *mailWaker) paneLocked(window, session string) *mailWakePane {
	if w.panes == nil {
		w.panes = map[string]*mailWakePane{}
	}
	p := w.panes[window]
	if p == nil {
		p = &mailWakePane{}
		w.panes[window] = p
		w.count.Add(1)
	}
	p.session = session
	return p
}

// dropLocked forgets a pane. It holds mu.
func (w *mailWaker) dropLocked(window string) {
	p := w.panes[window]
	if p == nil {
		return
	}
	if p.timer != nil {
		p.timer.Stop()
	}
	delete(w.panes, window)
	w.count.Add(-1)
}

// armLocked looks at the pane after the grace, or after the gap since the
// last wake when that is later, unless a look is already armed. It holds mu.
func (w *mailWaker) armLocked(d *Daemon, window string, p *mailWakePane) {
	if p.timer != nil {
		return
	}
	wait := w.graceFor()
	if last, ok := w.recent[window]; ok {
		wait = max(wait, w.gapFor()-time.Since(last))
	}
	p.timer = time.AfterFunc(wait, func() { d.mailWakeLook(window) })
}

// noteMailSent records a direct message for window, sent by a call with
// origin, and arms the look that may wake the pane. It runs after the message
// is stored.
func (d *Daemon) noteMailSent(session, window string, id uint64, origin queueOrigin) {
	w := &d.mailWake
	if !w.on.Load() || window == "" || window == AgentInboxHuman {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for pane, at := range w.recent {
		if time.Since(at) > w.gapFor() {
			delete(w.recent, pane)
		}
	}
	p := w.paneLocked(window, session)
	p.senders = append(p.senders, mailWakeSender{id: id, origin: origin})
	if n := len(p.senders); n > mailWakeMaxSenders {
		p.senders = append(p.senders[:0], p.senders[n-mailWakeMaxSenders:]...)
	}
	w.armLocked(d, window, p)
}

// noteMailWakeEvent folds one session event into the waker: a pane that comes
// to rest with mail waiting gets a look, and a pane that closes is forgotten.
// It runs on the session's event sink with the state lock held, so it reads
// nothing but the event.
func (d *Daemon) noteMailWakeEvent(ev SessionEvent) {
	w := &d.mailWake
	if w.count.Load() == 0 {
		return
	}
	switch ev.Type {
	case EventWindowClosed:
		w.mu.Lock()
		w.dropLocked(ev.Window)
		w.mu.Unlock()
	case EventAgentState:
		if ev.State != AgentStateIdle.Name() && ev.State != AgentStateDone.Name() && ev.State != AgentStateUnknown.Name() {
			return
		}
		w.mu.Lock()
		if p := w.panes[ev.Window]; p != nil {
			w.armLocked(d, ev.Window, p)
		}
		w.mu.Unlock()
	}
}

// mailWakeLook is the armed look at a pane. It queues the wake when the pane
// is at rest and holds mail nobody announced, and otherwise leaves the pane to
// its next rest or its next mail.
func (d *Daemon) mailWakeLook(window string) {
	w := &d.mailWake
	w.mu.Lock()
	p := w.panes[window]
	if p == nil {
		w.mu.Unlock()
		return
	}
	p.timer = nil
	senders := append([]mailWakeSender(nil), p.senders...)
	queued := p.entry
	w.mu.Unlock()

	if !w.on.Load() || d.agentsOff.Load() || d.ctx.Err() != nil {
		return
	}
	sess := d.sessionHolding(window)
	if sess == nil {
		w.mu.Lock()
		w.dropLocked(window)
		w.mu.Unlock()
		return
	}
	sum, mark := d.agents.unannounced(sess.Name(), window)
	if sum.unread == 0 || sum.newest <= mark {
		// Read, or announced by a hook. Nothing to wake for, and nothing to
		// keep until the next mail.
		w.mu.Lock()
		if w.panes[window] == p && p.timer == nil && (p.entry == nil || !d.queue.holds(window, p.entry)) {
			w.dropLocked(window)
		}
		w.mu.Unlock()
		return
	}
	if queued != nil && d.queue.holds(window, queued) {
		// A wake already waits in the pane's queue. It is written again
		// before it is typed, so it counts this message too.
		d.agents.claimNotice(sess.Name(), window, sum.newest, mark)
		return
	}
	target, ok := findWindowState(sess.GetState(), window)
	if !ok {
		return
	}
	// The pane must be at rest the way the queue judges it: idle or done,
	// and unknown only for a harness that cannot show idle. A pane on none
	// has no agent, and its prompt line is a shell's.
	if !d.agentReady(target, fanReadyStates) {
		return
	}
	session := sess.Name()
	e := &queueEntry{
		text: mailWakeText(sum, session, window),
		by:   mailWakeBy,
	}
	// The newest sender that may type into the pane now lends the wake its
	// authority. The queue checks it again before typing.
	picked := false
	for i := len(senders) - 1; i >= 0; i-- {
		if senders[i].id <= mark {
			break
		}
		e.origin = senders[i].origin
		if d.queueOriginRefusal(e, sess, target) == "" {
			picked = true
			break
		}
	}
	if !picked {
		LogBasic("Mail for window %s wakes nothing: no sender of it may type into the pane", shortWindowID(window))
		return
	}
	// The line is written again right before it is typed, and dropped when
	// the mail was read meanwhile.
	e.recheck = func() (string, bool) {
		now, _ := d.agents.unannounced(session, window)
		if now.unread == 0 {
			return "", false
		}
		return mailWakeText(now, session, window), true
	}
	e.done = func() { d.mailWakeTyped(window, e) }
	// The claim fails when a hook announced the mail since it was looked at:
	// the agent heard of it, and no wake is queued.
	if !d.agents.claimNotice(session, window, sum.newest, mark) {
		return
	}
	if _, verr := d.queuePrompt(sess, target, e); verr != nil {
		d.agents.unclaimNotice(window, sum.newest, mark)
		LogBasic("Mail for window %s wakes nothing: %s", shortWindowID(window), verr.Message)
		return
	}
	w.mu.Lock()
	if w.panes[window] == p {
		p.entry = e
		p.senders = p.senders[:0]
	}
	w.mu.Unlock()
}

// mailWakeTyped forgets the pane's wake record once its wake left the queue,
// typed or dropped, unless more mail came for it meanwhile. It notes when, for
// the gap.
func (d *Daemon) mailWakeTyped(window string, e *queueEntry) {
	w := &d.mailWake
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.recent == nil {
		w.recent = map[string]time.Time{}
	}
	w.recent[window] = time.Now()
	p := w.panes[window]
	if p == nil || p.entry != e {
		return
	}
	p.entry = nil
	if len(p.senders) == 0 && p.timer == nil {
		w.dropLocked(window)
	}
}

// holds reports whether e still waits in window's queue, not yet typed.
func (q *agentQueues) holds(window string, e *queueEntry) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	pq := q.panes[window]
	if pq == nil {
		return false
	}
	for _, x := range pq.entries {
		if x == e {
			return x.state == queueWaiting || x.state == queueDelivering
		}
	}
	return false
}

// mailOriginOf is the origin a wake for mail sent by cs carries: the person,
// a linked machine, the pane the connection belongs to, or a caller outside
// every pane. It reads the connection, never the message's from, which is a
// claim. It reports false for a caller that may not lend a wake its
// authority: a connection restricted to read_only, which may type into no
// pane, a restricted connection that is no pane, and a pane on another
// machine, which queue-prompt refuses too (refuseForwardedPane).
func (d *Daemon) mailOriginOf(cs *connState, verifiedHuman bool) (queueOrigin, bool) {
	if cs != nil {
		if cs.paneOnly {
			return queueOrigin{}, false
		}
		if sc := cs.scope.Load(); sc != nil {
			if sc.readOnly {
				return queueOrigin{}, false
			}
			// Under scope own the pane's grants decide, as for queue-prompt.
			if sc.window == "" {
				return queueOrigin{}, false
			}
			return queueOrigin{kind: "pane", window: sc.window, session: d.sessionNameByID(sc.sessionID)}, true
		}
	}
	switch {
	case cs != nil && cs.viaLink:
		cs.mu.Lock()
		peer := cs.linkPeer
		cs.mu.Unlock()
		return queueOrigin{kind: "link", peer: peer, conn: cs.clientID}, true
	case verifiedHuman:
		return queueOrigin{kind: queueByHuman}, true
	}
	if pa := d.paneAuthority(cs); pa != nil {
		if pa.hosted {
			return queueOrigin{}, false
		}
		return queueOrigin{kind: "pane", window: pa.window, session: pa.session}, true
	}
	return queueOrigin{kind: queueByShell}, true
}

// mailWakeText is the wake line: the count and the command, and nothing a
// sender chose.
func mailWakeText(s mailSummary, session, window string) string {
	if s.unread == 0 {
		return ""
	}
	if s.unread == 1 {
		return "You have 1 unread tuios message. Read it with: " + mailReadCommand(session, window)
	}
	return "You have " + strconv.Itoa(s.unread) + " unread tuios messages. Read them with: " + mailReadCommand(session, window)
}
