package session

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/herdrcli"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/memtrim"
	"github.com/Gaurav-Gosain/tuios/internal/openedcopy"
	"github.com/Gaurav-Gosain/tuios/internal/pastebuf"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/scrollclear"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
	"github.com/google/uuid"
)

// Daemon manages the persistent TUIOS server process.
// It owns PTYs and stores session state. Clients run the TUI.
type Daemon struct {
	execRunning atomic.Int32

	manager            *Manager
	tml                tmlHost
	artifacts          artifactLog
	recordings         recordings
	instance           string
	listener           net.Listener
	linkListener       net.Listener
	linkHumanListener  net.Listener
	herdrListener      net.Listener
	acceptWG           sync.WaitGroup
	acceptPaused       atomic.Bool
	handoffArgs        func(manifestFD int) []string
	handoffMu          sync.Mutex
	sessionOrderMu     sync.Mutex
	adopting           atomic.Bool
	pipes              pipeHolds
	prevMu             sync.Mutex
	prev               *prevBinary
	keeperMu           sync.Mutex
	keeper             *handoffKeeper
	lastHandoff        atomic.Pointer[HandoffStats]
	herdrSeqs          herdrSeqs
	herdrWorkspaceMeta herdrWsMeta
	herdrEvents        paneBuckets
	activityReports    paneBuckets
	herdrConns         herdrConnCount
	ctx                context.Context
	cancel             context.CancelFunc

	clients     map[string]*connState
	clientsMu   sync.RWMutex
	hostFocusMu sync.Mutex

	readBudgetOnce sync.Once
	personBudget   *memBudget
	peerBudget     *memBudget
	openConns      atomic.Int64
	openLinkConns  atomic.Int64
	openPeerConns  atomic.Int64
	peerConnsMu    sync.Mutex
	peerConns      map[peerConnKey]int
	refusalsTold   atomic.Int64
	connRefusals   atomic.Int64
	connRefusedLog atomic.Int64

	layoutMu     sync.Mutex
	windowSize   string
	singleClient atomic.Bool
	worktreePath atomic.Pointer[string]
	sshAgent     agentFollow
	display      displayFollow
	latest       latestState
	attachCount  atomic.Uint64

	pendingRequests   map[string]*pendingRequest
	pendingRequestsMu sync.RWMutex

	events *eventHub

	hooks        *hooks.Manager
	agentAlerts  atomic.Pointer[config.AgentAlertPolicy]
	agentHooks   *agentHookGate
	hooksWaiting atomic.Bool

	federation           *federation.Manager
	remoteHolds          remoteHoldSet
	dispatch             dispatchStore
	auto                 autoState
	hostDial             federation.Dialer
	fleet                *hostFleet
	federationMu         sync.Mutex
	federationProblems   []string
	hostsWatcher         *config.Watcher
	configPath           string
	plugins              *pluginHost
	pluginsWaiting       atomic.Bool
	pluginAsksMu         sync.Mutex
	pluginAsks           []PluginApprovalAsk
	prompts              promptStore
	notify               *pushNotifier
	approvalsWaiting     atomic.Bool
	hostsWaiting         atomic.Bool
	grantsWidenedAtStart atomic.Bool

	hostedPanes     map[string]*hostedPane
	hostedPanesMu   sync.Mutex
	hostedPanesShut bool

	outbox *hostOutbox

	linkPolicies   linkPolicyPointer
	linkTableNames linkTableNamesPointer

	agents *agentBus

	attention *attentionStore
	awake     *awakeKeeper

	queueDedup queueDedup

	responds         respondSlots
	fanReserved      fanReservations
	fanVerifies      fanVerifyRuns
	setups           worktreeSetups
	respondFromShell bool

	approvals     atomic.Pointer[ApprovalPolicy]
	activity      *activityStore
	recapTests    atomic.Pointer[[]string]
	approvalPeer  atomic.Pointer[peerPlacer]
	hostedPeer    func(cs *connState) string
	hookStarts    func(pid int) (uint64, bool)
	hookProc      func(pid int) (ppid int, start uint64, ok bool)
	selfPID       int
	sharedProcess bool

	protocolPanes sync.Map
	acpVersion    atomic.Int32
	agentProtoExe func() (string, error)

	stash        *stashStore
	transfers    *transferManager
	linkFiles    linkFileUsage
	pastes       *pasteStore
	replyHistory replyHistory
	buffers      *pastebuf.Store
	buffersOnce  sync.Once
	uploadsMu    sync.Mutex
	uploads      map[*connState]*bufferUpload

	bundles bundleStore

	queue     agentQueues
	mailWake  mailWaker
	delegates delegates
	childAsks childAskTable
	askTurns  askTurns

	reviewNotes reviewNoteStore

	checkpoints checkpointer

	agentPresets atomic.Pointer[map[string]config.AgentPreset]

	replyMarkers atomic.Bool

	shipMu   sync.Mutex
	shipAsks shipAsks
	prs      prPoller

	promptStallOverride time.Duration

	wg       sync.WaitGroup
	wgMu     sync.Mutex
	wgClosed bool

	shutdownOnce sync.Once

	startLock *os.File

	version string

	disableAutoRestore bool

	foreground bool
	logFile    string

	agentStallTimeout time.Duration

	cleanupEvery         time.Duration
	prPollEvery          time.Duration
	dispatchReady        time.Duration
	execTimeout          time.Duration
	askWait              time.Duration
	dropKeepAge          time.Duration
	dropKeepBytes        int64
	hostHeartbeat        time.Duration
	hostHeartbeatTimeout time.Duration
	hostCallTimeout      time.Duration

	silent silentAfter

	agentDetectInterval time.Duration

	agentMatcher agentMatcher
	goneHooks    goneHookNotes

	agentsOff atomic.Bool

	machineOfferOff atomic.Bool

	taskAutoOff  atomic.Bool
	lifecycleOff atomic.Bool
	verifies     taskVerifies
	verifyPeers  sync.Map
	machineCap   atomic.Int64
	pool         poolState
	attachLimit  atomic.Pointer[attachLimits]
	attachSweep  attachSweeper
	prLinks      prLinkCache
	openedKeep   time.Duration

	wakes          idleWakes
	agentsSwitchMu sync.Mutex
	paneCreators   sync.Map
	ports          portsCache

	evidenceClock func() time.Time

	transcriptWatcher *lazyTranscriptWatcher

	resumeAgents string

	pendingResumes []resumeOffer
	rig            bool
}

const defaultAgentStallTimeout = 30 * time.Second

const defaultAgentDetectInterval = 2 * time.Second

var agentDetectQuietBound = 10 * time.Second

func detectQuietTicks(interval time.Duration) int32 {
	if interval <= 0 {
		return 1
	}
	n := int32(agentDetectQuietBound / interval)
	if n < 1 {
		return 1
	}
	return n
}

func (p *PTY) detectScanDue(now int64, quietTicks int32) bool {
	last := p.lastDetectScan.Load()
	if last == 0 || p.lastOutput.Load() > last || p.detectSkips.Add(1) >= quietTicks {
		p.lastDetectScan.Store(now)
		p.detectSkips.Store(0)
		return true
	}
	return false
}

type pendingRequest struct {
	requester *connState
	resultCh  chan *CommandResultPayload
	created   time.Time
	ttl       time.Duration
}

type connState struct {
	dirWatch dirWatchSlot

	conn     net.Conn
	clientID string
	hello    *HelloPayload
	done     chan struct{}
	doneOnce sync.Once
	sendMu   sync.Mutex

	bcastMu   sync.Mutex
	bcastCond *sync.Cond
	bcastNext uint64
	bcastDone uint64

	pushOrigin string

	mu               sync.Mutex
	sessionID        string
	sessionName      string
	ptySubscriptions map[string]*ptySubscriber
	ptyResume        map[string]int64

	eventSub      *eventSub
	pendingStream *eventSub
	streaming     bool

	listingPush    bool
	paneMarks      bool
	goodbye        bool
	tmlSurfaces    bool
	pluginAsks     bool
	prompts        bool
	promptShown    string
	tmlOut         tmlOutbox
	isTUIClient    bool
	holdOptions    bool
	machine        string
	kind           string
	lastActivity   time.Time
	lastInput      atomic.Int64
	attachSeq      uint64
	repliedSession string
	agentRefused   atomic.Bool
	forwardedHook  atomic.Bool
	displayRefused atomic.Bool
	viewOnly       bool
	upgradeDialog  bool
	passive        bool

	verbCall inflightVerb

	takeover func(br *bufio.Reader)

	replyFailed func()

	viaLink bool

	linkHuman bool

	linkPeer            string
	linkPeerSet         bool
	linkPinned          bool
	linkAgentSock       string
	linkServed          bool
	linkCloseToken      string
	linkClosedOnPurpose bool

	peerPID      int
	peerUID      int
	peerUIDOK    bool
	fromPaneOnce sync.Once
	fromPane     bool
	placedIn     string
	placedWhy    string
	fromPaneWhy  string
	peerStart    uint64
	peerStartOK  bool
	peerEnv      procinfo.Env
	peerEnvOK    bool
	paneOnly     bool
	scope        atomic.Pointer[connScope]
	paneBound    atomic.Pointer[string]
	panePlaced   atomic.Pointer[string]
	paneView     atomic.Pointer[paneAuth]
	outsideKey   atomic.Pointer[string]
	pluginBound  atomic.Pointer[string]
	hostedEnded  <-chan struct{}
	hostedWindow string

	attached bool

	missedStateSync bool

	humanNonce string

	presenceNonce   string
	presenceSession string

	hostFocus hostFocus

	width   int
	height  int
	reserve LayoutReserve

	pixelWidth     int
	pixelHeight    int
	cellWidth      int
	cellHeight     int
	kittyGraphics  bool
	sixelGraphics  bool
	symbolImages   bool
	kittyAnimation bool
	terminalName   string
}

// DaemonConfig holds configuration for starting the daemon.
type DaemonConfig struct {
	Version    string
	SocketPath string
	// HandoffArgs is the command line, after the program, that runs this
	// daemon again adopting the panes a handoff wrote to the descriptor
	// manifestFD. The tuios binary sets it. A daemon run inside another
	// program leaves it nil, and refuses to hand off. See handoff.go.
	HandoffArgs func(manifestFD int) []string
	// SharedProcess says the daemon runs in a process that also runs other
	// code which starts processes of its own: the in-process test backend.
	// The daemon then places a caller by the processes it recorded starting
	// (pane shells, hosted panes, hooks), and not by the daemon's own pid,
	// which is the host's. It writes no pid file, so kill-server cannot
	// signal the host. A program that embeds the daemon as its server
	// (tuios-web, the ssh server) leaves it false. See spawn_trust.go.
	SharedProcess bool
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
	// ScrollbackDropOnClear lets ED 3, which clear sends, drop a pane's
	// history, from appearance.scrollback_keep_on_clear = false. The zero
	// value keeps it, the default. See internal/scrollclear.
	ScrollbackDropOnClear bool
	// NewWindowInheritCwd starts a new window in the focused pane's working
	// directory, from appearance.new_window_inherit_cwd. The daemon is the side
	// that spawns the shell, so it is the side that has to know.
	NewWindowInheritCwd bool
	// PreferredShell is appearance.preferred_shell: the shell a pane runs when
	// the client that made its session named none. Empty falls back to $SHELL
	// and then the platform default, the same order a standalone pane uses.
	PreferredShell string
	// ShellIntegration is [shell] integration: auto or off.
	ShellIntegration string
	// ShellLogin is [shell] login: new panes start a login shell.
	ShellLogin bool
	// HerdrProtocol is [agents] herdr_protocol: which panes are told about
	// the herdr protocol socket. See Manager.HerdrEnv.
	HerdrProtocol string
	// ACPVersion is [agents] acp_version. See Daemon.acpVersion.
	ACPVersion int
	// StayOnEmptyWorkspace is workspaces.return_when_empty turned off: the
	// session stays on a workspace that loses its last pane. The zero value
	// is the default, which returns. See empty_workspace.go.
	StayOnEmptyWorkspace bool
	// PasteBufferLimit and PasteBufferMaxBytes are [paste_buffers]: how many
	// paste buffers the daemon keeps and how many bytes they hold together.
	// Zero takes the default, so a daemon made from a hand-built config keeps
	// buffers. PasteBuffersOff is limit = 0 in the file. See verb_buffers.go.
	PasteBufferLimit    int
	PasteBufferMaxBytes int
	PasteBuffersOff     bool
	// DragAndDrop is [panes] drag_and_drop: the daemon records what a pane's
	// program says about drag and drop and answers its query only when on.
	// See dnd_registration.go.
	DragAndDrop bool
	// WorktreePath is [worktree] path: the template the worktrees new-worktree
	// and fan make are placed by. Empty is the default layout. See
	// worktree.PathFor.
	WorktreePath string
	// WorktreeSetup and WorktreeSetupAsync are [worktree] setup and
	// setup_async: the command every new worktree runs before its agent, and
	// whether the agent waits for it. See verb_worktree_setup.go.
	WorktreeSetup      string
	WorktreeSetupAsync bool
	// AgentStallTimeout overrides how long a pane may report working with no
	// output before the stall heuristic demotes it to idle. Zero uses the
	// default; a negative value disables the heuristic.
	AgentStallTimeout time.Duration
	// CleanupEvery, PRPollEvery, SubagentFade and TransferKeep are the
	// [daemon] timings of the same names. Zero or less uses the default.
	CleanupEvery time.Duration
	PRPollEvery  time.Duration
	SubagentFade time.Duration
	TransferKeep time.Duration
	// DispatchReady is how long a dispatched task waits for its agent to be
	// ready, at most 600 seconds. Zero uses start-agent's own wait.
	DispatchReady time.Duration
	// SaveEvery, ExecTimeout, AskWait, UndoWindow, DropKeepAge,
	// DropKeepBytes, HostHeartbeat, HostHeartbeatTimeout and HostCallTimeout
	// are the options of the same names. Zero or less uses the default.
	SaveEvery            time.Duration
	ExecTimeout          time.Duration
	AskWait              time.Duration
	UndoWindow           time.Duration
	DropKeepAge          time.Duration
	DropKeepBytes        int64
	HostHeartbeat        time.Duration
	HostHeartbeatTimeout time.Duration
	HostCallTimeout      time.Duration
	// AgentSilentAfter is [agents] silent_after. Zero means
	// config.DefaultSilentAfter and a negative value turns it off.
	AgentSilentAfter time.Duration
	// AgentAutoDetect toggles the foreground-process agent auto-detector. Nil
	// means enabled. A non-nil false disables it.
	AgentAutoDetect *bool
	// AgentDetectInterval overrides the auto-detector's poll interval. Zero
	// uses the default; a negative value disables auto-detection.
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
	// empty or unknown value is latest, the default. See window_size.go.
	WindowSize string
	// SingleClient is [daemon] single_client: one client per session, and
	// the newest attach wins. See detach_client.go.
	SingleClient bool
	// Display is [daemon] display: "follow", the default, gives a daemon
	// with no display of its own the display of the session's newest local
	// client. See display_follow.go.
	Display string
	// KeepAwake is [daemon] keep_awake: "on" holds the machine awake while
	// the daemon runs, "agents" while an agent pane on this machine works,
	// and "off", the default, never. See keep_awake.go.
	KeepAwake string
	// SSHAgent is [daemon] ssh_agent: "follow" keeps a link per session to
	// the agent socket of the client that attached or used it last, and
	// gives new panes SSH_AUTH_SOCK naming it. See ssh_agent_follow.go.
	SSHAgent string
	// QueueMax is [agents.queue] max: how many messages one pane's delivery
	// queue holds. Zero means the default. See agent_queue.go.
	QueueMax int
	// MailWakeOff is [agents.mail] wake = false: an agent at rest is not
	// told about its mail by a typed prompt. See mail_wake.go.
	MailWakeOff bool
	// ChildQuestionsToPerson is [agents.mail] questions_to_parent = false:
	// a delegated child's question goes to the person's Inbox at once, and
	// not to its parent agent first. See attention_children.go.
	ChildQuestionsToPerson bool
	// TaskAutoStageOff is [tasks] auto_stage = false: the daemon moves no
	// card on its own. See task_auto.go.
	TaskAutoStageOff bool
	// TaskLifecycleOff keeps an agent's finished card going to done rather
	// than to verify and review: [tasks] auto_stage = false, or [tasks]
	// stages without review. See task_verify.go.
	TaskLifecycleOff bool
	// MachineCap is [tasks] machine_cap: how many agents may work here
	// before placement looks elsewhere. Zero is no cap. See pool_place.go.
	MachineCap int
	// Tasks is [tasks], for the attachment limits. See task_attach.go.
	Tasks config.TasksConfig
	// OpenedCopyKeep is [files] open_copy_keep_days: how long a copy the
	// rail's o made of a far file stays when it is not opened again. Zero
	// keeps them all. See internal/openedcopy.
	OpenedCopyKeep time.Duration
	// Checkpoints is [agents.checkpoints]: whether a pane's work tree is
	// saved when its agent finishes a turn, and how many are kept. The zero
	// value is on with the default keep. See checkpoints.go.
	Checkpoints config.CheckpointsConfig
	// AgentPresets is [agents.presets]: named start-agent params. See
	// agent_presets.go.
	AgentPresets map[string]config.AgentPreset
	// ReplyMarkers is [agents] reply_markers. See reply_markers.go.
	ReplyMarkers bool
	// AgentsOff is [agents] enabled = false: the daemon starts with every
	// agent feature off. See agents_switch.go.
	AgentsOff bool
	// MachineOfferOff is notifications.machine_offer = false. See
	// ssh_offer.go.
	MachineOfferOff bool
	// Notify is the [notify] table: where the Inbox's push notifications go.
	// The zero value has no provider and sends nothing. See daemon_notify.go.
	Notify config.NotifyConfig
	// Plugins is the [plugins] table: the herdr plugins the daemon runs.
	// See plugin_host.go.
	Plugins config.PluginsConfig
}

// NewDaemon creates a new daemon instance.
func NewDaemon(cfg *DaemonConfig) *Daemon {
	registerHandoffTestVerbs()
	ctx, cancel := context.WithCancel(context.Background())

	d := &Daemon{
		manager:              NewManager(),
		instance:             uuid.NewString(),
		ctx:                  ctx,
		cancel:               cancel,
		clients:              make(map[string]*connState),
		pendingRequests:      make(map[string]*pendingRequest),
		events:               newEventHub(),
		agents:               newAgentBus(),
		version:              cfg.Version,
		selfPID:              os.Getpid(),
		sharedProcess:        cfg.SharedProcess,
		disableAutoRestore:   cfg.DisableAutoRestore,
		foreground:           cfg.Foreground,
		logFile:              cfg.LogFile,
		agentStallTimeout:    resolveAgentStallTimeout(cfg.AgentStallTimeout),
		cleanupEvery:         positiveOr(cfg.CleanupEvery, defaultCleanupEvery),
		prPollEvery:          positiveOr(cfg.PRPollEvery, prPollInterval),
		dispatchReady:        min(max(cfg.DispatchReady, 0), dispatchReadyMax),
		execTimeout:          positiveOr(cfg.ExecTimeout, execDefaultTimeout),
		askWait:              min(positiveOr(cfg.AskWait, defaultAskWait), maxAskWait),
		dropKeepAge:          positiveOr(cfg.DropKeepAge, dropKeepAge),
		dropKeepBytes:        cmp.Or(max(cfg.DropKeepBytes, 0), dropKeepBytes),
		hostHeartbeat:        cfg.HostHeartbeat,
		hostHeartbeatTimeout: cfg.HostHeartbeatTimeout,
		hostCallTimeout:      cfg.HostCallTimeout,
		agentMatcher:         newAgentMatcher(resolveAgentBinaries(cfg.AgentBinaries)),
		respondFromShell:     cfg.RespondFromShell,
		resumeAgents:         resolveResumeMode(cfg.ResumeAgents),
		windowSize:           windowSizePolicy(cfg.WindowSize),
		handoffArgs:          cfg.HandoffArgs,
	}
	d.singleClient.Store(cfg.SingleClient)
	d.SetSSHAgent(cfg.SSHAgent)
	displaySource := d.displayVars
	d.manager.displaySource.Store(&displaySource)
	cellSource := d.newPaneCellSize
	d.manager.cellSource.Store(&cellSource)
	d.SetDisplay(cfg.Display)
	d.attention = newAttentionStore(d.events.publish, d.events.currentSeq)
	d.awake = newAwakeKeeper(d.workingAgents, d.events.publish)
	d.awake.setMode(cfg.KeepAwake)
	d.SetApprovalPolicy(cfg.Approvals)
	d.activity = newActivityStore(d.events.publish)
	d.SetRecapTestPatterns(cfg.RecapTestPatterns)
	d.SetLinkPolicies(cfg.LinkPolicies)
	d.manager.SetPanePermissions(cfg.Permissions)
	d.SetQueueMax(cfg.QueueMax)
	d.silent.ns.Store(int64(resolveSilentAfter(cfg.AgentSilentAfter)))
	d.SetMailWake(!cfg.MailWakeOff)
	d.SetChildQuestionsToParent(!cfg.ChildQuestionsToPerson)
	d.taskAutoOff.Store(cfg.TaskAutoStageOff)
	d.lifecycleOff.Store(cfg.TaskLifecycleOff)
	d.machineCap.Store(int64(max(cfg.MachineCap, 0)))
	d.setAttachLimits(cfg.Tasks)
	d.openedKeep = cfg.OpenedCopyKeep
	d.SetCheckpoints(cfg.Checkpoints)
	d.SetAgentPresets(cfg.AgentPresets)
	d.replyMarkers.Store(cfg.ReplyMarkers)
	d.outbox = newHostOutbox(d)
	d.stash = newStashStore(func() string { return d.manager.SocketPath() })
	d.transfers = newTransferManager(d, positiveOr(cfg.TransferKeep, transferKeepDone))
	d.manager.SetSubagentFade(positiveOr(cfg.SubagentFade, defaultSubagentFade))
	d.manager.SetSaveEvery(positiveOr(cfg.SaveEvery, resurrectionInterval))
	d.attention.undoWindow = positiveOr(cfg.UndoWindow, attentionUndoWindow)
	d.pastes = newPasteStore(func() string { return d.manager.SocketPath() })
	d.buffers = pastebuf.New(cfg.pasteBufferLimit(), cfg.PasteBufferMaxBytes)
	d.manager.SetScrollbackLines(cfg.ScrollbackLines)
	scrollclear.SetKeep(!cfg.ScrollbackDropOnClear)
	d.manager.SetHistoryPolicy(cfg.History)
	d.manager.SetNewWindowInheritCwd(cfg.NewWindowInheritCwd)
	d.manager.SetPreferredShell(cfg.PreferredShell)
	d.manager.SetShellIntegration(cfg.ShellIntegration)
	d.manager.SetShellLogin(cfg.ShellLogin)
	d.manager.SetHerdrProtocol(cfg.HerdrProtocol)
	d.acpVersion.Store(int32(cfg.ACPVersion))
	d.manager.SetReturnWhenEmpty(!cfg.StayOnEmptyWorkspace)
	d.manager.SetDragAndDrop(cfg.DragAndDrop)
	d.SetWorktreePath(cfg.WorktreePath)
	d.setups.setConfig(cfg.WorktreeSetup, cfg.WorktreeSetupAsync)
	d.agentDetectInterval = resolveAgentDetectInterval(cfg.AgentAutoDetect, cfg.AgentDetectInterval)
	d.agentsOff.Store(cfg.AgentsOff)
	d.machineOfferOff.Store(cfg.MachineOfferOff)
	d.wakes.init()
	d.loadHooks(cfg)
	d.notify = newPushNotifier(d, cfg.Notify)

	d.transcriptWatcher = &lazyTranscriptWatcher{}

	if cfg.SocketPath != "" {
		d.manager.SetSocketPath(cfg.SocketPath)
	}

	d.manager.SetSessionHooks(d.onSessionCreated, d.onSessionDeleted)
	d.manager.SetRenameHook(d.onSessionRenamed)

	d.configPath = cfg.ConfigPath
	d.plugins = newPluginHost(d, cfg.Plugins)
	d.hostDial = cfg.HostDial
	d.fleet = newHostFleet(d)
	d.setupFederation(cfg.Hosts)

	return d
}

func (d *Daemon) setupFederation(hosts []federation.Host) {
	table, problems := federation.NewTable(hosts)
	for _, p := range problems {
		d.federationProblems = append(d.federationProblems, p.Error())
		log.Printf("[FEDERATION] %v", p)
	}
	dial := d.hostDial
	if dial == nil {
		dial = federation.SSHDialerEnv(os.Getenv("TUIOS_SSH"), d.linkSSHEnv)
	}
	d.federation = federation.New(table, federation.Options{
		Dial:            dial,
		ClientName:      "tuios-daemon",
		ClientVersion:   d.version,
		VerbProtocol:    VerbProtocolVersion,
		MinVerbProtocol: MinVerbProtocolVersion,
		Self:            linkSelfName(d.hostedPaneHostName()),
		Log: func(format string, args ...any) {
			log.Printf("[FEDERATION] "+format, args...)
		},
		OnStatus:         d.fleet.onStatus,
		OnMailHeld:       d.onMailHeld,
		Heartbeat:        d.hostHeartbeat,
		HeartbeatTimeout: d.hostHeartbeatTimeout,
		CallTimeout:      d.hostCallTimeout,
	})
}

func (d *Daemon) onSessionCreated(s *Session) {
	d.agentEnsureSession(s.ID)
	s.setIdleWakes(&d.wakes)
	s.setSSHOfferProbe(d.sshOfferFor)
	d.wakes.sessionsChanged()
	name := s.Name()
	sessionID := s.ID
	if d.transcriptWatcher != nil {
		s.SetTranscriptWatcher(d.transcriptWatcher)
	}
	if d.federation != nil {
		s.SetFederation(d.federation)
	}
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
	s.SetHostShownProbe(func() bool { return d.sessionShown(sessionID) })
	s.SetTaskAutoProbe(func() bool { return !d.taskAutoOff.Load() })
	s.SetTaskOutcomeSink(func(board string, finished bool) {
		if d.dispatch.noteOutcome(board, finished) {
			LogBasic("Task dispatch stopped on board %s: %s", board, dispatchStopFailures)
			go d.publishDispatch("stopped", dispatchStopFailures)
		}
	})
	s.SetPaneMarkSink(func(ptyID, windowID string, mark uint32) {
		d.goTracked(func() { d.sendPaneMark(sessionID, ptyID, windowID, mark) })
	})
	s.SetEventSink(func(ev SessionEvent) {
		name := s.Name()
		if ev.Type == EventWindowClosed {
			d.paneCreators.Delete(ev.Window)
			forgetMovedWindow(ev.Window)
		}
		if ev.Type == eventProgramStatus {
			if s.applyProgramStatus(ev.Window, ev.PTYID, !d.agentsOff.Load()) {
				if d.agentDetectInterval > 0 {
					s.scanAgentDetection(d.foregroundResolver(s), d.agentMatcher.identifyKept, nil)
				}
				if reg := d.agentMatcher.registry; reg != nil {
					s.scanPaneForAgent(ev.PTYID, reg)
				}
			}
			return
		}
		if ev.Type == EventOutput && d.agentsOff.Load() {
			if pty := s.GetPTY(ev.PTYID); pty != nil {
				pty.takeAgentProgress()
				pty.takeAgentNotify()
				s.noteCwdOnOutput(pty)
				pty.remoteForeground()
			}
		} else if ev.Type == EventOutput {
			if pty := s.GetPTY(ev.PTYID); pty != nil {
				if state, ok := pty.takeAgentProgress(); ok && !pty.ProgramStatusSeen() {
					s.applyPaneProgress(ev.PTYID, ev.Window, state, d.agentMatcher.registry)
				}
				if n, ok := pty.takeAgentNotify(); ok {
					s.applyAgentNotify(ev.PTYID, n, d.agentMatcher.registry)
				}
				s.noteCwdOnOutput(pty)
				pty.remoteForeground()
				if d.agentDetectInterval > 0 && pty.probeAgentExitDue(time.Now().UnixNano()) {
					s.reconcileAgentOnOutput(ev.PTYID, d.foregroundResolver(s), d.agentMatcher.identifyDetail)
				}
				reg := d.agentMatcher.registry
				if reg != nil {
					ptyID := ev.PTYID
					if !pty.hasScreenLook() {
						pty.setScreenLook(func() {
							if n, ok := pty.takeAgentNotify(); ok {
								s.applyAgentNotify(ptyID, n, reg)
							}
							s.readTranscriptOnOutput(ptyID)
							s.scanPaneForAgent(ptyID, reg)
						})
					}
					if pty.screenScanDue(time.Now().UnixNano()) {
						pty.runScreenLook()
					}
					pty.armScreenSettle()
				}
			}
		}
		if ev.Type == EventTranscript {
			if !d.agentsOff.Load() {
				d.events.publish(streamEvent{Type: EventTranscript, Session: name, Window: ev.Window, Cursor: ev.Cursor})
			}
			return
		}
		if (ev.Type == EventAgentState || ev.Type == eventAttentionDetail) && ev.hookKind == "approval" {
			ev.hookRoot = s.windowApprovalRoot(ev.hookWorktree, ev.Window, ev.hookAgentSession)
			ev.hookRunDir = resolvedRunDir(s.paneRunDir(ev.PTYID))
		}
		if ev.Type == EventLayoutChanged {
			d.events.publish(streamEvent{Type: EventLayoutChanged, Session: name, Workspace: ev.Workspace})
			return
		}
		if ev.Type == eventCompletionSeen || ev.Type == eventAttentionDetail || ev.Type == eventPaneFocused {
			d.attention.noteSessionEvent(name, ev)
			if ev.Type == eventCompletionSeen {
				d.events.publish(streamEvent{Type: EventAgentSeen, Session: name, Window: ev.Window, Action: "seen"})
			}
			return
		}
		if ev.Type == eventCompletionUnseen {
			d.events.publish(streamEvent{Type: EventAgentSeen, Session: name, Window: ev.Window, Action: "unread"})
			return
		}
		if ev.Type == eventTaskReleased {
			d.publishReleased(s, []TaskRow{*ev.taskRow}, ev.taskReason)
			return
		}
		if ev.Type == EventWindowClosed || (ev.Type == EventAgentState && ev.State == AgentStateNone.Name()) {
			d.releaseRemoteHolds(ev.Window)
		}
		d.activity.noteSessionEvent(s, ev)
		if ev.Type == EventAgentState {
			d.noteDispatchAgentState(s, ev.Window, ev.State)
		}
		if !d.agentsOff.Load() {
			d.noteCheckpointEvent(s, ev)
		}
		if ev.Type == EventAgentState || ev.Type == EventWindowClosed {
			d.awake.agentEvent()
		}
		d.fireSessionHooks(s, ev)
		if !d.agentsOff.Load() || !isAgentEvent(ev.Type) {
			defer d.attention.noteSessionEvent(name, ev)
			d.noteQueueEvent(name, ev)
		}
		d.reviewNotes.noteSessionEvent(ev)
		if ev.Type == EventWindowClosed {
			d.agents.forgetNotices(name, ev.Window)
			d.agents.forgetInbox(name, ev.Window)
		}
		d.noteMailWakeEvent(ev)
		d.noteDelegateEvent(ev)
		d.noteChildQuestionEvent(ev)
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
			Reason:     ev.Reason,
			Workspace:  ev.Workspace,
			Cmdline:    ev.Cmdline,
			ExitCode:   ev.ExitCode,
			DurationMS: ev.DurationMS,
			CommandSeq: ev.CommandSeq,
		})
	})
	if !d.adopting.Load() {
		d.events.publish(streamEvent{Type: EventSessionCreated, Session: name})
	}
}

func (d *Daemon) onSessionRenamed(s *Session, old string) {
	name := s.Name()
	d.agents.rename(old, name)
	d.attention.renameSession(old, name)
	d.renameQueuedSession(old, name)
	d.manager.grants.renameSession(old, name)
	d.events.publish(streamEvent{Type: EventSessionClosed, Session: old})
	d.events.publish(streamEvent{Type: EventSessionCreated, Session: name})
	d.clientsMu.RLock()
	var moved []streamEvent
	for _, cs := range d.clients {
		cs.mu.Lock()
		if cs.sessionID == s.ID {
			cs.sessionName = name
			moved = append(moved, streamEvent{Type: EventClientSessionChanged, ClientID: cs.clientID, PID: cs.peerPID, Session: name, Attached: ptr(true)})
		}
		cs.mu.Unlock()
	}
	d.clientsMu.RUnlock()
	for _, ev := range moved {
		d.events.publish(ev)
	}
}

func (d *Daemon) onSessionDeleted(s *Session) {
	d.wakes.sessionsChanged()
	d.agentForgetSession(s.ID)
	d.displayForgetSession(s.ID)
	d.forgetLatest(s.ID)
	d.herdrWorkspaceMeta.forget(s.ID)
	d.events.publish(streamEvent{Type: EventSessionClosed, Session: s.Name()})
	d.awake.agentEvent()
	d.agents.forget(s.Name())
	d.activity.forgetSession(s.ID)
	d.attention.closeSession(s.Name())
	d.forgetQueuedSession(s.Name())
	d.stash.forget(s.ID)
	d.forgetLauncher(s)
	d.forgetFanJudge(s.ID)
	d.broadcastToSession(s.ID, MsgSessionEnded, &SessionEndedPayload{
		SessionName: s.Name(),
		Reason:      "the session was terminated",
	}, "")
}

// Start starts the daemon.
func (d *Daemon) Start() error {
	socketPath := d.manager.SocketPath()
	d.startRig(socketPath)
	d.manager.SetSocketPath(socketPath)

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
			return errfmt.Errorf("daemon already running at %s", socketPath)
		}
		if err := os.Remove(socketPath); err != nil {
			return errfmt.Errorf("failed to remove stale socket: %w", err)
		}
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return errfmt.Errorf("failed to listen on socket: %w", err)
	}
	if ul, ok := listener.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	d.listener = listener

	if err := os.Chmod(socketPath, 0700); err != nil { //nolint:gosec // a socket, owner only; the execute bit means nothing on it
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return errfmt.Errorf("failed to set socket permissions: %w", err)
	}

	d.linkListener = listenLinkSocket(LinkSocketPath(socketPath), "Mail from other machines is not marked.")
	d.linkHumanListener = listenLinkSocket(LinkHumanSocketPath(socketPath), "A reply from human over a link is not verified.")
	if l := listenHerdrSocket(HerdrSocketPath(socketPath)); l != nil {
		d.serveHerdrOn(l, socketPath)
	}

	if err := d.writePidFile(); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return errfmt.Errorf("failed to write PID file: %w", err)
	}

	log.Printf("TUIOS daemon started on %s (PID %d)", socketPath, os.Getpid())

	if err := ptyspawn.AllowCtrlC(); err != nil {
		log.Printf("Warning: Ctrl+C could not be turned on for the panes, so a pane's program may ignore it: %v", err)
	}

	if d.handoffArgs != nil && handoffSupported() {
		d.keepPrevBinary()
	}

	CleanResurrectionDir()

	d.stash.sweep()
	d.startSSHAgent()
	d.startDisplayFollow()
	d.pastes.sweep()

	d.dropHistoryWhenOff()
	if !d.disableAutoRestore {
		d.restoreAllSessions()
	}

	d.loadStores(false)

	d.applyResumeOffers(d.pendingResumes)
	d.pendingResumes = nil

	d.serve()
	return nil
}

func (d *Daemon) loadStores(adopted bool) {
	if adopted {
		d.attention.loadHandoff(attentionPath(), d.attentionLive, d.attentionWaiting)
	} else {
		d.attention.load(attentionPath(), d.attentionLive)
		d.checkGrantsSinceLastRun()
	}
	d.noteHostProblems(d.configProblems())
	d.outbox.load(outboxPath())
	d.dispatch.load(dispatchPath())
	for _, s := range d.manager.AllSessions() {
		d.resumeVerifies(s)
	}
	d.rebuildTaskHolds()
	d.kickAuto()
	d.sweepAttachments()
	d.reviewNotes.load(reviewNotesPath(), func(window string) bool { return d.sessionOfWindow(window) != "" })
}

func (d *Daemon) serveHerdrOn(l net.Listener, socketPath string) {
	d.herdrListener = l
	d.manager.SetHerdrSocket(HerdrSocketPath(socketPath))
	if exe, err := d.agentProtoExecutable(); err == nil {
		bin := exe
		if link, err := herdrcli.InstallLink(HerdrLinkDir(socketPath), exe); err == nil {
			bin = link
		} else {
			log.Printf("The herdr link could not be made: %v. Panes get the tuios binary as HERDR_BIN_PATH, which answers herdr's pane and notification commands only.", err)
		}
		d.manager.SetHerdrBin(bin)
	}
}

func (d *Daemon) serve() {
	if d.federation != nil {
		d.federation.Start(d.ctx)
	}
	d.transfers.load()
	d.goTracked(func() {
		if n := worktree.SweepTemp(); n > 0 {
			LogBasic("Removed %d temporary git folders an earlier daemon left", n)
		}
	})
	d.fleet.start(d.ctx)
	d.startHostsWatch()
	d.notify.start()
	d.plugins.start()
	d.awake.start(d.ctx)

	go d.handleSignals()
	d.startAccepting()
	go d.cleanupLoop()
	go d.stallMonitor()
	go d.silentMonitor()
	go d.agentMonitor()
	go d.listingNotifier()
	go sweepOpenedCopies(d.openedKeep)
}

func sweepOpenedCopies(keep time.Duration) {
	root, err := openedcopy.Root()
	if err != nil {
		return
	}
	if n, err := openedcopy.Sweep(root, keep, time.Now()); err != nil {
		LogBasic("Could not remove the old copies of far files: %v", err)
	} else if n > 0 {
		LogBasic("Removed %d old copies of far files from %s", n, root)
	}
}

func (d *Daemon) startAccepting() {
	d.acceptWG.Go(d.acceptLoop)
	if d.linkListener != nil {
		d.acceptWG.Go(d.acceptLinkLoop)
	}
	if d.linkHumanListener != nil {
		d.acceptWG.Go(func() { d.acceptLinkOn(d.linkHumanListener, true) })
	}
	if d.herdrListener != nil {
		d.acceptWG.Go(func() { d.acceptHerdrLoop(d.herdrListener) })
	}
}

func (d *Daemon) pauseAccepting() {
	d.acceptPaused.Store(true)
	for _, l := range d.listeners() {
		if dl, ok := l.(interface{ SetDeadline(time.Time) error }); ok {
			_ = dl.SetDeadline(time.Now())
		}
	}
	d.acceptWG.Wait()
}

func (d *Daemon) resumeAccepting() {
	for _, l := range d.listeners() {
		if dl, ok := l.(interface{ SetDeadline(time.Time) error }); ok {
			_ = dl.SetDeadline(time.Time{})
		}
	}
	d.acceptPaused.Store(false)
	d.startAccepting()
}

func (d *Daemon) listeners() []net.Listener {
	var out []net.Listener
	for _, l := range []net.Listener{d.listener, d.linkListener, d.linkHumanListener, d.herdrListener} {
		if l != nil {
			out = append(out, l)
		}
	}
	return out
}

// Run starts the daemon and blocks until shutdown.
func (d *Daemon) Run() error {
	InstallDaemonLogging(d.foreground, d.logFile)
	LogBasic("Daemon %s starting (pid %d, log level %s)", d.version, os.Getpid(), GetDebugLevel())

	if err := d.Start(); err != nil {
		return err
	}
	<-d.ctx.Done()
	return d.shutdown()
}

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

func (cs *connState) closeDone() {
	cs.doneOnce.Do(func() { close(cs.done) })
}

func (cs *connState) takeBroadcastTicket() uint64 {
	cs.bcastMu.Lock()
	defer cs.bcastMu.Unlock()
	ticket := cs.bcastNext
	cs.bcastNext++
	return ticket
}

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

func (cs *connState) finishBroadcast() {
	cs.bcastMu.Lock()
	defer cs.bcastMu.Unlock()
	cs.bcastDone++
	if cs.bcastCond != nil {
		cs.bcastCond.Broadcast()
	}
}

func (cs *connState) drop() {
	cs.closeDone()
	_ = cs.conn.Close()
}

func (d *Daemon) shutdown() error {
	d.shutdownOnce.Do(func() {
		log.Println("Shutting down daemon...")
		d.sayGoodbye()

		d.stopRecordings("daemon stopped")

		d.awake.stop()

		d.releaseKeeper()

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
		d.plugins.stop()
		if d.herdrListener != nil {
			d.manager.SetHerdrSocket("")
			_ = d.herdrListener.Close()
			_ = os.Remove(HerdrSocketPath(d.manager.SocketPath()))
		}

		_ = d.transcriptWatcher.Close()

		d.stopHostsWatch()
		d.notify.stop()

		d.closeHostedPanes()

		d.fleet.stop()

		if d.federation != nil {
			d.federation.Stop()
		}

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

		d.wgMu.Lock()
		d.wgClosed = true
		d.wgMu.Unlock()

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

		d.manager.Shutdown()

		d.stash.sweep()
		d.pastes.removeWritten()

		d.stopSSHAgent()
		d.stopDisplayFollow()

		_ = os.Remove(d.manager.SocketPath() + ".pid")

		_ = os.Remove(d.manager.SocketPath())

		d.releaseStartLock()

		log.Println("Daemon shutdown complete")
	})
	return nil
}

func (d *Daemon) acceptLoop() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			if d.acceptPaused.Load() {
				return
			}
			select {
			case <-d.ctx.Done():
				return
			default:
				log.Printf("Accept error: %v", err)
				continue
			}
		}
		if !d.admitConnection(conn, &d.openConns, maxConnections) {
			continue
		}
		go func() {
			defer d.openConns.Add(-1)
			d.handleConnection(conn)
		}()
	}
}

func (d *Daemon) acceptLinkLoop() {
	d.acceptLinkOn(d.linkListener, false)
}

func (d *Daemon) acceptLinkOn(l net.Listener, human bool) {
	for {
		conn, err := l.Accept()
		if err != nil {
			if d.acceptPaused.Load() {
				return
			}
			select {
			case <-d.ctx.Done():
				return
			default:
				log.Printf("Accept error on a link socket: %v", err)
				continue
			}
		}
		if !d.admitConnection(conn, &d.openLinkConns, maxLinkConnections) {
			continue
		}
		go func() {
			defer d.openLinkConns.Add(-1)
			d.handleConnectionOn(conn, true, human)
		}()
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

func shortID(s string) string {
	if len(s) < 8 {
		return s
	}
	return s[:8]
}

func (d *Daemon) handleConnection(conn net.Conn) {
	d.handleConnectionFrom(conn, false)
}

var lastClientID atomic.Int64

func newClientID() string {
	for {
		last := lastClientID.Load()
		next := max(time.Now().UnixNano(), last+1)
		if lastClientID.CompareAndSwap(last, next) {
			return fmt.Sprintf("client-%d", next)
		}
	}
}

func (d *Daemon) handleConnectionFrom(conn net.Conn, viaLink bool) {
	d.handleConnectionOn(conn, viaLink, false)
}

func (d *Daemon) handleConnectionOn(conn net.Conn, viaLink, linkHuman bool) {
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
		ptySubscriptions: make(map[string]*ptySubscriber),
		ptyResume:        make(map[string]int64),
		viaLink:          viaLink,
		linkHuman:        viaLink && linkHuman,
		peerPID:          peerPID(conn),
	}
	cs.peerUID, cs.peerUIDOK = peerUID(conn)
	d.pinPeer(cs)
	if !viaLink {
		release, ok := d.admitPeer(cs)
		if !ok {
			return
		}
		defer release()
	}

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
		d.wakes.listing.Set()
		d.agentForgetHosts(clientID)

		cs.mu.Lock()
		sessionID := cs.sessionID
		sessionName := cs.sessionName
		input := cs.lastActivity
		subs := make([]string, 0, len(cs.ptySubscriptions))
		for ptyID := range cs.ptySubscriptions {
			subs = append(subs, ptyID)
		}
		cs.mu.Unlock()

		if sessionID != "" {
			d.events.publish(streamEvent{
				Type:     EventClientSessionChanged,
				ClientID: clientID,
				PID:      cs.peerPID,
				Session:  sessionName,
				Attached: ptr(false),
			})
			d.forgetPushes(cs, sessionID)
			if session := d.manager.GetSessionByID(sessionID); session != nil {
				for _, ptyID := range subs {
					if pty := session.GetPTY(ptyID); pty != nil {
						pty.Unsubscribe(clientID)
					}
				}
			}
		}

		d.pendingRequestsMu.Lock()
		for id, pr := range d.pendingRequests {
			if pr.requester == cs {
				delete(d.pendingRequests, id)
			}
		}
		d.pendingRequestsMu.Unlock()

		if sessionID != "" {
			d.notifyClientLeft(sessionID, clientID, input)
		}

		_ = conn.Close()
	}()

	br := bufio.NewReaderSize(conn, connReadBuffer)
	d.serveConnection(cs, br)
}

func (d *Daemon) serveConnection(cs *connState, br *bufio.Reader) {
	clientID := cs.clientID
	isJSON, err := d.detectJSONClient(cs, br)
	if errors.Is(err, errFirstByteTimeout) {
		LogBasic("Client %s sent nothing for %v and was closed", clientID, firstByteDeadline)
		return
	}
	if isJSON {
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

		msg, release, err := d.readClientFrame(cs, br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			if tooLarge, ok := errors.AsType[*FrameTooLargeError](err); ok {
				LogError("Refused a message from %s: %v", clientID, err)
				_ = d.replyError(cs, &Message{ReqID: tooLarge.ReqID}, ErrCodeInvalidMessage, "refused: "+err.Error())
				continue
			}
			if busy, ok := errors.AsType[*FrameBusyError](err); ok {
				LogError("Refused a message from %s: %v", clientID, err)
				_ = d.replyError(cs, &Message{ReqID: busy.ReqID}, ErrCodeBusy, "refused: "+err.Error()+". Try again.")
				continue
			}
			if forbidden, ok := errors.AsType[*FrameForbiddenError](err); ok {
				text := forbidden.Err.Message
				if forbidden.Err.Hint != nil {
					text += " " + forbidden.Err.Hint.Detail
				}
				_ = d.replyError(cs, &Message{ReqID: forbidden.ReqID}, ErrCodeForbidden, text)
				continue
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				LogError("Read timeout from %s mid-frame", clientID)
				return
			}
			LogError("Read error from %s: %v", clientID, err)
			return
		}

		d.serveMessage(cs, msg, release)
	}
}

func (d *Daemon) serveMessage(cs *connState, msg *Message, release func()) {
	if msg.Type == MsgInput {
		release()
	} else {
		defer release()
	}
	markLinkServed(cs)
	if err := d.handleMessage(cs, msg); err != nil {
		LogError("Error handling message from %s: %v", cs.clientID, err)
		_ = d.replyError(cs, msg, ErrCodeInternal, err.Error())
	}
}

func (d *Daemon) handleMessage(cs *connState, msg *Message) error {
	if msg.Type != MsgList {
		d.wakes.listing.Set()
	}
	switch msg.Type {
	case MsgHello:
		return d.handleHello(cs, msg)
	case MsgAttach:
		return d.handleAttach(cs, msg)
	case MsgDetach:
		return d.handleDetach(cs, msg)
	case MsgNew:
		return d.handleNew(cs, msg)
	case MsgList:
		return d.handleList(cs, msg)
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
	case MsgClientTheme:
		return d.handleClientTheme(cs, msg)
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
	case MsgSessionUsed:
		return d.handleSessionUsed(cs)
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
	case MsgPing:
		return d.reply(cs, msg, MsgPong, nil)
	default:
		return errfmt.Errorf("unknown message type: %d", msg.Type)
	}
}

const defaultCleanupEvery = 30 * time.Second

const dispatchReadyMax = 600 * time.Second

func optionSeconds(uc *config.UserConfig, path string) time.Duration {
	return time.Duration(config.IntValue(uc, path)) * time.Second
}

// CleanupEvery is the cleanup interval a client releases its own emulators'
// buffers at: daemon.cleanup_seconds as the environment sets it
// (TUIOS_CLEANUP_SECONDS), else the default. The client does not read the
// daemon's config file, so a test gives the client and the daemon their own
// interval through the environment.
func CleanupEvery() time.Duration {
	return positiveOr(optionSeconds(config.WithEnv(nil), "daemon.cleanup_seconds"), defaultCleanupEvery)
}

func positiveOr(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

func (d *Daemon) cleanupLoop() {
	every := d.cleanupEvery
	timer := time.NewTimer(every)
	defer timer.Stop()
	asleep := false

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-d.wakes.cleanup.C():
			if asleep && d.wakes.cleanup.Up() {
				asleep = false
				timer.Reset(every)
			}
		case <-timer.C:
			d.wakes.cleanupTicks.Add(1)
			active := d.wakes.cleanup.Take()
			if !d.cleanupOnce() && !active {
				asleep = true
				continue
			}
			timer.Reset(every)
		}
	}
}

func (d *Daemon) cleanupOnce() bool {
	const pendingRequestTTL = 2 * time.Minute

	d.releaseEmulatorBuffers()
	now := time.Now()
	d.pendingRequestsMu.Lock()
	for id, pr := range d.pendingRequests {
		ttl := pendingRequestTTL
		if pr.ttl > 0 {
			ttl = pr.ttl
		}
		if now.Sub(pr.created) > ttl {
			delete(d.pendingRequests, id)
		}
	}
	waiting := len(d.pendingRequests) > 0
	d.pendingRequestsMu.Unlock()
	memtrim.Request()
	return waiting
}

func resolveAgentStallTimeout(cfg time.Duration) time.Duration {
	if cfg < 0 {
		return 0
	}
	return positiveOr(cfg, defaultAgentStallTimeout)
}

func resolveAgentBinaries(cfg []string) []string {
	extra := append([]string(nil), cfg...)
	if s := os.Getenv("TUIOS_AGENT_BINARIES"); s != "" {
		for name := range strings.SplitSeq(s, ",") {
			if name = strings.TrimSpace(name); name != "" {
				extra = append(extra, name)
			}
		}
	}
	return extra
}

func resolveAgentDetectInterval(enabled *bool, cfg time.Duration) time.Duration {
	if (enabled != nil && !*enabled) || cfg < 0 {
		return 0
	}
	return positiveOr(cfg, defaultAgentDetectInterval)
}

func (d *Daemon) agentMonitor() {
	if d.agentDetectInterval <= 0 {
		return
	}
	timer := time.NewTimer(d.agentDetectInterval)
	defer timer.Stop()
	quietTicks := detectQuietTicks(d.agentDetectInterval)
	quiet := int32(0)
	asleep := false

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-d.wakes.detect.C():
			if asleep && d.wakes.detect.Up() {
				asleep, quiet = false, 0
				timer.Reset(d.agentDetectInterval)
			}
		case <-timer.C:
			d.wakes.detectTicks.Add(1)
			active := d.wakes.detect.Take()
			var pending bool
			if d.agentsOff.Load() {
				if d.machineOfferOff.Load() {
					asleep = true
					continue
				}
				pending = d.sshOfferScan()
			} else {
				pending = d.agentDetectScan()
			}
			if active {
				quiet = 0
			} else {
				quiet++
			}
			if quiet >= quietTicks && !pending {
				asleep = true
				continue
			}
			timer.Reset(d.agentDetectInterval)
		}
	}
}

func (d *Daemon) agentDetectScan() bool {
	quietTicks := detectQuietTicks(d.agentDetectInterval)
	reg := d.agentMatcher.registry
	now := time.Now().UnixNano()
	pending := false
	sessions := d.manager.AllSessions()
	taken := map[string]string{}
	for _, sess := range sessions {
		sess.addJoinedTranscripts(taken)
	}
	for _, sess := range sessions {
		due := func(ptyID string) bool {
			pty := sess.GetPTY(ptyID)
			return pty == nil || pty.sshOffer.waiting() || pty.detectScanDue(now, quietTicks)
		}
		sess.scanAgentDetection(d.foregroundResolver(sess), d.agentMatcher.identifyKept, due)
		for _, ptyID := range sess.takeHarnessNamed() {
			sess.scanPaneForAgent(ptyID, reg)
		}
		sess.maintainAgentTranscripts(reg, d.paneAgentIdentifier(sess), taken)
		if d.noteSessionScanned(sess) {
			break
		}
		if sess.detectPending() || sess.sshOfferWaiting() {
			pending = true
		}
	}
	if d.searchViewTranscripts(reg) {
		pending = true
	}
	return pending
}

func (d *Daemon) sshOfferScan() bool {
	quietTicks := detectQuietTicks(d.agentDetectInterval)
	now := time.Now().UnixNano()
	pending := false
	for _, sess := range d.manager.AllSessions() {
		due := func(ptyID string) bool {
			pty := sess.GetPTY(ptyID)
			return pty == nil || pty.sshOffer.waiting() || pty.detectScanDue(now, quietTicks)
		}
		sess.scanSSHOffers(d.foregroundResolver(sess), due)
		if sess.sshOfferWaiting() {
			pending = true
		}
	}
	return pending
}

func (d *Daemon) paneAgentIdentifier(sess *Session) func(ptyID string) agentIdentity {
	resolve := d.foregroundResolver(sess)
	return func(ptyID string) agentIdentity {
		info, running := resolve(ptyID)
		if !running {
			return agentIdentity{}
		}
		det, ok := d.agentMatcher.identifyDetail(info)
		if ok {
			info = det.proc
		}
		id := paneAgentIdentity(info)
		id.agent = ok
		if ok && d.agentMatcher.registry != nil {
			id.resumed, _ = d.agentMatcher.registry.ResumedSessionID(det.harness, info.argv)
			id.continued = id.resumed == "" && d.agentMatcher.registry.Continues(det.harness, info.argv)
		}
		return id
	}
}

func (d *Daemon) foregroundResolver(sess *Session) func(ptyID string) (foregroundInfo, bool) {
	return func(ptyID string) (foregroundInfo, bool) {
		pty := sess.GetPTY(ptyID)
		if pty == nil || pty.IsExited() {
			return foregroundInfo{}, false
		}
		if info, running, remote := pty.remoteForeground(); remote {
			return info, running
		}
		shellPID := pty.ShellPID()
		info, running := foregroundProcess(shellPID)
		info.shellPID = shellPID
		return info, running
	}
}

func (d *Daemon) stallMonitor() {
	if d.agentStallTimeout <= 0 {
		return
	}
	interval := min(max(d.agentStallTimeout/4, time.Second), 10*time.Second)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	for {
		if at, ok := d.nextStallCheck(interval); ok {
			timer.Reset(time.Until(at))
		} else {
			timer.Stop()
		}
		select {
		case <-d.ctx.Done():
			return
		case <-d.wakes.stall.C():
			d.wakes.stall.Take()
		case <-timer.C:
			d.wakes.stallTicks.Add(1)
			d.stallScan(time.Now())
		}
	}
}

func (d *Daemon) nextStallCheck(interval time.Duration) (time.Time, bool) {
	if d.agentsOff.Load() {
		return time.Time{}, false
	}
	var next int64
	found := false
	for _, sess := range d.manager.AllSessions() {
		at, ok := sess.nextStallAt(d.agentStallTimeout, func(ptyID string) int64 {
			if pty := sess.GetPTY(ptyID); pty != nil {
				return pty.LastOutput()
			}
			return 0
		})
		if ok && (!found || at < next) {
			next, found = at, true
		}
	}
	if !found {
		return time.Time{}, false
	}
	now := time.Now()
	at := time.Unix(0, next)
	if !at.After(now) {
		at = now.Add(interval)
	}
	return at, true
}

func (d *Daemon) stallScan(now time.Time) {
	if d.agentsOff.Load() {
		return
	}
	reg := d.agentMatcher.registry
	for _, sess := range d.manager.AllSessions() {
		sess.applyStallHeuristic(now, d.agentStallTimeout, func(ptyID string) int64 {
			if pty := sess.GetPTY(ptyID); pty != nil {
				return pty.LastOutput()
			}
			return 0
		}, func(ptyID string) bool {
			return sess.scanStalledPane(ptyID, reg)
		})
	}
}

func isDaemonRunningAt(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (d *Daemon) writePidFile() error {
	if d.sharedProcess {
		return nil
	}
	pidPath := d.manager.SocketPath() + ".pid"
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
