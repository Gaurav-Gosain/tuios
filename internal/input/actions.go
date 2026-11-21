package input

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// ActionHandler is a function that handles a specific action
type ActionHandler func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd)

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
	sidebarActSwitchEdge  = "switch_edge"
	sidebarActAgentFilter = "agents_filter"
	sidebarActAgentSort   = "agents_sort"
	sidebarActMail        = "mail"
	sidebarActNarrow      = "narrow"
	sidebarActWiden       = "widen"
	sidebarActPalette     = "palette"
	sidebarActFileSearch  = "file_search"
	sidebarActNewSession  = "new_session"
	sidebarActNewWindow   = "new_window"
	sidebarActRename      = "rename"
	sidebarActAccent      = "accent"
	sidebarActKill        = "kill"
	sidebarActMenu        = "menu"
	sidebarActHelp        = "help"
	sidebarActExit        = "exit"

	sidebarActFileCreate     = "file_create"
	sidebarActFileRename     = "file_rename"
	sidebarActFileDelete     = "file_delete"
	sidebarActFileDeleteAll  = "file_delete_forever"
	sidebarActFileCopy       = "file_copy"
	sidebarActFileCut        = "file_cut"
	sidebarActFilePaste      = "file_paste"
	sidebarActFileOpen       = "file_open"
	sidebarActFileEdit       = "file_edit"
	sidebarActFileCopyPath   = "file_copy_path"
	sidebarActFileSend       = "file_send"
	sidebarActFileInsertPath = "file_insert_path"
	sidebarActFileQuickLook  = "file_quick_look"
	sidebarActFileOpenSystem = "file_open_system"
	sidebarActJumpPrefix     = "jump_"
)

// ActionDispatcher runs the actions of the action table that the dispatcher
// runs (config.RunDispatch), by name.
type ActionDispatcher struct {
	handlers *config.HandlerTable[ActionHandler]
}

// NewActionDispatcher creates a new action dispatcher with all handlers registered
func NewActionDispatcher() *ActionDispatcher {
	d := &ActionDispatcher{
		handlers: config.NewHandlerTable[ActionHandler](config.RunDispatch),
	}
	d.registerHandlers()
	return d
}

func (d *ActionDispatcher) registerHandlers() {
	d.Register("new_window", handleNewWindow)
	d.Register("selection_copy", handleSelectionCopy)
	d.Register("selection_copy_markdown", handleSelectionCopyMarkdown)
	d.Register("selection_search", handleSelectionSearch)
	d.Register("new_window_ssh", handleNewWindowSSH)
	d.Register("close_window", handleCloseWindow)
	d.Register("rename_window", handleRenameWindow)
	d.Register("set_accent", handleSetAccent)
	d.Register("set_session_accent", handleSetSessionAccent)
	d.Register("minimize_window", handleMinimizeWindow)
	d.Register("restore_all", handleRestoreAll)
	d.Register("next_window", handleNextWindow)
	d.Register("prev_window", handlePrevWindow)
	d.Register("last_pane", handleLastPane)

	for i := 1; i <= 9; i++ {
		idx := i - 1
		d.Register("select_window_"+string(rune('0'+i)), makeSelectWindowHandler(idx))
	}

	for i := 1; i <= 9; i++ {
		d.Register("switch_workspace_"+string(rune('0'+i)), makeSwitchWorkspaceHandler(i))
		d.Register("move_and_follow_"+string(rune('0'+i)), makeMoveAndFollowHandler(i))
	}
	d.Register("next_workspace", handleNextWorkspace)
	d.Register("prev_workspace", handlePrevWorkspace)

	d.Register("snap_left", handleSnapLeft)
	d.Register("snap_right", handleSnapRight)
	d.Register("focus_up", handleTerminalFocusDirection("up"))
	d.Register("focus_down", handleTerminalFocusDirection("down"))
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

	for pct := 10; pct <= 90; pct += 10 {
		d.Register(fmt.Sprintf("resize_width_%d", pct), makeResizeWidthPercentHandler(pct))
		d.Register(fmt.Sprintf("resize_height_%d", pct), makeResizeHeightPercentHandler(pct))
	}

	d.Register("toggle_zoom", handleToggleZoom)
	d.Register("start_screensaver", handleStartScreensaver)

	d.Register("screenshot", handleScreenshotPick)
	d.Register("screenshot_window", handleScreenshotWindow)
	d.Register("screenshot_screen", handleScreenshotScreen)

	d.Register("scroll_focus_left", handleScrollFocusLeft)
	d.Register("scroll_focus_right", handleScrollFocusRight)
	d.Register("scroll_move_left", handleScrollMoveLeft)
	d.Register("scroll_move_right", handleScrollMoveRight)
	d.Register("scroll_cycle_width", handleScrollCycleWidth)
	d.Register("scroll_maximize", handleScrollMaximize)
	d.Register("scroll_consume", handleScrollConsume)
	d.Register("scroll_expel", handleScrollExpel)

	d.Register("smart_split", handleSmartSplit)
	d.Register("split_horizontal", handleSplitHorizontal)
	d.Register("split_vertical", handleSplitVertical)
	d.Register("split_ssh_horizontal", handleSplitSSHHorizontal)
	d.Register("split_ssh_vertical", handleSplitSSHVertical)
	d.Register("rotate_split", handleRotateSplit)
	d.Register("equalize_splits", handleEqualizeSplits)
	d.Register("cycle_tiling_scheme", handleCycleTilingScheme)
	for _, scheme := range config.TilingSchemes {
		d.Register("set_tiling_scheme_"+scheme, makeSetTilingSchemeHandler(scheme))
	}
	d.Register("cycle_master_position", handleCycleMasterPosition)
	for _, pos := range config.MasterPositions {
		d.Register("set_master_position_"+pos, makeSetMasterPositionHandler(pos))
	}
	d.Register("add_master", handleAddMaster)
	d.Register("remove_master", handleRemoveMaster)
	d.Register("swap_with_master", handleSwapWithMaster)
	d.Register("focus_master", handleFocusMaster)
	d.Register("preselect_left", handlePreselectLeft)
	d.Register("preselect_right", handlePreselectRight)
	d.Register("preselect_up", handlePreselectUp)
	d.Register("preselect_down", handlePreselectDown)

	d.Register("enter_terminal_mode", handleEnterTerminalMode)
	d.Register("enter_window_mode", handleEnterWindowMode)
	d.Register("toggle_help", handleToggleHelp)
	d.Register("open_settings", handleOpenSettings)
	d.Register("open_keybind_presets", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.OpenKeybindPresets()
		return o, nil
	})
	d.Register("use_simple_controls", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.SetProfile(config.ProfileSimple)
	})
	d.Register("use_keyboard_controls", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.SetProfile(config.ProfileKeyboard)
	})
	d.Register("quit", handleQuit)

	d.Register("focus_sidebar", handleFocusSidebar)

	d.Register("command_palette", handleOpenCommandPalette)
	d.Register("launcher", handleOpenLauncher)
	d.Register("new_session", handleNewSession)
	d.Register("next_session", handleNextSession)
	d.Register("prev_session", handlePrevSession)
	d.Register("next_host", handleNextHost)
	d.Register("prev_host", handlePrevHost)
	d.Register("next_agent", handleNextAgent)
	d.Register("prev_agent", handlePrevAgent)

	for i := 1; i <= 9; i++ {
		d.Register("switch_session_"+string(rune('0'+i)), makeSwitchSessionHandler(i))
	}

	d.Register("copy_selection", handleCopySelection)
	d.Register("paste_clipboard", handlePasteClipboard)
	d.Register("paste_image", handlePasteImage)
	d.Register("paste_buffer", handlePasteBuffer)
	d.Register("choose_buffer", handleChooseBuffer)
	d.Register("clear_selection", handleClearSelection)

	d.Register("settings_sidebar", handleSettingsSidebar)
	d.Register("rename_session", handleRenameSession)
	d.Register("set_note", handleSetNote)
	d.Register("rename_workspace", handleRenameWorkspace)
	d.Register("kill_session", handleKillSession)
	d.Register("kill_session_next", handleKillSessionNext)
	d.Register("kill_session_quit", handleKillSessionQuit)
	d.Register("restart_daemon", func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.OpenDaemonRestart()
		return o, nil
	})

	d.Register("session_new_worktree", handleSessionNewWorktree)
	d.Register("repo_new_worktree", handleRepoNewWorktree)
	d.Register("new_worktree", handleNewWorktree)
	d.Register("repo_toggle_fold", handleRepoToggleFold)

	for _, action := range []string{
		sidebarActFileCreate, sidebarActFileRename, sidebarActFileDelete,
		sidebarActFileDeleteAll, sidebarActFileCopy, sidebarActFileCut,
		sidebarActFilePaste, sidebarActFileOpen, sidebarActFileEdit,
		sidebarActFileCopyPath, sidebarActFileSend, sidebarActFileInsertPath, sidebarActFileQuickLook,
		sidebarActFileOpenSystem,
	} {
		d.Register(action, makeSidebarFileHandler(action))
	}

	d.Register(sidebarActReorderUp, makeSidebarReorderHandler(-1))
	d.Register(sidebarActReorderDown, makeSidebarReorderHandler(1))

	d.Register("toggle_logs", handleToggleLogs)
	d.Register("toggle_cache_stats", handleToggleCacheStats)
	d.Register("toggle_spotlight", handleToggleSpotlight)
	d.Register("toggle_pip", handleTogglePiP)
	d.Register("focus_rail_pane", handleFocusRailPane)

	d.registerMultifocusHandlers()

	d.Register("toggle_tape_manager", handleToggleTapeManager)
	d.Register("stop_recording", handleStopRecording)
	d.Register(config.ActionLeaveMode, handleLeaveMode)

	for i := range 9 {
		d.Register("restore_minimized_"+string(rune('1'+i)), makeRestoreMinimizedHandler(i))
	}

	d.registerPrefixHandlers()
	d.registerPaletteHandlers()
}

// Register binds handler to action, which must be a RunDispatch entry of
// the action table (see config.HandlerTable.Bind).
//
//go:noinline
func (d *ActionDispatcher) Register(action string, handler ActionHandler) {
	d.handlers.Bind(action, handler)
}

// Dispatch executes the handler for a given action
func (d *ActionDispatcher) Dispatch(action string, msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	action = config.CanonicalAction(action)
	handler, ok := d.handlers.Lookup(action)
	if !ok && strings.HasPrefix(action, config.CommandActionPrefix) {
		handler, ok = handleCommandBinding(action), true
	}
	if !ok && strings.HasPrefix(action, config.OpenMenuPrefix) {
		handler, ok = handleOpenMenu(action), true
	}
	if !ok && strings.HasPrefix(action, config.EnterModePrefix) {
		handler, ok = handleEnterMode(action), true
	}
	if !ok && strings.HasPrefix(action, config.UserActionPrefix) {
		handler, ok = handleUserAction(action), true
	}
	if ok {
		if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
			o.TapeRecorder.RecordAction(action)
		}
		o.NoteAction(action)
		if o.OnAction != nil {
			o.OnAction(action)
		}
		if o.LearnBlocksAction(action) {
			return o, nil
		}
		return handler(msg, o)
	}
	return o, nil
}

// HasAction checks if an action is registered
func (d *ActionDispatcher) HasAction(action string) bool {
	_, ok := d.handlers.Lookup(action)
	return ok || strings.HasPrefix(action, config.CommandActionPrefix) || strings.HasPrefix(action, config.OpenMenuPrefix) ||
		strings.HasPrefix(action, config.EnterModePrefix) || strings.HasPrefix(action, config.UserActionPrefix)
}

func handleUserAction(action string) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		extra := map[string]string{}
		if w := o.GetFocusedWindow(); w != nil {
			if text := selectionText(w); text != "" {
				extra["selection"] = text
			}
		}
		return o, o.RunUserAction(action, extra, nil)
	}
}

func handleCommandBinding(action string) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.RunCommandBinding(action)
	}
}

var globalDispatcher = NewActionDispatcher()

// GetDispatcher returns the global action dispatcher
func GetDispatcher() *ActionDispatcher {
	return globalDispatcher
}

func handleNewWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SidebarSessionFocused() {
		o.NewSidebarSessionWindow()
		return o, nil
	}
	if o.FollowSSHOnNewWindow() {
		o.NewWindowSSH()
		return o, nil
	}
	o.NewWindowHere()
	return o, nil
}

func handleNewWindowSSH(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SidebarSessionFocused() {
		o.NewSidebarSessionWindow()
		return o, nil
	}
	o.NewWindowSSH()
	return o, nil
}

func handleRenameWorkspace(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.BeginRenameWorkspace(o.CurrentWorkspace)
	return o, nil
}

func handleRenameWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.ShowCacheStats {
		app.GetGlobalStyleCache().ResetStats()
		o.ShowNotification("Cache statistics reset", "info", 2*time.Second)
		return o, nil
	}

	o.BeginRenameWindow(o.GetFocusedWindow())
	return o, nil
}

func handleSetAccent(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if w := o.GetFocusedWindow(); w != nil {
		o.OpenAccentPicker(w.ID)
	}
	return o, nil
}

func handleSetNote(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	name := o.TakeMenuSession()
	if name == "" {
		name = o.SessionName
	}
	o.BeginEditNote(name)
	return o, nil
}

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
	for i := range o.Windows {
		if o.Windows[i].Minimized && o.Windows[i].Workspace == o.CurrentWorkspace {
			o.RestoreWindow(i)
		}
	}
	if o.AutoTiling {
		o.TileAllWindows()
	}
	return o, nil
}

func handleNextWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	prev := o.FocusedWindow
	o.CycleToNextVisibleWindow()
	return afterFocusCommand(o, prev, focusEnterCycle)
}

func handlePrevWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	prev := o.FocusedWindow
	o.CycleToPreviousVisibleWindow()
	return afterFocusCommand(o, prev, focusEnterCycle)
}

func handleLastPane(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	prev := o.FocusedWindow
	if !o.LastPane() {
		o.ShowNotification("No pane to go back to", "info", o.Settings.NotificationDuration)
		return o, nil
	}
	return afterFocusCommand(o, prev, focusEnterTargeted)
}

func makeSelectWindowHandler(idx int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		prev := o.FocusedWindow
		selectWindowByIndex(idx+1, o)
		return afterFocusCommand(o, prev, focusEnterTargeted)
	}
}

func makeSwitchWorkspaceHandler(workspace int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.SwitchToWorkspace(workspace)
		return o, nil
	}
}

func makeSwitchSessionHandler(n int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.SwitchToSessionByIndex(n - 1)
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

func handleNextWorkspace(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.NextWorkspace()
	return o, nil
}

func handlePrevWorkspace(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.PrevWorkspace()
	return o, nil
}

func snapOrFocus(o *app.OS, direction string) (*app.OS, tea.Cmd) {
	if len(o.Windows) == 0 || o.FocusedWindow < 0 {
		return o, nil
	}
	prev := o.FocusedWindow
	_ = o.SnapByDirection(direction)
	return afterFocusCommand(o, prev, focusEnterTargeted)
}

func handleSnapLeft(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return snapOrFocus(o, "left")
}

func handleSnapRight(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return snapOrFocus(o, "right")
}

func snapNeedsTilingOff(o *app.OS, what string) {
	o.ShowNotification(what+" needs tiling off", "info", o.Settings.NotificationDuration)
}

func handleSnapFullscreen(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		snapNeedsTilingOff(o, "Fullscreen")
		return o, nil
	}
	if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		o.Snap(o.FocusedWindow, app.SnapFullScreen)
	}
	return o, nil
}

func handleUnsnap(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		snapNeedsTilingOff(o, "Unsnap")
		return o, nil
	}
	if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		o.Snap(o.FocusedWindow, app.Unsnap)
	}
	return o, nil
}

func makeSnapCornerHandler(corner app.SnapQuarter) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		if o.AutoTiling {
			o.ShowNotification("Corner snapping needs tiling off", "info", o.Settings.NotificationDuration)
			return o, nil
		}
		if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.Snap(o.FocusedWindow, corner)
		}
		return o, nil
	}
}

func handleToggleTiling(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleAutoTiling()
	if o.AutoTiling {
		o.ShowNotification("Tiling on [T]", "success", o.Settings.NotificationDuration)
	} else {
		o.ShowNotification("Tiling off", "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handleToggleFloating(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleFloating()
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
		o.ResizeFocusedWindowWidth(-4)
	}
	return o, nil
}

func handleResizeMasterGrow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowWidth(4)
	}
	return o, nil
}

func handleResizeHeightShrink(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowHeight(-2)
	}
	return o, nil
}

func handleResizeHeightGrow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowHeight(2)
	}
	return o, nil
}

func handleResizeMasterShrinkLeft(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowWidthLeft(4)
	}
	return o, nil
}

func handleResizeMasterGrowLeft(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowWidthLeft(-4)
	}
	return o, nil
}

func handleResizeHeightShrinkTop(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowHeightTop(2)
	}
	return o, nil
}

func handleResizeHeightGrowTop(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.ResizeFocusedWindowHeightTop(-2)
	}
	return o, nil
}

func makeResizeWidthPercentHandler(pct int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.SetFocusedWindowWidthPercent(pct)
		return o, nil
	}
}

func makeResizeHeightPercentHandler(pct int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.SetFocusedWindowHeightPercent(pct)
		return o, nil
	}
}

func handleScreenshotPick(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.BeginCapture(false)
	return o, nil
}

func handleScreenshotWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.ScreenshotFocusedWindow()
}

func handleScreenshotScreen(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.ScreenshotScreen()
}

func handleToggleZoom(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleZoom()
	fw := o.GetFocusedWindow()
	if fw != nil && fw.Zoomed {
		o.ShowNotification("ZOOM", "info", o.Settings.NotificationDuration)
	} else {
		o.ClearNotification("ZOOM")
	}
	return o, nil
}

func handleSmartSplit(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SmartSplitFocused()
		o.ShowNotification("Smart split", "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handleSplitHorizontal(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SidebarSessionFocused() {
		o.NewSidebarSessionWindow()
		return o, nil
	}
	if o.FollowSSHOnNewWindow() {
		return handleSplitSSHHorizontal(tea.KeyPressMsg{}, o)
	}
	if o.AutoTiling {
		o.SplitFocusedHorizontal()
		o.ShowNotification("Split horizontal", "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handleSplitVertical(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SidebarSessionFocused() {
		o.NewSidebarSessionWindow()
		return o, nil
	}
	if o.FollowSSHOnNewWindow() {
		return handleSplitSSHVertical(tea.KeyPressMsg{}, o)
	}
	if o.AutoTiling {
		o.SplitFocusedVertical()
		o.ShowNotification("Split vertical", "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handleSplitSSHHorizontal(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SplitFocusedHorizontalSSH()
		o.ShowNotification("Split horizontal", "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handleSplitSSHVertical(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SplitFocusedVerticalSSH()
		o.ShowNotification("Split vertical", "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handleRotateSplit(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.RotateFocusedSplit()
		o.ShowNotification("Split rotated", "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handleEqualizeSplits(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.EqualizeSplits()
		o.ShowNotification("Splits equalized", "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handleCycleTilingScheme(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if scheme := o.CycleTilingScheme(); scheme != "" {
		o.ShowNotification("Tiling scheme: "+app.TilingSchemeLabel(scheme), "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func makeSetTilingSchemeHandler(scheme string) ActionHandler {
	parsed := layout.ParseAutoScheme(scheme)
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		if o.SetTilingScheme(parsed) {
			o.ShowNotification("Tiling scheme: "+app.TilingSchemeLabel(scheme), "info", o.Settings.NotificationDuration)
		}
		return o, nil
	}
}

func handleCycleMasterPosition(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if pos := o.CycleMasterPosition(); pos != "" {
		o.ShowNotification(app.MasterPositionMessage(o, pos), "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func makeSetMasterPositionHandler(pos string) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		if o.SetMasterPosition(pos) {
			o.ShowNotification(app.MasterPositionMessage(o, pos), "info", o.Settings.NotificationDuration)
		}
		return o, nil
	}
}

func handleAddMaster(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if n := o.AddMaster(); n > 0 {
		o.ShowNotification(app.MasterCountMessage(o, n), "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handleRemoveMaster(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if n := o.RemoveMaster(); n > 0 {
		o.ShowNotification(app.MasterCountMessage(o, n), "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handleSwapWithMaster(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.SwapWithMaster()
	return o, nil
}

func handleFocusMaster(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.FocusMaster()
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

type focusEnterKind int

const (
	focusEnterCycle focusEnterKind = iota
	focusEnterTargeted
)

func afterFocusCommand(o *app.OS, previousFocused int, kind focusEnterKind) (*app.OS, tea.Cmd) {
	if o.FocusedWindow != previousFocused {
		o.RevealFocusedColumn()
	}
	return maybeEnterTerminalOnFocusChange(o, previousFocused, kind)
}

func maybeEnterTerminalOnFocusChange(o *app.OS, previousFocused int, kind focusEnterKind) (*app.OS, tea.Cmd) {
	if o.Mode != app.WindowManagementMode {
		return o, nil
	}
	if o.FocusedWindow == previousFocused || o.FocusedWindow < 0 || len(o.Windows) == 0 {
		return o, nil
	}
	switch o.Settings.AutoEnterTerminalOnFocus {
	case config.AutoEnterTerminalAll:
		return enterTerminalModeSilent(o)
	case config.AutoEnterTerminalTargeted:
		if kind != focusEnterTargeted {
			return o, nil
		}
		return enterTerminalModeSilent(o)
	default:
		return o, nil
	}
}

func enterTerminalModeSilent(o *app.OS) (*app.OS, tea.Cmd) {
	if len(o.Windows) == 0 || o.FocusedWindow < 0 {
		return o, nil
	}
	return o, o.EnterTerminalMode()
}

func handleEnterTerminalMode(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.LogInfo("Entering terminal mode for window: %s", focusedWindow.Title())
		}
		o.ShowNotification("Terminal mode", "info", o.Settings.NotificationDuration)
		return o, o.EnterTerminalMode()
	}
	o.LogWarn("Cannot enter terminal mode: no focused window")
	return o, nil
}

func handleEnterWindowMode(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.WindowModeOff() && o.GetFocusedWindow() != nil {
		o.NoteWindowModeOff()
		return o, nil
	}
	o.LogInfo("Entering window management mode")
	cmd := o.ExitTerminalMode()
	o.ShowNotification("Window management mode", "info", o.Settings.NotificationDuration)
	if focusedWindow := o.GetFocusedWindow(); focusedWindow != nil {
		focusedWindow.InvalidateCache()
	}
	return o, cmd
}

func handleToggleHelp(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ShowHelp = !o.ShowHelp
	if o.ShowHelp {
		o.HelpScrollOffset = 0
	}
	return o, nil
}

func handleQuit(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.ShowHelp {
		o.ShowHelp = false
		return o, nil
	}
	return requestQuit(o)
}

func handleToggleSpotlight(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	save := o.ToggleSpotlight()
	o.AnnounceSpotlight()
	return o, save
}

func handleTogglePiP(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.TogglePiP()
	return o, nil
}

func handleFocusRailPane(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if !o.FocusRailPane() {
		o.ShowNotification("The rail shows no pane. Add pane to appearance.sidebar.sections and set [appearance.sidebar.pane] command.",
			"info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handleToggleLogs(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	wasShowing := o.ShowLogs
	if !wasShowing {
		o.LogInfo("Log viewer opened")
	}
	o.ToggleLogViewer()
	return o, nil
}

func handleToggleCacheStats(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ShowCacheStats = !o.ShowCacheStats
	if o.ShowCacheStats {
		o.LogInfo("Cache statistics viewer opened")
	}
	return o, nil
}

func handleCopySelection(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	focusedWindow := o.GetFocusedWindow()
	if focusedWindow == nil {
		return o, nil
	}
	text := selectionText(focusedWindow)
	return o, tea.Batch(o.CopyToClipboard(text), o.SaveToPasteBuffers(text))
}

func handlePasteClipboard(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			return o, o.RequestPaste()
		}
	}
	return o, nil
}

func handlePasteImage(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.RequestImagePaste()
}

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

func handleNextSession(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CycleSession(1)
	return o, nil
}

func handlePrevSession(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CycleSession(-1)
	return o, nil
}

func handleNextHost(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CycleHost(1)
	return o, nil
}

func handlePrevHost(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CycleHost(-1)
	return o, nil
}

func handleNextAgent(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CycleAgent(1)
	return o, nil
}

func handlePrevAgent(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CycleAgent(-1)
	return o, nil
}

func handleOpenSettings(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenSettings()
	return o, nil
}

func handleSettingsSidebar(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenSettingsAt("Sidebar")
	return o, nil
}

func handleKillSession(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenSessionCloseFor(o.TakeMenuSession())
}

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

func handleSessionNewWorktree(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.BeginWorktreePromptForSession(o.TakeMenuSession())
	return o, nil
}

func handleRepoNewWorktree(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.BeginWorktreePromptForRepo(o.TakeMenuRepo())
	return o, nil
}

func handleNewWorktree(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.BeginWorktreePromptForFocusedPane()
	return o, nil
}

func handleRepoToggleFold(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.SidebarToggleRepoCollapsed(o.TakeMenuRepo())
	return o, nil
}

func handleKillSessionQuit(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return quitSession(o)
}

func makeRestoreMinimizedHandler(index int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.RestoreMinimizedByIndex(index)
		return o, nil
	}
}

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

func handleScrollFocusLeft(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	prev := o.FocusedWindow
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingFocusLeft()
	}
	return afterFocusCommand(o, prev, focusEnterTargeted)
}

func handleScrollFocusRight(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	prev := o.FocusedWindow
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingFocusRight()
	}
	return afterFocusCommand(o, prev, focusEnterTargeted)
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

func handleScrollMaximize(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingMaximizeColumn()
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

func handleStartScreensaver(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.StartScreensaverNow()
}

func handleOpenCommandPalette(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenCommandPalette()
}

func handleOpenFileSearch(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenFileSearch(false)
}

func handleOpenLauncher(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenLauncher()
}

func handleNewSession(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenNewSessionPicker()
	return o, nil
}
