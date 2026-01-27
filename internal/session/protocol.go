// Package session provides persistent session management for TUIOS.
// It implements a daemon-client architecture similar to tmux, allowing
// terminal sessions to persist when the client disconnects.
package session

import (
	"encoding/binary"
	"fmt"
	"io"
)

// MessageType identifies the type of protocol message.
type MessageType uint8

const (
	// Client -> Server messages
	MsgHello            MessageType = iota + 1 // Initial handshake with client info
	MsgAttach                                  // Attach to a session
	MsgDetach                                  // Detach from session (session persists)
	MsgNew                                     // Create a new session
	MsgList                                    // List available sessions
	MsgKill                                    // Kill/terminate a session
	MsgInput                                   // Keyboard/mouse input bytes
	MsgResize                                  // Terminal resize event
	MsgPing                                    // Keepalive ping
	MsgCreatePTY                               // Create new PTY in session
	MsgClosePTY                                // Close a PTY
	MsgListPTYs                                // List PTYs in session
	MsgFocusPTY                                // Switch focus to a PTY
	MsgGetState                                // Get session state
	MsgUpdateState                             // Update session state
	MsgSubscribePTY                            // Subscribe to PTY output
	MsgUnsubscribePTY                          // Unsubscribe from PTY output
	MsgGetTerminalState                        // Get terminal state for a PTY
	MsgExecuteCommand                          // Execute a tape command (routed to TUI)
	MsgSendKeys                                // Send keystrokes to focused window
	MsgSetConfig                               // Set a config option at runtime

	// Server -> Client messages
	MsgWelcome       // Response to Hello with server info
	MsgAttached      // Successfully attached to session
	MsgDetached      // Confirm detach
	MsgSessionList   // List of sessions
	MsgOutput        // Terminal output bytes
	MsgError         // Error message
	MsgPong          // Response to ping
	MsgSessionEnded  // Session terminated
	MsgWindowChanged // Window size changed (from other client)
	MsgPTYList       // List of PTYs in session
	MsgPTYCreated    // New PTY created
	MsgPTYClosed     // PTY closed
	MsgPTYOutput     // Output from a specific PTY
	MsgStateData     // Session state data
	MsgTerminalState // Terminal state for a PTY (screen + scrollback)
	MsgCommandResult // Result of a remote command execution
	MsgRemoteCommand // Remote command from daemon to TUI client for execution
	MsgGetLogs       // Request to retrieve daemon logs
	MsgLogsData      // Response with log entries
	MsgQueryWindows  // Query window list from TUI
	MsgWindowList    // Response with window list
	MsgQuerySession  // Query session info from TUI
	MsgSessionInfo   // Response with session info

	// Multi-client support messages
	MsgStateSync       // Broadcast state update to all clients in session
	MsgClientJoined    // Notification that another client joined the session
	MsgClientLeft      // Notification that another client left the session
	MsgSessionResize   // Session effective size changed (min of all clients)
	MsgForceRefresh    // Force all clients to re-render
	MsgRequestFullSync // Client requests full state sync from leader
)

// Message is the base protocol message structure.
// Wire format (v2): [4 bytes length][1 byte type][1 byte codec][payload]
// The codec byte indicates how the payload is encoded:
//   - 0 = gob (default, binary)
//   - 1 = json (for external clients)
type Message struct {
	Type    MessageType
	Payload []byte
}

// HelloPayload is sent by client on initial connection.
type HelloPayload struct {
	Version        string `json:"version"`                   // Client version
	Term           string `json:"term"`                      // TERM environment variable
	ColorTerm      string `json:"color_term"`                // COLORTERM environment variable
	Shell          string `json:"shell"`                     // Preferred shell
	Width          int    `json:"width"`                     // Terminal width
	Height         int    `json:"height"`                    // Terminal height
	PreferredCodec string `json:"preferred_codec,omitempty"` // "gob" (default) or "json"
	// Graphics capabilities from client's terminal
	PixelWidth    int    `json:"pixel_width,omitempty"`    // Terminal width in pixels
	PixelHeight   int    `json:"pixel_height,omitempty"`   // Terminal height in pixels
	CellWidth     int    `json:"cell_width,omitempty"`     // Cell width in pixels
	CellHeight    int    `json:"cell_height,omitempty"`    // Cell height in pixels
	KittyGraphics bool   `json:"kitty_graphics,omitempty"` // Kitty graphics protocol support
	SixelGraphics bool   `json:"sixel_graphics,omitempty"` // Sixel graphics support
	TerminalName  string `json:"terminal_name,omitempty"`  // Detected terminal (kitty, wezterm, etc.)
}

// WelcomePayload is sent by server in response to Hello.
type WelcomePayload struct {
	Version      string   `json:"version"`       // Server version
	SessionNames []string `json:"session_names"` // Available sessions
	Codec        string   `json:"codec"`         // Negotiated codec: "gob" or "json"
}

// AttachPayload requests attachment to a session.
type AttachPayload struct {
	SessionName string `json:"session_name"`         // Session to attach to (empty = default)
	CreateNew   bool   `json:"create_new,omitempty"` // Create if doesn't exist
	Width       int    `json:"width"`                // Client terminal width
	Height      int    `json:"height"`               // Client terminal height
}

// AttachedPayload confirms successful session attachment.
type AttachedPayload struct {
	SessionName string        `json:"session_name"`    // Attached session name
	SessionID   string        `json:"session_id"`      // Session unique ID
	Width       int           `json:"width"`           // Current session width
	Height      int           `json:"height"`          // Current session height
	WindowCount int           `json:"window_count"`    // Number of windows in session
	State       *SessionState `json:"state,omitempty"` // Session state for restore
}

// NewPayload requests creation of a new session.
type NewPayload struct {
	SessionName string `json:"session_name,omitempty"` // Desired session name (auto-generated if empty)
	Width       int    `json:"width"`                  // Initial terminal width
	Height      int    `json:"height"`                 // Initial terminal height
}

// SessionInfo describes a single session for listing.
type SessionInfo struct {
	Name        string `json:"name"`         // Session name
	ID          string `json:"id"`           // Session unique ID
	Created     int64  `json:"created"`      // Unix timestamp of creation
	LastActive  int64  `json:"last_active"`  // Unix timestamp of last activity
	WindowCount int    `json:"window_count"` // Number of windows
	Attached    bool   `json:"attached"`     // Whether a client is attached
	Width       int    `json:"width"`        // Session width
	Height      int    `json:"height"`       // Session height
}

// SessionListPayload contains list of available sessions.
type SessionListPayload struct {
	Sessions []SessionInfo `json:"sessions"`
}

// KillPayload requests termination of a session.
type KillPayload struct {
	SessionName string `json:"session_name"` // Session to kill
}

// ResizePayload notifies of terminal resize.
type ResizePayload struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// ErrorPayload contains an error message.
type ErrorPayload struct {
	Code    int    `json:"code"`    // Error code
	Message string `json:"message"` // Human-readable error
}

// PTY-related payloads

// PTYInfo describes a single PTY.
type PTYInfo struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Exited bool   `json:"exited"`
}

// PTYListPayload contains list of PTYs in a session.
type PTYListPayload struct {
	PTYs []PTYInfo `json:"ptys"`
}

// CreatePTYPayload requests creation of a new PTY.
type CreatePTYPayload struct {
	Title  string `json:"title,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// PTYCreatedPayload confirms PTY creation.
type PTYCreatedPayload struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ClosePTYPayload requests closing a PTY.
type ClosePTYPayload struct {
	PTYID string `json:"pty_id"`
}

// FocusPTYPayload requests focus on a PTY.
type FocusPTYPayload struct {
	PTYID string `json:"pty_id"`
}

// InputPayload carries input to a specific PTY.
type InputPayload struct {
	PTYID string `json:"pty_id"`
	Data  []byte `json:"data"`
}

// PTYOutputPayload carries output from a specific PTY.
type PTYOutputPayload struct {
	PTYID string `json:"pty_id"`
	Data  []byte `json:"data"`
}

// ResizePTYPayload requests resizing a specific PTY.
type ResizePTYPayload struct {
	PTYID  string `json:"pty_id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// SubscribePTYPayload requests subscribing to PTY output.
type SubscribePTYPayload struct {
	PTYID string `json:"pty_id"`
}

// UnsubscribePTYPayload requests unsubscribing from PTY output.
type UnsubscribePTYPayload struct {
	PTYID string `json:"pty_id"`
}

// GetTerminalStatePayload requests terminal state for a PTY.
type GetTerminalStatePayload struct {
	PTYID              string `json:"pty_id"`
	IncludeScrollback  bool   `json:"include_scrollback,omitempty"`
	MaxScrollbackLines int    `json:"max_scrollback_lines,omitempty"` // 0 = default (1000)
}

// TerminalStatePayload contains the terminal state response.
// Note: TerminalState struct is defined in session.go
type TerminalStatePayload struct {
	PTYID string         `json:"pty_id"`
	State *TerminalState `json:"state"`
}

// ExecuteCommandPayload requests execution of a tape command.
// The command is routed to the TUI client attached to the session.
type ExecuteCommandPayload struct {
	SessionName string   `json:"session_name,omitempty"` // Target session (empty = most recently active)
	CommandType string   `json:"command_type"`           // Tape command type (e.g., "NewWindow", "SwitchWorkspace")
	Args        []string `json:"args,omitempty"`         // Command arguments
	TapeScript  string   `json:"tape_script,omitempty"`  // Raw tape script to execute (alternative to CommandType)
	RequestID   string   `json:"request_id,omitempty"`   // Optional ID for matching responses
}

// SendKeysPayload requests sending keystrokes to a session.
type SendKeysPayload struct {
	SessionName string `json:"session_name,omitempty"` // Target session (empty = most recently active)
	Keys        string `json:"keys"`                   // Key sequence (e.g., "ctrl+b,n" or "Hello World")
	Literal     bool   `json:"literal,omitempty"`      // If true, send keys literally to PTY (no parsing)
	Raw         bool   `json:"raw,omitempty"`          // If true, treat each character as a separate key (no splitting on space/comma)
	RequestID   string `json:"request_id,omitempty"`   // Optional ID for matching responses
}

// SetConfigPayload requests changing a configuration option at runtime.
type SetConfigPayload struct {
	SessionName string `json:"session_name,omitempty"` // Target session (empty = most recently active)
	Path        string `json:"path"`                   // Config path (e.g., "appearance.dockbar_position")
	Value       string `json:"value"`                  // New value
	RequestID   string `json:"request_id,omitempty"`   // Optional ID for matching responses
}

// CommandResultPayload contains the result of a remote command execution.
type CommandResultPayload struct {
	RequestID string         `json:"request_id,omitempty"` // Matches the request
	Success   bool           `json:"success"`              // Whether the command succeeded
	Message   string         `json:"message,omitempty"`    // Result message or error
	Data      map[string]any `json:"data,omitempty"`       // Structured data (window_id, etc.)
}

// RemoteCommandPayload is sent from daemon to TUI client for execution.
// This is the routed version of ExecuteCommand/SendKeys/SetConfig.
type RemoteCommandPayload struct {
	RequestID   string   `json:"request_id,omitempty"`
	CommandType string   `json:"command_type"`           // "tape_command", "send_keys", "set_config"
	TapeCommand string   `json:"tape_command,omitempty"` // For tape commands
	TapeArgs    []string `json:"tape_args,omitempty"`    // Arguments for tape command
	TapeScript  string   `json:"tape_script,omitempty"`  // Raw tape script
	Keys        string   `json:"keys,omitempty"`         // For send_keys
	Literal     bool     `json:"literal,omitempty"`      // For send_keys (send to PTY)
	Raw         bool     `json:"raw,omitempty"`          // For send_keys (no splitting)
	ConfigPath  string   `json:"config_path,omitempty"`  // For set_config
	ConfigValue string   `json:"config_value,omitempty"` // For set_config
}

// GetLogsPayload requests log entries from the daemon.
type GetLogsPayload struct {
	Count int  `json:"count,omitempty"` // Number of entries to return (0 = all)
	Clear bool `json:"clear,omitempty"` // Clear logs after retrieval
}

// LogsDataPayload contains log entries from the daemon.
type LogsDataPayload struct {
	Entries []LogEntry `json:"entries"`
}

// QueryWindowsPayload requests window list from the TUI.
type QueryWindowsPayload struct {
	SessionName string `json:"session_name,omitempty"` // Target session (empty = most recently active)
	RequestID   string `json:"request_id,omitempty"`
}

// WindowInfo contains detailed information about a single window.
type WindowInfo struct {
	ID              string `json:"id"`                         // Unique window ID
	Title           string `json:"title"`                      // Window title (from PTY)
	CustomName      string `json:"custom_name,omitempty"`      // User-defined name
	DisplayName     string `json:"display_name"`               // CustomName if set, else Title
	Workspace       int    `json:"workspace"`                  // Workspace number (1-9)
	Focused         bool   `json:"focused"`                    // Is this the focused window
	Minimized       bool   `json:"minimized"`                  // Is window minimized
	Fullscreen      bool   `json:"fullscreen"`                 // Is window fullscreen
	X               int    `json:"x"`                          // X position
	Y               int    `json:"y"`                          // Y position
	Width           int    `json:"width"`                      // Width in columns
	Height          int    `json:"height"`                     // Height in rows
	PTYID           string `json:"pty_id,omitempty"`           // PTY ID (daemon mode)
	ForegroundPID   int    `json:"foreground_pid,omitempty"`   // PID of foreground process
	ForegroundCmd   string `json:"foreground_cmd,omitempty"`   // Command of foreground process
	ShellPID        int    `json:"shell_pid,omitempty"`        // PID of the shell
	ScrollbackLines int    `json:"scrollback_lines,omitempty"` // Lines in scrollback buffer
	CursorX         int    `json:"cursor_x"`                   // Cursor column
	CursorY         int    `json:"cursor_y"`                   // Cursor row
	CursorVisible   bool   `json:"cursor_visible"`             // Is cursor visible
}

// WindowListPayload contains the list of windows.
type WindowListPayload struct {
	RequestID string       `json:"request_id,omitempty"`
	Windows   []WindowInfo `json:"windows"`
	Total     int          `json:"total"`             // Total window count
	Focused   int          `json:"focused"`           // Index of focused window (-1 if none)
	Workspace int          `json:"current_workspace"` // Current workspace
}

// QuerySessionPayload requests session state from the TUI.
type QuerySessionPayload struct {
	SessionName string `json:"session_name,omitempty"` // Target session (empty = most recently active)
	RequestID   string `json:"request_id,omitempty"`
}

// SessionInfoPayload contains detailed session information.
type SessionInfoPayload struct {
	RequestID        string `json:"request_id,omitempty"`
	SessionName      string `json:"session_name"`
	SessionID        string `json:"session_id"`
	CurrentWorkspace int    `json:"current_workspace"` // Current workspace (1-9)
	TotalWindows     int    `json:"total_windows"`     // Total windows across all workspaces
	FocusedWindowID  string `json:"focused_window_id,omitempty"`
	Mode             string `json:"mode"`                      // "terminal" or "window_management"
	TilingEnabled    bool   `json:"tiling_enabled"`            // Is auto-tiling enabled
	TilingMode       string `json:"tiling_mode"`               // "bsp", "master-stack", etc.
	Theme            string `json:"theme"`                     // Current theme name
	DockbarPosition  string `json:"dockbar_position"`          // "top", "bottom", "hidden"
	AnimationsOn     bool   `json:"animations_enabled"`        // Are animations enabled
	ScriptMode       bool   `json:"script_mode"`               // Is a tape script running
	ScriptPaused     bool   `json:"script_paused"`             // Is script paused
	ScriptProgress   int    `json:"script_progress,omitempty"` // Script progress 0-100
	Width            int    `json:"width"`                     // Terminal width
	Height           int    `json:"height"`                    // Terminal height
	WorkspaceWindows []int  `json:"workspace_windows"`         // Window count per workspace [ws1, ws2, ...]
}

// StateSyncPayload broadcasts state changes to all clients in a session.
type StateSyncPayload struct {
	State       *SessionState `json:"state"`                  // Full session state
	TriggerType string        `json:"trigger_type,omitempty"` // What triggered the sync: "window", "workspace", "tiling", etc.
	SourceID    string        `json:"source_id,omitempty"`    // Client ID that triggered the change
}

// ClientJoinedPayload notifies clients that another client joined.
type ClientJoinedPayload struct {
	ClientID    string `json:"client_id"`    // Joining client's ID
	ClientCount int    `json:"client_count"` // Total clients now attached
	Width       int    `json:"width"`        // New client's width
	Height      int    `json:"height"`       // New client's height
}

// ClientLeftPayload notifies clients that another client left.
type ClientLeftPayload struct {
	ClientID    string `json:"client_id"`    // Leaving client's ID
	ClientCount int    `json:"client_count"` // Total clients now attached
}

// SessionResizePayload notifies clients of effective session size change.
// The effective size is the minimum dimensions of all attached clients.
type SessionResizePayload struct {
	Width       int `json:"width"`        // New effective width (min of all clients)
	Height      int `json:"height"`       // New effective height (min of all clients)
	ClientCount int `json:"client_count"` // Number of clients
}

// ForceRefreshPayload requests all clients to re-render.
type ForceRefreshPayload struct {
	Reason string `json:"reason,omitempty"` // Why refresh is needed
}

// Error codes
const (
	ErrCodeUnknown         = 1
	ErrCodeSessionNotFound = 2
	ErrCodeSessionExists   = 3
	ErrCodeInvalidMessage  = 4
	ErrCodeInternal        = 5
	ErrCodeNotAttached     = 6
	ErrCodePTYNotFound     = 7
	ErrCodeNoTUIAttached   = 8 // No TUI client attached to handle the command
	ErrCodeCommandFailed   = 9 // Command execution failed
)

// Protocol version for compatibility checking.
const ProtocolVersion = 2

// WriteMessageWithCodec writes a message with the specified codec.
// Wire format: [4 bytes BE length][1 byte type][1 byte codec][payload]
func WriteMessageWithCodec(w io.Writer, msg *Message, codec Codec) error {
	// Calculate total length: 1 (type) + 1 (codec) + len(payload)
	totalLen := uint32(2 + len(msg.Payload))

	// Write length
	if err := binary.Write(w, binary.BigEndian, totalLen); err != nil {
		return fmt.Errorf("failed to write message length: %w", err)
	}

	// Write type and codec
	if _, err := w.Write([]byte{byte(msg.Type), byte(codec.Type())}); err != nil {
		return fmt.Errorf("failed to write message header: %w", err)
	}

	// Write payload
	if len(msg.Payload) > 0 {
		if _, err := w.Write(msg.Payload); err != nil {
			return fmt.Errorf("failed to write message payload: %w", err)
		}
	}

	// Debug logging
	LogMessage("SEND", msg, codec)

	return nil
}

// ReadMessageWithCodec reads a message and returns it along with the codec type used.
// Wire format: [4 bytes BE length][1 byte type][1 byte codec][payload]
func ReadMessageWithCodec(r io.Reader) (*Message, CodecType, error) {
	// Read length
	var totalLen uint32
	if err := binary.Read(r, binary.BigEndian, &totalLen); err != nil {
		if err == io.EOF {
			return nil, CodecGob, err
		}
		return nil, CodecGob, fmt.Errorf("failed to read message length: %w", err)
	}

	// Sanity check length (max 16MB)
	if totalLen > 16*1024*1024 {
		return nil, CodecGob, fmt.Errorf("message too large: %d bytes (raw: 0x%08x)", totalLen, totalLen)
	}

	if totalLen < 2 {
		return nil, CodecGob, fmt.Errorf("message too small: %d bytes", totalLen)
	}

	// Read type and codec
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, CodecGob, fmt.Errorf("failed to read message header (after len=%d): %w", totalLen, err)
	}

	msgType := MessageType(header[0])
	codecType := CodecType(header[1])

	// Read payload
	payloadLen := totalLen - 2
	var payload []byte
	if payloadLen > 0 {
		payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, codecType, fmt.Errorf("failed to read message payload (len=%d, type=%d): %w", payloadLen, msgType, err)
		}
	}

	msg := &Message{
		Type:    msgType,
		Payload: payload,
	}

	// Debug logging
	LogMessage("RECV", msg, GetCodec(codecType))

	return msg, codecType, nil
}

// WriteMessage writes a message using the default codec (gob).
// This is a convenience wrapper for internal use.
func WriteMessage(w io.Writer, msg *Message) error {
	return WriteMessageWithCodec(w, msg, DefaultCodec())
}

// ReadMessage reads a message, ignoring the codec type.
// This is a convenience wrapper for internal use.
func ReadMessage(r io.Reader) (*Message, error) {
	msg, _, err := ReadMessageWithCodec(r)
	return msg, err
}

// NewMessageWithCodec creates a message with the specified codec.
func NewMessageWithCodec(msgType MessageType, payload any, codec Codec) (*Message, error) {
	var data []byte
	var err error

	if payload != nil {
		data, err = codec.Encode(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to encode payload: %w", err)
		}
	}

	return &Message{
		Type:    msgType,
		Payload: data,
	}, nil
}

// NewMessage creates a message with gob-encoded payload (default).
func NewMessage(msgType MessageType, payload any) (*Message, error) {
	return NewMessageWithCodec(msgType, payload, DefaultCodec())
}

// NewRawMessage creates a message with raw bytes payload (for binary data like PTY I/O).
func NewRawMessage(msgType MessageType, data []byte) *Message {
	return &Message{
		Type:    msgType,
		Payload: data,
	}
}

// ParsePayloadWithCodec decodes the message payload using the specified codec.
func (m *Message) ParsePayloadWithCodec(v any, codec Codec) error {
	if len(m.Payload) == 0 {
		return nil
	}
	return codec.Decode(m.Payload, v)
}

// ParsePayload decodes the message payload using gob (default).
func (m *Message) ParsePayload(v any) error {
	return m.ParsePayloadWithCodec(v, DefaultCodec())
}

// Binary message helpers for high-frequency PTY I/O
// These bypass the codec system for maximum performance.
// Format: [4 bytes length][1 byte type][1 byte codec=0][36 bytes PTY ID][raw data]

// WritePTYOutput writes PTY output in optimized binary format.
func WritePTYOutput(w io.Writer, ptyID string, data []byte) error {
	// Message format: [4 bytes length][1 byte type][1 byte codec][36 bytes ptyID][data]
	totalLen := uint32(2 + 36 + len(data))

	if err := binary.Write(w, binary.BigEndian, totalLen); err != nil {
		return err
	}
	// Type = MsgPTYOutput, Codec = 0 (gob/binary - but actually raw for PTY)
	if _, err := w.Write([]byte{byte(MsgPTYOutput), byte(CodecGob)}); err != nil {
		return err
	}
	// Write PTY ID as fixed 36-byte string (UUID format)
	idBytes := make([]byte, 36)
	copy(idBytes, ptyID)
	if _, err := w.Write(idBytes); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	return nil
}

// WritePTYInput writes PTY input in optimized binary format.
func WritePTYInput(w io.Writer, ptyID string, data []byte) error {
	totalLen := uint32(2 + 36 + len(data))

	if err := binary.Write(w, binary.BigEndian, totalLen); err != nil {
		return err
	}
	if _, err := w.Write([]byte{byte(MsgInput), byte(CodecGob)}); err != nil {
		return err
	}
	idBytes := make([]byte, 36)
	copy(idBytes, ptyID)
	if _, err := w.Write(idBytes); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	return nil
}

// ParseBinaryPTYMessage parses a binary PTY message (Input or Output).
// Returns ptyID and data.
func ParseBinaryPTYMessage(payload []byte) (ptyID string, data []byte, err error) {
	if len(payload) < 36 {
		return "", nil, fmt.Errorf("payload too short for PTY message: %d bytes", len(payload))
	}
	ptyID = string(payload[:36])
	// Trim null bytes from ID
	for i := len(ptyID) - 1; i >= 0; i-- {
		if ptyID[i] != 0 {
			ptyID = ptyID[:i+1]
			break
		}
	}
	data = payload[36:]
	return ptyID, data, nil
}

// NegotiateCodec determines the codec to use based on client preference.
// Returns gob by default unless the client explicitly requests json.
func NegotiateCodec(preferredCodec string) Codec {
	switch preferredCodec {
	case "json", "JSON":
		return GetCodec(CodecJSON)
	default:
		return GetCodec(CodecGob)
	}
}
