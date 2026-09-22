// Package session provides persistent session management for TUIOS.
package session

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// DebugLevel controls the verbosity of protocol logging.
type DebugLevel int

const (
	// DebugOff disables all debug output.
	DebugOff DebugLevel = iota
	// DebugErrors logs only errors.
	DebugErrors
	// DebugBasic logs connection events and errors.
	DebugBasic
	// DebugMessages logs all messages except high-frequency PTY I/O.
	DebugMessages
	// DebugVerbose logs everything including PTY I/O.
	DebugVerbose
	// DebugTrace logs full payload hex dumps.
	DebugTrace
)

// String returns the string representation of the debug level.
func (d DebugLevel) String() string {
	switch d {
	case DebugOff:
		return "off"
	case DebugErrors:
		return "errors"
	case DebugBasic:
		return "basic"
	case DebugMessages:
		return "messages"
	case DebugVerbose:
		return "verbose"
	case DebugTrace:
		return "trace"
	default:
		return fmt.Sprintf("unknown(%d)", d)
	}
}

// ParseDebugLevel parses a string into a DebugLevel.
func ParseDebugLevel(s string) DebugLevel {
	switch strings.ToLower(s) {
	case "off", "0", "":
		return DebugOff
	case "errors", "error", "1":
		return DebugErrors
	case "basic", "2":
		return DebugBasic
	case "messages", "message", "msg", "3":
		return DebugMessages
	case "verbose", "4":
		return DebugVerbose
	case "trace", "5", "all":
		return DebugTrace
	default:
		return DebugOff
	}
}

// LogEntry represents a single log entry in the ring buffer.
type LogEntry struct {
	Timestamp int64  `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// LogBuffer is a ring buffer for storing recent log entries.
type LogBuffer struct {
	entries []LogEntry
	size    int
	head    int
	count   int
	mu      sync.RWMutex
}

// NewLogBuffer creates a new log buffer with the specified capacity.
func NewLogBuffer(capacity int) *LogBuffer {
	return &LogBuffer{
		entries: make([]LogEntry, capacity),
		size:    capacity,
	}
}

// Add adds a new entry to the buffer.
func (b *LogBuffer) Add(level, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry := LogEntry{
		Timestamp: timeNow().UnixMilli(),
		Level:     level,
		Message:   message,
	}

	b.entries[b.head] = entry
	b.head = (b.head + 1) % b.size
	if b.count < b.size {
		b.count++
	}
}

// GetAll returns all entries in chronological order.
func (b *LogBuffer) GetAll() []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.count == 0 {
		return nil
	}

	result := make([]LogEntry, b.count)
	start := (b.head - b.count + b.size) % b.size

	for i := range b.count {
		result[i] = b.entries[(start+i)%b.size]
	}

	return result
}

// GetLast returns the last n entries in chronological order.
func (b *LogBuffer) GetLast(n int) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if n > b.count {
		n = b.count
	}
	if n == 0 {
		return nil
	}

	result := make([]LogEntry, n)
	start := (b.head - n + b.size) % b.size

	for i := range n {
		result[i] = b.entries[(start+i)%b.size]
	}

	return result
}

// Clear clears the buffer.
func (b *LogBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.head = 0
	b.count = 0
}

// timeNow is a variable to allow testing
var timeNow = time.Now

var (
	currentDebugLevel DebugLevel = DebugOff
	debugMu           sync.RWMutex
	debugLogger       *log.Logger
	logBuffer         *LogBuffer
)

func init() {
	// Initialize default logger
	debugLogger = log.New(os.Stderr, "[TUIOS] ", log.LstdFlags|log.Lmicroseconds)

	// Initialize log buffer with 1000 entries
	logBuffer = NewLogBuffer(1000)

	// Check environment variable for initial debug level
	if level := os.Getenv("TUIOS_LOG_LEVEL"); level != "" {
		SetDebugLevel(ParseDebugLevel(level))
	}
}

// SetDebugLevel sets the global debug level.
//
// Levels through messages record identifiers, sizes, counts, states and error
// text. Verbose and trace also record pane content, window titles and paths, so
// raising the level to one of those says so once. A machine caller that turns
// the level up to reproduce a fault gets the same notice as a person, in the
// same place it will read the capture back from.
func SetDebugLevel(level DebugLevel) {
	debugMu.Lock()
	previous := currentDebugLevel
	currentDebugLevel = level
	debugMu.Unlock()

	if level >= DebugVerbose && previous < DebugVerbose {
		logRaw(DebugErrors, "[WARNING] Log level "+level.String()+
			" records pane content, window titles and paths. Set the level to messages or lower when you finish.")
	}
}

// GetDebugLevel returns the current debug level.
func GetDebugLevel() DebugLevel {
	debugMu.RLock()
	defer debugMu.RUnlock()
	return currentDebugLevel
}

// SetDebugOutput sets the output writer for debug logs.
func SetDebugOutput(w io.Writer) {
	debugMu.Lock()
	defer debugMu.Unlock()
	debugLogger = log.New(w, "[TUIOS] ", log.LstdFlags|log.Lmicroseconds)
}

// ProtocolLog logs a message at the specified level.
func ProtocolLog(level DebugLevel, format string, args ...any) {
	logRaw(level, fmt.Sprintf(format, args...))
}

// logRaw records one already-formatted line. Every sink the daemon has is fed
// from here, so a line cannot reach the ring and miss the file.
//
// It takes the message rather than a format string because the callers that do
// not own their text (the standard library logger, an error string) would have
// their per-cent signs read as verbs.
func logRaw(level DebugLevel, message string) {
	// Always store in buffer (regardless of debug level)
	if logBuffer != nil {
		logBuffer.Add(level.String(), message)
	}

	// Only print if debug level is high enough
	if GetDebugLevel() >= level {
		debugMu.RLock()
		logger := debugLogger
		debugMu.RUnlock()
		logger.Print(message)
	}

	// The file keeps errors and basic events whatever the level, and follows the
	// level for anything above them.
	if alwaysFile(level) || GetDebugLevel() >= level {
		writeDaemonLogFile(level.String(), message)
	}
}

// LogError logs an error message (always logged if level >= DebugErrors).
func LogError(format string, args ...any) {
	ProtocolLog(DebugErrors, "[ERROR] "+format, args...)
}

// LogBasic logs a basic message (connections, disconnections).
func LogBasic(format string, args ...any) {
	ProtocolLog(DebugBasic, format, args...)
}

// LogMessage logs a protocol message with appropriate detail.
func LogMessage(direction string, msg *Message) {
	level := GetDebugLevel()
	if level < DebugMessages {
		return
	}

	// Skip high-frequency messages unless verbose
	if level < DebugVerbose {
		switch msg.Type {
		case MsgPTYOutput, MsgInput:
			return // Skip PTY I/O at normal message level
		}
	}

	typeName := MessageTypeName(msg.Type)

	if level >= DebugTrace {
		// Full payload dump (truncated for sanity)
		payloadPreview := msg.Payload
		if len(payloadPreview) > 256 {
			payloadPreview = payloadPreview[:256]
		}
		ProtocolLog(DebugTrace, "[%s] %s %d bytes: %x",
			direction, typeName, len(msg.Payload), payloadPreview)
	} else {
		ProtocolLog(DebugMessages, "[%s] %s %d bytes",
			direction, typeName, len(msg.Payload))
	}
}

// MessageTypeName returns a human-readable name for a message type.
func MessageTypeName(t MessageType) string {
	names := map[MessageType]string{
		MsgHello:            "Hello",
		MsgAttach:           "Attach",
		MsgDetach:           "Detach",
		MsgNew:              "New",
		MsgList:             "List",
		MsgKill:             "Kill",
		MsgInput:            "Input",
		MsgResize:           "Resize",
		MsgPing:             "Ping",
		MsgCreatePTY:        "CreatePTY",
		MsgReadDir:          "ReadDir",
		MsgDirListing:       "DirListing",
		MsgClosePTY:         "ClosePTY",
		MsgListPTYs:         "ListPTYs",
		MsgFocusPTY:         "FocusPTY",
		MsgGetState:         "GetState",
		MsgUpdateState:      "UpdateState",
		MsgSubscribePTY:     "SubscribePTY",
		MsgGetTerminalState: "GetTerminalState",
		MsgExecuteCommand:   "ExecuteCommand",
		MsgSendKeys:         "SendKeys",
		MsgSetConfig:        "SetConfig",
		MsgWelcome:          "Welcome",
		MsgAttached:         "Attached",
		MsgDetached:         "Detached",
		MsgSessionList:      "SessionList",
		MsgOutput:           "Output",
		MsgError:            "Error",
		MsgPong:             "Pong",
		MsgSessionEnded:     "SessionEnded",
		MsgWindowChanged:    "WindowChanged",
		MsgPTYList:          "PTYList",
		MsgPTYCreated:       "PTYCreated",
		MsgPTYClosed:        "PTYClosed",
		MsgPTYOutput:        "PTYOutput",
		MsgStateData:        "StateData",
		MsgTerminalState:    "TerminalState",
		MsgCommandResult:    "CommandResult",
		MsgRemoteCommand:    "RemoteCommand",
		MsgGetLogs:          "GetLogs",
		MsgLogsData:         "LogsData",
		MsgHostsChanged:     "HostsChanged",
	}
	if name, ok := names[t]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", t)
}

// GetLogEntries returns the last n log entries.
// If n <= 0, returns all entries.
func GetLogEntries(n int) []LogEntry {
	if logBuffer == nil {
		return nil
	}
	if n <= 0 {
		return logBuffer.GetAll()
	}
	return logBuffer.GetLast(n)
}

// ClearLogBuffer clears all log entries.
func ClearLogBuffer() {
	if logBuffer != nil {
		logBuffer.Clear()
	}
}
