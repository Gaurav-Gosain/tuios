// Package app provides the core TUIOS application logic and window management.
package app

import (
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
	"github.com/charmbracelet/ssh"
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
	ScrollbarDragging        bool
	ScrollbarDragWindowIndex int // -1 when not dragging
	Windows                  []*terminal.Window
	FocusedWindow            int
	Width                    int
	Height                   int
	X                        int
	Y                        int
	Mode                     Mode
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
	terminalMu         sync.RWMutex
	LastMouseX         int
	LastMouseY         int
	HasActiveTerminals bool
	idleFrames         int // Consecutive frames with no content changes (for adaptive tick)
	ShowHelp           bool
	InteractionMode    bool                       // True when actively dragging/resizing
	MouseSnapping      bool                       // Enable/disable mouse snapping
	WindowExitChan     chan string                // Channel to signal window closure
	PTYDataChan        chan struct{}              // Signaled by PTY readers when new output arrives (buffered 1, coalescing)
	StateSyncChan      chan *session.SessionState // Channel for thread-safe state sync from callbacks
	ClientEventChan    chan ClientEvent           // Channel for thread-safe client join/leave notifications
	Animations         []*ui.Animation            // Active animations
	CPUHistory         []float64                  // CPU usage history for graph
	LastCPUUpdate      time.Time                  // Last time CPU was updated
	RAMUsage           float64                    // Cached RAM usage percentage
	LastRAMUpdate      time.Time                  // Last time RAM was updated
	AutoTiling         bool                       // Automatic tiling mode enabled
	MasterRatio        float64                    // Master window width ratio for tiling (0.3-0.7)
	// BSP tiling state
	WorkspaceTrees        map[int]*layout.BSPTree // BSP tree per workspace
	PreselectionDir       layout.PreselectionDir  // Pending preselection direction (0 = none)
	TilingScheme          layout.AutoScheme       // Default auto-insertion scheme
	SplitTargetWindowID   string                  // Window ID to split (set before AddWindow for splits)
	WindowToBSPID         map[string]int          // Maps window UUID to stable BSP integer ID
	BSPIDToWindowID       map[int]string          // Reverse of WindowToBSPID: BSP integer ID to window UUID (speed-up for getWindowByIntID)
	NextBSPWindowID       int                     // Next BSP window ID to assign (starts at 1)
	RenamingWindow        bool                    // True when renaming a window
	RenameBuffer          string                  // Buffer for new window name
	PrefixActive          bool                    // True when prefix key was pressed (tmux-style)
	WorkspacePrefixActive bool                    // True when Ctrl+B, w was pressed (workspace sub-prefix)
	MinimizePrefixActive  bool                    // True when Ctrl+B, m was pressed (minimize sub-prefix)
	TilingPrefixActive    bool                    // True when Ctrl+B, t was pressed (tiling/window sub-prefix)
	DebugPrefixActive     bool                    // True when Ctrl+B, D was pressed (debug sub-prefix)
	LastPrefixTime        time.Time               // Time when prefix was activated
	HelpScrollOffset      int                     // Scroll offset for help menu
	HelpCategory          int                     // Current help category index (for left/right navigation)
	HelpSearchMode        bool                    // True when help search is active
	HelpSearchQuery       string                  // Current search query in help menu
	CurrentWorkspace      int                     // Current active workspace (1-9)
	NumWorkspaces         int                     // Total number of workspaces
	WorkspaceFocus        map[int]int             // Remembers focused window per workspace
	WorkspaceLayouts      map[int][]WindowLayout  // Stores custom layouts per workspace
	WorkspaceHasCustom    map[int]bool            // Tracks if workspace has custom layout
	WorkspaceMasterRatio  map[int]float64         // Stores master ratio per workspace
	ShowLogs              bool                    // True when showing log overlay
	LogMessages           []LogMessage            // Store log messages
	LogScrollOffset       int                     // Scroll offset for log viewer
	Notifications         []Notification          // Active notifications
	SelectionMode         bool                    // True when in text selection mode
	ClipboardContent      string                  // Store clipboard content from tea.ClipboardMsg
	ShowCacheStats        bool                    // True when showing style cache statistics overlay
	ShowQuitConfirm       bool                    // True when showing quit confirmation dialog
	QuitConfirmSelection  int                     // 0 = Yes (left), 1 = No (right)
	// Pending resize tracking for debouncing PTY resize during mouse drag
	PendingResizes map[string][2]int // windowID -> [width, height] of pending PTY resize
	// Performance optimization caches
	cachedSeparator      string // Cached dock separator string
	cachedSeparatorWidth int    // Width of cached separator
	workspaceActiveStyle *lipgloss.Style
	cachedViewContent    string // Cached full View() output to skip rendering on idle ticks
	renderSkipped        bool   // True when frame-skip fired; View() returns cached content
	// lastInteractionRender is when a drag/resize motion event last produced a
	// frame. Motion events arrive faster than a frame can be composed, so this
	// bounds how often they are allowed to redraw.
	lastInteractionRender time.Time
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
	IsDaemonSession   bool               // True when running as part of a persistent daemon session
	DaemonClient      *session.TUIClient // Client for daemon communication (nil in local mode)
	SessionName       string             // Name of the daemon session (if attached)
	RestoredFromState bool               // True after RestoreFromState, cleared after first resize
	// DaemonStateVersion is the daemon state version this client last saw. It is
	// echoed back on every state sync so the daemon can tell a snapshot built
	// from its current state apart from one built before a mutation of its own.
	DaemonStateVersion int
	SubscribedPTYs     map[string]bool // Tracks which PTY IDs are currently subscribed (for visibility optimization)
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
	// Multi-client effective size (min of all clients in session)
	EffectiveWidth  int // Effective width for rendering (min of all clients, 0 = use terminal size)
	EffectiveHeight int // Effective height for rendering (min of all clients, 0 = use terminal size)
	// Keyboard enhancement support (Kitty protocol)
	KeyboardEnhancementsEnabled bool // True when terminal supports keyboard enhancements
	// Keybind registry for user-configurable keybindings
	KeybindRegistry *config.KeybindRegistry
	// ConfigWarnings holds the problems found in the loaded config, reported to
	// the user once the TUI is up (see reportConfigWarnings).
	ConfigWarnings []string
	// Showkeys feature
	ShowKeys          bool       // True when showkeys overlay is enabled
	RecentKeys        []KeyEvent // Ring buffer of recently pressed keys
	KeyHistoryMaxSize int        // Maximum number of keys to display (default: 5)
	// Tape scripting support
	ScriptPlayer       any       // *tape.Player - script playback engine
	ScriptMode         bool      // True when running a tape script
	ScriptPaused       bool      // True when script playback is paused
	ScriptExecutor     any       // *tape.CommandExecutor - executes tape commands
	ScriptSleepUntil   time.Time // When to resume after a sleep command
	ScriptFinishedTime time.Time // When the script finished (for auto-hide)
	// WaitUntilRegex playback state. When ScriptWaitRegex is non-nil, playback
	// blocks until the focused window's screen matches it or ScriptWaitDeadline
	// passes, whichever comes first.
	ScriptWaitRegex    *regexp.Regexp
	ScriptWaitDeadline time.Time
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
	// Session switcher overlay
	ShowSessionSwitcher          bool
	SessionSwitcherQuery         string
	SessionSwitcherSelected      int
	SessionSwitcherScroll        int
	SessionSwitcherItems         []SessionSwitcherItem
	SessionSwitcherError         string
	SessionSwitcherConfirmDelete string // non-empty = confirming deletion of this session name
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
	ShowSettings     bool
	SettingsCategory int // active settings category (tab) index
	SettingsSelected int // selected row within the active category
	SettingsScroll   int // scroll offset within the active category

	// Theme picker overlay state.
	ShowThemePicker     bool
	ThemePickerQuery    string
	ThemePickerSelected int
	ThemePickerScroll   int
	ThemePickerOriginal string // theme active when the picker opened, for cancel

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

	// UserConfig is the loaded user configuration. The settings page mutates
	// it in place and persists it so live changes survive a restart. May be
	// nil if the config failed to load at startup.
	UserConfig *config.UserConfig
}

// Notification represents a temporary notification message.
type Notification struct {
	ID        string
	Message   string
	Type      string // "info", "success", "warning", "error"
	StartTime time.Time
	Duration  time.Duration
	Animation *ui.Animation
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
func (m *OS) SwitchToSession(targetSession string) error {
	if m.DaemonClient == nil {
		return fmt.Errorf("not in daemon mode")
	}
	if targetSession == "" {
		return fmt.Errorf("session name cannot be empty")
	}

	m.LogInfo("[SWITCH] Starting: %s → %s", m.SessionName, targetSession)

	// 1. Unsubscribe from all current PTYs and close windows
	for _, w := range m.Windows {
		if w.DaemonMode && w.PTYID != "" {
			m.DaemonClient.UnsubscribePTY(w.PTYID)
		}
		w.Close()
	}

	// 2. Clear all current state but preserve screen dimensions
	savedWidth, savedHeight := m.Width, m.Height
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

	// 3. Detach + attach in one operation (safe with read loop running)
	state, err := m.DaemonClient.SwitchSession(targetSession, savedWidth, savedHeight)
	if err != nil {
		return fmt.Errorf("switch failed: %w", err)
	}
	m.SessionName = m.DaemonClient.SessionName()

	// 4. Restore windows from new session state
	if state != nil && len(state.Windows) > 0 {
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

	m.MarkAllDirty()
	m.LogInfo("Session switch complete: now on %s with %d windows", m.SessionName, len(m.Windows))
	m.ShowNotification("Session: "+m.SessionName, "success", config.NotificationDuration)
	// Switching sessions is an attach: this client is now driving a different
	// session, and a hook that tracks which session is live has to hear about it
	// here as well as at startup.
	m.FireAttached()
	return nil
}

// Cleanup performs cleanup operations when the application exits.
// Cleanup releases per-session resources. It closes the daemon client, which
// stops the client read loop and drops the daemon-side connection, so an SSH or
// web session ending does not leak a goroutine, a socket, and a daemon connState.
// TUIClient.Close is idempotent, so calling Cleanup more than once is safe.
// State should be synced to the daemon before Cleanup, on the UI goroutine.
func (m *OS) Cleanup() {
	if m.DaemonClient != nil {
		_ = m.DaemonClient.Close()
	}
}
