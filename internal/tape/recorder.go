package tape

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Recorder records user interactions as tape commands
type Recorder struct {
	commands         []Command
	startTime        time.Time
	lastEventTime    time.Time
	enabled          bool
	minDelayMs       int    // Minimum delay to record between events (to filter out very fast inputs)
	typingBuffer     string // Buffer for accumulating typed characters
	initialMode      string // Initial mode when recording started
	initialWorkspace int    // Initial workspace when recording started
	initialTiling    bool   // Initial tiling state when recording started
}

// NewRecorder creates a new tape recorder
func NewRecorder() *Recorder {
	return &Recorder{
		commands:      []Command{},
		startTime:     time.Now(),
		lastEventTime: time.Now(),
		enabled:       false,
		minDelayMs:    10, // Min 10ms between recorded events
	}
}

// Start begins recording
func (r *Recorder) Start() {
	r.enabled = true
	r.startTime = time.Now()
	r.lastEventTime = time.Now()
	r.commands = []Command{} // Reset commands
}

// StartWithState begins recording and records the initial state
func (r *Recorder) StartWithState(mode string, workspace int, tilingEnabled bool) {
	r.enabled = true
	r.startTime = time.Now()
	r.lastEventTime = time.Now()
	r.commands = []Command{} // Reset commands
	r.initialMode = mode
	r.initialWorkspace = workspace
	r.initialTiling = tilingEnabled

	// Record initial workspace if not workspace 1
	if workspace > 1 {
		r.commands = append(r.commands, Command{
			Type:   CommandTypeSwitchWS,
			Args:   []string{fmt.Sprintf("%d", workspace)},
			Line:   1,
			Column: 1,
			Raw:    fmt.Sprintf("SwitchWorkspace %d", workspace),
		})
	}

	// Record initial tiling state
	if tilingEnabled {
		r.commands = append(r.commands, Command{
			Type:   CommandTypeEnableTiling,
			Args:   []string{},
			Line:   len(r.commands) + 1,
			Column: 1,
			Raw:    "EnableTiling",
		})
	} else {
		r.commands = append(r.commands, Command{
			Type:   CommandTypeDisableTiling,
			Args:   []string{},
			Line:   len(r.commands) + 1,
			Column: 1,
			Raw:    "DisableTiling",
		})
	}

	// Record initial mode
	if mode == "terminal" {
		r.commands = append(r.commands, Command{
			Type:   CommandTypeTerminalMode,
			Args:   []string{},
			Line:   len(r.commands) + 1,
			Column: 1,
			Raw:    "TerminalMode",
		})
	} else {
		r.commands = append(r.commands, Command{
			Type:   CommandTypeWindowManagementMode,
			Args:   []string{},
			Line:   len(r.commands) + 1,
			Column: 1,
			Raw:    "WindowManagementMode",
		})
	}
}

// Stop ends recording
func (r *Recorder) Stop() {
	r.flushTypingBuffer() // Flush any pending typed text
	r.enabled = false
}

// IsRecording returns whether recording is active
func (r *Recorder) IsRecording() bool {
	return r.enabled
}

// RecordKey records a key press event
func (r *Recorder) RecordKey(key string) {
	if !r.enabled {
		return
	}

	// Flush any pending typed text first
	r.flushTypingBuffer()

	// Calculate delay since last event
	now := time.Now()
	delay := now.Sub(r.lastEventTime)

	// Convert key to command
	cmd := r.keyToCommand(key)
	if cmd != nil {
		cmd.Delay = delay
		r.commands = append(r.commands, *cmd)
		r.lastEventTime = now
	}
}

// RecordType records typing text - accumulates consecutive characters
func (r *Recorder) RecordType(text string) {
	if !r.enabled {
		return
	}

	// Accumulate typed characters
	r.typingBuffer += text
	r.lastEventTime = time.Now()
}

// flushTypingBuffer writes accumulated typed text as a Type command
func (r *Recorder) flushTypingBuffer() {
	if r.typingBuffer == "" {
		return
	}

	cmd := Command{
		Type:   CommandTypeType,
		Args:   []string{r.typingBuffer},
		Delay:  0, // Delay is captured between commands
		Line:   len(r.commands) + 1,
		Column: 1,
		Raw:    fmt.Sprintf(`Type "%s"`, r.typingBuffer),
	}

	r.commands = append(r.commands, cmd)
	r.typingBuffer = ""
}

// RecordModeSwitch records a mode switch command and flushes the typing buffer
func (r *Recorder) RecordModeSwitch(cmdType CommandType) {
	if !r.enabled {
		return
	}

	// Flush any pending typed text first
	r.flushTypingBuffer()

	now := time.Now()
	delay := now.Sub(r.lastEventTime)

	raw := string(cmdType)
	cmd := Command{
		Type:   cmdType,
		Args:   []string{},
		Delay:  delay,
		Line:   len(r.commands) + 1,
		Column: 1,
		Raw:    raw,
	}

	r.commands = append(r.commands, cmd)
	r.lastEventTime = now
}

// actionToCommand maps action names to tape command types and raw output
var actionToCommand = map[string]struct {
	cmdType CommandType
	raw     string
}{
	"new_window":      {CommandTypeNewWindow, "NewWindow"},
	"close_window":    {CommandTypeCloseWindow, "CloseWindow"},
	"next_window":     {CommandTypeNextWindow, "NextWindow"},
	"prev_window":     {CommandTypePrevWindow, "PrevWindow"},
	"minimize_window": {CommandTypeMinimizeWindow, "MinimizeWindow"},
	"restore_all":     {CommandTypeRestoreWindow, "RestoreWindow"},
	"toggle_tiling":   {CommandTypeToggleTiling, "ToggleTiling"},
	"snap_left":       {CommandTypeSnapLeft, "SnapLeft"},
	"snap_right":      {CommandTypeSnapRight, "SnapRight"},
	"snap_fullscreen": {CommandTypeSnapFullscreen, "SnapFullscreen"},
}

// RecordAction records a window management action
func (r *Recorder) RecordAction(action string, args ...string) {
	if !r.enabled {
		return
	}

	// Skip tape control actions and mode switch actions (mode switches are recorded separately)
	switch action {
	case "toggle_tape_manager", "stop_recording", "enter_terminal_mode", "enter_window_mode":
		return
	}

	// Flush any pending typed text first
	r.flushTypingBuffer()

	now := time.Now()
	delay := now.Sub(r.lastEventTime)

	var cmdType CommandType
	var raw string

	// Check if we have a mapping for this action
	if mapping, ok := actionToCommand[action]; ok {
		cmdType = mapping.cmdType
		raw = mapping.raw
	} else if len(action) > 17 && action[:17] == "switch_workspace_" {
		// Handle workspace switching: switch_workspace_1 -> SwitchWorkspace 1
		ws := action[17:]
		cmdType = CommandTypeSwitchWS
		raw = "SwitchWorkspace " + ws
		args = []string{ws}
	} else if len(action) > 14 && action[:14] == "select_window_" {
		// Handle window selection: select_window_1 -> FocusWindow 1
		win := action[14:]
		cmdType = CommandTypeFocusWindow
		raw = "FocusWindow " + win
		args = []string{win}
	} else {
		// Unknown action, skip
		return
	}

	cmd := Command{
		Type:   cmdType,
		Args:   args,
		Delay:  delay,
		Line:   len(r.commands) + 1,
		Column: 1,
		Raw:    raw,
	}

	r.commands = append(r.commands, cmd)
	r.lastEventTime = now
}

// RecordWorkspaceSwitch records a workspace switch command
func (r *Recorder) RecordWorkspaceSwitch(workspace int) {
	if !r.enabled {
		return
	}

	// Flush any pending typed text first
	r.flushTypingBuffer()

	now := time.Now()
	delay := now.Sub(r.lastEventTime)

	cmd := Command{
		Type:   CommandTypeSwitchWS,
		Args:   []string{fmt.Sprintf("%d", workspace)},
		Delay:  delay,
		Line:   len(r.commands) + 1,
		Column: 1,
		Raw:    fmt.Sprintf("SwitchWorkspace %d", workspace),
	}

	r.commands = append(r.commands, cmd)
	r.lastEventTime = now
}

// RecordSleep explicitly records a sleep command
func (r *Recorder) RecordSleep(duration time.Duration) {
	if !r.enabled {
		return
	}

	now := time.Now()
	cmd := Command{
		Type:   CommandTypeSleep,
		Args:   []string{duration.String()},
		Delay:  duration,
		Line:   len(r.commands) + 1,
		Column: 1,
		Raw:    fmt.Sprintf("Sleep %v", duration),
	}

	r.commands = append(r.commands, cmd)
	r.lastEventTime = now
}

// GetCommands returns all recorded commands
func (r *Recorder) GetCommands() []Command {
	return r.commands
}

// WriteToFile saves the recorded tape to a file
func (r *Recorder) WriteToFile(filename string, header string) error {
	content := r.String(header)
	return writeFile(filename, content)
}

// String returns the tape content as a formatted string
func (r *Recorder) String(header string) string {
	var sb strings.Builder

	if header != "" {
		// Add header with timestamp
		fmt.Fprintf(&sb, "# %s\n", header)
		fmt.Fprintf(&sb, "# Recorded: %s\n\n", r.startTime.Format(time.RFC3339))
	}

	// Always start with DisableAnimations for reproducibility
	// This ensures tape playback is consistent regardless of user's animation settings
	sb.WriteString("# Disable animations for consistent playback\n")
	sb.WriteString("DisableAnimations\n\n")

	// Write commands
	for _, cmd := range r.commands {
		if cmd.Delay > 0 && cmd.Delay.Milliseconds() > 100 {
			fmt.Fprintf(&sb, "Sleep %v\n", cmd.Delay)
		}

		sb.WriteString(cmd.Raw)
		sb.WriteByte('\n')
	}

	// Re-enable animations at the end to restore user's preference
	sb.WriteString("\n# Re-enable animations\n")
	sb.WriteString("EnableAnimations\n")

	return sb.String()
}

// CommandCount returns the number of recorded commands
func (r *Recorder) CommandCount() int {
	return len(r.commands)
}

// keyToCommand converts a key string to a Command
func (r *Recorder) keyToCommand(key string) *Command {
	var cmdType CommandType
	var raw string

	switch key {
	case "enter":
		cmdType = CommandTypeEnter
		raw = "Enter"
	case " ":
		cmdType = CommandTypeSpace
		raw = "Space"
	case "backspace":
		cmdType = CommandTypeBackspace
		raw = "Backspace"
	case "delete":
		cmdType = CommandTypeDelete
		raw = "Delete"
	case "tab":
		cmdType = CommandTypeTab
		raw = "Tab"
	case "esc":
		cmdType = CommandTypeEscape
		raw = "Escape"
	case "up":
		cmdType = CommandTypeUp
		raw = "Up"
	case "down":
		cmdType = CommandTypeDown
		raw = "Down"
	case "left":
		cmdType = CommandTypeLeft
		raw = "Left"
	case "right":
		cmdType = CommandTypeRight
		raw = "Right"
	case "home":
		cmdType = CommandTypeHome
		raw = "Home"
	case "end":
		cmdType = CommandTypeEnd
		raw = "End"
	default:
		// Check if it's a modifier combination
		if isModifierCombo(key) {
			cmdType = CommandTypeKeyCombo
			raw = key
		} else if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			// Single printable character - record as Type command
			cmdType = CommandTypeType
			raw = fmt.Sprintf(`Type "%s"`, key)
			return &Command{
				Type:   cmdType,
				Args:   []string{key},
				Line:   len(r.commands) + 1,
				Column: 1,
				Raw:    raw,
			}
		} else {
			// Unknown key
			return nil
		}
	}

	return &Command{
		Type:   cmdType,
		Args:   []string{},
		Line:   len(r.commands) + 1,
		Column: 1,
		Raw:    raw,
	}
}

// isModifierCombo checks if a key string is a modifier combination
func isModifierCombo(key string) bool {
	// Simple check for Ctrl+, Alt+, Shift+ prefixes
	return len(key) > 0 && ((len(key) > 5 && key[:5] == "ctrl+") ||
		(len(key) > 4 && key[:4] == "alt+") ||
		(len(key) > 6 && key[:6] == "shift+"))
}

// writeFile is a helper to write content to a file
func writeFile(filename string, content string) error {
	return os.WriteFile(filename, []byte(content), 0o644)
}

// RecordingStats contains statistics about the recording
type RecordingStats struct {
	CommandCount int
	Duration     time.Duration
	IsRecording  bool
}

// GetStats returns recording statistics
func (r *Recorder) GetStats() RecordingStats {
	return RecordingStats{
		CommandCount: len(r.commands),
		Duration:     time.Since(r.startTime),
		IsRecording:  r.enabled,
	}
}

// Clear clears all recorded commands
func (r *Recorder) Clear() {
	r.commands = []Command{}
	r.typingBuffer = ""
	r.startTime = time.Now()
	r.lastEventTime = time.Now()
}
