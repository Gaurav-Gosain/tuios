package app

import (
	"errors"
	"fmt"
	"image/color"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) scriptDoneLinger() time.Duration {
	return time.Duration(m.Settings.TapeDoneLingerMS) * time.Millisecond
}

func (m *OS) maybeExitFinishedScript() bool {
	if !m.ScriptMode || m.ScriptFinishedTime.IsZero() {
		return false
	}
	if time.Since(m.ScriptFinishedTime) < m.scriptDoneLinger() {
		return false
	}
	m.exitScriptMode()
	return true
}

func (m *OS) exitScriptMode() {
	m.ScriptMode = false
	m.ScriptPaused = false
	m.ScriptPlayer = nil
	m.ScriptExecutor = nil
	m.ScriptSleepUntil = time.Time{}
	m.ScriptFinishedTime = time.Time{}
	m.ScriptWaitRegex = nil
	m.ScriptWaitDeadline = time.Time{}
	m.ScriptAwaitWindows = 0
	m.ScriptAwaitDeadline = time.Time{}
	m.ScriptWait = nil
	m.scriptInFlight = false
	m.scriptCmds = nil
	if m.scriptRequestID != "" {
		m.reportScriptResult(false, "the tape was stopped before it finished")
	}
}

func (m *OS) getWindowDisplayName(w *terminal.Window) string {
	if w.CustomName != "" {
		return w.CustomName
	}
	return w.Title()
}

func (m *OS) findWindowsByName(name string) []*terminal.Window {
	var matches []*terminal.Window
	for _, w := range m.Windows {
		displayName := m.getWindowDisplayName(w)
		if displayName == name {
			matches = append(matches, w)
		}
	}
	return matches
}

func (m *OS) findSingleWindowByName(name string) (*terminal.Window, error) {
	matches := m.findWindowsByName(name)
	if len(matches) == 0 {
		return nil, errfmt.Errorf("no window found with name: %s", name)
	}
	if len(matches) > 1 {
		return nil, errfmt.Errorf("multiple windows (%d) found with name: %s", len(matches), name)
	}
	return matches[0], nil
}

// ExecuteCommand executes a tape command.
func (m *OS) ExecuteCommand(_ *tape.Command) error {
	return nil
}

// GetFocusedWindowID returns the ID of the focused window.
func (m *OS) GetFocusedWindowID() string {
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		return m.Windows[m.FocusedWindow].ID
	}
	return ""
}

func (m *OS) resolveWindowTarget(target string) (string, error) {
	if target == "" {
		windowID := m.GetFocusedWindowID()
		if windowID == "" {
			return "", errfmt.Errorf("no focused window")
		}
		return windowID, nil
	}

	for _, w := range m.Windows {
		if w.ID == target {
			return w.ID, nil
		}
	}

	if idx, ok := session.WindowIndexTarget(target, len(m.Windows)); ok {
		return m.Windows[idx].ID, nil
	}

	matches := m.findWindowsByName(target)
	if len(matches) == 1 {
		return matches[0].ID, nil
	}
	if len(matches) > 1 {
		return "", errfmt.Errorf("ambiguous window name %q matches %d windows", target, len(matches))
	}

	var prefixMatch *terminal.Window
	prefixCount := 0
	for _, w := range m.Windows {
		if strings.HasPrefix(w.ID, target) {
			prefixMatch = w
			prefixCount++
		}
	}
	if prefixCount == 1 {
		return prefixMatch.ID, nil
	}
	if prefixCount > 1 {
		return "", errfmt.Errorf("ambiguous window ID prefix %q matches %d windows", target, prefixCount)
	}

	return "", errfmt.Errorf("no window found matching %q", target)
}

// SendToWindow sends bytes to a window's PTY.
// This works in both local and daemon mode.
func (m *OS) SendToWindow(windowID string, data []byte) error {
	for _, w := range m.Windows {
		if w.ID == windowID {
			return w.SendInput(data)
		}
	}
	return errfmt.Errorf("window not found: %s", windowID)
}

const scriptWindowWait = 5 * time.Second

func (m *OS) awaitNewWindow(before int) {
	m.ScriptAwaitWindows = before + 1
	m.ScriptAwaitDeadline = time.Now().Add(scriptWindowWait)
}

func (m *OS) scriptPaneReady() bool {
	if m.ScriptAwaitWindows == 0 {
		return true
	}
	if len(m.Windows) >= m.ScriptAwaitWindows {
		m.ScriptAwaitWindows = 0
		return true
	}
	if time.Now().Before(m.ScriptAwaitDeadline) {
		return false
	}
	m.ScriptAwaitWindows = 0
	m.ShowNotification(
		"Tape: the new pane never appeared. The rest runs in the current pane",
		"error", m.Settings.NotificationDuration*2)
	return true
}

// CreateNewWindow creates a new window with an optional name.
func (m *OS) CreateNewWindow() error {
	before := len(m.Windows)
	m.AddWindow("")
	m.awaitNewWindow(before)
	m.MarkAllDirty()
	return nil
}

// CreateNewWindowWithName creates a new window with a specific name.
//
// The name is passed to AddWindow rather than written onto the last window
// afterwards. In a daemon session the window does not exist yet when this
// returns (the daemon creates it and pushes it back), so naming it by position
// would name whatever happened to be last.
func (m *OS) CreateNewWindowWithName(name string) error {
	before := len(m.Windows)
	m.AddWindow(name)
	m.awaitNewWindow(before)
	m.MarkAllDirty()
	return nil
}

func (m *OS) getWindowInfo(w *terminal.Window, isFocused bool) map[string]any {
	info := map[string]any{
		"id":             w.ID,
		"title":          w.Title(),
		"display_name":   m.getWindowDisplayName(w),
		"workspace":      w.Workspace,
		"focused":        isFocused,
		"minimized":      w.Minimized,
		"fullscreen":     w.Width == m.Width && w.Height == m.GetUsableHeight(),
		"x":              w.X,
		"y":              w.Y,
		"width":          w.Width,
		"height":         w.Height,
		"cursor_x":       0,
		"cursor_y":       0,
		"cursor_visible": true,
	}

	if w.CustomName != "" {
		info["custom_name"] = w.CustomName
	}
	if w.IsScratch {
		info["scratch"] = true
	}

	if w.PTYID != "" {
		info["pty_id"] = w.PTYID
	}

	if w.Terminal != nil {
		cursorPos := w.Terminal.CursorPosition()
		info["cursor_x"] = cursorPos.X
		info["cursor_y"] = cursorPos.Y
		info["cursor_visible"] = !w.Terminal.IsCursorHidden()
		info["scrollback_lines"] = w.Terminal.ScrollbackLen()
	}

	if w.Cmd != nil && w.Cmd.Process != nil {
		info["shell_pid"] = w.Cmd.Process.Pid
	}
	if w.ShellPgid > 0 {
		info["shell_pgid"] = w.ShellPgid
	}

	info["has_foreground_process"] = w.HasForegroundProcess()

	return info
}

// GetWindowListData returns data about all windows.
func (m *OS) GetWindowListData() map[string]any {
	windows := make([]map[string]any, 0, len(m.Windows))

	for i, w := range m.Windows {
		isFocused := i == m.FocusedWindow
		windows = append(windows, m.getWindowInfo(w, isFocused))
	}

	workspaceWindows := make([]int, m.NumWorkspaces)
	for _, w := range m.Windows {
		if w.Workspace >= 1 && w.Workspace <= m.NumWorkspaces {
			workspaceWindows[w.Workspace-1]++
		}
	}

	focusedWindowID := ""
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		focusedWindowID = m.Windows[m.FocusedWindow].ID
	}

	return map[string]any{
		"windows":           windows,
		"total":             len(m.Windows),
		"focused_index":     m.FocusedWindow,
		"focused_window_id": focusedWindowID,
		"current_workspace": m.dockWorkspace(),
		"workspace_windows": workspaceWindows,
	}
}

// GetSessionInfoData returns data about the current session.
func (m *OS) GetSessionInfoData() map[string]any {
	mode := "window_management"
	if m.Mode == TerminalMode {
		mode = "terminal"
	}

	tilingMode := "floating"
	if m.AutoTiling {
		tilingMode = "tiling"
	}

	dockbarPosition := m.Settings.DockbarPosition
	if dockbarPosition == "" {
		dockbarPosition = "bottom"
	}

	workspaceWindows := make([]int, m.NumWorkspaces)
	for _, w := range m.Windows {
		if w.Workspace >= 1 && w.Workspace <= m.NumWorkspaces {
			workspaceWindows[w.Workspace-1]++
		}
	}

	focusedWindowID := ""
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		focusedWindowID = m.Windows[m.FocusedWindow].ID
	}

	themeName := ""
	if theme.IsEnabled() {
		if current := theme.Current(); current != nil {
			themeName = current.ID
		}
	}

	info := map[string]any{
		"current_workspace":  m.dockWorkspace(),
		"total_windows":      len(m.Windows),
		"focused_window_id":  focusedWindowID,
		"mode":               mode,
		"tiling_enabled":     m.AutoTiling,
		"tiling_mode":        tilingMode,
		"layout_mode":        m.LayoutModeName(),
		"theme":              themeName,
		"dockbar_position":   dockbarPosition,
		"animations_enabled": m.Settings.AnimationsOn(),
		"motion":             m.Settings.Motion,
		"width":              m.Width,
		"height":             m.Height,
		"workspace_windows":  workspaceWindows,
		"num_workspaces":     m.NumWorkspaces,
		"pane_requests":      m.PaneRequestsInFlight(),
	}

	if m.ScriptMode {
		info["script_mode"] = true
		info["script_paused"] = m.ScriptPaused
		if player := m.ScriptPlayer; player != nil {
			info["script_progress"] = player.Progress()
			info["script_current"] = player.CurrentIndex()
			info["script_total"] = player.TotalCommands()
		}
	} else {
		info["script_mode"] = false
	}

	info["prefix_active"] = m.PrefixActive

	return info
}

// GetWindowData returns data about a specific window by ID or name.
func (m *OS) GetWindowData(identifier string) (map[string]any, error) {
	for i, w := range m.Windows {
		if w.ID == identifier {
			return m.getWindowInfo(w, i == m.FocusedWindow), nil
		}
	}

	matches := m.findWindowsByName(identifier)
	if len(matches) == 0 {
		return nil, errfmt.Errorf("no window found with ID or name: %s", identifier)
	}
	if len(matches) > 1 {
		return nil, errfmt.Errorf("multiple windows (%d) found with name: %s", len(matches), identifier)
	}

	for i, w := range m.Windows {
		if w.ID == matches[0].ID {
			return m.getWindowInfo(w, i == m.FocusedWindow), nil
		}
	}

	return m.getWindowInfo(matches[0], false), nil
}

// GetFocusedWindowData returns data about the currently focused window.
func (m *OS) GetFocusedWindowData() (map[string]any, error) {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return nil, errfmt.Errorf("no window is focused")
	}
	return m.getWindowInfo(m.Windows[m.FocusedWindow], true), nil
}

// CloseWindow closes a window.
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

// CloseWindowByName closes all windows with the given name.
func (m *OS) CloseWindowByName(name string) error {
	matches := m.findWindowsByName(name)
	if len(matches) == 0 {
		return errfmt.Errorf("no window found with name: %s", name)
	}

	for i := len(m.Windows) - 1; i >= 0; i-- {
		displayName := m.getWindowDisplayName(m.Windows[i])
		if displayName == name {
			m.DeleteWindow(i)
		}
	}
	m.MarkAllDirty()
	return nil
}

// SwitchWorkspace switches to a workspace. An out-of-range workspace is an
// error rather than a silent no-op, so a tape asking for workspace 12 in a
// nine-workspace session says so instead of appearing to work.
func (m *OS) SwitchWorkspace(workspace int) error {
	if workspace < 1 || workspace > m.NumWorkspaces {
		return errfmt.Errorf("workspace %d is out of range (1-%d)", workspace, m.NumWorkspaces)
	}
	recorder := m.TapeRecorder
	m.TapeRecorder = nil
	m.switchToWorkspace(workspace, -1)
	m.TapeRecorder = recorder
	m.MarkAllDirty()
	return nil
}

// ToggleTiling toggles tiling mode.
func (m *OS) ToggleTiling() error {
	m.ToggleAutoTiling()
	m.MarkAllDirty()
	return nil
}

// SetMode sets the interaction mode.
func (m *OS) SetMode(mode string) error {
	switch mode {
	case "terminal", "Terminal", "TerminalMode":
		m.ChooseMode(TerminalMode, ModeCauseTape)
		if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
			for i, w := range m.Windows {
				if w.Workspace == m.CurrentWorkspace && !w.Minimized {
					m.FocusWindow(i)
					break
				}
			}
		}
	case "window", "Window", "WindowManagementMode":
		m.ChooseMode(WindowManagementMode, ModeCauseTape)
	}
	return nil
}

// NextWindow focuses the next window.
func (m *OS) NextWindow() error {
	if len(m.Windows) == 0 {
		return nil
	}
	m.CycleToNextVisibleWindow()
	m.MarkAllDirty()
	return nil
}

// PrevWindow focuses the previous window.
func (m *OS) PrevWindow() error {
	if len(m.Windows) == 0 {
		return nil
	}
	m.CycleToPreviousVisibleWindow()
	m.MarkAllDirty()
	return nil
}

// FocusWindowByID focuses a specific window by ID.
//
// A command that names the pane to focus asks to be taken there, so the
// scrolling strip shows the whole column. See RevealFocusedColumn.
//
// A minimized pane is restored first (issue 1731): focus-window and the
// navigator name a pane to look at, and a focus on a pane that stays hidden
// looks like it did nothing. A title row of a stack is left to FocusPane,
// which opens it in the stack's tile.
func (m *OS) FocusWindowByID(windowID string) error {
	for i, w := range m.Windows {
		if w.ID == windowID {
			if w.Minimized && w.Stack == "" {
				m.RestoreWindow(i)
			}
			m.FocusWindow(i)
			m.RevealFocusedColumn()
			m.MarkAllDirty()
			return nil
		}
	}
	return nil
}

// FocusWindowByName focuses a window by name. Errors if multiple windows match.
func (m *OS) FocusWindowByName(name string) error {
	win, err := m.findSingleWindowByName(name)
	if err != nil {
		return err
	}
	return m.FocusWindowByID(win.ID)
}

// RenameWindowByID renames a window by its ID (sets CustomName).
//
// In a daemon session the name is the daemon's: it is what every read verb
// answers with and what survives a detach, so renaming locally and hoping a
// later sync carries it could report success while list-windows kept the old
// name. The client redraws when the daemon pushes the change back.
func (m *OS) RenameWindowByID(windowID, name string) error {
	if m.IsDaemonSession && m.DaemonClient != nil {
		return m.DaemonClient.SendIntent("RenameWindow", windowID, name)
	}

	for _, w := range m.Windows {
		if w.ID == windowID {
			w.CustomName = name
			m.MarkAllDirty()
			return nil
		}
	}
	return nil
}

// RenameWindowByName renames a window by its current name. Errors if multiple windows match.
func (m *OS) RenameWindowByName(oldName, newName string) error {
	win, err := m.findSingleWindowByName(oldName)
	if err != nil {
		return err
	}
	return m.RenameWindowByID(win.ID, newName)
}

// MinimizeWindowByID minimizes a window.
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

// MinimizeWindowByName minimizes a window by name. Errors if multiple windows match.
func (m *OS) MinimizeWindowByName(name string) error {
	win, err := m.findSingleWindowByName(name)
	if err != nil {
		return err
	}
	return m.MinimizeWindowByID(win.ID)
}

// RestoreWindowByID restores a minimized window.
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

// RestoreWindowByName restores a minimized window by name. Errors if multiple windows match.
func (m *OS) RestoreWindowByName(name string) error {
	win, err := m.findSingleWindowByName(name)
	if err != nil {
		return err
	}
	return m.RestoreWindowByID(win.ID)
}

// EnableTiling enables tiling mode.
func (m *OS) EnableTiling() error {
	m.SetAutoTiling(true)
	m.MarkAllDirty()
	return nil
}

// DisableTiling disables tiling mode.
func (m *OS) DisableTiling() error {
	m.SetAutoTiling(false)
	m.MarkAllDirty()
	return nil
}

// SnapByDirection snaps the focused window to a direction with tiling off.
// With tiling on the tiler owns every rectangle and there is nothing to snap,
// so left and right move focus to the neighbour that way instead. That is one
// answer for every door onto the action: the h and l keys, a recorded tape,
// and run-command SnapLeft.
func (m *OS) SnapByDirection(direction string) error {
	if m.AutoTiling {
		switch direction {
		case "left", "right":
			return m.focusTiledNeighbour(direction)
		}
		return errfmt.Errorf("cannot snap windows while tiling mode is enabled")
	}

	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return errNoFocusedWindow
	}

	var quarter SnapQuarter
	switch direction {
	case "left":
		quarter = SnapLeft
	case "right":
		quarter = SnapRight
	case "fullscreen":
		m.Snap(m.FocusedWindow, SnapFullScreen)
		m.MarkAllDirty()
		return nil
	default:
		return errfmt.Errorf("invalid snap direction: %s (use: left, right, fullscreen)", direction)
	}

	m.Snap(m.FocusedWindow, quarter)
	m.MarkAllDirty()
	return nil
}

// MoveWindowToWorkspaceByID moves a window to a workspace.
func (m *OS) MoveWindowToWorkspaceByID(windowID string, workspace int) error {
	if workspace < 1 || workspace > m.NumWorkspaces {
		return errfmt.Errorf("workspace %d out of range (1-%d)", workspace, m.NumWorkspaces)
	}

	for i, w := range m.Windows {
		if w.ID == windowID {
			m.MoveWindowToWorkspace(i, workspace)
			return nil
		}
	}

	return errfmt.Errorf("window not found: %s", windowID)
}

// MoveAndFollowWorkspaceByID moves a window to a workspace and switches to it.
func (m *OS) MoveAndFollowWorkspaceByID(windowID string, workspace int) error {
	if workspace < 1 || workspace > m.NumWorkspaces {
		return errfmt.Errorf("workspace %d out of range (1-%d)", workspace, m.NumWorkspaces)
	}

	for i, w := range m.Windows {
		if w.ID == windowID {
			m.MoveWindowToWorkspaceAndFollow(i, workspace)
			return nil
		}
	}

	return errfmt.Errorf("window not found: %s", windowID)
}

var errTilingOff = errors.New("tiling is not enabled; run EnableTiling before this command")

var errNoFocusedWindow = errors.New("no focused window")

// SplitHorizontal splits the focused window horizontally.
func (m *OS) SplitHorizontal() error {
	if !m.AutoTiling {
		return errTilingOff
	}
	if m.GetFocusedWindow() == nil {
		return errNoFocusedWindow
	}
	before := len(m.Windows)
	m.SplitFocusedHorizontal()
	m.awaitNewWindow(before)
	m.MarkAllDirty()
	return nil
}

// SplitVertical splits the focused window vertically.
func (m *OS) SplitVertical() error {
	if !m.AutoTiling {
		return errTilingOff
	}
	if m.GetFocusedWindow() == nil {
		return errNoFocusedWindow
	}
	before := len(m.Windows)
	m.SplitFocusedVertical()
	m.awaitNewWindow(before)
	m.MarkAllDirty()
	return nil
}

// SplitToward splits the focused window and puts the new one on side of it:
// left, right, up or down. horizontal and vertical are down and right.
func (m *OS) SplitToward(side string) error {
	if !m.AutoTiling {
		return errTilingOff
	}
	if m.GetFocusedWindow() == nil {
		return errNoFocusedWindow
	}
	var dir layout.PreselectionDir
	switch side {
	case "left":
		dir = layout.PreselectionLeft
	case "right":
		dir = layout.PreselectionRight
	case "up":
		dir = layout.PreselectionUp
	case "down":
		dir = layout.PreselectionDown
	default:
		return errfmt.Errorf("unknown split side %q (use left, right, up or down)", side)
	}
	before := len(m.Windows)
	m.splitFocused(dir, false)
	m.awaitNewWindow(before)
	m.MarkAllDirty()
	return nil
}

// RotateSplit rotates the split direction at the focused window.
func (m *OS) RotateSplit() error {
	if !m.AutoTiling {
		return errTilingOff
	}
	m.RotateFocusedSplit()
	m.MarkAllDirty()
	return nil
}

// EqualizeSplitsExec equalizes all split ratios.
func (m *OS) EqualizeSplitsExec() error {
	if !m.AutoTiling {
		return errTilingOff
	}
	m.EqualizeSplits()
	m.MarkAllDirty()
	return nil
}

// Preselect sets the preselection direction for the next window.
func (m *OS) Preselect(direction string) error {
	if !m.AutoTiling {
		return errTilingOff
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

func (m *OS) remember(path, value string) {
	if m.UserConfig == nil {
		return
	}
	_ = config.SetOptionValue(m.UserConfig, path, value)
}

// SetMotion puts this client's motion level at one of config.MotionLevels and
// records it in the config the registry re-applies from. It is the one place a
// level is set at runtime, so it also lifts --no-animations: a level somebody
// chose after starting outranks the flag they started with.
func (m *OS) SetMotion(level string) error {
	if !slices.Contains(config.MotionLevels, level) {
		return errfmt.Errorf("unknown motion level: %s (want %s)", level, strings.Join(config.MotionLevels, ", "))
	}
	m.Settings.NoAnimationsFlag = false
	m.Settings.Motion = level
	m.remember("appearance.motion", level)
	return nil
}

// EnableAnimations sets the motion level to full.
func (m *OS) EnableAnimations() error {
	_ = m.SetMotion(config.MotionFull)
	m.ShowNotification("Animations on", "info", m.Settings.NotificationDuration)
	return nil
}

// DisableAnimations sets the motion level to none.
func (m *OS) DisableAnimations() error {
	_ = m.SetMotion(config.MotionNone)
	m.ShowNotification("Animations off", "info", m.Settings.NotificationDuration)
	return nil
}

// ToggleAnimations switches between no motion and full motion.
func (m *OS) ToggleAnimations() error {
	if m.Settings.AnimationsOn() {
		return m.DisableAnimations()
	}
	return m.EnableAnimations()
}

// SetConfig sets a configuration option at runtime.
//
// The cases below are the paths with a live effect beyond writing a value: an
// animation switch that has to start or stop the running animations, a dockbar
// move that repositions it. Everything else the config registry describes is
// handled generically by setConfigFromRegistry.
func (m *OS) SetConfig(path, value string) error {
	switch path {
	case "appearance.dockbar_position", "dockbar_position":
		return m.SetDockbarPosition(value)
	case "appearance.border_style", "border_style":
		return m.SetBorderStyle(value)
	case "appearance.motion", "motion":
		return m.SetMotion(value)
	case "appearance.animations_enabled", "animations_enabled", "animations":
		switch value {
		case "true", "on", "1", "enabled":
			return m.EnableAnimations()
		case "false", "off", "0", "disabled":
			return m.DisableAnimations()
		default:
			return m.ToggleAnimations()
		}
	case "appearance.hide_window_buttons", "hide_window_buttons":
		switch value {
		case "true", "on", "1":
			m.Settings.HideWindowButtons = true
		case "false", "off", "0":
			m.Settings.HideWindowButtons = false
		}
		m.remember("appearance.hide_window_buttons", value)
		m.MarkAllDirty()
		return nil
	case "appearance.window_button_style", "window_button_style":
		if !slices.Contains(config.WindowButtonStyles, value) {
			return errfmt.Errorf("unknown window button style: %s (want %s)",
				value, strings.Join(config.WindowButtonStyles, " or "))
		}
		m.Settings.WindowButtonStyle = value
		m.remember("appearance.window_button_style", value)
		m.MarkAllDirty()
		return nil
	case "appearance.window_button_position", "window_button_position":
		if !slices.Contains(config.WindowButtonPositions, value) {
			return errfmt.Errorf("unknown window button position: %s (want %s)",
				value, strings.Join(config.WindowButtonPositions, " or "))
		}
		m.Settings.WindowButtonPosition = value
		m.remember("appearance.window_button_position", value)
		m.MarkAllDirty()
		return nil
	default:
		return m.setConfigFromRegistry(path, value)
	}
}

func (m *OS) setConfigFromRegistry(path, value string) error {
	if _, ok := config.LookupOption(path); !ok {
		if !strings.Contains(path, ".") {
			if _, ok := config.LookupOption("appearance." + path); ok {
				return m.setConfigFromRegistry("appearance."+path, value)
			}
		}
		if msg, retired := config.RetiredOption(path); retired {
			return errors.New(msg)
		}
		return errfmt.Errorf("unknown config path: %s", path)
	}
	if m.UserConfig == nil {
		return errfmt.Errorf("cannot set %s: no configuration is loaded", path)
	}
	if err := config.SetOptionValue(m.UserConfig, path, value); err != nil {
		return err
	}
	if path == "daemon.window_size" {
		m.sendWindowSizeToDaemon(value)
	}
	if path == "appearance.motion" {
		m.Settings.NoAnimationsFlag = false
	}

	config.ApplyAppearanceConfig(m.UserConfig, &m.Settings)
	m.keepSessionSidebar(path == "appearance.sidebar.enabled" || path == "appearance.sidebar_enabled")
	m.applyAppearanceLive(true)
	return nil
}

// SetTheme changes the active theme.
func (m *OS) SetTheme(themeName string) error {
	if err := theme.Initialize(themeName); err != nil {
		return errfmt.Errorf("failed to set theme: %w", err)
	}

	for _, w := range m.Windows {
		if w != nil && w.Terminal != nil {
			w.LockIO()
			if fg, _, cur, pal, ok := theme.EmulatorColors(); ok {
				w.Terminal.SetThemeColors(
					fg,
					nil,
					cur,
					pal,
				)
			} else {
				w.Terminal.SetThemeColors(nil, nil, nil, [16]color.Color{})
			}
			w.UnlockIO()
			w.InvalidateCache()
		}
	}

	m.ShowNotification(fmt.Sprintf("Theme: %s", themeName), "info", m.Settings.NotificationDuration)
	m.MarkAllDirty()
	return nil
}

// SetDockbarPosition changes the dockbar position.
func (m *OS) SetDockbarPosition(position string) error {
	if !slices.Contains(config.DockbarPositions, position) {
		return errfmt.Errorf("invalid dockbar position: %s (use: %s)",
			position, strings.Join(config.DockbarPositions, ", "))
	}
	m.Settings.DockbarPosition = position
	m.remember("appearance.dockbar_position", position)
	m.ShowNotification(fmt.Sprintf("Dockbar: %s", position), "info", m.Settings.NotificationDuration)
	m.MarkAllDirty()
	return nil
}

// SetBorderStyle changes the window border style.
func (m *OS) SetBorderStyle(style string) error {
	if !slices.Contains(config.BorderStyles, style) {
		return errfmt.Errorf("invalid border style: %s (use: %s)",
			style, strings.Join(config.BorderStyles, ", "))
	}
	m.Settings.BorderStyle = style
	m.remember("appearance.border_style", style)
	m.ShowNotification(fmt.Sprintf("Border: %s", style), "info", m.Settings.NotificationDuration)
	m.MarkAllDirty()
	return nil
}

// ShowNotificationCmd displays a notification in the UI.
func (m *OS) ShowNotificationCmd(message, notificationType string) error {
	m.ShowNotification(message, notificationType, m.Settings.NotificationDuration)
	return nil
}

func (m *OS) focusTiledNeighbour(direction string) error {
	if m.UseScrollingLayout {
		if direction == "left" {
			m.ScrollingFocusLeft()
		} else {
			m.ScrollingFocusRight()
		}
		return nil
	}
	return m.FocusDirection(direction)
}

// FocusDirection focuses the nearest window in a direction, in any layout. See
// layout.Neighbour for what counts as nearest.
func (m *OS) FocusDirection(direction string) error {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return errNoFocusedWindow
	}
	side, ok := layout.ParseSide(direction)
	if !ok {
		return errfmt.Errorf("invalid direction: %s (use: left, right, up, down)", direction)
	}
	targetIndex := m.findWindowInDirection(m.Windows[m.FocusedWindow], side)
	if targetIndex < 0 {
		return errfmt.Errorf("no window %s of the focused one", direction)
	}
	m.FocusWindow(targetIndex)
	m.RevealFocusedColumn()
	m.MarkAllDirty()

	return nil
}

// MasterLayoutExec runs a master-stack command (tape executor interface). An
// error says what was wrong and what the command takes, because run-command
// shows it to the caller.
func (m *OS) MasterLayoutExec(command tape.CommandType, args []string) error {
	if !m.AutoTiling {
		return errfmt.Errorf("%s needs tiling. Turn tiling on first", command)
	}
	switch command {
	case tape.CommandTypeSetMasterPosition:
		if !m.SetMasterPosition(args[0]) {
			return errfmt.Errorf("%q is not a master position. Use %s", args[0], strings.Join(config.MasterPositions, ", "))
		}
	case tape.CommandTypeSetMasterCount:
		n, err := strconv.Atoi(args[0])
		if err != nil || !config.ValidMasterCount(n) {
			return errfmt.Errorf("%q is not a master count. Use a number from %d to %d", args[0], config.MasterCountMin, config.MasterCountMax)
		}
		m.SetMasterCount(n)
	case tape.CommandTypeCycleMasterPosition:
		m.CycleMasterPosition()
	case tape.CommandTypeAddMaster:
		m.AddMaster()
	case tape.CommandTypeRemoveMaster:
		m.RemoveMaster()
	case tape.CommandTypeSwapWithMaster:
		if !m.SwapWithMaster() {
			return errfmt.Errorf("%s needs the master-stack layout and a pane to swap with", command)
		}
	case tape.CommandTypeFocusMaster:
		if !m.FocusMaster() {
			return errfmt.Errorf("%s needs the master-stack layout and a pane", command)
		}
	}
	m.MarkAllDirty()
	return nil
}

// ToggleZoomExec toggles zoom on the focused window (tape executor interface).
func (m *OS) ToggleZoomExec() error {
	m.ToggleZoom()
	return nil
}

// ScreenshotExec renders the focused window to a file (tape executor
// interface).
func (m *OS) ScreenshotExec() error {
	cmd := m.ScreenshotFocusedWindow()
	if cmd == nil {
		return errors.New("no window to screenshot")
	}
	msg, ok := cmd().(screenshotResultMsg)
	if !ok {
		return nil
	}
	m.HandleScreenshotResult(msg)
	return msg.err
}

// SmartSplitFocusedExec performs a smart split on the focused window (tape executor interface).
func (m *OS) SmartSplitFocusedExec() error {
	if !m.AutoTiling {
		return errTilingOff
	}
	if m.GetFocusedWindow() == nil {
		return errNoFocusedWindow
	}
	before := len(m.Windows)
	m.SmartSplitFocused()
	m.awaitNewWindow(before)
	return nil
}

// ShowCommandPaletteExec opens the command palette (tape executor interface).
// The $PATH rescan it returns is dropped: the tape executor has no channel
// back to Bubble Tea, and a tape asserting on the palette wants the rows that
// were already there, not ones that arrive mid-assertion.
func (m *OS) ShowCommandPaletteExec() error {
	_ = m.OpenCommandPalette()
	return nil
}

// SaveLayoutExec saves the current layout with the given name (tape executor interface).
func (m *OS) SaveLayoutExec(name string) error {
	return m.LayoutTemplateNow("save", name)
}

// LoadLayoutExec loads a saved layout by name (tape executor interface).
func (m *OS) LoadLayoutExec(name string) error {
	return m.LayoutTemplateNow("load", name)
}

func (m *OS) startRemoteSendKeys(keys string, literal bool, raw bool, windowTarget string, requestID string) (tea.Cmd, error) {
	if literal {
		windowID, err := m.resolveWindowTarget(windowTarget)
		if err != nil {
			return nil, err
		}
		if err := m.SendToWindow(windowID, []byte(keys)); err != nil {
			return nil, err
		}
		if m.DaemonClient != nil && requestID != "" {
			_ = m.DaemonClient.SendCommandResult(requestID, true, "keys sent")
		}
		return nil, nil
	}

	var keyMsgs []tea.KeyPressMsg
	if raw {
		keyMsgs = m.parseKeysToMessagesRaw(keys)
	} else {
		keyMsgs = m.parseKeysToMessages(keys)
	}
	if len(keyMsgs) == 0 {
		return nil, errfmt.Errorf("no valid keys in sequence: %s", keys)
	}

	m.remoteKeyChains++
	m.ProcessingRemoteKeys = true
	m.Settings.AnimationsSuppressed = true

	firstKey := keyMsgs[0]
	remaining := keyMsgs[1:]

	return func() tea.Msg {
		return RemoteKeyMsg{
			Key:           firstKey,
			RemainingKeys: remaining,
			RequestID:     requestID,
		}
	}, nil
}

func (m *OS) parseKeysToMessagesRaw(keys string) []tea.KeyPressMsg {
	var msgs []tea.KeyPressMsg
	for _, char := range keys {
		msg := m.parseKeyToMessage(string(char))
		msgs = append(msgs, msg)
	}
	return msgs
}

func (m *OS) parseKeysToMessages(keys string) []tea.KeyPressMsg {
	var msgs []tea.KeyPressMsg

	normalized := strings.ReplaceAll(keys, ",", " ")

	for part := range strings.FieldsSeq(normalized) {

		if strings.EqualFold(part, "$PREFIX") || strings.EqualFold(part, "PREFIX") {
			leaderKey := m.Settings.LeaderKey
			if leaderKey == "" {
				leaderKey = "ctrl+b"
			}
			msg := m.parseKeyToMessage(config.CanonicalKey(leaderKey))
			msgs = append(msgs, msg)
			continue
		}

		msg := m.parseKeyToMessage(part)
		msgs = append(msgs, msg)
	}

	return msgs
}

func (m *OS) parseKeyToMessage(key string) tea.KeyPressMsg {
	var mod tea.KeyMod
	var code, shifted rune
	var text string

	if strings.Contains(key, "+") {
		parts := strings.Split(key, "+")
		keyPart := ""

		for _, part := range parts {
			part = strings.TrimSpace(part)
			name, isMod := config.ParseModifier(part)
			if !isMod {
				keyPart = part
				continue
			}
			switch name {
			case config.ModCtrl:
				mod |= tea.ModCtrl
			case config.ModAlt:
				mod |= tea.ModAlt
			case config.ModShift:
				mod |= tea.ModShift
			case config.ModSuper:
				mod |= tea.ModSuper
			case config.ModHyper:
				mod |= tea.ModHyper
			}
		}
		key = keyPart
	}

	lowerKey := strings.ToLower(key)
	if lowerKey == "comma" {
		key, lowerKey = ",", ","
	}

	switch lowerKey {
	case "enter", "return":
		code = tea.KeyEnter
	case "space":
		code = tea.KeySpace
		if mod == 0 {
			text = " "
		}
	case "tab":
		code = tea.KeyTab
	case "escape", "esc":
		code = tea.KeyEscape
	case "backspace":
		code = tea.KeyBackspace
	case "delete":
		code = tea.KeyDelete
	case "up":
		code = tea.KeyUp
	case "down":
		code = tea.KeyDown
	case "left":
		code = tea.KeyLeft
	case "right":
		code = tea.KeyRight
	case "home":
		code = tea.KeyHome
	case "end":
		code = tea.KeyEnd
	case "pageup", "pgup":
		code = tea.KeyPgUp
	case "pagedown", "pgdown":
		code = tea.KeyPgDown
	case "insert":
		code = tea.KeyInsert
	case "f1":
		code = tea.KeyF1
	case "f2":
		code = tea.KeyF2
	case "f3":
		code = tea.KeyF3
	case "f4":
		code = tea.KeyF4
	case "f5":
		code = tea.KeyF5
	case "f6":
		code = tea.KeyF6
	case "f7":
		code = tea.KeyF7
	case "f8":
		code = tea.KeyF8
	case "f9":
		code = tea.KeyF9
	case "f10":
		code = tea.KeyF10
	case "f11":
		code = tea.KeyF11
	case "f12":
		code = tea.KeyF12
	default:
		if len(key) == 1 {
			char := rune(key[0])
			if char >= 'A' && char <= 'Z' {
				code = char - 'A' + 'a'
			} else {
				code = char
			}
			if mod == 0 {
				text = string(code)
				if code != char {
					text = string(char)
					shifted = char
					mod = tea.ModShift
				}
			}
		} else {
			if len(key) > 0 {
				code = rune(strings.ToLower(key)[0])
				if mod == 0 {
					text = strings.ToLower(key)
				}
			}
		}
	}

	return tea.KeyPressMsg{
		Code:        code,
		ShiftedCode: shifted,
		Text:        text,
		Mod:         mod,
	}
}

func (m *OS) findWindowInDirection(from *terminal.Window, side layout.Side) int {
	var cands []layout.Rect
	var index []int
	for i, win := range m.Windows {
		if win == from || win.Workspace != m.CurrentWorkspace || win.Minimized {
			continue
		}
		cands = append(cands, layout.Rect{X: win.X, Y: win.Y, W: win.Width, H: win.Height})
		index = append(index, i)
	}
	origin := layout.Rect{X: from.X, Y: from.Y, W: from.Width, H: from.Height}
	if from.Zoomed && !m.zoomUsesLayout(from) && from.PreZoomWidth > 0 && from.PreZoomHeight > 0 {
		origin = layout.Rect{X: from.PreZoomX, Y: from.PreZoomY, W: from.PreZoomWidth, H: from.PreZoomHeight}
	}
	if n := layout.Neighbour(origin, cands, side, !m.AutoTiling); n >= 0 {
		return index[n]
	}
	if m.AutoTiling && m.Settings.FocusWrap &&
		(!m.UseScrollingLayout || side == layout.SideUp || side == layout.SideDown) {
		if n := layout.Wrap(origin, cands, side); n >= 0 {
			return index[n]
		}
	}
	return -1
}

func (m *OS) startScriptWaitRegex(cmd *tape.Command) {
	if len(cmd.Args) == 0 {
		m.ShowNotification("WaitUntilRegex: missing pattern", "error", m.Settings.NotificationDuration)
		return
	}
	re, err := regexp.Compile(cmd.Args[0])
	if err != nil {
		m.ShowNotification(fmt.Sprintf("WaitUntilRegex: invalid pattern: %v", err), "error", m.Settings.NotificationDuration)
		return
	}
	timeout := 5000 * time.Millisecond
	if len(cmd.Args) > 1 {
		if ms, convErr := strconv.Atoi(cmd.Args[1]); convErr == nil && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}
	m.ScriptWaitRegex = re
	m.ScriptWaitDeadline = time.Now().Add(timeout)
}

func (m *OS) checkScriptWaitRegex() bool {
	if m.ScriptWaitRegex == nil {
		return true
	}

	matched := false
	if win := m.GetFocusedWindow(); win != nil && win.Terminal != nil {
		win.RLockIO()
		content := win.Terminal.String()
		win.RUnlockIO()
		matched = m.ScriptWaitRegex.MatchString(content)
	}

	if matched {
		m.ScriptWaitRegex = nil
		m.ScriptWaitDeadline = time.Time{}
		return true
	}

	if !m.ScriptWaitDeadline.IsZero() && time.Now().After(m.ScriptWaitDeadline) {
		m.ShowNotification("WaitUntilRegex: timed out", "warning", m.Settings.NotificationDuration)
		m.ScriptWaitRegex = nil
		m.ScriptWaitDeadline = time.Time{}
		return true
	}

	return false
}

// LeaderKeyPress is the leader key as the key press it is, for send_prefix,
// which hands it to the pane the way a second press of the leader does.
func (m *OS) LeaderKeyPress() tea.KeyPressMsg {
	leader := m.Settings.LeaderKey
	if leader == "" {
		leader = config.DefaultLeaderKey
	}
	return m.parseKeyToMessage(leader)
}
