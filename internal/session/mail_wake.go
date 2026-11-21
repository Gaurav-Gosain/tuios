package session

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const mailWakeGrace = 5 * time.Second

const mailWakeGap = 30 * time.Second

const mailWakeBy = "tuios mail"

type mailWakeSender struct {
	id       uint64
	origin   queueOrigin
	finished string
}

type mailWakePane struct {
	session string
	senders []mailWakeSender
	timer   *time.Timer
	entry   *queueEntry
	answers []mailWakeSender
}

const mailWakeMaxSenders = 16

type mailWaker struct {
	on     atomic.Bool
	mu     sync.Mutex
	panes  map[string]*mailWakePane
	count  atomic.Int64
	recent map[string]time.Time
	grace  time.Duration
	gap    time.Duration
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

func (d *Daemon) noteMailSent(session, window string, id uint64, origin queueOrigin) {
	d.noteMailSentAs(session, window, id, origin, "")
}

func (d *Daemon) noteMailSentAs(session, window string, id uint64, origin queueOrigin, finished string) {
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
	p.senders = append(p.senders, mailWakeSender{id: id, origin: origin, finished: finished})
	if n := len(p.senders); n > mailWakeMaxSenders {
		p.senders = append(p.senders[:0], p.senders[n-mailWakeMaxSenders:]...)
	}
	w.armLocked(d, window, p)
}

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
		if ev.Reason == AgentReasonSilent || ev.State != AgentStateIdle.Name() && ev.State != AgentStateDone.Name() && ev.State != AgentStateUnknown.Name() {
			return
		}
		w.mu.Lock()
		if p := w.panes[ev.Window]; p != nil {
			w.armLocked(d, ev.Window, p)
		}
		w.mu.Unlock()
	}
}

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
		w.mu.Lock()
		if w.panes[window] == p && p.timer == nil && (p.entry == nil || !d.queue.holds(window, p.entry)) {
			w.dropLocked(window)
		}
		w.mu.Unlock()
		return
	}
	var answers []mailWakeSender
	for _, s := range senders {
		if s.finished != "" && s.id > mark {
			answers = append(answers, s)
		}
	}
	if queued != nil && d.queue.holds(window, queued) {
		w.mu.Lock()
		if w.panes[window] == p {
			for _, a := range answers {
				if !slices.ContainsFunc(p.answers, func(x mailWakeSender) bool { return x.id == a.id }) {
					p.answers = append(p.answers, a)
				}
			}
		}
		w.mu.Unlock()
		d.agents.claimNotice(sess.Name(), window, sum.newest, mark)
		return
	}
	target, ok := findWindowState(sess.GetState(), window)
	if !ok {
		return
	}
	if !d.agentReady(target, fanReadyStates) {
		return
	}
	session := sess.Name()
	tally := func() string {
		w.mu.Lock()
		got := answers
		if w.panes[window] == p && p.entry != nil {
			got = slices.Clone(p.answers)
		}
		w.mu.Unlock()
		return d.childTally(session, got)
	}
	e := &queueEntry{
		text: mailWakeText(sum, session, window, tally()),
		by:   mailWakeBy,
	}
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
	e.recheck = func() (string, bool) {
		now, _ := d.agents.unannounced(session, window)
		if now.unread == 0 {
			return "", false
		}
		return mailWakeText(now, session, window, tally()), true
	}
	e.done = func() { d.mailWakeTyped(window, e) }
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
		p.answers = answers
		p.senders = p.senders[:0]
	}
	w.mu.Unlock()
}

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
	p.entry, p.answers = nil, nil
	if len(p.senders) == 0 && p.timer == nil {
		w.dropLocked(window)
	}
}

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

func (d *Daemon) mailOriginOf(cs *connState, verifiedHuman bool) (queueOrigin, bool) {
	if cs != nil {
		if cs.paneOnly {
			return queueOrigin{}, false
		}
		if sc := cs.scope.Load(); sc != nil {
			if sc.readOnly {
				return queueOrigin{}, false
			}
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
		return queueOrigin{kind: "pane", window: pa.window, session: pa.session, outside: pa.outsideWithAdmin()}, true
	}
	return queueOrigin{kind: queueByShell}, true
}

func mailWakeText(s mailSummary, session, window, tally string) string {
	if s.unread == 0 {
		return ""
	}
	if tally != "" {
		tally += " "
	}
	if s.unread == 1 {
		return "You have 1 unread tuios message. " + tally + "Read it with: " + mailReadCommand(session, window)
	}
	return "You have " + strconv.Itoa(s.unread) + " unread tuios messages. " + tally + "Read them with: " + mailReadCommand(session, window)
}

var childTallyOrder = []string{delegateDone, delegateErrored, delegateClosed}

func (d *Daemon) childTally(session string, answers []mailWakeSender) string {
	if len(answers) == 0 {
		return ""
	}
	n := map[string]int{}
	for _, a := range answers {
		if !d.agents.messageRead(session, a.id) {
			n[a.finished]++
		}
	}
	var parts []string
	for _, state := range childTallyOrder {
		if n[state] > 0 {
			parts = append(parts, strconv.Itoa(n[state])+" "+state)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Child agents finished: " + strings.Join(parts, ", ") + "."
}
