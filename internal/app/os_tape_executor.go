package app

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	tea "charm.land/bubbletea/v2"
)

// The following methods implement the tape.Executor interface for
// scripted automation and tape playback functionality.

// getWindowDisplayName returns the display name for a window (CustomName if set, else Title).
func (m *OS) getWindowDisplayName(w *terminal.Window) string {
	if w.CustomName != "" {
		return w.CustomName
	}
	return w.Title
}

// findWindowsByName returns all windows matching the given name (checks CustomName first, then Title).
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

// findSingleWindowByName returns a single window by name, or an error if not found or ambiguous.
func (m *OS) findSingleWindowByName(name string) (*terminal.Window, error) {
	matches := m.findWindowsByName(name)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no window found with name: %s", name)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("multiple windows (%d) found with name: %s", len(matches), name)
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

// SendToWindow sends bytes to a window's PTY.
// This works in both local and daemon mode.
func (m *OS) SendToWindow(windowID string, data []byte) error {
	for _, w := range m.Windows {
		if w.ID == windowID {
			return w.SendInput(data)
		}
	}
	return fmt.Errorf("window not found: %s", windowID)
}

// CreateNewWindow creates a new window with an optional name.
func (m *OS) CreateNewWindow() error {
	m.AddWindow("Window")
	m.MarkAllDirty()
	return nil
}

// CreateNewWindowWithName creates a new window with a specific name.
func (m *OS) CreateNewWindowWithName(name string) error {
	m.AddWindow("")
	// Set the CustomName on the newly created window
	if len(m.Windows) > 0 {
		m.Windows[len(m.Windows)-1].CustomName = name
	}
	m.MarkAllDirty()
	return nil
}

// CreateNewWindowReturningID creates a new window and returns its ID and display name.
// This is safe because Bubble Tea's Update runs on a single goroutine.
func (m *OS) CreateNewWindowReturningID(name string) (windowID string, displayName string, err error) {
	prevCount := len(m.Windows)
	m.AddWindow("")

	// Check if window was actually created
	if len(m.Windows) <= prevCount {
		return "", "", fmt.Errorf("failed to create window")
	}

	newWindow := m.Windows[len(m.Windows)-1]
	if name != "" {
		newWindow.CustomName = name
	}
	m.MarkAllDirty()
	return newWindow.ID, m.getWindowDisplayName(newWindow), nil
}

// getWindowInfo returns detailed information about a window.
func (m *OS) getWindowInfo(w *terminal.Window, isFocused bool) map[string]interface{} {
	info := map[string]interface{}{
		"id":             w.ID,
		"title":          w.Title,
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

	if w.PTYID != "" {
		info["pty_id"] = w.PTYID
	}

	// Get cursor info from terminal emulator
	if w.Terminal != nil {
		cursorPos := w.Terminal.CursorPosition()
		info["cursor_x"] = cursorPos.X
		info["cursor_y"] = cursorPos.Y
		info["cursor_visible"] = !w.Terminal.IsCursorHidden()
		// Get scrollback info from the terminal's screen
		info["scrollback_lines"] = w.Terminal.ScrollbackLen()
	}

	// Get process info (Unix only - will be 0 on Windows)
	if w.Cmd != nil && w.Cmd.Process != nil {
		info["shell_pid"] = w.Cmd.Process.Pid
	}
	if w.ShellPgid > 0 {
		info["shell_pgid"] = w.ShellPgid
	}

	// Check if there's a foreground process running
	info["has_foreground_process"] = w.HasForegroundProcess()

	return info
}

// GetWindowListData returns data about all windows.
func (m *OS) GetWindowListData() map[string]interface{} {
	windows := make([]map[string]interface{}, 0, len(m.Windows))

	for i, w := range m.Windows {
		isFocused := i == m.FocusedWindow
		windows = append(windows, m.getWindowInfo(w, isFocused))
	}

	// Count windows per workspace
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

	return map[string]interface{}{
		"windows":           windows,
		"total":             len(m.Windows),
		"focused_index":     m.FocusedWindow,
		"focused_window_id": focusedWindowID,
		"current_workspace": m.CurrentWorkspace,
		"workspace_windows": workspaceWindows,
	}
}

// GetSessionInfoData returns data about the current session.
func (m *OS) GetSessionInfoData() map[string]interface{} {
	// Determine mode
	mode := "window_management"
	if m.Mode == TerminalMode {
		mode = "terminal"
	}

	// Get tiling mode string
	tilingMode := "floating"
	if m.AutoTiling {
		tilingMode = "bsp"
	}

	// Get dockbar position - it's stored as a string in config
	dockbarPosition := config.DockbarPosition
	if dockbarPosition == "" {
		dockbarPosition = "bottom"
	}

	// Count windows per workspace
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

	// Get current theme name from theme package
	themeName := ""
	if theme.IsEnabled() {
		if current := theme.Current(); current != nil {
			themeName = current.ID
		}
	}

	info := map[string]interface{}{
		"current_workspace":  m.CurrentWorkspace,
		"total_windows":      len(m.Windows),
		"focused_window_id":  focusedWindowID,
		"mode":               mode,
		"tiling_enabled":     m.AutoTiling,
		"tiling_mode":        tilingMode,
		"theme":              themeName,
		"dockbar_position":   dockbarPosition,
		"animations_enabled": config.AnimationsEnabled,
		"width":              m.Width,
		"height":             m.Height,
		"workspace_windows":  workspaceWindows,
		"num_workspaces":     m.NumWorkspaces,
	}

	// Script playback info
	if m.ScriptMode {
		info["script_mode"] = true
		info["script_paused"] = m.ScriptPaused
		if m.ScriptPlayer != nil {
			if player, ok := m.ScriptPlayer.(*tape.Player); ok {
				info["script_progress"] = player.Progress()
				info["script_current"] = player.CurrentIndex()
				info["script_total"] = player.TotalCommands()
			}
		}
	} else {
		info["script_mode"] = false
	}

	// Add prefix key state
	info["prefix_active"] = m.PrefixActive

	return info
}

// GetWindowData returns data about a specific window by ID or name.
func (m *OS) GetWindowData(identifier string) (map[string]interface{}, error) {
	// First try by ID
	for i, w := range m.Windows {
		if w.ID == identifier {
			return m.getWindowInfo(w, i == m.FocusedWindow), nil
		}
	}

	// Then try by name
	matches := m.findWindowsByName(identifier)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no window found with ID or name: %s", identifier)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("multiple windows (%d) found with name: %s", len(matches), identifier)
	}

	// Find the index to check if focused
	for i, w := range m.Windows {
		if w.ID == matches[0].ID {
			return m.getWindowInfo(w, i == m.FocusedWindow), nil
		}
	}

	return m.getWindowInfo(matches[0], false), nil
}

// GetFocusedWindowData returns data about the currently focused window.
func (m *OS) GetFocusedWindowData() (map[string]interface{}, error) {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return nil, fmt.Errorf("no window is focused")
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
		return fmt.Errorf("no window found with name: %s", name)
	}

	// Close in reverse order to avoid index shifting issues
	for i := len(m.Windows) - 1; i >= 0; i-- {
		displayName := m.getWindowDisplayName(m.Windows[i])
		if displayName == name {
			m.DeleteWindow(i)
		}
	}
	m.MarkAllDirty()
	return nil
}

// SwitchWorkspace switches to a workspace.
func (m *OS) SwitchWorkspace(workspace int) error {
	if workspace >= 1 && workspace <= m.NumWorkspaces {
		recorder := m.TapeRecorder
		m.TapeRecorder = nil
		m.SwitchToWorkspace(workspace)
		m.TapeRecorder = recorder
		m.MarkAllDirty()
	}
	return nil
}

// ToggleTiling toggles tiling mode.
func (m *OS) ToggleTiling() error {
	m.AutoTiling = !m.AutoTiling
	if m.AutoTiling {
		m.TileAllWindows()
	}
	m.MarkAllDirty()
	return nil
}

// SetMode sets the interaction mode.
func (m *OS) SetMode(mode string) error {
	switch mode {
	case "terminal", "Terminal", "TerminalMode":
		m.Mode = TerminalMode
		if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
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

// FocusWindowByName focuses a window by name. Errors if multiple windows match.
func (m *OS) FocusWindowByName(name string) error {
	win, err := m.findSingleWindowByName(name)
	if err != nil {
		return err
	}
	return m.FocusWindowByID(win.ID)
}

// RenameWindowByID renames a window by its ID (sets CustomName).
func (m *OS) RenameWindowByID(windowID, name string) error {
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
	if !m.AutoTiling {
		m.AutoTiling = true
		m.TileAllWindows()
		m.MarkAllDirty()
	}
	return nil
}

// DisableTiling disables tiling mode.
func (m *OS) DisableTiling() error {
	m.AutoTiling = false
	m.MarkAllDirty()
	return nil
}

// SnapByDirection snaps a window to a direction.
func (m *OS) SnapByDirection(direction string) error {
	if m.AutoTiling {
		return fmt.Errorf("cannot snap windows while tiling mode is enabled")
	}

	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return nil
	}

	quarter := SnapTopLeft
	switch direction {
	case "left":
		quarter = SnapLeft
	case "right":
		quarter = SnapRight
	case "fullscreen":
		m.Snap(m.FocusedWindow, SnapTopLeft)
		m.MarkAllDirty()
		return nil
	}

	m.Snap(m.FocusedWindow, quarter)
	m.MarkAllDirty()
	return nil
}

// MoveWindowToWorkspaceByID moves a window to a workspace.
func (m *OS) MoveWindowToWorkspaceByID(windowID string, workspace int) error {
	if workspace < 1 || workspace > m.NumWorkspaces {
		return fmt.Errorf("workspace %d out of range (1-%d)", workspace, m.NumWorkspaces)
	}

	for i, w := range m.Windows {
		if w.ID == windowID {
			m.MoveWindowToWorkspace(i, workspace)
			return nil
		}
	}

	return fmt.Errorf("window not found: %s", windowID)
}

// MoveAndFollowWorkspaceByID moves a window to a workspace and switches to it.
func (m *OS) MoveAndFollowWorkspaceByID(windowID string, workspace int) error {
	if workspace < 1 || workspace > m.NumWorkspaces {
		return fmt.Errorf("workspace %d out of range (1-%d)", workspace, m.NumWorkspaces)
	}

	for i, w := range m.Windows {
		if w.ID == windowID {
			m.MoveWindowToWorkspaceAndFollow(i, workspace)
			return nil
		}
	}

	return fmt.Errorf("window not found: %s", windowID)
}

// SplitHorizontal splits the focused window horizontally.
func (m *OS) SplitHorizontal() error {
	if !m.AutoTiling {
		return nil
	}
	m.SplitFocusedHorizontal()
	m.MarkAllDirty()
	return nil
}

// SplitVertical splits the focused window vertically.
func (m *OS) SplitVertical() error {
	if !m.AutoTiling {
		return nil
	}
	m.SplitFocusedVertical()
	m.MarkAllDirty()
	return nil
}

// RotateSplit rotates the split direction at the focused window.
func (m *OS) RotateSplit() error {
	if !m.AutoTiling {
		return nil
	}
	m.RotateFocusedSplit()
	m.MarkAllDirty()
	return nil
}

// EqualizeSplitsExec equalizes all split ratios.
func (m *OS) EqualizeSplitsExec() error {
	if !m.AutoTiling {
		return nil
	}
	m.EqualizeSplits()
	m.MarkAllDirty()
	return nil
}

// Preselect sets the preselection direction for the next window.
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

// EnableAnimations enables UI animations.
func (m *OS) EnableAnimations() error {
	config.AnimationsEnabled = true
	m.ShowNotification("Animations: ON", "info", config.NotificationDuration)
	return nil
}

// DisableAnimations disables UI animations.
func (m *OS) DisableAnimations() error {
	config.AnimationsEnabled = false
	m.ShowNotification("Animations: OFF", "info", config.NotificationDuration)
	return nil
}

// ToggleAnimations toggles UI animations.
func (m *OS) ToggleAnimations() error {
	config.AnimationsEnabled = !config.AnimationsEnabled
	if config.AnimationsEnabled {
		m.ShowNotification("Animations: ON", "info", config.NotificationDuration)
	} else {
		m.ShowNotification("Animations: OFF", "info", config.NotificationDuration)
	}
	return nil
}

// SetConfig sets a configuration option at runtime.
// Supported paths: appearance.dockbar_position, appearance.border_style,
// appearance.animations_enabled, appearance.hide_window_buttons
func (m *OS) SetConfig(path, value string) error {
	switch path {
	case "appearance.dockbar_position", "dockbar_position":
		return m.SetDockbarPosition(value)
	case "appearance.border_style", "border_style":
		return m.SetBorderStyle(value)
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
			config.HideWindowButtons = true
		case "false", "off", "0":
			config.HideWindowButtons = false
		}
		m.MarkAllDirty()
		return nil
	default:
		return fmt.Errorf("unknown config path: %s", path)
	}
}

// SetTheme changes the active theme.
func (m *OS) SetTheme(themeName string) error {
	// Initialize the new theme
	if err := theme.Initialize(themeName); err != nil {
		return fmt.Errorf("failed to set theme: %w", err)
	}

	// Update terminal colors for all windows
	for _, w := range m.Windows {
		if w != nil && w.Terminal != nil {
			if theme.IsEnabled() {
				w.Terminal.SetThemeColors(
					theme.TerminalFg(),
					nil, // Always use transparent background
					theme.TerminalCursor(),
					theme.GetANSIPalette(),
				)
			} else {
				// Disable theme colors
				w.Terminal.SetThemeColors(nil, nil, nil, [16]color.Color{})
			}
			w.InvalidateCache()
		}
	}

	m.ShowNotification(fmt.Sprintf("Theme: %s", themeName), "info", config.NotificationDuration)
	m.MarkAllDirty()
	return nil
}

// SetDockbarPosition changes the dockbar position.
func (m *OS) SetDockbarPosition(position string) error {
	switch position {
	case "top", "bottom", "hidden":
		config.DockbarPosition = position
		m.ShowNotification(fmt.Sprintf("Dockbar: %s", position), "info", config.NotificationDuration)
		m.MarkAllDirty()
		return nil
	default:
		return fmt.Errorf("invalid dockbar position: %s (use: top, bottom, hidden)", position)
	}
}

// SetBorderStyle changes the window border style.
func (m *OS) SetBorderStyle(style string) error {
	switch style {
	case "rounded", "normal", "thick", "double", "hidden", "block", "ascii":
		config.BorderStyle = style
		m.ShowNotification(fmt.Sprintf("Border: %s", style), "info", config.NotificationDuration)
		m.MarkAllDirty()
		return nil
	default:
		return fmt.Errorf("invalid border style: %s (use: rounded, normal, thick, double, hidden, block, ascii)", style)
	}
}

// ShowNotificationCmd displays a notification in the UI.
func (m *OS) ShowNotificationCmd(message, notificationType string) error {
	m.ShowNotification(message, notificationType, config.NotificationDuration)
	return nil
}

// FocusDirection focuses a window in a direction (for BSP tiling).
func (m *OS) FocusDirection(direction string) error {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return nil
	}

	focusedWindow := m.Windows[m.FocusedWindow]
	targetIndex := -1

	switch direction {
	case "left":
		targetIndex = m.findWindowInDirection(focusedWindow, -1, 0)
	case "right":
		targetIndex = m.findWindowInDirection(focusedWindow, 1, 0)
	case "up":
		targetIndex = m.findWindowInDirection(focusedWindow, 0, -1)
	case "down":
		targetIndex = m.findWindowInDirection(focusedWindow, 0, 1)
	default:
		return fmt.Errorf("invalid direction: %s (use: left, right, up, down)", direction)
	}

	if targetIndex >= 0 {
		m.FocusWindow(targetIndex)
		m.MarkAllDirty()
	}

	return nil
}

// handleRemoteSendKeys processes key sequences for TUIOS.
// When literal=true, keys are sent directly to the focused terminal PTY.
// When raw=true, each character is treated as a separate key (no splitting on space/comma).
// When both are false, keys are parsed as space/comma separated tokens.
// Returns a tea.Cmd if additional processing is needed.
//
// Key format (when literal=false and raw=false):
//   - Single keys: "i", "n", "Enter", "Escape", "Space"
//   - Key combos: "ctrl+b", "alt+1", "shift+Enter"
//   - Sequences (space or comma-separated): "ctrl+b,n" or "ctrl+b n"
//
// startRemoteSendKeys initiates sequential key processing for remote send-keys.
// Keys are processed one at a time via RemoteKeyMsg to allow proper UI updates between keys.
// Animations are disabled during remote key processing to ensure immediate layout updates.
//
// Special key names: Enter, Return, Space, Tab, Escape, Esc, Backspace, Delete,
// Up, Down, Left, Right, Home, End, PageUp, PageDown, F1-F12
func (m *OS) startRemoteSendKeys(keys string, literal bool, raw bool, requestID string) (tea.Cmd, error) {
	if literal {
		// Send directly to the focused terminal PTY
		windowID := m.GetFocusedWindowID()
		if windowID == "" {
			return nil, fmt.Errorf("no focused window")
		}
		return nil, m.SendToWindow(windowID, []byte(keys))
	}

	// Parse and synthesize TUIOS key events
	var keyMsgs []tea.KeyPressMsg
	if raw {
		// Raw mode: each character is a separate key, no splitting
		keyMsgs = m.parseKeysToMessagesRaw(keys)
	} else {
		// Normal mode: split by space/comma
		keyMsgs = m.parseKeysToMessages(keys)
	}
	if len(keyMsgs) == 0 {
		return nil, fmt.Errorf("no valid keys in sequence: %s", keys)
	}

	// Disable animations during remote key processing
	// This ensures immediate layout updates instead of animations that might not complete
	m.ProcessingRemoteKeys = true
	config.AnimationsSuppressed = true

	// Start processing the first key, remaining keys will be processed sequentially
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

// handleRemoteSendKeys sends keystrokes to the focused window (legacy, kept for compatibility).
// Use startRemoteSendKeys for proper sequential processing.
func (m *OS) handleRemoteSendKeys(keys string, literal bool) (tea.Cmd, error) {
	return m.startRemoteSendKeys(keys, literal, false, "")
}

// executeTapeScript parses and executes a tape script remotely.
// Commands are processed one at a time via RemoteTapeCommandMsg.
func (m *OS) executeTapeScript(script string, requestID string) (tea.Cmd, error) {
	// Parse the tape script
	lexer := tape.New(script)
	parser := tape.NewParser(lexer)
	commands := parser.Parse()

	if len(commands) == 0 {
		return nil, fmt.Errorf("tape script has no commands or contains errors")
	}

	// Disable animations during script execution
	m.ProcessingRemoteKeys = true
	config.AnimationsSuppressed = true

	// Set up script mode for progress display
	m.ScriptMode = true
	m.ScriptPaused = false
	m.ScriptFinishedTime = time.Time{}
	// Note: We don't use ScriptPlayer for remote exec - we track progress via message fields

	// Start processing the first command
	totalCmds := len(commands)
	firstCmd := commands[0]
	var remaining []tape.Command
	if len(commands) > 1 {
		remaining = commands[1:]
	}

	return func() tea.Msg {
		return RemoteTapeCommandMsg{
			Command:           firstCmd,
			RemainingCommands: remaining,
			RequestID:         requestID,
			CommandIndex:      0,
			TotalCommands:     totalCmds,
		}
	}, nil
}

// parseKeysToMessagesRaw parses a key sequence treating each character as a separate key.
// No splitting on spaces or commas - useful for typing literal text with spaces.
func (m *OS) parseKeysToMessagesRaw(keys string) []tea.KeyPressMsg {
	var msgs []tea.KeyPressMsg
	for _, char := range keys {
		msg := m.parseKeyToMessage(string(char))
		msgs = append(msgs, msg)
	}
	return msgs
}

// parseKeysToMessages parses a key sequence string into tea.KeyPressMsg events.
// Supports multiple separators: comma, space, or both.
// Special tokens:
//   - $PREFIX or PREFIX: expands to the configured leader key (default: ctrl+b)
func (m *OS) parseKeysToMessages(keys string) []tea.KeyPressMsg {
	var msgs []tea.KeyPressMsg

	// Normalize: replace commas with spaces, then split by whitespace
	// This allows "ctrl+b,q", "ctrl+b q", or "ctrl+b, q" to all work
	normalized := strings.ReplaceAll(keys, ",", " ")
	parts := strings.Fields(normalized)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Handle $PREFIX or PREFIX special token
		if strings.EqualFold(part, "$PREFIX") || strings.EqualFold(part, "PREFIX") {
			// Get the configured leader key
			leaderKey := config.LeaderKey
			if leaderKey == "" {
				leaderKey = "ctrl+b"
			}
			msg := m.parseKeyToMessage(leaderKey)
			msgs = append(msgs, msg)
			continue
		}

		msg := m.parseKeyToMessage(part)
		msgs = append(msgs, msg)
	}

	return msgs
}

// parseKeyToMessage parses a single key or key combo into a tea.KeyPressMsg.
func (m *OS) parseKeyToMessage(key string) tea.KeyPressMsg {
	var mod tea.KeyMod
	var code rune
	var text string

	// Check if it's a key combo (contains +)
	if strings.Contains(key, "+") {
		parts := strings.Split(key, "+")
		keyPart := ""

		for _, part := range parts {
			part = strings.TrimSpace(part)
			switch strings.ToLower(part) {
			case "ctrl":
				mod |= tea.ModCtrl
			case "alt", "opt":
				mod |= tea.ModAlt
			case "shift":
				mod |= tea.ModShift
			case "super", "cmd", "win":
				mod |= tea.ModSuper
			case "meta":
				mod |= tea.ModMeta
			default:
				keyPart = part
			}
		}
		key = keyPart
	}

	// Parse the key itself
	lowerKey := strings.ToLower(key)

	// Check for special keys
	switch lowerKey {
	case "enter", "return":
		code = tea.KeyEnter
	case "space":
		code = tea.KeySpace
		text = " "
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
		// Regular character
		if len(key) == 1 {
			char := rune(key[0])
			// Normalize to lowercase for the code (consistent with how bubbletea handles keys)
			if char >= 'A' && char <= 'Z' {
				code = char - 'A' + 'a' // Convert to lowercase
			} else {
				code = char
			}
			// Only set Text if there are no modifiers (otherwise String() ignores modifiers)
			if mod == 0 {
				text = string(code)
			}
		} else {
			// Unknown key, try as-is
			if len(key) > 0 {
				code = rune(strings.ToLower(key)[0])
				if mod == 0 {
					text = strings.ToLower(key)
				}
			}
		}
	}

	return tea.KeyPressMsg{
		Code: code,
		Text: text,
		Mod:  mod,
	}
}

// findWindowInDirection finds the nearest window in the specified direction.
// dx, dy specify the direction (-1, 0, or 1 for each axis).
func (m *OS) findWindowInDirection(from *terminal.Window, dx, dy int) int {
	targetIndex := -1
	minDistance := m.Width + m.Height // Start with max possible distance

	for i, win := range m.Windows {
		if win == from || win.Workspace != m.CurrentWorkspace || win.Minimized || win.Minimizing {
			continue
		}

		// Check horizontal direction
		if dx != 0 {
			// dx > 0: look for windows to the right
			// dx < 0: look for windows to the left
			if dx > 0 && win.X >= from.X+from.Width-5 {
				// Window is to the right, check vertical overlap
				if win.Y < from.Y+from.Height && win.Y+win.Height > from.Y {
					distance := win.X - (from.X + from.Width)
					if distance < minDistance {
						minDistance = distance
						targetIndex = i
					}
				}
			} else if dx < 0 && win.X+win.Width <= from.X+5 {
				// Window is to the left, check vertical overlap
				if win.Y < from.Y+from.Height && win.Y+win.Height > from.Y {
					distance := from.X - (win.X + win.Width)
					if distance < minDistance {
						minDistance = distance
						targetIndex = i
					}
				}
			}
		}

		// Check vertical direction
		if dy != 0 {
			// dy > 0: look for windows below
			// dy < 0: look for windows above
			if dy > 0 && win.Y >= from.Y+from.Height-5 {
				// Window is below, check horizontal overlap
				if win.X < from.X+from.Width && win.X+win.Width > from.X {
					distance := win.Y - (from.Y + from.Height)
					if distance < minDistance {
						minDistance = distance
						targetIndex = i
					}
				}
			} else if dy < 0 && win.Y+win.Height <= from.Y+5 {
				// Window is above, check horizontal overlap
				if win.X < from.X+from.Width && win.X+win.Width > from.X {
					distance := from.Y - (win.Y + win.Height)
					if distance < minDistance {
						minDistance = distance
						targetIndex = i
					}
				}
			}
		}
	}

	return targetIndex
}
