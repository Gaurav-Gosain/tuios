// Package app provides the core TUIOS application logic and window management.
package app

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
	"github.com/charmbracelet/lipgloss/v2"
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
	DraggedWindowIndex int // Index of window being dragged
	Windows            []*terminal.Window
	FocusedWindow      int
	Width              int
	Height             int
	X                  int
	Y                  int
	Mode               Mode
	terminalMu         sync.Mutex
	LastMouseX         int
	LastMouseY         int
	HasActiveTerminals bool
	ShowHelp           bool
	InteractionMode    bool            // True when actively dragging/resizing
	MouseSnapping      bool            // Enable/disable mouse snapping
	WindowExitChan     chan string     // Channel to signal window closure
	Animations         []*ui.Animation // Active animations
	CPUHistory         []float64       // CPU usage history for graph
	LastCPUUpdate      time.Time       // Last time CPU was updated
	RAMUsage           float64         // Cached RAM usage percentage
	LastRAMUpdate      time.Time       // Last time RAM was updated
	AutoTiling         bool            // Automatic tiling mode enabled
	MasterRatio        float64         // Master window width ratio for tiling (0.3-0.7)
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
	// SSH mode fields
	SSHSession ssh.Session // SSH session reference (nil in local mode)
	IsSSHMode  bool        // True when running over SSH
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
	TapePrefixActive  bool              // True when Ctrl+B, T was pressed (tape sub-prefix)
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
		logsPerPage := maxDisplayHeight - fixedLines
		if logsPerPage < 1 {
			logsPerPage = 1
		}
		maxScroll := totalLogs - logsPerPage
		if maxScroll < 0 {
			maxScroll = 0
		}
		m.LogScrollOffset = maxScroll
	}
}

// LogInfo logs an informational message.
func (m *OS) LogInfo(format string, args ...any) {
	m.Log("INFO", format, args...)
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

	// Simple Z-index assignment: focused window gets highest Z
	highestZ := len(m.Windows) - 1
	m.Windows[i].Z = highestZ

	// Assign Z-indices to other windows in order
	z := 0
	for j := range m.Windows {
		if j != i {
			m.Windows[j].Z = z
			z++
		}
	}

	// Always invalidate caches for immediate visual feedback on focus change
	// The Z-index change needs to be visible immediately when user clicks
	if oldFocused >= 0 && oldFocused < len(m.Windows) {
		m.Windows[oldFocused].MarkPositionDirty() // Use lighter invalidation
	}

	// Invalidate cache for new focused window (border color change)
	m.Windows[i].MarkPositionDirty() // Use lighter invalidation

	return m
}

// AddWindow adds a new window to the current workspace.
func (m *OS) AddWindow(title string) *OS {
	newID := createID()
	if title == "" {
		title = fmt.Sprintf("Terminal %s", newID[:8])
	}

	m.LogInfo("Creating new window: %s (workspace %d)", title, m.CurrentWorkspace)

	// Handle case where screen dimensions aren't available yet
	screenWidth := m.Width
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

	window := terminal.NewWindow(newID, title, x, y, width, height, len(m.Windows), m.WindowExitChan)
	if window == nil {
		m.LogError("Failed to create window %s (PTY creation failed)", title)
		return m // Failed to create window
	}

	// Set the workspace for the new window
	window.Workspace = m.CurrentWorkspace

	m.Windows = append(m.Windows, window)
	m.LogInfo("Window created successfully: %s (ID: %s, total windows: %d)", title, newID[:8], len(m.Windows))

	// Focus the new window, which will bring it to the front
	m.FocusWindow(len(m.Windows) - 1)

	// Auto-tile if in tiling mode
	if m.AutoTiling {
		m.LogInfo("Auto-tiling triggered for new window")
		// Use BSP tree if available
		tree := m.GetOrCreateBSPTree()
		if tree != nil {
			m.AddWindowToBSPTree(window)
		} else {
			m.TileAllWindows()
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
func (m *OS) DeleteWindow(i int) *OS {
	if len(m.Windows) == 0 || i < 0 || i >= len(m.Windows) {
		m.LogWarn("Cannot delete window: invalid index %d (total windows: %d)", i, len(m.Windows))
		return m
	}

	// Clean up window resources
	deletedWindow := m.Windows[i]
	m.LogInfo("Deleting window: %s (index: %d, ID: %s)", deletedWindow.Title, i, deletedWindow.ID[:8])

	// Get the window int ID BEFORE deleting (for BSP tree removal)
	windowIntID := m.getWindowIntID(deletedWindow.ID)

	// Clean up the BSP ID mapping
	if m.WindowToBSPID != nil {
		delete(m.WindowToBSPID, deletedWindow.ID)
		m.LogInfo("BSP: Removed ID mapping for window %s (int ID %d)", deletedWindow.ID[:8], windowIntID)
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
		// Use BSP tree if available
		tree := m.WorkspaceTrees[m.CurrentWorkspace]
		if tree != nil && windowIntID > 0 {
			tree.RemoveWindow(windowIntID)
			m.LogInfo("BSP: Removed window from tree, tree now has %d windows", tree.WindowCount())

			// If tree is now empty, clear it completely so next window starts fresh
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
	halfWidth := m.Width / 2
	halfHeight := usableHeight / 2
	topMargin := m.GetTopMargin()

	switch quarter {
	case SnapLeft:
		return 0, topMargin, halfWidth, usableHeight
	case SnapRight:
		return halfWidth, topMargin, m.Width - halfWidth, usableHeight
	case SnapTopLeft:
		return 0, topMargin, halfWidth, halfHeight
	case SnapTopRight:
		return halfWidth, topMargin, halfWidth, halfHeight
	case SnapBottomLeft:
		return 0, halfHeight + topMargin, halfWidth, usableHeight - halfHeight
	case SnapBottomRight:
		return halfWidth, halfHeight + topMargin, halfWidth, usableHeight - halfHeight
	case SnapFullScreen:
		return 0, topMargin, m.Width, usableHeight
	case Unsnap:
		return m.Width / 4, usableHeight/4 + topMargin, halfWidth, halfHeight
	default:
		return m.Width / 4, usableHeight/4 + topMargin, halfWidth, halfHeight
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
			m.TileRemainingWindows(i)
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
			// Simply mark as not minimized and let TileAllWindows() position it
			window.Minimized = false

			// Set to a temporary position (will be overridden by TileAllWindows)
			window.X = 10
			window.Y = 5
			window.Width = config.DefaultWindowWidth
			window.Height = config.DefaultWindowHeight

			// Bring the window to front and focus it
			m.FocusWindow(i)
			m.Mode = WindowManagementMode

			// Note: No animation in tiling mode. TileAllWindows() should be called
			// by the caller after all restores are complete.
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

// GetTopMargin returns the margin at the top (possibly reserved space for the dockbar)
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

// GetUsableHeight returns the usable height excluding the dock.
func (m *OS) GetUsableHeight() int {
	if config.DockbarPosition == "hidden" {
		return m.Height
	}
	// Reserve space for the dock (at top or bottom)
	return m.Height - config.DockHeight
}

// MarkAllDirty marks all windows as dirty for re-rendering.
func (m *OS) MarkAllDirty() {
	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()
	for i := range m.Windows {
		m.Windows[i].Dirty = true
		m.Windows[i].ContentDirty = true
	}
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
		if window.Terminal == nil || window.Pty == nil {
			continue
		}

		activeTerminals++

		// Skip content checking for windows that are being moved/resized
		// This prevents btop and other rapidly-updating programs from interfering
		if window.IsBeingManipulated {
			continue
		}

		// Smart content updating with throttling
		isFocused := i == focusedWindowIndex

		if isFocused {
			// Always update focused window immediately for responsive interaction
			window.MarkContentDirty()
			hasChanges = true
		} else {
			// For background windows, throttle updates to reduce CPU usage
			window.UpdateCounter++
			if window.UpdateCounter%3 == 0 { // Update every 3rd cycle (~20Hz instead of 60Hz)
				window.MarkContentDirty()
				hasChanges = true
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
		if window == nil || window.Terminal == nil || window.Pty == nil {
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
	maxX := window.Width - 2
	maxY := window.Height - 2

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
		line := ""

		// Determine start and end columns for this line
		lineStartX := 0
		lineEndX := window.Width - 2 // Account for borders

		if y == startY {
			lineStartX = startX
		}
		if y == endY {
			lineEndX = endX
		}

		// Extract characters from the terminal for this line
		for x := lineStartX; x <= lineEndX && x < window.Width-2; x++ {
			// Get the cell from the terminal at this position
			cell := window.Terminal.CellAt(x, y)
			if cell != nil && cell.Content != "" {
				line += string(cell.Content)
			} else {
				line += " "
			}
		}

		selectedLines = append(selectedLines, line)
	}

	return strings.Join(selectedLines, "\n")
}

// Cleanup performs cleanup operations when the application exits.
func (m *OS) Cleanup() {
	// Reserved for future cleanup operations
}

// ExecuteCommand executes a tape command (implements tape.Executor interface).
func (m *OS) ExecuteCommand(_ *tape.Command) error {
	return nil // Basic implementation
}

// GetFocusedWindowID returns the ID of the focused window (implements tape.Executor interface)
func (m *OS) GetFocusedWindowID() string {
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		return m.Windows[m.FocusedWindow].ID
	}
	return ""
}

// SendToWindow sends bytes to a window's PTY (implements tape.Executor interface)
func (m *OS) SendToWindow(windowID string, data []byte) error {
	for _, w := range m.Windows {
		if w.ID == windowID {
			_, err := w.Pty.Write(data)
			return err
		}
	}
	return nil
}

// CreateNewWindow creates a new window (implements tape.Executor interface)
func (m *OS) CreateNewWindow() error {
	m.AddWindow("Window")
	m.MarkAllDirty()
	return nil
}

// CloseWindow closes a window (implements tape.Executor interface)
func (m *OS) CloseWindow(windowID string) error {
	for i, w := range m.Windows {
		if w.ID == windowID {
			m.DeleteWindow(i)
			m.MarkAllDirty()
			return nil
		}
	}
	return nil
}

// SwitchWorkspace switches to a workspace (implements tape.Executor interface)
func (m *OS) SwitchWorkspace(workspace int) error {
	if workspace >= 1 && workspace <= m.NumWorkspaces {
		// Use the full SwitchToWorkspace which handles focus, mode, etc.
		// But disable recording during playback to avoid re-recording
		recorder := m.TapeRecorder
		m.TapeRecorder = nil
		m.SwitchToWorkspace(workspace)
		m.TapeRecorder = recorder
		m.MarkAllDirty()
	}
	return nil
}

// ToggleTiling toggles tiling mode (implements tape.Executor interface)
func (m *OS) ToggleTiling() error {
	m.AutoTiling = !m.AutoTiling
	if m.AutoTiling {
		m.TileAllWindows()
	}
	m.MarkAllDirty()
	return nil
}

// SetMode sets the interaction mode (implements tape.Executor interface)
func (m *OS) SetMode(mode string) error {
	switch mode {
	case "terminal", "Terminal", "TerminalMode":
		m.Mode = TerminalMode
		// Ensure a window is focused when entering terminal mode
		if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
			// Find first visible window in current workspace to focus
			for i, w := range m.Windows {
				if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
					m.FocusWindow(i)
					break
				}
			}
		}
	case "window", "Window", "WindowManagementMode":
		m.Mode = WindowManagementMode
	}
	return nil
}

// NextWindow focuses the next window (implements tape.Executor interface)
func (m *OS) NextWindow() error {
	if len(m.Windows) == 0 {
		return nil
	}
	m.CycleToNextVisibleWindow()
	m.MarkAllDirty()
	return nil
}

// PrevWindow focuses the previous window (implements tape.Executor interface)
func (m *OS) PrevWindow() error {
	if len(m.Windows) == 0 {
		return nil
	}
	m.CycleToPreviousVisibleWindow()
	m.MarkAllDirty()
	return nil
}

// FocusWindowByID focuses a specific window by ID (implements tape.Executor interface)
func (m *OS) FocusWindowByID(windowID string) error {
	for i, w := range m.Windows {
		if w.ID == windowID {
			m.FocusWindow(i)
			m.MarkAllDirty()
			return nil
		}
	}
	return nil
}

// RenameWindowByID renames a window by its ID (implements tape.Executor interface).
func (m *OS) RenameWindowByID(windowID, name string) error {
	for _, w := range m.Windows {
		if w.ID == windowID {
			w.Title = name
			m.MarkAllDirty()
			return nil
		}
	}
	return nil
}

// MinimizeWindowByID minimizes a window (implements tape.Executor interface)
func (m *OS) MinimizeWindowByID(windowID string) error {
	for i, w := range m.Windows {
		if w.ID == windowID {
			m.MinimizeWindow(i)
			m.MarkAllDirty()
			return nil
		}
	}
	return nil
}

// RestoreWindowByID restores a minimized window (implements tape.Executor interface)
func (m *OS) RestoreWindowByID(windowID string) error {
	for i, w := range m.Windows {
		if w.ID == windowID {
			m.RestoreWindow(i)
			m.MarkAllDirty()
			return nil
		}
	}
	return nil
}

// EnableTiling enables tiling mode (implements tape.Executor interface)
func (m *OS) EnableTiling() error {
	if !m.AutoTiling {
		m.AutoTiling = true
		m.TileAllWindows()
		m.MarkAllDirty()
	}
	return nil
}

// DisableTiling disables tiling mode (implements tape.Executor interface)
func (m *OS) DisableTiling() error {
	m.AutoTiling = false
	m.MarkAllDirty()
	return nil
}

// SnapByDirection snaps a window to a direction (implements tape.Executor interface)
func (m *OS) SnapByDirection(direction string) error {
	// Cannot snap windows while tiling mode is enabled
	if m.AutoTiling {
		return fmt.Errorf("cannot snap windows while tiling mode is enabled")
	}

	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return nil
	}

	// Convert direction string to SnapQuarter if possible
	quarter := SnapTopLeft
	switch direction {
	case "left":
		quarter = SnapLeft
	case "right":
		quarter = SnapRight
	case "fullscreen":
		// Fullscreen is handled separately
		m.Snap(m.FocusedWindow, SnapTopLeft)
		m.MarkAllDirty()
		return nil
	}

	m.Snap(m.FocusedWindow, quarter)
	m.MarkAllDirty()
	return nil
}

// MoveWindowToWorkspaceByID moves a window to a workspace (implements tape.Executor interface)
func (m *OS) MoveWindowToWorkspaceByID(windowID string, workspace int) error {
	if workspace < 1 || workspace > m.NumWorkspaces {
		return nil
	}

	for i, w := range m.Windows {
		if w.ID == windowID {
			w.Workspace = workspace
			// Remove from current layout if in managed window
			if m.FocusedWindow == i {
				m.FocusedWindow = -1
			}
			// Retile if in tiling mode
			if m.AutoTiling {
				m.TileAllWindows()
			}
			m.MarkAllDirty()
			return nil
		}
	}

	return nil
}

// MoveAndFollowWorkspaceByID moves a window to a workspace and switches to it (implements tape.Executor interface)
func (m *OS) MoveAndFollowWorkspaceByID(windowID string, workspace int) error {
	if workspace < 1 || workspace > m.NumWorkspaces {
		return nil
	}

	for _, w := range m.Windows {
		if w.ID == windowID {
			w.Workspace = workspace
			m.CurrentWorkspace = workspace
			m.FocusedWindow = -1
			// Retile if in tiling mode
			if m.AutoTiling {
				m.TileAllWindows()
			}
			m.MarkAllDirty()
			return nil
		}
	}

	return nil
}

// SplitHorizontal splits the focused window horizontally (implements tape.Executor interface)
func (m *OS) SplitHorizontal() error {
	if !m.AutoTiling {
		return nil
	}
	m.SplitFocusedHorizontal()
	m.MarkAllDirty()
	return nil
}

// SplitVertical splits the focused window vertically (implements tape.Executor interface)
func (m *OS) SplitVertical() error {
	if !m.AutoTiling {
		return nil
	}
	m.SplitFocusedVertical()
	m.MarkAllDirty()
	return nil
}

// RotateSplit rotates the split direction at the focused window (implements tape.Executor interface)
func (m *OS) RotateSplit() error {
	if !m.AutoTiling {
		return nil
	}
	m.RotateFocusedSplit()
	m.MarkAllDirty()
	return nil
}

// EqualizeSplitsExec equalizes all split ratios (implements tape.Executor interface)
func (m *OS) EqualizeSplitsExec() error {
	if !m.AutoTiling {
		return nil
	}
	m.EqualizeSplits()
	m.MarkAllDirty()
	return nil
}

// Preselect sets the preselection direction for the next window (implements tape.Executor interface)
func (m *OS) Preselect(direction string) error {
	if !m.AutoTiling {
		return nil
	}
	switch direction {
	case "left":
		m.SetPreselection(layout.PreselectionLeft)
	case "right":
		m.SetPreselection(layout.PreselectionRight)
	case "up":
		m.SetPreselection(layout.PreselectionUp)
	case "down":
		m.SetPreselection(layout.PreselectionDown)
	default:
		m.ClearPreselection()
	}
	return nil
}

// EnableAnimations enables UI animations (implements tape.Executor interface)
func (m *OS) EnableAnimations() error {
	config.AnimationsEnabled = true
	m.ShowNotification("Animations: ON", "info", config.NotificationDuration)
	return nil
}

// DisableAnimations disables UI animations (implements tape.Executor interface)
func (m *OS) DisableAnimations() error {
	config.AnimationsEnabled = false
	m.ShowNotification("Animations: OFF", "info", config.NotificationDuration)
	return nil
}

// ToggleAnimations toggles UI animations (implements tape.Executor interface)
func (m *OS) ToggleAnimations() error {
	config.AnimationsEnabled = !config.AnimationsEnabled
	if config.AnimationsEnabled {
		m.ShowNotification("Animations: ON", "info", config.NotificationDuration)
	} else {
		m.ShowNotification("Animations: OFF", "info", config.NotificationDuration)
	}
	return nil
}
