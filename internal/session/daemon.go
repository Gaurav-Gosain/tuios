package session

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/google/uuid"
)

// Daemon manages the persistent TUIOS server process.
// It owns PTYs and stores session state. Clients run the TUI.
type Daemon struct {
	manager *Manager
	// instance names this daemon run, fresh on every start. hello reports
	// it, and a hub records it for each host, so a client attached to a
	// session on another machine can tell which of that machine's hosts is
	// the daemon it came from. A host name cannot: each machine names the
	// others in its own [hosts] table.
	instance string
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc

	// Connection tracking
	clients   map[string]*connState
	clientsMu sync.RWMutex

	// layoutMu serialises the recalculation of what a session measures (its
	// effective size and its chrome reserve) so that a read over every client
	// and the write that follows it cannot interleave with another one. See
	// recalculateAndBroadcastSize.
	layoutMu sync.Mutex
	// windowSize is [daemon] window_size, resolved. A session's own override
	// from set-option wins over it. See window_size.go.
	windowSize string
	// latest is each session's latest client, for the latest policy.
	latest latestState
	// attachCount hands out connState.attachSeq.
	attachCount atomic.Uint64

	// Pending requests: maps requestID to the client that made the request
	// Used to route command results back to the original requester
	pendingRequests   map[string]*pendingRequest
	pendingRequestsMu sync.RWMutex

	// events is the control-plane event hub backing the subscribe verb's event
	// stream and the wait-for verb's blocking waits.
	events *eventHub

	// hooks is the session-side hook table: the commands the daemon runs when a
	// window, a workspace or an agent state changes. It is nil when no hooks are
	// configured, so a daemon without hooks pays one nil check per event. See
	// daemon_hooks.go.
	hooks *hooks.Manager
	// agentAlerts is the [notifications.agent] policy the after-agent-state hook
	// is gated by. It is the same policy the client applies to the dock message
	// and the sound, read from the same config table.
	agentAlerts config.AgentAlertPolicy
	// agentHooks holds the settle timers for after-agent-state. Nil when hooks
	// are not loaded.
	agentHooks *agentHookGate
	// federationMu guards federationProblems and the hosts watcher, both of
	// which a config reload rewrites while a verb is reading them.
	federationMu sync.Mutex
	// hostsWatcher follows the config file so an edit to the [hosts] table
	// reaches the links without a restart. Nil when no config path is set,
	// which is every test that builds a DaemonConfig by hand.
	hostsWatcher *config.Watcher
	// configPath is the file hostsWatcher follows.
	configPath string
	// grantsWidenedAtStart is set when panes on the default hold more at
	// this start than at the last run (checkGrantsSinceLastRun).
	grantsWidenedAtStart atomic.Bool
	// pastes holds the images the person pasted into panes. See
	// paste_image.go.
	pastes *pasteStore

	// Goroutine tracking for clean shutdown. Start a tracked goroutine with
	// goTracked, never with wg.Add or wg.Go directly: see goTracked.
	wg sync.WaitGroup
	// wgMu guards wgClosed. shutdown sets wgClosed before it waits on wg, and
	// goTracked refuses to register once it is set.
	wgMu     sync.Mutex
	wgClosed bool

	// shutdownOnce makes shutdown idempotent (Run and Stop can both call it).
	shutdownOnce sync.Once

	// startLock is the exclusive lock that serialises daemon startup. It is held
	// open for the daemon's life, since releasing it early would let a second
	// starter reach the stale-socket recovery while this one is still listening.
	startLock *os.File

	// Configuration
	version string

	// disableAutoRestore, when true, skips cold-start resurrection of saved
	// sessions on daemon start. Sessions can still be brought back on demand
	// with the resurrect verb.
	disableAutoRestore bool

	// foreground and logFile are the logging settings this daemon was started
	// with. Run installs them; see logsink.go.
	foreground bool
	logFile    string

	// approvalPeer places the process on a connection in a pane, for
	// request-approval, restrict-connection and fan's launched_from. Unset
	// uses peerPaneWindow; a test sets it to stand in for a process table.
	//
	// It is atomic because a test swaps it while the daemon runs, and the
	// connection goroutines read it with nothing else ordering the two: the
	// reply a test waits on is written with writev, which gives the race
	// detector no happens-before edge.
	approvalPeer atomic.Pointer[peerPlacer]

	// The state of the features tuios-slim leaves out. See
	// daemon_features_full.go, and daemon_features_slim.go for the empty slim
	// counterpart.
	daemonFeatures
}

// defaultAgentStallTimeout is the conservative default silence window before a
// pane that reported working but never reported anything after is assumed idle.
// It is long on purpose: the heuristic is a fallback for agents that do not
// report, and demoting a genuinely-busy-but-quiet pane too eagerly is worse than
// leaving it looking busy a little longer.
const defaultAgentStallTimeout = 30 * time.Second

// defaultAgentDetectInterval is how often the foreground-process auto-detector
// polls each pane. It is modest on purpose: agent presence changes on a human
// timescale, and a per-pane /proc read every couple of seconds is cheap.
const defaultAgentDetectInterval = 2 * time.Second

// agentDetectQuietBound is the longest a quiet pane goes between two reads of
// its foreground process. A pane that has printed nothing since it was last
// read, and holds no agent, is read only every few ticks: nothing a person
// starts in it goes unechoed, so output is what says it is worth reading
// again, and this bound covers a program started with no output at all. At the
// default two-second tick it is every fifth tick. See PTY.detectScanDue.
const agentDetectQuietBound = 10 * time.Second

// pendingRequest tracks a routed command awaiting its result, with the time it
// was created so cleanupLoop can expire stale entries.
//
// A request is delivered one of two ways when the TUI replies:
//   - requester != nil: the result is forwarded to that connection as a normal
//     MsgCommandResult (the binary control path).
//   - resultCh != nil: the result is handed to a goroutine blocked in
//     routeToTUISync (the JSON verb path), which then writes the JSON response.
type pendingRequest struct {
	requester *connState
	resultCh  chan *CommandResultPayload
	created   time.Time
}

// connState tracks state for a connected client.
type connState struct {
	// dirWatch is the one folder watch MsgWatchDir keeps for this
	// connection. See daemon_dirwatch.go.
	dirWatch dirWatchSlot

	conn     net.Conn
	clientID string
	hello    *HelloPayload
	done     chan struct{}
	doneOnce sync.Once // gates close(done) so shutdown is safe to call twice
	sendMu   sync.Mutex

	// Broadcasts to this client are written in the order they were made.
	// broadcastToSession hands each one a ticket while it still holds the
	// clients lock, so the tickets are in broadcast order, and the goroutine
	// that writes a broadcast waits for its turn. The write itself stays off
	// the broadcaster's goroutine, so a slow client still stalls nobody but
	// its own queue. Without the tickets two pushes from one client could
	// reach a peer swapped, and a peer adopting the older tree last kept a
	// layout the session had moved on from.
	bcastMu   sync.Mutex
	bcastCond *sync.Cond // built on first use, under bcastMu
	bcastNext uint64     // the next ticket handed out
	bcastDone uint64     // broadcasts written so far

	// pushOrigin is the name this connection's state pushes carry (see
	// SessionState.PushOrigin), guarded by mu. It is remembered so the
	// session's push table can drop the entry when the client leaves.
	pushOrigin string

	// mu guards the mutable per-connection fields below (sessionID, width,
	// height, isTUIClient, ptySubscriptions). These are written on this
	// connection's own goroutine and read from other goroutines (PTY exit
	// callbacks, size recalculation, command routing). Lock ordering: readers
	// that also hold d.clientsMu always take d.clientsMu first, then cs.mu; no
	// path takes cs.mu then d.clientsMu.
	mu               sync.Mutex
	sessionID        string // Session they're attached to
	ptySubscriptions map[string]struct{}
	// ptyResume is where each PTY's stream had got to when this client last
	// unsubscribed, so hiding and showing a pane resumes rather than replays.
	// It lives on the connection because that is what owns its lifetime: the
	// positions go away with the client instead of accumulating on the PTY.
	ptyResume map[string]int64

	// Event stream state (JSON verb protocol). eventSub is the hub subscription
	// once this connection has issued a subscribe verb; streaming guards against a
	// second subscribe on the same connection; pendingStream hands the fresh
	// subscription from the subscribe handler to the dispatch loop, which starts
	// the streamer only after the ack response has been written.
	eventSub      *eventSub
	pendingStream *eventSub
	streaming     bool

	// isTUIClient indicates this is a full TUI client (vs a control client)
	// TUI clients can receive and execute remote commands
	isTUIClient bool
	// treeOps says the client's hello offered MsgLayoutTree. Set once, at
	// hello. See Daemon.refreshTreeOps.
	treeOps bool
	// scratchWS says the client's hello offered scratch workspaces. See
	// Daemon.refreshTreeOps.
	scratchWS bool
	// windowSizeCap says the client's hello offered WindowSize: it reports
	// activity and can draw a session larger than itself. Set at hello,
	// read under mu. See window_size.go.
	windowSizeCap bool
	// lastActivity is when the person at this client last gave input, from
	// MsgClientActivity. Guarded by mu.
	lastActivity time.Time
	// attachSeq orders the clients by when they attached, for the latest
	// policy when no client has had input. Guarded by mu.
	attachSeq uint64
	// viewOnly says the client's attach marked it as sending no input. Under
	// largest and latest it does not count toward the session's size.
	// Guarded by mu.
	viewOnly bool

	// takeover, when a verb sets it, runs after that verb's reply line has been
	// written and owns the connection from then on; the JSON loop returns
	// when it does. open-host-connection sets it to relay the connection to
	// another machine's daemon.
	takeover func(br *bufio.Reader)

	// replyFailed, when a verb sets it, runs if writing that verb's reply
	// fails, which is how a call that waited learns its caller is gone.
	// ask-human sets it so an answer is mailed rather than lost. It is
	// cleared after every reply.
	replyFailed func()

	// viaLink says this connection was accepted on the link socket, which
	// only the proxy on this machine dials, for a stream that came in over a
	// hub's link. It is a fact about the connection, not a claim in any
	// message: whatever arrives on it was written on another machine, and
	// the mailbox marks what it stores from it as such.
	viaLink bool

	// linkHuman says this connection was accepted on the link-human socket:
	// it came over a hub's link, and the hub vouched that the process that
	// opened it on the hub's machine is not inside one of the hub's panes. It
	// implies viaLink. See human_origin.go.
	linkHuman bool

	// linkPeer is the machine a link connection came from, as the link-peer
	// handshake named it; empty when it named none. linkPeerSet says the
	// handshake ran, linkPinned that the name was pinned on this machine with
	// stdio-proxy --as, and linkServed that something other than the
	// handshake has run, after which the peer can no longer be named. All
	// four are guarded by mu. See link_policy.go.
	linkPeer    string
	linkPeerSet bool
	linkPinned  bool
	linkServed  bool

	// peerPID is the pid of the process on the other end, as the kernel
	// recorded it at connect time, or 0 where the platform does not say. For
	// a link connection it is the pid of this machine's stdio-proxy.
	peerPID int
	// fromPane caches paneOrigin for peerPID, computed the first time a call
	// needs it. See mayActAsHuman.
	fromPaneOnce sync.Once
	fromPane     bool
	// placedIn and placedWhy cache placeClient: the session
	// whose pane shows this client, by ID, and which test said so. Guarded by
	// mu. See nested_attach.go.
	placedIn  string
	placedWhy string
	// fromPaneWhy is the paneOrigin reason for fromPane.
	fromPaneWhy string
	// peerStart is when the process peerPID named started, read at accept,
	// and peerStartOK whether it could be read. Together with peerPID they
	// pin one process: a pid reused after that process exits has another
	// start time (peerChanged).
	peerStart   uint64
	peerStartOK bool
	// paneOnly marks a call a pane on another machine sent through its report
	// channel (hosted_calls.go). It is a pane by construction, whatever the
	// pid says, so it can never act as the person.
	paneOnly bool
	// scope is what restrict-connection narrowed this connection to, nil for
	// a connection that never called it. It only ever narrows. See
	// conn_scope.go.
	scope atomic.Pointer[connScope]
	// paneBound is the pane this connection presented the token of with
	// pane-grants, nil when it presented none. panePlaced caches the pane the
	// kernel placed the caller in, once it named one. paneView is the pane
	// and grants the last checked call ran under, which the event stream is
	// filtered by. See pane_grants.go.
	paneBound  atomic.Pointer[string]
	panePlaced atomic.Pointer[string]
	paneView   atomic.Pointer[paneAuth]
	// hostedEnded, on a paneOnly call, is closed when the report channel the
	// call came on ends. A wait-for stops on it, so a wait does not outlive
	// the channel that would carry its answer. nil means it never ends.
	hostedEnded <-chan struct{}

	// attached says the attach reply has been written to this connection, and
	// it is what broadcastToSession requires before it will send anything.
	//
	// sessionID is set at the top of handleAttach, because everything that
	// measures the session (the client count, the effective size, the chrome
	// reserve) has to include the joining client from that moment. But a
	// client is not ready to be spoken to until it has been told it is
	// attached: it is still inside its attach call reading the one reply it
	// asked for, with no read loop yet, so an unsolicited message arriving
	// first is read as that reply and fails the attach.
	//
	// The window between the two is short and was harmless for as long as
	// nothing broadcast into it. It is not the sort of thing to leave resting
	// on that.
	attached bool

	// missedStateSync says a state sync was broadcast to this client's session
	// while the client was in that window, so the broadcast skipped it. The
	// attach handler reads it after the reply and sends the current state when
	// it is set. Guarded by mu, and cleared at the top of every attach.
	//
	// A change that reaches the client by broadcast does not set it. The
	// handler used to compare the state before and after the reply instead,
	// and that also counted every change made after the reply, which the
	// client was already being sent: a peer pushing the moment the attach
	// returned had its first pushes sent to this client twice, and the second
	// copy went outside the broadcast order and could arrive ahead of the
	// pushes before it.
	missedStateSync bool

	// humanNonce is the secret handed to this client in its attach reply, and
	// replaced on every attach. A mail reply signed with it, from=human, is
	// stored as verified_human; see verifyHumanNonce. Guarded by mu.
	humanNonce string

	// hostFocus is what this client last said about its host terminal's
	// focus: focusUnknown until it says anything. Guarded by mu. See
	// client_focus.go.
	hostFocus hostFocus

	// Client terminal dimensions (for multi-client size calculation)
	width  int
	height int
	// reserve is the chrome this client draws around the panes. The session's
	// reserve is the largest any of its clients asks for, so that the box the
	// panes are partitioned into is the same on every client. See
	// LayoutReserve.
	reserve LayoutReserve

	// Client's terminal graphics capabilities (pixel dimensions, etc.)
	// Used to set proper PTY pixel sizes for tools like kitty icat
	pixelWidth    int
	pixelHeight   int
	cellWidth     int
	cellHeight    int
	kittyGraphics bool
	sixelGraphics bool
	// kittyAnimation is HelloPayload.KittyAnimation.
	kittyAnimation bool
	terminalName   string
}

// DaemonConfig holds configuration for starting the daemon.
type DaemonConfig struct {
	Version    string
	SocketPath string
	// Foreground is true for `tuios daemon`, which owns a terminal. A foreground
	// daemon echoes its log to stderr; a background one does not, because its
	// stderr belongs to the process that spawned it.
	Foreground bool
	// LogFile is the file the daemon appends its log to. Empty, the default,
	// means DefaultDaemonLogPath.
	LogFile string
	// DisableAutoRestore skips restoring saved sessions on daemon start.
	DisableAutoRestore bool
	// ScrollbackLines is how many lines of history every pane the daemon makes
	// keeps, from appearance.scrollback_lines. Zero means the default. A pane
	// takes it when it is made and keeps it, so a change applies to panes made
	// after the daemon next reads its config.
	ScrollbackLines int
	// NewWindowInheritCwd starts a new window in the focused pane's working
	// directory, from appearance.new_window_inherit_cwd. The daemon is the side
	// that spawns the shell, so it is the side that has to know.
	NewWindowInheritCwd bool
	// PreferredShell is appearance.preferred_shell: the shell a pane runs when
	// the client that made its session named none. Empty falls back to $SHELL
	// and then the platform default, the same order a standalone pane uses.
	PreferredShell string
	// HerdrProtocol is [agents] herdr_protocol: which panes are told about
	// the herdr protocol socket. See Manager.HerdrEnv.
	HerdrProtocol string
	// AgentStallTimeout overrides how long a pane may report working with no
	// output before the stall heuristic demotes it to idle. Zero falls back to
	// the TUIOS_AGENT_STALL_SECONDS environment override, then to the default; a
	// negative value disables the heuristic.
	AgentStallTimeout time.Duration
	// AgentAutoDetect toggles the foreground-process agent auto-detector. Nil
	// falls back to the TUIOS_AGENT_AUTODETECT environment override, then to
	// enabled. A non-nil false disables it.
	AgentAutoDetect *bool
	// AgentDetectInterval overrides the auto-detector's poll interval. Zero falls
	// back to the TUIOS_AGENT_DETECT_SECONDS environment override, then to the
	// default; a negative value disables auto-detection.
	AgentDetectInterval time.Duration
	// AgentBinaries are extra binary names to treat as agents, merged with the
	// built-in defaults. It also picks up the TUIOS_AGENT_BINARIES environment
	// override (comma-separated).
	AgentBinaries []string
	// ResumeAgents is daemon.resume_agents: what a restore does with the agent
	// conversations its panes were running. "ask" and anything unrecognised,
	// including empty, opens a resume item in the Inbox for each; "auto" types
	// the resume command into each restored shell; "off" does neither. See
	// agent_resume.go.
	ResumeAgents string
	// History is daemon.persist_scrollback and its bounds, resolved: whether
	// each pane's history is saved with its session and shown again when a
	// restart restores it. The zero value saves nothing; a real starter fills
	// it through DaemonConfigFromUser.
	History HistoryPolicy
	// Hosts are the federated peers from the [hosts] config table. Empty, the
	// default, means the daemon holds no links and every federation verb reports
	// an empty table.
	Hosts []federation.Host
	// Hooks is the [hooks] table from the user config. Empty, the default, means
	// the daemon fires no hooks and a detached session runs no commands.
	Hooks map[string]any
	// AgentAlerts is the [notifications.agent] table. The after-agent-state hook
	// is gated by it, exactly as it is in the client.
	AgentAlerts *config.AgentAlertsConfig
	// AgentHookCommand is [notifications.agent].command, the shorthand spelling
	// of an after-agent-state hook.
	AgentHookCommand string
	// ConfigPath is the user config file the daemon follows for changes to the
	// [hosts] table. DaemonConfigFromUser fills it, so every real starter has
	// it and a hand-built config in a test does not.
	ConfigPath string
	// HostDial opens the transport to a host. Nil, the default, runs ssh. A
	// test sets it to reach a second daemon in the same process without an ssh
	// server.
	HostDial federation.Dialer
	// RespondFromShell is [daemon] respond_from_shell: let a caller whose
	// process the kernel names, and which runs outside every pane, answer a
	// prompt with respond without an attach nonce. False, the default, leaves
	// respond to a client attached right now. See verb_respond.go.
	RespondFromShell bool
	// Approvals is the [agents.approvals] table. The zero value is the
	// default: no harness holds a prompt for the Inbox.
	Approvals ApprovalPolicy
	// LinkPolicies is the [hosts] table as the machine a link arrives at reads
	// it: what each machine linked to this one may do here. Nil gives every
	// link the built-in default. See link_policy.go.
	LinkPolicies map[string]config.HostConfig
	// RecapTestPatterns is [agents.recap] test_patterns: the commands an
	// agent-activity recap reads as a test run. Nil means the defaults.
	RecapTestPatterns []string
	// Permissions is [agents.permissions]: what a pane started with no
	// grants of its own may do through tuios. The zero value is mode open,
	// under which such a pane holds admin, what every pane held before
	// grants existed. See pane_grants.go.
	Permissions config.ResolvedPermissions
	// WindowSize is [daemon] window_size: smallest, largest or latest. An
	// empty or unknown value is smallest. See window_size.go.
	WindowSize string
	// QueueMax is [agents.queue] max: how many messages one pane's delivery
	// queue holds. Zero means the default. See agent_queue.go.
	QueueMax int
}

// NewDaemon creates a new daemon instance.
func NewDaemon(cfg *DaemonConfig) *Daemon {
	ctx, cancel := context.WithCancel(context.Background())

	d := &Daemon{
		manager:            NewManager(),
		instance:           uuid.NewString(),
		ctx:                ctx,
		cancel:             cancel,
		clients:            make(map[string]*connState),
		pendingRequests:    make(map[string]*pendingRequest),
		events:             newEventHub(),
		version:            cfg.Version,
		disableAutoRestore: cfg.DisableAutoRestore,
		foreground:         cfg.Foreground,
		logFile:            cfg.LogFile,
		windowSize:         windowSizePolicy(cfg.WindowSize),
	}
	// The agent, attention, queue, stash and herdr state. tuios-slim has
	// stand-ins that do nothing. See daemon_features_slim.go.
	d.agents = newAgentBus()
	d.agentStallTimeout = resolveAgentStallTimeout(cfg.AgentStallTimeout)
	d.agentMatcher = newAgentMatcher(resolveAgentBinaries(cfg.AgentBinaries))
	d.respondFromShell = cfg.RespondFromShell
	d.resumeAgents = resolveResumeMode(cfg.ResumeAgents)
	d.attention = newAttentionStore(d.events.publish, d.events.currentSeq)
	d.SetApprovalPolicy(cfg.Approvals)
	d.activity = newActivityStore(d.events.publish)
	d.SetRecapTestPatterns(cfg.RecapTestPatterns)
	d.SetLinkPolicies(cfg.LinkPolicies)
	d.SetQueueMax(cfg.QueueMax)
	d.outbox = newHostOutbox(d)
	// The socket path is read through a closure rather than copied, because
	// it may still change and the stash root is derived from it.
	d.stash = newStashStore(func() string { return d.manager.SocketPath() })
	d.manager.SetHerdrProtocol(cfg.HerdrProtocol)
	d.agentDetectInterval = resolveAgentDetectInterval(cfg.AgentAutoDetect, cfg.AgentDetectInterval)

	// A daemon with no watcher is a working daemon: every transcript join falls
	// back to reading on its pane's own output. So the error is dropped rather
	// than reported, and it is dropped rather than logged because the only thing
	// fsnotify would say is which path it failed on.
	if w, err := NewTranscriptWatcher(); err == nil {
		d.transcriptWatcher = w
	}
	d.manager.SetPanePermissions(cfg.Permissions)
	d.pastes = newPasteStore(func() string { return d.manager.SocketPath() })
	d.manager.SetScrollbackLines(cfg.ScrollbackLines)
	d.manager.SetHistoryPolicy(cfg.History)
	d.manager.SetNewWindowInheritCwd(cfg.NewWindowInheritCwd)
	d.manager.SetPreferredShell(cfg.PreferredShell)
	d.loadHooks(cfg)

	if cfg.SocketPath != "" {
		d.manager.SetSocketPath(cfg.SocketPath)
	}

	// Wire session lifecycle to the event hub: every session created through the
	// manager gets an event sink that publishes to the hub, and creation/deletion
	// raise session lifecycle events.
	d.manager.SetSessionHooks(d.onSessionCreated, d.onSessionDeleted)
	d.manager.SetRenameHook(d.onSessionRenamed)

	d.configPath = cfg.ConfigPath
	d.hostDial = cfg.HostDial
	d.fleet = newHostFleet(d)
	d.setupFederation(cfg.Hosts)

	return d
}

// onSessionCreated installs a session's event and state sinks and publishes a
// session-created event. It runs on the manager's create hook.
func (d *Daemon) onSessionCreated(s *Session) {
	name := s.Name()
	// Every daemon-side mutation reaches the attached clients from here, so a
	// change the daemon made itself shows up in a live TUI without the verb that
	// made it knowing a client exists. Source is empty because the daemon, not a
	// client, is the origin: every attached client needs to hear it.
	sessionID := s.ID
	if d.transcriptWatcher != nil {
		s.SetTranscriptWatcher(d.transcriptWatcher)
	}
	// The links a window on another machine is opened over. The nil check is
	// not defensive noise: d.federation is a typed pointer, and handing a nil
	// one to an interface parameter makes an interface that is not nil and
	// panics on first use, so the session would think it had links.
	if d.federation != nil {
		s.SetFederation(d.federation)
	}
	// A window opened on another machine gets a report channel, so an agent
	// in it can report its state and read its mail. See hosted_calls.go.
	s.SetRemotePaneHook(func(windowID string, p *remotePane) {
		if d.ctx.Err() != nil {
			return
		}
		d.goTracked(func() {
			d.serveHostedCalls(s, windowID, p)
		})
	})
	s.SetStateSink(func(state *SessionState) {
		d.broadcastStateSync(sessionID, state, "update", "")
	})
	s.SetEventSink(func(ev SessionEvent) {
		// Read per event, not once at creation: a rename changes it, and
		// every record made from here must carry the name the session has now.
		name := s.Name()
		// A pane's own output is the signal that its agent quit: when the agent
		// leaves the foreground the shell prompt returns as output, so probe that
		// pane and clear an auto-detected glyph at once rather than waiting for the
		// next detection poll. Throttled per PTY so a busy pane pays no cost.
		if ev.Type == EventOutput {
			if pty := s.GetPTY(ev.PTYID); pty != nil {
				d.onPaneOutput(s, ev, pty)
			}
		}
		// A pane seen, or a new kind or message on a pane whose state did not
		// change, is news for the Inbox only: it is not a stream event and
		// raises no hook.
		if ev.Type == eventCompletionSeen || ev.Type == eventAttentionDetail || ev.Type == eventPaneFocused {
			d.attention.noteSessionEvent(name, ev)
			return
		}
		// A pane with an activity ring gets its shell's commands and its
		// state changes added to it. A pane without one costs a map lookup.
		d.activity.noteSessionEvent(s, ev)
		// Hooks run before the fan-out because a hook is a side effect of the
		// fact and a subscriber is a reader of it. Fire itself only starts
		// goroutines, so nothing here waits on a command.
		d.fireSessionHooks(s, ev)
		// The event goes out before the Inbox change it causes, so a
		// subscriber sees the transition and then the item it opened.
		defer d.attention.noteSessionEvent(name, ev)
		d.noteQueueEvent(name, ev)
		d.reviewNotes.noteSessionEvent(ev)
		d.events.publish(streamEvent{
			Type:       ev.Type,
			Session:    name,
			Window:     ev.Window,
			PTYID:      ev.PTYID,
			Title:      ev.Title,
			Body:       ev.Body,
			Bytes:      ev.Bytes,
			Mode:       ev.Mode,
			Enabled:    ev.Enabled,
			State:      ev.State,
			Workspace:  ev.Workspace,
			Cmdline:    ev.Cmdline,
			ExitCode:   ev.ExitCode,
			DurationMS: ev.DurationMS,
			CommandSeq: ev.CommandSeq,
		})
	})
	d.events.publish(streamEvent{Type: EventSessionCreated, Session: name})
}

// Start starts the daemon.
func (d *Daemon) Start() error {
	socketPath := d.manager.SocketPath()

	// Held until shutdown so the stale-socket recovery below can never run
	// against a daemon that is itself between bind and listen.
	startLock, err := acquireStartLock(socketPath)
	if err != nil {
		return err
	}
	d.startLock = startLock
	defer func() {
		if d.listener == nil {
			d.releaseStartLock()
		}
	}()

	if _, err := os.Stat(socketPath); err == nil {
		if isDaemonRunningAt(socketPath) {
			return fmt.Errorf("daemon already running at %s", socketPath)
		}
		if err := os.Remove(socketPath); err != nil {
			return fmt.Errorf("failed to remove stale socket: %w", err)
		}
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}
	// A *net.UnixListener unlinks its socket file on Close by default, and
	// shutdown closes the listener first, which silently unlinked the socket
	// at the top of shutdown, while every session's state was still unsaved.
	// The socket file is the signal WaitForDaemonShutdown and 'tuios
	// kill-server' rely on, and the whole contract is that it disappears last,
	// in shutdown's own explicit Remove. Under load the early unlink was
	// observable: the wait returned mid-shutdown and read state that was not
	// yet on disk.
	if ul, ok := listener.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	d.listener = listener

	if err := os.Chmod(socketPath, 0700); err != nil { //nolint:gosec // a socket, owner only; the execute bit means nothing on it
		_ = listener.Close()
		_ = os.Remove(socketPath) // Close no longer unlinks; a failed start must not leave a stale socket
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	// The link socket. It is optional: a daemon that cannot bind it still
	// serves, and the proxy falls back to the main socket, at the cost of a
	// message from another machine not being marked as one. The start lock
	// is held, so a stale file here is a dead daemon's and is removed.
	// The link sockets other machines reach this daemon through, and the
	// herdr socket. tuios-slim opens none of them.
	d.listenFeatureSockets(socketPath)

	if err := d.writePidFile(); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath) // Close no longer unlinks; a failed start must not leave a stale socket
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	log.Printf("TUIOS daemon started on %s (PID %d)", socketPath, os.Getpid())

	// Sweep the previous daemon's leftovers before reading the directory. Runs
	// whether or not auto-restore is on, since the residue is there either way.
	CleanResurrectionDir()

	// The stash root is cleared before anything is served. A daemon that was
	// killed rather than stopped could not delete its own stashed files, so this
	// is where they go. A restored session does not get its old stash back, for
	// the same reason it does not get its old mail: its panes are new processes.
	d.stash.sweep()
	// Pasted images a killed daemon left behind go once they are past their
	// TTL. A standalone client may share the directory, so only expired
	// ones are taken.
	d.pastes.sweep()

	// Restore sessions saved before the previous shutdown/crash before we start
	// accepting clients, so an attach immediately after start finds them. Runs
	// synchronously; a single corrupt file is archived and skipped, never fatal.
	d.dropHistoryWhenOff()
	if !d.disableAutoRestore {
		d.restoreAllSessions()
	}

	// The Inbox comes back after the sessions do, since what it keeps is
	// decided by which sessions and panes came back.
	d.attention.load(attentionPath(), d.attentionLive)
	d.checkGrantsSinceLastRun()
	// A host entry dropped at start is said now, when the Inbox can show it.
	d.noteHostProblems(d.configProblems())
	// Mail still waiting for another machine comes back with its Inbox items,
	// and goes when that machine's link comes up.
	d.outbox.load(outboxPath())
	// Review notes come back for the panes that came back.
	d.reviewNotes.load(reviewNotesPath(), func(window string) bool { return d.sessionOfWindow(window) != "" })

	// The resume offers the restore found are opened only now, after the saved
	// Inbox items took their ids, so an offer never shares an id with one.
	d.applyResumeOffers(d.pendingResumes)
	d.pendingResumes = nil

	// The links come up in the background. Start returns at once whatever the
	// remote machines are doing, so a host that is powered off cannot delay the
	// daemon's own socket by one millisecond.
	if d.federation != nil {
		d.federation.Start(d.ctx)
	}
	// Each linked host's agents are followed from here on. See host_fleet.go.
	d.fleet.start(d.ctx)
	// The config file is followed from here on, so a host added while the daemon
	// runs reaches the links without a restart.
	d.startHostsWatch()

	go d.handleSignals()
	go d.acceptLoop()
	if d.linkListener != nil {
		go d.acceptLinkLoop()
	}
	if d.linkHumanListener != nil {
		go d.acceptLinkOn(d.linkHumanListener, true)
	}
	if d.herdrListener != nil {
		go d.acceptHerdrLoop(d.herdrListener)
	}
	go d.cleanupLoop()
	go d.stallMonitor()
	go d.agentMonitor()

	return nil
}

// Run starts the daemon and blocks until shutdown.
func (d *Daemon) Run() error {
	// The daemon process installs this before it builds the daemon, so this
	// call is normally a no-op. It stays as the backstop for a Run reached any
	// other way. Only Run installs it: an in-process daemon inside a client
	// calls Start, and must not take over that client's standard logger.
	InstallDaemonLogging(d.foreground, d.logFile)
	LogBasic("Daemon %s starting (pid %d, log level %s)", d.version, os.Getpid(), GetDebugLevel())

	if err := d.Start(); err != nil {
		return err
	}
	<-d.ctx.Done()
	return d.shutdown()
}

// goTracked runs f on a new goroutine that shutdown waits for. It returns
// false, and does not run f, once shutdown has started waiting: the
// sync.WaitGroup rules forbid an Add that starts at a zero count from running
// concurrently with Wait. Work refused here belongs to a daemon that is going
// away, so callers drop it.
func (d *Daemon) goTracked(f func()) bool {
	d.wgMu.Lock()
	defer d.wgMu.Unlock()
	if d.wgClosed {
		return false
	}
	d.wg.Go(f)
	return true
}

// Stop signals the daemon to stop and performs cleanup.
func (d *Daemon) Stop() {
	d.cancel()
	_ = d.shutdown()
}

// closeDone closes cs.done exactly once, even if shutdown races the connection
// goroutine.
func (cs *connState) closeDone() {
	cs.doneOnce.Do(func() { close(cs.done) })
}

// takeBroadcastTicket reserves this client's next place in the broadcast
// order. It is called by the broadcaster, so the tickets are handed out in
// the order the broadcasts were made.
func (cs *connState) takeBroadcastTicket() uint64 {
	cs.bcastMu.Lock()
	defer cs.bcastMu.Unlock()
	ticket := cs.bcastNext
	cs.bcastNext++
	return ticket
}

// awaitBroadcastTurn blocks until every broadcast with an earlier ticket has
// been written.
func (cs *connState) awaitBroadcastTurn(ticket uint64) {
	cs.bcastMu.Lock()
	defer cs.bcastMu.Unlock()
	if cs.bcastCond == nil {
		cs.bcastCond = sync.NewCond(&cs.bcastMu)
	}
	for cs.bcastDone != ticket {
		cs.bcastCond.Wait()
	}
}

// finishBroadcast releases the next ticket. It runs whether the write
// succeeded or not, so a dropped client cannot wedge the broadcasts behind it.
func (cs *connState) finishBroadcast() {
	cs.bcastMu.Lock()
	defer cs.bcastMu.Unlock()
	cs.bcastDone++
	if cs.bcastCond != nil {
		cs.bcastCond.Broadcast()
	}
}

// drop tears a client down after an unrecoverable send failure. A write that
// fails mid-frame (e.g. a slow client hitting the write deadline) leaves a
// partial frame on the wire and permanently desyncs framing, so the only
// coherent recovery is to close done and the connection: that unblocks the read
// loop, whose deferred cleanup then unsubscribes every PTY, removes the client,
// and purges its pending requests. Safe to call from any goroutine and more than
// once (closeDone is once-guarded and Close is idempotent).
func (cs *connState) drop() {
	cs.closeDone()
	_ = cs.conn.Close()
}

func (d *Daemon) shutdown() error {
	d.shutdownOnce.Do(func() {
		log.Println("Shutting down daemon...")

		// The Inbox is saved first and not again. Everything below closes
		// panes, and a pane closing on shutdown is not the person dealing
		// with what was waiting in it.
		d.attention.saveNowAndFreeze()
		d.reviewNotes.saveNowAndFreeze()

		if d.listener != nil {
			_ = d.listener.Close()
		}
		if d.linkListener != nil {
			_ = d.linkListener.Close()
			_ = os.Remove(LinkSocketPath(d.manager.SocketPath()))
		}
		if d.linkHumanListener != nil {
			_ = d.linkHumanListener.Close()
			_ = os.Remove(LinkHumanSocketPath(d.manager.SocketPath()))
		}
		if d.herdrListener != nil {
			d.manager.SetHerdrSocket("")
			_ = d.herdrListener.Close()
			_ = os.Remove(HerdrSocketPath(d.manager.SocketPath()))
		}

		// Closing the watcher ends its goroutine and returns every inotify watch
		// to the kernel, which matters on a machine where several daemons have
		// come and gone.
		_ = d.transcriptWatcher.Close()

		// The config watch ends before the links do, so a save landing during
		// shutdown cannot dial a host the daemon is about to drop.
		d.stopHostsWatch()

		// Panes running here on another machine's behalf end with the daemon
		// that was relaying them. Their owner is on the far side of a link
		// that is about to go, so a survivor would be a shell on a pty nobody
		// can reach.
		d.closeHostedPanes()

		// The streams that follow the linked hosts end before the links do,
		// so none of them reads a closing link as a host going away.
		d.fleet.stop()

		// Every ssh child is killed here. A link left running would outlive the
		// daemon that owns it.
		if d.federation != nil {
			d.federation.Stop()
		}

		// Parked agent-state firings are dropped and the hooks already running
		// are given a moment to finish. Hooks run in their own goroutines, which
		// the process exit would otherwise discard unrun, and the timeout is
		// what keeps a hook that never returns from holding the daemon open.
		if d.agentHooks != nil {
			d.agentHooks.stop()
		}
		if d.hooks != nil {
			d.hooks.WaitTimeout(hookDrainTimeout)
		}

		d.clientsMu.Lock()
		for _, cs := range d.clients {
			cs.closeDone()
			_ = cs.conn.Close()
		}
		d.clients = make(map[string]*connState)
		d.clientsMu.Unlock()

		// No goroutine registers with wg from here on. An Add that starts at a
		// zero count must happen before Wait, and a connection handler that
		// was still finishing a verb could otherwise start one during it.
		d.wgMu.Lock()
		d.wgClosed = true
		d.wgMu.Unlock()

		// Wait for goroutines with timeout
		done := make(chan struct{})
		go func() {
			d.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			log.Println("All goroutines exited cleanly")
		case <-time.After(5 * time.Second):
			log.Println("Warning: goroutine shutdown timed out after 5s, forcing shutdown")
		}

		// Stopping the manager stops every session, and each session's Stop
		// writes its final resurrection state synchronously. When this returns,
		// everything that will be persisted has been persisted.
		d.manager.Shutdown()

		// Every stashed file goes now. Sessions are stopped above, so nothing is
		// still writing, and the promise the stash makes is that its files do not
		// outlive the daemon that owns them.
		d.stash.sweep()
		d.pastes.removeWritten()

		// Unlinking the socket is deliberately the last thing the daemon does,
		// after the final resurrection saves and after the pid file. It is the
		// signal 'tuios kill-server' waits on, so anything ordered after it
		// would make that signal a lie: a caller could observe the socket gone,
		// start a new daemon, and race a write from the old one. Closing the
		// listener is not a usable signal for the same reason, since it happens
		// at the top of shutdown while state is still unsaved.
		pidPath, err := GetPidFilePath()
		if err == nil {
			_ = os.Remove(pidPath)
		}

		_ = os.Remove(d.manager.SocketPath())

		// After the socket is gone, so the next daemon can only take the lock
		// once there is nothing left of this one to race.
		d.releaseStartLock()

		log.Println("Daemon shutdown complete")
	})
	return nil
}

// handleSignals is defined in platform-specific files:
// - daemon_unix.go for Unix/Linux/macOS
// - daemon_windows.go for Windows

func (d *Daemon) acceptLoop() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-d.ctx.Done():
				return
			default:
				log.Printf("Accept error: %v", err)
				continue
			}
		}
		go d.handleConnection(conn)
	}
}

// acceptLinkOn accepts on one of the two link sockets. human marks every
// connection from it as one the hub vouched for; see LinkHumanSocketPath.
func (d *Daemon) acceptLinkOn(l net.Listener, human bool) {
	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-d.ctx.Done():
				return
			default:
				log.Printf("Accept error on a link socket: %v", err)
				continue
			}
		}
		go d.handleConnectionOn(conn, true, human)
	}
}

// LinkSocketPath is the socket the local proxy dials for a connection that
// arrived over a hub's link, beside the daemon's own socket. The daemon marks
// everything accepted on it as from another machine. A proxy that finds no
// such socket dials the main one, which is what an older daemon has, so the
// two builds still link; the mark is then simply absent.
func LinkSocketPath(socketPath string) string {
	return socketPath + ".link"
}

// LinkHumanSocketPath is the socket the local proxy dials, instead of
// LinkSocketPath, for a stream the hub opened on behalf of a process it
// checked was not inside one of its own panes. A client attached through it
// can be issued the attach nonce that verifies a reply from human; one attached
// through the plain link socket cannot. The daemon still checks the process on
// this end, which is the proxy: a process inside one of this machine's panes
// that dials the socket itself is refused the same way. See human_origin.go.
//
// A proxy that finds no such socket dials the plain link socket, which is what
// a daemon from before it has. That daemon trusts every link attach as it
// always did.
func LinkHumanSocketPath(socketPath string) string {
	return socketPath + ".link-human"
}

// listenLinkSocket opens one of the optional link sockets, owner only, and
// returns nil after logging what is lost when it cannot. The start lock is
// held, so a stale file at path is a dead daemon's and is removed.
func listenLinkSocket(path, lost string) net.Listener {
	_ = os.Remove(path)
	ll, err := net.Listen("unix", path)
	if err != nil {
		log.Printf("The link socket %s could not be opened: %v. %s", path, err, lost)
		return nil
	}
	if ul, ok := ll.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	if err := os.Chmod(path, 0700); err != nil { //nolint:gosec // a socket, owner only; the execute bit means nothing on it
		_ = ll.Close()
		_ = os.Remove(path)
		log.Printf("The link socket %s could not be secured: %v. %s", path, err, lost)
		return nil
	}
	return ll
}

// shortID returns the first 8 bytes of s, or all of s if it is shorter. IDs
// reaching the daemon can be client-controlled and arbitrarily short, so a
// plain s[:8] slice would panic; this makes ID truncation for logs safe.
func shortID(s string) string {
	if len(s) < 8 {
		return s
	}
	return s[:8]
}

func (d *Daemon) handleConnection(conn net.Conn) {
	d.handleConnectionFrom(conn, false)
}

// lastClientID is the number in the last client id handed out.
var lastClientID atomic.Int64

// newClientID returns an id no other connection in this process has had. It
// is the time in nanoseconds, as ids always were, moved past the last one
// handed out: the clock on macOS only moves in microseconds, so two
// connections accepted in the same microsecond used to get the same id, and
// the second replaced the first in the client table.
func newClientID() string {
	for {
		last := lastClientID.Load()
		next := max(time.Now().UnixNano(), last+1)
		if lastClientID.CompareAndSwap(last, next) {
			return fmt.Sprintf("client-%d", next)
		}
	}
}

// handleConnectionFrom serves one connection. viaLink marks it as accepted on
// the link socket.
func (d *Daemon) handleConnectionFrom(conn net.Conn, viaLink bool) {
	d.handleConnectionOn(conn, viaLink, false)
}

// handleConnectionOn is handleConnectionFrom with the link-human mark.
func (d *Daemon) handleConnectionOn(conn net.Conn, viaLink, linkHuman bool) {
	// A panic on the untrusted client-parsed message surface must not take down
	// the daemon and every other session. Recover, log, and drop just this
	// client. Registered before the cleanup defer below so cleanup (which closes
	// the connection and unsubscribes) runs first on unwind; conn.Close here is
	// a defensive backstop for a panic before that defer is installed.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC in handleConnection: %v\n%s", r, debug.Stack())
			_ = conn.Close()
		}
	}()

	clientID := newClientID()

	cs := &connState{
		conn:             conn,
		clientID:         clientID,
		done:             make(chan struct{}),
		ptySubscriptions: make(map[string]struct{}),
		ptyResume:        make(map[string]int64),
		viaLink:          viaLink,
		linkHuman:        viaLink && linkHuman,
		// Read before a byte is served: the kernel's record of who connected,
		// which nothing the peer sends can change. See human_origin.go.
		peerPID: peerPID(conn),
	}
	// Placed now, while the process that connected is the one the pid
	// names. A process that hands the connection to another and exits
	// leaves its pid free for reuse; see peerChanged.
	d.pinPeer(cs)

	if viaLink {
		LogBasic("Client %s connected over a link", clientID)
	} else {
		LogBasic("Client %s connected", clientID)
	}

	d.clientsMu.Lock()
	d.clients[clientID] = cs
	d.clientsMu.Unlock()

	defer func() {
		LogBasic("Client %s disconnected", clientID)

		// Wake any per-connection background goroutines (the event streamer) so
		// they observe the disconnect and unwind, and release a lingering event
		// subscription if the streamer never started (e.g. the ack write failed).
		cs.closeDone()
		cs.stopDirWatch()
		cs.mu.Lock()
		sub := cs.eventSub
		cs.eventSub = nil
		cs.mu.Unlock()
		if sub != nil {
			d.events.unsubscribe(sub)
		}

		d.clientsMu.Lock()
		delete(d.clients, clientID)
		d.clientsMu.Unlock()

		// Snapshot subscriptions and session under cs.mu before unsubscribing.
		cs.mu.Lock()
		sessionID := cs.sessionID
		subs := make([]string, 0, len(cs.ptySubscriptions))
		for ptyID := range cs.ptySubscriptions {
			subs = append(subs, ptyID)
		}
		cs.mu.Unlock()

		// Unsubscribe from all PTYs
		if sessionID != "" {
			d.forgetPushes(cs, sessionID)
			if session := d.manager.GetSessionByID(sessionID); session != nil {
				for _, ptyID := range subs {
					if pty := session.GetPTY(ptyID); pty != nil {
						pty.Unsubscribe(clientID)
					}
				}
			}
		}

		// Purge any pending requests this client was waiting on so its
		// connState is not pinned forever.
		d.pendingRequestsMu.Lock()
		for id, pr := range d.pendingRequests {
			if pr.requester == cs {
				delete(d.pendingRequests, id)
			}
		}
		d.pendingRequestsMu.Unlock()

		// A client that drops without detaching has still left, and the effective
		// session size is the minimum over the clients that are here. Nothing
		// recomputed it on this path, so a browser tab closed on a phone left
		// every other client boxed into the phone's columns for good. Runs after
		// the client is out of d.clients, so the size is taken over the ones that
		// remain; a clean detach cleared sessionID above and does not come here.
		if sessionID != "" {
			d.notifyClientLeft(sessionID, clientID)
		}

		_ = conn.Close()
	}()

	// Wrap the connection so the first byte can be inspected without consuming
	// it from the binary read path. A JSON verb-protocol client sends a line
	// starting with '{' (or leading whitespace); a binary client's first byte is
	// the high byte of a big-endian length prefix, which is 0x00 or 0x01 for any
	// frame under the 16MB cap and so never collides with '{' or whitespace.
	br := bufio.NewReaderSize(conn, 64*1024)
	d.serveConnection(cs, br)
}

// serveConnection reads a connection from its next byte as JSON or binary and
// serves it until it ends. The link-peer handshake calls it again after its
// reply, so a connection that named its peer is served from scratch.
func (d *Daemon) serveConnection(cs *connState, br *bufio.Reader) {
	conn, clientID := cs.conn, cs.clientID
	if d.detectJSONClient(cs, br) {
		d.handleJSONConnection(cs, br)
		return
	}

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-cs.done:
			return
		default:
		}

		// No deadline between frames: the wait costs nothing until a frame
		// arrives or the connection is closed, and both drop and shutdown
		// close it. The body gets a deadline so a large payload cannot be cut
		// mid-frame and desync framing.
		msg, err := readMessageBufferedLimit(conn, br, 0, 30*time.Second, daemonFrameLimit)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			// A frame over its type's limit was skipped unread, so the stream
			// is still in step: tell the sender and go on serving it. See
			// wire_bounds.go.
			if _, ok := errors.AsType[*FrameTooLargeError](err); ok {
				LogError("Refused a message from %s: %v", clientID, err)
				_ = d.sendError(cs, ErrCodeInvalidMessage, "refused: "+err.Error())
				continue
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				// A body that stalled past its deadline. The frame is now cut
				// in the middle, so the stream cannot be resumed.
				LogError("Read timeout from %s mid-frame", clientID)
				return
			}
			LogError("Read error from %s: %v", clientID, err)
			return
		}

		// A binary message on a link connection is held to the peer's policy
		// like a verb is. See link_policy.go.
		if verr := d.checkLinkMessage(cs, msg.Type); verr != nil {
			_ = d.sendError(cs, ErrCodeForbidden, verr.Message+" "+verr.Hint.Detail)
			continue
		}
		markLinkServed(cs)
		// A pane that does not hold admin may not use the client protocol.
		// See pane_grants.go.
		if verr := d.checkGrantMessage(cs, msg.Type); verr != nil {
			_ = d.sendError(cs, ErrCodeForbidden, verr.Message+" "+verr.Hint.Detail)
			continue
		}
		if err := d.handleMessage(cs, msg); err != nil {
			LogError("Error handling message from %s: %v", clientID, err)
			_ = d.sendError(cs, ErrCodeInternal, err.Error())
		}
	}
}

func (d *Daemon) handleMessage(cs *connState, msg *Message) error {
	switch msg.Type {
	case MsgHello:
		return d.handleHello(cs, msg)
	case MsgAttach:
		return d.handleAttach(cs, msg)
	case MsgDetach:
		return d.handleDetach(cs)
	case MsgNew:
		return d.handleNew(cs, msg)
	case MsgList:
		return d.handleList(cs)
	case MsgKill:
		return d.handleKill(cs, msg)
	case MsgResurrect:
		return d.handleResurrect(cs, msg)
	case MsgInput:
		return d.handleInput(cs, msg)
	case MsgResize:
		return d.handleResize(cs, msg)
	case MsgCreatePTY:
		return d.handleCreatePTY(cs, msg)
	case MsgReadDir:
		return d.handleReadDir(cs, msg)
	case MsgWatchDir:
		return d.handleWatchDir(cs, msg)
	case MsgClientFocus:
		return d.handleClientFocus(cs, msg)
	case MsgClientGraphics:
		return d.handleClientGraphics(cs, msg)
	case MsgClientActivity:
		return d.handleClientActivity(cs)
	case MsgTypeAtPrompt:
		return d.handleTypeAtPrompt(cs, msg)
	case MsgClosePTY:
		return d.handleClosePTY(cs, msg)
	case MsgUpdateState:
		return d.handleUpdateState(cs, msg)
	case MsgLayoutTree:
		return d.handleLayoutTree(cs, msg)
	case MsgMasterLayout:
		return d.handleMasterLayout(cs, msg)
	case MsgSidebarVisibility:
		return d.handleSidebarVisibility(cs, msg)
	case MsgSubscribePTY:
		return d.handleSubscribePTY(cs, msg)
	case MsgUnsubscribePTY:
		return d.handleUnsubscribePTY(cs, msg)
	case MsgGetTerminalState:
		return d.handleGetTerminalState(cs, msg)
	case MsgExecuteCommand:
		return d.handleExecuteCommand(cs, msg)
	case MsgCommandResult:
		return d.handleCommandResult(cs, msg)
	case MsgGetLogs:
		return d.handleGetLogs(cs, msg)
	default:
		return fmt.Errorf("unknown message type: %d", msg.Type)
	}
}

func (d *Daemon) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	const pendingRequestTTL = 2 * time.Minute

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			// Expire stale pending requests whose TUI result never arrived so
			// they do not pin the requester's connState forever.
			now := time.Now()
			d.pendingRequestsMu.Lock()
			for id, pr := range d.pendingRequests {
				if now.Sub(pr.created) > pendingRequestTTL {
					delete(d.pendingRequests, id)
				}
			}
			d.pendingRequestsMu.Unlock()
		}
	}
}

// isDaemonRunningAt checks if a daemon is listening on the given socket path.
func isDaemonRunningAt(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (d *Daemon) writePidFile() error {
	pidPath, err := GetPidFilePath()
	if err != nil {
		return err
	}
	return os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0600)
}

// IsDaemonRunning checks if a daemon is already running.
func IsDaemonRunning() bool {
	socketPath, err := GetSocketPath()
	if err != nil {
		return false
	}
	return isDaemonRunningAt(socketPath)
}

// GetDaemonPID is defined in platform-specific files:
// - daemon_unix.go for Unix/Linux/macOS
// - daemon_windows.go for Windows
