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
func SetDebugLevel(level DebugLevel) {
	debugMu.Lock()
	defer debugMu.Unlock()
	currentDebugLevel = level
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
	message := fmt.Sprintf(format, args...)

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
func LogMessage(direction string, msg *Message, codec Codec) {
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
	codecName := codec.Type().String()

	if level >= DebugTrace {
		// Full payload dump (truncated for sanity)
		payloadPreview := msg.Payload
		if len(payloadPreview) > 256 {
			payloadPreview = payloadPreview[:256]
		}
		ProtocolLog(DebugTrace, "[%s] %s (%s) %d bytes: %x",
			direction, typeName, codecName, len(msg.Payload), payloadPreview)
	} else {
		ProtocolLog(DebugMessages, "[%s] %s (%s) %d bytes",
			direction, typeName, codecName, len(msg.Payload))
	}
}

// LogMessageDecoded logs a decoded message payload for debugging.
func LogMessageDecoded(direction string, msg *Message, codec Codec) {
	level := GetDebugLevel()
	if level < DebugTrace {
		return
	}

	typeName := MessageTypeName(msg.Type)
	decoded := DebugPayload(msg, codec)
	ProtocolLog(DebugTrace, "[%s] %s: %s", direction, typeName, decoded)
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
	}
	if name, ok := names[t]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", t)
}

// DebugPayload decodes and formats a payload for debugging.
func DebugPayload(msg *Message, codec Codec) string {
	if len(msg.Payload) == 0 {
		return "<empty>"
	}

	switch msg.Type {
	case MsgHello:
		var p HelloPayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("Hello{Version:%q, Term:%q, %dx%d, Codec:%q}",
				p.Version, p.Term, p.Width, p.Height, p.PreferredCodec)
		}
	case MsgWelcome:
		var p WelcomePayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("Welcome{Version:%q, Sessions:%v, Codec:%q}",
				p.Version, p.SessionNames, p.Codec)
		}
	case MsgAttach:
		var p AttachPayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("Attach{Session:%q, Create:%v, %dx%d}",
				p.SessionName, p.CreateNew, p.Width, p.Height)
		}
	case MsgAttached:
		var p AttachedPayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("Attached{Session:%q, ID:%s, %dx%d, Windows:%d}",
				p.SessionName, truncateID(p.SessionID), p.Width, p.Height, p.WindowCount)
		}
	case MsgNew:
		var p NewPayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("New{Session:%q, %dx%d}", p.SessionName, p.Width, p.Height)
		}
	case MsgKill:
		var p KillPayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("Kill{Session:%q}", p.SessionName)
		}
	case MsgResize:
		var p ResizePayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("Resize{%dx%d}", p.Width, p.Height)
		}
	case MsgError:
		var p ErrorPayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("Error{Code:%d, Msg:%q}", p.Code, p.Message)
		}
	case MsgCreatePTY:
		var p CreatePTYPayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("CreatePTY{Title:%q, %dx%d}", p.Title, p.Width, p.Height)
		}
	case MsgPTYCreated:
		var p PTYCreatedPayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("PTYCreated{ID:%s, Title:%q}", truncateID(p.ID), p.Title)
		}
	case MsgClosePTY:
		var p ClosePTYPayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("ClosePTY{ID:%s}", truncateID(p.PTYID))
		}
	case MsgSubscribePTY:
		var p SubscribePTYPayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("SubscribePTY{ID:%s}", truncateID(p.PTYID))
		}
	case MsgUnsubscribePTY:
		var p UnsubscribePTYPayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("UnsubscribePTY{ID:%s}", truncateID(p.PTYID))
		}
	case MsgInput, MsgPTYOutput:
		// Binary data - just show size
		return fmt.Sprintf("<%d bytes>", len(msg.Payload))
	case MsgSessionList:
		var p SessionListPayload
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("SessionList{Count:%d}", len(p.Sessions))
		}
	case MsgStateData:
		var p SessionState
		if err := codec.Decode(msg.Payload, &p); err == nil {
			return fmt.Sprintf("StateData{Name:%q, Windows:%d}", p.Name, len(p.Windows))
		}
	}

	return fmt.Sprintf("<%d bytes>", len(msg.Payload))
}

// truncateID shortens a UUID for display.
func truncateID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
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
