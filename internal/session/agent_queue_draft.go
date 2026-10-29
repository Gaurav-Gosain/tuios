package session

import (
	"os"
	"strconv"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"tuios.dev/vt"
)

// Draft-safe delivery: a queued entry is not typed into the person's draft.
//
// An agent at rest is where a person writes their next prompt, and a rest is
// also when the queue types. A mail wake or a queued message typed then lands
// in the middle of the half-written prompt. So on top of the rest the queue
// already waits for, the look at a pane holds its next entry while either is
// true:
//
//   - a person typed into the pane through a client less than
//     queueTypingQuiet ago (PTY.humanKeyAt), focus and mouse reports left out;
//   - the harness's [prompt_box] block (internal/harness/draft.go) reads the
//     input box, with faint cells left out, and finds text in it.
//
// A pause in typing is not an empty box, which is why the box is read too. A
// harness with no [prompt_box] block gets the typing pause alone.
//
// Nothing polls. While an entry is held, the pane's PTY carries a watch that
// re-arms the look a moment after each output chunk and each key, so the
// look runs again when the box changes and not otherwise. A pause in typing
// arms one timer for the end of the pause. A draft held for
// queueDraftSurface opens a question in the Inbox, which moves the agent's row
// to Needs you. Nothing is ever dropped for waiting: the person decides.

// queueTypingQuiet is how long after a person's last key into a pane a
// queued entry may be typed into it.
const queueTypingQuiet = 3 * time.Second

// queueDraftSurface is how long a draft may hold a queue before the Inbox asks
// the person about it.
const queueDraftSurface = 10 * time.Minute

// queueDraftLook is how long after a change in a held pane the look runs, so
// a burst of output or keys costs one look.
const queueDraftLook = screenSettleDelay

// DraftHeldSummary is the Inbox question for a queue a draft held for
// queueDraftSurface. The agents view shows it as "Waits: ...".
const DraftHeldSummary = "your draft is in the prompt box"

// draftSurfaceFor is queueDraftSurface, or the TUIOS_E2E_DRAFT_SURFACE_MS an
// E2E test sets, so the move to Needs you can be shown without ten minutes.
func draftSurfaceFor() time.Duration {
	if os.Getenv("TUIOS_E2E") == "1" {
		if ms, err := strconv.Atoi(os.Getenv("TUIOS_E2E_DRAFT_SURFACE_MS")); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return queueDraftSurface
}

// noteHumanKey records a key a person typed into the pane through a client.
// A focus report is the client's, and a mouse report is no text in a box.
func (p *PTY) noteHumanKey(data []byte) {
	if len(data) == 0 || isFocusReport(data) || isMouseReport(data) {
		return
	}
	p.humanKeyAt.Store(time.Now().UnixNano())
	if f := p.draftWatch.Load(); f != nil {
		(*f)()
	}
}

// isMouseReport reports whether data starts as an SGR or X10 mouse report.
func isMouseReport(data []byte) bool {
	return len(data) >= 3 && data[0] == 0x1b && data[1] == '[' && (data[2] == '<' || data[2] == 'M')
}

// draftGate is what a look reads about the person's draft before it takes the
// queue's lock.
type draftGate struct {
	pty *PTY
	// keyAt is PTY.humanKeyAt.
	keyAt int64
	// draft is true when the harness's box rule found text in the box.
	draft bool
}

// readDraftGate reads the pane's key clock and, for a harness with a box rule,
// its input box.
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

// tailShown is tailText, and beside it the same rows with every faint cell
// left blank: a placeholder or a suggestion in an input box is drawn faint,
// and the person's own text is not.
func (p *PTY) tailShown(lines int) (tail, shown []string) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil || lines <= 0 {
		return nil, nil
	}
	t := p.terminal
	w, h := t.Width(), t.Height()
	var plain, faintless strings.Builder
	for y := h - 1; y >= 0 && len(tail) < lines; y-- {
		plain.Reset()
		faintless.Reset()
		for x := range w {
			c := t.CellAt(x, y)
			if c == nil {
				continue
			}
			text := vt.CellText(c.Content)
			plain.WriteString(text)
			if c.Style.Attrs&uv.AttrFaint != 0 {
				text = strings.Repeat(" ", max(c.Width, 1))
			}
			faintless.WriteString(text)
		}
		if line := strings.TrimRight(plain.String(), " \t"); line != "" {
			tail = append(tail, line)
			shown = append(shown, strings.TrimRight(faintless.String(), " \t"))
		}
	}
	for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
		tail[i], tail[j] = tail[j], tail[i]
		shown[i], shown[j] = shown[j], shown[i]
	}
	return tail, shown
}

// holdLocked decides whether the person's typing or draft holds the pane's
// next entry. A held entry gets the watch on its pane, and a pause in typing a
// look at its end. changed says the draft mark on the pane changed, for
// publishQueued. It holds mu.
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
		// The box holds a draft: the watch looks again when it changes.
		pq.timer.Stop()
	}
	return true, changed
}

// unholdLocked takes the watch off the pane and forgets the draft. It reports
// whether the pane was marked as held by a draft. It holds mu.
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

// endDraftLocked forgets the draft and its timer. It holds mu.
func (q *agentQueues) endDraftLocked(pq *paneQueue) {
	pq.draftSince = 0
	if pq.surface != nil {
		pq.surface.Stop()
		pq.surface = nil
	}
}

// draftHeld reports whether a draft holds the pane's queue.
func (q *agentQueues) draftHeld(window string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	pq := q.panes[window]
	return pq != nil && pq.draftSince != 0
}

// nudgeQueued is the watch on a held pane: output or a key arms the look a
// moment later.
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

// surfaceDraft opens the Inbox question for a draft that has held the queue
// for queueDraftSurface.
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

// openDraftHeld opens a question on the pane's blocking key: a queued message
// waits for the person's draft. Like the held-screen question, the pane's next
// state change closes it, and publishQueued closes it when the hold ends.
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
