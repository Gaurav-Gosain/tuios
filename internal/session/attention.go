//go:build !slim

package session

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/risk"
)

// The Inbox: one daemon-owned queue of everything waiting for the person.
//
// Attention used to be spread over the rail's counts, title glyphs, the dock
// (for the attached session only), the newest-message jump and the mail
// overlay, and a pane in a session nobody was attached to raised nothing at
// all. The daemon already sees every transition in every session, so it keeps
// the one list here and every surface reads it: list-attention answers it, and
// every change to it is an attention event on the resumable stream.
//
// An item is derived from facts the daemon already owns, and it closes when the
// fact that opened it stops being true:
//
//   - approval and question: a pane on needs_input, split by blocked_by. It
//     closes when the pane leaves needs_input.
//   - errored: a pane on errored. It closes when the pane leaves errored.
//   - finished: a pane's completion_seq went up while it came to rest. It
//     closes when an attached client focuses the pane (the same rule as
//     finished_unread), when the agent starts another turn, or on dismiss.
//   - mail: a message to human in one thread. It closes when the ring says the
//     person's mail in that thread is read.
//   - resume: a pane a restore brought back with an agent conversation that
//     can be resumed (agent_resume.go). It closes when the conversation is
//     resumed, when an agent starts working in the pane, or on dismiss.
//
// Every item is keyed by what it is about (session, window and class, or
// session and thread for mail), so a pane flapping, a harness repeating itself
// or sixteen agents in a fan finishing together update one row each rather
// than adding rows, and an update that changes nothing publishes nothing.
//
// Items survive a daemon restart, within reason. finished and errored describe
// something that happened and has not been looked at, and that is still true
// after a restart. approval and question describe a prompt on a screen, and the
// process that painted it does not survive the restart, so they are dropped on
// load rather than shown as a question nobody is asking any more. mail is
// dropped too: it points into the message ring, which does not survive, and
// thread ids start again from 1, so a saved item would be merged into whatever
// unrelated thread next took its id. resume is dropped on load as well, because
// the restore that follows every start opens it again from the window's
// recorded conversation, which is where the fact lives.

// attentionMaxItems bounds the queue. One item per pane per class and one per
// mail thread is already bounded by the panes and the ring, so this is only a
// backstop against a pathological number of sessions.
const attentionMaxItems = 1024

// attentionMaxSummary bounds an item's summary, in bytes. The summary is what a
// dock line and an Inbox row show, and it is written to disk and sent to every
// subscriber, so it is kept to a line.
const attentionMaxSummary = 160

// attentionSaveDelay is how long a change waits before the queue is written,
// so a burst of transitions costs one write.
const attentionSaveDelay = 500 * time.Millisecond

// attentionStore is the daemon's queue. Its lock is its own and nothing is
// called under it but the event hub, whose lock is a leaf, so the session event
// sink may call into it with the session's state lock held.
type attentionStore struct {
	mu     sync.Mutex
	items  map[string]*AttentionItem
	byKey  map[string]string
	nextID uint64
	rev    uint64

	// publish delivers an attention event. It is called with mu held, so the
	// order of events on the stream is the order of changes to the queue.
	publish func(streamEvent)
	// currentSeq reads the hub's last seq, for a listing's resume point.
	currentSeq func() uint64

	// path is where the queue is saved, empty for a store that is never saved.
	path      string
	saveTimer *time.Timer
	// frozen stops every change from being saved, from the moment the daemon's
	// final save is taken. Shutdown closes panes after it, and those closes must
	// not reach the file as if the person had dealt with the items.
	frozen bool

	// holds are the approval requests a hook is waiting on, by request id, and
	// settled remembers how the most recent ones ended so a second reply is
	// answered with the first one's result. Both live under mu with the items
	// they point at, so an item and its hold never disagree. See approvals.go.
	holds   map[string]*approvalHold
	settled map[string]approvalOutcome
	// settledOrder is settled's ids oldest first, for its bound.
	settledOrder []string

	// asks are the questions ask-human put to the person, by request id, and
	// askSettled how the most recent ones ended, for a caller that comes back
	// for the answer. They live under mu with their items for the reason holds
	// do. See ask_human.go.
	asks            map[string]*askHold
	askSettled      map[string]askOutcome
	askSettledOrder []string

	// hostItems are the items mirrored from linked hosts, keyed by their id
	// here, which is the host's name, a colon and the host's own id. They are
	// kept apart from items so nothing a host sends can evict an item of this
	// machine, and they are never saved: the host keeps its own queue. See
	// attention_hosts.go.
	hostItems map[string]*AttentionItem
	// hostHidden are host items the person dismissed here, with the host's
	// revision of the item at the time. The item stays hidden until the host
	// changes it again.
	hostHidden map[string]uint64

	// snoozed are the items the person snoozed, by id, each with
	// SnoozedUntil set, and snoozedKey finds one by what it is about, the way
	// byKey finds an open item. An item is in items or in snoozed, never both.
	// See attention_lifecycle.go.
	snoozed    map[string]*AttentionItem
	snoozedKey map[string]string
	// hostSnoozed are host items the person snoozed here, with the host's
	// revision kept on the copy. Like a dismiss, a snooze of another
	// machine's item marks nothing on that machine.
	hostSnoozed map[string]*AttentionItem
	// wakeTimer wakes the snoozed items whose time has come. There is one,
	// for the earliest, and only while a snooze with a time is running;
	// wakeAt is when it fires.
	wakeTimer *time.Timer
	wakeAt    int64
	// wakeGen counts the timers set, so a timer that fires after it was
	// replaced can tell (see armWakeLocked).
	wakeGen uint64
	// undo is the person's last closes, newest last, for restore.
	undo []attentionUndo
	// now is the clock, replaced in tests.
	now func() time.Time
	// beforeRestore, set only by tests, runs once as restore starts, before
	// it takes mu for the reopen: the moment a pane's transition could slip
	// through. Read and cleared under mu.
	beforeRestore func()

	// risk are the rules that mark an approval risky, set from the approval
	// policy. It is read under mu and replaced whole, so a reload never
	// changes a set a match is reading. See approval_risk.go.
	risk atomic.Pointer[riskSet]
}

func newAttentionStore(publish func(streamEvent), currentSeq func() uint64) *attentionStore {
	return &attentionStore{
		items:      make(map[string]*AttentionItem),
		byKey:      make(map[string]string),
		hostItems:  make(map[string]*AttentionItem),
		hostHidden: make(map[string]uint64),
		publish:    publish,
		currentSeq: currentSeq,
		now:        time.Now,
	}
}

// attentionPath is where the queue is saved: beside the session state, in a
// directory of its own so the session listing never reads it as a session.
func attentionPath() string {
	return filepath.Join(getResurrectionDir(), "attention", "items.json")
}

// attentionKey names what an item is about. A pane has at most one blocking
// item, one errored item and one finished item; a thread has one mail item.
func attentionKey(kind, session, window string, thread uint64) string {
	class := kind
	switch kind {
	case AttentionApproval, AttentionQuestion, AttentionPlan:
		class = "block"
	case AttentionMail:
		return "mail\x00" + session + "\x00" + strconv.FormatUint(thread, 10)
	}
	return class + "\x00" + session + "\x00" + window
}

// attentionItemKey is the key an item is held under. An outbox item is about
// a machine rather than a pane, so it is keyed by that. An ask is keyed by its
// request rather than its pane, since a script outside every pane asks with no
// pane at all. Everything else is attentionKey.
func attentionItemKey(it *AttentionItem) string {
	switch it.Kind {
	case AttentionOutbox:
		return "outbox\x00" + it.ForHost
	case AttentionAsk:
		return "ask\x00" + it.Session + "\x00" + it.RequestID
	case AttentionErrored:
		if it.Session == "" && it.Window == "" {
			// A notice about the daemon itself, such as config.toml,
			// keyed by its name (noteConfigNotice).
			return "notice\x00" + it.Name
		}
	}
	return attentionKey(it.Kind, it.Session, it.Window, it.Thread)
}

// noteOutbox opens, updates or closes the outbox item for host: how many
// messages wait for it and what it refused. With neither, the item closes.
func (a *attentionStore) noteOutbox(host string, waiting int, fails []outboxFailure, since int64) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := "outbox\x00" + host
	if waiting == 0 && len(fails) == 0 {
		a.closeKeyLocked(key, AttentionClosedResolved)
		return
	}
	it := AttentionItem{
		Kind:    AttentionOutbox,
		ForHost: host,
		Name:    attentionText(host, attentionMaxSummary),
		Summary: attentionText(outboxSummary(host, waiting, fails), attentionMaxSummary),
		Count:   waiting,
	}
	if since == 0 && len(fails) > 0 {
		since = fails[0].At
	}
	if _, ok := a.byKey[key]; !ok && since != 0 {
		it.Since = since
	}
	a.upsertLocked(it)
}

// attentionEvent is the stream event for one change. Session and Window are
// copied to the top level so a subscriber's session and window filters apply
// to attention events exactly as to every other event.
func attentionEvent(action string, it AttentionItem) streamEvent {
	return streamEvent{
		Type: EventAttention,
		// Set for an item mirrored from a linked host, so a subscriber that
		// filters by session does not read the host's session name as one on
		// this machine.
		Host:      it.Host,
		Session:   it.Session,
		Window:    it.Window,
		Action:    action,
		Attention: &it,
	}
}

// upsertLocked opens the item named by next's key or updates the one already
// open, and publishes what changed. The caller holds mu. An update keeps the
// open item's id and Since, so it does not reset the wait.
func (a *attentionStore) upsertLocked(next AttentionItem) {
	key := attentionItemKey(&next)
	if id, ok := a.byKey[key]; ok {
		cur := a.items[id]
		next.ID, next.Since = cur.ID, cur.Since
		next.Seq = cur.Seq
		// A hold belongs to the approval it was asked for, and the item
		// shows that approval's line for as long as the hold runs, since
		// that line is what an answer approves. A new message on the same
		// pane, a second call reported before its hook opens its own hold or
		// a notification's generic text, is kept for when the hold ends and
		// does not replace it: the text and the request id an answer
		// carries never come from two different calls. The block turning
		// into a question means the prompt the hook holds is not the one on
		// the screen any more, so the hold ends.
		if cur.RequestID != "" && next.RequestID == "" {
			if next.Kind == AttentionApproval {
				if h := a.holds[cur.RequestID]; h != nil {
					h.latest = next.Summary
				}
				next.RequestID, next.Options, next.Expires = cur.RequestID, cur.Options, cur.Expires
				next.AlwaysScope, next.Summary = cur.AlwaysScope, cur.Summary
				// So is what the hold marked on it: a plan stays a plan, and
				// the risk is the held call's, not the newer line's.
				next.Kind, next.Risk, next.DenyMessage = cur.Kind, cur.Risk, cur.DenyMessage
				next.PlanLines, next.PlanSHA = cur.PlanLines, cur.PlanSHA
			} else {
				a.endHoldLocked(cur.RequestID, approvalOutcome{Reason: approvalEndResolved}, false)
			}
		}
		if attentionSame(*cur, next) {
			return
		}
		a.rev++
		next.Seq = a.rev
		*cur = next
		a.publish(attentionEvent(AttentionUpdated, *cur))
		a.changedLocked()
		return
	}
	// A snoozed item whose fact changed wakes with its id and since. A
	// report that says what the item already said leaves it asleep.
	if a.wakeOnChangeLocked(key, next) {
		return
	}
	if len(a.items) >= attentionMaxItems {
		a.evictOldestLocked()
	}
	a.nextID++
	a.rev++
	next.ID = strconv.FormatUint(a.nextID, 10)
	next.Seq = a.rev
	if next.Since == 0 {
		next.Since = time.Now().UnixNano()
	}
	it := next
	a.items[it.ID] = &it
	a.byKey[key] = it.ID
	a.publish(attentionEvent(AttentionOpened, it))
	a.changedLocked()
}

// attentionSame reports whether two versions of one item say the same thing,
// so a repeated report publishes nothing.
func attentionSame(a, b AttentionItem) bool {
	return a.Kind == b.Kind && a.Workspace == b.Workspace && a.Harness == b.Harness &&
		a.Name == b.Name && a.Summary == b.Summary && a.Count == b.Count &&
		a.CompletionSeq == b.CompletionSeq && a.Window == b.Window &&
		a.RequestID == b.RequestID && a.Expires == b.Expires && a.Stale == b.Stale &&
		a.HeldID == b.HeldID && a.HeldFor == b.HeldFor && a.ForHost == b.ForHost &&
		slices.Equal(a.Options, b.Options) && slices.Equal(a.AlwaysScope, b.AlwaysScope) &&
		a.SnoozedUntil == b.SnoozedUntil && a.MarkedUnread == b.MarkedUnread &&
		slices.Equal(a.Risk, b.Risk) && a.DenyMessage == b.DenyMessage &&
		a.PlanLines == b.PlanLines && a.PlanSHA == b.PlanSHA
}

// closeLocked closes the item with this id, if it is open. The caller holds mu.
func (a *attentionStore) closeLocked(id, reason string) bool {
	return a.closeWithLocked(id, reason, nil)
}

// closeWithLocked is closeLocked with a chance to fill in the closing copy of
// the item before it is published: an answered approval names its answer. An
// item a hook was holding ends the hold, with the close reason as the reason
// the hook gets no answer.
func (a *attentionStore) closeWithLocked(id, reason string, fill func(*AttentionItem)) bool {
	it, ok := a.items[id]
	if !ok {
		return false
	}
	if it.RequestID != "" && reason != AttentionClosedAnswered {
		if it.Kind == AttentionAsk {
			a.endAskLocked(it.RequestID, askOutcome{Reason: reason})
		} else {
			a.endHoldLocked(it.RequestID, approvalOutcome{Reason: reason}, false)
		}
	}
	delete(a.items, id)
	delete(a.byKey, attentionItemKey(it))
	a.rev++
	closed := *it
	closed.Seq = a.rev
	closed.Closed = reason
	if fill != nil {
		fill(&closed)
	}
	a.publish(attentionEvent(AttentionClosed, closed))
	a.changedLocked()
	return true
}

// closeKeyLocked closes the item open under key, if any. A snoozed item
// under key is dropped for the same reason: what it was about is over.
func (a *attentionStore) closeKeyLocked(key, reason string) {
	if id, ok := a.byKey[key]; ok {
		a.closeLocked(id, reason)
	}
	if id, ok := a.snoozedKey[key]; ok {
		a.dropSnoozedLocked(id, reason)
	}
}

// evictOldestLocked makes room by closing the oldest item, finished items
// first, since they are the ones nothing is waiting on.
func (a *attentionStore) evictOldestLocked() {
	first := func(it, than *AttentionItem) bool {
		itDone, thanDone := it.Kind == AttentionFinished, than.Kind == AttentionFinished
		if itDone != thanDone {
			return itDone
		}
		return it.Since < than.Since
	}
	var victim *AttentionItem
	for _, it := range a.items {
		if victim == nil || first(it, victim) {
			victim = it
		}
	}
	if victim != nil {
		a.closeLocked(victim.ID, AttentionClosedEvicted)
	}
}

// noteSessionEvent folds one session event into the queue. It runs on the
// session's event sink with the session's state lock held, so it reads nothing
// but the event.
func (a *attentionStore) noteSessionEvent(sessionName string, ev SessionEvent) {
	if a == nil {
		return
	}
	switch ev.Type {
	case EventAgentState, eventAttentionDetail:
		a.noteAgentState(sessionName, ev)
	case EventWindowClosed:
		a.closeWindow(sessionName, ev.Window)
	case eventCompletionSeen:
		a.noteCompletionSeen(sessionName, ev.Window, ev.completionSeq)
	case eventPaneFocused:
		a.noteFocused(sessionName, ev.Window)
	}
}

// noteAgentState is the agent half of the queue: needs_input opens an approval
// or question item, errored opens an errored item, a finished turn opens a
// finished item, and leaving a state closes what it opened.
func (a *attentionStore) noteAgentState(sessionName string, ev SessionEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := ev.State
	base := AttentionItem{
		Session:   sessionName,
		Window:    ev.Window,
		Workspace: ev.hookWorkspace,
		Harness:   ev.hookHarness,
		Name:      attentionText(ev.hookTitle, attentionMaxSummary),
	}

	if state != AgentStateNeedsInput.Name() {
		a.closeKeyLocked(attentionKey(AttentionApproval, sessionName, ev.Window, 0), AttentionClosedResolved)
	}
	if state != AgentStateErrored.Name() {
		a.closeKeyLocked(attentionKey(AttentionErrored, sessionName, ev.Window, 0), AttentionClosedResolved)
	}
	// A new turn, a new question or a failure is newer news than a turn that
	// finished before it, so the finished item goes. An agent that finished
	// and then left the pane (none) did still finish, so that one stays.
	switch state {
	case AgentStateWorking.Name(), AgentStateNeedsInput.Name(), AgentStateErrored.Name():
		a.closeKeyLocked(attentionKey(AttentionFinished, sessionName, ev.Window, 0), AttentionClosedResolved)
	}
	// An agent at work in a restored pane is the pane in use again, by a
	// resume typed some other way or by a new agent, so the offer to resume
	// is spent. A pane settling to idle or none says nothing either way.
	switch state {
	case AgentStateWorking.Name(), AgentStateNeedsInput.Name():
		a.closeKeyLocked(attentionKey(AttentionResume, sessionName, ev.Window, 0), AttentionClosedResolved)
	}

	switch state {
	case AgentStateNeedsInput.Name():
		it := base
		it.Kind = AttentionQuestion
		if ev.hookKind == "approval" {
			it.Kind = AttentionApproval
			// An approval nobody holds is marked from the line its pane
			// reported. A held one keeps what its hold marked (upsertLocked).
			it.Risk = risk.Names(a.riskOfLine(ev.hookMessage, ev.hookRoot))
		}
		it.Summary = attentionText(ev.hookMessage, attentionMaxSummary)
		a.upsertLocked(it)
	case AgentStateErrored.Name():
		it := base
		it.Kind = AttentionErrored
		it.Summary = attentionText(ev.hookMessage, attentionMaxSummary)
		a.upsertLocked(it)
	}

	if ev.completionSeq > ev.prevCompletionSeq && agentStateFinishes(AgentState(state)) {
		it := base
		it.Kind = AttentionFinished
		it.Summary = attentionText(ev.hookMessage, attentionMaxSummary)
		it.CompletionSeq = ev.completionSeq
		it.Count = 1
		if cur := a.openOrSnoozedLocked(attentionKey(AttentionFinished, sessionName, ev.Window, 0)); cur != nil {
			it.Count = cur.Count + int(ev.completionSeq-ev.prevCompletionSeq)
		}
		a.upsertLocked(it)
	}
}

// noteCompletionSeen closes a pane's finished item once a client has focused
// the pane with the item's turn counted.
func (a *attentionStore) noteCompletionSeen(sessionName, window string, seen uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := attentionKey(AttentionFinished, sessionName, window, 0)
	// A snoozed finished turn the person has now looked at is seen too.
	if id, ok := a.snoozedKey[key]; ok && a.snoozed[id].CompletionSeq <= seen {
		a.dropSnoozedLocked(id, AttentionClosedSeen)
	}
	id, ok := a.byKey[key]
	if !ok || a.items[id].CompletionSeq > seen {
		return
	}
	a.closeLocked(id, AttentionClosedSeen)
}

// closeWindow closes the items about a pane that closed. Mail is kept: the
// message is still unread, and the ring still holds it.
func (a *attentionStore) closeWindow(sessionName, window string) {
	if window == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, kind := range []string{AttentionApproval, AttentionErrored, AttentionFinished, AttentionResume} {
		a.closeKeyLocked(attentionKey(kind, sessionName, window, 0), AttentionClosedWindow)
	}
	// An ask from the pane has nobody to hand the answer to any more.
	for _, id := range a.sortedIDsLocked() {
		if it := a.items[id]; it.Kind == AttentionAsk && it.Session == sessionName && it.Window == window {
			a.closeLocked(id, AttentionClosedWindow)
		}
	}
}

// openResume opens the resume item for a restored pane, or updates the one
// already open for it.
func (a *attentionStore) openResume(it AttentionItem) {
	if a == nil {
		return
	}
	it.Kind = AttentionResume
	a.mu.Lock()
	defer a.mu.Unlock()
	a.upsertLocked(it)
}

// heldPromptSummary is the summary of the item that says an agent the daemon
// started is not ready for its first prompt.
const heldPromptSummary = "waiting at a screen tuios does not recognise: look at the pane and answer it"

// openHeldPrompt opens a question item for a pane the daemon started an agent
// in, which has not been ready for its first prompt for a while (see
// waitAgentStart), and returns the summary it carries, which is what
// closeHeldPrompt closes by. It is a question because the pane needs the person
// to answer whatever is on its screen, most often a first-run choice. It shares
// the pane's blocking key, so the pane's next state change closes it like any
// question, and a needs_input the pane reaches replaces it with the real one.
func (a *attentionStore) openHeldPrompt(sessionName string, w WindowState) string {
	if a == nil {
		return ""
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
		Summary:   heldPromptSummary,
	})
	return heldPromptSummary
}

// closeHeldPrompt closes the item openHeldPrompt opened, if it still says what
// it said then. An item on the same key that says something else is the pane's
// own question, and it is left alone.
func (a *attentionStore) closeHeldPrompt(sessionName, window, summary string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := attentionKey(AttentionQuestion, sessionName, window, 0)
	if id, ok := a.snoozedKey[key]; ok {
		if it := a.snoozed[id]; it.Kind == AttentionQuestion && it.Summary == summary {
			a.dropSnoozedLocked(id, AttentionClosedResolved)
		}
	}
	id, ok := a.byKey[key]
	if !ok {
		return
	}
	if it := a.items[id]; it.Kind == AttentionQuestion && it.Summary == summary {
		a.closeLocked(id, AttentionClosedResolved)
	}
}

// closeResume closes a pane's resume item, if one is open.
func (a *attentionStore) closeResume(sessionName, window, reason string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeKeyLocked(attentionKey(AttentionResume, sessionName, window, 0), reason)
}

// closeSession closes every item in a session that ended.
func (a *attentionStore) closeSession(sessionName string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range a.sortedIDsLocked() {
		if a.items[id].Session == sessionName {
			a.closeLocked(id, AttentionClosedSession)
		}
	}
	for _, id := range a.snoozedIDsLocked() {
		if a.snoozed[id].Session == sessionName {
			a.dropSnoozedLocked(id, AttentionClosedSession)
		}
	}
}

// sortedIDsLocked returns the open ids in a stable order, so a sweep that
// closes several publishes them in the same order every time.
func (a *attentionStore) sortedIDsLocked() []string {
	ids := make([]string, 0, len(a.items))
	for id := range a.items {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(x, y string) int {
		return cmp.Compare(a.items[x].Seq, a.items[y].Seq)
	})
	return ids
}

// noteMail opens or updates the mail item for the thread a message to the
// person landed in.
func (a *attentionStore) noteMail(msg AgentMessage) {
	if a == nil || msg.Kind != agentMsgDirect || msg.To != AgentInboxHuman || msg.From == AgentInboxHuman {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	name := msg.FromLabel
	if msg.Origin == AgentOriginLink {
		host := msg.OriginHost
		if host == "" {
			host = "another machine"
		}
		name += " @ " + host
	}
	summary := strings.TrimSpace(msg.Subject)
	if summary == "" {
		summary, _, _ = strings.Cut(strings.TrimSpace(msg.Text), "\n")
	}
	it := AttentionItem{
		Kind:    AttentionMail,
		Session: msg.Session,
		Window:  msg.From,
		Name:    attentionText(name, attentionMaxSummary),
		Summary: attentionText(summary, attentionMaxSummary),
		Thread:  msg.ThreadID,
		Count:   1,
	}
	if msg.Held {
		it.HeldID = msg.ID
		it.HeldFor = attentionText(msg.HeldForLabel, attentionMaxSummary)
		if it.HeldFor == "" {
			it.HeldFor = "the session"
		}
	}
	if id, ok := a.byKey[attentionKey(AttentionMail, msg.Session, "", msg.ThreadID)]; ok {
		cur := a.items[id]
		it.Count = cur.Count + 1
		// The newest message names the thread's latest word, but the row is
		// still the thread the person has not read.
		if it.Window == "" {
			it.Window = cur.Window
		}
		// A held message stays offered for release until it is released,
		// whatever arrives after it in the thread.
		if it.HeldID == 0 {
			it.HeldID, it.HeldFor = cur.HeldID, cur.HeldFor
		}
	}
	a.upsertLocked(it)
}

// noteMailRead closes a thread's mail item once nothing in it is unread for
// the person.
func (a *attentionStore) noteMailRead(sessionName string, thread uint64) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeKeyLocked(attentionKey(AttentionMail, sessionName, "", thread), AttentionClosedRead)
}

// dismiss closes one item and returns what it was. A snoozed item can be
// dismissed too. The person can restore what they dismissed for a moment
// after (see attention_lifecycle.go), except mail waiting for another machine,
// which a dismiss discards, and a question put with ask-human, whose asker is
// told it was dismissed.
func (a *attentionStore) dismiss(id string) (AttentionItem, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if it, ok := a.hostItems[id]; ok {
		out := *it
		a.hideHostLocked(id)
		a.rememberUndoLocked(out, AttentionClosedDismissed)
		return out, true
	}
	if it, ok := a.hostSnoozed[id]; ok {
		out := *it
		out.SnoozedUntil = 0
		a.dropHostSnoozedLocked(id, AttentionClosedDismissed)
		a.hostHidden[id] = out.remoteSeq
		a.rememberUndoLocked(out, AttentionClosedDismissed)
		return out, true
	}
	if it, ok := a.snoozed[id]; ok {
		out := *it
		out.SnoozedUntil = 0
		a.dropSnoozedLocked(id, AttentionClosedDismissed)
		a.rememberUndoLocked(out, AttentionClosedDismissed)
		return out, true
	}
	it, ok := a.items[id]
	if !ok {
		return AttentionItem{}, false
	}
	out := *it
	a.closeLocked(id, AttentionClosedDismissed)
	a.rememberUndoLocked(out, AttentionClosedDismissed)
	return out, true
}

// attentionQuery narrows a listing.
type attentionQuery struct {
	session string
	kinds   map[string]bool
	// snoozed also lists the snoozed items, after the open ones. They are
	// not in the counts, which are what is waiting.
	snoozed bool
	// host is empty for every machine, federation.LocalHostName for this one,
	// or a linked host's name. A session with no host names a session on this
	// machine, which is what session meant before items from other machines
	// were listed.
	host string
}

// matchHost reports whether an item's machine is the one the query names.
func (q attentionQuery) matchHost(itemHost string) bool {
	switch {
	case q.host == "":
		return q.session == "" || itemHost == ""
	case q.host == localAttentionHost:
		return itemHost == ""
	default:
		return itemHost == q.host
	}
}

// list returns the open items in Inbox order, the counts per kind over the
// whole queue, and the hub seq the listing is current to. The seq is read under
// the store lock, which every attention event is published under, so every
// event at or below it is reflected in the listing and every later one is not.
func (a *attentionStore) list(q attentionQuery) ([]AttentionItem, map[string]int, uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	counts := make(map[string]int, len(AttentionKindNames))
	for _, k := range AttentionKindNames {
		counts[k] = 0
	}
	out := make([]AttentionItem, 0, len(a.items)+len(a.hostItems))
	add := func(it *AttentionItem) {
		counts[it.Kind]++
		if !q.matchHost(it.Host) {
			return
		}
		if q.session != "" && it.Session != q.session {
			return
		}
		if len(q.kinds) > 0 && !q.kinds[it.Kind] {
			return
		}
		out = append(out, *it)
	}
	for _, it := range a.items {
		add(it)
	}
	for _, it := range a.hostItems {
		add(it)
	}
	SortAttention(out)
	if q.snoozed {
		var sleeping []AttentionItem
		for _, m := range []map[string]*AttentionItem{a.snoozed, a.hostSnoozed} {
			for _, it := range m {
				if q.matchHost(it.Host) && (q.session == "" || it.Session == q.session) && (len(q.kinds) == 0 || q.kinds[it.Kind]) {
					sleeping = append(sleeping, *it)
				}
			}
		}
		SortAttention(sleeping)
		out = append(out, sleeping...)
	}
	var seq uint64
	if a.currentSeq != nil {
		seq = a.currentSeq()
	}
	return out, counts, seq
}

// attentionFile is the on-disk form of the queue.
type attentionFile struct {
	Version int             `json:"version"`
	NextID  uint64          `json:"next_id"`
	Rev     uint64          `json:"rev"`
	Items   []AttentionItem `json:"items"`
}

// changedLocked schedules a save. The caller holds mu.
func (a *attentionStore) changedLocked() {
	if a.path == "" || a.frozen {
		return
	}
	if a.saveTimer != nil {
		return
	}
	a.saveTimer = time.AfterFunc(attentionSaveDelay, func() {
		a.mu.Lock()
		a.saveTimer = nil
		frozen := a.frozen
		data := a.encodeLocked()
		a.mu.Unlock()
		if !frozen {
			writeAttentionFile(a.path, data)
		}
	})
}

// encodeLocked serialises the queue. The caller holds mu.
func (a *attentionStore) encodeLocked() []byte {
	f := attentionFile{Version: 1, NextID: a.nextID, Rev: a.rev, Items: make([]AttentionItem, 0, len(a.items)+len(a.snoozed))}
	for _, id := range a.sortedIDsLocked() {
		f.Items = append(f.Items, *a.items[id])
	}
	// A snoozed item is saved with snoozed_until, and load puts it back to
	// sleep. An older daemon reading the file lists it as open, which is
	// what the item was before the snooze.
	for _, id := range a.snoozedIDsLocked() {
		f.Items = append(f.Items, *a.snoozed[id])
	}
	data, err := json.Marshal(f)
	if err != nil {
		return nil
	}
	return data
}

// writeAttentionFile writes the queue atomically, readable by the owner only:
// summaries are what agents said, and nobody else on the machine has any
// business reading them.
func writeAttentionFile(path string, data []byte) {
	if data == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		LogError("Failed to create the attention directory: %v", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		LogError("Failed to save the attention queue: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		LogError("Failed to save the attention queue: %v", err)
	}
}

// saveNowAndFreeze writes the queue as it stands and stops saving from then
// on. It is the daemon's final save, taken before shutdown closes any pane.
func (a *attentionStore) saveNowAndFreeze() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.saveTimer != nil {
		a.saveTimer.Stop()
		a.saveTimer = nil
	}
	a.frozen = true
	path := a.path
	data := a.encodeLocked()
	a.mu.Unlock()
	if path != "" {
		writeAttentionFile(path, data)
	}
}

// load reads the queue a previous daemon saved and keeps what is still true:
// items in a session that came back, about a pane that came back, and never
// an approval or a question, since the prompt died with the process that
// painted it, nor mail, since its thread id means nothing to the new ring.
// live reports whether a session is live, and whether a window is
// in it when window is not empty. The ids and revision carry on from the file,
// so an id is never reused on this machine.
//
// The file read and the live checks run with no lock held. live reads session
// state under the session's stateMu, and the event sink takes the locks the
// other way round (stateMu, then mu), so holding mu across live could
// deadlock with a restored pane that exits while the Inbox loads. mu is taken
// only to merge the survivors in.
//
// Items opened before load keep their ids and win over a saved item with the
// same key. A saved item whose id one of them already holds gets a fresh id,
// so byKey and items always agree.
func (a *attentionStore) load(path string, live func(session, window string) bool) {
	var f attentionFile
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &f); err != nil || f.Version != 1 {
			LogError("Discarding the saved attention queue, it could not be read: %v", err)
			f = attentionFile{}
		}
	}
	kept := make([]AttentionItem, 0, len(f.Items))
	for _, it := range f.Items {
		switch it.Kind {
		case AttentionErrored, AttentionFinished:
			if it.ID == "" || !live(it.Session, it.Window) {
				continue
			}
		default:
			continue
		}
		it.Closed = ""
		kept = append(kept, it)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.path = path
	opened := len(a.items) > 0
	a.nextID = max(a.nextID, f.NextID)
	a.rev = max(a.rev, f.Rev)
	for _, it := range kept {
		if len(a.items)+len(a.snoozed) >= attentionMaxItems {
			break
		}
		key := attentionItemKey(&it)
		if _, dup := a.byKey[key]; dup {
			continue
		}
		if _, dup := a.snoozedKey[key]; dup {
			continue
		}
		item := it
		_, taken := a.items[item.ID]
		if _, sleeping := a.snoozed[item.ID]; taken || sleeping {
			a.nextID++
			item.ID = strconv.FormatUint(a.nextID, 10)
		}
		// A snooze survives the restart. Its time may have passed while
		// the daemon was down, and armWakeLocked below wakes it then.
		if item.SnoozedUntil != 0 {
			a.putSnoozedLocked(&item)
			continue
		}
		a.items[item.ID] = &item
		a.byKey[key] = item.ID
	}
	a.armWakeLocked()
	// An item opened before load had no path to be saved to.
	if opened {
		a.changedLocked()
	}
}

// renameSession moves every item of the session named old to newName, open
// and snoozed alike, so a rename leaves the queue pointing at the session it
// was about. The keys are worked out again because they hold the name.
func (a *attentionStore) renameSession(old, newName string) {
	if a == nil || old == newName {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	moved := false
	for id, it := range a.items {
		if it.Session != old {
			continue
		}
		if a.byKey[attentionItemKey(it)] == id {
			delete(a.byKey, attentionItemKey(it))
		}
		it.Session = newName
		a.byKey[attentionItemKey(it)] = id
		moved = true
	}
	for id, it := range a.snoozed {
		if it.Session != old {
			continue
		}
		if a.snoozedKey[attentionItemKey(it)] == id {
			delete(a.snoozedKey, attentionItemKey(it))
		}
		it.Session = newName
		a.snoozedKey[attentionItemKey(it)] = id
		moved = true
	}
	// The held questions and approvals are matched by session name when
	// they are answered, so they move too.
	for _, h := range a.asks {
		if h.session == old {
			h.session = newName
		}
	}
	for _, h := range a.holds {
		if h.session == old {
			h.session = newName
		}
	}
	if moved {
		a.rev++
		a.changedLocked()
	}
}
