package session

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	agentMsgDirect = "message"
	agentMsgNotice = "notice"
	agentMsgAsk    = "ask"
)

// AgentInboxHuman is the reserved inbox id of the person at the attached
// client. It is the one address in the mailbox that is not a window: a message
// to it is read from the client's mail overlay, and a reply from there is sent
// from it. It cannot be asked, because there is no keyboard behind it.
const AgentInboxHuman = "human"

const (
	agentMsgMaxText         = 8 << 10
	agentMsgMaxSubject      = 120
	agentMsgMaxAttachments  = 8
	agentMailboxMaxMessages = 256
	agentMailboxMaxBytes    = 512 << 10
	agentReadDefaultLimit   = 20
	agentSendBurst          = 10
	agentSendPerMinute      = 30
	agentMaxBuckets         = 256
	agentOverflowBucket     = queueByLinkPrefix + "*"
	agentLinkMaxQueued      = 32
	agentMsgMaxHostName     = 64
)

// AgentOriginLink is the Origin of a message that arrived over a link. It is
// the only value Origin takes: a message from this machine has none.
const AgentOriginLink = "link"

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
	// Stashed says the daemon owns this file rather than the sender: it was put
	// in the session's stash, so it is there until the session ends. It is
	// resolved at send time, when the daemon is already looking at the path, and
	// it exists so a reader can tell the two kinds of reference apart. A sender-
	// owned path may be gone by the time it is read; a stashed one may not.
	Stashed bool `json:"stashed,omitempty"`
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

	// ReplyTo is the id of the message this one answers, zero when it answers
	// nothing. It is what "acked" means between two agents: no transport
	// receipt says anything about whether the other side understood, and a
	// reply does.
	ReplyTo uint64 `json:"reply_to,omitempty"`
	// ThreadID is the id of the message the thread started from, and a message
	// that starts one carries its own id here. It is resolved once, at send
	// time, rather than walked from ReplyTo at read time: the ring is bounded,
	// so a walk would stop finding the root the moment the root aged out, and
	// the same thread would answer to two different ids depending on when it
	// was read.
	//
	// It is not a second namespace. A thread id is a message id, so the number
	// send prints is the number a filter takes.
	ThreadID uint64 `json:"thread_id"`
	// ReplyToMissing records that ReplyTo named a message the ring no longer
	// held when this one was threaded, so the thread was rooted at the parent's
	// own id instead of the parent's thread. Refusing the reply would be worse
	// (a bounded ring forgets, and a reply to something it forgot is still a
	// reply), and silently starting a fresh thread would be worse still: it
	// would lose the one fact the reader wanted. This says which happened.
	ReplyToMissing bool `json:"reply_to_missing,omitempty"`

	Attachments []AgentAttachment `json:"attachments,omitempty"`
	SentAt      int64             `json:"sent_at"`
	// ReadAt is zero while the message is unread. Reading marks rather than
	// consumes: a consumed message leaves nothing behind for a human to look at
	// afterwards, and the cap already bounds the ring.
	ReadAt int64 `json:"read_at,omitempty"`
	// SeenAt is zero until the recipient looked at the message without
	// reading it: a peek that names the recipient's inbox stamps it. Seen sits
	// between unread and read, so the sender and the person can tell a message
	// was looked at. ReadAt stays zero while a message is only seen, so a peer
	// that does not know this field counts the message as unread, which is
	// still true. A marking read sets ReadAt and leaves SeenAt alone.
	SeenAt int64 `json:"seen_at,omitempty"`
	// SettledBy is set on an ask record only: which signal ended the wait, as
	// ask-agent reported it ("agent-state", "idle", "timeout", ...).
	SettledBy string `json:"settled_by,omitempty"`
	// Origin is AgentOriginLink when the send arrived on the daemon's link
	// socket, which is to say from another machine, and empty when it came
	// from a process on this one. It is set by the daemon from the connection
	// the send arrived on and never from anything in the request, so a
	// message cannot claim to be local. OriginHost is what the sender said
	// its machine is called: a claim, bounded and shown as one. A reader,
	// human or agent, is told both, because who wrote a message is the
	// first thing that decides how much of it to believe.
	Origin     string `json:"origin,omitempty"`
	OriginHost string `json:"origin_host,omitempty"`
	// OriginLink is the name this daemon's link policy knows the sending
	// machine by, set from the connection like Origin. It is what
	// agent-message-receipts checks, so a machine is told about the mail it
	// sent itself and no other machine's.
	OriginLink string `json:"origin_link,omitempty"`
	// VerifiedHuman and ClaimedHuman say how far a message from human can be
	// believed, and at most one is set. VerifiedHuman means the send carried
	// the nonce of a client attached to this session at the time, which is the
	// mail overlay's reply path. ClaimedHuman means the send said from=human
	// and carried no such nonce: anything that can open the socket can do that,
	// so it is a claim. Both are set by the daemon at send time and never taken
	// from the request. Both false on a message not from human, and on one
	// stored by an older daemon, which verified nothing.
	VerifiedHuman bool `json:"verified_human,omitempty"`
	// FromProven says the daemon placed the sending connection in the pane
	// From names, rather than taking From as the sender's claim. Only a proven
	// sender can be a reader's lead (FromLead).
	FromProven bool `json:"from_proven,omitempty"`
	// FromLead is set on a read, never stored: From is the pane that started
	// the reader (start-agent's parent link), that pane is still live, and the
	// send was proven to come from it. Mail from your lead directs your work
	// within the task you were started for; it grants nothing.
	FromLead     bool `json:"from_lead,omitempty"`
	ClaimedHuman bool `json:"claimed_human,omitempty"`
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
	// WasSeen means the message was already seen, and not read, before this
	// read. SeenAt cannot say it on the call that sets it, for the same
	// reason WasUnread exists.
	WasSeen bool `json:"was_seen,omitempty"`

	// Held is set on mail from another machine that this machine's link
	// policy (hold_mail) put in the person's inbox instead of the recipient's.
	// HeldFor and HeldForLabel are the window it was addressed to, empty for
	// a notice to the session. Released is set once the person passed it on
	// with release-agent-message, and ReleasedFrom on the copy that was
	// delivered, naming the held message. All are set by the daemon and
	// never taken from a request.
	Held         bool   `json:"held,omitempty"`
	HeldFor      string `json:"held_for,omitempty"`
	HeldForLabel string `json:"held_for_label,omitempty"`
	Released     bool   `json:"released,omitempty"`
	ReleasedFrom uint64 `json:"released_from,omitempty"`
}

func (b *agentBus) takeHeld(session string, id uint64) (AgentMessage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	mb, ok := b.boxes[session]
	if !ok {
		return AgentMessage{}, false
	}
	m := mb.find(id)
	if m == nil || !m.Held || m.Released {
		return AgentMessage{}, false
	}
	m.Released = true
	if m.ReadAt == 0 {
		m.ReadAt = time.Now().UnixNano()
	}
	return *m, true
}

type agentBus struct {
	mu     sync.Mutex
	boxes  map[string]*agentMailbox
	nextID uint64

	asks map[string]map[string]int

	noticed  map[string]noticeMark
	leaseSeq uint64
}

type agentMailbox struct {
	msgs              []*AgentMessage
	bytes             int
	evicted           uint64
	evictedUnread     map[string]uint64
	evictedUnreadGone uint64
	buckets           map[string]*sendBucket
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

func (b *agentBus) box(session string) *agentMailbox {
	mb := b.boxes[session]
	if mb == nil {
		mb = &agentMailbox{buckets: map[string]*sendBucket{}}
		b.boxes[session] = mb
	}
	return mb
}

func (b *agentBus) forget(session string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	delete(b.boxes, session)
	b.mu.Unlock()
	b.forgetNotices(session, "")
}

func (b *agentBus) forgetInbox(session, window string) {
	if b == nil || window == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	mb := b.boxes[session]
	if mb == nil {
		return
	}
	if n, ok := mb.evictedUnread[window]; ok {
		mb.evictedUnreadGone += n
		delete(mb.evictedUnread, window)
	}
}

func (mb *agentMailbox) allow(sender string, now time.Time) bool {
	bk := mb.buckets[sender]
	if bk == nil {
		bk = mb.makeBucket(sender, now)
		if bk == nil {
			return false
		}
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

func (mb *agentMailbox) makeBucket(sender string, now time.Time) *sendBucket {
	fresh := func(key string) *sendBucket {
		bk := &sendBucket{tokens: agentSendBurst, last: now}
		mb.buckets[key] = bk
		return bk
	}
	evict := func(match func(string) bool) bool {
		for k := range mb.buckets {
			if k != "" && match(k) {
				delete(mb.buckets, k)
				return true
			}
		}
		return false
	}
	isLink := func(k string) bool { return strings.HasPrefix(k, queueByLinkPrefix) }
	if len(mb.buckets) < agentMaxBuckets {
		return fresh(sender)
	}
	for k, bk := range mb.buckets {
		if k != "" && bk.tokens+now.Sub(bk.last).Minutes()*agentSendPerMinute >= agentSendBurst {
			delete(mb.buckets, k)
		}
	}
	if len(mb.buckets) < agentMaxBuckets {
		return fresh(sender)
	}
	if isLink(sender) {
		if bk := mb.buckets[agentOverflowBucket]; bk != nil {
			return bk
		}
		if !evict(isLink) {
			return nil
		}
		return fresh(agentOverflowBucket)
	}
	if !evict(isLink) && !evict(func(string) bool { return true }) {
		return nil
	}
	return fresh(sender)
}

func (mb *agentMailbox) append(m *AgentMessage) {
	mb.msgs = append(mb.msgs, m)
	mb.bytes += len(m.Text)
	for len(mb.msgs) > agentMailboxMaxMessages || (mb.bytes > agentMailboxMaxBytes && len(mb.msgs) > 1) {
		i := mb.victim()
		gone := mb.msgs[i]
		mb.msgs = slices.Delete(mb.msgs, i, i+1)
		mb.bytes -= len(gone.Text)
		mb.evicted++
		if waitsToBeRead(gone) {
			if mb.evictedUnread == nil {
				mb.evictedUnread = map[string]uint64{}
			}
			mb.evictedUnread[gone.To]++
		}
	}
}

func (mb *agentMailbox) victim() int {
	for i, m := range mb.msgs[:len(mb.msgs)-1] {
		if !waitsToBeRead(m) {
			return i
		}
	}
	return 0
}

func waitsToBeRead(m *AgentMessage) bool {
	return m.Kind == agentMsgDirect && m.ReadAt == 0
}

func (b *agentBus) send(session string, m AgentMessage) AgentMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	m.ID = b.nextID
	m.Session = session
	m.SentAt = time.Now().UnixNano()
	mb := b.box(session)
	m.ThreadID, m.ReplyToMissing = mb.threadFor(m.ID, m.ReplyTo)
	stored := &m
	mb.append(stored)
	return *stored
}

func (b *agentBus) checkRate(session, sender string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.box(session).allow(sender, time.Now())
}

type readQuery struct {
	inbox      string
	unreadOnly bool
	notices    bool
	peek       bool
	markSeen   bool
	thread     uint64
	limit      int
	live       func(windowID string) bool
}

type readResult struct {
	Messages      []AgentMessage
	Unread        int
	Seen          int
	MarkedRead    int
	MarkedSeen    int
	Total         int
	Evicted       uint64
	EvictedUnread uint64
}

func (b *agentBus) read(session string, q readQuery) readResult {
	res := b.collect(session, q)
	for i := range res.Messages {
		res.Messages[i].Attachments = resolveAttachments(res.Messages[i].Attachments)
	}
	return res
}

func (b *agentBus) collect(session string, q readQuery) readResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	mb := b.box(session)

	now := time.Now().UnixNano()
	res := readResult{Evicted: mb.evicted}
	if q.inbox != "" {
		res.EvictedUnread = mb.evictedUnread[q.inbox]
	} else {
		res.EvictedUnread = mb.evictedUnreadGone
		for _, n := range mb.evictedUnread {
			res.EvictedUnread += n
		}
	}

	var picked []*AgentMessage
	for _, m := range mb.msgs {
		if q.thread != 0 && m.ThreadID != q.thread {
			continue
		}
		switch m.Kind {
		case agentMsgNotice:
			if q.unreadOnly || (q.inbox != "" && !q.notices) {
				continue
			}
		case agentMsgAsk:
			if q.unreadOnly || (q.inbox != "" && m.To != q.inbox) {
				continue
			}
		default:
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
			out.WasSeen = out.SeenAt != 0
			if out.WasSeen {
				res.Seen++
			}
			if q.inbox != "" && m.To == q.inbox {
				switch {
				case !q.peek:
					m.ReadAt = now
					out.ReadAt = now
					res.MarkedRead++
				case q.markSeen && m.SeenAt == 0:
					m.SeenAt = now
					out.SeenAt = now
					res.MarkedSeen++
				}
			}
		}
		res.Messages = append(res.Messages, out)
	}
	return res
}

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

func (b *agentBus) linkQueued(session string) (unread, notices int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.box(session).msgs {
		if m.Origin != AgentOriginLink {
			continue
		}
		switch m.Kind {
		case agentMsgDirect:
			if m.ReadAt == 0 {
				unread++
			}
		case agentMsgNotice:
			notices++
		}
	}
	return unread, notices
}

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

func (b *agentBus) seenCounts(session string) map[string]int {
	counts := map[string]int{}
	if b == nil {
		return counts
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.box(session).msgs {
		if m.Kind == agentMsgDirect && m.ReadAt == 0 && m.SeenAt != 0 && m.To != "" {
			counts[m.To]++
		}
	}
	return counts
}

func (b *agentBus) threadSeen(session, inbox string, thread uint64) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, m := range b.box(session).msgs {
		if m.ThreadID == thread && m.Kind == agentMsgDirect && m.To == inbox && m.ReadAt == 0 && m.SeenAt != 0 {
			n++
		}
	}
	return n
}

func (b *agentBus) highestID() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nextID
}

func (b *agentBus) newerThan(session string, baseline, thread uint64) (AgentMessage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.box(session).msgs {
		if thread != 0 && m.ThreadID != thread {
			continue
		}
		if m.ID > baseline {
			return *m, true
		}
	}
	return AgentMessage{}, false
}

func (b *agentBus) firstUnread(session, inbox string, thread uint64) (AgentMessage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.box(session).msgs {
		if thread != 0 && m.ThreadID != thread {
			continue
		}
		if m.Kind == agentMsgDirect && m.To == inbox && m.ReadAt == 0 {
			return *m, true
		}
	}
	return AgentMessage{}, false
}

func (mb *agentMailbox) find(id uint64) *AgentMessage {
	for _, m := range mb.msgs {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func (mb *agentMailbox) threadFor(id, replyTo uint64) (uint64, bool) {
	if replyTo == 0 {
		return id, false
	}
	if parent := mb.find(replyTo); parent != nil {
		return parent.ThreadID, false
	}
	return replyTo, true
}

func (b *agentBus) resolveThread(session string, id uint64) uint64 {
	if id == 0 {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if m := b.box(session).find(id); m != nil {
		return m.ThreadID
	}
	return id
}

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

func shortWindowID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

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

func (b *agentBus) rename(old, newName string) {
	if b == nil || old == newName {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if mb := b.boxes[old]; mb != nil {
		delete(b.boxes, old)
		if b.boxes[newName] == nil {
			b.boxes[newName] = mb
		}
	}
	for w, m := range b.noticed {
		if m.session == old {
			m.session = newName
			b.noticed[w] = m
		}
	}
}
