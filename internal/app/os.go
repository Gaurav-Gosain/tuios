// Package app provides the core TUIOS application logic and window management.
package app

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/dockengine"
	"github.com/Gaurav-Gosain/tuios/internal/scrollback"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/agentseen"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/pastebuf"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
	"github.com/google/uuid"
	"tuios.dev/swatch"
)

// Mode represents the current interaction mode of the application.
type Mode int

const (
	// WindowManagementMode allows window manipulation and navigation.
	WindowManagementMode Mode = iota
	// TerminalMode passes input directly to the focused terminal.
	TerminalMode
)

// ResizeCorner identifies which corner is being used for window resizing.
type ResizeCorner int

const (
	// TopLeft represents the top-left corner for resizing.
	TopLeft ResizeCorner = iota
	// TopRight represents the top-right corner for resizing.
	TopRight
	// BottomLeft represents the bottom-left corner for resizing.
	BottomLeft
	// BottomRight represents the bottom-right corner for resizing.
	BottomRight
)

// BorderResizeEdge names which single edge a pane-border drag moves. Unlike a
// corner resize, a border drag moves exactly one edge: a tiled pane's shared
// divider, or one side of a floating pane.
type BorderResizeEdge int

const (
	// BorderEdgeNone means no border-resize gesture is active.
	BorderEdgeNone BorderResizeEdge = iota
	// BorderEdgeLeft moves the window's left edge.
	BorderEdgeLeft
	// BorderEdgeRight moves the window's right edge.
	BorderEdgeRight
	// BorderEdgeTop moves the window's top edge.
	BorderEdgeTop
	// BorderEdgeBottom moves the window's bottom edge.
	BorderEdgeBottom
)

// SnapQuarter represents window snapping positions.
type SnapQuarter int

const (
	// NoSnap indicates the window is not snapped.
	NoSnap SnapQuarter = iota
	// SnapLeft snaps window to left half of screen.
	SnapLeft
	// SnapRight snaps window to right half of screen.
	SnapRight
	// SnapTopLeft snaps window to top-left quarter.
	SnapTopLeft
	// SnapTopRight snaps window to top-right quarter.
	SnapTopRight
	// SnapBottomLeft snaps window to bottom-left quarter.
	SnapBottomLeft
	// SnapBottomRight snaps window to bottom-right quarter.
	SnapBottomRight
	// SnapFullScreen maximizes window to full screen.
	SnapFullScreen
	// Unsnap restores window to its previous position.
	Unsnap
)

// WindowLayout stores a window's position and size for workspace persistence
type WindowLayout struct {
	WindowID string
	X        int
	Y        int
	Width    int
	Height   int
}

// OS represents the main application state and window manager.
// It manages all windows, workspaces, and user interactions.
type OS struct {
	tml tmlClient

	switchSeenPending bool

	// ClientTitle is the title of the terminal this client runs in, set
	// through herdr's client.window_title.set. Empty sets none.
	ClientTitle string

	// FullZoom makes every zoom take the whole pane region, as zoom_size = 100
	// does, whatever the config says. A zoom of part of the screen is a camera
	// over the whole layout, which resizes the hidden panes' PTYs for every
	// client of the session. A renderer that draws the zoom itself (the GUI
	// bridge) sets it, so a zoom there resizes the zoomed pane alone.
	FullZoom bool

	// NoRail keeps this model from taking any columns for a rail, and from
	// reading or writing whether the session shows one. A renderer that draws
	// its own sidebar (the GUI bridge) sets it. A rail its terminal clients
	// show would take columns from panes the renderer draws whole, and a rail
	// this model hid would stay hidden for them.
	NoRail bool

	// OwnPaneBorders lays the panes out with shared borders on this client
	// only. Pane geometry is session state, and a terminal client with
	// shared_borders off gives each guest two columns and two rows less than
	// its rectangle. A renderer that draws its own frames (the GUI bridge)
	// sets it to give the guest the whole rectangle, and sends
	// SessionSharedBorders in its state, so the session keeps the setting it had.
	OwnPaneBorders bool

	// SessionSharedBorders is the session's shared borders setting while
	// OwnPaneBorders is on. It starts as this client's config says.
	SessionSharedBorders bool

	frameRate frameRate

	Dragging         bool
	Resizing         bool
	BorderResizing   bool // a pane-border drag is moving one edge
	BorderResizeEdge BorderResizeEdge
	// BorderResizeGrab is how far the dragged edge lies from the cell the
	// pointer pressed, along the axis it moves: a pane-border drag keeps the
	// edge that far from the pointer, so the pane does not jump on the press.
	BorderResizeGrab         int
	ResizeCorner             ResizeCorner
	PreResizeState           terminal.Window
	ResizeStartX             int
	ResizeStartY             int
	DragOffsetX              int
	DragOffsetY              int
	DragStartX               int // Track where drag started
	DragStartY               int // Track where drag started
	TiledX                   int // Original tiled position X
	TiledY                   int // Original tiled position Y
	TiledWidth               int // Original tiled width
	TiledHeight              int // Original tiled height
	DraggedWindowIndex       int // Index of window being dragged
	AutoScrollDir            int // -1 = up, 0 = none, 1 = down (for drag auto-scroll)
	AutoScrollActive         bool
	SelectionDragged         bool // the pointer moved during the current selection gesture
	ScrollbarDragging        bool
	ScrollbarDragWindowIndex int // -1 when not dragging
	// ScrollbarGrabOffset is the rows between the pointer and the thumb's first
	// row, fixed when the bar is grabbed so the thumb rides under the pointer
	// instead of jumping to it on every motion event.
	ScrollbarGrabOffset int
	Windows             []*terminal.Window
	FocusedWindow       int
	// PrevFocusedID is the window focus came from most recently, for the
	// last_pane toggle. A window ID, not an index: indices shift on close and
	// reorder.
	PrevFocusedID                 string
	lastFocusedID                 string
	Width                         int
	Height                        int
	Mode                          Mode
	mode                          modeState
	terminalMu                    sync.RWMutex
	LastMouseX                    int
	LastMouseY                    int
	sessionView                   sessionView
	pointerInLayout               bool
	pressInLayout                 bool
	pressInLeftover               bool
	pointerSwallowed              bool
	lastActivity                  time.Time
	wasLatest                     bool
	activitySession               string
	activitySince                 time.Time
	viewMark                      viewMarkCache
	leftoverRight, leftoverBottom leftoverCache
	sessionClients                int
	pointerSeenX                  int
	pointerSeenY                  int
	ShowHelp                      bool
	InteractionMode               bool        // True when actively dragging/resizing
	WindowExitChan                chan string // Channel to signal window closure
	windowExits                   windowExitQueue
	PTYDataChan                   chan struct{}     // Signaled by PTY readers when new output arrives (buffered 1, coalescing)
	StateSyncChan                 chan StateSyncMsg // Channel for thread-safe state sync from callbacks
	ClientEventChan               chan ClientEvent  // Channel for thread-safe client join/leave notifications
	pluginCellsIn                 chan session.PluginUIPayload
	// DaemonExitChan carries the two daemon events that end this client: the
	// session was destroyed, or the connection dropped. See daemon_exit.go.
	DaemonExitChan     chan tea.Msg
	daemonReconnect    *daemonReconnect
	daemonReconnectGen int
	daemonWaiting      atomic.Bool
	restart            daemonRestart
	// RestartExec is set with ExitRestarted: what the entry point runs to
	// bring the client back. RestartFailure is set with ExitRestartFailed.
	RestartExec    *RestartExec
	RestartFailure *RestartFailure
	// UpgradeExec is set with ExitUpgradeFollowed: the build a live upgrade
	// gave the daemon, for the entry point to run. followedUpgrade is
	// OSOptions.AfterUpgrade, upgradeAnnounced says its note is shown, and
	// followUpgrades is OSOptions.FollowUpgrades.
	// See upgrade_follow.go.
	UpgradeExec      *UpgradeExec
	followedUpgrade  string
	upgradeAnnounced bool
	followUpgrades   bool
	calm             calmState
	restartLater     bool
	afterRestart     bool
	Animations       []*ui.Animation // Active animations
	CPUHistory       []float64       // CPU usage history for graph
	LastCPUUpdate    time.Time       // Last time CPU was updated
	RAMUsage         float64         // Cached RAM usage percentage
	LastRAMUpdate    time.Time       // Last time RAM was updated
	cpuLast          cpuTicks
	cpuHasLast       bool
	cpuUnavailable   bool
	ramUnavailable   bool
	AutoTiling       bool    // Automatic tiling mode enabled
	MasterRatio      float64 // Master window width ratio for tiling (0.1-0.9)
	// TouchClient marks a session whose pointer is a finger. It is per session
	// rather than a config global because one server holds several at once and
	// a phone attaching must not change what the desktop beside it can hit.
	TouchClient bool
	// BrowserPaste says which keys make this session's browser paste. See
	// OSOptions.BrowserPaste.
	BrowserPaste BrowserPasteKeys

	// RemoteClient marks a client process that is not on the user's machine.
	// See OSOptions.RemoteClient.
	RemoteClient bool

	// Settings is this session's appearance and behaviour, copied from the
	// process seed when the session was built and thereafter its own.
	//
	// A value, not a pointer, so a read is a field offset and an OS built as a
	// struct literal is never nil. It is written only on the Bubble Tea
	// goroutine: the settings page edits it live through setConfigFromRegistry,
	// and a config reload replaces it wholesale. Background goroutines that
	// need a setting are handed the value they need when they are started, not
	// a pointer to this.
	//
	// This is the thing that stops one client's settings page reaching another
	// client's frame. See config.Settings.
	Settings config.Settings

	// Caps is the terminal this session draws to, as it described itself when
	// the connection was made. It is per session rather than a package global
	// because one server process holds several sessions at once: an SSH client
	// in kitty and one in xterm are two different terminals, and the graphics
	// each gets must be decided from its own.
	//
	// Immutable after NewOS. Never nil: NewOS falls back to the process host
	// when the caller has nothing better, and a hand-built OS literal in a test
	// gets the same fallback through Caps() below.
	Caps *HostCapabilities

	// Capture is capture mode: the window pick, the region drag and the
	// full-screen grab. Zero when the mode is off.
	Capture captureState

	// ShotPreview is the panel that opens after a capture. Zero when closed,
	// and never read by tickNeedsWork: it is a static panel.
	ShotPreview screenshotPreview

	captureHits []captureHit

	shotImagePlaced bool
	shotImageSent   bool
	shotPlacement   screenshotPlacementState

	shotCaptures int

	shotDiscarded []int

	// BSP tiling state
	WorkspaceTrees      map[int]*layout.BSPTree // BSP tree per workspace
	PreselectionDir     layout.PreselectionDir  // Pending preselection direction (0 = none)
	TilingScheme        layout.AutoScheme       // Default auto-insertion scheme
	SplitTargetWindowID string                  // Window ID to split (set before AddWindow for splits)
	pendingSplitDir     layout.PreselectionDir
	pendingSplitTarget  string
	WindowToBSPID       map[string]int // Maps window UUID to stable BSP integer ID
	BSPIDToWindowID     map[int]string // Reverse of WindowToBSPID: BSP integer ID to window UUID (speed-up for GetWindowByIntID)
	NextBSPWindowID     int            // Next BSP window ID to assign (starts at 1)
	RenameKind          RenameKind     // What the open rename editor targets (RenameNone when closed)
	rename              textedit.Line
	RenameTargetID      string // Window the rename in flight applies to
	renameHit           overlay.Rect
	renameClearHit      overlay.Rect
	PrefixActive        bool // True when prefix key was pressed (tmux-style)
	// SubMenu is the leader sub-menu open now (leader, then w, m, t, D, T, L
	// or the tuios menu key), SubMenuNone when none is.
	SubMenu        SubMenu
	LastPrefixTime time.Time // Time when prefix was activated
	// LockHeld is true while lock mode keeps a menu open after a repeatable
	// action, until esc. See lock_mode.go.
	LockHeld               bool
	nest                   bool
	windowModeNoted        bool
	keyModes               []string
	HelpScrollOffset       int  // Scroll offset for help menu
	HelpCategory           int  // Current help category index (for left/right navigation)
	HelpSearchMode         bool // True when help search is active
	helpSearch             textedit.Line
	CurrentWorkspace       int                         // Current active workspace (1-9)
	NumWorkspaces          int                         // Total number of workspaces
	WorkspaceFocus         map[int]int                 // Remembers focused window per workspace
	FocusHistory           map[int][]string            // Newest-first focus history per workspace
	WorkspaceLayouts       map[int][]WindowLayout      // Stores custom layouts per workspace
	WorkspaceHasCustom     map[int]bool                // Tracks if workspace has custom layout
	WorkspaceMasterRatio   map[int]float64             // Stores master ratio per workspace
	WorkspaceStackRatio    map[int]float64             // Stack ratio per workspace, the only copy (see setWorkspaceStackRatio)
	WorkspaceMasterSplits  map[int]layout.MasterSplits // Every other master-stack split per workspace, the only copy (see SyncMasterStackFromGeometry)
	WorkspaceLayoutMode    map[int]string              // Layout mode chosen per workspace; no entry follows the session's
	sessionLayoutMode      string
	ShowLogs               bool         // True when showing log overlay
	LogMessages            []LogMessage // Store log messages
	logList                listnav.State
	msgView                messageViewState
	recentMessages         []messageEntry
	notifHoverAt           time.Time
	notifHoldSpent         bool
	wrapCache              [wrapCacheSize]wrapCacheEntry
	wrapCacheNext          int
	Notifications          []Notification // Active notifications
	notifHit               notifHitZones
	dockWorkspaceHits      []dockWorkspaceHit
	dockWorkspaceArrowHits []dockWorkspaceArrowHit
	dockStrip              dockStripMemo
	dockWorkspaceScroll    int
	dockWorkspaceScrollFor int
	dockWorkspaceScrollAt  int
	dockWorkspaceDrag      dockWorkspaceDragState
	dockItemHits           []dockItemHit
	dockOverflowHit        dockOverflowHit
	dockSessionHits        []dockSessionHit
	notifDrawn             notifDrawn
	dockSessionHover       DockSessionAction
	dockCustomHits         []dockCustomHit
	welcomeAsk             bool
	welcomeHits            []welcomeChoiceHit
	dockButtonHover        int
	dockButtonsShown       int
	dockPlan               dockPlan
	diagramsTold           map[string]bool
	pluginCells            map[string]session.PluginCell
	dockEngine             *dockengine.Engine
	// RemoteCommandChan carries a verb the daemon routed to this client, for
	// the hosts that cannot Send into the program directly. See dock_remote.go.
	RemoteCommandChan chan RemoteCommandMsg
	stateSyncsArrived atomic.Uint64
	stateSyncsApplied uint64
	heldRemote        []RemoteCommandMsg
	heldRemoteGen     uint64
	ClipboardContent  string // Store clipboard content from tea.ClipboardMsg
	ShowCacheStats    bool   // True when showing style cache statistics overlay
	// Quit menu state. The menu replaces the old yes/no quit dialog: a small
	// list overlay on the shared list-overlay grammar, registered in OverlayHits
	// as kind "quit" so hover, click and click-away routing come from the same
	// machinery as every other overlay. Items are built once per open (see
	// OpenQuitMenu) so the rows reflect the session state at that moment.
	ShowQuitMenu          bool
	quitList              listnav.State
	QuitMenuItems         []QuitMenuItem
	QuitMenuOtherSessions []string // non-current sessions at open time; [0] is the kill-and-go-next target
	// Close-session confirmation, the micro-dialog the dock's recessed control
	// and ctrl+b X both raise. Its rows are fixed, so only the selection is
	// state; what it says about the session is counted as it draws.
	// SessionCloseTarget is the session it was raised on, by identity, or "" for
	// the attached one: closing that quits this client, closing any other leaves
	// this client where it is.
	ShowSessionClose   bool
	SessionCloseTarget string
	// SessionCloseWorkspace turns the dialog into close_workspace's: it asks
	// about the panes of this workspace, not the session. 0 is the session.
	SessionCloseWorkspace int
	// SessionCloseWindow turns the dialog into a one-pane confirmation. It is
	// how a tuios://window/<id>/close link closes: the pane a program in
	// another pane named gets named back to the person holding the mouse
	// before anything dies. It holds the pane's ID, not its index, because
	// m.Windows can change while the dialog is open, and an index would then
	// name another pane. "" is no pane.
	SessionCloseWindow string

	focusOnlyHidden map[int][]string
	closeList       listnav.State
	// SessionCloseWorktreeToll is what the delete-worktree row would take, read
	// by a round trip after the dialog opened. Nil, or Loaded false, is a toll
	// that has not landed yet, and the dialog says nothing about the worktree
	// until it has: a count it does not have is a count it must not invent.
	SessionCloseWorktreeToll *worktreeToll
	pendingWorktreeKill      bool
	// Pending resize tracking for debouncing PTY resize during mouse drag
	PendingResizes     map[string][2]int // windowID -> [width, height] of pending PTY resize
	pendingCopy        string
	selectionSeq       uint64
	pastePending       bool
	pasteSeq           uint64
	pasteAskedAt       time.Time
	imagePaste         imagePasteState
	clipboardAsks      map[string]*clipboardAsk
	clipboardCopyNotes map[string]string
	clipboardAskSeq    uint64
	clipboardApproved  *string

	cachedSeparator      string
	cachedSeparatorWidth int
	cachedSeparatorChar  string
	cachedSeparatorColor color.Color
	cachedViewContent    viewContent
	renderSkipped        bool
	screen               *termio.Screen
	screenSeq            uint64
	blank                struct {
		s    string
		w, h int
	}
	directHold             paneHold
	keepHostPictures       atomic.Bool
	quietFrame             bool
	tickStats              tickStats
	emulatorReleasedAt     time.Time
	emulatorReleasePending bool
	tick                   tickTimer
	screensaver            screensaverState
	spotlight              spotlightState
	motion                 motionState

	shake                 shakeState
	celebration           celebrationState
	themeFx               themeTransition
	wsSlide               workspaceSlide
	arriving              bool
	lastInteractionRender time.Time
	frameWritten          frameWrittenClock
	pointerMotionPending  bool
	viewportResizing      bool
	viewportResizeGen     uint64
	viewportResizeAt      time.Time
	lastPointerAt         time.Time

	wheelAxis wheelAxis

	zenHidden              bool
	pointerDown            bool
	announceGestureHeld    bool
	pendingBSPSync         bool
	bspResizeScratch       map[int]layout.Rect
	renderCanvas           *frameCanvas
	canvasIsFrame          bool
	separatorMemo          separatorMemo
	layerCells             map[string]*cellLayer
	composeGen             uint64
	keepLayerCells         bool
	composeScratch         []composedLayer
	kept                   keptCanvas
	canvasKept             bool
	frameRows              []bool
	frameRowsAll           bool
	railFit                railScratch
	groundCache            [surfaceCount]groundMemo
	paneContentRects       map[string]image.Rectangle
	host                   hostColors
	paneReport             paneReportMemo
	paneReportSent         paneReport
	paneReportSentSet      bool
	hostPixel              hostPixelMouse
	splitMouse             splitMouseReport
	conptyNUL              heldNUL
	hostPS                 hostProgramStatus
	fastPaint              fastPainter
	scrollbarRects         map[string]ScrollbarRect
	windowButtonRects      map[string][]WindowButtonRect
	windowButtonHover      string
	linkHover              PaneLink
	linkHoverOn            bool
	kittyPosMap            map[string]*WindowPositionInfo
	kittyPosBacking        []WindowPositionInfo
	kittyChrome            []cellRect
	placementScrollbackLen map[string]int
	// SSH mode fields
	SSHSession        SSHConn // SSH session reference (nil in local mode)
	configReloads     <-chan tea.Msg
	themeReloads      <-chan ThemeFilesReloadedMsg
	stopConfigWatch   func()
	omarchyChanges    <-chan swatch.Palette
	stopOmarchyFollow func()
	// Client is where the person looking at this screen is sitting. The
	// booleans around it are what it implies, kept because they are what the
	// code reads and what the tests set. See ClientKind.
	Client    ClientKind
	IsSSHMode bool // True when running over SSH
	// SSHIsLoopback is true when an SSH session arrived over loopback (the
	// same machine). The human is at this box, so the native clipboard is
	// theirs and the local fallback is safe; a remote SSH peer is not.
	SSHIsLoopback bool
	// Daemon mode fields
	IsDaemonSession bool               // True when running as part of a persistent daemon session
	DaemonClient    *session.TUIClient // Client for daemon communication (nil in local mode)
	SessionName     string             // Name of the daemon session (if attached)
	lastWorkspace   int
	lastSession     lastSession
	shownOn         map[string]string
	// AttachedHost is the host DaemonClient reaches its daemon through, or ""
	// for the daemon on this machine. See SwitchToHostSession.
	AttachedHost         string
	hostReturn           string
	sessionSwitchHook    func(host, name string) error
	hostReconnect        *hostReconnect
	hostReconnectGen     int
	hostReconnectReason  string
	foreignTickGen       uint64
	foreignSessionReplan bool

	// SessionDisplayName and SessionAccent are the attached session's
	// daemon-owned label and accent slot, both empty when unset. They are
	// labels only: SessionName stays the identity every keyed map, every
	// switch and the daemon's own addressing use.
	SessionDisplayName string
	SessionAccent      string
	// SessionNote is the attached session's return note, daemon-owned like
	// the label. noteAnnouncedFor is the session whose note the dock last
	// showed on arrival, so the note is shown once per attach or switch and
	// not again when it changes while you stay. paneNotes are the attached
	// session's pane notes by window id, only the panes that have one.
	SessionNote      string
	noteAnnouncedFor string
	paneNotes        map[string]string
	sessionColors    map[string]Accent
	sessionPoolCache []sessionHue
	sessionPoolKey   sessionPoolKey
	// SessionRestored is the attached session's daemon-owned restored mark. The
	// daemon clears it on attach, so it is normally false here; it is carried
	// anyway so the attached row reads from the same field every other row does.
	// Distinct from RestoredFromState below, which is this client's own
	// bookkeeping about having applied a state snapshot.
	SessionRestored bool
	// SessionGlobal is the attached session's daemon-owned global mark, which
	// says it is meant to hold panes from more than one machine. Every other
	// session's arrives with the cached listing; this one comes down with the
	// session state, the same way SessionRestored does.
	SessionGlobal bool
	// SessionSpace is the space the attached session is filed under, empty for
	// none. It is daemon-owned and comes down with the session state, like
	// SessionGlobal; every other session's arrives with the cached listing.
	SessionSpace   string
	markStyleCache *markStyles

	machineSheet *machineSheet
	msCanvas     *ckCanvas

	taskCapture       *taskCapture
	taskCaptureClosed bool
	tcFiling          *taskCapture
	tcOpenGen         uint64
	tcCanvas          *ckCanvas

	copyFlash     *copyFlash
	copyFlashMore []copyFlash

	// MultiCopy is multi copy mode while it is on, or nil. See multicopy.go.
	MultiCopy *MultiCopy

	hints                *hintsState
	paneLabels           *paneLabelsState
	dropZone             *Drop
	dropZoneShown        bool
	dropZoneGen          uint64
	dropZoneX, dropZoneY int
	dropLook             dropLook
	fileDrag             fileDragState
	sidebarPressCopy     bool
	dropLayout           map[int]layout.Rect

	prefixRepeatUntil time.Time

	// SessionWorktree is the attached session's daemon-owned worktree record,
	// nil when its directory is not a linked git worktree. Every other
	// session's arrives with the cached listing; this one comes down with the
	// session state, so the rail reads the attached row from live state exactly
	// as it reads the rest of that row.
	SessionWorktree *session.WorktreeInfo
	setupCard       setupCardState
	// WorkspaceNames maps a workspace number to its daemon-owned label. An
	// unnamed workspace shows its number, so an absent entry is not a missing
	// label but the normal case.
	WorkspaceNames        map[int]string
	paneIconNames         map[string]string
	pendingWorkspaceOrder []int
	workspaceTabKeys      map[int]int
	workspaceTabKeysKnown bool
	workspaceMoveInFlight bool
	RestoredFromState     bool // True after RestoreFromState, cleared after first resize
	// DaemonStateVersion is the daemon state version this client last saw. It is
	// echoed back on every state sync so the daemon can tell a snapshot built
	// from its current state apart from one built before a mutation of its own.
	DaemonStateVersion int
	streamTap          StreamTap
	// ReloadFilter, when set, sees a config read from disk before it is put
	// in force. tuios gui-bridge keeps its own chrome off with it: its
	// renderer draws the dock and the rail itself, and a reload would bring
	// them back above the panes for every client of the session.
	ReloadFilter func(*config.UserConfig) *config.UserConfig
	// SnapshotScrollback is how many history rows a pane's snapshot asks
	// the daemon for; 0 is the daemon's default, 1000. tuios gui-bridge sets
	// it to its renderer's own history, so a reopen shows as much as the
	// renderer keeps.
	SnapshotScrollback int
	SubscribedPTYs     map[string]bool // Tracks which PTY IDs are currently subscribed (for visibility optimization)
	hostEndedTold      map[string]bool
	// RestoredStreamSeq is the stream position each pane's snapshot was taken
	// at, from the restore that precedes the subscribe on the attach path.
	RestoredStreamSeq map[string]int64
	// ExitReason records why the program stopped, for the caller to report and
	// to pick an exit status. Empty means the user quit or detached normally.
	// It is written only on the Bubble Tea goroutine, in Update.
	ExitReason ExitReason

	detachFired     atomic.Bool
	focusReportedID string
	// QuitRequested records that the user deliberately quit this client, which
	// in a daemon session also kills the session. The daemon then announces the
	// session ending and the connection dropping, and both announcements can
	// arrive before the program finishes quitting. Without this flag those
	// announcements are indistinguishable from a session killed from elsewhere,
	// and a deliberate quit reports an error. Written only on the Bubble Tea
	// goroutine, like ExitReason.
	QuitRequested bool

	stripSnapGen uint64
	stripSnapDir int

	clickReveal struct {
		armed bool
		x, y  int
	}

	// SidebarWidthPref is the expanded rail width this session asks for, or 0
	// to take the configured default. It is synced with the session's other
	// clients: the rail is chrome, and the panes' box is settled across every
	// client, so two clients disagreeing about the rail costs one of them a
	// blank band rather than moving anybody's panes.
	SidebarWidthPref int

	// SharedBorders and PaneGap are the layout arithmetic inside the panes' box:
	// whether tiled panes merge their border boxes into single dividers, and how
	// much empty ground the tiler keeps between neighbours. They are model state
	// rather than reads of the config globals because they are inputs to pane
	// geometry, and every input to pane geometry has to be identical across a
	// session's attached clients, because a PTY has exactly one size. Two clients whose
	// config files (or live settings) disagreed here computed different
	// rectangles for the same panes and dragged the shared PTYs back and forth
	// between the two answers on every push.
	//
	// Seeded from this client's config, then settled across the session by state
	// sync (session.PaneGeometryState). Purely visual appearance (theme,
	// colours, border style, title position, dimming) deliberately stays
	// per-client and configurable; these two are synced only because they move
	// rectangles.
	SharedBorders bool
	PaneGap       int
	// ScrollColumnWidth is a column's width in the scrolling layout, as a
	// percent of the screen, before anything resizes it. Session state for the
	// same reason the two above are: it is what every column's cell width is
	// computed from, so two clients holding different values would hand the same
	// pane two different widths.
	ScrollColumnWidth       int
	lastConfigSharedBorders bool
	lastConfigPaneGap       int
	lastConfigScrollWidth   int
	// WorkspaceMasterLayout is each workspace's master-stack shape, for the
	// workspaces somebody changed at run time. See master_layout.go.
	WorkspaceMasterLayout map[int]session.MasterLayoutState
	masterSeeded          map[int]bool
	sidebarSeeded         bool
	sidebarSession        string
	chromeSeen            struct {
		reserve       session.LayoutReserve
		width, height int
	}
	layoutGenApplied, layoutGenPushed uint64
	commandDriving, commandFocusMoved bool
	focusGiven                        string
	focusGivenSet                     bool

	lastConfigMaster session.MasterLayoutState

	// SessionReserve is the chrome reserve every client attached to this
	// session lays its panes out around: the largest any of them asks for, as
	// settled by the daemon. The panes' box is the render size less this, which
	// is what makes the box, and so every pane's size, identical on every
	// client. Zero outside a daemon session, where this client's own chrome is
	// the only chrome there is.
	SessionReserve session.LayoutReserve

	// Multi-client effective size (min of all clients in session)
	EffectiveWidth  int // Effective width for rendering (min of all clients, 0 = use terminal size)
	EffectiveHeight int // Effective height for rendering (min of all clients, 0 = use terminal size)

	syncedFP    uint64
	syncedFPSet bool
	treeSeen    map[int]string
	treeDerived map[int]string

	daemonLayoutSeen int

	applyingPeerSync   bool
	syncAnswerOwed     bool
	treeAnswerBase     int
	treeAnswerUser     map[int]bool
	turnsWithinSync    []*terminal.Window
	daemonWindowIntent bool
	windowIntentErr    error
	heldNotice         string
	paneRequests       map[int]paneRequest
	// Keyboard enhancement support (Kitty protocol)
	KeyboardEnhancementsEnabled bool // True when terminal supports keyboard enhancements
	quitting                    bool
	lastKeyboardEnhancements    tea.KeyboardEnhancements
	// KeyboardFlags is the flag set the host answered the enhancement query
	// with, so tuios knows what it actually got rather than what it asked for.
	// Zero means the terminal never answered, which is not the same as a refusal.
	KeyboardFlags      int
	hostGrantedAllKeys bool
	paneKeysDown       map[rune]string
	hostKey            tea.KeyPressMsg
	hold               holdMode
	optionAdviceShown  bool
	currentPointer     PointerShape
	// Keybind registry for user-configurable keybindings
	KeybindRegistry *config.KeybindRegistry
	// ConfigWarnings holds the problems found in the loaded config, reported to
	// the user once the TUI is up (see reportConfigWarnings).
	ConfigWarnings []string
	// ConfigNotices holds settings the file asks for that this client cannot
	// carry out, such as desktop notifications in a browser. The file is not
	// wrong, so they are logged and announced as notices, never counted as
	// config problems.
	ConfigNotices []string
	// ConfigReadOnly stops the settings page writing the config file. Set by
	// entrypoints that serve someone else's session; see OSOptions.
	ConfigReadOnly bool
	// ListKeys is the keys that move every list, from [keybindings.lists]. Nil
	// reads the defaults.
	ListKeys *listnav.Keymap
	// BrowserClient says the far end is a browser tab. See browser_client.go
	// for what that costs and what tuios says about it.
	BrowserClient bool
	// LearnMode is the guided tour in the browser build: nothing quits, and
	// what the demo cannot do says so. See learn_mode.go.
	LearnMode  bool
	learnSpawn func() LearnSpawnInfo
	learnFans  map[string]ckWorktree
	// OnAction, when set, hears the name of every action the dispatcher runs,
	// before it runs. The browser build reports it to the page, so a lesson
	// knows which binding a key reached. Nil everywhere else.
	OnAction func(action string)
	// OnNotification, when set, hears every message shown in the dock, with
	// its level. The browser build reports it to the page. Nil everywhere else.
	OnNotification func(message, level string)
	// LearnNotes, when set, replaces Learn mode's notes by their default
	// text. The notes talk about a browser tab; tuios tutor runs in a
	// terminal. An empty replacement shows no note.
	LearnNotes         map[string]string
	learn              LearnBackend
	configReadOnlyTold bool
	settingsSaves      *settingsSaveQueue
	// Showkeys overlay: a bottom-right, dock-aware keycast that shows the last
	// few keypresses as styled pills and expires them after a short timeout. It
	// renders purely from ShowKeys plus RecentKeys, gated on nothing else.
	ShowKeys      bool // True when the showkeys overlay is enabled
	overrides     config.Overrides
	RecentKeys    []KeyEvent // Ring buffer of recently pressed keys
	crash         *CrashReport
	crashNotice   string
	recentActions []string
	// Tape scripting support
	ScriptPlayer       *tape.Player          // script playback engine
	ScriptMode         bool                  // True when running a tape script
	ScriptPaused       bool                  // True when script playback is paused
	ScriptExecutor     *tape.CommandExecutor // executes tape commands
	ScriptSleepUntil   time.Time             // When to resume after a sleep command
	ScriptFinishedTime time.Time             // When the script finished (for auto-hide)
	// WaitUntilRegex playback state. When ScriptWaitRegex is non-nil, playback
	// blocks until the focused window's screen matches it or ScriptWaitDeadline
	// passes, whichever comes first.
	ScriptWaitRegex    *regexp.Regexp
	ScriptWaitDeadline time.Time
	// Pane-readiness gate. A tape command that creates a pane (Split, NewWindow,
	// SmartSplit) does not create it here: in a daemon session it asks the daemon
	// and the pane arrives later, on a state push. Until it does, the focused
	// window is still the old one, so the next Type would be typed into the pane
	// the tape just split away from. ScriptAwaitWindows is the window count
	// playback must see before it dispatches anything else, and
	// ScriptAwaitDeadline bounds the wait so a pane that never arrives stalls the
	// tape for a few seconds rather than forever.
	ScriptAwaitWindows  int
	ScriptAwaitDeadline time.Time
	// Tape manager UI
	ShowTapeManager   bool              // True when showing tape manager overlay
	TapeManager       *TapeManagerState // Tape manager state
	TapeRecorder      *tape.Recorder    // Tape recorder for recording sessions
	TapeRecordingName string            // Name of current recording
	// Menu is the declared menu open now (see config.MenuConfig), "" when none is.
	Menu       string
	menuOrigin MenuOrigin
	// MenuAction is an action a menu opened over an overlay hands back to
	// that overlay, for the one call of its key handler that follows
	// (internal/input/menus.go). It is set and cleared there only.
	MenuAction MenuHandback
	// MouseOff is set by toggle_mouse: the client stops asking the host for
	// mouse reports, so the host terminal selects text itself, as tmux's
	// mouse off does.
	MouseOff bool
	// MarkedPaneID is the pane mark_pane marked, for join_marked_*.
	MarkedPaneID string
	// ProcessingRemoteKeys is true while the client handles a key that
	// send-keys typed, and between the keys of a send-keys chain that is
	// still running. Every gate that asks "did the person press this" reads
	// it. A RemoteKeyMsg sets it for its own handler call, whatever other
	// chains do, and a key from the person's terminal clears it for its call.
	// Between messages it follows remoteKeyChains.
	ProcessingRemoteKeys bool
	remoteKeyChains      int
	// ScriptWait is the WaitFor playback is holding for, nil when none is.
	ScriptWait *scriptWait
	// ScriptFailure says where and why the last tape stopped, empty when it
	// ran to the end. It outlives script mode, so tuios tape play can exit
	// with it after the person quits.
	ScriptFailure           string
	scriptInFlight          bool
	scriptRequestID         string
	scriptRestoreAnimations bool
	scriptCmds              []tea.Cmd
	// Kitty Graphics Protocol passthrough for forwarding to host terminal
	KittyPassthrough *KittyPassthrough
	// Sixel Graphics passthrough for forwarding to host terminal
	SixelPassthrough *SixelPassthrough
	kittySymbols     kittySymbols
	TextSizingState  *TextSizingState
	richSource       map[string]bool
	richPicture      map[string]bool
	mermaidPics      map[mermaidPicKey]*mermaidPic
	mermaidCellsWait bool
	blocks           map[ckBlockKey]*ckBlockDrawing
	blockGen         int
	blockView        int
	PostRenderWriter *PostRenderWriter
	cursorPark       *cursorPark
	// Hooks manager for shell-command hooks
	HookManager *hooks.Manager
	// PendingClipboardSet receives clipboard content from guest apps via OSC 52.
	// The bubbletea Update loop reads this and calls tea.SetClipboard().
	PendingClipboardSet chan ClipboardSetMsg
	// PendingSessionCreate receives the outcome of a detached-session creation,
	// which is a daemon round trip and must not run on the Update goroutine: it
	// contends with the background session poll for the client's round-trip lock,
	// and blocking here stops input, rendering and socket draining.
	PendingSessionCreate chan SessionCreatedMsg
	// PendingSessionKill receives the outcome of killing another session, which
	// waits for the daemon's post-kill listing and so cannot run on the Update
	// goroutine either.
	PendingSessionKill chan SessionKilledMsg
	// PendingNotification receives guest desktop notifications and bells (OSC 9/777/99, BEL).
	// The notification callbacks fire on a window's PTY writer goroutine, so they cannot
	// touch OS notification state directly (the render goroutine reads m.Notifications).
	// The bubbletea Update loop drains this and calls ShowNotification, mirroring the
	// PendingClipboardSet path.
	PendingNotification chan NotificationMsg
	fileWatch           fileWatcher
	// PendingCwdChange receives OSC 7 working-directory changes from windows'
	// PTY goroutines. The bubbletea Update loop drains it and, for the focused
	// window only, checks whether the new directory carries a .tuios.tape. This
	// is the detection half of the project-tape feature; it never executes
	// anything, it only stats, reads to hash, and surfaces a passive indicator.
	PendingCwdChange      chan CwdChangedMsg
	PendingNvimNavigation chan NvimNavigationMsg
	nvimNavigators        map[string]bool
	pendingNvimNavigation *pendingNvimNavigation
	dndIn                 *dndInbox
	dndPanes              map[string]*dndPane
	dndPress              *dndPress
	dndDrag               *dndDrag
	dndHost               dndHostState
	dndDropGen            int
	dndDropArmed          bool
	tapeDetect            tapeDetectState
	// ShowTapeReview is true when the project-tape review/trust dialog is open.
	// TapeReview holds its state (path, trust status, reviewed content, header).
	// See tape_review.go.
	ShowTapeReview bool
	TapeReview     *TapeReviewState
	review         reviewState
	// OverlayLeader is a leader pressed while an overlay owned the keyboard,
	// held for one key to see whether it starts the screenshot chord. Nil when
	// nothing is held. See routeOverlayScreenshot in internal/input.
	OverlayLeader *tea.KeyPressMsg
	hostFocus     hostFocusState
	// Scrollback browser overlay
	ShowScrollbackBrowser bool
	ScrollbackBrowser     *scrollback.Browser
	// Command palette overlay
	ShowCommandPalette  bool
	fileSearch          bool
	fileSearchGen       uint64
	fileSearchCancel    *atomic.Bool
	fileSearchScanning  bool
	fileSearchTruncated bool
	fileSearchErr       string
	paletteList         listnav.State
	// PaletteSessionItems holds the session/window entries built from the
	// session tree when the palette opens. Built once per open, not per frame:
	// BuildSessionTree does a blocking daemon round trip in daemon mode, and the
	// palette renders every frame it is on screen.
	PaletteSessionItems []CommandPaletteItem
	// PaletteItems is the merged list the filter and the renderer read. Merging
	// is cached because the renderer rebuilds the filtered list every frame, and
	// with a few thousand programs on $PATH rebuilding the merge that often is
	// the difference between a palette that costs nothing to leave open and one
	// that does not.
	PaletteItems []CommandPaletteItem
	// PaletteKeybindItems holds the one-row-per-action keybind entries, built
	// when the palette opens. They are reached only behind the "#" token (see
	// splitPaletteKeybinds): a few hundred rows in the palette's default list
	// would bury the twenty commands it is actually for.
	PaletteKeybindItems []CommandPaletteItem
	// PaletteSettingItems is one row per settings row, built when the palette
	// opens. See CommandPaletteItem.Setting.
	PaletteSettingItems []CommandPaletteItem
	// Launcher overlay: the programs a session can start, which is a separate
	// list from the palette's commands (see launcher.go for why).
	ShowLauncher bool
	launcherList listnav.State
	// LauncherItems holds one row per known program, rebuilt when a scan lands
	// rather than per frame: with a few thousand programs on $PATH, rebuilding
	// per frame is the difference between an overlay that costs nothing to
	// leave open and one that does not.
	LauncherItems []LauncherItem
	// LauncherOpenedAt is when the launcher last opened, which is when its
	// scan started: "Scanning for programs" waits for the loading delay.
	LauncherOpenedAt time.Time
	loadingFrameAt   time.Time
	whichKeyCache    whichKeyCache
	launcherIcons    *launcherIcons
	replyPreview     *replyPreview
	pastePreview     pastePreviewState

	quickLook           quickLookState
	paths               pathCache
	replyEditorDir      string
	scrollbackEditorDir string
	launcherIconCells   []launcherIconPlacement
	pathApps            *applist.Cache
	desktopApps         *desktopCache
	guestApps           []applist.Entry
	launcherSource      []applist.Entry
	launchHistory       *applist.Frecency
	pendingSeeds        []pendingSeed
	// The agent mailbox overlay and the mirror behind it. See agent_mail.go.
	ShowAgentMail bool
	AgentMail     AgentMailState
	draftMouse    draftPress
	// The Inbox overlay and the mirror of the daemon's attention queue behind
	// it. See inbox.go.
	ShowInbox         bool
	Inbox             InboxState
	inboxEvents       chan tea.Msg
	stopInbox         func()
	transfers         transfersState
	farOpen           farOpenState
	agentsSwitchedOff bool

	// FederationHosts is what the daemon last said about the machines in the
	// [hosts] config table, with the sessions each holds. The rail reads it and
	// never writes it; it is refreshed by a Cmd, never inside Update. See
	// sidebar_hosts.go.
	FederationHosts []FederationHost
	// LocalLoad is the load of the machine the client's daemon runs on, as
	// the last host listing carried it: Cores is 0 until one did.
	LocalLoad             federation.Load
	federationGen         uint64
	federationPolling     bool
	federationPushed      bool
	hostSignInUntil       time.Time
	hostSignInPending     map[string]time.Time
	hostOpen              *hostOpenPending
	hostOpenGen           uint64
	hostReconnectDeferred bool
	federationTickGen     uint64
	hostTests             map[string]federation.HostReport
	hostTestRunning       bool
	hostsToApply          []string
	navigator             navigatorState
	gotoPicker            gotoPickerState
	navLayoutPick         string
	buffers               bufferChooser
	pasteBufs             *pastebuf.Store
	pasteHold             pasteHold
	// Machine picker overlay: which machine a new window's process runs on.
	//
	// A session can hold windows from several machines, and the only way to
	// make one was the command line. This is the way from inside, and it is a
	// picker rather than a prompt because the answer is one of a known list.
	ShowHostPicker bool
	// HostPickerPurpose is what the machine picker was opened for: a window or
	// a session. The list and the keys are the same; only the title and what
	// enter does differ. See host_picker.go.
	HostPickerPurpose HostPickerPurpose
	hostList          listnav.State
	// HostPickerItems is the list as it stood when the picker opened. It is
	// held rather than rebuilt per keystroke so the rows cannot move under the
	// cursor while somebody is typing: a link coming up mid-search would
	// otherwise change what Enter means.
	HostPickerItems     []HostPickerItem
	attachedHosts       attachedHostView
	attachedHostsLoaded bool
	attachedHostsCh     chan AttachedHostsMsg

	// Layout picker overlay
	ShowLayoutPicker bool
	LayoutCycleIndex int             // Current index in saved layouts for cycling
	MultifocusSet    map[string]bool // Window IDs that receive keystrokes simultaneously
	UseBSPLayout     bool            // true = BSP tiling, false = master-stack
	announceDepth    int
	// Scrolling tiling (niri-like) layout
	UseScrollingLayout        bool                            // true = scrolling columns mode
	WorkspaceScrollingLayouts map[int]*layout.ScrollingLayout // per-workspace scrolling layouts
	scrollingFocusSyncing     bool
	pendingScrollColumns      map[int][]session.SerializedScrollColumn
	LayoutPickerItems         []LayoutTemplate
	// LayoutPickerLoading is true while the picker waits on the daemon's list.
	LayoutPickerLoading bool
	LayoutPickerMode    string // "load" or "save"
	layoutList          listnav.State
	layoutSaveName      textedit.Line

	// Settings overlay state.
	ShowSettings     bool
	SettingsCategory int // active settings category (tab) index
	settingsList     listnav.State
	SettingsEditing  bool // true while a text setting is being edited inline
	settingsEdit     textedit.Line
	settingsSearch   settingsSearchState
	settingsUndo     []settingsUndoEntry
	wheelMoving      bool

	// Keybind manager overlay state. ShowKeybindManager and KeybindTab are
	// exported because the renderer and the input handler live in other
	// packages; everything else belongs to the overlay alone (see
	// keybind_manager.go) and is reached through methods, so the recorder's
	// armed flag cannot be set from anywhere that would not also disarm it.
	ShowKeybindManager bool
	KeybindTab         int
	keybinds           keybindManager

	// Theme picker overlay state.
	ShowThemePicker     bool
	presetPicker        presetPickerState
	presetHint          *presetSuggestion
	themeList           listnav.State
	ThemePickerOriginal string // theme active when the picker opened, for cancel
	// ThemePickerShade lists all themes, only dark ones or only light ones.
	ThemePickerShade int
	// ThemePickerDelete is the theme a ctrl+d asked to delete, waiting for y.
	ThemePickerDelete string
	// ThemePickerPair is the theme a pick waits on while the picker asks
	// whether it is for the dark theme, the light one or both. Empty when
	// it asks nothing. See appearance.theme_light.
	ThemePickerPair  string
	schemeApplied    string
	themeHook        themeHookState
	themeEvent       themeEventState
	gradient         borderGradient
	themeEditor      themeEditorState
	workspaceMarkSet map[int]uint8

	// Glyph picker overlay state, the theme picker's opposite number.
	ShowGlyphPicker     bool
	glyphList           listnav.State
	GlyphPickerOriginal string // set active when the picker opened, for cancel
	// GlyphPickerSamples is one preview per set, built when the picker opens.
	// Each borrows the active selection to read what its set draws, which is
	// not something to do per frame while composing one.
	GlyphPickerSamples map[string]glyphSample

	// Screen saver effect picker state. The third of the family, and the one
	// whose preview is an animation rather than a still: see effect_picker.go.
	ShowEffectPicker     bool
	effectList           listnav.State
	EffectPickerOriginal string // effect set when the picker opened
	effectPreview        effectPreview

	// Dock layout editor state. The dock is three ordered lists, which is the
	// one thing on the settings page no row can express.
	ShowDockEditor     bool
	dockList           listnav.State
	DockEditorOriginal dockLists // the lists when it opened, for cancel

	// Rail layout editor state. The rail's sections are one ordered list with a
	// percent on each entry, which no settings row can hold either.
	ShowSectionEditor     bool
	sectionList           listnav.State
	SectionEditorOriginal string // the layout when it opened, for undo

	// Floating overlay placement + mouse hit-testing. Each overlay kind keeps
	// its own drag displacement in OverlayOffsets so panels (e.g. settings and
	// the theme picker) can be moved independently. OverlayHits records every
	// panel rendered in the current frame, back to front, so the mouse handlers
	// can route clicks to the topmost panel under the cursor.
	OverlayOffsets map[string][2]int
	overlayAnchors map[string]overlayAnchor
	OverlayHits    []overlayPanelHit
	OverlayDrag    overlayDragState
	// OverlayZOrder is the stacking order of the currently-open draggable
	// overlays, bottom to top. Clicking a panel moves it to the end (top).
	OverlayZOrder []string

	// Sidebar mouse hit-testing and view state. SidebarHits records the on-screen
	// rectangle of every sidebar row rendered in the current frame, so the mouse
	// handlers can route clicks, wheels, and right-clicks without re-deriving the
	// layout. Each of the rail's sections holds its own scroll offset, so the
	// wheel scrolls the one under the pointer and no header can be scrolled
	// away; sidebarSectionY is where each section was drawn, which is how a
	// wheel event finds its section. Scrolls are clamped by the next render.
	SidebarHits               []sidebarRowHit
	sidebarSessions           [2]sidebarSessionView
	sidebarSessionEvents      chan sidebarSessionEvent
	sidebarSessionStateEvents [2]chan sidebarSessionEvent
	sidebarSessionStateMu     [2]sync.Mutex
	sidebarSessionDone        chan struct{}
	sidebarSessionFocus       int
	// SidebarScroll is each rail section's own scroll offset, indexed by
	// section, so a reset or a swap covers every section at once.
	SidebarScroll [sidebarSectionCount]int

	stopMu sync.Mutex
	stopCh chan struct{}
	stopOn bool

	sidebarAgentAnchor     sidebarScrollAnchor
	sidebarAgentsUnfolded  bool
	sidebarSubagentFold    map[string]bool
	sidebarSubagentFoldGen int
	subagentHideDue        time.Time
	subagentHideArmed      time.Time
	sidebarReveal          sidebarRevealState
	sidebarSectionY        [sidebarSectionCount][2]int
	// SidebarSectionSplit is the pinned section's dragged share of the rail in
	// percent, or 0 for the layout's own. Persisted in the sidebar state file.
	// sidebarSplit is the drag on the divider and sidebarSplitGeom what the
	// last frame wrote down for it. See sidebar_split.go.
	SidebarSectionSplit int
	sidebarSplit        sidebarSplitState
	sidebarSplitGeom    sidebarSplitGeom
	sidebarStripRows    []sidebarStripRow
	// Tooltip is the hover label state shared by the collapsed rail and the
	// dock's session controls: which control the pointer is on, when it landed,
	// and whether the label has been drawn. Gesture-scoped runtime state; the
	// Shown latch is also the tick gate.
	Tooltip   tooltipState
	filesView fileViewState
	filesTree fileTreeState
	filesRows fileRowsCache

	gitView             gitView
	railCustom          railCustomState
	filePrompt          filePromptState
	worktreePrompt      worktreePromptState
	pluginApproval      pluginApprovalState
	upgradeConfirm      upgradeConfirmState
	prompt              promptState
	pressingTapeKeys    bool
	runningScriptAction bool
	hooksWaitTold       bool
	fileClip            fileClipboard
	sidebarPendingCmd   tea.Cmd
	integrationAsk      integrationAsk
	// SidebarPeek is the session the terminals section is previewing while the
	// pointer or the rail cursor rests on its row. Gesture-scoped runtime state
	// like the marquee: never persisted, cleared by the same motion stream that
	// created it.
	SidebarPeek string
	// SidebarAgentFilter and SidebarAgentSort are the agents section's two
	// controls: which sessions it lists ("all" or "session") and in what order
	// ("priority" or "recent"). Persisted in the sidebar state file; an empty or
	// unrecognised value reads back as the default.
	SidebarAgentFilter string
	SidebarAgentSort   string
	// SidebarAgentsSeen is set, and persisted, once an agent has run where
	// this client could see it. See agentsSeen.
	SidebarAgentsSeen         bool
	agentIntegrationInstalled bool
	agentsPage                agentsPageState
	agentNoticesDismissed     map[string]bool
	machineOffersShown        map[string]bool
	machineOffersNever        map[string]bool
	machineOffersRemoved      map[string]bool
	settingsAgentsOpen        *bool
	// SidebarCollapsed is the rail folded down to its glyph strip. It is one of
	// the two states the user can put the rail in (the other is the stored
	// width, which a drag on the edge still sets freely); the responsive
	// breakpoints fold over it exactly as they fold over the stored width.
	// Persisted in the sidebar state file.
	SidebarCollapsed bool
	// SidebarCollapsedRepos is the set of repositories whose worktree group is
	// folded shut on the rail, keyed by repository name. A group holds whatever
	// sessions that repository has right now, so the name is the only key that
	// survives the sessions in it being replaced. Persisted in the sidebar
	// state file. See sidebar_worktrees.go.
	SidebarCollapsedRepos map[string]bool
	// SidebarCollapsedHosts is the set of machines whose group is folded shut
	// on the rail, keyed by host name. Persisted in the sidebar state file.
	// See sidebar_hosts.go.
	SidebarCollapsedHosts map[string]bool
	// SidebarHostOrder is the user's drag-defined order of the machine groups,
	// applied over the daemon's sorted table (machines not named here keep
	// their sorted order after the named ones). This machine is always first
	// and is not in it. SidebarHostIDs is the machine order displayed last
	// frame, which is what a starting drag snapshots. Persisted in the sidebar
	// state file.
	SidebarHostOrder     []string
	SidebarHostIDs       []string
	sidebarMachineGroups bool
	// SidebarHostSessionOrder is the drag-defined session order of each other
	// machine, keyed by host name, kept apart from SidebarOrder so a drag while
	// attached on build cannot write build's names over this machine's order.
	SidebarHostSessionOrder map[string][]string
	sessionOrderLocal       map[string]string
	sessionOrderPush        map[string]*sessionOrderPush
	// SidebarOrder is the user's drag-defined session order, applied over the
	// daemon's creation-order list (sessions not named here keep their natural
	// order after the named ones). Persisted in the sidebar state file.
	// SidebarSessionIDs is the session order actually
	// displayed last frame (the draft order while a drag is in progress), which
	// is what a starting drag snapshots. SidebarDrag carries the press-or-drag
	// gesture on a session row between mouse events.
	SidebarOrder      []string
	SidebarSessionIDs []string
	SidebarDrag       sidebarDragState
	// SidebarAccents is the accent the user gave a window, by window ID: either
	// a theme ANSI slot or a picked colour (see Accent). Persisted alongside the
	// order; the daemon does not own it, so it is this client's view of its own
	// windows.
	SidebarAccents map[string]Accent
	// SidebarAgentSeen is the unread bit of finished panes, by window ID: an
	// entry means "this done pane has been looked at". Whether a client has
	// looked at a pane is that client's business, not the daemon's, so it lives
	// beside the accents rather than in session state.
	SidebarAgentSeen map[string]bool
	// SidebarAgentSeenSeq is, by window ID, the daemon's finished-turn count
	// (CompletionSeq) the pane had when this client's user last focused it. A
	// pane at rest whose count has moved past it finished a turn nobody here
	// has looked at, and the rail draws it as finished and unread.
	SidebarAgentSeenSeq map[string]uint64
	// SidebarAgentSeenAt is, by window ID, when this client's user last had an
	// agent pane in front of them (Unix nanoseconds). It is written when focus
	// enters or leaves an agent pane, so for a pane not in front of the user it
	// is the moment they looked away: where "while you were away" starts.
	// Persisted beside SidebarAgentSeenSeq, and per client for the same reason.
	SidebarAgentSeenAt map[string]int64
	// SidebarAgentViewed is, by window ID, the agent's state-entry time
	// (AgentStateAt) when this client's user last had its thread on screen
	// in the agents view. A row whose agent finished or failed after it draws
	// the unseen mark. See agent_unseen.go.
	SidebarAgentViewed map[string]int64
	agentAlertRuns     agentseen.AlertLedger
	sidebarStateSocket string
	pendingAgentAlerts map[string]pendingAgentAlert
	programAlerts      map[string]*programAlert
	// Accent picker state: what is being accented (a pane or a session) and
	// which one, the colour under the cursor and where that cursor is, and the
	// hit geometry the renderer records as it draws the grid, the hue strip and
	// the chips.
	ShowAccentPicker     bool
	AccentPickerTarget   AccentTarget
	AccentPickerTargetID string
	AccentPicker         accentPickerState
	accentHits           []accentHit
	accentDrag           accentHitKind
	accentDragging       bool
	accentDragCol        int
	// Sidebar hover: the last mouse position seen inside the band, so the row
	// under the cursor is highlighted the way overlay rows are. SidebarHoverActive is
	// cleared as soon as motion leaves the band.
	SidebarHoverActive bool
	SidebarHoverX      int
	SidebarHoverY      int
	// SidebarEdge carries the width-resize gesture on the rail's edge rule
	// between mouse events; while Active the pointer column sets the rail width.
	SidebarEdge sidebarEdgeState
	// Sidebar marquee: the identity of the hovered row whose overflowing title is
	// scrolling and when that scroll began. An empty key means nothing scrolls,
	// so the render tick idles; sidebarMarqueeSeen is the per-frame mark that
	// keeps the key alive only while its row still renders as hovered.
	SidebarMarqueeKey   string
	SidebarMarqueeStart time.Time
	sidebarMarqueeSeen  bool
	// Sidebar keyboard focus scope (the rail). While SidebarFocused the rail owns
	// the keyboard: pane and window bindings do not fire, and the cursor row is
	// SidebarNav[SidebarCursor]. SidebarNav is the ordered list of interactive
	// rows the last frame rendered, in drawn order, the keyboard equivalent of
	// SidebarHits, so keyboard navigation lands on exactly the rows a click
	// would. Every control the renderer records a rectangle for is in it, its own
	// key or not: the walk is the one route that depends on no binding but the
	// cursor keys, and the section keys are the way past the controls for anyone
	// who does not want to step on them. SidebarRevealedForFocus records that entering the
	// scope had to turn the sidebar on, so exiting turns it back off.
	SidebarFocused          bool
	SidebarCursor           int
	SidebarNav              []sidebarNavRow
	SidebarRevealedForFocus bool
	sidebarReturnWindow     string
	sidebarLastRow          sidebarNavRow
	sidebarLastRowSet       bool
	sidebarFollowSession    string
	sidebarFollowFile       bool
	sidebarFollowFileName   string
	sidebarFollowFileGen    uint64
	sidebarCursorWant       sidebarNavRow
	sidebarCursorWantSet    bool
	zoomRelayout            bool
	zoomCanvasNow           zoomCanvas
	sidebarCache            sidebarRenderCache
	railNow                 time.Time
	secondarySidebarCache   sidebarRenderCache
	secondarySidebarHits    []sidebarRowHit
	secondaryRailView       secondaryRailView
	secondaryWidthPref      int
	secondaryCollapsed      bool
	sidebarFocusSecondary   bool
	sidebarDrawing          bool
	sidebarDrawingWidth     int
	sidebarDrawingEdge      sidebarEdge
	railCustomSecond        railCustomState
	sidebarTitles           map[string]railTitleEntry
	sidebarTitlePending     bool
	sidebarTitleGen         uint64

	// Right-click gesture disambiguation. A plain right press on a pane arms
	// both a corner resize and a pending context menu; the release decides.
	// Movement past the drag threshold keeps the resize, a release without it
	// cancels the resize and opens the menu at the press cell.
	RightClickPending bool
	RightPressX       int
	RightPressY       int

	// ClickToTypePending is armed by a left press on a pane's content area in
	// window-management mode. A release without a drag focuses the pane and
	// enters terminal mode, so clicking a pane is enough to start typing. The
	// title bar and borders never arm it, so dragging is unaffected.
	ClickToTypePending bool

	// Ctrl-drag gesture: a ctrl + left press on a pane's content is a
	// newcomer-friendly way to grab a window for moving without aiming at the
	// title bar. CtrlDragPending arms the click-vs-drag decision; it commits to
	// a move (CtrlDragging, routed through the same drag machinery as a
	// title-bar drag) only once the pointer passes the drag threshold, and a
	// sub-threshold release falls through to the ctrl+click multi-select.
	// CtrlDragIndex is the grabbed window. A committed drag drops on release or
	// as soon as a mouse event arrives without ctrl held.
	CtrlDragPending bool
	CtrlDragging    bool
	CtrlDragIndex   int
	// CtrlClickLink is the link under a ctrl + left press, when the press
	// was on one and link_click allows ctrl. A release before the drag
	// threshold opens it; a drag clears it. See handleMouseRelease.
	CtrlClickLink string

	// ContextMenu is the open shift+right-click menu, or nil. It is deliberately
	// not one of the draggable overlay kinds: a context menu is anchored to the
	// cell it was opened on and is dismissed by the next click, so it has no use
	// for a drag offset or a place in the click-to-raise order.
	ContextMenu *ContextMenu

	pathPicker *pathPicker

	menuWorkspace int
	menuFrom      *ContextMenu
	menuSession   string
	menuRepo      string
	menuFile      fileMenuTarget

	// UserConfig is the loaded user configuration. The settings page mutates
	// it in place and persists it so live changes survive a restart. May be
	// nil if the config failed to load at startup.
	UserConfig *config.UserConfig

	startupApplied bool

	pendingStartTerminalMode bool
	pip                      pipState
	railPaneView             railPaneState
	// PiPPressed says the button that is down was pressed on the
	// picture-in-picture view, so its release is the view's as well.
	PiPPressed bool
	// MiddleBindingPressed says a terminal-mode binding took the middle
	// button press, so its release is the binding's as well.
	MiddleBindingPressed bool
	scratchPending       string
	scratchPendingAt     time.Time
	scratchReturnID      string
	scratchStarted       map[string]time.Time
	deadScratch          map[string]bool
	scratchBase          int
	scratchBaseTiling    bool
	scratchViewName      string
	scratchReturnMode    Mode
	forceTerminalMode    bool
	layoutInstant        bool
	msgClock             time.Time

	sessionUnarranged bool
}

// Notification represents a message shown in the dock's right-hand block.
//
// There is no Animation field any more. The old corner toast faded in and out,
// which meant every notification's appearance depended on how recently a frame
// had been composed; the message now occupies a block of the dock and its age
// is carried by the dock's hairline burning down beneath it, which is a
// function of wall-clock time alone.
type Notification struct {
	ID        string
	Message   string
	Type      string // "info", "success", "warning", "error"
	StartTime time.Time
	Duration  time.Duration

	// Target is the pane the message came from, when it came from one. A
	// targeted message is clickable: its body jumps there and dismisses it,
	// which is why it draws underlined. Nil for the messages with no source to
	// go to (copy confirmations, config warnings, switch failures).
	Target *NotifTarget

	// Sticky messages ignore Duration and wait to be dismissed with esc. An
	// error is sticky by default: nothing carrying a failure should vanish on a
	// timer the user did not start.
	Sticky bool

	// AgentState is the agent state a message announces, empty for every other
	// message. The dock draws that state's own mark and colour for it
	// (agentMark), the one the rail and the title bar draw, rather than a
	// Nerd Font severity icon that said the same thing in a different shape.
	AgentState string

	// Source names where the message came from, resolved when it was raised.
	// Empty means tuios itself. See notifSourceName.
	Source string

	// Ambient marks a notice tuios raises on its own about what changed
	// around this client. It never covers a live message that is not
	// ambient. See queueNotification.
	Ambient bool
}

// NotifTarget names the pane a message came from. The workspace is deliberately
// absent: it is resolved from live state at jump time, because a stored index
// goes stale the moment the window is moved.
type NotifTarget struct {
	// Host is the machine the session is on, as attachedMachine names it, when
	// the message names one: the Inbox's alerts do. Empty means the machine
	// the client is attached to, which is every other message.
	Host      string
	SessionID string
	WindowID  string
	// Thread, when set, is the mail thread the message is about: activating
	// the message opens the mailbox on it rather than jumping to a pane.
	Thread uint64
	// ClipboardAsk, when set, names a pane's clipboard write that waits for
	// the user: activating the message allows it. See clipboard_osc52.go.
	ClipboardAsk uint64
	// DaemonRestart marks the version notice: activating it opens the
	// restart dialog. See warnOnBuildMismatch.
	DaemonRestart bool
	// AgentInstall names the harness of an integration notice: activating
	// the message asks to install or update that integration, then does it
	// the way the Agents tab's action row does. See installFromNotice.
	AgentInstall string
	// FarOpen names an offer to copy a file of another machine here and
	// open it: activating the message takes it. See
	// sidebar_file_open_far.go.
	FarOpen uint64
	// ClipboardVersion counts the changes to that ask's text. It is part of
	// the target, so a message drawn with one text is not the same message as
	// one holding another. See clickVisibleNotification.
	ClipboardVersion uint64
	// OfferTarget, OfferDest and OfferMachine are the offer to add a
	// machine: the ssh target Never stores, the destination Add sets up, and
	// the name the message shows. See machine_offer.go.
	OfferTarget, OfferDest, OfferMachine string
	// InboxItems is the ids of the Inbox items an alert is about, joined by
	// spaces. The message leaves the dock when the last of them closes, so
	// an alert does not stay after what it announced is over. See
	// clearClosedInboxAlerts.
	InboxItems string
}

// LogMessage represents a log entry with timestamp and level.
type LogMessage struct {
	Time    time.Time
	Level   string // INFO, WARN, ERROR
	Message string
	// Source names where the message came from, when a notification from a
	// pane wrote it. Empty means tuios itself.
	Source string
}

// KeyEvent represents a captured keyboard event for the showkeys overlay.
type KeyEvent struct {
	Key       string    // The key string representation
	Modifiers []string  // Modifier names (Ctrl, Shift, Alt, Cmd)
	Timestamp time.Time // When the key was pressed
	Count     int       // Number of consecutive identical keys
	Action    string    // Resolved action name (optional)
}

func createID() string {
	return uuid.New().String()
}

var verboseLog = debuglog.Enabled

type nestedSwitchError struct{ inside, target string }

func (e *nestedSwitchError) Error() string {
	return fmt.Sprintf("You are inside session %q. Switching this pane to %q would show tuios inside itself.", e.inside, e.target)
}

// SwitchToSession detaches from the current daemon session and attaches to another.
// The connection to the daemon stays open. Only the session binding changes.
//
// The round trip goes first and the windows come down only once it has landed.
// Tearing down first meant a switch the daemon refused answered "show me that
// session" by leaving the user with no session at all, and nothing here could
// put it back: closing a window nils its terminal and its PTY behind a latch
// that only goes one way.
func (m *OS) SwitchToSession(targetSession string) error {
	if m.DaemonClient == nil {
		return errfmt.Errorf("not in daemon mode")
	}
	if targetSession == "" {
		return errfmt.Errorf("session name cannot be empty")
	}
	m.cancelHostOpen()

	m.LogInfo("[SWITCH] Starting: %s → %s", m.SessionName, targetSession)

	savedWidth, savedHeight := m.Width, m.Height
	state, err := m.DaemonClient.SwitchSession(targetSession, savedWidth, savedHeight)
	if err != nil {
		m.LogError("[SWITCH] %v", err)
		if rollback, ok := errors.AsType[*session.SwitchRollback](err); ok {
			if rollback.State != nil {
				m.SessionName = m.DaemonClient.SessionName()
				m.rebuildForSession(rollback.State, savedWidth, savedHeight)
				m.MarkAllDirty()
			}
			if nested, ok := session.AsNestedAttach(err); ok && rollback.State != nil {
				return &nestedSwitchError{inside: nested.Session, target: targetSession}
			}
			return err
		}
		return errfmt.Errorf("switch to %q failed: %w; still on %q", targetSession, err, m.SessionName)
	}
	from := lastSession{host: m.attachedMachine(), name: m.SessionName}
	m.SessionName = m.DaemonClient.SessionName()
	m.rebuildForSession(state, savedWidth, savedHeight)
	m.switchSeenPending = true
	m.leftSession(from)
	m.SyncDockContext()
	m.resetAgentMail()
	m.QueueClientEvent(ClientEvent{Type: "agent-mail-load"})

	m.MarkAllDirty()
	m.LogInfo("Session switch complete: now on %s with %d windows", m.SessionName, len(m.Windows))
	m.ShowNotification("Session: "+m.SessionName, "success", m.Settings.NotificationDuration)
	m.FireAttached()
	return nil
}

func (m *OS) rebuildForSession(state *session.SessionState, savedWidth, savedHeight int) {
	m.pruneTMLViews(state)
	for _, w := range m.Windows {
		if w.DaemonMode && w.PTYID != "" {
			m.DaemonClient.UnsubscribePTY(w.PTYID)
		}
		w.Close()
	}

	m.Windows = nil
	m.FocusedWindow = -1
	m.WorkspaceFocus = make(map[int]int)
	m.WorkspaceTrees = make(map[int]*layout.BSPTree)
	m.WorkspaceScrollingLayouts = make(map[int]*layout.ScrollingLayout)
	m.pendingScrollColumns = nil
	m.WindowToBSPID = make(map[string]int)
	m.BSPIDToWindowID = make(map[int]string)
	m.NextBSPWindowID = 1
	m.Animations = nil
	m.MultifocusSet = nil
	m.MultiCopy = nil
	m.CurrentWorkspace = 1
	m.SubscribedPTYs = make(map[string]bool)
	m.paneRequests = nil
	m.pip = pipState{occluder: m.pip.occluder[:0]}
	m.cockpitLiveForget()

	m.Width, m.Height = savedWidth, savedHeight
	m.EffectiveWidth, m.EffectiveHeight = savedWidth, savedHeight
	m.sessionClients = m.DaemonClient.AttachClientCount()
	if state != nil && state.Width > 0 && state.Height > 0 {
		m.EffectiveWidth, m.EffectiveHeight = state.Width, state.Height
	}
	if m.DaemonClient != nil {
		m.SessionReserve = m.DaemonClient.SessionLayoutReserve()
	}

	if state == nil || len(state.Windows) == 0 {
		m.sessionUnarranged = true
		m.adoptEmptySessionVersion(state)
		if state == nil {
			state = &session.SessionState{}
		}
		m.adoptSessionLabels(state)
		return
	}

	if err := m.RestoreFromState(state); err != nil {
		m.LogError("Failed to restore state: %v", err)
	}
	m.Width = savedWidth
	m.Height = savedHeight

	m.rehydrateWindows()
	m.TriggerAltScreenRedraws()
}

func (m *OS) stopSignal() <-chan struct{} {
	m.stopMu.Lock()
	defer m.stopMu.Unlock()
	if m.stopCh == nil {
		m.stopCh = make(chan struct{})
	}
	return m.stopCh
}

func (m *OS) signalStop() {
	m.stopMu.Lock()
	defer m.stopMu.Unlock()
	if m.stopCh == nil {
		m.stopCh = make(chan struct{})
	}
	if !m.stopOn {
		m.stopOn = true
		close(m.stopCh)
	}
}

// Cleanup performs cleanup operations when the application exits.
// Cleanup releases per-session resources. It closes the daemon client, which
// stops the client read loop and drops the daemon-side connection, so an SSH or
// web session ending does not leak a goroutine, a socket, and a daemon connState.
// TUIClient.Close is idempotent, so calling Cleanup more than once is safe.
// State should be synced to the daemon before Cleanup, on the UI goroutine.
func (m *OS) Cleanup() {
	if m.IsDaemonSession && m.ExitReason == ExitNormal {
		m.FireDetached()
	}

	m.stopSidebarSessions()
	m.replyPreview.removeOpened()
	m.removeReplyEditorFile()
	m.removeScrollbackEditorFile()
	m.stopWindowExitDrain()
	m.endConfigWatch()
	m.endOmarchyFollow()
	m.endInboxWatch()
	m.endTransfersWatch()
	m.stopFileWatch()
	m.stopThreadWatch()
	m.signalStop()
	m.KittyPassthrough.stopAsyncWriter()
	m.StopDockComponents()
	if m.DaemonClient != nil {
		_ = m.DaemonClient.Close()
		for _, w := range m.Windows {
			w.Close()
		}
		return
	}

	for _, w := range m.Windows {
		w.Close()
	}
}
