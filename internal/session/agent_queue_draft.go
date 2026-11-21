package session

import (
	"time"
	"unsafe"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
	uv "github.com/charmbracelet/ultraviolet"

	"tuios.dev/vt"
)

const queueTypingQuiet = 3 * time.Second

const queueDraftSurface = 10 * time.Minute

const queueDraftLook = screenSettleDelay

// DraftHeldSummary is the Inbox question for a queue a draft held for
// queueDraftSurface. The agents view shows it as "Waits: ...".
const DraftHeldSummary = "your draft is in the prompt box"

func draftSurfaceFor() time.Duration {
	if d := testknob.Millis("draft_surface_ms"); d > 0 {
		return d
	}
	return queueDraftSurface
}

func (p *PTY) noteHumanKey(data []byte) {
	if len(data) == 0 || isFocusReport(data) || isMouseReport(data) {
		return
	}
	p.humanKeyAt.Store(time.Now().UnixNano())
	if f := p.draftWatch.Load(); f != nil {
		(*f)()
	}
}

func isMouseReport(data []byte) bool {
	return len(data) >= 3 && data[0] == 0x1b && data[1] == '[' && (data[2] == '<' || data[2] == 'M')
}

type draftGate struct {
	pty   *PTY
	keyAt int64
	draft bool
}

func (d *Daemon) readDraftGate(sess *Session, target WindowState) draftGate {
	pty, err := d.resolvePTYForTarget(sess, target.ID)
	if err != nil {
		return draftGate{}
	}
	g := draftGate{pty: pty, keyAt: pty.humanKeyAt.Load()}
	reg := d.agentMatcher.registry
	hid := firstNonEmpty(target.AgentHarness, sess.agentClaimFor(target.ID).harness)
	if reg.HasBoxRule(hid) {
		tail, shown := pty.tailShown(reg.ScreenLines(hid))
		g.draft, _ = reg.BoxDraft(hid, tail, shown)
	}
	return g
}

func (p *PTY) tailShown(lines int) (tail, shown []string) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil || lines <= 0 {
		return nil, nil
	}
	t := p.terminal
	w, h := t.Width(), t.Height()
	if w <= 0 || h <= 0 {
		return nil, nil
	}
	m := min(lines, h)
	var plainStack, shownStack [64]int
	plainEnds, shownEnds := plainStack[:0], shownStack[:0]
	if m > len(plainStack) {
		plainEnds = make([]int, 0, m)
		shownEnds = make([]int, 0, m)
	}
	plain := make([]byte, 0, m*w)
	faintless := make([]byte, 0, m*w)
	plainLast, shownLast := w, w
	for y := h - 1; y >= 0 && len(plainEnds) < lines; y-- {
		ps, fs := len(plain), len(faintless)
		rows := min(lines-len(plainEnds), y+1)
		if cap(plain)-ps < plainLast {
			plain = regrowBytes(plain, rows*plainLast)
		}
		if cap(faintless)-fs < shownLast {
			faintless = regrowBytes(faintless, rows*shownLast)
		}
		for x := range w {
			c := t.CellAt(x, y)
			if c == nil {
				continue
			}
			text := vt.CellText(c.Content)
			plain = append(plain, text...)
			if c.Style.Attrs&uv.AttrFaint != 0 {
				for range max(c.Width, 1) {
					faintless = append(faintless, ' ')
				}
				continue
			}
			faintless = append(faintless, text...)
		}
		plainLast = max(plainLast, len(plain)-ps)
		shownLast = max(shownLast, len(faintless)-fs)
		pe := trimBlanks(plain, ps)
		plain = plain[:pe]
		if pe == ps {
			faintless = faintless[:fs]
			continue
		}
		se := trimBlanks(faintless, fs)
		faintless = faintless[:se]
		plainEnds = append(plainEnds, pe)
		shownEnds = append(shownEnds, se)
	}
	k := len(plainEnds)
	if k == 0 {
		return nil, nil
	}
	tail = make([]string, k)
	shown = make([]string, k)
	ps := unsafe.String(unsafe.SliceData(plain), len(plain))
	fs := unsafe.String(unsafe.SliceData(faintless), len(faintless))
	pprev, sprev := 0, 0
	for i := range k {
		tail[k-1-i] = ps[pprev:plainEnds[i]]
		shown[k-1-i] = fs[sprev:shownEnds[i]]
		pprev, sprev = plainEnds[i], shownEnds[i]
	}
	return tail, shown
}

func trimBlanks(buf []byte, start int) int {
	i := len(buf)
	for i > start && (buf[i-1] == ' ' || buf[i-1] == '\t') {
		i--
	}
	return i
}

func regrowBytes(buf []byte, extra int) []byte {
	nb := make([]byte, len(buf), len(buf)+extra)
	copy(nb, buf)
	return nb
}

func (q *agentQueues) holdLocked(d *Daemon, window string, pq *paneQueue, g draftGate, now time.Time) (held, changed bool) {
	typing := time.Duration(0)
	if g.keyAt > 0 {
		if since := now.Sub(time.Unix(0, g.keyAt)); since < queueTypingQuiet {
			typing = queueTypingQuiet - since
		}
	}
	if typing == 0 && !g.draft {
		return false, q.unholdLocked(pq)
	}
	if g.draft && pq.draftSince == 0 {
		pq.draftSince = now.UnixNano()
		pq.surface = time.AfterFunc(draftSurfaceFor(), func() { d.surfaceDraft(window) })
		changed = true
	} else if !g.draft && pq.draftSince != 0 {
		q.endDraftLocked(pq)
		changed = true
	}
	if pq.watched != g.pty && g.pty != nil {
		if pq.watched != nil {
			pq.watched.draftWatch.Store(nil)
		}
		f := func() { d.nudgeQueued(window) }
		g.pty.draftWatch.Store(&f)
		pq.watched = g.pty
	}
	if typing > 0 {
		q.armLocked(d, window, pq, typing)
	} else if pq.timer != nil {
		pq.timer.Stop()
	}
	return true, changed
}

func (q *agentQueues) unholdLocked(pq *paneQueue) bool {
	if pq.watched != nil {
		pq.watched.draftWatch.Store(nil)
		pq.watched = nil
	}
	if pq.draftSince == 0 {
		return false
	}
	q.endDraftLocked(pq)
	return true
}

func (q *agentQueues) endDraftLocked(pq *paneQueue) {
	pq.draftSince = 0
	if pq.surface != nil {
		pq.surface.Stop()
		pq.surface = nil
	}
}

func (q *agentQueues) draftHeld(window string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	pq := q.panes[window]
	return pq != nil && pq.draftSince != 0
}

func (d *Daemon) nudgeQueued(window string) {
	q := &d.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	pq := q.panes[window]
	if pq == nil || pq.watched == nil || pq.delivering {
		return
	}
	q.armLocked(d, window, pq, queueDraftLook)
}

func (d *Daemon) surfaceDraft(window string) {
	q := &d.queue
	q.mu.Lock()
	pq := q.panes[window]
	held := pq != nil && pq.draftSince != 0
	q.mu.Unlock()
	if !held {
		return
	}
	sess := d.sessionHolding(window)
	if sess == nil {
		return
	}
	target, ok := findWindowState(sess.GetState(), window)
	if !ok {
		return
	}
	LogBasic("A draft held the queue for window %s for %s", shortWindowID(window), draftSurfaceFor())
	d.attention.openDraftHeld(sess.Name(), target)
}

func (a *attentionStore) openDraftHeld(sessionName string, w WindowState) {
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
		Summary:   DraftHeldSummary,
	})
}
