package app

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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

// OS represents the main application state and window manager.
// It manages all windows, workspaces, and user interactions.
type OS struct {
	Dragging              bool
	Resizing              bool
	ResizeCorner          ResizeCorner
	PreResizeState        terminal.Window
	ResizeStartX          int
	ResizeStartY          int
	DragOffsetX           int
	DragOffsetY           int
	DragStartX            int // Track where drag started
	DragStartY            int // Track where drag started
	TiledX                int // Original tiled position X
	TiledY                int // Original tiled position Y
	TiledWidth            int // Original tiled width
	TiledHeight           int // Original tiled height
	DraggedWindowIndex    int // Index of window being dragged
	Windows               []*terminal.Window
	FocusedWindow         int
	Width                 int
	Height                int
	X                     int
	Y                     int
	Mode                  Mode
	terminalMu            sync.Mutex
	LastMouseX            int
	LastMouseY            int
	HasActiveTerminals    bool
	ShowHelp              bool
	InteractionMode       bool            // True when actively dragging/resizing
	MouseSnapping         bool            // Enable/disable mouse snapping
	WindowExitChan        chan string     // Channel to signal window closure
	Animations            []*ui.Animation // Active animations
	CPUHistory            []float64       // CPU usage history for graph
	LastCPUUpdate         time.Time       // Last time CPU was updated
	RAMUsage              float64         // Cached RAM usage percentage
	LastRAMUpdate         time.Time       // Last time RAM was updated
	AutoTiling            bool            // Automatic tiling mode enabled
	RenamingWindow        bool            // True when renaming a window
	RenameBuffer          string          // Buffer for new window name
	PrefixActive          bool            // True when prefix key was pressed (tmux-style)
	WorkspacePrefixActive bool            // True when Ctrl+B, w was pressed (workspace sub-prefix)
	MinimizePrefixActive  bool            // True when Ctrl+B, m was pressed (minimize sub-prefix)
	TilingPrefixActive    bool            // True when Ctrl+B, t was pressed (tiling/window sub-prefix)
	LastPrefixTime        time.Time       // Time when prefix was activated
	HelpScrollOffset      int             // Scroll offset for help menu
	CurrentWorkspace      int             // Current active workspace (1-9)
	NumWorkspaces         int             // Total number of workspaces
	WorkspaceFocus        map[int]int     // Remembers focused window per workspace
	ShowLogs              bool            // True when showing log overlay
	LogMessages           []LogMessage    // Store log messages
	LogScrollOffset       int             // Scroll offset for log viewer
	Notifications         []Notification  // Active notifications
	SelectionMode         bool            // True when in text selection mode
	ClipboardContent      string          // Store clipboard content from tea.ClipboardMsg
	ShowCacheStats        bool            // True when showing style cache statistics overlay
	// Performance optimization caches
	cachedSeparator        string // Cached dock separator string
	cachedSeparatorWidth   int    // Width of cached separator
	workspaceActiveStyle   *lipgloss.Style
	workspaceInactiveStyle *lipgloss.Style
	// SSH mode fields
	SSHSession ssh.Session // SSH session reference (nil in local mode)
	IsSSHMode  bool        // True when running over SSH
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

func createID() string {
	return uuid.New().String()
}

// Log adds a new log message to the log buffer.
func (m *OS) Log(level, format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	logMsg := LogMessage{
		Time:    time.Now(),
		Level:   level,
		Message: message,
	}

	// Keep only last MaxLogMessages messages
	m.LogMessages = append(m.LogMessages, logMsg)
	if len(m.LogMessages) > config.MaxLogMessages {
		m.LogMessages = m.LogMessages[len(m.LogMessages)-config.MaxLogMessages:]
	}
}

// LogInfo logs an informational message.
func (m *OS) LogInfo(format string, args ...interface{}) {
	m.Log("INFO", format, args...)
}

// LogWarn logs a warning message.
func (m *OS) LogWarn(format string, args ...interface{}) {
	m.Log("WARN", format, args...)
}

// LogError logs an error message.
func (m *OS) LogError(format string, args ...interface{}) {
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

	// Create fade-in animation
	notif.Animation = &ui.Animation{
		StartTime: time.Now(),
		Duration:  config.DefaultAnimationDuration,
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

	// Handle case where screen dimensions aren't available yet
	screenWidth := m.Width
	screenHeight := m.GetUsableHeight()

	if screenWidth == 0 || screenHeight == 0 {
		// Use sensible defaults when screen size is unknown
		screenWidth = 80
		screenHeight = 24
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
		return m // Failed to create window
	}

	// Set the workspace for the new window
	window.Workspace = m.CurrentWorkspace

	m.Windows = append(m.Windows, window)

	// Focus the new window, which will bring it to the front
	m.FocusWindow(len(m.Windows) - 1)

	// Auto-tile if in tiling mode
	if m.AutoTiling {
		m.TileAllWindows()
	}

	return m
}

// DeleteWindow removes the window at the specified index.
func (m *OS) DeleteWindow(i int) *OS {
	if len(m.Windows) == 0 || i < 0 || i >= len(m.Windows) {
		return m
	}

	// Clean up window resources
	m.Windows[i].Close()

	movedZ := m.Windows[i].Z
	for j := range m.Windows {
		if m.Windows[j].Z > movedZ {
			m.Windows[j].Z--
			// Invalidate cache for windows whose Z changed
			m.Windows[j].InvalidateCache()
		}
	}

	m.Windows = slices.Delete(m.Windows, i, i+1)

	// Update focused window index
	if len(m.Windows) == 0 {
		m.FocusedWindow = -1
		// Reset to window management mode when no windows are left
		m.Mode = WindowManagementMode
	} else if i < m.FocusedWindow {
		m.FocusedWindow--
	} else if i == m.FocusedWindow {
		// If we deleted the focused window, find the next visible window to focus
		m.FocusNextVisibleWindow()
	}

	// Retile if in tiling mode
	if m.AutoTiling && len(m.Windows) > 0 {
		m.TileAllWindows()
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

	switch quarter {
	case SnapLeft:
		return 0, 0, halfWidth, usableHeight
	case SnapRight:
		return halfWidth, 0, m.Width - halfWidth, usableHeight
	case SnapTopLeft:
		return 0, 0, halfWidth, halfHeight
	case SnapTopRight:
		return halfWidth, 0, halfWidth, halfHeight
	case SnapBottomLeft:
		return 0, halfHeight, halfWidth, usableHeight - halfHeight
	case SnapBottomRight:
		return halfWidth, halfHeight, halfWidth, usableHeight - halfHeight
	case SnapFullScreen:
		return 0, 0, m.Width, usableHeight
	case Unsnap:
		return m.Width / 4, usableHeight / 4, halfWidth, halfHeight
	default:
		return m.Width / 4, usableHeight / 4, halfWidth, halfHeight
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
		// Store current position before minimizing
		window := m.Windows[i]
		window.PreMinimizeX = window.X
		window.PreMinimizeY = window.Y
		window.PreMinimizeWidth = window.Width
		window.PreMinimizeHeight = window.Height

		// Mark as minimizing (for dock placeholder)
		window.Minimizing = true

		// Create and start animation
		anim := m.CreateMinimizeAnimation(i)
		if anim != nil {
			m.Animations = append(m.Animations, anim)
		}

		// Don't change focus yet - wait for animation to complete

		// Retile remaining windows if in tiling mode
		if m.AutoTiling {
			// Schedule retiling after the minimize animation
			// We do this by creating tiling animations for other windows
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
	for i := 0; i < len(m.Windows); i++ {
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

// GetUsableHeight returns the usable height excluding the dock.
func (m *OS) GetUsableHeight() int {
	// Always reserve space for the dock at the bottom
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
