package session

import (
	"sync"
	"sync/atomic"
	"time"
)

// This file implements the daemon's event hub: the fan-out that backs the
// subscribe verb's long-lived event stream and the wait-for verb's blocking
// waits. Event sources (PTY output/bell/mode changes, window and session
// lifecycle) publish typed events to the hub, which stamps each with a
// daemon-global monotonic sequence number and delivers it to every matching
// subscriber. Subscribers have a bounded queue: a slow subscriber's events are
// dropped and a gap marker is delivered rather than blocking the daemon (the
// same slow-client discipline used by daemon_stream.go for raw PTY output).

// Event type discriminators carried in a stream event's "type" field. These are
// part of the public protocol surface; keep the string values stable.
const (
	EventWindowCreated  = "window-created"  // a daemon-owned window was created
	EventWindowClosed   = "window-closed"   // a daemon-owned window was closed/removed
	EventWindowExit     = "window-exit"     // a window's shell process exited
	EventWindowRetitled = "window-retitled" // a window's title/name changed
	EventOutput         = "output"          // a window produced output (activity)
	EventBell           = "bell"            // a window rang the terminal bell
	EventModeChanged    = "mode-changed"    // a terminal mode toggled (e.g. alt-screen)
	EventSessionCreated = "session-created" // a session was created
	EventSessionClosed  = "session-closed"  // a session was terminated
	EventGap            = "gap"             // slow-subscriber marker: N events were dropped
	EventSubscribed     = "subscribed"      // subscribe ack result type
)

// defaultEventQueue bounds a subscriber's per-connection event queue. When it is
// full the hub drops the event and records the drop as a gap rather than blocking
// the publisher (and thus the daemon).
const defaultEventQueue = 256

// streamEvent is one event as delivered on the wire (a JSON line). Seq is the
// daemon-global monotonic sequence number; every subscriber sees the same Seq for
// the same event. Zero-value fields are omitted so a bell event is not padded
// with an empty title, byte count, and so on.
type streamEvent struct {
	Seq     uint64 `json:"seq,omitempty"`
	Type    string `json:"type"`
	Session string `json:"session,omitempty"`
	Window  string `json:"window,omitempty"`
	PTYID   string `json:"pty_id,omitempty"`
	Title   string `json:"title,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Enabled bool   `json:"enabled,omitempty"`
	Dropped uint64 `json:"dropped,omitempty"`
	Time    int64  `json:"time,omitempty"`
}

// SessionEvent is the source-side event a Session emits through its event sink.
// The daemon's sink wrapper adds the session name, sequence number, and time
// before publishing to the hub. Window/PTYID are filled in by the per-PTY emitter
// or the window op that raises the event.
type SessionEvent struct {
	Type    string
	Window  string
	PTYID   string
	Title   string
	Bytes   int
	Mode    string
	Enabled bool
}

// eventFilter selects which events a subscriber receives. A zero value matches
// everything. An empty types set matches all event types.
type eventFilter struct {
	session string
	window  string
	ptyID   string
	types   map[string]bool
}

func (f eventFilter) match(ev streamEvent) bool {
	if f.session != "" && ev.Session != f.session {
		return false
	}
	if f.window != "" && ev.Window != f.window {
		return false
	}
	if f.ptyID != "" && ev.PTYID != f.ptyID {
		return false
	}
	if len(f.types) > 0 && !f.types[ev.Type] {
		return false
	}
	return true
}

// eventSub is one subscription: a bounded delivery channel, its filter, and a
// dropped-since-last-delivered counter driving the gap marker. stop lets an
// explicit unsubscribe wake the streamer.
type eventSub struct {
	ch       chan streamEvent
	filter   eventFilter
	dropped  atomic.Uint64
	stop     chan struct{}
	stopOnce sync.Once
}

// close signals the subscription's streamer (if any) to exit. Safe to call more
// than once.
func (s *eventSub) close() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// eventHub fans typed events out to every matching subscriber, assigning each a
// daemon-global monotonic sequence number.
type eventHub struct {
	mu   sync.Mutex
	seq  uint64
	subs map[*eventSub]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subs: make(map[*eventSub]struct{})}
}

// subscribe registers a new subscription with the given filter and queue size
// (bufSize <= 0 uses the default). The returned sub must be released with
// unsubscribe.
func (h *eventHub) subscribe(filter eventFilter, bufSize int) *eventSub {
	if bufSize <= 0 {
		bufSize = defaultEventQueue
	}
	sub := &eventSub{
		ch:     make(chan streamEvent, bufSize),
		filter: filter,
		stop:   make(chan struct{}),
	}
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

// unsubscribe removes a subscription and signals its streamer to stop. Idempotent.
func (h *eventHub) unsubscribe(sub *eventSub) {
	if sub == nil {
		return
	}
	h.mu.Lock()
	delete(h.subs, sub)
	h.mu.Unlock()
	sub.close()
}

// currentSeq returns the last assigned sequence number, so a fresh subscriber can
// learn the baseline from which its stream begins.
func (h *eventHub) currentSeq() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

// publish stamps ev with the next global sequence number and a timestamp, then
// delivers it to every matching subscriber. Delivery is non-blocking: a
// subscriber whose queue is full has the event dropped and its drop counter
// incremented (surfaced later as a gap marker), so one slow subscriber can never
// stall the publisher or any other subscriber.
func (h *eventHub) publish(ev streamEvent) {
	h.mu.Lock()
	h.seq++
	ev.Seq = h.seq
	if ev.Time == 0 {
		ev.Time = time.Now().UnixNano()
	}
	for sub := range h.subs {
		if !sub.filter.match(ev) {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			sub.dropped.Add(1)
		}
	}
	h.mu.Unlock()
}
