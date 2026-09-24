package session

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
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
//
// The hub also keeps the most recent events in a bounded ring, so a subscriber
// that reconnects can ask for everything after the last seq it saw. Each daemon
// start picks a random boot id that is stamped on every event: a seq is only
// meaningful next to the boot id it came with, because a restarted daemon
// numbers from 1 again.

// Event type discriminators carried in a stream event's "type" field. These are
// part of the public protocol surface; keep the string values stable.
const (
	EventWindowCreated     = "window-created"     // a window was created
	EventWindowClosed      = "window-closed"      // a window was closed/removed
	EventWindowExit        = "window-exit"        // a window's shell process exited
	EventWindowRetitled    = "window-retitled"    // a window's title/name changed
	EventWindowFocused     = "window-focused"     // a window became the focused window
	EventWindowMoved       = "window-moved"       // a window moved to another workspace
	EventWindowMinimized   = "window-minimized"   // a window was minimized
	EventWindowRestored    = "window-restored"    // a minimized window was restored
	EventWorkspaceSwitched = "workspace-switched" // the session's current workspace changed
	EventAgentState        = "agent-state"        // a window's agent state changed
	// EventAgentMessage is one agent leaving a message for another. Window is
	// the recipient's window id, or empty for a session-wide notice. It carries
	// nothing else on purpose: a subscriber reads the message back from the ring
	// rather than trusting a payload that went stale the moment it was queued.
	EventAgentMessage = "agent-message"
	EventOutput       = "output" // a window produced output (activity)
	EventBell         = "bell"   // a window rang the terminal bell
	// EventNotification is a window sending a desktop notification with OSC 9,
	// OSC 777 or OSC 99. Title and Body carry its text.
	EventNotification   = "notification"
	EventModeChanged    = "mode-changed"    // a terminal mode toggled (e.g. alt-screen)
	EventSessionCreated = "session-created" // a session was created
	EventSessionClosed  = "session-closed"  // a session was terminated
	EventGap            = "gap"             // slow-subscriber marker: N events were dropped
	// EventAttention is a change to the Inbox, the daemon's attention queue:
	// an item opened, updated or closed. Action says which, and Attention
	// carries the item. See attention.go.
	EventAttention = "attention"
	// EventHostChanged says what this daemon knows about a linked host moved:
	// its link came up or went down, or its sessions or agents changed. Host
	// names it and Status is its link status. It carries nothing else, so a
	// client that shows hosts lists them again. See host_fleet.go.
	EventHostChanged = "host-changed"
	// EventPrompt, EventCommandStarted and EventCommandFinished follow a
	// shell's commands through its OSC 133 marks (shell_commands.go). A shell
	// without that integration raises none of them. EventPrompt is the shell
	// showing a prompt after anything else; EventCommandStarted carries the
	// command line in Cmdline; EventCommandFinished carries Cmdline, ExitCode
	// (absent when the shell sent no status), DurationMS and CommandSeq.
	EventPrompt          = "prompt"
	EventCommandStarted  = "command-started"
	EventCommandFinished = "command-finished"
	// EventAgentActivity is one entry of an agent pane's activity ring: a
	// prompt, a tool call, a tool result or the end of a turn, as its hooks
	// reported them. It is chatty, so it reaches only a subscriber that names
	// it in types. See agent_activity.go.
	EventAgentActivity = "agent-activity"
	EventSubscribed    = "subscribed" // subscribe ack result type
)

// defaultEventQueue bounds a subscriber's per-connection event queue. When it is
// full the hub drops the event and records the drop as a gap rather than blocking
// the publisher (and thus the daemon).
const defaultEventQueue = 256

// defaultEventRing bounds how many events the hub keeps for replay. Output
// events are not kept (see eventHub.retain), so this counts lifecycle, agent
// and bell events, which arrive at human speed.
const defaultEventRing = 4096

// Reasons a gap marker carries. A gap without a reason predates them; readers
// should treat an unknown or missing reason as "resync".
const (
	// GapOverflow: the subscriber's queue was full and events were dropped.
	GapOverflow = "overflow"
	// GapEvicted: a resume asked for events the replay ring no longer holds.
	GapEvicted = "evicted"
	// GapBootChanged: a resume named a boot id from another daemon start, or
	// a seq this daemon has not reached, so the numbering is not comparable.
	GapBootChanged = "boot_changed"
	// GapNotRetained: a resume's filter admits output events, and output
	// events are not kept for replay, so some may be missing.
	GapNotRetained = "not_retained"
)

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
	// Body is a notification event's text. Title carries its title, which
	// OSC 9 never sets.
	Body    string `json:"body,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Enabled bool   `json:"enabled,omitempty"`
	// State is the agent state an agent-state event reports, in its wire
	// spelling; a pane ceasing to be an agent reports "none" rather than
	// omitting the field, so the transition is visible.
	State string `json:"state,omitempty"`
	// Workspace carries the workspace a window moved to (window-moved) or the
	// workspace that became current (workspace-switched). Workspaces are 1-based,
	// so a zero value is always "not applicable" and is omitted.
	Workspace int    `json:"workspace,omitempty"`
	Dropped   uint64 `json:"dropped,omitempty"`
	// Reason says why a gap marker was sent: one of the Gap* constants.
	Reason string `json:"reason,omitempty"`
	// BootID names the daemon start that numbered Seq. It is on every event
	// and on every gap marker.
	BootID string `json:"boot_id,omitempty"`
	Time   int64  `json:"time,omitempty"`
	// Action is what an attention event did to its item: open, update or
	// close. Attention is the item, and on close its closed field says why.
	Action    string         `json:"action,omitempty"`
	Attention *AttentionItem `json:"attention,omitempty"`
	// Host names the linked machine an event is about, empty for this one. It
	// is on the attention events of items mirrored from a host, on
	// host-changed, and on the events relayed from a host to a subscriber
	// that asked for them with hosts. Status is a host-changed event's link
	// status.
	Host   string `json:"host,omitempty"`
	Status string `json:"status,omitempty"`
	// Cmdline is the command line of a command-started or command-finished
	// event, cut to 512 bytes with likely secrets masked. ExitCode is the
	// status a command-finished reports, nil when the shell sent none.
	// DurationMS is how long it ran and CommandSeq how many commands the pane
	// has finished, this one included.
	Cmdline    string `json:"cmdline,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	CommandSeq uint64 `json:"command_seq,omitempty"`
	// Entry is an agent-activity event's entry of the pane's activity ring.
	Entry *AgentActivityEntry `json:"entry,omitempty"`

	// relayed marks an event copied from a linked host's own stream. It is
	// delivered only to a subscriber that asked for other machines' events,
	// since every other subscriber reads session and window as names on this
	// machine.
	relayed bool
}

// SessionEvent is the source-side event a Session emits through its event sink.
// The daemon's sink wrapper adds the session name, sequence number, and time
// before publishing to the hub. Window/PTYID are filled in by the per-PTY emitter
// or the window op that raises the event.
type SessionEvent struct {
	Type      string
	Window    string
	PTYID     string
	Title     string
	Body      string
	Bytes     int
	Mode      string
	Enabled   bool
	State     string
	Workspace int
	// The command fields of the prompt and command events. See streamEvent.
	Cmdline    string
	ExitCode   *int
	DurationMS int64
	CommandSeq uint64

	// The fields below carry everything the hook dispatcher needs, so it can
	// build a hook's environment from the event alone. That is not a
	// convenience: the sink runs with the session's state lock held, so a
	// dispatcher that read the session back would deadlock it.
	//
	// They are unexported so they cannot reach the wire. The subscribe stream
	// reports the state a session arrived at, and these say what it left, what a
	// window that is already gone was called, and which agent a transition was
	// about.
	hookTitle         string
	hookWorkspace     int
	hookPrevState     string
	hookPrevWorkspace int
	hookHarness       string
	hookMessage       string

	// These feed the attention queue, and like the hook fields they never reach
	// the wire. hookKind is the blocked_by of a needs_input window.
	// completionSeq and prevCompletionSeq are the window's completion_seq after
	// and before the transition, so a finished turn can be told from a pane that
	// merely came to rest. A completion-seen event carries the count that was
	// seen in completionSeq.
	hookKind          string
	completionSeq     uint64
	prevCompletionSeq uint64
}

// eventCompletionSeen is raised when an attached client's state push shows it
// focused a pane with more finished turns than anybody had seen. It is internal:
// the daemon's sink hands it to the attention queue and never publishes it, and
// no hook fires on it.
const eventCompletionSeen = "completion-seen"

// eventAttentionDetail is raised when a needs_input or errored window keeps its
// state but what the Inbox shows about it changes: the blocked_by kind, the
// message, the display title, the harness or the workspace. A hook reporting
// kind=approval after the screen tier already set needs_input is the common
// case. It is internal on the same terms as eventCompletionSeen: the Inbox
// updates its item, nothing is published and no hook fires, because the agent
// state did not change.
const eventAttentionDetail = "attention-detail"

// eventPaneFocused is raised when a push from a client the person holds moves
// the session's focus to another pane. It is internal on the same terms as
// eventCompletionSeen: the Inbox ends an approval hold on the pane, so the
// harness shows its own prompt to the person now looking at it.
const eventPaneFocused = "pane-focused"

// eventFilter selects which events a subscriber receives. A zero value matches
// everything. An empty types set matches all event types.
type eventFilter struct {
	session string
	window  string
	ptyID   string
	types   map[string]bool
	// hosts admits the events relayed from linked hosts, and lets the session
	// and window filters match events of other machines. Without it an event
	// about another machine reaches only a subscriber that names no session,
	// window or pane, and only when this daemon produced it (an Inbox item
	// mirrored from a host, or host-changed), because every other subscriber
	// reads session and window as names on this machine.
	hosts bool
}

func (f eventFilter) match(ev streamEvent) bool {
	if ev.Host != "" && !f.hosts {
		if ev.relayed || f.session != "" || f.window != "" || f.ptyID != "" {
			return false
		}
	}
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
	// An opt-in type reaches only a filter that names it, so a subscriber
	// written before it existed, which names no types, never sees it.
	if len(f.types) == 0 && optInEventTypes[ev.Type] {
		return false
	}
	return true
}

// optInEventTypes are the event types a subscription receives only when its
// types filter names them. They arrive at the speed an agent works rather
// than the speed a person does.
var optInEventTypes = map[string]bool{EventAgentActivity: true}

// admitsOutput reports whether the filter lets output events through, which
// decides whether a resume can be exact: output events are not kept for replay.
func (f eventFilter) admitsOutput() bool {
	return len(f.types) == 0 || f.types[EventOutput]
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

	// preface holds what a resumed subscription must write before any live
	// event: a gap marker when the resume could not be exact, then the replayed
	// events. It is filled once at subscribe time, under the hub lock, so every
	// replayed seq is at or below the seq the subscription went live at and no
	// event can be both replayed and delivered live.
	preface []streamEvent
}

// close signals the subscription's streamer (if any) to exit. Safe to call more
// than once.
func (s *eventSub) close() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// eventHub fans typed events out to every matching subscriber, assigning each a
// daemon-global monotonic sequence number.
type eventHub struct {
	mu     sync.Mutex
	seq    uint64
	bootID string
	subs   map[*eventSub]struct{}

	// ring is a circular buffer of the most recent retained events, oldest at
	// ringStart. evictedSeq is the seq of the newest event pushed out of it, so
	// the ring holds every retained event after evictedSeq. lastOutputSeq is
	// the seq of the newest output event, which the ring does not keep, and
	// lastActivitySeq the same for agent-activity events.
	ring            []streamEvent
	ringStart       int
	ringLen         int
	evictedSeq      uint64
	lastOutputSeq   uint64
	lastActivitySeq uint64
}

func newEventHub() *eventHub {
	return newEventHubSize(defaultEventRing)
}

// newEventHubSize is newEventHub with a replay ring of ringSize events, so a
// test can reach eviction without publishing thousands of events.
func newEventHubSize(ringSize int) *eventHub {
	if ringSize < 1 {
		ringSize = 1
	}
	return &eventHub{
		subs:   make(map[*eventSub]struct{}),
		bootID: newBootID(),
		ring:   make([]streamEvent, ringSize),
	}
}

// newBootID returns a random id for one daemon start. It only has to differ
// between starts, so 64 random bits written as hex is plenty.
func newBootID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// resumePoint is where a reconnecting subscriber left off: the last seq it saw
// and the boot id that seq belongs to. An empty bootID means the caller did not
// say, and only a seq this daemon has not reached can then be recognised as
// coming from another start.
type resumePoint struct {
	afterSeq uint64
	bootID   string
}

// errResumeAhead is returned by subscribeFrom when the caller names this
// daemon's boot id with a seq it has not assigned yet.
type errResumeAhead struct{ afterSeq, current uint64 }

func (e errResumeAhead) Error() string {
	return "after_seq " + strconv.FormatUint(e.afterSeq, 10) +
		" is ahead of this daemon's seq " + strconv.FormatUint(e.current, 10)
}

// subscribe registers a new subscription with the given filter and queue size
// (bufSize <= 0 uses the default). The returned sub must be released with
// unsubscribe.
func (h *eventHub) subscribe(filter eventFilter, bufSize int) *eventSub {
	sub, _, _ := h.subscribeFrom(filter, bufSize, nil)
	return sub
}

// subscribeFrom is subscribe with an optional resume point. It returns the
// subscription and the seq it went live at (the ack's baseline). With a resume
// point, sub.preface carries the events after it that the ring still holds,
// preceded by a gap marker when that is not every event the caller missed.
//
// Registration and the ring read happen under one lock, so an event is either
// in the preface (seq at or below the baseline) or delivered live (above it),
// never both and never neither.
func (h *eventHub) subscribeFrom(filter eventFilter, bufSize int, from *resumePoint) (*eventSub, uint64, error) {
	if bufSize <= 0 {
		bufSize = defaultEventQueue
	}
	sub := &eventSub{
		ch:     make(chan streamEvent, bufSize),
		filter: filter,
		stop:   make(chan struct{}),
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if from != nil {
		if from.bootID == h.bootID && from.afterSeq > h.seq {
			return nil, 0, errResumeAhead{afterSeq: from.afterSeq, current: h.seq}
		}
		sub.preface = h.replayLocked(filter, *from)
	}
	h.subs[sub] = struct{}{}
	return sub, h.seq, nil
}

// replayLocked builds the preface for a resume. The caller holds h.mu.
func (h *eventHub) replayLocked(filter eventFilter, from resumePoint) []streamEvent {
	gap := func(reason string) streamEvent {
		return streamEvent{Type: EventGap, Reason: reason, BootID: h.bootID}
	}
	if (from.bootID != "" && from.bootID != h.bootID) || from.afterSeq > h.seq {
		// Another start numbered that seq. Nothing here lines up with it.
		return []streamEvent{gap(GapBootChanged)}
	}
	var out []streamEvent
	switch {
	case from.afterSeq < h.evictedSeq:
		out = append(out, gap(GapEvicted))
	case filter.admitsOutput() && h.lastOutputSeq > from.afterSeq,
		filter.types[EventAgentActivity] && h.lastActivitySeq > from.afterSeq:
		out = append(out, gap(GapNotRetained))
	}
	for i := range h.ringLen {
		ev := h.ring[(h.ringStart+i)%len(h.ring)]
		if ev.Seq > from.afterSeq && filter.match(ev) {
			out = append(out, ev)
		}
	}
	return out
}

// retain records ev in the replay ring. The caller holds h.mu. Output events
// are only counted: they fire on every PTY read, so keeping them would push the
// agent and lifecycle events a reconnecting subscriber actually needs out of a
// bounded ring within seconds. Agent-activity events are counted the same way:
// one fires on every tool call of every agent, and a subscriber that missed
// some reads the pane's ring with agent-activity instead.
func (h *eventHub) retain(ev streamEvent) {
	switch ev.Type {
	case EventOutput:
		h.lastOutputSeq = ev.Seq
		return
	case EventAgentActivity:
		h.lastActivitySeq = ev.Seq
		return
	}
	if h.ringLen == len(h.ring) {
		h.evictedSeq = h.ring[h.ringStart].Seq
		h.ring[h.ringStart] = ev
		h.ringStart = (h.ringStart + 1) % len(h.ring)
		return
	}
	h.ring[(h.ringStart+h.ringLen)%len(h.ring)] = ev
	h.ringLen++
}

// bootIdentity returns this daemon start's boot id.
func (h *eventHub) bootIdentity() string {
	return h.bootID
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
	ev.BootID = h.bootID
	if ev.Time == 0 {
		ev.Time = time.Now().UnixNano()
	}
	h.retain(ev)
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
