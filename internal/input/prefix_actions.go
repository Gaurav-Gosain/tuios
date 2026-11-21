package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

func (d *ActionDispatcher) registerPrefixHandlers() {
	d.Register("prefix_cancel", handlePrefixCancel)
	d.Register("prefix_rename_window", handlePrefixRenameWindow)
	d.Register("prefix_keybinds", handlePrefixKeybinds)
	d.Register("prefix_next_window", handlePrefixNextWindow)
	d.Register("prefix_prev_window", handlePrefixPrevWindow)
	for i := range 10 {
		d.Register("prefix_select_"+string(rune('0'+i)), makePrefixSelectHandler(i))
	}
	d.Register("prefix_toggle_tiling", handlePrefixToggleTiling)
	d.Register("prefix_fullscreen", handlePrefixFullscreen)
	d.Register("prefix_selection", handlePrefixSelection)
	d.Register("prefix_scrollback", handlePrefixScrollback)
	d.Register("prefix_screenshot", handlePrefixScreenshot)
	d.Register("prefix_file_search", handleOpenFileSearch)
	d.Register("prefix_toggle_sidebar", handlePrefixToggleSidebar)
	d.Register("prefix_toggle_other_rail", handlePrefixToggleOtherSidebar)
	d.Register("prefix_session_switcher", handlePrefixSessionSwitcher)
	d.Register("prefix_new_task", handlePrefixNewTask)
	d.Register("prefix_workspace_switcher", handlePrefixWorkspaceSwitcher)
	d.Register("choose_tree", handleChooseTree)
	d.Register("goto_picker", handleGotoPicker)
	d.Register("prefix_explore", handleToggleFocusSidebar)
	d.Register("prefix_jump_notif", handlePrefixJumpNotif)
	d.Register("prefix_last_message", handlePrefixLastMessage)
	d.Register("prefix_mail", handlePrefixMail)
	d.Register("prefix_inbox", handlePrefixInbox)
	d.Register("new_agent", handleNewAgent)
	d.Register("prefix_transfers", handlePrefixTransfers)
	d.Register("prefix_next_attention", handlePrefixNextAttention)
	d.Register("prefix_review", handlePrefixReview)
	d.Register("prefix_next_finished", handlePrefixNextFinished)
	d.Register("prefix_agents_settings", handlePrefixAgentsSettings)
	d.Register("prefix_detach", handlePrefixDetach)
	d.Register("prefix_close_session", handlePrefixCloseSession)
	d.Register("prefix_exit_mode", handlePrefixExitMode)
	d.Register("prefix_quit", handlePrefixQuit)
	d.Register("hints", handleOpenHints)
	d.Register("toggle_scratch", handleToggleScratch)
	d.Register("toggle_calm", handleToggleCalm)
	d.Register(config.ActionEditScrollback, handleEditScrollback)
	d.Register(config.ActionClearScrollback, handleClearScrollback)
	d.Register("hints_all_panes", handleOpenHintsAllPanes)
	d.Register("display_panes", handleDisplayPanes)
	d.Register("pick_path", handleOpenPathPicker)
	d.Register(config.ActionCopyModeSearchForward, handleCopyModeSearchForward)
	d.Register(config.ActionCopyModeSearchBackward, handleCopyModeSearchBackward)

	d.Register("prefix_workspace", makeSubPrefixHandler(app.SubMenuWorkspace))
	d.Register("prefix_minimize", makeSubPrefixHandler(app.SubMenuMinimize))
	d.Register("prefix_window", makeSubPrefixHandler(app.SubMenuTiling))
	d.Register("prefix_debug", makeSubPrefixHandler(app.SubMenuDebug))
	d.Register("prefix_tape", makeSubPrefixHandler(app.SubMenuTape))
	d.Register("prefix_layout", makeSubPrefixHandler(app.SubMenuLayout))
	d.Register("prefix_tuios", makeSubPrefixHandler(app.SubMenuTuios))

	d.Register("window_prefix_rename", handleWindowPrefixRename)
	d.Register("window_prefix_float", handleToggleFloating)
	d.registerPrefixTreeHandlers()

	d.Register("minimize_prefix_focused", handleMinimizeFocused)
	for i := 1; i <= 9; i++ {
		d.Register("minimize_prefix_restore_"+string(rune('0'+i)), makeRestoreMinimizedByPositionHandler(i))
	}
	d.Register("minimize_prefix_focus_only", handleFocusOnly)

	for i := 1; i <= 9; i++ {
		d.Register("workspace_prefix_switch_"+string(rune('0'+i)), makeSwitchWorkspaceHandler(i))
		d.Register("workspace_prefix_move_"+string(rune('0'+i)), makeMoveAndFollowHandler(i))
	}
	d.Register("workspace_prefix_rename", handleWorkspaceRename)
	d.Register("workspace_pill_switch", handleWorkspacePillSwitch)

	d.Register("debug_prefix_logs", handleDebugLogs)
	d.Register("debug_prefix_cache", handleDebugCache)
	d.Register("debug_prefix_showkeys", handleDebugShowkeys)
	d.Register("debug_prefix_animations", handleDebugAnimations)

	d.Register("tape_prefix_review", handleTapeReview)
	d.Register("tape_prefix_record", handleTapeRecord)
	d.Register("tape_prefix_stop", handleTapeStop)

	d.Register("layout_prefix_load", handleLayoutPrefixLoad)
	d.Register("layout_prefix_save", handleLayoutPrefixSave)

	d.Register("terminal_next_window", handleTerminalNextWindow)
	d.Register("terminal_prev_window", handleTerminalPrevWindow)
	d.Register("terminal_exit_mode", handleTerminalExitMode)
	d.Register("terminal_focus_left", handleTerminalFocusDirection("left"))
	d.Register("terminal_focus_right", handleTerminalFocusDirection("right"))
}

func handleTerminalFocusDirection(dir string) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		prev := o.FocusTerminalDirection(dir)
		return afterFocusCommand(o, prev, focusEnterTargeted)
	}
}

func refreshFocusedWindow(o *app.OS) {
	if focused := o.GetFocusedWindow(); focused != nil {
		focused.InvalidateCache()
	}
}

func makeSubPrefixHandler(sub app.SubMenu) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.OpenSubMenu(sub)
		return o, nil
	}
}

func handlePrefixCancel(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, nil
}

func handleCloseWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SidebarSessionFocused() {
		if w := o.SidebarSessionWindow(); w != nil {
			o.SidebarSessionIntent("CloseWindow", w.ID)
		}
		return o, nil
	}
	if len(o.Windows) == 0 || o.FocusedWindow < 0 {
		return o, nil
	}
	w := o.Windows[o.FocusedWindow]
	if w.IsScratch {
		o.CloseWindowByHand(o.FocusedWindow)
		return o, nil
	}
	if o.CloseNeedsConfirm(o.FocusedWindow) {
		o.OpenSessionCloseForWindow(o.FocusedWindow)
		return o, nil
	}
	o.FireHook(hooks.AfterCloseWindow, w.ID, w.Title())
	o.DeleteWindow(o.FocusedWindow)
	if len(o.Windows) > 0 {
		refreshFocusedWindow(o)
	} else if o.Mode == app.TerminalMode {
		o.ChooseMode(app.WindowManagementMode, app.ModeCauseNoPane)
	}
	return o, nil
}

func startRename(o *app.OS) {
	if len(o.Windows) == 0 || o.FocusedWindow < 0 {
		return
	}
	focused := o.GetFocusedWindow()
	if focused == nil {
		return
	}
	o.ChooseMode(app.WindowManagementMode, app.ModeCauseAction)
	o.BeginRenameWindow(focused)
}

func handlePrefixRenameWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	startRename(o)
	return o, nil
}

func handleWindowPrefixRename(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.ShowCacheStats {
		app.GetGlobalStyleCache().ResetStats()
		o.ShowNotification("Cache statistics reset", "info", 2*time.Second)
		return o, nil
	}
	return handlePrefixRenameWindow(msg, o)
}

func handleWorkspaceRename(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ChooseMode(app.WindowManagementMode, app.ModeCauseAction)
	o.BeginRenameCurrentWorkspace()
	return o, nil
}

func handleWorkspacePillSwitch(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if ws := o.TakeMenuWorkspace(); ws > 0 {
		o.SwitchToWorkspace(ws)
	}
	return o, nil
}

func handlePrefixKeybinds(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenKeybindManager()
	return o, nil
}

func handlePrefixNextWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SidebarSessionFocused() {
		o.SidebarSessionIntent("NextWindow")
		return o, nil
	}
	prev := o.FocusedWindow
	if len(o.Windows) > 0 {
		o.CycleToNextVisibleWindow()
		refreshFocusedWindow(o)
	}
	return afterFocusCommand(o, prev, focusEnterCycle)
}

func handlePrefixPrevWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SidebarSessionFocused() {
		o.SidebarSessionIntent("PrevWindow")
		return o, nil
	}
	prev := o.FocusedWindow
	if len(o.Windows) > 0 {
		o.CycleToPreviousVisibleWindow()
		refreshFocusedWindow(o)
	}
	return afterFocusCommand(o, prev, focusEnterCycle)
}

func makePrefixSelectHandler(num int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		prev := o.FocusedWindow
		position := 0
		for i, win := range o.Windows {
			if win.Workspace != o.CurrentWorkspace {
				continue
			}
			if o.AutoTiling && win.Minimized {
				continue
			}
			position++
			if position == num || (num == 0 && position == 10) {
				o.FocusWindow(i)
				break
			}
		}
		refreshFocusedWindow(o)
		return afterFocusCommand(o, prev, focusEnterTargeted)
	}
}

func handlePrefixToggleTiling(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleAutoTiling()
	return o, nil
}

func handlePrefixFullscreen(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if len(o.Windows) == 0 || o.FocusedWindow < 0 {
		return o, nil
	}
	o.ToggleZoom()
	if fw := o.GetFocusedWindow(); fw != nil && fw.Zoomed {
		o.ShowNotification("ZOOM", "info", o.Settings.NotificationDuration)
	} else {
		o.ClearNotification("ZOOM")
	}
	return o, nil
}

func handlePrefixSelection(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.ShowInbox && o.CockpitCopyStart() {
		return o, nil
	}
	o.EnterCopyModeFocused()
	return o, nil
}

func handlePrefixScrollback(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	OpenScrollbackBrowser(o)
	return o, nil
}

func handleClearScrollback(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.ClearScrollback()
}

func handleEditScrollback(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.EditScrollbackInEditor()
}

func handlePrefixScreenshot(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.BeginCapture(false)
	return o, nil
}

func handlePrefixToggleSidebar(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleSidebar()
	state := "off"
	if o.Settings.SidebarEnabled {
		state = "on"
	}
	o.ShowNotification("Sidebar "+state, "success", o.Settings.NotificationDuration)
	return o, nil
}

func handlePrefixToggleOtherSidebar(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	state := "off"
	if o.ToggleOtherSidebar() {
		state = "on"
	}
	o.ShowNotification("Opposite sidebar "+state, "success", o.Settings.NotificationDuration)
	return o, nil
}

func handlePrefixJumpNotif(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if !o.JumpToNotification() {
		o.ShowNotification("No message to jump to", "info", o.Settings.NotificationDuration)
	}
	return o, o.TakeSidebarCmd()
}

func handlePrefixLastMessage(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if !o.OpenLastMessage() {
		o.ShowNotification("There is no message to show", "info", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handlePrefixMail(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenInbox(session.AttentionMail)
	return o, nil
}

func handlePrefixInbox(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenInbox("")
	return o, nil
}

func handleNewAgent(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenInbox("")
	if !o.ShowInbox {
		return o, nil
	}
	return o, o.CockpitNewAgent()
}

func handlePrefixAgentsSettings(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenAgentsSettings()
	return o, nil
}

func handlePrefixNextAttention(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.JumpToNextAttention()
}

func handlePrefixReview(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	cmd, _ := o.ReviewFocusedPane()
	return o, cmd
}

func handlePrefixNextFinished(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	cmd, _ := o.JumpToNewestFinished()
	return o, cmd
}

func handlePrefixSessionSwitcher(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenSessionSwitcher()
	return o, nil
}

func handlePrefixNewTask(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenTaskCapture()
}

func handlePrefixWorkspaceSwitcher(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenWorkspaceSwitcher()
	return o, nil
}

func leaveTerminalMode(o *app.OS) {
	if o.Mode != app.TerminalMode {
		return
	}
	if o.WindowModeOff() {
		o.NoteWindowModeOff()
		return
	}
	o.ChooseMode(app.WindowManagementMode, app.ModeCauseKey)
	o.ShowNotification("Window management mode", "info", o.Settings.NotificationDuration)
	refreshFocusedWindow(o)
}

func handlePrefixDetach(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if m, cmd, detached := detachSession(o); detached {
		return m, cmd
	}
	leaveTerminalMode(o)
	return o, nil
}

func handlePrefixCloseSession(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenSessionClose()
}

func handlePrefixExitMode(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	leaveTerminalMode(o)
	return o, nil
}

func handlePrefixQuit(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return requestQuit(o)
}

func handleMinimizeFocused(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		o.MinimizeWindow(o.FocusedWindow)
	}
	return o, nil
}

func handleFocusOnly(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleFocusOnly()
	return o, nil
}

func makeRestoreMinimizedByPositionHandler(position int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		minimized := o.MinimizedInOrder()
		if position < 1 || position > len(minimized) {
			return o, nil
		}
		o.RestoreWindow(minimized[position-1])
		if o.AutoTiling {
			o.TileAllWindows()
		}
		return o, nil
	}
}

func toggleNotify(o *app.OS, label string, on bool) {
	state := "OFF"
	if on {
		state = "ON"
	}
	o.ShowNotification(label+": "+state, "info", o.Settings.NotificationDuration)
}

func handleDebugLogs(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleLogViewer()
	toggleNotify(o, "Log viewer", o.ShowLogs)
	return o, nil
}

func handleDebugCache(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ShowCacheStats = !o.ShowCacheStats
	toggleNotify(o, "Cache stats", o.ShowCacheStats)
	return o, nil
}

func handleDebugShowkeys(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	save := o.ToggleShowKeys()
	toggleNotify(o, "Showkeys", o.ShowKeys)
	return o, save
}

func handleDebugAnimations(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	level := config.MotionFull
	if o.Settings.AnimationsOn() {
		level = config.MotionNone
	}
	_ = o.SetMotion(level)
	toggleNotify(o, "Animations", o.Settings.AnimationsOn())
	return o, nil
}

func handleTapeReview(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenTapeReview()
	return o, nil
}

func handleTapeRecord(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
		o.ShowNotification("Already recording", "warning", o.Settings.NotificationDuration)
		return o, nil
	}
	o.TapeManagerStartRecording()
	o.ShowTapeManager = true
	return o, nil
}

func handleTapeStop(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
		o.TapeManagerStopRecording()
	} else {
		o.ShowNotification("Not recording", "warning", o.Settings.NotificationDuration)
	}
	return o, nil
}

func handleTerminalNextWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	prev := o.FocusedWindow
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingFocusRight()
	} else {
		o.CycleToNextVisibleWindow()
	}
	refreshFocusedWindow(o)
	return afterFocusCommand(o, prev, focusEnterCycle)
}

func handleTerminalPrevWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	prev := o.FocusedWindow
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingFocusLeft()
	} else {
		o.CycleToPreviousVisibleWindow()
	}
	refreshFocusedWindow(o)
	return afterFocusCommand(o, prev, focusEnterCycle)
}

func handleTerminalExitMode(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	leaveTerminalMode(o)
	return o, nil
}

func handleToggleScratch(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.ToggleScratch()
}

func handleToggleCalm(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.ToggleCalm()
}
