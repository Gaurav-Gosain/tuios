package session

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

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
	// EventWorkspaceRenamed: a workspace's name was set, changed or cleared.
	// Workspace is the workspace, and Title its new name, absent when cleared.
	EventWorkspaceRenamed = "workspace-renamed"
	// EventLayoutChanged: the panes on a workspace now sit differently. A
	// pane opened, closed, moved, was minimized, zoomed or resized, or the
	// session changed size. Workspace names it. It carries no rectangles: a
	// client that mirrors the layout reads list-windows again. It is opt-in
	// and not kept for replay, since a dragged split raises one per step.
	EventLayoutChanged = "layout-changed"
	EventAgentState    = "agent-state" // a window's agent state changed
	// EventAgentSeen: the person looked at a pane whose agent finished a turn
	// they had not seen, or marked such a pane unread again. Window names the
	// pane, and Action is seen or unread. The agent state does not change, so
	// no agent-state event comes with it. list-agents' finished_unread reads
	// the same fact.
	EventAgentSeen = "agent-seen"
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
	// EventDaemonUpgraded is the daemon's image replaced by a live upgrade,
	// with every pane still running. Title is the build the panes came from
	// and Body the build that has them now; State is fallback when the new
	// binary could not take them and the previous one did, and recovered
	// when the new image died first and the previous binary was started by
	// the handoff's keeper, under a new pid. Raised once, by the image that
	// took the panes over.
	EventDaemonUpgraded = "daemon-upgraded"
	EventSessionClosed  = "session-closed" // a session was terminated
	// EventSessionLabelled: a session's display name was set, changed or
	// cleared. Title is the new display name, absent when cleared. A rename
	// raises session-closed and session-created instead.
	EventSessionLabelled = "session-labelled"
	EventGap             = "gap" // slow-subscriber marker: N events were dropped
	// EventClientSessionChanged reports a client attaching, detaching or switching.
	EventClientSessionChanged = "client-session-changed"
	// EventAttention is a change to the Inbox, the daemon's attention queue:
	// an item opened, updated or closed. Action says which, and Attention
	// carries the item. See attention.go.
	EventAttention = "attention"
	// EventHostChanged says what this daemon knows about a linked host moved:
	// its link came up or went down, or its sessions or agents changed. Host
	// names it and Status is its link status. It carries nothing else, so a
	// client that shows hosts lists them again. See host_fleet.go.
	EventHostChanged = "host-changed"
	// EventWorktreeRemoved says a git worktree tuios made was removed: by
	// remove-worktree, or by the undo of a setup or a delegation that did
	// not start. Path is the folder that is gone and Session the worktree's
	// session, which may be gone too. A plugin hook on worktree.removed runs
	// on it, so a tool that keeps something per folder can clean up.
	EventWorktreeRemoved = "worktree-removed"
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
	// EventTranscript says the transcript a pane is joined to grew, so a
	// reader of agent-transcript can read again. Cursor is the cursor a read
	// to the end returns now. It carries nothing of the file, and it reaches
	// only a subscriber that names it in types. See verb_agent_transcript.go.
	EventTranscript = "transcript"
	// EventTransfer is a change of a copy the daemon runs (transfer.go):
	// Action is created, state, ended or removed, and Transfer is the job's
	// row as transfer-list gives it. A copy's state is said once per change.
	// removed says a copy that ended left the list, by transfer-clear or 30
	// minutes after it ended, and carries the last row it had.
	EventTransfer = "transfer"
	// EventTransferProgress is a copy's row while its bytes move, at most
	// four times a second per copy. It reaches only a subscriber that names
	// it in types, and it is not kept for replay: the next one replaces it.
	EventTransferProgress = "transfer-progress"
	// EventTransferFile is one file of a copy: Action and File.State are
	// done, same, skipped, conflict or failed. It reaches only a subscriber
	// that names it in types, and it is not kept for replay.
	EventTransferFile = "transfer-file"
	// EventTML is what a person did on a TML surface that its owner hears:
	// an emit action, or the close. TML carries the surface, the name and
	// the value. It reaches only a subscriber that names it in types, and a
	// tml-open with events. See tml_surface.go.
	EventTML = "tml-event"
	// EventTMLSurface follows a TML surface for a client that draws it from
	// verbs, such as guios, which has no attach for the pushes a TUI gets.
	// TMLSurface carries the surface, its versions, the JSON Patch that made
	// the version when one did and is small, and the close. A client that
	// holds the version before applies the patch; any other reads the
	// surface with tml-get. It reaches only a subscriber that names it in
	// types, and it is not kept for replay.
	EventTMLSurface = "tml-surface"
	// EventTaskClaimed, EventTaskFinished and EventTaskChanged follow the
	// task cards of a session (task.go). Task carries the card's row as
	// list-tasks gives it. On claimed and finished, Window is the pane that
	// held the card. On changed, Action says what happened: added, staged,
	// blocked, unblocked, released or removed, and Reason carries why a card
	// was released without its holder asking (pane closed, agent left,
	// restart).
	EventTaskClaimed  = "task-claimed"
	EventTaskFinished = "task-finished"
	EventTaskChanged  = "task-changed"
	// EventKeepAwake says the daemon took or dropped its hold that keeps the
	// machine awake, or [daemon] keep_awake changed (keep_awake.go). Mode is
	// off, on or agents, State is held or released, Working is the number of
	// working agent panes on this machine, and Reason says why the machine
	// could not be held awake, when it could not. It names no session.
	EventKeepAwake = "keep-awake"
	// EventTheme is the theme a TUI client shows, on each change, previews
	// from the theme picker included (theme_event.go). Theme carries the id,
	// the background, whether it is light and whether it is a preview. It
	// reaches only a subscriber that names it in types.
	EventTheme      = "theme"
	EventSubscribed = "subscribed" // subscribe ack result type
)

const defaultEventQueue = 256

const maxEventQueue = 4096

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

type streamEvent struct {
	Seq        uint64              `json:"seq,omitempty"`
	Type       string              `json:"type"`
	Session    string              `json:"session,omitempty"`
	Window     string              `json:"window,omitempty"`
	PTYID      string              `json:"pty_id,omitempty"`
	Title      string              `json:"title,omitempty"`
	ClientID   string              `json:"client_id,omitempty"`
	PID        int                 `json:"pid,omitempty"`
	Attached   *bool               `json:"attached,omitempty"`
	Body       string              `json:"body,omitempty"`
	Bytes      int                 `json:"bytes,omitempty"`
	Mode       string              `json:"mode,omitempty"`
	Enabled    bool                `json:"enabled,omitempty"`
	State      string              `json:"state,omitempty"`
	Workspace  int                 `json:"workspace,omitempty"`
	Dropped    uint64              `json:"dropped,omitempty"`
	Reason     string              `json:"reason,omitempty"`
	BootID     string              `json:"boot_id,omitempty"`
	Time       int64               `json:"time,omitempty"`
	Action     string              `json:"action,omitempty"`
	Attention  *AttentionItem      `json:"attention,omitempty"`
	Host       string              `json:"host,omitempty"`
	Status     string              `json:"status,omitempty"`
	Cmdline    string              `json:"cmdline,omitempty"`
	ExitCode   *int                `json:"exit_code,omitempty"`
	DurationMS int64               `json:"duration_ms,omitempty"`
	CommandSeq uint64              `json:"command_seq,omitempty"`
	Entry      *AgentActivityEntry `json:"entry,omitempty"`
	Cursor     string              `json:"cursor,omitempty"`
	Transfer   *TransferRow        `json:"transfer,omitempty"`
	File       *TransferFile       `json:"file,omitempty"`
	TML        *TMLEvent           `json:"tml,omitempty"`
	TMLSurface *TMLSurfaceEvent    `json:"tml_surface,omitempty"`
	Task       *TaskRow            `json:"task,omitempty"`
	Dispatch   *DispatchStatus     `json:"dispatch,omitempty"`
	Working    *int                `json:"working,omitempty"`
	Path       string              `json:"path,omitempty"`
	Theme      *ThemeEvent         `json:"theme,omitempty"`

	relayed bool
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
	Body    string
	Bytes   int
	Mode    string
	Enabled bool
	State   string
	// Reason is why an agent-state event's State is not the state the agent
	// gave: AgentReasonSilent, or empty. See agent_silent.go.
	Reason    string
	Workspace int
	// The command fields of the prompt and command events. See streamEvent.
	Cmdline    string
	ExitCode   *int
	DurationMS int64
	CommandSeq uint64
	// Cursor is a transcript event's cursor. See streamEvent.
	Cursor string

	agentQuiet        bool
	hookTitle         string
	hookWorkspace     int
	hookPrevState     string
	hookPrevWorkspace int
	hookHarness       string
	hookMessage       string
	hookProgram       bool

	hookKind                       string
	completionSeq                  uint64
	prevCompletionSeq              uint64
	hookWorktree, hookAgentSession string
	hookRoot                       string
	hookRunDir                     string
	link                           childLink
	taskRow                        *TaskRow
	taskReason                     string
}

const eventCompletionSeen = "completion-seen"

const eventCompletionUnseen = "completion-unseen"

const eventAttentionDetail = "attention-detail"

const eventPaneFocused = "pane-focused"

const eventTaskReleased = "task-released"

const eventProgramStatus = "program-status"

type eventFilter struct {
	session string
	sess    *Session
	window  string
	ptyID   string
	types   map[string]bool
	hosts   bool
	surface string
}

func (f eventFilter) match(ev streamEvent) bool {
	if ev.Host != "" && !f.hosts {
		if ev.relayed || f.session != "" || f.window != "" || f.ptyID != "" {
			return false
		}
	}
	follows := ev.Host == "" && ((f.window != "" && ev.Window == f.window) || (f.ptyID != "" && ev.PTYID == f.ptyID))
	if f.sess != nil {
		if ev.Session != f.sess.Name() && !follows {
			return false
		}
	} else if f.session != "" && ev.Session != f.session && !follows {
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
	if f.surface != "" && (ev.TML == nil || ev.TML.Surface != f.surface) {
		return false
	}
	if len(f.types) == 0 && optInEventTypes[ev.Type] {
		return false
	}
	return true
}

var optInEventTypes = map[string]bool{EventLayoutChanged: true, EventAgentActivity: true, EventTranscript: true, EventTransferProgress: true, EventTransferFile: true, EventTML: true, EventTMLSurface: true, EventTheme: true}

func (f eventFilter) admitsOutput() bool {
	return len(f.types) == 0 || f.types[EventOutput]
}

type eventSub struct {
	ch       chan streamEvent
	filter   eventFilter
	dropped  atomic.Uint64
	stop     chan struct{}
	stopOnce sync.Once

	preface []streamEvent
}

func (s *eventSub) close() {
	s.stopOnce.Do(func() { close(s.stop) })
}

type eventHub struct {
	mu     sync.Mutex
	seq    uint64
	bootID string
	subs   map[*eventSub]struct{}

	ring              []streamEvent
	ringStart         int
	ringLen           int
	evictedSeq        uint64
	lastOutputSeq     uint64
	lastActivitySeq   uint64
	lastTranscriptSeq uint64
	lastProgressSeq   uint64
	lastFileSeq       uint64
	lastLayoutSeq     uint64
	lastTMLSeq        uint64
	lastTMLSurfaceSeq uint64
}

func newEventHub() *eventHub {
	return newEventHubSize(defaultEventRing)
}

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

func newBootID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

type resumePoint struct {
	afterSeq uint64
	bootID   string
}

type errResumeAhead struct{ afterSeq, current uint64 }

func (e errResumeAhead) Error() string {
	return "after_seq " + strconv.FormatUint(e.afterSeq, 10) +
		" is ahead of this daemon's seq " + strconv.FormatUint(e.current, 10)
}

func (h *eventHub) subscribe(filter eventFilter, bufSize int) *eventSub {
	sub, _, _ := h.subscribeFrom(filter, bufSize, nil)
	return sub
}

func (h *eventHub) subscribeFrom(filter eventFilter, bufSize int, from *resumePoint) (*eventSub, uint64, error) {
	if bufSize <= 0 {
		bufSize = defaultEventQueue
	}
	bufSize = min(bufSize, maxEventQueue)
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

func (h *eventHub) replayLocked(filter eventFilter, from resumePoint) []streamEvent {
	gap := func(reason string) streamEvent {
		return streamEvent{Type: EventGap, Reason: reason, BootID: h.bootID}
	}
	if (from.bootID != "" && from.bootID != h.bootID) || from.afterSeq > h.seq {
		return []streamEvent{gap(GapBootChanged)}
	}
	var out []streamEvent
	switch {
	case from.afterSeq < h.evictedSeq:
		out = append(out, gap(GapEvicted))
	case filter.admitsOutput() && h.lastOutputSeq > from.afterSeq,
		filter.types[EventAgentActivity] && h.lastActivitySeq > from.afterSeq,
		filter.types[EventTranscript] && h.lastTranscriptSeq > from.afterSeq,
		filter.types[EventTransferProgress] && h.lastProgressSeq > from.afterSeq,
		filter.types[EventTransferFile] && h.lastFileSeq > from.afterSeq,
		filter.types[EventLayoutChanged] && h.lastLayoutSeq > from.afterSeq,
		filter.types[EventTML] && h.lastTMLSeq > from.afterSeq,
		filter.types[EventTMLSurface] && h.lastTMLSurfaceSeq > from.afterSeq:
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

func (h *eventHub) retain(ev streamEvent) {
	switch ev.Type {
	case EventOutput:
		h.lastOutputSeq = ev.Seq
		return
	case EventAgentActivity:
		h.lastActivitySeq = ev.Seq
		return
	case EventTranscript:
		h.lastTranscriptSeq = ev.Seq
		return
	case EventTransferProgress:
		h.lastProgressSeq = ev.Seq
		return
	case EventTransferFile:
		h.lastFileSeq = ev.Seq
		return
	case EventLayoutChanged:
		h.lastLayoutSeq = ev.Seq
		return
	case EventTML:
		h.lastTMLSeq = ev.Seq
		return
	case EventTMLSurface:
		h.lastTMLSurfaceSeq = ev.Seq
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

func (h *eventHub) bootIdentity() string {
	return h.bootID
}

func (h *eventHub) unsubscribe(sub *eventSub) {
	if sub == nil {
		return
	}
	h.mu.Lock()
	delete(h.subs, sub)
	h.mu.Unlock()
	sub.close()
}

func (h *eventHub) currentSeq() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

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
