package session

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// This file holds the agent mailbox: a bounded, in-memory, per-session ring of
// messages agents leave for each other, plus the loop guards that keep two
// agents holding each other's address from running away.
//
// The daemon already has an event hub, so the first question was whether it
// already did this. It does not, and the reason is worth writing down: the hub
// delivers only to connections subscribed at the instant of publish and keeps no
// backfill, while an agent drives tuios through one-shot CLI calls and is almost
// never subscribed. Store-and-forward is the single thing the hub cannot do.
// Everything around it, delivery, filtering and blocking reads, is still the
// hub's, which is why this is a ring and four verbs rather than a broker.
//
// What it deliberately is not: durable. A message dies with the daemon. A
// restored session brings back panes whose shells are new, so a queued
// instruction surviving into one would be addressed to an agent that no longer
// exists and no longer holds the context the instruction assumed. Delivering it
// then is a bug, not a feature.

const (
	// agentMsgDirect is a message addressed to one window's inbox. It has a
	// recipient and a read state.
	agentMsgDirect = "message"
	// agentMsgNotice is addressed to the session rather than to anyone. It is
	// the notification half of the surface: everyone can read it, nobody owns
	// it, and it is never unread for a particular reader. One store and two
	// addressing modes, rather than two subsystems that drift apart.
	agentMsgNotice = "notice"
)

// Caps. Every one of these exists because an unread queue with no bound is a
// memory leak with a friendly name.
const (
	// agentMsgMaxText bounds one message body: bigger than a paragraph, smaller
	// than a file. A message that wants to carry a file attaches it instead.
	agentMsgMaxText = 8 << 10
	// agentMsgMaxSubject bounds the one-line summary a reader scans.
	agentMsgMaxSubject = 120
	// agentMsgMaxAttachments bounds how many references one message carries.
	agentMsgMaxAttachments = 8
	// agentMailboxMaxMessages bounds a session's ring by count.
	agentMailboxMaxMessages = 256
	// agentMailboxMaxBytes bounds the same ring by the text it holds, so a full
	// ring of maximum-sized messages still cannot cost more than this.
	agentMailboxMaxBytes = 512 << 10
	// agentReadDefaultLimit is how many messages a read answers with when the
	// caller names no limit.
	agentReadDefaultLimit = 20
	// agentSendBurst is how many sends one sender may make back to back.
	agentSendBurst = 10
	// agentSendPerMinute is the sustained rate one sender's bucket refills at.
	agentSendPerMinute = 30
)

var (
	errAttachNotAbsolute = errors.New("attachment path must be absolute")
	errAttachIsDirectory = errors.New("attachment path is a directory")
)

// AgentAttachment is a reference to something a message points at, never the
// bytes themselves. The queue holds a path and the producer keeps the file.
//
// Copying is the expensive part: a megabyte image sitting in an in-memory ring
// nobody reads is the unbounded-growth problem with a bigger constant. Kitty's
// graphics protocol reached the same conclusion, which is why its t=f and t=s
// media pass a path or a shared-memory name instead of the pixels.
//
// The consequence is stated rather than hidden: the file belongs to the
// producer, the queue never copies it, and a reader that comes late may find it
// gone. Missing records that, resolved at read time rather than trusted from
// send time.
type AgentAttachment struct {
	// Kind is the closed set a reader switches on: image or file. It is small on
	// purpose. Extending it later is adding a name; an untyped blob every reader
	// has to sniff can never be narrowed again.
	Kind string `json:"kind"`
	// Path is absolute and host-local. Sender and reader are both processes on
	// the daemon's host, so a path means the same thing to both.
	Path      string `json:"path"`
	MediaType string `json:"media_type,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
}

// AgentMessage is one entry in a session's ring.
type AgentMessage struct {
	ID      uint64 `json:"id"`
	Kind    string `json:"kind"`
	Session string `json:"session"`
	// From is the window id the sender claimed. It is a claim rather than an
	// identity: the daemon's socket carries no per-pane credential, so any
	// process that can open it can say it is any window. The loop guards below
	// are built to stop an accident, not an adversary, and the skill says so.
	From      string `json:"from,omitempty"`
	FromLabel string `json:"from_label,omitempty"`
	// To is the recipient window id, empty for a notice.
	To      string `json:"to,omitempty"`
	ToLabel string `json:"to_label,omitempty"`
	Subject string `json:"subject,omitempty"`
	Text    string `json:"text"`

	Attachments []AgentAttachment `json:"attachments,omitempty"`
	SentAt      int64             `json:"sent_at"`
	// ReadAt is zero while the message is unread. Reading marks rather than
	// consumes: a consumed message leaves nothing behind for a human to look at
	// afterwards, and the cap already bounds the ring.
	ReadAt int64 `json:"read_at,omitempty"`
	// Undeliverable is resolved at read time and means the recipient window is
	// gone. A message is never re-homed onto a new pane that happens to carry
	// the old one's name, because that pane is a different agent holding
	// different context.
	Undeliverable bool `json:"undeliverable,omitempty"`
	// WasUnread means this read is the first one to see the message. It exists
	// because ReadAt cannot answer that question on the call that sets it: a
	// marking read stamps ReadAt before returning, so every message it hands
	// back looks read, and a reader could not tell the one that just arrived
	// from the twenty it had already seen.
	WasUnread bool `json:"was_unread,omitempty"`
}

// agentBus is the daemon's whole cross-agent surface: the per-session rings and
// the in-flight ask graph. One field on the daemon rather than two.
type agentBus struct {
	mu     sync.Mutex
	boxes  map[string]*agentMailbox
	nextID uint64

	// asks records the ask edges open right now, so a cycle can be refused
	// before it is created rather than noticed after it has spun.
	asks map[string]map[string]int
}

// agentMailbox is one session's ring.
type agentMailbox struct {
	msgs    []*AgentMessage
	bytes   int
	evicted uint64
	// buckets is one token bucket per claimed sender.
	buckets map[string]*sendBucket
}

type sendBucket struct {
	tokens float64
	last   time.Time
}

func newAgentBus() *agentBus {
	return &agentBus{
		boxes: map[string]*agentMailbox{},
		asks:  map[string]map[string]int{},
	}
}

// box returns the session's ring, creating it on first use. Callers hold b.mu.
func (b *agentBus) box(session string) *agentMailbox {
	mb := b.boxes[session]
	if mb == nil {
		mb = &agentMailbox{buckets: map[string]*sendBucket{}}
		b.boxes[session] = mb
	}
	return mb
}

// forget drops a session's ring. A session that is gone has no agents left to
// address, so keeping its mail would be keeping a leak.
func (b *agentBus) forget(session string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	delete(b.boxes, session)
	b.mu.Unlock()
}

// allow charges one send against the sender's token bucket, reporting whether
// there was a token to charge. An unclaimed sender shares one bucket keyed by
// the empty string, which is the right default: an anonymous flood is a flood.
func (mb *agentMailbox) allow(sender string, now time.Time) bool {
	bk := mb.buckets[sender]
	if bk == nil {
		bk = &sendBucket{tokens: agentSendBurst, last: now}
		mb.buckets[sender] = bk
	}
	if refill := now.Sub(bk.last).Minutes() * agentSendPerMinute; refill > 0 {
		bk.tokens = min(bk.tokens+refill, agentSendBurst)
		bk.last = now
	}
	if bk.tokens < 1 {
		return false
	}
	bk.tokens--
	return true
}

// append adds m to the ring and evicts from the oldest end until both caps
// hold. Evictions are counted rather than silent: a reader is told how many
// messages it will never see, the discipline the hub's gap marker follows.
func (mb *agentMailbox) append(m *AgentMessage) {
	mb.msgs = append(mb.msgs, m)
	mb.bytes += len(m.Text)
	for len(mb.msgs) > agentMailboxMaxMessages || (mb.bytes > agentMailboxMaxBytes && len(mb.msgs) > 1) {
		mb.bytes -= len(mb.msgs[0].Text)
		mb.msgs = mb.msgs[1:]
		mb.evicted++
	}
}

// send records a message and returns the stored copy, so a caller can report the
// id without reaching back into the ring.
func (b *agentBus) send(session string, m AgentMessage) AgentMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	m.ID = b.nextID
	m.Session = session
	m.SentAt = time.Now().UnixNano()
	stored := &m
	b.box(session).append(stored)
	return *stored
}

// checkRate reports whether sender may send into session right now.
func (b *agentBus) checkRate(session, sender string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.box(session).allow(sender, time.Now())
}

// readQuery selects which messages a read returns.
type readQuery struct {
	// inbox is the recipient window id whose mail is wanted. Empty reads
	// everything in the session and then marks nothing: a read that did not name
	// an inbox must not consume someone else's mail as a side effect.
	inbox string
	// unreadOnly restricts to directed messages nobody has read.
	unreadOnly bool
	// notices includes session-wide notices in an inbox read. They have no
	// recipient, so an inbox read leaves them out unless this is set.
	notices bool
	// peek reads without marking anything read.
	peek bool
	// limit bounds the answer to the newest n. Zero means the default.
	limit int
	// live reports whether a window id still exists, for the undeliverable flag.
	live func(windowID string) bool
}

// readResult is one read of a session's ring.
type readResult struct {
	Messages []AgentMessage
	Unread   int
	Total    int
	Evicted  uint64
}

// read answers a query against a session's ring, marking the returned directed
// messages read unless the query peeks or named no inbox.
//
// Attachments are resolved after the lock is dropped. Stat is a syscall against
// whatever filesystem the sender chose, and holding the bus through it would
// let one slow path block every other agent's mail.
func (b *agentBus) read(session string, q readQuery) readResult {
	res := b.collect(session, q)
	for i := range res.Messages {
		res.Messages[i].Attachments = resolveAttachments(res.Messages[i].Attachments)
	}
	return res
}

// collect is read's critical section: pick the matching messages, copy them out,
// and mark what this read consumed.
func (b *agentBus) collect(session string, q readQuery) readResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	mb := b.box(session)

	now := time.Now().UnixNano()
	res := readResult{Evicted: mb.evicted}

	var picked []*AgentMessage
	for _, m := range mb.msgs {
		if m.Kind == agentMsgNotice {
			if q.unreadOnly || (q.inbox != "" && !q.notices) {
				continue
			}
		} else {
			if q.inbox != "" && m.To != q.inbox {
				continue
			}
			if q.unreadOnly && m.ReadAt != 0 {
				continue
			}
		}
		picked = append(picked, m)
	}
	res.Total = len(picked)

	limit := q.limit
	if limit <= 0 {
		limit = agentReadDefaultLimit
	}
	if len(picked) > limit {
		picked = picked[len(picked)-limit:]
	}

	res.Messages = make([]AgentMessage, 0, len(picked))
	for _, m := range picked {
		out := *m
		if out.Kind == agentMsgDirect && out.To != "" && q.live != nil && !q.live(out.To) {
			out.Undeliverable = true
		}
		if out.Kind == agentMsgDirect && out.ReadAt == 0 {
			res.Unread++
			out.WasUnread = true
			if !q.peek && q.inbox != "" && m.To == q.inbox {
				m.ReadAt = now
				out.ReadAt = now
			}
		}
		res.Messages = append(res.Messages, out)
	}
	return res
}

// resolveAttachments copies a message's references and stats each one, so the
// answer says whether the file the reference names is still there. The copy
// matters: the stored message must not gain a Missing flag that was only true
// for one read.
func resolveAttachments(in []AgentAttachment) []AgentAttachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]AgentAttachment, len(in))
	copy(out, in)
	for i := range out {
		if _, err := os.Stat(out[i].Path); err != nil {
			out[i].Missing = true
		}
	}
	return out
}

// unreadCounts returns the unread count for every inbox in a session in one
// pass, so listing n agents does not take n locks.
func (b *agentBus) unreadCounts(session string) map[string]int {
	counts := map[string]int{}
	if b == nil {
		return counts
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.box(session).msgs {
		if m.Kind == agentMsgDirect && m.ReadAt == 0 && m.To != "" {
			counts[m.To]++
		}
	}
	return counts
}

// highestID is the id of the newest message the bus has recorded. A wait that
// names no inbox uses it as a baseline, so it matches what arrives after it
// started rather than returning at once because the ring is not empty.
func (b *agentBus) highestID() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nextID
}

// newerThan returns the oldest message in the session's ring past baseline.
func (b *agentBus) newerThan(session string, baseline uint64) (AgentMessage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.box(session).msgs {
		if m.ID > baseline {
			return *m, true
		}
	}
	return AgentMessage{}, false
}

// firstUnread returns the oldest unread message for one inbox.
func (b *agentBus) firstUnread(session, inbox string) (AgentMessage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.box(session).msgs {
		if m.Kind == agentMsgDirect && m.To == inbox && m.ReadAt == 0 {
			return *m, true
		}
	}
	return AgentMessage{}, false
}

// openAsk records an in-flight ask edge from -> to, refusing it when it would
// close a cycle. Two agents holding each other's address is the failure mode
// this feature invents, so the graph is checked before the edge is added rather
// than after the loop has run.
//
// The graph is keyed on claimed window ids, so it is exactly as trustworthy as
// the claim. That is enough for what it is for: an orchestrator wiring A to B to
// A by mistake is stopped, and nothing here pretends to stop a caller that lies.
func (b *agentBus) openAsk(from, to string) bool {
	if from == "" || to == "" {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reaches(to, from) {
		return false
	}
	if b.asks[from] == nil {
		b.asks[from] = map[string]int{}
	}
	b.asks[from][to]++
	return true
}

// closeAsk releases an edge openAsk took.
func (b *agentBus) closeAsk(from, to string) {
	if from == "" || to == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.asks[from] == nil {
		return
	}
	b.asks[from][to]--
	if b.asks[from][to] <= 0 {
		delete(b.asks[from], to)
	}
	if len(b.asks[from]) == 0 {
		delete(b.asks, from)
	}
}

// reaches reports whether src can get to dst by following open ask edges.
// Callers hold b.mu. The graph holds one node per agent currently blocked in an
// ask, so it is tiny and a plain walk is the right shape.
func (b *agentBus) reaches(src, dst string) bool {
	if src == dst {
		return true
	}
	seen := map[string]bool{src: true}
	queue := []string{src}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for next := range b.asks[n] {
			if next == dst {
				return true
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// openAskEdges renders every open edge, for the message on a refusal. A caller
// told only that something would loop has to go and find the loop itself.
func (b *agentBus) openAskEdges() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for src, dsts := range b.asks {
		for dst := range dsts {
			out = append(out, shortWindowID(src)+" -> "+shortWindowID(dst))
		}
	}
	sort.Strings(out)
	return out
}

// shortWindowID renders a window id the way list-windows does, so an error
// message and a listing name the same pane the same way.
func shortWindowID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// classifyAttachment fills in the kind, media type and size of a referenced
// file, refusing anything that is not an existing absolute path. Refusing at
// send time is the point: a reference the reader cannot resolve is a message
// that looks delivered and is not.
func classifyAttachment(path string) (AgentAttachment, error) {
	if !filepath.IsAbs(path) {
		return AgentAttachment{}, errAttachNotAbsolute
	}
	info, err := os.Stat(path)
	if err != nil {
		return AgentAttachment{}, err
	}
	if info.IsDir() {
		return AgentAttachment{}, errAttachIsDirectory
	}
	media := mediaTypeFor(path)
	kind := "file"
	if strings.HasPrefix(media, "image/") {
		kind = "image"
	}
	return AgentAttachment{Kind: kind, Path: path, MediaType: media, Bytes: info.Size()}, nil
}

// mediaTypeFor names an attachment's type from its extension. The stdlib table
// is deliberately not consulted: it varies with the host's mime.types file, and
// an attachment that classified as an image on one machine and a file on another
// would make the closed kind set a lie.
func mediaTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	case ".txt", ".log", ".md":
		return "text/plain"
	case ".json":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}
