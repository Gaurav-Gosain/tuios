package input

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// ActionHandler is a function that handles a specific action
type ActionHandler func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd)

// ActionDispatcher maps action names to handler functions
type ActionDispatcher struct {
	handlers map[string]ActionHandler
}

// NewActionDispatcher creates a new action dispatcher with all handlers registered
func NewActionDispatcher() *ActionDispatcher {
	d := &ActionDispatcher{
		handlers: make(map[string]ActionHandler),
	}
	d.registerHandlers()
	return d
}

// registerHandlers registers all action handlers
func (d *ActionDispatcher) registerHandlers() {
	// Window Management actions
	d.Register("new_window", handleNewWindow)
	d.Register("close_window", handleCloseWindow)
	d.Register("rename_window", handleRenameWindow)
	d.Register("minimize_window", handleMinimizeWindow)
	d.Register("restore_all", handleRestoreAll)
	d.Register("next_window", handleNextWindow)
	d.Register("prev_window", handlePrevWindow)

	// Window selection (1-9)
	for i := 1; i <= 9; i++ {
		idx := i - 1 // Convert to 0-based index
		d.Register("select_window_"+string(rune('0'+i)), makeSelectWindowHandler(idx))
	}

	// Workspace switching (1-9)
	for i := 1; i <= 9; i++ {
		d.Register("switch_workspace_"+string(rune('0'+i)), makeSwitchWorkspaceHandler(i))
		d.Register("move_and_follow_"+string(rune('0'+i)), makeMoveAndFollowHandler(i))
	}

	// Layout actions
	d.Register("snap_left", handleSnapLeft)
	d.Register("snap_right", handleSnapRight)
	d.Register("snap_fullscreen", handleSnapFullscreen)
	d.Register("unsnap", handleUnsnap)
	d.Register("snap_corner_1", makeSnapCornerHandler(app.SnapTopLeft))
	d.Register("snap_corner_2", makeSnapCornerHandler(app.SnapTopRight))
	d.Register("snap_corner_3", makeSnapCornerHandler(app.SnapBottomLeft))
	d.Register("snap_corner_4", makeSnapCornerHandler(app.SnapBottomRight))
	d.Register("toggle_tiling", handleToggleTiling)
	d.Register("swap_left", handleSwapLeft)
	d.Register("swap_right", handleSwapRight)
	d.Register("swap_up", handleSwapUp)
	d.Register("swap_down", handleSwapDown)
	d.Register("resize_master_shrink", handleResizeMasterShrink)
	d.Register("resize_master_grow", handleResizeMasterGrow)
	d.Register("resize_height_shrink", handleResizeHeightShrink)
	d.Register("resize_height_grow", handleResizeHeightGrow)
	d.Register("resize_master_shrink_left", handleResizeMasterShrinkLeft)
	d.Register("resize_master_grow_left", handleResizeMasterGrowLeft)
	d.Register("resize_height_shrink_top", handleResizeHeightShrinkTop)
	d.Register("resize_height_grow_top", handleResizeHeightGrowTop)

	// BSP tiling actions
	d.Register("split_horizontal", handleSplitHorizontal)
	d.Register("split_vertical", handleSplitVertical)
	d.Register("rotate_split", handleRotateSplit)
	d.Register("equalize_splits", handleEqualizeSplits)
	d.Register("preselect_left", handlePreselectLeft)
	d.Register("preselect_right", handlePreselectRight)
	d.Register("preselect_up", handlePreselectUp)
	d.Register("preselect_down", handlePreselectDown)

	// Mode control actions
	d.Register("enter_terminal_mode", handleEnterTerminalMode)
	d.Register("enter_window_mode", handleEnterWindowMode)
	d.Register("toggle_help", handleToggleHelp)
	d.Register("quit", handleQuit)

	// Clipboard actions
	d.Register("paste_clipboard", handlePasteClipboard)

	// System actions
	d.Register("toggle_logs", handleToggleLogs)
	d.Register("toggle_cache_stats", handleToggleCacheStats)

	// Tape manager actions
	d.Register("toggle_tape_manager", handleToggleTapeManager)
	d.Register("stop_recording", handleStopRecording)

	// Navigation actions (arrow keys)
	d.Register("nav_up", handleUpKey)
	d.Register("nav_down", handleDownKey)
	d.Register("nav_left", handleLeftKey)
	d.Register("nav_right", handleRightKey)

	// Selection extension (shift+arrow keys)
	d.Register("extend_up", handleShiftUpKey)
	d.Register("extend_down", handleShiftDownKey)
	d.Register("extend_left", handleShiftLeftKey)
	d.Register("extend_right", handleShiftRightKey)

	// Restore minimized by index (shift+1-9)
	for i := range 9 {
		d.Register("restore_minimized_"+string(rune('1'+i)), makeRestoreMinimizedHandler(i))
	}
}

// Register adds an action handler
func (d *ActionDispatcher) Register(action string, handler ActionHandler) {
	d.handlers[action] = handler
}

// Dispatch executes the handler for a given action
func (d *ActionDispatcher) Dispatch(action string, msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if handler, ok := d.handlers[action]; ok {
		// Record the action if tape recording is active
		if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
			o.TapeRecorder.RecordAction(action)
		}
		return handler(msg, o)
	}
	return o, nil
}

// HasAction checks if an action is registered
func (d *ActionDispatcher) HasAction(action string) bool {
	_, ok := d.handlers[action]
	return ok
}

// Global action dispatcher instance
var globalDispatcher = NewActionDispatcher()

// GetDispatcher returns the global action dispatcher
func GetDispatcher() *ActionDispatcher {
	return globalDispatcher
}

// ============================================================================
// Window Management Action Handlers
// ============================================================================

func handleNewWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.AddWindow("")
	return o, nil
}

func handleCloseWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		o.DeleteWindow(o.FocusedWindow)
	}
	return o, nil
}

func handleRenameWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// If showing cache stats, reset them instead
	if o.ShowCacheStats {
		app.GetGlobalStyleCache().ResetStats()
		o.ShowNotification("Cache statistics reset", "info", 2*time.Second)
		return o, nil
	}

	// Don't allow rename if window titles are hidden
	if config.WindowTitlePosition == "hidden" {
		return o, nil
	}

	// Otherwise, rename window
	if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.RenamingWindow = true
			o.RenameBuffer = focusedWindow.CustomName
		}
	}
	return o, nil
}

func handleMinimizeWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && !focusedWindow.Minimized {
			o.MinimizeWindow(o.FocusedWindow)
		}
	}
	return o, nil
}

func handleRestoreAll(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Restore all minimized windows in current workspace
	for i := range o.Windows {
		if o.Windows[i].Minimized && o.Windows[i].Workspace == o.CurrentWorkspace {
			o.RestoreWindow(i)
		}
	}
	// Retile if in tiling mode
	if o.AutoTiling {
		o.TileAllWindows()
	}
	return o, nil
}

func handleNextWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CycleToNextVisibleWindow()
	return o, nil
}

func handlePrevWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CycleToPreviousVisibleWindow()
	return o, nil
}

// makeSelectWindowHandler creates a handler for selecting a window by index
func makeSelectWindowHandler(_ int) ActionHandler {
	return func(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return handleNumberKey(msg, o)
	}
}

// ============================================================================
// Workspace Action Handlers
// ============================================================================

func makeSwitchWorkspaceHandler(workspace int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.SwitchToWorkspace(workspace)
		return o, nil
	}
}

func makeMoveAndFollowHandler(workspace int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
			o.MoveWindowToWorkspaceAndFollow(o.FocusedWindow, workspace)
		}
		return o, nil
	}
}

// ============================================================================
// Layout Action Handlers
// ============================================================================

func handleSnapLeft(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		o.Snap(o.FocusedWindow, app.SnapLeft)
	}
	return o, nil
}

func handleSnapRight(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		o.Snap(o.FocusedWindow, app.SnapRight)
	}
	return o, nil
}

func handleSnapFullscreen(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		o.Snap(o.FocusedWindow, app.SnapFullScreen)
	}
	return o, nil
}

func handleUnsnap(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		o.Snap(o.FocusedWindow, app.Unsnap)
	}
	return o, nil
}

func makeSnapCornerHandler(corner app.SnapQuarter) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.Snap(o.FocusedWindow, corner)
		}
		return o, nil
	}
}

func handleToggleTiling(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.AutoTiling = !o.AutoTiling
	if o.AutoTiling {
		o.TileAllWindows()
		o.ShowNotification("Tiling Mode Enabled [T]", "success", config.NotificationDuration)
	} else {
		o.ShowNotification("Tiling Mode Disabled", "info", config.NotificationDuration)
	}
	return o, nil
}

func handleSwapLeft(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.FocusedWindow >= 0 {
		o.SwapWindowLeft()
	}
	return o, nil
}

func handleSwapRight(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.FocusedWindow >= 0 {
		o.SwapWindowRight()
	}
	return o, nil
}

func handleSwapUp(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.FocusedWindow >= 0 {
		o.SwapWindowUp()
	}
	return o, nil
}

func handleSwapDown(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.FocusedWindow >= 0 {
		o.SwapWindowDown()
	}
	return o, nil
}

func handleResizeMasterShrink(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowWidth(-4) // Shrink by 4 columns (split-line based)
	}
	return o, nil
}

func handleResizeMasterGrow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowWidth(4) // Grow by 4 columns (split-line based)
	}
	return o, nil
}

func handleResizeHeightShrink(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowHeight(-2) // Shrink by 2 rows (faster)
	}
	return o, nil
}

func handleResizeHeightGrow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowHeight(2) // Grow by 2 rows (faster)
	}
	return o, nil
}

func handleResizeMasterShrinkLeft(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowWidthLeft(4) // Shrink from left by 4 columns
	}
	return o, nil
}

func handleResizeMasterGrowLeft(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowWidthLeft(-4) // Grow from left by 4 columns (negative shrinks left edge)
	}
	return o, nil
}

func handleResizeHeightShrinkTop(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowHeightTop(2) // Shrink from top by 2 rows
	}
	return o, nil
}

func handleResizeHeightGrowTop(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowHeightTop(-2) // Grow from top by 2 rows (negative shrinks top edge)
	}
	return o, nil
}

// ============================================================================
// BSP Tiling Action Handlers
// ============================================================================

func handleSplitHorizontal(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SplitFocusedHorizontal()
		o.ShowNotification("Split Horizontal", "info", config.NotificationDuration)
	}
	return o, nil
}

func handleSplitVertical(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SplitFocusedVertical()
		o.ShowNotification("Split Vertical", "info", config.NotificationDuration)
	}
	return o, nil
}

func handleRotateSplit(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.RotateFocusedSplit()
		o.ShowNotification("Split Rotated", "info", config.NotificationDuration)
	}
	return o, nil
}

func handleEqualizeSplits(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.EqualizeSplits()
		o.ShowNotification("Splits Equalized", "info", config.NotificationDuration)
	}
	return o, nil
}

func handlePreselectLeft(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SetPreselection(layout.PreselectionLeft)
	}
	return o, nil
}

func handlePreselectRight(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SetPreselection(layout.PreselectionRight)
	}
	return o, nil
}

func handlePreselectUp(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SetPreselection(layout.PreselectionUp)
	}
	return o, nil
}

func handlePreselectDown(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SetPreselection(layout.PreselectionDown)
	}
	return o, nil
}

// ============================================================================
// Mode Control Action Handlers
// ============================================================================

func handleEnterTerminalMode(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.LogInfo("Entering terminal mode for window: %s", focusedWindow.Title)
		}
		o.ShowNotification("Terminal Mode", "info", config.NotificationDuration)
		// Clear selection state when entering terminal mode
		if focusedWindow != nil {
			focusedWindow.SelectedText = ""
			focusedWindow.IsSelecting = false
			focusedWindow.InvalidateCache()
		}
		// Enter terminal mode and start raw input reader
		return o, o.EnterTerminalMode()
	}
	o.LogWarn("Cannot enter terminal mode: no focused window")
	return o, nil
}

func handleEnterWindowMode(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.LogInfo("Entering window management mode")
	// Exit terminal mode to window management mode
	cmd := o.ExitTerminalMode()
	o.ShowNotification("Window Management Mode", "info", config.NotificationDuration)
	if focusedWindow := o.GetFocusedWindow(); focusedWindow != nil {
		focusedWindow.InvalidateCache()
	}
	return o, cmd
}

func handleToggleHelp(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ShowHelp = !o.ShowHelp
	if o.ShowHelp {
		o.HelpScrollOffset = 0 // Reset scroll when opening
	}
	return o, nil
}

func handleQuit(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Close help if showing
	if o.ShowHelp {
		o.ShowHelp = false
		return o, nil
	}
	// Exit selection mode if active
	if o.SelectionMode {
		o.SelectionMode = false
		o.ShowNotification("Selection Mode Exited", "info", config.NotificationDuration)
		if focusedWindow := o.GetFocusedWindow(); focusedWindow != nil {
			focusedWindow.SelectedText = ""
			focusedWindow.IsSelecting = false
			focusedWindow.ScrollbackOffset = 0
			focusedWindow.InvalidateCache()
		}
		return o, nil
	}
	// Show quit confirmation dialog (only if there are terminals with foreground processes)
	if shouldShowQuitDialog(o) {
		o.ShowQuitConfirm = true
		o.QuitConfirmSelection = 0 // Default to Yes
	} else {
		// No foreground processes - quit and kill daemon session
		if o.IsDaemonSession && o.DaemonClient != nil {
			_ = o.DaemonClient.KillSession()
		}
		o.Cleanup()
		return o, tea.Quit
	}
	return o, nil
}

// ============================================================================
// System Action Handlers
// ============================================================================

func handleToggleLogs(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	wasShowing := o.ShowLogs
	o.ShowLogs = !o.ShowLogs
	if o.ShowLogs && !wasShowing {
		// Opening the log viewer - log the message first
		o.LogInfo("Log viewer opened")

		// Then calculate actual max scroll and go to bottom
		maxDisplayHeight := max(o.Height-8, 8)
		totalLogs := len(o.LogMessages)
		fixedLines := 4
		if totalLogs > maxDisplayHeight-fixedLines {
			fixedLines = 6
		}
		logsPerPage := max(maxDisplayHeight-fixedLines, 1)
		maxScroll := max(totalLogs-logsPerPage, 0)
		o.LogScrollOffset = maxScroll
	}
	return o, nil
}

func handleToggleCacheStats(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ShowCacheStats = !o.ShowCacheStats
	if o.ShowCacheStats {
		o.LogInfo("Cache statistics viewer opened")
	}
	return o, nil
}

func handlePasteClipboard(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			// Request clipboard content from Bubbletea
			return o, tea.ReadClipboard
		}
	}
	return o, nil
}

// ============================================================================
// Restore Minimized Window Handlers
// ============================================================================

func makeRestoreMinimizedHandler(index int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.RestoreMinimizedByIndex(index)
		return o, nil
	}
}

// ============================================================================
// Tape Manager Action Handlers
// ============================================================================

func handleToggleTapeManager(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleTapeManager()
	return o, nil
}

func handleStopRecording(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
		o.TapeManagerStopRecording()
	}
	return o, nil
}
