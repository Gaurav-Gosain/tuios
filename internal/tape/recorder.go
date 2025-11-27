package tape

import (
	"fmt"
	"os"
	"time"
)

// Recorder records user interactions as tape commands
type Recorder struct {
	commands      []Command
	startTime     time.Time
	lastEventTime time.Time
	enabled       bool
	minDelayMs    int // Minimum delay to record between events (to filter out very fast inputs)
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

// Stop ends recording
func (r *Recorder) Stop() {
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

	// Calculate delay since last event
	now := time.Now()
	delay := now.Sub(r.lastEventTime)

	// Check minimum delay to avoid recording too many events
	if delay.Milliseconds() < int64(r.minDelayMs) {
		return
	}

	// Convert key to command
	cmd := r.keyToCommand(key)
	if cmd != nil {
		cmd.Delay = delay
		r.commands = append(r.commands, *cmd)
		r.lastEventTime = now
	}
}

// RecordType records typing text
func (r *Recorder) RecordType(text string) {
	if !r.enabled {
		return
	}

	now := time.Now()
	delay := now.Sub(r.lastEventTime)

	cmd := Command{
		Type:   CommandTypeType,
		Args:   []string{text},
		Delay:  delay,
		Line:   len(r.commands) + 1,
		Column: 1,
		Raw:    fmt.Sprintf(`Type "%s"`, text),
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
	result := ""

	if header != "" {
		// Add header with timestamp
		result += fmt.Sprintf("# %s\n", header)
		result += fmt.Sprintf("# Recorded: %s\n\n", r.startTime.Format(time.RFC3339))
	}

	// Write commands
	for _, cmd := range r.commands {
		if cmd.Delay > 0 && cmd.Delay.Milliseconds() > 100 {
			result += fmt.Sprintf("Sleep %v\n", cmd.Delay)
		}

		result += cmd.Raw + "\n"
	}

	return result
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
	r.startTime = time.Now()
	r.lastEventTime = time.Now()
}
