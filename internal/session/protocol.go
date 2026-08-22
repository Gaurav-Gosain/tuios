// Package session provides persistent session management for TUIOS.
// It implements a daemon-client architecture similar to tmux, allowing
// terminal sessions to persist when the client disconnects.
package session

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
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
	MsgPing                                    // Reserved: keepalive ping (no sender; numbering is wire format)
	MsgCreatePTY                               // Create new PTY in session
	MsgClosePTY                                // Close a PTY
	MsgListPTYs                                // Reserved: list PTYs (no sender)
	MsgFocusPTY                                // Reserved: focus PTY (never dispatched)
	MsgGetState                                // Reserved: get session state (no sender)
	MsgUpdateState                             // Update session state
	MsgSubscribePTY                            // Subscribe to PTY output
	MsgUnsubscribePTY                          // Unsubscribe from PTY output
	MsgGetTerminalState                        // Get terminal state for a PTY
	MsgExecuteCommand                          // Execute a tape command (routed to TUI)
	MsgSendKeys                                // Reserved: send-keys moved to the JSON verb plane
	MsgSetConfig                               // Reserved: set-config moved to the JSON verb plane
	MsgCapturePane                             // Reserved: capture-pane moved to the JSON verb plane

	// Server -> Client messages
	MsgWelcome       // Response to Hello with server info
	MsgAttached      // Successfully attached to session
	MsgDetached      // Confirm detach
	MsgSessionList   // List of sessions
	MsgOutput        // Terminal output bytes
	MsgError         // Error message
	MsgPong          // Reserved: response to MsgPing
	MsgSessionEnded  // Session terminated
	MsgWindowChanged // Window size changed (from other client)
	MsgPTYList       // Reserved: response to MsgListPTYs
	MsgPTYCreated    // New PTY created
	MsgPTYClosed     // PTY closed
	MsgPTYOutput     // Output from a specific PTY
	MsgStateData     // Reserved: response to MsgGetState
	MsgTerminalState // Terminal state for a PTY (screen + scrollback)
	MsgCommandResult // Result of a remote command execution
	MsgRemoteCommand // Remote command from daemon to TUI client for execution
	MsgGetLogs       // Request to retrieve daemon logs
	MsgLogsData      // Response with log entries
	MsgQueryWindows  // Reserved: list-windows moved to the JSON verb plane
	MsgWindowList    // Reserved: response to MsgQueryWindows
	MsgQuerySession  // Reserved: session-info moved to the JSON verb plane
	MsgSessionInfo   // Reserved: response to MsgQuerySession

	// Multi-client support messages
	MsgStateSync     // Broadcast state update to all clients in session
	MsgClientJoined  // Notification that another client joined the session
	MsgClientLeft    // Notification that another client left the session
	MsgSessionResize // Session effective size changed (min of all clients)
	MsgForceRefresh  // Reserved: refreshes ride the client event channel now
	// MsgRequestFullSync is declared and never sent. No daemon has ever had a
	// handler for it, and the case it was meant for, a client that missed a
	// state sync, is handled where the sync is queued instead: the queue keeps
	// the newest snapshot rather than the first (see app.OS.QueueStateSync), so
	// there is nothing to re-request. It stays in place because the iota order
	// is the wire format and removing it would move every value below it.
	MsgRequestFullSync

	// Appended after all existing types to keep every value above stable for
	// older clients that share this iota order.
	MsgResurrect  // Restore a saved session on demand (cold-start restore)
	MsgPTYResized // A pane's emulator changed size at this point in its output stream
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
	// Protocol is the wire protocol version the client speaks. Zero means a
	// client that predates the field, which is read as LegacyProtocolVersion:
	// gob ignores a field the peer does not know, so silence here is age, not
	// disagreement.
	Protocol int `json:"protocol,omitempty"`
}

// WelcomePayload is sent by server in response to Hello.
type WelcomePayload struct {
	Version      string   `json:"version"`       // Server version
	SessionNames []string `json:"session_names"` // Available sessions
	Codec        string   `json:"codec"`         // Negotiated codec: "gob" or "json"
	// Protocol is the wire protocol version the daemon speaks. Zero means a
	// daemon that predates the field; see HelloPayload.Protocol.
	Protocol int `json:"protocol,omitempty"`
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
	// Detach requests a headless session: the daemon spawns an initial window
	// with no client attached, so the session is immediately usable by control
	// verbs. Additive and backward compatible; older daemons ignore it and
	// simply create an empty session. Zero value keeps the pre-existing
	// "create an empty session" behavior.
	Detach bool `json:"detach,omitempty"`
}

// WindowSummary is a lightweight per-window entry in a session listing: enough
// for a session-management surface to draw and expand a non-attached session's
// window tree without querying that session's PTYs.
type WindowSummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	AgentState string `json:"agent_state,omitempty"`
	// AgentStateAt is when the pane entered AgentState (Unix nanoseconds).
	// Additive and omitted when zero, so an older peer just reads no elapsed time.
	AgentStateAt int64 `json:"agent_state_at,omitempty"`
	// AgentHarness is which agent is running in the pane, as the detecting
	// manifest or the reporting source named it. Without it a rail watching
	// eight agents cannot say which of them is Claude. Additive and omitted when
	// empty, which is what an older peer sends and what every client reads as
	// "the agent did not say".
	AgentHarness string `json:"agent_harness,omitempty"`
	// AgentMessage is the short note the pane reported alongside its state
	// ("editing files", "awaiting approval"). It is the one thing a rail
	// watching somebody else's session cannot infer from the state alone.
	// Additive and omitted when empty, which is what an older peer sends and
	// what every client reads as "the agent did not say".
	AgentMessage string `json:"agent_message,omitempty"`
	// ForegroundCmd is what the pane is running, for a row that would otherwise
	// repeat the title its siblings carry. Empty for a shell and for a pane the
	// user has named, whose name is already the answer. Additive and omitted
	// when empty, so an older peer just reads the title as before.
	ForegroundCmd string `json:"foreground_cmd,omitempty"`
	// Workspace is the workspace the pane sits on, so a client previewing a
	// session it is not attached to can say where a pane lives. Additive and
	// omitted when zero, which is exactly how an older daemon's listing reads:
	// unknown, and the row goes untagged rather than wrongly tagged.
	Workspace int `json:"workspace,omitempty"`
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
	// Windows lists per-window summaries so a client can expand a non-attached
	// session's tree from the listing alone. Omitted by older daemons, which
	// older and newer clients both read back as "windows not known yet".
	Windows []WindowSummary `json:"windows,omitempty"`
	// DisplayName and Accent carry the session's daemon-owned label and accent
	// slot. Only a state push carries them, and a push only reaches the session
	// a client is attached to, so without them here a client could not show a
	// label for any session but its own. Both are omitted when unset, which is
	// how a client reads "fall back to Name".
	DisplayName string `json:"display_name,omitempty"`
	Accent      string `json:"accent,omitempty"`
	// CurrentWorkspace is the workspace the session is showing, which is what
	// decides whether one of its panes counts as "here" and so goes untagged.
	// Additive and omitted when zero; a client reading zero tags nothing, which
	// is the same graceful silence it had before the field existed.
	CurrentWorkspace int `json:"current_workspace,omitempty"`
	// Restored marks a session rebuilt from saved state that nobody has attached
	// to yet, so a listing can say why it is here without the client having to
	// attach to find out. Omitted when false, which is what an older daemon
	// sends and what every client reads as "an ordinary live session".
	Restored bool `json:"restored,omitempty"`
}

// SessionListPayload contains list of available sessions.
type SessionListPayload struct {
	Sessions []SessionInfo `json:"sessions"`
}

// KillPayload requests termination of a session.
type KillPayload struct {
	SessionName string `json:"session_name"` // Session to kill
}

// ResurrectPayload requests restoring a saved session on demand.
type ResurrectPayload struct {
	SessionName string `json:"session_name"` // Session to resurrect from saved state
}

// SessionEndedPayload tells an attached client that its session was terminated.
// It is sent on MsgSessionEnded, a message type that has existed since the first
// protocol version but had no payload defined, so both fields are optional and
// an older client that ignores the message is unaffected.
type SessionEndedPayload struct {
	SessionName string `json:"session_name,omitempty"` // Session that ended
	Reason      string `json:"reason,omitempty"`       // Short human explanation
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

// CreatePTYPayload requests creation of a new PTY.
type CreatePTYPayload struct {
	Title  string `json:"title,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	// WindowID is the client-side window UUID. It is exported to the spawned
	// shell as TUIOS_WINDOW_ID. Empty from older clients (unset, as before).
	WindowID string `json:"window_id,omitempty"`
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

// ResizePTYPayload requests resizing a specific PTY.
type ResizePTYPayload struct {
	PTYID  string `json:"pty_id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// SubscribePTYPayload requests subscribing to PTY output. FromSeq, when
// positive, is the stream position the client's emulator has been restored to,
// so the daemon replays only what came after it. Zero means the client has no
// claim to make and the daemon uses whatever position it recorded for this
// connection, which is what an older client sends.
type SubscribePTYPayload struct {
	PTYID   string `json:"pty_id"`
	FromSeq int64  `json:"from_seq,omitempty"`
}

// PTYResizedPayload announces the size the daemon's emulator took, delivered in
// the pane's output stream at the byte it took it. Two emulators fed the same
// bytes only agree on where a line wrapped if they change width at the same
// byte, so a client applies this where it arrives rather than when it asked.
type PTYResizedPayload struct {
	PTYID  string `json:"pty_id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
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
	// HaveScrollback is how many scrollback rows the caller's own emulator
	// already holds. The daemon sends only the rows beyond it, because that is
	// all the caller can use: a client whose emulator survived keeps its own
	// history and merges just the lines that scrolled off while it was away.
	// Zero means none, which is a fresh emulator and the whole window.
	//
	// A daemon that predates this field ignores it and sends the whole window,
	// which is what the caller already handles, so the two directions of
	// version skew are both safe.
	HaveScrollback int `json:"have_scrollback,omitempty"`
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
	SessionName  string `json:"session_name,omitempty"`  // Target session (empty = most recently active)
	Keys         string `json:"keys"`                    // Key sequence (e.g., "ctrl+b,n" or "Hello World")
	Literal      bool   `json:"literal,omitempty"`       // If true, send keys literally to PTY (no parsing)
	Raw          bool   `json:"raw,omitempty"`           // If true, treat each character as a separate key (no splitting on space/comma)
	WindowTarget string `json:"window_target,omitempty"` // Target window by name or ID (empty = focused window)
	RequestID    string `json:"request_id,omitempty"`    // Optional ID for matching responses
}

// CapturePanePayload requests capturing the content of a pane.
type CapturePanePayload struct {
	SessionName  string `json:"session_name,omitempty"`  // Target session (empty = most recently active)
	WindowTarget string `json:"window_target,omitempty"` // Target window by name or ID (empty = focused)
	Scrollback   bool   `json:"scrollback,omitempty"`    // Include scrollback history (not just visible screen)
	ANSI         bool   `json:"ansi,omitempty"`          // Include ANSI escape codes in output
	RequestID    string `json:"request_id,omitempty"`    // Optional ID for matching responses
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
	RequestID    string   `json:"request_id,omitempty"`
	CommandType  string   `json:"command_type"`            // "tape_command", "send_keys", "set_config"
	TapeCommand  string   `json:"tape_command,omitempty"`  // For tape commands
	TapeArgs     []string `json:"tape_args,omitempty"`     // Arguments for tape command
	TapeScript   string   `json:"tape_script,omitempty"`   // Raw tape script
	Keys         string   `json:"keys,omitempty"`          // For send_keys
	Literal      bool     `json:"literal,omitempty"`       // For send_keys (send to PTY)
	Raw          bool     `json:"raw,omitempty"`           // For send_keys (no splitting)
	WindowTarget string   `json:"window_target,omitempty"` // For send_keys (target window by name or ID)
	ConfigPath   string   `json:"config_path,omitempty"`   // For set_config
	ConfigValue  string   `json:"config_value,omitempty"`  // For set_config
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

// ProtocolVersion is the wire protocol this build speaks. Both sides announce it
// in the handshake (HelloPayload.Protocol, WelcomePayload.Protocol) and both
// refuse a peer outside the range they serve.
//
// Bump it on any change an older peer cannot read. Appending a message type to
// the end of the iota block is not such a change; inserting one is, because the
// type is a single byte on the wire and every value after the insertion moves.
//
// 3 is this version because exactly that happened since v0.7.0: MsgCapturePane
// was inserted after MsgSetConfig (f0810a1), which moved MsgWelcome from 22 to
// 23 and every server-to-client type with it, while ProtocolVersion stayed at 2.
// A v0.7.0 daemon answers a hello with type 22, which this build reads as
// MsgCapturePane. It was found by building the v0.7.0 tag and pointing this
// client at it. The number is corrected here rather than the numbering reverted,
// because no released build speaks 3 yet, so the bump costs nothing and the
// refusal it enables is the honest outcome for a pairing that cannot work.
const ProtocolVersion = 3

// MinProtocolVersion is the oldest wire protocol this build still serves. A peer
// announcing anything older is told to upgrade rather than allowed to proceed
// into undefined behavior.
const MinProtocolVersion = 3

// LegacyProtocolVersion is what a peer that announces nothing is taken to speak.
// gob leaves a field the sender did not know at its zero value, so silence is
// age: a build from before the version fields existed, which is a build from
// before the numbering moved. It is outside the range above, so such a peer is
// refused, which is correct: it cannot read this build's messages.
const LegacyProtocolVersion = 2

// LegacyWelcomeType is the type byte a protocol-2 daemon answers a hello with.
// Nothing else about such a daemon is reachable, but the welcome payload still
// decodes, which is enough to name its version and how many sessions restarting
// it would move. Only the compatibility probe reads it.
const LegacyWelcomeType MessageType = 22

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
	var totalLen uint32
	if err := binary.Read(r, binary.BigEndian, &totalLen); err != nil {
		if err == io.EOF {
			return nil, CodecGob, err
		}
		return nil, CodecGob, fmt.Errorf("failed to read message length: %w", err)
	}
	return readMessageBody(r, totalLen)
}

// ReadMessageConn reads a framed message from conn, applying boundaryTimeout
// only to the 4-byte length prefix and bodyTimeout to the header and payload.
// Splitting the deadline keeps idle-connection keepalive timeouts short while
// preventing a large payload that arrives across several reads from being cut
// mid-frame, which would otherwise desync the stream. A bodyTimeout of 0
// clears the read deadline for the body.
func ReadMessageConn(conn net.Conn, boundaryTimeout, bodyTimeout time.Duration) (*Message, CodecType, error) {
	_ = conn.SetReadDeadline(time.Now().Add(boundaryTimeout))

	var totalLen uint32
	if err := binary.Read(conn, binary.BigEndian, &totalLen); err != nil {
		if err == io.EOF {
			return nil, CodecGob, err
		}
		return nil, CodecGob, fmt.Errorf("failed to read message length: %w", err)
	}

	if bodyTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(bodyTimeout))
	} else {
		_ = conn.SetReadDeadline(time.Time{})
	}

	return readMessageBody(conn, totalLen)
}

// ReadMessageBuffered reads a framed binary message the same way as
// ReadMessageConn, but reads the bytes from r (typically a *bufio.Reader
// wrapping conn) while still applying the split boundary/body deadlines to conn.
// The daemon wraps each accepted connection in a bufio.Reader to peek the first
// byte for JSON-versus-binary detection, so the binary read loop must continue
// through that same buffered reader rather than reading conn directly.
func ReadMessageBuffered(conn net.Conn, r io.Reader, boundaryTimeout, bodyTimeout time.Duration) (*Message, CodecType, error) {
	_ = conn.SetReadDeadline(time.Now().Add(boundaryTimeout))

	var totalLen uint32
	if err := binary.Read(r, binary.BigEndian, &totalLen); err != nil {
		if err == io.EOF {
			return nil, CodecGob, err
		}
		return nil, CodecGob, fmt.Errorf("failed to read message length: %w", err)
	}

	if bodyTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(bodyTimeout))
	} else {
		_ = conn.SetReadDeadline(time.Time{})
	}

	return readMessageBody(r, totalLen)
}

// readMessageBody reads the header and payload after the length prefix has
// already been consumed from r.
func readMessageBody(r io.Reader, totalLen uint32) (*Message, CodecType, error) {
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
	LogMessage("RECV", msg, DefaultCodec())

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

// ptyFrameHeaderLen is the fixed part of a binary PTY frame: the length
// prefix, the type and codec bytes, and the padded PTY id.
const ptyFrameHeaderLen = 4 + 2 + 36

// ptyFrameBufs hands out frame buffers so a keystroke does not allocate one.
var ptyFrameBufs = sync.Pool{New: func() any {
	b := make([]byte, 0, ptyFrameHeaderLen+4096)
	return &b
}}

// writePTYFrame writes one binary PTY frame in a single Write.
//
// It used to be four: a binary.Write for the length, one for the type and
// codec bytes, one for a freshly allocated 36-byte id, and one for the data.
// Both callers write straight to an unbuffered unix socket, so those were four
// syscalls, and on the input path they are four syscalls the user is waiting
// through with the client's whole-client mutex held. One assembled frame and
// one write measured 3346ns to 1140ns for a keystroke.
//
// The bytes are unchanged. The id field is still a fixed 36 bytes, zero padded
// when the id is shorter and truncated when it is longer, which is what copy
// into a 36-byte slice did before.
func writePTYFrame(w io.Writer, msg MessageType, ptyID string, data []byte) error {
	bufp := ptyFrameBufs.Get().(*[]byte)
	buf := (*bufp)[:0]
	if cap(buf) < ptyFrameHeaderLen+len(data) {
		buf = make([]byte, 0, ptyFrameHeaderLen+len(data))
	}
	buf = buf[:ptyFrameHeaderLen]
	binary.BigEndian.PutUint32(buf, uint32(2+36+len(data)))
	buf[4], buf[5] = byte(msg), byte(CodecGob)
	clear(buf[6:ptyFrameHeaderLen])
	copy(buf[6:ptyFrameHeaderLen], ptyID)
	buf = append(buf, data...)

	_, err := w.Write(buf)

	// A batch far larger than the pool's buffers should not be kept alive by
	// it; anything up to the usual size goes back.
	if cap(buf) <= ptyFrameHeaderLen+256*1024 {
		*bufp = buf
		ptyFrameBufs.Put(bufp)
	}
	return err
}

// WritePTYOutput writes PTY output in optimized binary format.
func WritePTYOutput(w io.Writer, ptyID string, data []byte) error {
	return writePTYFrame(w, MsgPTYOutput, ptyID, data)
}

// WritePTYInput writes PTY input in optimized binary format.
func WritePTYInput(w io.Writer, ptyID string, data []byte) error {
	return writePTYFrame(w, MsgInput, ptyID, data)
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
