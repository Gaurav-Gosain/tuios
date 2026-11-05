// Package agentseen holds two small rules about what a person has already
// seen of an agent: whether its row earns the unseen mark, and whether a state
// change still owes a notification. Both are pure, so the TUI and the desktop
// app draw and alert from the same answers.
//
// The unseen mark: a row is unseen when its agent finished or failed after
// the person last viewed its thread. The watermark is the agent's own
// state-entry time, as the daemon stamps it, so two clocks never meet.
//
// The notification baseline: an agent notifies once per run and state. The
// first state a client sees an agent in is the baseline and notifies nothing,
// so an attach, a reconnect or a repeated report does not replay an alert.
package agentseen

// Unseen reports whether an agent row earns the unseen mark. state and
// doneSeen are the state the rows draw and the rail's seen bit for a done
// agent (a person who focused the pane has seen its turn). stateAt is when
// the agent entered that state, and viewedAt the stateAt the person last
// viewed its thread at, zero for never.
//
// A finished agent is unseen until either the thread is viewed or the pane is
// focused. A failed agent is unseen until the thread is viewed, even if it was
// never opened before.
func Unseen(state string, doneSeen bool, stateAt, viewedAt int64) bool {
	switch state {
	case "done":
		if doneSeen {
			return false
		}
	case "errored":
	default:
		return false
	}
	return stateAt > viewedAt
}

// runClass is the class of states that notify once per run: the rest states
// share one, since a turn that ends idle and then reports done is one finish.
// It is "" for a state that is not keyed, such as needs_input, where each
// question is news.
func runClass(state string) string {
	switch state {
	case "done", "idle", "unknown":
		return "rest"
	case "errored":
		return "errored"
	}
	return ""
}

// ledgerEntry is what one agent has notified: the run each class last
// notified for, or was baselined at.
type ledgerEntry struct {
	rest, errored       uint64
	restSet, erroredSet bool
}

func (e *ledgerEntry) slot(class string) (*uint64, *bool) {
	if class == "errored" {
		return &e.errored, &e.erroredSet
	}
	return &e.rest, &e.restSet
}

// AlertLedger records, per agent, the run each keyed state last notified for.
// A run is the agent's finished-turn count (CompletionSeq): a turn that
// finishes, reports done again, or fails twice before it finishes, is one run.
// The zero value is ready to use.
type AlertLedger struct {
	agents map[string]*ledgerEntry
}

// Baseline records the state an agent is in when this ledger first hears of
// it, so that run notifies nothing more for that state. It does nothing for an
// agent the ledger knows already.
func (l *AlertLedger) Baseline(id, state string, run uint64) {
	if _, ok := l.agents[id]; ok {
		return
	}
	if l.agents == nil {
		l.agents = make(map[string]*ledgerEntry)
	}
	e := &ledgerEntry{}
	l.agents[id] = e
	if class := runClass(state); class != "" {
		r, set := e.slot(class)
		*r, *set = run, true
	}
}

// Due reports whether a change into state, in run, still owes a
// notification. Only the same run is held back: a count that went down, as
// after a daemon restart, is a new run.
func (l *AlertLedger) Due(id, state string, run uint64) bool {
	class := runClass(state)
	if class == "" {
		return true
	}
	e, ok := l.agents[id]
	if !ok {
		return true
	}
	r, set := e.slot(class)
	return !*set || *r != run
}

// Note records that state notified in run.
func (l *AlertLedger) Note(id, state string, run uint64) {
	class := runClass(state)
	if l.agents == nil {
		l.agents = make(map[string]*ledgerEntry)
	}
	e, ok := l.agents[id]
	if !ok {
		e = &ledgerEntry{}
		l.agents[id] = e
	}
	if class == "" {
		return
	}
	r, set := e.slot(class)
	*r, *set = run, true
}

// Len is how many agents the ledger knows.
func (l *AlertLedger) Len() int { return len(l.agents) }

// Keep drops every agent live reports false for.
func (l *AlertLedger) Keep(live func(id string) bool) {
	for id := range l.agents {
		if !live(id) {
			delete(l.agents, id)
		}
	}
}
