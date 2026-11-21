package tape

import (
	"fmt"
	"strings"
	"time"
)

// Recorder records user interactions as tape commands
type Recorder struct {
	commands         []Command
	startTime        time.Time
	lastEventTime    time.Time
	enabled          bool
	minDelayMs       int
	typingBuffer     string
	initialMode      string
	initialWorkspace int
	initialTiling    bool
}

// NewRecorder creates a new tape recorder
func NewRecorder() *Recorder {
	return &Recorder{
		commands:      []Command{},
		startTime:     time.Now(),
		lastEventTime: time.Now(),
		enabled:       false,
		minDelayMs:    10,
	}
}

// Start begins recording
func (r *Recorder) Start() {
	r.enabled = true
	r.startTime = time.Now()
	r.lastEventTime = time.Now()
	r.commands = []Command{}
}

// StartWithState begins recording and records the initial state
func (r *Recorder) StartWithState(mode string, workspace int, tilingEnabled bool) {
	r.enabled = true
	r.startTime = time.Now()
	r.lastEventTime = time.Now()
	r.commands = []Command{}
	r.initialMode = mode
	r.initialWorkspace = workspace
	r.initialTiling = tilingEnabled

	if workspace > 1 {
		r.commands = append(r.commands, Command{
			Type:   CommandTypeSwitchWS,
			Args:   []string{fmt.Sprintf("%d", workspace)},
			Line:   1,
			Column: 1,
			Raw:    fmt.Sprintf("SwitchWorkspace %d", workspace),
		})
	}

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
	r.flushTypingBuffer()
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

	r.flushTypingBuffer()

	now := time.Now()
	delay := now.Sub(r.lastEventTime)

	cmd := r.keyToCommand(key)
	if cmd != nil {
		cmd.Delay = delay
		r.commands = append(r.commands, *cmd)
		r.lastEventTime = now
	}
}

// RecordType records typed text, accumulating consecutive characters.
func (r *Recorder) RecordType(text string) {
	if !r.enabled {
		return
	}

	r.typingBuffer += text
	r.lastEventTime = time.Now()
}

func (r *Recorder) flushTypingBuffer() {
	if r.typingBuffer == "" {
		return
	}

	cmd := Command{
		Type:   CommandTypeType,
		Args:   []string{r.typingBuffer},
		Delay:  0,
		Line:   len(r.commands) + 1,
		Column: 1,
		Raw:    fmt.Sprintf("Type %q", r.typingBuffer),
	}

	r.commands = append(r.commands, cmd)
	r.typingBuffer = ""
}

// RecordModeSwitch records a mode switch command and flushes the typing buffer
func (r *Recorder) RecordModeSwitch(cmdType CommandType) {
	if !r.enabled {
		return
	}

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

var actionToCommand = map[string]struct {
	cmdType CommandType
	raw     string
}{
	"new_window":      {CommandTypeNewWindow, "NewWindow"},
	"close_window":    {CommandTypeCloseWindow, "CloseWindow"},
	"next_window":     {CommandTypeNextWindow, "NextWindow"},
	"prev_window":     {CommandTypePrevWindow, "PrevWindow"},
	"minimize_window": {CommandTypeMinimizeWindow, "MinimizeWindow"},
	"toggle_tiling":   {CommandTypeToggleTiling, "ToggleTiling"},
}

func recordableAction(action string) bool {
	switch action {
	case "toggle_tape_manager", "stop_recording", "script_pause",
		"enter_terminal_mode", "enter_window_mode", "terminal_exit_mode", "prefix_exit_mode", "hold_window_mode",
		"prefix_workspace", "prefix_minimize", "prefix_window", "prefix_debug", "prefix_tape", "prefix_layout":
		return false
	}
	for _, prefix := range []string{"tape_prefix_", "switch_workspace_"} {
		if strings.HasPrefix(action, prefix) {
			return false
		}
	}
	return !strings.HasSuffix(action, "_cancel")
}

// RecordAction records a window management action
func (r *Recorder) RecordAction(action string, args ...string) {
	if !r.enabled {
		return
	}

	if !recordableAction(action) {
		return
	}

	r.flushTypingBuffer()

	now := time.Now()
	delay := now.Sub(r.lastEventTime)

	var cmdType CommandType
	var raw string

	if mapping, ok := actionToCommand[action]; ok {
		cmdType = mapping.cmdType
		raw = mapping.raw
	} else {
		cmdType = CommandTypeAction
		raw = "Action " + action
		args = []string{action}
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

// GetCommands returns all recorded commands
func (r *Recorder) GetCommands() []Command {
	return r.commands
}

// String returns the tape content as a formatted string
func (r *Recorder) String(header string) string {
	var sb strings.Builder

	if header != "" {
		fmt.Fprintf(&sb, "# %s\n", header)
		fmt.Fprintf(&sb, "# Recorded: %s\n\n", r.startTime.Format(time.RFC3339))
	}

	sb.WriteString("# Disable animations for consistent playback\n")
	sb.WriteString("DisableAnimations\n\n")

	for _, cmd := range r.commands {
		if cmd.Delay > 0 && cmd.Delay.Milliseconds() > 100 {
			fmt.Fprintf(&sb, "Sleep %v\n", cmd.Delay.Round(time.Millisecond))
		}

		sb.WriteString(cmd.Raw)
		sb.WriteByte('\n')
	}

	sb.WriteString("\n# Re-enable animations\n")
	sb.WriteString("EnableAnimations\n")

	return sb.String()
}

// CommandCount returns the number of recorded commands
func (r *Recorder) CommandCount() int {
	return len(r.commands)
}

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
		if isModifierCombo(key) {
			cmdType = CommandTypeKeyCombo
			raw = key
		} else if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			cmdType = CommandTypeType
			raw = fmt.Sprintf("Type %q", key)
			return &Command{
				Type:   cmdType,
				Args:   []string{key},
				Line:   len(r.commands) + 1,
				Column: 1,
				Raw:    raw,
			}
		} else {
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

func isModifierCombo(key string) bool {
	return len(key) > 0 && ((len(key) > 5 && key[:5] == "ctrl+") ||
		(len(key) > 4 && key[:4] == "alt+") ||
		(len(key) > 6 && key[:6] == "shift+"))
}

// Clear clears all recorded commands
func (r *Recorder) Clear() {
	r.commands = []Command{}
	r.typingBuffer = ""
	r.startTime = time.Now()
	r.lastEventTime = time.Now()
}
