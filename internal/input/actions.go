package input

import (
	"fmt"
	"strings"
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

	// The files section's own six. They are resolved before the rail's other
	// bindings while the cursor is on a file row, which is what lets "x" mean
	// cut on a name and kill on a pane without either losing its key. See
	// HandleSidebarKey.
	sidebarActFileCreate    = "file_create"
	sidebarActFileRename    = "file_rename"
	sidebarActFileDelete    = "file_delete"
	sidebarActFileDeleteAll = "file_delete_forever"
	sidebarActFileCopy      = "file_copy"
	sidebarActFileCut       = "file_cut"
	sidebarActFilePaste     = "file_paste"
	// file_open is not one of the six that touch the disk: it takes the row the
	// way a click on it does, so a folder opens and a file's path goes to the
	// clipboard. It exists as an action because the files context menu's first
	// row needs one, and a menu row with no registry action behind it is a
	// hardcoded key hint waiting to go stale.
	sidebarActFileOpen = "file_open"
	sidebarActFileEdit = "file_edit"
	// file_copy_path copies the row's path whatever the row is. file_open
	// copies a file's path too, but on a folder it opens the folder.
	sidebarActFileCopyPath = "file_copy_path"
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
	d.Register("next_workspace", handleNextWorkspace)
	d.Register("prev_workspace", handlePrevWorkspace)

	// Layout actions
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

	// Percentage resizing of the focused pane (issue #29): one action per
	// ten-point step, so a user can bind exactly the percentages they use.
	// resize_width_N sizes the width to N% of the content region,
	// resize_height_N the height to N% of the usable height.
	for pct := 10; pct <= 90; pct += 10 {
		d.Register(fmt.Sprintf("resize_width_%d", pct), makeResizeWidthPercentHandler(pct))
		d.Register(fmt.Sprintf("resize_height_%d", pct), makeResizeHeightPercentHandler(pct))
	}

	// Window actions
	d.Register("toggle_zoom", handleToggleZoom)
	d.Register("start_screensaver", handleStartScreensaver)

	// Screenshot actions. Three, because the three things a person means by
	// "take a screenshot" are different: pick one, take this window, take the
	// lot. All three are reachable from run-command and from a custom keybind.
	d.Register("screenshot", handleScreenshotPick)
	d.Register("screenshot_window", handleScreenshotWindow)
	d.Register("screenshot_screen", handleScreenshotScreen)

	// Scrolling tiling actions (niri-like)
	d.Register("scroll_focus_left", handleScrollFocusLeft)
	d.Register("scroll_focus_right", handleScrollFocusRight)
	d.Register("scroll_move_left", handleScrollMoveLeft)
	d.Register("scroll_move_right", handleScrollMoveRight)
	d.Register("scroll_cycle_width", handleScrollCycleWidth)
	d.Register("scroll_maximize", handleScrollMaximize)
	d.Register("scroll_consume", handleScrollConsume)
	d.Register("scroll_expel", handleScrollExpel)

	// BSP tiling actions
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
	// The palette and the launcher. They are reachable from the global
	// section, from a prefix and from window mode, so they are ordinary
	// dispatcher actions rather than cases of a switch; see global_binds.go.
	d.Register("command_palette", handleOpenCommandPalette)
	d.Register("launcher", handleOpenLauncher)
	d.Register("new_session", handleNewSession)
	d.Register("next_session", handleNextSession)
	d.Register("prev_session", handlePrevSession)

	// Session switching (1-9), by the rail's own order
	for i := 1; i <= 9; i++ {
		d.Register("switch_session_"+string(rune('0'+i)), makeSwitchSessionHandler(i))
	}

	// Clipboard actions
	d.Register("copy_selection", handleCopySelection)
	d.Register("paste_clipboard", handlePasteClipboard)
	d.Register("paste_image", handlePasteImage)
	d.Register("paste_buffer", handlePasteBuffer)
	d.Register("choose_buffer", handleChooseBuffer)
	d.Register("clear_selection", handleClearSelection)

	// Session lifecycle actions (context menu rows; the quit menu's kill rows
	// route through the same OS methods, so the two cannot drift apart)
	d.Register("settings_sidebar", handleSettingsSidebar)
	d.Register("rename_session", handleRenameSession)
	d.Register("rename_workspace", handleRenameWorkspace)
	d.Register("kill_session", handleKillSession)
	d.Register("kill_session_next", handleKillSessionNext)
	d.Register("kill_session_quit", handleKillSessionQuit)

	// The rail's file actions. They are dispatched by key from HandleSidebarKey,
	// which reads its own scope, and they are registered here because the files
	// context menu hands its rows to this dispatcher like every other menu. Both
	// ways in run handleSidebarFileAction, so there is one body per action.
	//
	// Registering them here does not make them reachable from window mode: they
	// live only in [keybindings.sidebar_files], which the window-mode lookup
	// never reads.
	for _, action := range []string{
		sidebarActFileCreate, sidebarActFileRename, sidebarActFileDelete,
		sidebarActFileDeleteAll, sidebarActFileCopy, sidebarActFileCut,
		sidebarActFilePaste, sidebarActFileOpen, sidebarActFileEdit,
		sidebarActFileCopyPath,
	} {
		d.Register(action, makeSidebarFileHandler(action))
	}

	// The rail's two reorder actions. They are dispatched by key from
	// HandleSidebarKey, which reads the rail's own scope, and they are
	// registered here because a machine header's context menu hands its rows to
	// this dispatcher like every other menu. Both ways in call
	// SidebarReorderCursor, so there is one body per direction.
	//
	// Registering them does not put them on a window-mode key: they live in
	// [keybindings.sidebar], which the window-mode lookup never reads. Reached
	// with no rail cursor they move nothing.
	d.Register(sidebarActReorderUp, makeSidebarReorderHandler(-1))
	d.Register(sidebarActReorderDown, makeSidebarReorderHandler(1))

	// System actions
	d.Register("toggle_logs", handleToggleLogs)
	d.Register("toggle_cache_stats", handleToggleCacheStats)
	d.Register("toggle_spotlight", handleToggleSpotlight)
	d.Register("prefix_toggle_spotlight", handleToggleSpotlight)
	d.Register("toggle_pip", handleTogglePiP)

	// Multifocus actions (see multifocus_actions.go)
	d.registerMultifocusHandlers()

	// Tape manager actions
	d.Register("toggle_tape_manager", handleToggleTapeManager)
	d.Register("stop_recording", handleStopRecording)

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
	handler, ok := d.handlers[action]
	// A [[keybindings.command]] entry is an action of its own, named after
	// the entry. It has no handler in the table: the entry says what to run.
	if !ok && strings.HasPrefix(action, config.CommandActionPrefix) {
		handler, ok = handleCommandBinding(action), true
	}
	if ok {
		// Record the action if tape recording is active
		if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
			o.TapeRecorder.RecordAction(action)
		}
		// And record it for a crash report, which needs the same fact and
		// cannot ask the user to have been recording. This is the one choke
		// point every keybinding, prefix chord and context menu row passes
		// through, so one line here covers all three. Names only: see
		// NoteAction.
		o.NoteAction(action)
		// The browser build's guided tour hears every action by name, and
		// in Learn mode an action the demo cannot do shows a note instead.
		if o.OnAction != nil {
			o.OnAction(action)
		}
		if o.LearnBlocksAction(action) {
			return o, nil
		}
		// Inside a scratch group a layout key acts on the group's panes, since
		// the group's workspace is the current one. The few that would take a
		// pane out of the group are refused by the OS methods they call.
		return handler(msg, o)
	}
	return o, nil
}

// HasAction checks if an action is registered
func (d *ActionDispatcher) HasAction(action string) bool {
	_, ok := d.handlers[action]
	return ok || strings.HasPrefix(action, config.CommandActionPrefix)
}

// handleCommandBinding runs the [[keybindings.command]] entry behind action.
func handleCommandBinding(action string) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, o.RunCommandBinding(action)
	}
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
	if o.FollowSSHOnNewWindow() {
		o.NewWindowSSH()
		return o, nil
	}
	o.NewWindowHere()
	return o, nil
}

// handleNewWindowSSH opens a window that runs the focused pane's ssh, or an
// ordinary one when that pane runs none.
func handleNewWindowSSH(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.NewWindowSSH()
	return o, nil
}

func handleCloseWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		w := o.Windows[o.FocusedWindow]
		if w.IsScratch {
			o.CloseWindowByHand(o.FocusedWindow)
			return o, nil
		}
		o.FireHook(hooks.AfterCloseWindow, w.ID, w.Title())
		o.DeleteWindow(o.FocusedWindow)
	}
	return o, nil
}

// handleRenameWorkspace opens the rename editor on the workspace the session
// is showing now.
func handleRenameWorkspace(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.BeginRenameWorkspace(o.CurrentWorkspace)
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

// makeSelectWindowHandler creates a handler for selecting a window by index.
// The index comes from the action name, so the binding can be any key.
func makeSelectWindowHandler(idx int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		prev := o.FocusedWindow
		selectWindowByIndex(idx+1, o)
		return afterFocusCommand(o, prev, focusEnterTargeted)
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

// next_workspace and prev_workspace cycle through the workspaces that hold
// a pane. See OS.NextWorkspace.

func handleNextWorkspace(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.NextWorkspace()
	return o, nil
}

func handlePrevWorkspace(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.PrevWorkspace()
	return o, nil
}

// ============================================================================
// Layout Action Handlers
// ============================================================================

// snapOrFocus is what snap_left and snap_right do. With tiling off the focused
// window snaps to that half of the screen. With tiling on there is nothing to
// snap, and the key moves focus to the neighbour in that direction instead, so
// h and l are not dead keys in the default layout; H and L swap in the same
// directions and alt+h and alt+l preselect there. A step with no neighbour
// that way does nothing, the way a column step at the end of the strip does.
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

// snapNeedsTilingOff says why a snap key did nothing. Fullscreen and unsnap
// have no tiling meaning of their own: zoom is the tiled answer to the first
// and the tiler owns every rectangle, which makes the second a no-op.
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
			// Corner snapping moves a floating window, so with tiling on there
			// is nothing for it to move; saying so keeps the binding honest
			// (the window-mode path and the layout chord both reach this
			// handler now that the layout prefix routes through the dispatcher).
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

// makeResizeWidthPercentHandler returns a handler that sizes the focused
// window's width to pct percent of the content region (issue #29).
func makeResizeWidthPercentHandler(pct int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.SetFocusedWindowWidthPercent(pct)
		return o, nil
	}
}

// makeResizeHeightPercentHandler returns a handler that sizes the focused
// window's height to pct percent of the usable height (issue #29).
func makeResizeHeightPercentHandler(pct int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.SetFocusedWindowHeightPercent(pct)
		return o, nil
	}
}

// ============================================================================
// BSP Tiling Action Handlers
// ============================================================================

// handleScreenshotPick opens capture mode so the user chooses what to take.
func handleScreenshotPick(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.BeginCapture(false)
	return o, nil
}

// handleScreenshotWindow takes the focused window with no further gesture.
func handleScreenshotWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.ScreenshotFocusedWindow()
}

// handleScreenshotScreen takes the whole composed frame.
func handleScreenshotScreen(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.ScreenshotScreen()
}

func handleToggleZoom(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleZoom()
	fw := o.GetFocusedWindow()
	if fw != nil && fw.Zoomed {
		o.ShowNotification("ZOOM", "info", o.Settings.NotificationDuration)
	} else {
		o.ShowNotification("", "info", 0) // clear
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
	if o.FollowSSHOnNewWindow() {
		return handleSplitSSHVertical(tea.KeyPressMsg{}, o)
	}
	if o.AutoTiling {
		o.SplitFocusedVertical()
		o.ShowNotification("Split vertical", "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

// handleSplitSSHHorizontal splits top/bottom and runs the focused pane's ssh
// in the new pane, or a shell when that pane runs no ssh.
func handleSplitSSHHorizontal(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SplitFocusedHorizontalSSH()
		o.ShowNotification("Split horizontal", "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

// handleSplitSSHVertical splits left/right and runs the focused pane's ssh in
// the new pane, or a shell when that pane runs no ssh.
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

// makeSetTilingSchemeHandler returns a handler for set_tiling_scheme_<name>,
// one per config.TilingSchemes entry, so a scheme can be bound directly
// instead of stepped to with cycle_tiling_scheme.
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

// makeSetMasterPositionHandler returns a handler for set_master_position_<side>,
// one per config.MasterPositions entry, so a side can be bound directly
// instead of stepped to with cycle_master_position.
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

// ============================================================================
// Mode Control Action Handlers
// ============================================================================

// focusEnterKind is whether a focus command was walking the pane list or
// picking one pane. targeted follows the mouse bargain (go there and type);
// cycle does not, because Tab has to keep reaching a third pane.
type focusEnterKind int

const (
	focusEnterCycle focusEnterKind = iota
	focusEnterTargeted
)

// afterFocusCommand is the tail of every keyboard command that moves focus:
// next, previous, the four directions, and select-by-number, in window mode
// and behind the prefix alike.
//
// It brings the newly focused column fully on screen in the scrolling layout.
// Focus alone went through the least-scroll rule, which is for a focus that
// moved for a reason the user did not ask for; a key that says "take me to the
// next pane" is the other kind, and it was landing on panes still half off the
// edge. See RevealFocusedColumn.
//
// Nothing happens when focus did not actually move, and nothing happens in any
// layout but the scrolling one.
func afterFocusCommand(o *app.OS, previousFocused int, kind focusEnterKind) (*app.OS, tea.Cmd) {
	if o.FocusedWindow != previousFocused {
		o.RevealFocusedColumn()
	}
	return maybeEnterTerminalOnFocusChange(o, previousFocused, kind)
}

// maybeEnterTerminalOnFocusChange enters terminal mode after a window-focus
// command that actually moved focus, from window-management mode. Hover-focus
// and click-to-type keep their own policies; this is only the keyboard (and
// prefix) focus commands. A no-op that leaves the already-focused pane focused
// does not change mode.
//
// The auto path is silent: a toast per Tab is noise. Explicit enter_terminal_mode
// still notifies.
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
	o.ShowNotification("Window management mode", "info", o.Settings.NotificationDuration)
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

// handleToggleSpotlight turns the beam on and off. It is B in window mode and
// leader, B in either mode; the chord is the way out of the beam in terminal
// mode, where esc belongs to the program in the pane. See
// internal/app/spotlight_exit.go.
func handleToggleSpotlight(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	save := o.ToggleSpotlight()
	o.AnnounceSpotlight()
	return o, save
}

// handleTogglePiP pins the focused pane as the picture-in-picture view, or
// unpins the pinned one. See internal/app/pip.go.
func handleTogglePiP(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.TogglePiP()
	return o, nil
}

func handleToggleLogs(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	wasShowing := o.ShowLogs
	// Log before the toggle: the line itself moves the bottom of the buffer
	// down by one, and the toggle lands the view on that new bottom.
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
	text := selectionText(focusedWindow)
	return o, tea.Batch(o.CopyToClipboard(text), o.SaveToPasteBuffers(text))
}

func handlePasteClipboard(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			// Paste the clipboard's image when it holds only an image, and
			// ask the terminal for its text otherwise. See app.RequestPaste.
			return o, o.RequestPaste()
		}
	}
	return o, nil
}

// handlePasteImage pastes the clipboard's image into the focused pane as the
// path of a file on the pane's machine. See app.RequestImagePaste.
func handlePasteImage(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.RequestImagePaste()
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

// handleKillSessionNext kills the current session after switching this client
// to the next one, in that order (see OS.KillSessionGoNext for why).
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

// handleStartScreensaver covers the screen on request rather than on a timer.
func handleStartScreensaver(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.StartScreensaverNow()
}

// handleOpenCommandPalette opens the command palette.
func handleOpenCommandPalette(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenCommandPalette()
}

func handleOpenFileSearch(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenFileSearch(false)
}

// handleOpenLauncher opens the app launcher.
func handleOpenLauncher(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenLauncher()
}

// handleNewSession makes a session, asking which machine when there is more
// than one and offering a global session alongside them.
func handleNewSession(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenNewSessionPicker()
	return o, nil
}
