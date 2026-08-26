// Package app provides the core TUIOS application logic and window management.
package app

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/ssh"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
	"github.com/google/uuid"
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
	Dragging                 bool
	Resizing                 bool
	BorderResizing           bool // a pane-border drag is moving one edge
	BorderResizeEdge         BorderResizeEdge
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
	Width               int
	Height              int
	Mode                Mode
	// terminalMu guards the m.Windows slice and the per-window dirty flags and
	// render caches against the UI goroutine's render pass. It does NOT guard
	// emulator cell data; that is Window.ioMu.
	//
	//   LOCK ORDER (global, whole process):
	//       app.OS.terminalMu  ->  Window.ioMu  ->  KittyPassthrough.mu / SixelPassthrough.mu
	//
	//   terminalMu is the outermost of the three. renderTerminal is the only
	//   place that holds terminalMu and a window's ioMu at once, and it takes
	//   them in that order. Nothing may take terminalMu while holding any
	//   window's ioMu.
	//
	//   NOT REENTRANT. The holders here (MarkAllDirty,
	//   MarkTerminalsWithNewContent, FlushPTYBuffersAfterResize,
	//   renderTerminal) must not call each other. In particular do not call
	//   MarkAllDirty from inside a renderTerminal locked region.
	//
	//   NEVER BLOCK WHILE HOLDING IT: it is taken on the UI goroutine every
	//   frame, so any block here is a visible stall.
	terminalMu      sync.RWMutex
	LastMouseX      int
	LastMouseY      int
	ShowHelp        bool
	InteractionMode bool                       // True when actively dragging/resizing
	MouseSnapping   bool                       // Enable/disable mouse snapping
	WindowExitChan  chan string                // Channel to signal window closure
	windowExits     windowExitQueue            // Overflow for exits WindowExitChan could not take
	PTYDataChan     chan struct{}              // Signaled by PTY readers when new output arrives (buffered 1, coalescing)
	StateSyncChan   chan *session.SessionState // Channel for thread-safe state sync from callbacks
	ClientEventChan chan ClientEvent           // Channel for thread-safe client join/leave notifications
	Animations      []*ui.Animation            // Active animations
	CPUHistory      []float64                  // CPU usage history for graph
	LastCPUUpdate   time.Time                  // Last time CPU was updated
	RAMUsage        float64                    // Cached RAM usage percentage
	LastRAMUpdate   time.Time                  // Last time RAM was updated
	AutoTiling      bool                       // Automatic tiling mode enabled
	MasterRatio     float64                    // Master window width ratio for tiling (0.3-0.7)
	// TouchClient marks a session whose pointer is a finger. It is per session
	// rather than a config global because one server holds several at once and
	// a phone attaching must not change what the desktop beside it can hit.
	TouchClient bool

	// RemoteClient marks a client process that is not on the user's machine.
	// See OSOptions.RemoteClient.
	RemoteClient bool

	// Capture is capture mode: the window pick, the region drag and the
	// full-screen grab. Zero when the mode is off.
	Capture captureState

	// ShotPreview is the panel that opens after a capture. Zero when closed,
	// and never read by tickNeedsWork: it is a static panel.
	ShotPreview screenshotPreview

	// captureHits are the window rectangles capture mode drew a highlight
	// around this frame, so the click handler reads what was drawn instead of
	// recomputing a layout. Reused between frames, never reallocated.
	captureHits []captureHit

	// shotImagePlaced records that the preview's kitty placement is on the
	// host, so closing the panel takes it down again. shotImageSent records
	// that the picture itself is resident, so a panel that only moved costs a
	// placement and not another upload. shotPlacement is what was last drawn,
	// so an unchanged frame emits nothing at all.
	shotImagePlaced bool
	shotImageSent   bool
	shotPlacement   screenshotPlacementState

	// shotCaptures counts the captures this client has taken. It is what
	// names the picture the host holds, because the host holds one picture
	// under the preview's image id and the only question that matters is
	// whether that picture is this capture's. The file name cannot answer it:
	// two captures in one second share a name.
	shotCaptures int

	// shotDiscarded holds the serials of captures the user dismissed before
	// their file was written. The result that arrives afterwards removes its
	// own file and says nothing. Nil when nothing is pending, which is almost
	// always, so it costs an idle frame nothing.
	shotDiscarded []int

	// BSP tiling state
	WorkspaceTrees      map[int]*layout.BSPTree // BSP tree per workspace
	PreselectionDir     layout.PreselectionDir  // Pending preselection direction (0 = none)
	TilingScheme        layout.AutoScheme       // Default auto-insertion scheme
	SplitTargetWindowID string                  // Window ID to split (set before AddWindow for splits)
	// pendingSplitDir/pendingSplitTarget carry a forced-direction split (ctrl+b |
	// / -) across the daemon round trip. The daemon owns window creation, so the
	// new pane is not built locally; it arrives later through a state sync. These
	// remember which side of which pane the split was meant for so the sync path
	// can honor the direction instead of falling back to the spiral scheme.
	pendingSplitDir        layout.PreselectionDir
	pendingSplitTarget     string
	WindowToBSPID          map[string]int          // Maps window UUID to stable BSP integer ID
	BSPIDToWindowID        map[int]string          // Reverse of WindowToBSPID: BSP integer ID to window UUID (speed-up for getWindowByIntID)
	NextBSPWindowID        int                     // Next BSP window ID to assign (starts at 1)
	RenameKind             RenameKind              // What the open rename editor targets (RenameNone when closed)
	RenameBuffer           string                  // Buffer for new window name
	RenameTargetID         string                  // Window the rename in flight applies to
	renameHit              overlay.Rect            // Where the dialog was drawn, in screen cells
	PrefixActive           bool                    // True when prefix key was pressed (tmux-style)
	WorkspacePrefixActive  bool                    // True when Ctrl+B, w was pressed (workspace sub-prefix)
	MinimizePrefixActive   bool                    // True when Ctrl+B, m was pressed (minimize sub-prefix)
	TilingPrefixActive     bool                    // True when Ctrl+B, t was pressed (tiling/window sub-prefix)
	DebugPrefixActive      bool                    // True when Ctrl+B, D was pressed (debug sub-prefix)
	LastPrefixTime         time.Time               // Time when prefix was activated
	HelpScrollOffset       int                     // Scroll offset for help menu
	HelpCategory           int                     // Current help category index (for left/right navigation)
	HelpSearchMode         bool                    // True when help search is active
	HelpSearchQuery        string                  // Current search query in help menu
	CurrentWorkspace       int                     // Current active workspace (1-9)
	NumWorkspaces          int                     // Total number of workspaces
	WorkspaceFocus         map[int]int             // Remembers focused window per workspace
	WorkspaceLayouts       map[int][]WindowLayout  // Stores custom layouts per workspace
	WorkspaceHasCustom     map[int]bool            // Tracks if workspace has custom layout
	WorkspaceMasterRatio   map[int]float64         // Stores master ratio per workspace
	ShowLogs               bool                    // True when showing log overlay
	LogMessages            []LogMessage            // Store log messages
	LogScrollOffset        int                     // Scroll offset for log viewer
	Notifications          []Notification          // Active notifications
	notifHit               notifHitZones           // where the message block was drawn last frame
	dockWorkspaceHits      []dockWorkspaceHit      // where the dock's workspace pills were drawn last frame
	dockWorkspaceArrowHits []dockWorkspaceArrowHit // where the strip's overflow arrows were drawn last frame
	dockWorkspaceScroll    int                     // index of the first workspace pill the strip draws
	dockWorkspaceScrollFor int                     // the workspace that offset was last pulled into view for
	dockWorkspaceScrollAt  int                     // the viewport width it was pulled into view at
	dockWorkspaceDrag      dockWorkspaceDragState  // the click-or-drag gesture on a workspace pill
	dockItemHits           []dockItemHit           // where the dock's minimized entries were drawn last frame
	dockOverflowHit        dockOverflowHit         // where the entries' overflow marker was drawn last frame
	dockSessionHits        []dockSessionHit        // where the dock's session controls were drawn last frame
	dockSessionHover       DockSessionAction       // which session control the pointer is on, DockSessionNone for neither
	dockCustomHits         []dockCustomHit         // where the custom components were drawn last frame
	dockPlan               dockPlan                // which components are on which side, in draw order
	dockEngine             *dockEngine             // refresh scheduler for the components that move on their own
	// RemoteCommandChan carries a verb the daemon routed to this client, for
	// the hosts that cannot Send into the program directly. See dock_remote.go.
	RemoteCommandChan chan RemoteCommandMsg
	ClipboardContent  string // Store clipboard content from tea.ClipboardMsg
	ShowCacheStats    bool   // True when showing style cache statistics overlay
	// Quit menu state. The menu replaces the old yes/no quit dialog: a small
	// list overlay on the shared list-overlay grammar, registered in OverlayHits
	// as kind "quit" so hover, click and click-away routing come from the same
	// machinery as every other overlay. Items are built once per open (see
	// OpenQuitMenu) so the rows reflect the session state at that moment.
	ShowQuitMenu          bool
	QuitMenuSelected      int
	QuitMenuScroll        int
	QuitMenuItems         []QuitMenuItem
	QuitMenuOtherSessions []string // non-current sessions at open time; [0] is the kill-and-go-next target
	// Close-session confirmation, the micro-dialog the dock's recessed control
	// and ctrl+b X both raise. Its rows are fixed, so only the selection is
	// state; what it says about the session is counted as it draws.
	// SessionCloseTarget is the session it was raised on, by identity, or "" for
	// the attached one: closing that quits this client, closing any other leaves
	// this client where it is.
	ShowSessionClose     bool
	SessionCloseTarget   string
	SessionCloseSelected int
	// Pending resize tracking for debouncing PTY resize during mouse drag
	PendingResizes map[string][2]int // windowID -> [width, height] of pending PTY resize
	// pendingCopy is text a settled multi-click selection will put on the
	// clipboard, and selectionSeq names the gesture it belongs to. See
	// clipboard_copy.go for why the write waits.
	pendingCopy  string
	selectionSeq uint64

	// Performance optimization caches
	cachedSeparator      string // Cached dock separator string
	cachedSeparatorWidth int    // Width of cached separator
	cachedSeparatorChar  string // Glyph the cached separator was built from
	cachedViewContent    string // Cached full View() output to skip rendering on idle ticks
	renderSkipped        bool   // True when frame-skip fired; View() returns cached content
	// tickStats records how the maintenance tick spent itself so the idle
	// benchmark and idle e2e can prove ticks stay cheap when nothing moves.
	tickStats tickStats
	// lastInteractionRender is when a drag/resize motion event last produced a
	// frame. Motion events arrive faster than a frame can be composed, so this
	// bounds how often they are allowed to redraw.
	lastInteractionRender time.Time
	// viewportResizing is set while terminal sizes are still arriving. A retile
	// it drives places panes directly instead of easing them into position, and
	// resizes them visually only, exactly as a mouse resize drag does: a resize
	// is not a transition to be animated, and telling every PTY and backend
	// about a size the user is still choosing is the single most expensive thing
	// in the path.
	viewportResizing bool
	// viewportResizeGen counts terminal resizes so a settle armed by an earlier
	// one can be recognised as stale and ignored.
	viewportResizeGen uint64
	// viewportResizeAt is when the last terminal size arrived, and
	// lastPointerAt when the last mouse event did. They are what make the two
	// deferrals above expire on their own: see resizeDeferralActive. A flag that
	// is only ever cleared by a message arriving is a flag that stays set
	// forever the one time that message does not arrive, and there is no way to
	// guarantee it does - a panic recovered in Update drops the command that
	// would have armed the settle, and a mouse release is lost whenever the
	// pointer leaves the surface the events come from.
	viewportResizeAt time.Time
	lastPointerAt    time.Time

	// zenHidden records the zen-mode border visibility of the last composed
	// frame, so the idle tick can detect the mouse-mode timeout crossing and
	// force a repaint (borders must reappear or melt away exactly once).
	zenHidden bool
	// pointerDown tracks whether a mouse button is held, so a gesture cannot
	// outlive the button that started it even when no further event arrives.
	pointerDown bool
	// pendingBSPSync is set when a resize motion changed window geometry and the
	// BSP tree's ratios have not been re-derived from it yet. The sync exists so
	// the shared-borders separator overlay follows the drag, so it only has to
	// run on frames that are actually composed; it is whole-tree work and running
	// it per motion event makes the drag cost scale with window count.
	pendingBSPSync bool
	// bspResizeScratch holds the layout rebuilt on each resize step. It is
	// reused so a mouse drag does not allocate a map per motion event.
	bspResizeScratch map[int]layout.Rect
	renderCanvas     *lipgloss.Canvas // Reused across frames; resized on change, cleared per frame
	// scrollbarRects is where each pane's scrollbar was drawn on the last frame,
	// keyed by window ID. Recorded by the renderer, read by input.
	scrollbarRects map[string]ScrollbarRect
	// windowButtonRects is where each window's title-bar controls were drawn,
	// keyed by window ID. Recorded by the renderer, read by input. Unlike the
	// scrollbar's it is not cleared per frame: a window composed from its cached
	// layer still has its controls on screen. See pruneWindowButtonRects.
	windowButtonRects map[string][]WindowButtonRect
	// windowButtonHover is the window whose controls the pointer is on, empty
	// for none. The dots style reveals its symbols on it.
	windowButtonHover string
	// Reused per-frame scratch for graphics placement refresh (avoids per-frame allocs)
	kittyPosMap     map[string]*WindowPositionInfo // Reused map for kitty placement refresh
	kittyPosBacking []WindowPositionInfo           // Backing storage for kittyPosMap values
	sixelWinIndex   map[string]*terminal.Window    // Reused window-by-ID index for sixel placement refresh
	sixelPosValue   WindowPositionInfo             // Reused value returned to the sixel refresh callback
	// Scrollback lengths snapshotted before a placement refresh takes the
	// passthrough lock. The refresh callbacks run under kp.mu/sp.mu and must
	// not take a window's ioMu there: the PTY reader holds ioMu while
	// Terminal.Write drives the kitty and sixel callbacks, which take
	// kp.mu/sp.mu, so reading ioMu under kp.mu/sp.mu closes a lock cycle.
	placementScrollbackLen map[string]int
	// SSH mode fields
	SSHSession ssh.Session // SSH session reference (nil in local mode)
	IsSSHMode  bool        // True when running over SSH
	// Daemon mode fields
	IsDaemonSession bool               // True when running as part of a persistent daemon session
	DaemonClient    *session.TUIClient // Client for daemon communication (nil in local mode)
	SessionName     string             // Name of the daemon session (if attached)
	// SessionDisplayName and SessionAccent are the attached session's
	// daemon-owned label and accent slot, both empty when unset. They are
	// labels only: SessionName stays the identity every keyed map, every
	// switch and the daemon's own addressing use.
	SessionDisplayName string
	SessionAccent      string
	// sessionColors is the automatic colour each session was arbitrated onto for
	// the surface currently being drawn, settled once per render by
	// refreshSessionColors. Derived state, never persisted and never synced: it
	// is a pure function of the session names on screen, so every client
	// computes the same map without saying anything to anyone.
	sessionColors map[string]Accent
	// SessionRestored is the attached session's daemon-owned restored mark. The
	// daemon clears it on attach, so it is normally false here; it is carried
	// anyway so the attached row reads from the same field every other row does.
	// Distinct from RestoredFromState below, which is this client's own
	// bookkeeping about having applied a state snapshot.
	SessionRestored bool
	// WorkspaceNames maps a workspace number to its daemon-owned label. The
	// number stays the workspace's identity and is what an unnamed workspace
	// shows, so an absent entry is not a missing label but the normal case.
	WorkspaceNames map[int]string
	// WorkspaceOrder is the daemon-owned order the workspaces are shown in. It
	// arranges and never addresses: the number stays the identity, so nothing
	// keyed by one moves when this does. Empty is the plain ascending order.
	WorkspaceOrder    []int
	RestoredFromState bool // True after RestoreFromState, cleared after first resize
	// DaemonStateVersion is the daemon state version this client last saw. It is
	// echoed back on every state sync so the daemon can tell a snapshot built
	// from its current state apart from one built before a mutation of its own.
	DaemonStateVersion int
	SubscribedPTYs     map[string]bool // Tracks which PTY IDs are currently subscribed (for visibility optimization)
	// RestoredStreamSeq is the stream position each pane's snapshot was taken
	// at, from the restore that precedes the subscribe on the attach path.
	RestoredStreamSeq map[string]int64
	// ExitReason records why the program stopped, for the caller to report and
	// to pick an exit status. Empty means the user quit or detached normally.
	// It is written only on the Bubble Tea goroutine, in Update.
	ExitReason ExitReason
	// QuitRequested records that the user deliberately quit this client, which
	// in a daemon session also kills the session. The daemon then announces the
	// session ending and the connection dropping, and both announcements can
	// arrive before the program finishes quitting. Without this flag those
	// announcements are indistinguishable from a session killed from elsewhere,
	// and a deliberate quit reports an error. Written only on the Bubble Tea
	// goroutine, like ExitReason.
	QuitRequested bool

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
	// session's attached clients - a PTY has exactly one size. Two clients whose
	// config files (or live settings) disagreed here computed different
	// rectangles for the same panes and dragged the shared PTYs back and forth
	// between the two answers on every push.
	//
	// Seeded from this client's config, then settled across the session by state
	// sync (session.PaneGeometryState). Purely visual appearance - theme,
	// colours, border style, title position, dimming - deliberately stays
	// per-client and configurable; these two are synced only because they move
	// rectangles.
	SharedBorders bool
	PaneGap       int
	// lastConfigSharedBorders and lastConfigPaneGap are the config globals as
	// this OS last saw them, so adoptConfigPaneGeometry can tell a config-side
	// change (adopt it) from a session-side one (leave the config alone). See
	// adoptConfigPaneGeometry.
	lastConfigSharedBorders bool
	lastConfigPaneGap       int

	// SessionReserve is the chrome reserve every client attached to this
	// session lays its panes out around: the largest any of them asks for, as
	// settled by the daemon. The panes' box is the render size less this, which
	// is what makes the box - and so every pane's size - identical on every
	// client. Zero outside a daemon session, where this client's own chrome is
	// the only chrome there is.
	SessionReserve session.LayoutReserve

	// Multi-client effective size (min of all clients in session)
	EffectiveWidth  int // Effective width for rendering (min of all clients, 0 = use terminal size)
	EffectiveHeight int // Effective height for rendering (min of all clients, 0 = use terminal size)

	// syncedFP fingerprints the state last accepted by the daemon from this
	// client, and syncedFPSet says whether there is one. See SyncStateToDaemon.
	syncedFP    uint64
	syncedFPSet bool

	// applyingPeerSync is set while ApplyStateSync is folding a state that came
	// from somewhere else into this client. It is what makes a sync loop
	// impossible rather than unlikely: no push may leave this client while it is
	// set, so a layout this client worked out because it disagreed with the
	// arriving one cannot travel back out and provoke the same disagreement in
	// reverse. See SyncStateToDaemon and syncAnswerOwed.
	applyingPeerSync bool
	// syncAnswerOwed records that something inside the sync did have news for
	// the daemon - a window the daemon asked this client to place, and it
	// placed. That is an answer to a question, not an echo of a layout, so it is
	// sent once, after the sync has been applied and the guard is down.
	syncAnswerOwed bool
	// Keyboard enhancement support (Kitty protocol)
	KeyboardEnhancementsEnabled bool // True when terminal supports keyboard enhancements
	// KeyboardFlags is the flag set the host answered the enhancement query
	// with, so tuios knows what it actually got rather than what it asked for.
	// Zero means the terminal never answered, which is not the same as a refusal.
	KeyboardFlags int
	// hold is the momentary window-management mode (see hold_mode.go).
	hold holdMode
	// optionAdviceShown keeps the macOS Option advice to once per run.
	optionAdviceShown bool
	// currentPointer is the last OSC 22 pointer shape written, to avoid
	// redundant writes. Per model, since each served client has its own host.
	currentPointer PointerShape
	// Keybind registry for user-configurable keybindings
	KeybindRegistry *config.KeybindRegistry
	// ConfigWarnings holds the problems found in the loaded config, reported to
	// the user once the TUI is up (see reportConfigWarnings).
	ConfigWarnings []string
	// ConfigReadOnly stops the settings page writing the config file. Set by
	// entrypoints that serve someone else's session; see OSOptions.
	ConfigReadOnly bool
	// BrowserClient says the far end is a browser tab. See browser_client.go
	// for what that costs and what tuios says about it.
	BrowserClient bool
	// configReadOnlyTold keeps the "this will not be saved" notice to once per
	// session, since it would otherwise fire on every keypress in the settings
	// page.
	configReadOnlyTold bool
	// Showkeys overlay: a bottom-right, dock-aware keycast that shows the last
	// few keypresses as styled pills and expires them after a short timeout. It
	// renders purely from ShowKeys plus RecentKeys, gated on nothing else.
	ShowKeys          bool       // True when the showkeys overlay is enabled
	RecentKeys        []KeyEvent // Ring buffer of recently pressed keys
	KeyHistoryMaxSize int        // Maximum number of keys to display (default: 5)
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
	ShowTapeManager    bool              // True when showing tape manager overlay
	TapeManager        *TapeManagerState // Tape manager state
	TapeRecorder       *tape.Recorder    // Tape recorder for recording sessions
	TapeRecordingName  string            // Name of current recording
	TapePrefixActive   bool              // True when Ctrl+B, T was pressed (tape sub-prefix)
	LayoutPrefixActive bool              // True when Ctrl+B, L was pressed (layout sub-prefix)
	// Remote command processing
	ProcessingRemoteKeys bool // True when processing remote send-keys (disables animations)
	// Remote tape script progress (used instead of ScriptPlayer for tape exec)
	RemoteScriptIndex int // Current command index (0-based)
	RemoteScriptTotal int // Total commands in remote script
	// Kitty Graphics Protocol passthrough for forwarding to host terminal
	KittyPassthrough *KittyPassthrough
	// Sixel Graphics passthrough for forwarding to host terminal
	SixelPassthrough *SixelPassthrough
	TextSizingState  *TextSizingState
	PostRenderWriter *PostRenderWriter
	// Hooks manager for shell-command hooks
	HookManager *hooks.Manager
	// PendingClipboardSet receives clipboard content from guest apps via OSC 52.
	// The bubbletea Update loop reads this and calls tea.SetClipboard().
	PendingClipboardSet chan string
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
	// PendingCwdChange receives OSC 7 working-directory changes from windows'
	// PTY goroutines. The bubbletea Update loop drains it and, for the focused
	// window only, checks whether the new directory carries a .tuios.tape. This
	// is the detection half of the project-tape feature; it never executes
	// anything, it only stats, reads to hash, and surfaces a passive indicator.
	PendingCwdChange chan CwdChangedMsg
	// tapeDetect holds the project-tape detection state (trust store, session
	// memory of handled directories, debounce bookkeeping, and the current
	// passive indicator). See tape_detect.go.
	tapeDetect tapeDetectState
	// ShowTapeReview is true when the project-tape review/trust dialog is open.
	// TapeReview holds its state (path, trust status, reviewed content, header).
	// See tape_review.go.
	ShowTapeReview bool
	TapeReview     *TapeReviewState
	// TerminalModeEnteredAt tracks when we last switched to TerminalMode.
	// Used to suppress misparsed mouse-sequence fragments (phantom keypresses)
	// during the AllMotion→CellMotion transition window.
	TerminalModeEnteredAt time.Time
	// Scrollback browser overlay
	ShowScrollbackBrowser bool
	ScrollbackBrowser     any // *scrollback.Browser  - typed as any to avoid import cycle
	// Command palette overlay
	ShowCommandPalette     bool
	CommandPaletteQuery    string
	CommandPaletteSelected int
	CommandPaletteScroll   int
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
	// Launcher overlay: the programs a session can start, which is a separate
	// list from the palette's commands (see launcher.go for why).
	ShowLauncher     bool
	LauncherQuery    string
	LauncherSelected int
	LauncherScroll   int
	// LauncherItems holds one row per known program, rebuilt when a scan lands
	// rather than per frame: with a few thousand programs on $PATH, rebuilding
	// per frame is the difference between an overlay that costs nothing to
	// leave open and one that does not.
	LauncherItems []LauncherItem
	// launcherIcons holds the decoded app icons and what is currently drawn on
	// the host. Nil until the launcher first needs one.
	launcherIcons *launcherIcons
	// launcherIconCells is where the last frame put each row's icon, in
	// panel-relative cells, for the flush that follows the frame.
	launcherIconCells []launcherIconPlacement
	// pathApps caches the $PATH scan across opens, refreshing only the
	// directories whose mtime moved.
	pathApps *applist.Cache
	// desktopApps caches the .desktop scan across opens, reparsing only the
	// files whose mtime moved. Nil on a platform that has no such thing.
	desktopApps *desktopCache
	// launcherSource is the two caches merged into the one list the launcher
	// ranks, refreshed when a scan lands.
	launcherSource []applist.Entry
	// launchHistory ranks programs by how recently and often they were run.
	launchHistory *applist.Frecency
	// pendingSeeds holds command lines waiting for the daemon-created panes
	// they are to be typed into.
	pendingSeeds []pendingSeed
	// Session switcher overlay
	ShowSessionSwitcher          bool
	SessionSwitcherQuery         string
	SessionSwitcherSelected      int
	SessionSwitcherScroll        int
	SessionSwitcherItems         []sessiontree.Node
	SessionSwitcherError         string
	SessionSwitcherConfirmDelete string // non-empty = confirming deletion of this session name
	// Workspace switcher overlay, scoped to the attached session
	ShowWorkspaceSwitcher     bool
	WorkspaceSwitcherQuery    string
	WorkspaceSwitcherSelected int
	WorkspaceSwitcherScroll   int
	WorkspaceSwitcherItems    []WorkspaceItem
	// Aggregate view overlay (all windows across workspaces)
	ShowAggregateView     bool
	AggregateViewQuery    string
	AggregateViewSelected int
	AggregateViewScroll   int
	// Layout picker overlay
	ShowLayoutPicker bool
	LayoutCycleIndex int             // Current index in saved layouts for cycling
	MultifocusSet    map[string]bool // Window IDs that receive keystrokes simultaneously
	UseBSPLayout     bool            // true = BSP tiling, false = master-stack
	// announceDepth counts the open settleSizes holds. See announce_batch.go.
	announceDepth int
	// Scrolling tiling (niri-like) layout
	UseScrollingLayout        bool                            // true = scrolling columns mode
	WorkspaceScrollingLayouts map[int]*layout.ScrollingLayout // per-workspace scrolling layouts
	scrollingFocusSyncing     bool                            // guard to prevent recursive sync
	LayoutPickerItems         []LayoutTemplate
	LayoutPickerSelected      int
	LayoutPickerScroll        int
	LayoutPickerQuery         string
	LayoutPickerMode          string // "load" or "save"
	LayoutSaveBuffer          string // Buffer for layout name when saving

	// Settings overlay state.
	ShowSettings       bool
	SettingsCategory   int    // active settings category (tab) index
	SettingsSelected   int    // selected row within the active category
	SettingsScroll     int    // scroll offset within the active category
	SettingsEditing    bool   // true while a text setting is being edited inline
	SettingsEditBuffer string // in-progress text for the setting being edited

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
	ThemePickerQuery    string
	ThemePickerSelected int
	ThemePickerScroll   int
	ThemePickerOriginal string // theme active when the picker opened, for cancel

	// Glyph picker overlay state, the theme picker's opposite number.
	ShowGlyphPicker     bool
	GlyphPickerQuery    string
	GlyphPickerSelected int
	GlyphPickerScroll   int
	GlyphPickerOriginal string // set active when the picker opened, for cancel
	// GlyphPickerSamples is one preview per set, built when the picker opens.
	// Each borrows the active selection to read what its set draws, which is
	// not something to do per frame while composing one.
	GlyphPickerSamples map[string]glyphSample

	// Dock layout editor state. The dock is three ordered lists, which is the
	// one thing on the settings page no row can express.
	ShowDockEditor     bool
	DockEditorSelected int
	DockEditorScroll   int
	DockEditorOriginal dockLists // the lists when it opened, for cancel

	// Floating overlay placement + mouse hit-testing. Each overlay kind keeps
	// its own drag displacement in OverlayOffsets so panels (e.g. settings and
	// the theme picker) can be moved independently. OverlayHits records every
	// panel rendered in the current frame, back to front, so the mouse handlers
	// can route clicks to the topmost panel under the cursor.
	OverlayOffsets map[string][2]int
	OverlayHits    []overlayPanelHit
	OverlayDrag    overlayDragState
	// OverlayZOrder is the stacking order of the currently-open draggable
	// overlays, bottom to top. Clicking a panel moves it to the end (top).
	OverlayZOrder []string

	// Sidebar mouse hit-testing and view state. SidebarHits records the on-screen
	// rectangle of every sidebar row rendered in the current frame, so the mouse
	// handlers can route clicks, wheels, and right-clicks without re-deriving the
	// layout. Each of the rail's three sections holds its own scroll offset, so
	// the wheel scrolls the one under the pointer and no header can be scrolled
	// away; sidebarSectionY is where each section was drawn, which is how a
	// wheel event finds its section. Scrolls are clamped by the next render.
	SidebarHits    []sidebarRowHit
	SidebarScrollS int
	SidebarScrollT int
	SidebarScrollA int
	// sidebarAgentAnchor keeps the agents section's viewport on the row it was
	// left on rather than on the index that row happened to have, since that
	// section resorts itself on live agent state. See sidebar_anchor.go.
	sidebarAgentAnchor sidebarScrollAnchor
	sidebarSectionY    [sidebarSectionCount][2]int
	// sidebarStripRows is what the collapsed strip drew on each of its lines,
	// recorded by the renderer as it draws. The hover tooltip reads it to name
	// what is under the pointer, including the badge, which is a readout rather
	// than a control and so has no hit rectangle of its own.
	sidebarStripRows []sidebarStripRow
	// Tooltip is the hover label state shared by the collapsed rail and the
	// dock's session controls: which control the pointer is on, when it landed,
	// and whether the label has been drawn. Gesture-scoped runtime state; the
	// Shown latch is also the tick gate.
	Tooltip tooltipState
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
	// SidebarCollapsed is the rail folded down to its glyph strip. It is one of
	// the two states the user can put the rail in (the other is the stored
	// width, which a drag on the edge still sets freely); the responsive
	// breakpoints fold over it exactly as they fold over the stored width.
	// Persisted in the sidebar state file.
	SidebarCollapsed bool
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
	// sidebarStateSocket is the daemon socket the persisted window-keyed maps
	// were written against, and the guard on pruning them: window IDs mean
	// nothing outside the daemon that issued them.
	sidebarStateSocket string
	// pendingAgentAlerts holds agent alerts waiting out their settle window, by
	// window ID. A non-empty map is the only thing that keeps the maintenance
	// tick awake for them, so an idle session with nothing parked pays nothing.
	pendingAgentAlerts map[string]pendingAgentAlert
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
	// accentDragCol pins a slider drag to the channel it started on. The kind
	// alone is not enough for a control that has five of itself stacked in a
	// column: without this, sliding down from R onto G would start driving G.
	accentDragCol int
	// Sidebar hover: the last mouse position seen inside the band, so the row
	// under the cursor is highlighted the way overlay rows are. HoverActive is
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
	// sidebarReturn* record where the keyboard came from when the rail took it,
	// so leaving the rail can hand that back. sidebarLastRow is the same bargain
	// pointing the other way: the row the rail was left on, so re-entering starts
	// where it stopped rather than back at the attached session. See
	// sidebar_return.go.
	sidebarReturnArmed  bool
	sidebarReturnMode   Mode
	sidebarReturnWindow string
	sidebarLastRow      sidebarNavRow
	sidebarLastRowSet   bool
	// sidebarFollowSession, when set, tells the next nav build to place the
	// cursor on that session's row after it rebuilds. It is how a reorder or a
	// switch keeps the cursor on the session it moved once the tree relaid out,
	// without the handler guessing the post-relayout index.
	sidebarFollowSession string
	// sidebarCache holds the last styled rail keyed by a cheap signature of every
	// input that changes the rows, so a frame drawn for an unrelated reason (a
	// pane printing output) does not rebuild and restyle the whole rail.
	sidebarCache sidebarRenderCache
	// sidebarTitles debounces window titles for the rail so bursty title churn
	// does not thrash the rows; sidebarTitlePending is set while an adopted title
	// is still catching up, keeping the tick alive until it settles.
	sidebarTitles       map[string]railTitleEntry
	sidebarTitlePending bool
	// sidebarTitleGen is the daemon listing generation the rail titles were last
	// brought up to date with. A window this client dropped its subscription to
	// only ever retitles through that listing, so the generation is the tick's
	// only signal that such a title moved.
	sidebarTitleGen uint64

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
	// CtrlDragWasTerminal remembers that the grab started in terminal mode, so
	// dropping the window puts the user back where they were instead of leaving
	// them in window management. Moving a pane is not a request to stop typing.
	CtrlDragWasTerminal bool
	// pointerGestureWasTerminal is the same bargain for a resize or an alt-drag
	// move: see BeginPointerGesture.
	pointerGestureWasTerminal bool

	// ContextMenu is the open shift+right-click menu, or nil. It is deliberately
	// not one of the draggable overlay kinds: a context menu is anchored to the
	// cell it was opened on and is dismissed by the next click, so it has no use
	// for a drag offset or a place in the click-to-raise order.
	ContextMenu *ContextMenu

	// menuWorkspace carries the workspace a pill menu was opened on across the
	// gap between the menu closing and its row's action being dispatched. See
	// TakeMenuWorkspace.
	menuWorkspace int
	// menuSession carries the session a rail row's menu was opened on, the same
	// way menuWorkspace carries a pill's. See TakeMenuSession.
	menuSession string

	// UserConfig is the loaded user configuration. The settings page mutates
	// it in place and persists it so live changes survive a restart. May be
	// nil if the config failed to load at startup.
	UserConfig *config.UserConfig

	// startupApplied guards the one-shot startup preferences (open a default
	// window, start tiled) so they run only on the first WindowSizeMsg, once
	// the real terminal dimensions are known, and never again.
	startupApplied bool

	// pendingStartTerminalMode records that the start_in_terminal_mode startup
	// preference still needs to be applied but had no window to focus yet. In a
	// daemon session the default window is created asynchronously, so entry into
	// terminal mode is deferred until that window materializes through a state
	// sync and can be focused.
	pendingStartTerminalMode bool
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
}

// NotifTarget names the pane a message came from. The workspace is deliberately
// absent: it is resolved from live state at jump time, because a stored index
// goes stale the moment the window is moved.
type NotifTarget struct {
	SessionID string
	WindowID  string
}

// LogMessage represents a log entry with timestamp and level.
type LogMessage struct {
	Time    time.Time
	Level   string // INFO, WARN, ERROR
	Message string
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

// verboseLog controls whether INFO-level logs are formatted and recorded.
// It is off by default so hot paths (retile traces) pay nothing in production,
// and is enabled by setting TUIOS_DEBUG_INTERNAL=1, the same switch that gates
// the internal kitty/sixel passthrough trace logs. WARN and ERROR are always
// recorded regardless of this flag.
var verboseLog = os.Getenv("TUIOS_DEBUG_INTERNAL") == "1"

// SwitchToSession detaches from the current daemon session and attaches to another.
// The connection to the daemon stays open  - only the session binding changes.
//
// The round trip goes first and the windows come down only once it has landed.
// Tearing down first meant a switch the daemon refused answered "show me that
// session" by leaving the user with no session at all, and nothing here could
// put it back: closing a window nils its terminal and its PTY behind a latch
// that only goes one way.
func (m *OS) SwitchToSession(targetSession string) error {
	if m.DaemonClient == nil {
		return fmt.Errorf("not in daemon mode")
	}
	if targetSession == "" {
		return fmt.Errorf("session name cannot be empty")
	}

	m.LogInfo("[SWITCH] Starting: %s → %s", m.SessionName, targetSession)

	savedWidth, savedHeight := m.Width, m.Height
	state, err := m.DaemonClient.SwitchSession(targetSession, savedWidth, savedHeight)
	if err != nil {
		m.LogError("[SWITCH] %v", err)
		var rollback *session.SwitchRollback
		if errors.As(err, &rollback) {
			if rollback.State != nil {
				// The daemon took the detach and refused the attach, and the
				// client is back where it started. Detaching dropped every
				// subscription, so the panes still on screen have stopped
				// streaming and have to be rebuilt from the state the return
				// attach handed back.
				m.SessionName = m.DaemonClient.SessionName()
				m.rebuildForSession(rollback.State, savedWidth, savedHeight)
				m.MarkAllDirty()
			}
			// Already says which session the client ended up on.
			return err
		}
		// The detach never landed, so nothing was given up: this client is still
		// on its own session with every pane streaming.
		return fmt.Errorf("switch to %q failed: %w; still on %q", targetSession, err, m.SessionName)
	}
	m.SessionName = m.DaemonClient.SessionName()
	m.rebuildForSession(state, savedWidth, savedHeight)
	// The components tell their commands which session they are drawing for,
	// and that answer just changed.
	m.SyncDockContext()

	m.MarkAllDirty()
	m.LogInfo("Session switch complete: now on %s with %d windows", m.SessionName, len(m.Windows))
	m.ShowNotification("Session: "+m.SessionName, "success", config.NotificationDuration)
	// Switching sessions is an attach: this client is now driving a different
	// session, and a hook that tracks which session is live has to hear about it
	// here as well as at startup.
	m.FireAttached()
	return nil
}

// rebuildForSession replaces everything this client holds for one session with
// everything it holds for another: the windows go, the per-session collections
// are reset, and the panes named by state are built and subscribed.
//
// It runs only once the daemon has confirmed which session this connection is
// on, because every step of it is irreversible.
func (m *OS) rebuildForSession(state *session.SessionState, savedWidth, savedHeight int) {
	// The daemon already dropped these subscriptions when it took the detach.
	// Unsubscribing again is what clears the client's own output, resize and
	// exit handlers, so a pane from the session just left cannot fire into the
	// session just joined.
	for _, w := range m.Windows {
		if w.DaemonMode && w.PTYID != "" {
			m.DaemonClient.UnsubscribePTY(w.PTYID)
		}
		w.Close()
	}

	m.Windows = nil
	m.FocusedWindow = -1
	m.WorkspaceTrees = make(map[int]*layout.BSPTree)
	m.WorkspaceScrollingLayouts = make(map[int]*layout.ScrollingLayout)
	m.WindowToBSPID = make(map[string]int)
	m.BSPIDToWindowID = make(map[int]string)
	m.NextBSPWindowID = 1
	m.Animations = nil
	m.MultifocusSet = nil
	// Default to workspace 1, not 0: a brand-new target session has no windows,
	// so RestoreFromState (which repairs the workspace) never runs, and any
	// window then created would land on workspace 0, which SwitchToWorkspace
	// refuses to navigate to, leaving it permanently invisible.
	m.CurrentWorkspace = 1
	m.SubscribedPTYs = make(map[string]bool)

	if state == nil || len(state.Windows) == 0 {
		return
	}

	if err := m.RestoreFromState(state); err != nil {
		m.LogError("Failed to restore state: %v", err)
	}
	// Restore current workspace from state
	if state.CurrentWorkspace > 0 {
		m.CurrentWorkspace = state.CurrentWorkspace
	}
	// Restore real screen dimensions (RestoreFromState may overwrite with saved values)
	m.Width = savedWidth
	m.Height = savedHeight
	m.EffectiveWidth = savedWidth
	m.EffectiveHeight = savedHeight

	if err := m.RestoreTerminalStates(); err != nil {
		m.LogError("Failed to restore terminal states: %v", err)
	}
	if err := m.SetupPTYOutputHandlers(); err != nil {
		m.LogError("Failed to setup PTY handlers: %v", err)
	}
	// Re-tile to set correct window dimensions for current screen
	if m.AutoTiling {
		m.TileAllWindows()
	}
	// Sync PTY dimensions to match the tiled layout
	m.SyncDaemonPTYDimensions()
	// Trigger redraws for alt-screen apps
	m.TriggerAltScreenRedraws()
}

// Cleanup performs cleanup operations when the application exits.
// Cleanup releases per-session resources. It closes the daemon client, which
// stops the client read loop and drops the daemon-side connection, so an SSH or
// web session ending does not leak a goroutine, a socket, and a daemon connState.
// TUIClient.Close is idempotent, so calling Cleanup more than once is safe.
// State should be synced to the daemon before Cleanup, on the UI goroutine.
func (m *OS) Cleanup() {
	m.stopWindowExitDrain()
	// The dock's components are subprocesses this client started, and a push
	// component is a process that never exits on its own. An ephemeral SSH or
	// web session is a goroutine inside a long-lived server, so without this
	// every disconnect would leave one running command per push component and
	// the goroutines reading them.
	m.StopDockComponents()
	if m.DaemonClient != nil {
		_ = m.DaemonClient.Close()
		return
	}

	// Ephemeral mode: the windows own local PTYs (child shells). When the whole
	// process is exiting these would be reaped anyway, but an ephemeral SSH
	// session is a goroutine inside a long-lived server: without closing them
	// here every disconnect would leak a shell process, its PTY, and the
	// window's I/O goroutines. Window.Close is idempotent, so this is safe even
	// when the local binary also calls Cleanup after the program exits.
	for _, w := range m.Windows {
		w.Close()
	}
}
