package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// The prefix chords used to be two hand-written switch statements over literal
// key strings, one for terminal mode and one for window-management mode, which
// meant the six prefix sections of config.toml (prefix_mode, window_prefix,
// minimize_prefix, workspace_prefix, debug_prefix, tape_prefix) and the
// terminal_mode section were parsed, validated, written into every user's
// config, and then never consulted. Rebinding a prefix key did nothing.
//
// Everything below is registered in the same dispatcher as the
// window-management actions and reached through the registry lookups, so a
// rebind in config.toml is the binding. The handlers cover both modes; where
// the two switches used to differ (cache invalidation, leaving terminal mode
// when the last window closes) the handler branches on o.Mode instead of
// existing twice.

// registerPrefixHandlers registers the handlers for every action reachable
// through a prefix chord.
func (d *ActionDispatcher) registerPrefixHandlers() {
	// Main prefix (leader, ...)
	d.Register("prefix_new_window", handlePrefixNewWindow)
	d.Register("prefix_close_window", handlePrefixCloseWindow)
	d.Register("prefix_rename_window", handlePrefixRenameWindow)
	d.Register("prefix_settings", handlePrefixSettings)
	d.Register("prefix_next_window", handlePrefixNextWindow)
	d.Register("prefix_prev_window", handlePrefixPrevWindow)
	for i := range 10 {
		d.Register("prefix_select_"+string(rune('0'+i)), makePrefixSelectHandler(i))
	}
	d.Register("prefix_toggle_tiling", handlePrefixToggleTiling)
	d.Register("prefix_fullscreen", handlePrefixFullscreen)
	d.Register("prefix_split_horizontal", handlePrefixSplitHorizontal)
	d.Register("prefix_split_vertical", handlePrefixSplitVertical)
	d.Register("prefix_rotate_split", handlePrefixRotateSplit)
	d.Register("prefix_equalize_splits", handlePrefixEqualizeSplits)
	d.Register("prefix_selection", handlePrefixSelection)
	d.Register("prefix_scrollback", handlePrefixScrollback)
	d.Register("prefix_help", handlePrefixHelp)
	d.Register("prefix_command_palette", handlePrefixCommandPalette)
	d.Register("prefix_session_switcher", handlePrefixSessionSwitcher)
	d.Register("prefix_detach", handlePrefixDetach)
	d.Register("prefix_exit_mode", handlePrefixExitMode)
	d.Register("prefix_quit", handlePrefixQuit)

	// Sub-prefixes: each keeps the prefix active so the which-key overlay stays
	// up for the second key.
	d.Register("prefix_workspace", makeSubPrefixHandler(func(o *app.OS) { o.WorkspacePrefixActive = true }))
	d.Register("prefix_minimize", makeSubPrefixHandler(func(o *app.OS) { o.MinimizePrefixActive = true }))
	d.Register("prefix_window", makeSubPrefixHandler(func(o *app.OS) { o.TilingPrefixActive = true }))
	d.Register("prefix_debug", makeSubPrefixHandler(func(o *app.OS) { o.DebugPrefixActive = true }))
	d.Register("prefix_tape", makeSubPrefixHandler(func(o *app.OS) { o.TapePrefixActive = true }))
	d.Register("prefix_layout", makeSubPrefixHandler(func(o *app.OS) { o.LayoutPrefixActive = true }))

	// Window prefix (leader, t, ...)
	d.Register("window_prefix_new", handlePrefixNewWindow)
	d.Register("window_prefix_close", handlePrefixCloseWindow)
	d.Register("window_prefix_rename", handleWindowPrefixRename)
	d.Register("window_prefix_next", handlePrefixNextWindow)
	d.Register("window_prefix_prev", handlePrefixPrevWindow)
	d.Register("window_prefix_tiling", handleToggleTiling)
	d.Register("window_prefix_cancel", handlePrefixCancel)

	// Minimize prefix (leader, m, ...)
	d.Register("minimize_prefix_focused", handleMinimizeFocused)
	for i := 1; i <= 9; i++ {
		d.Register("minimize_prefix_restore_"+string(rune('0'+i)), makeRestoreMinimizedByPositionHandler(i))
	}
	d.Register("minimize_prefix_restore_all", handleRestoreAll)
	d.Register("minimize_prefix_cancel", handlePrefixCancel)

	// Workspace prefix (leader, w, ...)
	for i := 1; i <= 9; i++ {
		d.Register("workspace_prefix_switch_"+string(rune('0'+i)), makeSwitchWorkspaceHandler(i))
		d.Register("workspace_prefix_move_"+string(rune('0'+i)), makeMoveAndFollowHandler(i))
	}
	d.Register("workspace_prefix_cancel", handlePrefixCancel)

	// Debug prefix (leader, D, ...)
	d.Register("debug_prefix_logs", handleDebugLogs)
	d.Register("debug_prefix_cache", handleDebugCache)
	d.Register("debug_prefix_showkeys", handleDebugShowkeys)
	d.Register("debug_prefix_animations", handleDebugAnimations)
	d.Register("debug_prefix_cancel", handlePrefixCancel)

	// Tape prefix (leader, T, ...)
	d.Register("tape_prefix_manager", handleToggleTapeManager)
	d.Register("tape_prefix_review", handleTapeReview)
	d.Register("tape_prefix_record", handleTapeRecord)
	d.Register("tape_prefix_stop", handleTapeStop)
	d.Register("tape_prefix_cancel", handlePrefixCancel)

	// Terminal mode direct binds (no prefix)
	d.Register("terminal_next_window", handleTerminalNextWindow)
	d.Register("terminal_prev_window", handleTerminalPrevWindow)
	d.Register("terminal_exit_mode", handleTerminalExitMode)
}

// refreshFocusedWindow invalidates the focused window's render cache. Every
// focus change needs it in terminal mode, where the newly focused pane is drawn
// with the cursor and must not come from the cache.
func refreshFocusedWindow(o *app.OS) {
	if focused := o.GetFocusedWindow(); focused != nil {
		focused.InvalidateCache()
	}
}

// makeSubPrefixHandler builds a handler that enters a sub-prefix. The prefix
// stays active so the next key is routed to the sub-prefix rather than to the
// terminal, and the timer restarts so the chord gets a fresh timeout.
func makeSubPrefixHandler(activate func(*app.OS)) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		activate(o)
		o.PrefixActive = true
		o.LastPrefixTime = time.Now()
		return o, nil
	}
}

// handlePrefixCancel dismisses a prefix without doing anything else. The prefix
// flags are already cleared by the routing layer before dispatch.
func handlePrefixCancel(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, nil
}

func handlePrefixNewWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.AddWindow("")
	return o, nil
}

func handlePrefixCloseWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if len(o.Windows) == 0 || o.FocusedWindow < 0 {
		return o, nil
	}
	w := o.Windows[o.FocusedWindow]
	o.FireHook(hooks.AfterCloseWindow, w.ID, w.Title())
	o.DeleteWindow(o.FocusedWindow)
	if len(o.Windows) > 0 {
		refreshFocusedWindow(o)
	} else if o.Mode == app.TerminalMode {
		// Nothing left to type into.
		o.Mode = app.WindowManagementMode
	}
	return o, nil
}

// startRename puts the focused window into rename mode. Renaming is a
// window-management activity, so terminal mode is left first; the caller in
// window-management mode is already there.
func startRename(o *app.OS) {
	if config.WindowTitlePosition == "hidden" || len(o.Windows) == 0 || o.FocusedWindow < 0 {
		return
	}
	focused := o.GetFocusedWindow()
	if focused == nil {
		return
	}
	o.Mode = app.WindowManagementMode
	o.RenamingWindow = true
	o.RenameBuffer = focused.CustomName
	focused.InvalidateCache()
}

func handlePrefixRenameWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	startRename(o)
	return o, nil
}

// handleWindowPrefixRename doubles as the cache-stats reset while that overlay
// is up, matching the standalone rename_window binding.
func handleWindowPrefixRename(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.ShowCacheStats {
		app.GetGlobalStyleCache().ResetStats()
		o.ShowNotification("Cache statistics reset", "info", 2*time.Second)
		return o, nil
	}
	return handlePrefixRenameWindow(msg, o)
}

func handlePrefixSettings(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenSettings()
	return o, nil
}

func handlePrefixNextWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if len(o.Windows) > 0 {
		o.CycleToNextVisibleWindow()
		refreshFocusedWindow(o)
	}
	return o, nil
}

func handlePrefixPrevWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if len(o.Windows) > 0 {
		o.CycleToPreviousVisibleWindow()
		refreshFocusedWindow(o)
	}
	return o, nil
}

// makePrefixSelectHandler focuses the num-th window of the current workspace.
// 0 selects the tenth, matching the tmux-style numbering where the row of digit
// keys wraps around.
func makePrefixSelectHandler(num int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		position := 0
		for i, win := range o.Windows {
			if win.Workspace != o.CurrentWorkspace {
				continue
			}
			// In tiling mode minimized windows are not on screen, so they do not
			// take up a number.
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
		return o, nil
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
		o.ShowNotification("ZOOM", "info", config.NotificationDuration)
	}
	return o, nil
}

func handlePrefixSplitHorizontal(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SplitFocusedHorizontal()
		o.ShowNotification("Split Horizontal", "info", config.NotificationDuration)
	}
	return o, nil
}

func handlePrefixSplitVertical(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.SplitFocusedVertical()
		o.ShowNotification("Split Vertical", "info", config.NotificationDuration)
	}
	return o, nil
}

func handlePrefixRotateSplit(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.RotateFocusedSplit()
		o.ShowNotification("Split Rotated", "info", config.NotificationDuration)
	}
	return o, nil
}

func handlePrefixEqualizeSplits(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling {
		o.EqualizeSplits()
		o.ShowNotification("Splits Equalized", "info", config.NotificationDuration)
	}
	return o, nil
}

func handlePrefixSelection(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if focused := o.GetFocusedWindow(); focused != nil {
		focused.EnterCopyMode()
		o.ShowNotification("COPY MODE (hjkl/q)", "info", 2*config.NotificationDuration)
	}
	return o, nil
}

func handlePrefixScrollback(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	OpenScrollbackBrowser(o)
	return o, nil
}

func handlePrefixHelp(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ShowHelp = !o.ShowHelp
	return o, nil
}

func handlePrefixCommandPalette(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ShowCommandPalette = true
	o.CommandPaletteQuery = ""
	o.CommandPaletteSelected = 0
	o.CommandPaletteScroll = 0
	return o, nil
}

func handlePrefixSessionSwitcher(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ShowSessionSwitcher = true
	o.SessionSwitcherQuery = ""
	o.SessionSwitcherSelected = 0
	o.SessionSwitcherScroll = 0
	o.SessionSwitcherError = ""
	o.SessionSwitcherItems = o.RefreshSessionList()
	return o, nil
}

// leaveTerminalMode returns to window-management mode and says so. No-op when
// already there.
func leaveTerminalMode(o *app.OS) {
	if o.Mode != app.TerminalMode {
		return
	}
	o.Mode = app.WindowManagementMode
	o.ShowNotification("Window Management Mode", "info", config.NotificationDuration)
	refreshFocusedWindow(o)
}

func handlePrefixDetach(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if m, cmd, detached := detachSession(o); detached {
		return m, cmd
	}
	// Outside a daemon session there is nothing to detach from, so the closest
	// useful thing is to step back out to window-management mode.
	leaveTerminalMode(o)
	return o, nil
}

func handlePrefixExitMode(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	leaveTerminalMode(o)
	return o, nil
}

func handlePrefixQuit(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return requestQuit(o)
}

// ============================================================================
// Minimize prefix
// ============================================================================

func handleMinimizeFocused(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		o.MinimizeWindow(o.FocusedWindow)
	}
	return o, nil
}

// minimizedInCurrentWorkspace lists the indices of minimized windows on the
// current workspace, in the order the restore digits address them.
func minimizedInCurrentWorkspace(o *app.OS) []int {
	var indices []int
	for i, win := range o.Windows {
		if win.Minimized && win.Workspace == o.CurrentWorkspace {
			indices = append(indices, i)
		}
	}
	return indices
}

// makeRestoreMinimizedByPositionHandler restores the position-th minimized
// window (1-based) of the current workspace.
func makeRestoreMinimizedByPositionHandler(position int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		minimized := minimizedInCurrentWorkspace(o)
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

// ============================================================================
// Debug prefix
// ============================================================================

// toggleNotify flips a bool and announces the new state as "<label>: ON/OFF".
func toggleNotify(o *app.OS, label string, on bool) {
	state := "OFF"
	if on {
		state = "ON"
	}
	o.ShowNotification(label+": "+state, "info", config.NotificationDuration)
}

func handleDebugLogs(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ShowLogs = !o.ShowLogs
	toggleNotify(o, "Log Viewer", o.ShowLogs)
	return o, nil
}

func handleDebugCache(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ShowCacheStats = !o.ShowCacheStats
	toggleNotify(o, "Cache Stats", o.ShowCacheStats)
	return o, nil
}

func handleDebugShowkeys(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleShowKeys()
	toggleNotify(o, "Showkeys", o.ShowKeys)
	return o, nil
}

func handleDebugAnimations(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	config.AnimationsEnabled = !config.AnimationsEnabled
	toggleNotify(o, "Animations", config.AnimationsEnabled)
	return o, nil
}

// ============================================================================
// Tape prefix
// ============================================================================

// handleTapeReview opens the project-tape review/trust dialog for the tape in
// the focused window's current directory. It is the deliberate action that lets
// the user read a detected tape and choose to run or trust it.
func handleTapeReview(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenTapeReview()
	return o, nil
}

func handleTapeRecord(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
		o.ShowNotification("Already recording", "warning", config.NotificationDuration)
		return o, nil
	}
	o.TapeManagerStartRecording()
	o.ShowTapeManager = true // Show the UI for naming
	return o, nil
}

func handleTapeStop(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
		o.TapeManagerStopRecording()
	} else {
		o.ShowNotification("Not recording", "warning", config.NotificationDuration)
	}
	return o, nil
}

// ============================================================================
// Terminal mode direct binds
// ============================================================================

// handleTerminalNextWindow moves focus forward. In the scrolling layout the
// windows form a strip rather than a cycle, so focus moves along it instead.
func handleTerminalNextWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingFocusRight()
	} else {
		o.CycleToNextVisibleWindow()
	}
	refreshFocusedWindow(o)
	return o, nil
}

func handleTerminalPrevWindow(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AutoTiling && o.UseScrollingLayout {
		o.ScrollingFocusLeft()
	} else {
		o.CycleToPreviousVisibleWindow()
	}
	refreshFocusedWindow(o)
	return o, nil
}

func handleTerminalExitMode(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	leaveTerminalMode(o)
	return o, nil
}
