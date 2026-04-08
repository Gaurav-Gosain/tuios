// Package app provides the core TUIOS application logic and window management.
package app

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
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
	Dragging           bool
	Resizing           bool
	ResizeCorner       ResizeCorner
	PreResizeState     terminal.Window
	ResizeStartX       int
	ResizeStartY       int
	DragOffsetX        int
	DragOffsetY        int
	DragStartX         int // Track where drag started
	DragStartY         int // Track where drag started
	TiledX             int // Original tiled position X
	TiledY             int // Original tiled position Y
	TiledWidth         int // Original tiled width
	TiledHeight        int // Original tiled height
	DraggedWindowIndex       int // Index of window being dragged
	AutoScrollDir            int // -1 = up, 0 = none, 1 = down (for drag auto-scroll)
	AutoScrollActive         bool
	ScrollbarDragging        bool
	ScrollbarDragWindowIndex int // -1 when not dragging
	Windows            []*terminal.Window
	FocusedWindow      int
	Width              int
	Height             int
	X                  int
	Y                  int
	Mode               Mode
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
	// SSH mode fields
	SSHSession ssh.Session // SSH session reference (nil in local mode)
	IsSSHMode  bool        // True when running over SSH
	// Daemon mode fields
	IsDaemonSession   bool               // True when running as part of a persistent daemon session
	DaemonClient      *session.TUIClient // Client for daemon communication (nil in local mode)
	SessionName       string             // Name of the daemon session (if attached)
	RestoredFromState bool               // True after RestoreFromState, cleared after first resize
	SubscribedPTYs    map[string]bool    // Tracks which PTY IDs are currently subscribed (for visibility optimization)
	// Multi-client effective size (min of all clients in session)
	EffectiveWidth  int // Effective width for rendering (min of all clients, 0 = use terminal size)
	EffectiveHeight int // Effective height for rendering (min of all clients, 0 = use terminal size)
	// Keyboard enhancement support (Kitty protocol)
	KeyboardEnhancementsEnabled bool // True when terminal supports keyboard enhancements
	// Keybind registry for user-configurable keybindings
	KeybindRegistry *config.KeybindRegistry
	// Showkeys feature
	ShowKeys          bool       // True when showkeys overlay is enabled
	RecentKeys        []KeyEvent // Ring buffer of recently pressed keys
	KeyHistoryMaxSize int        // Maximum number of keys to display (default: 5)
	// Tape scripting support
	ScriptPlayer       any       // *tape.Player - script playback engine
	ScriptMode         bool      // True when running a tape script
	ScriptPaused       bool      // True when script playback is paused
	ScriptConverter    any       // *tape.ScriptMessageConverter - converts tape commands to tea.Msg
	ScriptExecutor     any       // *tape.CommandExecutor - executes tape commands
	ScriptSleepUntil   time.Time // When to resume after a sleep command
	ScriptFinishedTime time.Time // When the script finished (for auto-hide)
	// Tape manager UI
	ShowTapeManager   bool              // True when showing tape manager overlay
	TapeManager       *TapeManagerState // Tape manager state
	TapeRecorder      *tape.Recorder    // Tape recorder for recording sessions
	TapeRecordingName string            // Name of current recording
	TapePrefixActive   bool             // True when Ctrl+B, T was pressed (tape sub-prefix)
	LayoutPrefixActive bool             // True when Ctrl+B, L was pressed (layout sub-prefix)
	// Remote command processing
	ProcessingRemoteKeys bool // True when processing remote send-keys (disables animations)
	// Remote tape script progress (used instead of ScriptPlayer for tape exec)
	RemoteScriptIndex int // Current command index (0-based)
	RemoteScriptTotal int // Total commands in remote script
	// Kitty Graphics Protocol renderer for image support
	KittyRenderer *KittyRenderer
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
	ShowSessionSwitcher     bool
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
	ShowLayoutPicker     bool
	LayoutCycleIndex     int              // Current index in saved layouts for cycling
	MultifocusSet        map[int]bool     // Windows that receive keystrokes simultaneously
	UseBSPLayout         bool             // true = BSP tiling, false = master-stack
	// Scrolling tiling (niri-like) layout
	UseScrollingLayout        bool                            // true = scrolling columns mode
	WorkspaceScrollingLayouts map[int]*layout.ScrollingLayout // per-workspace scrolling layouts
	scrollingFocusSyncing     bool                            // guard to prevent recursive sync
	LayoutPickerItems    []LayoutTemplate
	LayoutPickerSelected int
	LayoutPickerScroll   int
	LayoutPickerQuery    string
	LayoutPickerMode     string // "load" or "save"
	LayoutSaveBuffer     string // Buffer for layout name when saving
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

// Log adds a new log message to the log buffer.
func (m *OS) Log(level, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	logMsg := LogMessage{
		Time:    time.Now(),
		Level:   level,
		Message: message,
	}

	// Check if we're at the bottom before adding new log
	wasAtBottom := false
	if m.ShowLogs {
		maxDisplayHeight := max(m.Height-8, 8)
		totalLogs := len(m.LogMessages)

		// Fixed overhead: title (1) + blank after title (1) + blank before hint (1) + hint (1) = 4
		fixedLines := 4
		// If scrollable, add scroll indicator: blank (1) + indicator (1) = 2
		if totalLogs > maxDisplayHeight-fixedLines {
			fixedLines = 6
		}
		logsPerPage := max(maxDisplayHeight-fixedLines, 1)

		maxScroll := max(totalLogs-logsPerPage, 0)
		// Consider "at bottom" if within 2 lines of the end (to handle edge cases)
		wasAtBottom = m.LogScrollOffset >= maxScroll-2
	}

	// Keep only last MaxLogMessages messages
	m.LogMessages = append(m.LogMessages, logMsg)
	if len(m.LogMessages) > config.MaxLogMessages {
		m.LogMessages = m.LogMessages[len(m.LogMessages)-config.MaxLogMessages:]
	}

	// Auto-scroll to bottom if we were already at bottom (sticky scroll)
	if wasAtBottom && m.ShowLogs {
		// Recalculate maxScroll with the new log added
		maxDisplayHeight := max(m.Height-8, 8)
		totalLogs := len(m.LogMessages)
		fixedLines := 4
		if totalLogs > maxDisplayHeight-fixedLines {
			fixedLines = 6
		}
		logsPerPage := max(maxDisplayHeight-fixedLines, 1)
		maxScroll := max(totalLogs-logsPerPage, 0)
		m.LogScrollOffset = maxScroll
	}
}

// LogInfo logs an informational message.
func (m *OS) LogInfo(format string, args ...any) {
	m.Log("INFO", format, args...)
}

// RebuildBSPTreeFromPositions rebuilds the BSP tree from current window positions.
// Used after layout loading to sync the tree with the loaded positions without retiling.
func (m *OS) RebuildBSPTreeFromPositions() {
	// Clear existing tree and rebuild from scratch with current window order
	delete(m.WorkspaceTrees, m.CurrentWorkspace)
	tree := m.GetOrCreateBSPTree()
	if tree == nil {
		return
	}

	var visibleWindows []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
			visibleWindows = append(visibleWindows, w)
		}
	}

	// Re-add all windows to BSP tree
	for _, w := range visibleWindows {
		intID := m.getWindowIntID(w.ID)
		existingIDs := tree.GetAllWindowIDs()
		if len(existingIDs) == 0 {
			tree.InsertWindow(intID, 0, layout.SplitNone, 0.5, m.GetBSPBounds())
		} else {
			tree.InsertWindow(intID, existingIDs[len(existingIDs)-1], layout.SplitNone, 0.5, m.GetBSPBounds())
		}
	}

	// Sync ratios from actual positions
	windowRects := make(map[int]layout.Rect)
	for _, w := range visibleWindows {
		intID := m.getWindowIntID(w.ID)
		windowRects[intID] = layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height}
	}
	tree.SyncRatiosFromGeometry(windowRects, m.GetBSPBounds())
}

// ToggleLayoutMode cycles through layout modes: BSP -> master-stack -> scrolling -> BSP.
func (m *OS) ToggleLayoutMode() {
	m.resetTiledFlags()
	if m.UseScrollingLayout {
		// scrolling -> BSP
		m.UseScrollingLayout = false
		m.UseBSPLayout = true
		if m.WorkspaceTrees == nil {
			m.WorkspaceTrees = make(map[int]*layout.BSPTree)
		}
		m.WorkspaceTrees[m.CurrentWorkspace] = nil
		m.ShowNotification("Layout: BSP tiling", "info", config.NotificationDuration)
	} else if m.UseBSPLayout {
		// BSP -> master-stack
		m.UseBSPLayout = false
		m.ShowNotification("Layout: master-stack", "info", config.NotificationDuration)
	} else {
		// master-stack -> scrolling
		m.UseScrollingLayout = true
		delete(m.WorkspaceScrollingLayouts, m.CurrentWorkspace)
		m.ShowNotification("Layout: scrolling (niri)", "info", config.NotificationDuration)
	}
	if !m.AutoTiling && (m.UseScrollingLayout || m.UseBSPLayout) {
		m.AutoTiling = true
	}
	if m.AutoTiling {
		m.TileAllWindows()
	}
}

// resetTiledFlags clears the Tiled flag on all current workspace windows
// and invalidates caches. Call when switching layout modes to prevent
// stale shared-border state from bleeding between modes.
func (m *OS) resetTiledFlags() {
	for i := range m.Windows {
		if m.Windows[i].Workspace == m.CurrentWorkspace {
			m.Windows[i].Tiled = false
			m.Windows[i].InvalidateCache()
		}
	}
}

// EnableScrollingLayout directly enables scrolling layout mode.
func (m *OS) EnableScrollingLayout() {
	m.resetTiledFlags()
	m.UseScrollingLayout = true
	m.UseBSPLayout = false
	if !m.AutoTiling {
		m.AutoTiling = true
	}
	// Clear old scrolling layout to rebuild from current windows
	delete(m.WorkspaceScrollingLayouts, m.CurrentWorkspace)
	m.TileAllWindows()
	m.ShowNotification("Layout: scrolling (niri)", "info", config.NotificationDuration)
}

// EnableBSPLayout directly enables BSP layout mode.
func (m *OS) EnableBSPLayout() {
	m.resetTiledFlags()
	m.UseScrollingLayout = false
	m.UseBSPLayout = true
	if !m.AutoTiling {
		m.AutoTiling = true
	}
	// Clear old BSP tree to rebuild
	if m.WorkspaceTrees == nil {
		m.WorkspaceTrees = make(map[int]*layout.BSPTree)
	}
	m.WorkspaceTrees[m.CurrentWorkspace] = nil
	m.TileAllWindows()
	m.ShowNotification("Layout: BSP tiling", "info", config.NotificationDuration)
}

// EnableMasterStackLayout directly enables master-stack layout mode.
func (m *OS) EnableMasterStackLayout() {
	m.resetTiledFlags()
	m.UseScrollingLayout = false
	m.UseBSPLayout = false
	if !m.AutoTiling {
		m.AutoTiling = true
	}
	m.TileAllWindows()
	m.ShowNotification("Layout: master-stack", "info", config.NotificationDuration)
}

// DisableAllTiling disables all tiling modes and resets window state.
func (m *OS) DisableAllTiling() {
	m.AutoTiling = false
	m.UseScrollingLayout = false
	m.resetTiledFlags()
	m.ShowNotification("Tiling disabled", "info", config.NotificationDuration)
}

// GetOrCreateScrollingLayout returns the scrolling layout for the current workspace.
func (m *OS) GetOrCreateScrollingLayout() *layout.ScrollingLayout {
	if m.WorkspaceScrollingLayouts == nil {
		m.WorkspaceScrollingLayouts = make(map[int]*layout.ScrollingLayout)
	}
	sl, ok := m.WorkspaceScrollingLayouts[m.CurrentWorkspace]
	if !ok || sl == nil {
		sl = layout.NewScrollingLayout()
		m.WorkspaceScrollingLayouts[m.CurrentWorkspace] = sl

		// Populate with existing visible windows
		for _, w := range m.Windows {
			if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
				intID := m.getWindowIntID(w.ID)
				sl.AddColumn(intID)
			}
		}

		// Sync FocusedCol with the OS focused window so the viewport
		// shows the correct column instead of always the last one.
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			fw := m.Windows[m.FocusedWindow]
			if fw.Workspace == m.CurrentWorkspace && !fw.IsFloating {
				intID := m.getWindowIntID(fw.ID)
				sl.FocusColumnContaining(intID)
			}
		}
	}
	return sl
}

// scrollingSetPositions applies the scrolling layout positions and dimensions.
// When animate is true, windows slide to their new positions.
func (m *OS) scrollingSetPositions() {
	m.scrollingSetPositionsAnimated(true)
}

// scrollingSetPositionsInstant applies positions without animation (mouse wheel).
func (m *OS) scrollingSetPositionsInstant() {
	m.scrollingSetPositionsAnimated(false)
}

func (m *OS) scrollingSetPositionsAnimated(animate bool) {
	sl := m.GetOrCreateScrollingLayout()
	screenW := m.GetRenderWidth()

	sl.ClampViewport(screenW)

	layouts := sl.ComputePositions(screenW, m.GetUsableHeight(), m.GetTopMargin())

	// Scrolling layout transitions always animate (even with --no-animations)
	// because the viewport shift is disorienting without the slide.
	dur := 150 * time.Millisecond
	if config.GetAnimationDuration() > 0 {
		dur = config.GetAnimationDuration()
	}

	for windowIntID, rect := range layouts {
		win := m.getWindowByIntID(windowIntID)
		if win == nil || win.Workspace != m.CurrentWorkspace || win.Minimized || win.IsFloating {
			continue
		}
		if win.Width != rect.W || win.Height != rect.H {
			win.Resize(rect.W, rect.H)
		}

		// If this window already has an in-flight animation heading to
		// the same target, don't touch it. TileAllWindows and other
		// callers re-run scrollingSetPositions frequently; without this
		// guard each call would cancel + recreate the animation from the
		// current intermediate position, making it stutter.
		if m.windowHasAnimationTo(win, rect.X, rect.Y, rect.W, rect.H) {
			continue
		}

		alreadyPlaced := win.X != 0 || win.Y != 0 || win.Width != 0
		if animate && alreadyPlaced && (win.X != rect.X || win.Y != rect.Y) {
			if !m.windowHasAnimationTo(win, rect.X, rect.Y, rect.W, rect.H) {
				m.CancelAnimationsForWindow(win)
				anim := ui.NewSnapAnimation(win, rect.X, rect.Y, rect.W, rect.H, dur)
				if anim != nil {
					m.Animations = append(m.Animations, anim)
					continue
				}
			} else {
				continue
			}
		}

		win.X = rect.X
		win.Y = rect.Y
		win.Width = rect.W
		win.Height = rect.H
		win.Tiled = false
		win.MarkPositionDirty()
		win.InvalidateCache()
	}
}

// windowHasAnimationTo checks if a window has an active animation
// heading to the exact target position. Used to avoid canceling
// in-flight animations when scrollingSetPositions is called repeatedly.
func (m *OS) windowHasAnimationTo(win *terminal.Window, x, y, w, h int) bool {
	for _, anim := range m.Animations {
		if anim.Window == win && !anim.Complete &&
			anim.EndX == x && anim.EndY == y &&
			anim.EndWidth == w && anim.EndHeight == h {
			return true
		}
	}
	return false
}

// ScrollingFocusLeft navigates to the column to the left.
func (m *OS) ScrollingFocusLeft() {
	sl := m.GetOrCreateScrollingLayout()
	sl.FocusLeft()
	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSyncFocusToOS()
	m.scrollingSetPositions()
}

// ScrollingFocusRight navigates to the column to the right.
func (m *OS) ScrollingFocusRight() {
	sl := m.GetOrCreateScrollingLayout()
	sl.FocusRight()
	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSyncFocusToOS()
	m.scrollingSetPositions()
}

// ScrollingMoveColumnLeft moves the focused column left.
func (m *OS) ScrollingMoveColumnLeft() {
	sl := m.GetOrCreateScrollingLayout()
	sl.MoveColumnLeft()
	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSetPositions()
}

// ScrollingMoveColumnRight moves the focused column right.
func (m *OS) ScrollingMoveColumnRight() {
	sl := m.GetOrCreateScrollingLayout()
	sl.MoveColumnRight()
	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSetPositions()
}

// ScrollingCycleWidth cycles the focused column through preset widths.
func (m *OS) ScrollingCycleWidth() {
	sl := m.GetOrCreateScrollingLayout()
	sl.CycleWidth()
	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSetPositions()
}

// ScrollingConsumeWindow absorbs the next column's window into the focused column.
func (m *OS) ScrollingConsumeWindow() {
	sl := m.GetOrCreateScrollingLayout()
	sl.ConsumeWindow()
	m.scrollingSetPositions()
}

// ScrollingExpelWindow ejects the last stacked window into its own column.
func (m *OS) ScrollingExpelWindow() {
	sl := m.GetOrCreateScrollingLayout()
	sl.ExpelWindow()
	m.scrollingSetPositions()
}

// ScrollingScrollViewport scrolls the viewport manually (mouse wheel).
// Uses instant positioning so scrolling feels direct and responsive.
func (m *OS) ScrollingScrollViewport(delta int) {
	sl := m.GetOrCreateScrollingLayout()
	screenW := m.GetRenderWidth()
	// Cancel any in-flight slide animations so the wheel feels direct
	m.CompleteAllAnimations()
	sl.ViewportX += delta * (screenW / 5)
	sl.ClampViewport(screenW)
	m.scrollingSetPositionsInstant()
}

// ScrollingOnFocusChange is called when the OS focus changes (click, etc.)
// to sync the scrolling layout and scroll the focused column into view.
// Only updates viewport/positions, never changes dimensions.
func (m *OS) ScrollingOnFocusChange() {
	sl := m.GetOrCreateScrollingLayout()
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}
	intID := m.getWindowIntID(fw.ID)
	if !sl.FocusColumnContaining(intID) {
		sl.AddColumn(intID)
		sl.FocusColumnContaining(intID)
	}

	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSetPositions()
}

// ScrollingOnWindowAdded adds a new window to the scrolling layout.
// Only adds the column  - FocusWindow handles viewport and positioning.
func (m *OS) ScrollingOnWindowAdded(w *terminal.Window) {
	sl := m.GetOrCreateScrollingLayout()
	intID := m.getWindowIntID(w.ID)
	// GetOrCreateScrollingLayout populates from m.Windows on first call.
	// If the window was already appended to m.Windows before this call,
	// the layout already has it. Don't add a duplicate.
	if sl.HasWindow(intID) {
		m.LogInfo("[SCROLL-ADD] ScrollingOnWindowAdded: window=%s intID=%d already in layout, skipping", w.ID[:8], intID)
		return
	}
	m.LogInfo("[SCROLL-ADD] ScrollingOnWindowAdded: window=%s intID=%d", w.ID[:8], intID)
	sl.AddColumn(intID)
}

// ScrollingOnWindowRemoved removes a window and focuses the neighbor.
func (m *OS) ScrollingOnWindowRemoved(windowIntID int) {
	sl := m.GetOrCreateScrollingLayout()
	sl.RemoveWindow(windowIntID)
	if sl.WindowCount() > 0 {
		sl.EnsureFocusedVisible(m.GetRenderWidth())
		m.scrollingSyncFocusToOS()
		m.scrollingSetPositions()
	}
}

// scrollingSyncFocusToOS sets the OS focused window to match the scrolling layout's focus.
// GetWindowIntID returns the integer BSP ID for a window by its string ID.
func (m *OS) GetWindowIntID(windowID string) int {
	return m.getWindowIntID(windowID)
}

// ScrollingSetPositions applies scrolling layout positions (public wrapper).
func (m *OS) ScrollingSetPositions() {
	m.scrollingSetPositions()
}

// GetWindowByIntID returns the window with the given integer BSP ID.
func (m *OS) GetWindowByIntID(intID int) *terminal.Window {
	return m.getWindowByIntID(intID)
}

// scrollingResizeColumn changes the focused column's width by delta pixels.
func (m *OS) scrollingResizeColumn(delta int) {
	sl := m.GetOrCreateScrollingLayout()
	if sl.FocusedCol < 0 || sl.FocusedCol >= len(sl.Columns) {
		return
	}
	col := &sl.Columns[sl.FocusedCol]
	// Get current width and apply delta, capped at 90% of screen
	screenW := m.GetRenderWidth()
	maxWidth := screenW * 9 / 10
	currentWidth := sl.ResolveColumnWidth(sl.FocusedCol, screenW)
	newWidth := max(min(currentWidth+delta, maxWidth), 20)
	col.FixedWidth = newWidth
	col.Proportion = 0 // FixedWidth takes priority
	sl.ScrollToFocusedColumn(m.GetRenderWidth())
	m.scrollingSetPositionsInstant() // resize must be instant, not animated
}

func (m *OS) scrollingSyncFocusToOS() {
	sl := m.GetOrCreateScrollingLayout()
	focusedWinID := sl.GetFocusedWindowID()
	if focusedWinID < 0 {
		return
	}
	win := m.getWindowByIntID(focusedWinID)
	if win == nil {
		return
	}
	m.scrollingFocusSyncing = true
	defer func() { m.scrollingFocusSyncing = false }()
	for i, w := range m.Windows {
		if w == win {
			m.FocusWindow(i)
			return
		}
	}
}

// EditScrollbackInEditor captures the focused pane's scrollback to a temp file
// and returns a tea.Cmd that suspends bubbletea and opens $EDITOR.
func (m *OS) EditScrollbackInEditor() tea.Cmd {
	content, err := m.capturePane("", "scrollback") // plain text, no ANSI
	if err != nil {
		m.ShowNotification("Capture failed: "+err.Error(), "error", 0)
		return nil
	}

	tmpFile, err := os.CreateTemp("", "tuios-scrollback-*.txt")
	if err != nil {
		m.ShowNotification("Failed to create temp file: "+err.Error(), "error", 0)
		return nil
	}
	if _, err := tmpFile.WriteString(content); err != nil {
		_ = tmpFile.Close()
		m.ShowNotification("Failed to write temp file: "+err.Error(), "error", 0)
		return nil
	}
	_ = tmpFile.Close()

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	// Use tea.ExecProcess to properly suspend bubbletea while editor runs
	c := exec.Command(editor, tmpFile.Name()) //nolint:gosec
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return nil
		}
		return nil
	})
}

// ToggleFloating toggles the focused window between floating and tiled mode.
func (m *OS) ToggleFloating() {
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}

	fw.IsFloating = !fw.IsFloating

	if fw.IsFloating {
		// Remove from BSP tree when floating
		if m.AutoTiling {
			m.RemoveWindowFromBSPTree(fw)
		}
		fw.Tiled = false
		fw.InvalidateCache()
		m.RecalcZOrder()
		m.ShowNotification("Window: floating", "info", config.NotificationDuration)
	} else {
		// Re-add to tiling layout when unfloating
		if m.AutoTiling {
			if m.UseScrollingLayout {
				intID := m.getWindowIntID(fw.ID)
				sl := m.GetOrCreateScrollingLayout()
				if !sl.HasWindow(intID) {
					sl.AddColumn(intID)
				}
				m.TileAllWindows()
			} else {
				m.AddWindowToBSPTree(fw)
			}
		}
		fw.InvalidateCache()
		m.RecalcZOrder()
		m.ShowNotification("Window: tiled", "info", config.NotificationDuration)
	}
}

// setupClipboardPassthrough wires a window's OSC 52 clipboard to bubbletea.
func (m *OS) setupClipboardPassthrough(window *terminal.Window) {
	if window == nil {
		return
	}
	window.ClipboardSetFunc = func(text string) {
		if m.PendingClipboardSet != nil {
			select {
			case m.PendingClipboardSet <- text:
			default:
				// Channel full, drop (non-blocking)
			}
		}
	}
}

// ToggleMultifocus toggles a window in/out of the multifocus set.
// When multiple windows are in the set, keystrokes are sent to all of them.
func (m *OS) ToggleMultifocus(windowIndex int) {
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		return
	}
	if m.MultifocusSet == nil {
		m.MultifocusSet = make(map[int]bool)
	}
	if m.MultifocusSet[windowIndex] {
		delete(m.MultifocusSet, windowIndex)
		if len(m.MultifocusSet) == 0 {
			m.MultifocusSet = nil
		}
		m.ShowNotification("Multifocus: removed window", "info", config.NotificationDuration)
	} else {
		m.MultifocusSet[windowIndex] = true
		m.ShowNotification(fmt.Sprintf("Multifocus: %d windows", len(m.MultifocusSet)), "info", config.NotificationDuration)
	}
	// Invalidate caches to show visual indicator on all affected windows
	m.Windows[windowIndex].InvalidateCache()
	for idx := range m.MultifocusSet {
		if idx < len(m.Windows) {
			m.Windows[idx].InvalidateCache()
		}
	}
}

// ClearMultifocus removes all windows from the multifocus set.
func (m *OS) ClearMultifocus() {
	if m.MultifocusSet != nil {
		for idx := range m.MultifocusSet {
			if idx < len(m.Windows) {
				m.Windows[idx].InvalidateCache()
			}
		}
	}
	m.MultifocusSet = nil
	m.ShowNotification("Multifocus: cleared", "info", 0)
}

// IsMultifocused returns true if the given window index is in the multifocus set.
func (m *OS) IsMultifocused(windowIndex int) bool {
	return m.MultifocusSet != nil && m.MultifocusSet[windowIndex]
}

// GetMultifocusWindows returns all window indices in the multifocus set.
func (m *OS) GetMultifocusWindows() []int {
	if m.MultifocusSet == nil {
		return nil
	}
	var indices []int
	for idx := range m.MultifocusSet {
		indices = append(indices, idx)
	}
	return indices
}

// NextLayout cycles to the next saved layout template.
func (m *OS) NextLayout() {
	templates, err := LoadLayoutTemplates()
	if err != nil || len(templates) == 0 {
		m.ShowNotification("No saved layouts", "warn", 0)
		return
	}

	m.LayoutCycleIndex = (m.LayoutCycleIndex + 1) % len(templates)
	tmpl := templates[m.LayoutCycleIndex]
	ApplyLayoutTemplate(tmpl, m)
	m.ShowNotification(fmt.Sprintf("Layout: %s (%d/%d)", tmpl.Name, m.LayoutCycleIndex+1, len(templates)), "info", 0)
}

// PrevLayout cycles to the previous saved layout template.
func (m *OS) PrevLayout() {
	templates, err := LoadLayoutTemplates()
	if err != nil || len(templates) == 0 {
		m.ShowNotification("No saved layouts", "warn", 0)
		return
	}

	m.LayoutCycleIndex--
	if m.LayoutCycleIndex < 0 {
		m.LayoutCycleIndex = len(templates) - 1
	}
	tmpl := templates[m.LayoutCycleIndex]
	ApplyLayoutTemplate(tmpl, m)
	m.ShowNotification(fmt.Sprintf("Layout: %s (%d/%d)", tmpl.Name, m.LayoutCycleIndex+1, len(templates)), "info", 0)
}

// FireHook fires a hook event with the current context.
func (m *OS) FireHook(event hooks.Event, windowID, windowName string) {
	if m.HookManager == nil {
		return
	}
	m.HookManager.Fire(event, hooks.Context{
		WindowID:   windowID,
		WindowName: windowName,
		Workspace:  m.CurrentWorkspace,
	})
}

// LogWarn logs a warning message.
func (m *OS) LogWarn(format string, args ...any) {
	m.Log("WARN", format, args...)
}

// LogError logs an error message.
func (m *OS) LogError(format string, args ...any) {
	m.Log("ERROR", format, args...)
}

// ShowNotification displays a temporary notification with animation.
func (m *OS) ShowNotification(message, notifType string, duration time.Duration) {
	notif := Notification{
		ID:        createID(),
		Message:   message,
		Type:      notifType,
		StartTime: time.Now(),
		Duration:  duration,
	}

	// Create fade-in animation (uses getter so it's instant when animations disabled)
	notif.Animation = &ui.Animation{
		StartTime: time.Now(),
		Duration:  config.GetAnimationDuration(),
		Progress:  0.0,
		Complete:  false,
	}

	m.Notifications = append(m.Notifications, notif)

	// Also log the notification
	switch notifType {
	case "error":
		m.LogError("%s", message)
	case "warning":
		m.LogWarn("%s", message)
	default:
		m.LogInfo("%s", message)
	}
}

// CleanupNotifications removes expired notifications.
func (m *OS) CleanupNotifications() {
	now := time.Now()
	var active []Notification

	for _, notif := range m.Notifications {
		if now.Sub(notif.StartTime) < notif.Duration {
			active = append(active, notif)
		}
	}

	m.Notifications = active
}

// CycleToNextVisibleWindow cycles focus to the next visible window in the current workspace.
func (m *OS) CycleToNextVisibleWindow() {
	if len(m.Windows) == 0 {
		return
	}
	// Find next visible (non-minimized and non-minimizing) window in current workspace
	visibleWindows := []int{}
	for i, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			visibleWindows = append(visibleWindows, i)
		}
	}
	if len(visibleWindows) == 0 {
		return
	}

	// Find current position in visible windows
	currentPos := -1
	for i, idx := range visibleWindows {
		if idx == m.FocusedWindow {
			currentPos = i
			break
		}
	}

	// Cycle to next visible window
	if currentPos >= 0 && currentPos < len(visibleWindows)-1 {
		m.FocusWindow(visibleWindows[currentPos+1])
	} else {
		m.FocusWindow(visibleWindows[0])
	}
}

// CycleToPreviousVisibleWindow cycles focus to the previous visible window in the current workspace.
func (m *OS) CycleToPreviousVisibleWindow() {
	if len(m.Windows) == 0 {
		return
	}
	// Find previous visible (non-minimized and non-minimizing) window in current workspace
	visibleWindows := []int{}
	for i, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			visibleWindows = append(visibleWindows, i)
		}
	}
	if len(visibleWindows) == 0 {
		return
	}

	// Find current position in visible windows
	currentPos := -1
	for i, idx := range visibleWindows {
		if idx == m.FocusedWindow {
			currentPos = i
			break
		}
	}

	// Cycle to previous visible window
	if currentPos > 0 {
		m.FocusWindow(visibleWindows[currentPos-1])
	} else {
		m.FocusWindow(visibleWindows[len(visibleWindows)-1])
	}
}

// FocusWindow sets focus to the window at the specified index.
func (m *OS) FocusWindow(i int) *OS {
	// Simple bounds check
	if len(m.Windows) == 0 || i < 0 || i >= len(m.Windows) {
		return m
	}

	// Don't do anything if already focused
	if m.FocusedWindow == i {
		return m
	}

	oldFocused := m.FocusedWindow

	// ATOMIC: Set focus and Z-index in one operation
	m.FocusedWindow = i

	// Save focus for current workspace
	if m.Windows[i].Workspace == m.CurrentWorkspace {
		m.WorkspaceFocus[m.CurrentWorkspace] = i
	}

	// Recalculate Z-ordering (floating always above non-floating)
	m.RecalcZOrder()

	// Always invalidate caches for immediate visual feedback on focus change
	// The Z-index change needs to be visible immediately when user clicks
	if oldFocused >= 0 && oldFocused < len(m.Windows) {
		m.Windows[oldFocused].MarkPositionDirty() // Use lighter invalidation
	}

	// Invalidate cache for new focused window (border color change + fresh content)
	m.Windows[i].InvalidateCache() // Full invalidation to show latest content

	m.FireHook(hooks.AfterFocusChange, m.Windows[i].ID, m.Windows[i].Title)

	// Sync scrolling layout focus and scroll into view when focus changes
	// via click or external means (not from scrollingSyncFocusToOS).
	if m.AutoTiling && m.UseScrollingLayout && !m.scrollingFocusSyncing {
		m.LogInfo("[SCROLL-FOCUS] FocusWindow(%d) -> triggering ScrollingOnFocusChange (old=%d)", i, oldFocused)
		m.ScrollingOnFocusChange()
	}

	return m
}

// RecalcZOrder recalculates Z-index values for all windows, ensuring floating
// windows are always above non-floating windows. Call after toggling IsFloating.
func (m *OS) RecalcZOrder() {
	focused := m.FocusedWindow
	z := 0
	// Non-floating, non-focused first
	for j := range m.Windows {
		if j != focused && !m.Windows[j].IsFloating {
			m.Windows[j].Z = z
			z++
		}
	}
	// Focused non-floating
	if focused >= 0 && focused < len(m.Windows) && !m.Windows[focused].IsFloating {
		m.Windows[focused].Z = z
		z++
	}
	// Non-focused floating
	for j := range m.Windows {
		if j != focused && m.Windows[j].IsFloating {
			m.Windows[j].Z = z
			z++
		}
	}
	// Focused floating (very top)
	if focused >= 0 && focused < len(m.Windows) && m.Windows[focused].IsFloating {
		m.Windows[focused].Z = z
	}
	m.MarkAllDirty()
}

// AddWindow adds a new window to the current workspace.
// In daemon mode, this creates a daemon-managed PTY and window.
func (m *OS) AddWindow(title string) *OS {
	// In daemon mode, use daemon PTY management
	if m.IsDaemonSession && m.DaemonClient != nil {
		return m.AddDaemonWindow(title)
	}

	newID := createID()
	if title == "" {
		title = fmt.Sprintf("Terminal %s", newID[:8])
	}

	m.LogInfo("Creating new window: %s (workspace %d)", title, m.CurrentWorkspace)

	// Handle case where screen dimensions aren't available yet
	screenWidth := m.GetRenderWidth()
	screenHeight := m.GetUsableHeight()

	if screenWidth == 0 || screenHeight == 0 {
		// Use sensible defaults when screen size is unknown
		screenWidth = 80
		screenHeight = 24
		m.LogWarn("Screen dimensions unknown, using defaults (%dx%d)", screenWidth, screenHeight)
	}

	width := screenWidth / 2
	height := screenHeight / 2

	// In floating mode, spawn at cursor position
	// In tiling mode, position doesn't matter as it will be auto-tiled
	var x, y int
	if !m.AutoTiling && m.LastMouseX > 0 && m.LastMouseY > 0 {
		// Spawn at cursor position, but ensure window stays on screen
		x = m.LastMouseX
		y = m.LastMouseY

		// Adjust if window would go off screen
		if x+width > screenWidth {
			x = screenWidth - width
		}
		if y+height > screenHeight {
			y = screenHeight - height
		}
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
	} else {
		// Center the window (default behavior for tiling mode or no cursor position)
		x = screenWidth / 4
		y = screenHeight / 4
	}

	window := terminal.NewWindow(newID, title, x, y, width, height, len(m.Windows), m.WindowExitChan, m.PTYDataChan)
	if window == nil {
		m.LogError("Failed to create window %s (PTY creation failed)", title)
		return m // Failed to create window
	}

	caps := GetHostCapabilities()
	if caps.CellWidth > 0 && caps.CellHeight > 0 {
		window.SetCellPixelDimensions(caps.CellWidth, caps.CellHeight)
	}

	window.Workspace = m.CurrentWorkspace

	m.setupKittyPassthrough(window)
	m.setupSixelPassthrough(window)
	m.setupTextSizingPassthrough(window)
	m.setupClipboardPassthrough(window)

	m.Windows = append(m.Windows, window)
	m.LogInfo("Window created successfully: %s (ID: %s, total windows: %d)", title, newID[:8], len(m.Windows))
	m.FireHook(hooks.AfterNewWindow, newID, title)

	// In scrolling mode, add to layout BEFORE focusing so that
	// ScrollingOnFocusChange can find the window's column.
	if m.AutoTiling && m.UseScrollingLayout {
		m.ScrollingOnWindowAdded(window)
	}

	// Focus the new window, which will bring it to the front
	m.FocusWindow(len(m.Windows) - 1)

	// Auto-tile if in tiling mode
	if m.AutoTiling {
		if m.UseScrollingLayout {
			m.TileAllWindows()
		} else {
			tree := m.GetOrCreateBSPTree()
			if tree != nil {
				m.AddWindowToBSPTree(window)
			} else {
				m.TileAllWindows()
			}
		}
	}

	return m
}

// UpdateAllWindowThemes updates the terminal colors for all windows when the theme changes
func (m *OS) UpdateAllWindowThemes() {
	m.LogInfo("Updating terminal colors for all windows after theme change")
	for _, window := range m.Windows {
		if window != nil {
			window.UpdateThemeColors()
		}
	}
}

// DeleteWindow removes the window at the specified index.
// In daemon mode, this also cleans up the daemon-managed PTY.
func (m *OS) DeleteWindow(i int) *OS {
	if len(m.Windows) == 0 || i < 0 || i >= len(m.Windows) {
		m.LogWarn("Cannot delete window: invalid index %d (total windows: %d)", i, len(m.Windows))
		return m
	}

	// Clean up window resources
	deletedWindow := m.Windows[i]
	m.LogInfo("Deleting window: %s (index: %d, ID: %s)", deletedWindow.Title, i, deletedWindow.ID[:8])

	// In daemon mode, clean up daemon-managed PTY
	if deletedWindow.DaemonMode && deletedWindow.PTYID != "" && m.DaemonClient != nil {
		m.DaemonClient.UnsubscribePTY(deletedWindow.PTYID)
		if err := m.DaemonClient.ClosePTY(deletedWindow.PTYID); err != nil {
			m.LogError("Failed to close daemon PTY: %v", err)
		}
	}

	// Get the window int ID BEFORE deleting (for BSP tree removal)
	windowIntID := m.getWindowIntID(deletedWindow.ID)

	// Clean up the BSP ID mapping
	if m.WindowToBSPID != nil {
		delete(m.WindowToBSPID, deletedWindow.ID)
		m.LogInfo("BSP: Removed ID mapping for window %s (int ID %d)", deletedWindow.ID[:8], windowIntID)
	}

	if m.KittyPassthrough != nil {
		m.KittyPassthrough.OnWindowClose(deletedWindow.ID)
		if data := m.KittyPassthrough.FlushPending(); len(data) > 0 {
			m.KittyPassthrough.WriteToHost(data)
		}
	}

	// Update MultifocusSet: remove the deleted index and shift higher indices down
	if len(m.MultifocusSet) > 0 {
		delete(m.MultifocusSet, i)
		updated := make(map[int]bool, len(m.MultifocusSet))
		for idx := range m.MultifocusSet {
			if idx > i {
				updated[idx-1] = true
			} else {
				updated[idx] = true
			}
		}
		if len(updated) == 0 {
			m.MultifocusSet = nil
		} else {
			m.MultifocusSet = updated
		}
	}

	deletedWindow.Close()

	// Remove any animations referencing this window to prevent memory leaks
	cleanedAnimations := make([]*ui.Animation, 0, len(m.Animations))
	animsCleaned := 0
	for _, anim := range m.Animations {
		if anim.Window != deletedWindow {
			cleanedAnimations = append(cleanedAnimations, anim)
		} else {
			animsCleaned++
		}
	}
	m.Animations = cleanedAnimations
	if animsCleaned > 0 {
		m.LogInfo("Cleaned up %d animations for deleted window", animsCleaned)
	}

	movedZ := deletedWindow.Z
	for j := range m.Windows {
		if m.Windows[j].Z > movedZ {
			m.Windows[j].Z--
			// Invalidate cache for windows whose Z changed
			m.Windows[j].InvalidateCache()
		}
	}

	m.Windows = slices.Delete(m.Windows, i, i+1)

	// Explicitly clear the deleted window pointer to help GC
	deletedWindow = nil

	m.LogInfo("Window deleted successfully (remaining windows: %d)", len(m.Windows))

	// Update focused window index
	if len(m.Windows) == 0 {
		m.FocusedWindow = -1
		m.LogInfo("No windows remaining, switching to window management mode")
		// Reset to window management mode when no windows are left
		m.Mode = WindowManagementMode
	} else if i < m.FocusedWindow {
		m.FocusedWindow--
	} else if i == m.FocusedWindow {
		// If we deleted the focused window, find the next visible window to focus
		m.FocusNextVisibleWindow()
	}

	// Retile if in tiling mode
	if m.AutoTiling {
		if m.UseScrollingLayout {
			// Scrolling mode: only touch the scrolling layout
			if windowIntID > 0 {
				m.ScrollingOnWindowRemoved(windowIntID)
			}
		} else {
			// BSP/master-stack mode
			tree := m.WorkspaceTrees[m.CurrentWorkspace]
			if tree != nil && windowIntID > 0 {
				tree.RemoveWindow(windowIntID)
				m.LogInfo("BSP: Removed window from tree, tree now has %d windows", tree.WindowCount())

				if tree.IsEmpty() {
					m.LogInfo("BSP: Tree is now empty, clearing workspace tree")
					m.WorkspaceTrees[m.CurrentWorkspace] = nil
				} else if len(m.Windows) > 0 {
					m.ApplyBSPLayout()
				}
			}

			// If there are still visible windows in this workspace, retile them
			if len(m.Windows) > 0 {
				hasVisibleInWorkspace := false
				for _, w := range m.Windows {
					if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
						hasVisibleInWorkspace = true
						break
					}
				}
				if hasVisibleInWorkspace && (tree == nil || tree.IsEmpty()) {
					m.TileAllWindows()
				}
			}
		}
	}

	// Sync state to daemon after window deletion
	m.SyncStateToDaemon()

	return m
}

// Snap snaps the window at index i to the specified position.
func (m *OS) Snap(i int, quarter SnapQuarter) *OS {
	if i < 0 || i >= len(m.Windows) {
		return m
	}

	// Create and start snap animation
	anim := m.CreateSnapAnimation(i, quarter)
	if anim != nil {
		m.Animations = append(m.Animations, anim)
	} else {
		// No animation needed (already at target), but still resize terminal if needed
		win := m.Windows[i]
		_, _, targetWidth, targetHeight := m.calculateSnapBounds(quarter)

		// Enforce minimum size
		targetWidth = max(targetWidth, config.DefaultWindowWidth)
		targetHeight = max(targetHeight, config.DefaultWindowHeight)

		// Make sure terminal is properly sized even if no animation
		if win.Width != targetWidth || win.Height != targetHeight {
			win.Resize(targetWidth, targetHeight)
		}
	}

	return m
}

func (m *OS) calculateSnapBounds(quarter SnapQuarter) (x, y, width, height int) {
	usableHeight := m.GetUsableHeight()
	renderWidth := m.GetRenderWidth()
	halfWidth := renderWidth / 2
	halfHeight := usableHeight / 2
	topMargin := m.GetTopMargin()

	switch quarter {
	case SnapLeft:
		return 0, topMargin, halfWidth, usableHeight
	case SnapRight:
		return halfWidth, topMargin, renderWidth - halfWidth, usableHeight
	case SnapTopLeft:
		return 0, topMargin, halfWidth, halfHeight
	case SnapTopRight:
		return halfWidth, topMargin, halfWidth, halfHeight
	case SnapBottomLeft:
		return 0, halfHeight + topMargin, halfWidth, usableHeight - halfHeight
	case SnapBottomRight:
		return halfWidth, halfHeight + topMargin, halfWidth, usableHeight - halfHeight
	case SnapFullScreen:
		return 0, topMargin, renderWidth, usableHeight
	case Unsnap:
		return renderWidth / 4, usableHeight/4 + topMargin, halfWidth, halfHeight
	default:
		return renderWidth / 4, usableHeight/4 + topMargin, halfWidth, halfHeight
	}
}

// ScaleWindowsToTerminal proportionally scales all windows when terminal size changes.
// This is called when restoring from daemon state to ensure windows fit the new terminal size.
// oldWidth/oldHeight are the terminal dimensions when state was saved.
// newWidth/newHeight are the current terminal dimensions.
func (m *OS) ScaleWindowsToTerminal(oldWidth, oldHeight, newWidth, newHeight int) {
	if m.AutoTiling {
		return // Tiling mode handles its own layout
	}

	if oldWidth <= 0 || oldHeight <= 0 || newWidth <= 0 || newHeight <= 0 {
		return // Invalid dimensions
	}

	oldUsableHeight := oldHeight - m.GetTopMargin()
	if config.DockbarPosition != "hidden" {
		oldUsableHeight -= 1
	}

	newUsableHeight := m.GetUsableHeight()
	newRenderWidth := m.GetRenderWidth()

	widthScale := float64(newRenderWidth) / float64(oldWidth)
	heightScale := float64(newUsableHeight) / float64(oldUsableHeight)

	m.LogInfo("[SCALE] Scaling windows: width %.2fx, height %.2fx", widthScale, heightScale)

	for _, win := range m.Windows {
		if win.Minimized {
			continue
		}

		// Scale position and size
		win.X = int(float64(win.X) * widthScale)
		win.Y = int(float64(win.Y) * heightScale)
		win.Width = int(float64(win.Width) * widthScale)
		win.Height = int(float64(win.Height) * heightScale)

		// Ensure minimum size
		if win.Width < config.DefaultWindowWidth {
			win.Width = config.DefaultWindowWidth
		}
		if win.Height < config.DefaultWindowHeight {
			win.Height = config.DefaultWindowHeight
		}

		// Ensure windows don't exceed terminal bounds
		if win.Width > newRenderWidth {
			win.Width = newRenderWidth
		}
		if win.Height > newUsableHeight {
			win.Height = newUsableHeight
		}

		// Ensure position keeps window on screen
		if win.X < 0 {
			win.X = 0
		}
		if win.Y < 0 {
			win.Y = 0
		}
		if win.X+win.Width > newRenderWidth {
			win.X = newRenderWidth - win.Width
		}
		if win.Y+win.Height > newUsableHeight {
			win.Y = newUsableHeight - win.Height
		}

		// Mark dirty and resize PTY
		win.MarkPositionDirty()
		win.Resize(win.Width, win.Height)
	}
}

// ClampWindowsToView ensures all floating windows are visible within the current terminal bounds.
// This is called when reattaching with a smaller terminal or when the terminal shrinks.
// Windows that would be off-screen are repositioned to remain visible.
func (m *OS) ClampWindowsToView() {
	if m.AutoTiling {
		return // Tiling mode handles its own layout
	}

	usableHeight := m.GetUsableHeight()
	renderWidth := m.GetRenderWidth()
	topMargin := m.GetTopMargin()
	minVisibleX := 20 // Minimum visible horizontal pixels (matches mouse.go)
	minVisibleY := 3  // Minimum visible vertical rows (matches mouse.go)
	clampedCount := 0

	for _, win := range m.Windows {
		if win.Workspace != m.CurrentWorkspace || win.Minimized {
			continue
		}

		originalX, originalY := win.X, win.Y
		needsResize := false

		// Clamp window size to fit within terminal if larger
		if win.Width > renderWidth {
			win.Width = renderWidth
			needsResize = true
		}
		if win.Height > usableHeight {
			win.Height = usableHeight
			needsResize = true
		}

		// Ensure minimum size
		if win.Width < config.DefaultWindowWidth {
			win.Width = config.DefaultWindowWidth
			needsResize = true
		}
		if win.Height < config.DefaultWindowHeight {
			win.Height = config.DefaultWindowHeight
			needsResize = true
		}

		// Clamp X position: ensure at least minVisibleX pixels are visible
		if win.X+win.Width < minVisibleX {
			win.X = minVisibleX - win.Width
		}
		if win.X > renderWidth-minVisibleX {
			win.X = renderWidth - minVisibleX
		}

		// Clamp Y position: ensure at least minVisibleY rows visible, and can't go behind dock
		if win.Y < topMargin {
			win.Y = topMargin
		}
		maxY := topMargin + usableHeight - minVisibleY
		if win.Y > maxY {
			win.Y = maxY
		}

		// If position changed, mark as dirty and log
		if win.X != originalX || win.Y != originalY || needsResize {
			win.MarkPositionDirty()
			if needsResize {
				win.Resize(win.Width, win.Height)
			}
			clampedCount++
		}
	}

	if clampedCount > 0 {
		m.LogInfo("[CLAMP] Repositioned %d windows to fit terminal bounds (%dx%d)", clampedCount, renderWidth, m.GetRenderHeight())
		m.SyncStateToDaemon()
	}
}

// GetFocusedWindow returns the currently focused window.
func (m *OS) GetFocusedWindow() *terminal.Window {
	if len(m.Windows) > 0 && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		// Only return the focused window if it's in the current workspace
		if m.Windows[m.FocusedWindow].Workspace == m.CurrentWorkspace {
			return m.Windows[m.FocusedWindow]
		}
	}
	return nil
}

// MinimizeWindow minimizes the window at the specified index.
func (m *OS) MinimizeWindow(i int) {
	if i >= 0 && i < len(m.Windows) && !m.Windows[i].Minimized && !m.Windows[i].Minimizing {
		// Get pointer to the actual window (not a copy)
		window := m.Windows[i]

		// Store current position before minimizing
		window.PreMinimizeX = window.X
		window.PreMinimizeY = window.Y
		window.PreMinimizeWidth = window.Width
		window.PreMinimizeHeight = window.Height

		// Immediately minimize without animation
		now := time.Now()
		window.Minimized = true
		window.Minimizing = false
		window.MinimizeOrder = now.UnixNano() // Track order for dock sorting

		// Set highlight timestamp for dock tab
		window.MinimizeHighlightUntil = now.Add(1 * time.Second)

		// DEBUG: Log minimize action
		if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
			if f, err := os.OpenFile("/tmp/tuios-minimize-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err == nil {
				_, _ = fmt.Fprintf(f, "[MINIMIZE] Window index=%d, ID=%s, CustomName=%s, Highlight set until %s\n",
					i, window.ID, window.CustomName, window.MinimizeHighlightUntil.Format("15:04:05.000"))
				_ = f.Close()
			}
		}

		// Change focus to next visible window
		if i == m.FocusedWindow {
			m.FocusNextVisibleWindow()
		}

		// Retile remaining windows if in tiling mode
		if m.AutoTiling {
			if m.UseScrollingLayout {
				// Remove from scrolling layout and retile
				intID := m.getWindowIntID(window.ID)
				sl := m.GetOrCreateScrollingLayout()
				sl.RemoveWindow(intID)
				sl.EnsureFocusedVisible(m.GetRenderWidth())
				m.scrollingSetPositions()
			} else {
				m.TileRemainingWindows(i)
			}
		}
	}
}

// RestoreWindow restores a minimized window at the specified index.
func (m *OS) RestoreWindow(i int) {
	if i >= 0 && i < len(m.Windows) && m.Windows[i].Minimized {
		window := m.Windows[i]

		// In tiling mode, skip animation and let TileAllWindows() handle positioning
		// This prevents incorrect tiling calculations when restoring multiple windows
		if m.AutoTiling {
			window.Minimized = false

			if m.UseScrollingLayout {
				// Re-add to scrolling layout
				intID := m.getWindowIntID(window.ID)
				sl := m.GetOrCreateScrollingLayout()
				if !sl.HasWindow(intID) {
					sl.AddColumn(intID)
				}
			}

			// Bring the window to front and focus it
			m.FocusWindow(i)
			m.TileAllWindows()
			return
		}

		// Non-tiling mode: create smooth animation to PreMinimize position
		// Create and start animation
		anim := m.CreateRestoreAnimation(i)
		if anim != nil {
			// Set window to animation start position (dock position) to avoid flashing
			window.X = anim.StartX
			window.Y = anim.StartY
			window.Width = anim.StartWidth
			window.Height = anim.StartHeight

			m.Animations = append(m.Animations, anim)
		}

		// Mark as not minimized after setting position so it shows during animation
		window.Minimized = false

		// Bring the window to front and focus it
		m.FocusWindow(i)
		// Enter window management mode to interact with the restored window
		m.Mode = WindowManagementMode
	}
}

// ToggleZoom toggles the focused window between zoomed (fullscreen) and normal state.
// When zoomed, the window fills the entire viewport (minus dock). When unzoomed, it
// returns to its previous size and position. Other windows are hidden while zoomed.
func (m *OS) ToggleZoom() {
	fw := m.GetFocusedWindow()
	if fw == nil {
		return
	}

	if fw.Zoomed {
		// Restore from zoom
		fw.Zoomed = false
		fw.X = fw.PreZoomX
		fw.Y = fw.PreZoomY
		fw.Width = fw.PreZoomWidth
		fw.Height = fw.PreZoomHeight
		fw.InvalidateCache()
		// Resize terminal to match restored dimensions
		termW := fw.ContentWidth()
		termH := fw.ContentHeight()
		if fw.Terminal != nil {
			fw.Terminal.Resize(termW, termH)
		}
		if fw.Pty != nil {
			_ = fw.Pty.Resize(termW, termH)
		}
		// If tiling, retile all
		if m.AutoTiling {
			m.TileAllWindows()
		}
		m.MarkAllDirty()
	} else {
		// Save current position and zoom to fullscreen
		fw.PreZoomX = fw.X
		fw.PreZoomY = fw.Y
		fw.PreZoomWidth = fw.Width
		fw.PreZoomHeight = fw.Height
		fw.Zoomed = true

		// Calculate zoom dimensions, respecting the dockbar's reserved space.
		topMargin := 0
		if config.DockbarPosition == "top" {
			topMargin = config.DockHeight
		}
		bottomMargin := 0
		if config.DockbarPosition == "bottom" {
			bottomMargin = config.DockHeight
		}
		screenWidth := m.GetRenderWidth()
		zoomWidth := screenWidth
		// If ZoomMaxWidth is set, cap width and center horizontally
		if config.ZoomMaxWidth > 0 && config.ZoomMaxWidth < screenWidth {
			zoomWidth = config.ZoomMaxWidth
		}
		fw.X = (screenWidth - zoomWidth) / 2
		fw.Y = topMargin
		fw.Width = zoomWidth
		fw.Height = m.GetRenderHeight() - topMargin - bottomMargin
		fw.InvalidateCache()
		// Resize terminal to match zoomed dimensions
		termW := fw.ContentWidth()
		termH := fw.ContentHeight()
		if fw.Terminal != nil {
			fw.Terminal.Resize(termW, termH)
		}
		if fw.Pty != nil {
			_ = fw.Pty.Resize(termW, termH)
		}
		m.MarkAllDirty()
	}
}

// RestoreMinimizedByIndex restores a minimized window by its minimized index.
func (m *OS) RestoreMinimizedByIndex(index int) {
	// Find the nth minimized window in current workspace
	minimizedCount := 0
	for i, window := range m.Windows {
		if window.Workspace == m.CurrentWorkspace && window.Minimized {
			if minimizedCount == index {
				m.RestoreWindow(i)
				return
			}
			minimizedCount++
		}
	}
}

// FocusNextVisibleWindow focuses the next visible window in the current workspace.
func (m *OS) FocusNextVisibleWindow() {
	// Find the next non-minimized and non-minimizing window to focus in current workspace
	// Start from the beginning to find any visible window

	// First pass: find any visible window in current workspace
	for i := range len(m.Windows) {
		if m.Windows[i].Workspace == m.CurrentWorkspace && !m.Windows[i].Minimized && !m.Windows[i].Minimizing {
			m.FocusWindow(i)
			return
		}
	}

	// No visible windows in workspace, set focus to -1
	m.FocusedWindow = -1
}

// HasMinimizedWindows returns true if there are any minimized windows.
func (m *OS) HasMinimizedWindows() bool {
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && w.Minimized {
			return true
		}
	}
	return false
}

// GetTopMargin returns the margin at the top (reserved space for the dockbar
// when positioned at "top").
func (m *OS) GetTopMargin() int {
	if config.DockbarPosition == "top" {
		return config.DockHeight
	}

	return 0
}

// GetDockbarContentYPosition returns the Y position of the dockbar
func (m *OS) GetDockbarContentYPosition() int {
	if config.DockbarPosition == "top" {
		return 0
	}

	return m.Height - 1
}

// GetTimeYPosition returns the Y position of the time display
func (m *OS) GetTimeYPosition() int {
	if config.DockbarPosition == "top" {
		return m.Height - 1
	}

	return 0
}

// GetUsableHeight returns the usable height excluding the dock. Auto-hide
// mode keeps the reservation so tiled windows have a stable layout  - the dock
// only hides when a specific window (zoom/float) explicitly expands into its
// rows.
func (m *OS) GetUsableHeight() int {
	if config.DockbarPosition == "hidden" {
		return m.GetRenderHeight()
	}
	return m.GetRenderHeight() - config.DockHeight
}

// GetRenderWidth returns the width to use for rendering.
// In multi-client mode, this is the minimum of the terminal width and
// the effective session width (min of all connected clients).
func (m *OS) GetRenderWidth() int {
	// If terminal size not yet known, use effective size if available
	if m.Width == 0 {
		if m.EffectiveWidth > 0 {
			return m.EffectiveWidth
		}
		return 0
	}
	// Use minimum of terminal and effective size
	if m.EffectiveWidth > 0 && m.EffectiveWidth < m.Width {
		return m.EffectiveWidth
	}
	return m.Width
}

// GetRenderHeight returns the height to use for rendering.
// In multi-client mode, this is the minimum of the terminal height and
// the effective session height (min of all connected clients).
func (m *OS) GetRenderHeight() int {
	// If terminal size not yet known, use effective size if available
	if m.Height == 0 {
		if m.EffectiveHeight > 0 {
			return m.EffectiveHeight
		}
		return 0
	}
	// Use minimum of terminal and effective size
	if m.EffectiveHeight > 0 && m.EffectiveHeight < m.Height {
		return m.EffectiveHeight
	}
	return m.Height
}

// MarkAllDirty marks all windows as dirty for re-rendering.
func (m *OS) MarkAllDirty() {
	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()
	for i := range m.Windows {
		m.Windows[i].Dirty = true
		m.Windows[i].ContentDirty = true
	}
	m.cachedViewContent = "" // Invalidate view cache
}

// MarkTerminalsWithNewContent marks terminals that have new content as dirty.
func (m *OS) MarkTerminalsWithNewContent() bool {
	// Fast path: no windows
	if len(m.Windows) == 0 {
		m.HasActiveTerminals = false
		return false
	}

	// Skip all terminal updates if we're actively dragging/resizing ANY window
	// This prevents content updates from interfering with mouse coordinate calculations
	if m.InteractionMode || m.Dragging || m.Resizing {
		return false
	}

	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()

	hasChanges := false
	activeTerminals := 0
	focusedWindowIndex := m.FocusedWindow

	for i := range m.Windows {
		window := m.Windows[i]

		// Skip invalid terminals
		// For daemon-mode windows, we don't have a local PTY but still need to update
		if window.Terminal == nil {
			continue
		}
		if window.Pty == nil && !window.DaemonMode {
			continue
		}

		activeTerminals++

		// Skip content checking for minimized windows or windows on a different workspace.
		// Their PTY data is still consumed (preventing buffer overflow), but we avoid
		// marking them dirty and triggering unnecessary rendering work.
		if window.Minimized || window.Workspace != m.CurrentWorkspace {
			// Drain the new-output flag so it doesn't accumulate
			window.HasNewOutput.Swap(false)
			continue
		}

		// Skip content checking for windows that are being moved/resized
		// This prevents btop and other rapidly-updating programs from interfering
		if window.IsBeingManipulated {
			continue
		}

		// Only mark dirty when the terminal actually received new output.
		// This avoids the old unconditional dirty-marking that defeated frame skipping.
		newOutput := window.HasNewOutput.Swap(false)
		if !newOutput {
			continue
		}

		// Mark window as dirty. Focused windows always update immediately.
		// Background windows update every 3rd cycle to reduce CPU, but
		// keep HasNewOutput set so they update when focused.
		isFocused := i == focusedWindowIndex
		if isFocused {
			window.MarkContentDirty()
			hasChanges = true
		} else {
			window.UpdateCounter++
			if window.UpdateCounter%3 == 0 {
				window.MarkContentDirty()
				hasChanges = true
			} else {
				// Don't clear the flag  - let it stay set so the window
				// updates on the next cycle or when focused
				window.HasNewOutput.Store(true)
			}
		}
	}

	m.HasActiveTerminals = activeTerminals > 0
	return hasChanges
}

// FlushPTYBuffersAfterResize flushes buffered PTY content and forces content polling
// after a resize operation completes. This ensures that shell prompt redraws in response
// to SIGWINCH are properly processed and displayed.
func (m *OS) FlushPTYBuffersAfterResize() {
	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()

	// Mark all windows as dirty to force full redraw
	for i := range m.Windows {
		window := m.Windows[i]
		if window == nil || window.Terminal == nil {
			continue
		}
		// For daemon-mode windows, we don't have a local PTY but still need to update
		if window.Pty == nil && !window.DaemonMode {
			continue
		}

		// Mark content as dirty to trigger re-rendering
		window.MarkContentDirty()

		// Invalidate cache to force fresh render
		window.InvalidateCache()
	}
}

// MoveSelectionCursor moves the selection cursor in the specified direction.
// Parameters:
//   - window: The window to operate on
//   - dx, dy: Direction to move cursor (-1, 0, 1)
//   - extending: true if extending selection (Shift+Arrow), false if just moving cursor
func (m *OS) MoveSelectionCursor(window *terminal.Window, dx, dy int, extending bool) {
	if window.Terminal == nil {
		return
	}

	screen := window.Terminal
	if screen == nil {
		return
	}

	// Get terminal dimensions (account for borders)
	maxX := window.ContentWidth()
	maxY := window.ContentHeight()

	// Initialize selection cursor if not set (only for non-extending moves)
	if !extending && !window.IsSelecting {
		// Position at terminal cursor when starting cursor movement
		cursor := screen.CursorPosition()
		window.SelectionCursor.X = cursor.X
		window.SelectionCursor.Y = cursor.Y
	}

	// Move cursor
	newX := window.SelectionCursor.X + dx
	newY := window.SelectionCursor.Y + dy

	// Handle scrollback when cursor moves beyond visible area in selection mode
	if newY < 0 {
		// Trying to move up past the top - scroll up in scrollback
		// Note: We DON'T enter scrollbackMode, we just adjust the offset
		// This allows selection to work with scrollback seamlessly
		if window.Terminal != nil {
			scrollbackLen := window.ScrollbackLen()
			if scrollbackLen > 0 && window.ScrollbackOffset < scrollbackLen {
				// Scroll up by increasing offset
				window.ScrollbackOffset++
				if window.ScrollbackOffset > scrollbackLen {
					window.ScrollbackOffset = scrollbackLen
				}
				window.InvalidateCache()
			}
		}
		newY = 0 // Keep cursor at top
	} else if newY >= maxY {
		// Trying to move down past the bottom - scroll down in scrollback
		if window.ScrollbackOffset > 0 {
			window.ScrollbackOffset--
			if window.ScrollbackOffset < 0 {
				window.ScrollbackOffset = 0
			}
			window.InvalidateCache()
		}
		newY = maxY - 1 // Keep cursor at bottom
	}

	// X boundary checking
	if newX < 0 {
		newX = 0
	}
	if newX >= maxX {
		newX = maxX - 1
	}

	// Update cursor position
	window.SelectionCursor.X = newX
	window.SelectionCursor.Y = newY

	if extending {
		// Extending selection - update selection end
		if !window.IsSelecting {
			// Start selection
			window.IsSelecting = true
			window.SelectionStart = window.SelectionCursor
		}
		window.SelectionEnd = window.SelectionCursor

		// Extract selected text
		selectedText := m.extractSelectedText(window)
		window.SelectedText = selectedText

	} else {
		// Just moving cursor - start new selection
		if window.IsSelecting || window.SelectedText != "" {
			// Clear existing selection
			window.IsSelecting = false
			window.SelectedText = ""
		}

		// Start new selection at cursor position
		window.SelectionStart = window.SelectionCursor
		window.SelectionEnd = window.SelectionCursor
		window.IsSelecting = true
	}

	window.InvalidateCache()
}

// extractSelectedText extracts text from the terminal within the selected region.
func (m *OS) extractSelectedText(window *terminal.Window) string {
	if window.Terminal == nil {
		return ""
	}

	// Ensure selection coordinates are valid
	startX := window.SelectionStart.X
	startY := window.SelectionStart.Y
	endX := window.SelectionEnd.X
	endY := window.SelectionEnd.Y

	// Normalize selection (ensure start comes before end)
	if startY > endY || (startY == endY && startX > endX) {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}

	var selectedLines []string

	// Extract text line by line
	for y := startY; y <= endY; y++ {
		var lineBuilder strings.Builder

		// Determine start and end columns for this line
		lineStartX := 0
		lineEndX := window.ContentWidth() // Account for borders

		if y == startY {
			lineStartX = startX
		}
		if y == endY {
			lineEndX = endX
		}

		// Extract characters from the terminal for this line
		for x := lineStartX; x <= lineEndX && x < window.ContentWidth(); x++ {
			// Get the cell from the terminal at this position
			cell := window.Terminal.CellAt(x, y)
			if cell != nil && cell.Content != "" {
				lineBuilder.WriteString(string(cell.Content))
			} else {
				lineBuilder.WriteByte(' ')
			}
		}

		selectedLines = append(selectedLines, lineBuilder.String())
	}

	return strings.Join(selectedLines, "\n")
}

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
	m.NextBSPWindowID = 1
	m.Animations = nil
	m.MultifocusSet = nil
	m.CurrentWorkspace = 0
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
	return nil
}

// Cleanup performs cleanup operations when the application exits.
func (m *OS) Cleanup() {
	// Reserved for future cleanup operations
}
