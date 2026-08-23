package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// ActionHandler is a function that handles a specific action
type ActionHandler func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd)

// Rail (sidebar keyboard scope) action names. These are the actions in the
// [keybindings] sidebar config section, dispatched by HandleSidebarKey while the
// rail owns the keyboard. Constants keep the routing switch and any future
// callers typo-proof now that there are ~14 of them.
const (
	sidebarActCursorDown  = "cursor_down"
	sidebarActCursorUp    = "cursor_up"
	sidebarActFirst       = "first"
	sidebarActLast        = "last"
	sidebarActExpand      = "expand"
	sidebarActCollapse    = "collapse"
	sidebarActActivate    = "activate"
	sidebarActReorderDown = "reorder_down"
	sidebarActReorderUp   = "reorder_up"
	sidebarActSection     = "section"
	sidebarActAgentFilter = "agents_filter"
	sidebarActAgentSort   = "agents_sort"
	sidebarActNarrow      = "narrow"
	sidebarActWiden       = "widen"
	sidebarActPalette     = "palette"
	sidebarActNewSession  = "new_session"
	sidebarActNewWindow   = "new_window"
	sidebarActRename      = "rename"
	sidebarActAccent      = "accent"
	sidebarActKill        = "kill"
	sidebarActMenu        = "menu"
	sidebarActHelp        = "help"
	sidebarActExit        = "exit"
	// jump_1..jump_9 are matched by prefix; see HandleSidebarKey.
	sidebarActJumpPrefix = "jump_"
)

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
	d.Register("set_accent", handleSetAccent)
	d.Register("set_session_accent", handleSetSessionAccent)
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

	// Window actions
	d.Register("toggle_zoom", handleToggleZoom)

	// Scrolling tiling actions (niri-like)
	d.Register("scroll_focus_left", handleScrollFocusLeft)
	d.Register("scroll_focus_right", handleScrollFocusRight)
	d.Register("scroll_move_left", handleScrollMoveLeft)
	d.Register("scroll_move_right", handleScrollMoveRight)
	d.Register("scroll_cycle_width", handleScrollCycleWidth)
	d.Register("scroll_consume", handleScrollConsume)
	d.Register("scroll_expel", handleScrollExpel)

	// BSP tiling actions
	d.Register("smart_split", handleSmartSplit)
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
	d.Register("open_settings", handleOpenSettings)
	d.Register("quit", handleQuit)

	// Enter the sidebar rail's keyboard scope (window mode "s"). The exit and the
	// per-row keys are not dispatcher actions: they route through HandleSidebarKey
	// only while SidebarFocused, so they cannot fire on a pane.
	d.Register("focus_sidebar", handleFocusSidebar)

	// Session navigation. Bound to chords, and allowed from terminal mode by
	// isTerminalSafeAction, so walking sessions does not first cost an Esc.
	d.Register("next_session", handleNextSession)
	d.Register("prev_session", handlePrevSession)

	// Clipboard actions
	d.Register("copy_selection", handleCopySelection)
	d.Register("paste_clipboard", handlePasteClipboard)
	d.Register("clear_selection", handleClearSelection)

	// Session lifecycle actions (context menu rows; the quit menu's kill rows
	// route through the same OS methods, so the two cannot drift apart)
	d.Register("settings_sidebar", handleSettingsSidebar)
	d.Register("rename_session", handleRenameSession)
	d.Register("kill_session", handleKillSession)
	d.Register("kill_session_next", handleKillSessionNext)
	d.Register("kill_session_quit", handleKillSessionQuit)

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

	// Restore minimized by index (shift+1-9)
	for i := range 9 {
		d.Register("restore_minimized_"+string(rune('1'+i)), makeRestoreMinimizedHandler(i))
	}

	// Prefix-chord and terminal-mode actions (see prefix_actions.go)
	d.registerPrefixHandlers()
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
		w := o.Windows[o.FocusedWindow]
		o.FireHook(hooks.AfterCloseWindow, w.ID, w.Title())
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

	// The editor is a centred dialog, so it carries its own frame and needs
	// neither a title bar nor a rail row to land on.
	o.BeginRenameWindow(o.GetFocusedWindow())
	return o, nil
}

// handleSetAccent opens the accent swatches for the focused window. It backs the
// context menu's "Accent color" row; the rail's own key targets the cursor row
// instead, which need not be the focused pane.
func handleSetAccent(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if w := o.GetFocusedWindow(); w != nil {
		o.OpenAccentPicker(w.ID)
	}
	return o, nil
}

// handleSetSessionAccent opens the same picker on a session: the row's own
// session when the action came from a rail menu, and the attached one when it
// came from a key.
func handleSetSessionAccent(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	name := o.TakeMenuSession()
	if name == "" {
		name = o.SessionName
	}
	o.OpenSessionAccentPicker(name)
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

// makeSelectWindowHandler creates a handler for selecting a window by index.
// The index comes from the action name, so the binding can be any key.
func makeSelectWindowHandler(idx int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		selectWindowByIndex(idx+1, o)
		return o, nil
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
	o.ToggleAutoTiling()
	if o.AutoTiling {
		o.ShowNotification("Tiling on [T]", "success", config.NotificationDuration)
	} else {
		o.ShowNotification("Tiling off", "info", config.NotificationDuration)
	}
	return o, nil
}

func handleSwapLeft(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.FocusedWindow >= 0 {
		if o.UseScrollingLayout {
			o.ScrollingMoveColumnLeft()
		} else {
			o.SwapWindowLeft()
		}
	}
	return o, nil
}

func handleSwapRight(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.FocusedWindow >= 0 {
		if o.UseScrollingLayout {
			o.ScrollingMoveColumnRight()
		} else {
			o.SwapWindowRight()
		}
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

func handleToggleZoom(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleZoom()
	fw := o.GetFocusedWindow()
	if fw != nil && fw.Zoomed {
		o.ShowNotification("ZOOM", "info", config.NotificationDuration)
	} else {
		o.ShowNotification("", "info", 0) // clear
	}
	return o, nil
}

func handleSmartSplit(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SmartSplitFocused()
		o.ShowNotification("Smart split", "info", config.NotificationDuration)
	}
	return o, nil
}

func handleSplitHorizontal(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SplitFocusedHorizontal()
		o.ShowNotification("Split horizontal", "info", config.NotificationDuration)
	}
	return o, nil
}

func handleSplitVertical(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SplitFocusedVertical()
		o.ShowNotification("Split vertical", "info", config.NotificationDuration)
	}
	return o, nil
}

func handleRotateSplit(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.RotateFocusedSplit()
		o.ShowNotification("Split rotated", "info", config.NotificationDuration)
	}
	return o, nil
}

func handleEqualizeSplits(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.EqualizeSplits()
		o.ShowNotification("Splits equalized", "info", config.NotificationDuration)
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
			o.LogInfo("Entering terminal mode for window: %s", focusedWindow.Title())
		}
		o.ShowNotification("Terminal mode", "info", config.NotificationDuration)
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
	o.ShowNotification("Window management mode", "info", config.NotificationDuration)
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
	return requestQuit(o)
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

		// Scroll to bottom to show most recent entries
		_, maxScroll := logScrollBounds(o.Height, len(o.LogMessages))
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

// handleCopySelection copies the focused pane's selection to the system
// clipboard.
//
// The text is derived from the selection now rather than read from a field
// filled in earlier, so it is whatever is highlighted on screen at the moment
// the user asks for it. It went the other way once, off Window.SelectedText,
// and that field belonged to a selection system the mouse stopped using: a
// drag-selected pane offered a copy that silently did nothing.
//
// The write goes through CopyToClipboard, which is also what a drag release
// uses, so a copy asked for by menu or key and a copy-on-select land the same
// way and say the same thing.
func handleCopySelection(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	focusedWindow := o.GetFocusedWindow()
	if focusedWindow == nil {
		return o, nil
	}
	return o, o.CopyToClipboard(selectionText(focusedWindow))
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

// handleClearSelection drops the focused window's text selection without
// copying it. Offered on the terminal-mode selection menu, where the selection
// is the only reason the menu opened at all.
func handleClearSelection(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	w := o.GetFocusedWindow()
	if w == nil {
		return o, nil
	}
	if w.InCopyMode() {
		w.ExitCopyMode()
	}
	w.InvalidateCache()
	return o, nil
}

// handleNextSession and handlePrevSession walk the sessions in the rail's order.
func handleNextSession(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CycleSession(1)
	return o, nil
}

func handlePrevSession(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CycleSession(-1)
	return o, nil
}

// handleKillSessionNext kills the current session after switching this client
// to the next one, in that order (see OS.KillSessionGoNext for why).
// handleOpenSettings opens the settings overlay. Bound to "," in mode_control,
// which the default layout binds too; mode_control is consulted after layout, so
// settings wins and `keybinds doctor` names the resize bind it shadows.
func handleOpenSettings(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenSettings()
	return o, nil
}

// handleSettingsSidebar opens the settings overlay on its Sidebar tab, so the
// rail's context menu lands on the rows it is about.
func handleSettingsSidebar(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenSettingsAt("Sidebar")
	return o, nil
}

// handleKillSession kills the session whose row the menu was opened on, after
// the confirmation names it. Reached by key rather than from a menu it means
// the attached session, which is the only session a key can be about.
//
// The other two kill rows say what becomes of this client and so can only mean
// the session it is in; this one is the row every other session's menu carries.
func handleKillSession(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenSessionCloseFor(o.TakeMenuSession())
	return o, nil
}

// handleRenameSession opens the rename editor on the session whose row the menu
// was opened on, the same carry the accent and kill rows use. Reached by key it
// means the attached session, because a key names no row.
func handleRenameSession(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	name := o.TakeMenuSession()
	if name == "" {
		name = o.SessionName
	}
	o.BeginRenameSession(name)
	return o, nil
}

func handleKillSessionNext(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.KillSessionGoNext(o.NextSessionName())
}

// handleKillSessionQuit kills the current session and quits this client, the
// same call the quit menu's kill-and-quit row makes.
func handleKillSessionQuit(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return quitSession(o)
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

// ============================================================================
// Scrolling Tiling Action Handlers (niri-like)
// ============================================================================

func handleScrollFocusLeft(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingFocusLeft()
	}
	return o, nil
}

func handleScrollFocusRight(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingFocusRight()
	}
	return o, nil
}

func handleScrollMoveLeft(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingMoveColumnLeft()
	}
	return o, nil
}

func handleScrollMoveRight(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingMoveColumnRight()
	}
	return o, nil
}

func handleScrollCycleWidth(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingCycleWidth()
	}
	return o, nil
}

func handleScrollConsume(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingConsumeWindow()
	}
	return o, nil
}

func handleScrollExpel(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingExpelWindow()
	}
	return o, nil
}
