package session

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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

// Attention kinds. They are wire values: the kind field of an item and the
// kind filter of list-attention.
const (
	AttentionApproval = "approval"
	AttentionQuestion = "question"
	AttentionMail     = "mail"
	AttentionErrored  = "errored"
	AttentionFinished = "finished"
	// AttentionResume is a restored pane whose agent conversation can be
	// resumed. See agent_resume.go.
	AttentionResume = "resume"
)

// AttentionKindNames lists the kinds in the order the Inbox groups them: what
// blocks an agent first, then what an agent said, then what went wrong, then
// what a restart left to bring back, then what finished. list-attention sorts
// by it.
var AttentionKindNames = []string{AttentionApproval, AttentionQuestion, AttentionMail, AttentionErrored, AttentionResume, AttentionFinished}

// Close reasons an attention event carries on its closing action.
const (
	// AttentionClosedResolved: the fact behind the item stopped being true,
	// such as the pane leaving needs_input.
	AttentionClosedResolved = "resolved"
	// AttentionClosedSeen: an attached client focused the pane.
	AttentionClosedSeen = "seen"
	// AttentionClosedRead: the person's mail in the thread was read.
	AttentionClosedRead = "read"
	// AttentionClosedDismissed: a verified human dismissed it.
	AttentionClosedDismissed = "dismissed"
	// AttentionClosedWindow: the pane the item was about closed.
	AttentionClosedWindow = "window_closed"
	// AttentionClosedSession: the session the item was in ended.
	AttentionClosedSession = "session_closed"
	// AttentionClosedEvicted: the queue was over its cap and this was the oldest.
	AttentionClosedEvicted = "evicted"
	// AttentionClosedAnswered: the person answered a held approval from the
	// Inbox. The closing item carries the answer and who gave it.
	AttentionClosedAnswered = "answered"
	// AttentionClosedHostRemoved: the item came from a linked host that was
	// taken out of the [hosts] table.
	AttentionClosedHostRemoved = "host_removed"
)

// Actions an attention event carries.
const (
	AttentionOpened  = "open"
	AttentionUpdated = "update"
	AttentionClosed  = "close"
)

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

// AttentionItem is one thing waiting for the person.
type AttentionItem struct {
	// ID is stable for the item's life and unique across daemon restarts on
	// this machine. It is what dismiss-attention takes.
	ID string `json:"id"`
	// Kind is one of AttentionKindNames.
	Kind string `json:"kind"`
	// Host is the machine the item is on, empty for this one. It is here so a
	// hub can merge items from linked hosts into the same list; this daemon
	// only produces its own.
	Host string `json:"host,omitempty"`
	// Session is the session name, the one every verb addresses it by.
	Session string `json:"session"`
	// Window is the pane the item is about: the blocked or finished agent, or
	// the pane that sent the mail. Empty when there is none.
	Window string `json:"window,omitempty"`
	// Workspace is the pane's workspace when the item last changed.
	Workspace int `json:"workspace,omitempty"`
	// Harness is the harness id, when one is known.
	Harness string `json:"harness,omitempty"`
	// Name is what to call the pane: its name, else its title, else the
	// sender's label for mail.
	Name string `json:"name,omitempty"`
	// Summary is one line: the question a blocked agent asked, the error, the
	// note a finished turn carried, or the mail's subject. Control characters
	// are removed, likely secrets are masked and it is cut to 160 bytes.
	Summary string `json:"summary,omitempty"`
	// Options are the answers reply-approval takes for this item, set only
	// while RequestID is: once, always and deny, or the subset the harness can
	// honour. See approvals.go.
	Options []string `json:"options,omitempty"`
	// RequestID is set while a harness hook is holding its permission prompt
	// for an answer from the Inbox, and names that request to reply-approval.
	// It is cleared when the hold ends, whatever ended it, and the item then
	// stays open as long as the pane is still blocked.
	RequestID string `json:"request_id,omitempty"`
	// AlwaysScope is what answering always allows from now on, one rule per
	// line, set only while RequestID is and Options holds always. A client
	// shows it beside the key; the daemon refuses to offer always without it.
	AlwaysScope []string `json:"always_scope,omitempty"`
	// Expires is when the hold ends, in unix nanoseconds, set with RequestID.
	Expires int64 `json:"expires,omitempty"`
	// Answer and AnsweredBy are set only on the item a close event with reason
	// answered carries: the decision the person made and the client they made
	// it from, so every other client can say it was answered elsewhere.
	Answer     string `json:"answer,omitempty"`
	AnsweredBy string `json:"answered_by,omitempty"`
	// Since is when the item started waiting, in unix nanoseconds. An update
	// keeps it, so the wait time an Inbox row shows is the whole wait.
	Since int64 `json:"since"`
	// Seq is the queue's revision when the item last changed. It only ever
	// goes up, across restarts too.
	Seq uint64 `json:"seq"`
	// Thread is the mail thread, for a mail item.
	Thread uint64 `json:"thread,omitempty"`
	// HeldID is set on a mail item whose newest message is mail from another
	// machine held for the person (hold_mail): the message id
	// release-agent-message takes. HeldFor is the name of the window it was
	// addressed to, empty for a notice to the session.
	HeldID  uint64 `json:"held_id,omitempty"`
	HeldFor string `json:"held_for,omitempty"`
	// Count is how many unread messages a mail item stands for, or how many
	// turns a finished item stands for.
	Count int `json:"count,omitempty"`
	// CompletionSeq is the pane's completion_seq when a finished item last
	// changed. Focusing the pane at that count or later closes it.
	CompletionSeq uint64 `json:"completion_seq,omitempty"`
	// Closed is the close reason, set only on the item an attention event
	// with action close carries.
	Closed string `json:"closed,omitempty"`
	// Stale is set on an item from another machine whose link is down. The
	// item is what that machine said last, and nobody here can check it now.
	// SeenAt is when this daemon last heard from that machine, in unix
	// nanoseconds. Both are empty for an item of this machine.
	Stale  bool  `json:"stale,omitempty"`
	SeenAt int64 `json:"seen_at,omitempty"`

	// remoteSeq is the Seq the machine the item came from gave it, for an
	// item mirrored from a linked host. It orders that machine's changes,
	// which can reach this daemon out of order around a relisting.
	remoteSeq uint64
}

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
}

func newAttentionStore(publish func(streamEvent), currentSeq func() uint64) *attentionStore {
	return &attentionStore{
		items:      make(map[string]*AttentionItem),
		byKey:      make(map[string]string),
		hostItems:  make(map[string]*AttentionItem),
		hostHidden: make(map[string]uint64),
		publish:    publish,
		currentSeq: currentSeq,
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
	case AttentionApproval, AttentionQuestion:
		class = "block"
	case AttentionMail:
		return "mail\x00" + session + "\x00" + strconv.FormatUint(thread, 10)
	}
	return class + "\x00" + session + "\x00" + window
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
	key := attentionKey(next.Kind, next.Session, next.Window, next.Thread)
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
		slices.Equal(a.Options, b.Options) && slices.Equal(a.AlwaysScope, b.AlwaysScope)
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
		a.endHoldLocked(it.RequestID, approvalOutcome{Reason: reason}, false)
	}
	delete(a.items, id)
	delete(a.byKey, attentionKey(it.Kind, it.Session, it.Window, it.Thread))
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

// closeKeyLocked closes the item open under key, if any.
func (a *attentionStore) closeKeyLocked(key, reason string) {
	if id, ok := a.byKey[key]; ok {
		a.closeLocked(id, reason)
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
		if id, ok := a.byKey[attentionKey(AttentionFinished, sessionName, ev.Window, 0)]; ok {
			it.Count = a.items[id].Count + int(ev.completionSeq-ev.prevCompletionSeq)
		}
		a.upsertLocked(it)
	}
}

// noteCompletionSeen closes a pane's finished item once a client has focused
// the pane with the item's turn counted.
func (a *attentionStore) noteCompletionSeen(sessionName, window string, seen uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, ok := a.byKey[attentionKey(AttentionFinished, sessionName, window, 0)]
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

// dismiss closes one item and returns what it was.
func (a *attentionStore) dismiss(id string) (AttentionItem, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if it, ok := a.hostItems[id]; ok {
		out := *it
		a.hideHostLocked(id)
		return out, true
	}
	it, ok := a.items[id]
	if !ok {
		return AttentionItem{}, false
	}
	out := *it
	a.closeLocked(id, AttentionClosedDismissed)
	return out, true
}

// attentionQuery narrows a listing.
type attentionQuery struct {
	session string
	kinds   map[string]bool
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

// localAttentionHost is the host filter that names this machine. It is the
// name the listings and the rail give it.
const localAttentionHost = "local"

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
	var seq uint64
	if a.currentSeq != nil {
		seq = a.currentSeq()
	}
	return out, counts, seq
}

// SortAttention puts items in Inbox order: by kind in AttentionKindNames
// order, then oldest first, then by id so the order is total.
func SortAttention(items []AttentionItem) {
	slices.SortFunc(items, func(x, y AttentionItem) int {
		if c := cmp.Compare(AttentionKindRank(x.Kind), AttentionKindRank(y.Kind)); c != 0 {
			return c
		}
		if c := cmp.Compare(x.Since, y.Since); c != 0 {
			return c
		}
		return cmp.Compare(x.Seq, y.Seq)
	})
}

// AttentionKindRank is a kind's place in the Inbox order. An unknown kind
// sorts last.
func AttentionKindRank(kind string) int {
	if i := slices.Index(AttentionKindNames, kind); i >= 0 {
		return i
	}
	return len(AttentionKindNames)
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
	f := attentionFile{Version: 1, NextID: a.nextID, Rev: a.rev, Items: make([]AttentionItem, 0, len(a.items))}
	for _, id := range a.sortedIDsLocked() {
		f.Items = append(f.Items, *a.items[id])
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
		if len(a.items) >= attentionMaxItems {
			break
		}
		key := attentionKey(it.Kind, it.Session, it.Window, it.Thread)
		if _, dup := a.byKey[key]; dup {
			continue
		}
		item := it
		if _, taken := a.items[item.ID]; taken {
			a.nextID++
			item.ID = strconv.FormatUint(a.nextID, 10)
		}
		a.items[item.ID] = &item
		a.byKey[key] = item.ID
	}
	// An item opened before load had no path to be saved to.
	if opened {
		a.changedLocked()
	}
}

// attentionSecret matches the shapes a command line leaks a credential in:
// a key=value or key: value whose key names a secret, and an Authorization
// bearer token. It is a net for the common case, not a guarantee, which is why
// the summary is also kept short.
var attentionSecret = regexp.MustCompile(`(?i)\b((?:[a-z0-9_]*(?:token|secret|password|passwd|api[_-]?key|access[_-]?key|private[_-]?key|credential)s?)\s*[=:]\s*|bearer\s+)("[^"]*"|'[^']*'|[^\s"']+)`)

// attentionSecretWords are the words attentionSecret keys on, lower case. A
// summary with none of them cannot match, and most summaries have none, so the
// regular expression only runs on the ones that might.
var attentionSecretWords = []string{"token", "secret", "passw", "key", "credential", "bearer"}

// attentionMaySecret reports whether s holds any of attentionSecretWords.
func attentionMaySecret(s string) bool {
	lower := strings.ToLower(s)
	for _, w := range attentionSecretWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// attentionText is text an agent reported, made safe to show and to keep: one
// line, no control characters, likely secrets masked, at most limit bytes.
func attentionText(s string, limit int) string {
	var b strings.Builder
	b.Grow(min(len(s), limit+8))
	space := false
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t' || r == '\r' || r == ' ':
			space = true
			continue
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
		if b.Len() > limit*4 {
			break
		}
	}
	out := b.String()
	if attentionMaySecret(out) {
		out = attentionSecret.ReplaceAllString(out, "${1}[redacted]")
	}
	if len(out) <= limit {
		return out
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return strings.TrimSpace(out[:cut])
}
