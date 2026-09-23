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
	MsgOutput        // Reserved: never sent
	MsgError         // Error message
	MsgPong          // Reserved: response to MsgPing
	MsgSessionEnded  // Session terminated
	MsgWindowChanged // Reserved: never sent
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
	// MsgAgentMail is one message stored in the session's agent ring, pushed to
	// every attached TUI client at the moment it is stored. It exists because an
	// attached client is the one reader that cannot poll: an idle client must do
	// no work, and a message an agent leaves has to reach the person anyway.
	MsgAgentMail
	// MsgHostsChanged tells every attached TUI client that the daemon's [hosts]
	// table changed: a host was added, removed or redialed. It is pushed for
	// the same reason MsgAgentMail is: a client whose daemon has no hosts stops
	// polling for them, so the only way it can learn about the first host is
	// to be told. It carries no listing; the client polls once on receipt.
	MsgHostsChanged
	// MsgReadDir asks the daemon to list a directory, and MsgDirListing is the
	// answer. Appended, so every value above stays where an older peer expects
	// it; see the note on ProtocolVersion.
	//
	// The rail's file section used to read the filesystem itself. That was the
	// same machine as the pane for as long as a pane could only be on this
	// machine, and federation ended that: a client attached to a session on
	// another host was listing its own disk and reporting that the pane's
	// directory did not exist. The daemon that owns the pane owns the
	// filesystem the pane is on, so the listing is asked for rather than taken.
	//
	// It carries no new authority over a link. Attaching a session on a host
	// already gives a shell on it, and reading the names in a directory is
	// strictly less than that.
	MsgReadDir
	MsgDirListing
)

// HostsChangedPayload names the change behind a MsgHostsChanged push.
type HostsChangedPayload struct {
	Added    []string
	Removed  []string
	Redialed []string
	// Changed names hosts whose link, sessions or agents changed, pushed by
	// the fleet (host_fleet.go). Added after the others: gob drops a field the
	// reader does not have, so an older client reads the push as before.
	Changed []string
}

// Message is the base protocol message structure.
// Wire format (v2): [4 bytes length][1 byte type][1 byte codec][payload]
// The codec byte is always 0 (gob). The value 1 once meant JSON and stays
// reserved; readers ignore the byte.
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
	PreferredCodec string `json:"preferred_codec,omitempty"` // Always "gob"; kept for older daemons
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
	Codec        string   `json:"codec"`         // Always "gob"; kept for older clients
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
	// Reserve is the chrome this client draws around the panes. See
	// LayoutReserve. A client that sends none reserves nothing, which is what an
	// older client means and what a client with no chrome means.
	Reserve LayoutReserve `json:"reserve,omitempty"`
}

// LayoutReserve is the rows and columns a client keeps for its own chrome (the
// sidebar rail, the dock) before a single pane is placed.
//
// It is on the wire because a pane's box is not a per-client quantity. Every
// client attached is looking at the same PTYs, and a PTY has exactly one size,
// so the box the panes are partitioned into has to be identical everywhere. The
// daemon takes the largest reserve any client asks for and hands that back as
// the session's; a client whose own chrome is narrower than the agreed reserve
// leaves the difference blank rather than handing it to the panes.
//
// Without this each client folded its own reserve into the box privately, so
// two clients with different chrome partitioned different boxes, computed
// different rectangles for the same panes, and dragged the shared PTYs back and
// forth between the two answers: a narrowing resize each way, on every push.
type LayoutReserve struct {
	Left   int `json:"left,omitempty"`
	Right  int `json:"right,omitempty"`
	Top    int `json:"top,omitempty"`
	Bottom int `json:"bottom,omitempty"`
}

// Max returns the reserve that satisfies both, which is the one a session
// agrees on: every client's own chrome fits inside it.
func (r LayoutReserve) Max(o LayoutReserve) LayoutReserve {
	return LayoutReserve{
		Left:   max(r.Left, o.Left),
		Right:  max(r.Right, o.Right),
		Top:    max(r.Top, o.Top),
		Bottom: max(r.Bottom, o.Bottom),
	}
}

// AttachedPayload confirms successful session attachment.
type AttachedPayload struct {
	SessionName string        `json:"session_name"`    // Attached session name
	SessionID   string        `json:"session_id"`      // Session unique ID
	Width       int           `json:"width"`           // Current session width
	Height      int           `json:"height"`          // Current session height
	WindowCount int           `json:"window_count"`    // Number of windows in session
	State       *SessionState `json:"state,omitempty"` // Session state for restore
	// Reserve is the session's agreed chrome reserve, the largest any attached
	// client asks for. The panes go in what is left of the size above.
	Reserve LayoutReserve `json:"reserve,omitempty"`
	// Generation is where this session's layout had got to when the reply was
	// written, so a broadcast still in flight from before it is recognised as
	// stale. See SessionResizePayload.Generation.
	Generation uint64 `json:"generation,omitempty"`
	// HumanNonce is a secret issued for this attach. The client passes it as
	// human_nonce when it sends mail from=human, and the daemon stores such
	// mail as verified_human only while the attach it was issued to is live.
	// Empty from an older daemon, which verifies nothing; a client then sends
	// no nonce and its mail is stored as claimed_human.
	HumanNonce string `json:"human_nonce,omitempty"`
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
	// Global creates the session as a global one: a session meant to hold
	// panes from more than one machine. See SessionState.Global.
	//
	// It also suppresses the initial window a detached create would spawn. A
	// global session's first pane is the one the user picks a machine for, and
	// a window spawned here would be a local pane nobody asked for, in the one
	// session whose whole point is that the machine is chosen.
	Global bool `json:"global,omitempty"`
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
	// AgentKind is what sort of block a needs_input pane is on ("approval",
	// "question"), so a rail watching another session can say what the pane
	// wants from you. Empty in any other state. Additive and omitted when
	// empty, which is what an older peer sends and reads as "not said".
	AgentKind string `json:"agent_kind,omitempty"`
	// CompletionSeq is the pane's count of finished turns (see
	// WindowState.CompletionSeq), so a rail can mark a finished turn in a
	// session it is not attached to. Additive and omitted when zero, which is
	// what an older peer sends and what reads as no turn counted.
	CompletionSeq uint64 `json:"completion_seq,omitempty"`
	// AgentMeta is what the pane reported about its agent through
	// set-agent-meta, so a rail watching another session can draw it too.
	// Additive and omitted when empty, which is what an older peer sends.
	AgentMeta []AgentMetaToken `json:"agent_meta,omitempty"`
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
	// Dir and Branch say where the focused pane's shell is: the directory's
	// base name ("~" for home) and the git branch checked out there. A rail
	// labels an unnamed session with Dir and follows any label with Branch.
	// Both are omitted when unknown, which an older daemon always is.
	Dir    string `json:"dir,omitempty"`
	Branch string `json:"branch,omitempty"`
	// Restored marks a session rebuilt from saved state that nobody has attached
	// to yet, so a listing can say why it is here without the client having to
	// attach to find out. Omitted when false, which is what an older daemon
	// sends and what every client reads as "an ordinary live session".
	Restored bool `json:"restored,omitempty"`
	// Global marks a session meant to hold panes from more than one machine,
	// which the rail files in its own group rather than under a machine. See
	// SessionState.Global. Omitted when false, which is what an older daemon
	// sends and what every client reads as an ordinary session.
	Global bool `json:"global,omitempty"`
	// Worktree is set for a session whose directory is a git worktree: which
	// repository, which branch, and whether the directory still exists. It is
	// what the rail groups by. Omitted for every other session, which is also
	// what an older daemon sends.
	Worktree *WorktreeInfo `json:"worktree,omitempty"`
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

// ResizePTYPayload requests resizing a specific PTY.
type ResizePTYPayload struct {
	PTYID  string `json:"pty_id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// Reserve is the sending client's own chrome, read only when PTYID is
	// empty, which is the message that announces a client's viewport rather
	// than a pane's size. See LayoutReserve.
	Reserve LayoutReserve `json:"reserve,omitempty"`
}

// SubscribePTYPayload requests subscribing to PTY output. FromSeq, when
// positive, is the stream position the client's emulator has been restored to,
// so the daemon replays only what came after it. Zero means the client has no
// claim to make and the daemon uses whatever position it recorded for this
// connection, which is what an older client sends.
//
// FromSnapshot marks a subscribe that follows a freshly restored snapshot: the
// client holds an authoritative copy of the pane's state at FromSeq, so a
// rolled catch-up (whose ring has moved past FromSeq) must not clear that
// state before replaying the tail. The clear exists for a client resuming a
// screen it drew byte by byte, whose missing bytes would otherwise splice two
// halves of the stream; a snapshot is already the whole of it, and clearing
// it is what emptied rows a full-screen program had drawn (issue #123).
type SubscribePTYPayload struct {
	PTYID        string `json:"pty_id"`
	FromSeq      int64  `json:"from_seq,omitempty"`
	FromSnapshot bool   `json:"from_snapshot,omitempty"`
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

// AgentMailPayload is the MsgAgentMail body: the stored message, as the ring
// holds it. The client keeps its own mirror of the ring from these, so opening
// the mailbox costs no round trip and a message that arrived while the person
// was looking elsewhere is already there when they look.
//
// A push carries either a stored message or a read receipt, never both. The
// receipt lists the ids a read just marked read, so the count a client draws
// beside a pane's inbox follows the agent actually reading rather than the
// client's guess.
type AgentMailPayload struct {
	Message AgentMessage `json:"message"`
	ReadIDs []uint64     `json:"read_ids,omitempty"`
	ReadAt  int64        `json:"read_at,omitempty"`
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
	// Packed asks for the cells in their packed form (TerminalState.Styles and
	// the Packed fields) instead of as [][]CellState. A daemon that predates
	// the field ignores it and sends cells, which every client reads.
	Packed bool `json:"packed,omitempty"`
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
	// Reserve is the session's agreed chrome reserve, the largest any attached
	// client asks for. It travels with the size because it is the other half of
	// the same answer: the panes' box is the size less this.
	Reserve LayoutReserve `json:"reserve,omitempty"`
	// Generation counts the times this session's layout has been settled, and
	// is what lets a client tell a stale announcement from a fresh one.
	//
	// It has to. Each of these is written to each client on a goroutine of its
	// own, so two settlements close together race for the connection and the
	// order they arrive in is the scheduler's choice. Measured: two clients
	// announcing their chrome at the same moment ended up holding different
	// reserves for good, because one of them took the older of two messages
	// last. A client ignores any generation it has already passed.
	//
	// Zero means a daemon that predates the field, and is never treated as
	// stale.
	Generation uint64 `json:"generation,omitempty"`
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
	// ErrCodeForbidden refuses a message a machine linked to this one may not
	// send under its link policy. Nothing was done. See link_policy.go.
	ErrCodeForbidden = 10
)

// WriteMessage writes one framed message.
// Wire format: [4 bytes BE length][1 byte type][1 byte codec][payload]
//
// The header and the payload go out through net.Buffers. On a *net.UnixConn,
// which is what every production caller passes, that is one writev with no
// copy of the payload; on any other writer it is one write for the header and
// one for the payload. Writing the length, the type and codec bytes and the
// payload separately would be three syscalls under the caller's send lock,
// for the same bytes on the wire.
//
// Measured on a unix socket against three writes, a 64-byte frame takes about
// 1.3us instead of 2.2us and a 4 KiB frame 1.65us instead of 2.2us. At 64 KiB
// and above the copy into the socket dominates and the two are within noise of each other.
func WriteMessage(w io.Writer, msg *Message) error {
	var hdr [6]byte
	// Length counts the type and codec bytes plus the payload.
	binary.BigEndian.PutUint32(hdr[:4], uint32(2+len(msg.Payload)))
	hdr[4], hdr[5] = byte(msg.Type), wireCodecGob

	if len(msg.Payload) == 0 {
		if _, err := w.Write(hdr[:]); err != nil {
			return fmt.Errorf("failed to write message header: %w", err)
		}
	} else {
		bufs := net.Buffers{hdr[:], msg.Payload}
		if _, err := bufs.WriteTo(w); err != nil {
			return fmt.Errorf("failed to write message: %w", err)
		}
	}

	// Debug logging
	LogMessage("SEND", msg)

	return nil
}

// ReadMessage reads one framed message. The codec byte is read and ignored.
// Wire format: [4 bytes BE length][1 byte type][1 byte codec][payload]
func ReadMessage(r io.Reader) (*Message, error) {
	var totalLen uint32
	if err := binary.Read(r, binary.BigEndian, &totalLen); err != nil {
		if err == io.EOF {
			return nil, err
		}
		return nil, fmt.Errorf("failed to read message length: %w", err)
	}
	return readMessageBody(r, totalLen)
}

// ReadMessageBuffered reads a framed message from r, a *bufio.Reader over
// conn, applying boundaryTimeout only to the 4-byte length prefix and
// bodyTimeout to the header and payload. Splitting the deadline keeps a large
// payload that arrives across several reads from being cut mid-frame, which
// would desync the stream. A timeout of 0 clears the read deadline for that
// part: at the boundary, the read then waits for the next frame with no
// wakeup at all.
//
// Both read loops go through here. The daemon wraps each accepted connection
// in a bufio.Reader to peek the first byte for JSON-versus-binary detection,
// and the client wraps its connection so a frame is one read rather than
// three; neither may read conn directly once the reader holds bytes.
func ReadMessageBuffered(conn net.Conn, r io.Reader, boundaryTimeout, bodyTimeout time.Duration) (*Message, error) {
	setBoundaryDeadline(conn, boundaryTimeout)

	var totalLen uint32
	if err := binary.Read(r, binary.BigEndian, &totalLen); err != nil {
		if err == io.EOF {
			return nil, err
		}
		return nil, fmt.Errorf("failed to read message length: %w", err)
	}

	if bodyTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(bodyTimeout))
	} else {
		_ = conn.SetReadDeadline(time.Time{})
	}

	return readMessageBody(r, totalLen)
}

// setBoundaryDeadline arms the deadline for the wait between frames, or
// clears it when there is none.
//
// The read loops do not poll their done channels with a short deadline. A
// 100 ms poll costs an idle connection a timer, a wakeup and an empty read ten
// times a second on each side, and it is not needed: the only things that
// close those channels close the connection with them, and a closed
// connection wakes the read on its own.
func setBoundaryDeadline(conn net.Conn, timeout time.Duration) {
	if timeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
}

// readMessageBody reads the header and payload after the length prefix has
// already been consumed from r.
func readMessageBody(r io.Reader, totalLen uint32) (*Message, error) {
	// Sanity check length (max 16MB)
	if totalLen > 16*1024*1024 {
		return nil, fmt.Errorf("message too large: %d bytes (raw: 0x%08x)", totalLen, totalLen)
	}

	if totalLen < 2 {
		return nil, fmt.Errorf("message too small: %d bytes", totalLen)
	}

	// Read type and codec. The codec byte is always gob and is ignored.
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("failed to read message header (after len=%d): %w", totalLen, err)
	}

	msgType := MessageType(header[0])

	// Read payload
	payloadLen := totalLen - 2
	var payload []byte
	if payloadLen > 0 {
		payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, fmt.Errorf("failed to read message payload (len=%d, type=%d): %w", payloadLen, msgType, err)
		}
	}

	msg := &Message{
		Type:    msgType,
		Payload: payload,
	}

	// Debug logging
	LogMessage("RECV", msg)

	return msg, nil
}

// NewMessage creates a message with a gob-encoded payload. A nil payload
// gives an empty one.
func NewMessage(msgType MessageType, payload any) (*Message, error) {
	var data []byte
	var err error

	if payload != nil {
		data, err = encodePayload(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to encode payload: %w", err)
		}
	}

	return &Message{
		Type:    msgType,
		Payload: data,
	}, nil
}

// ParsePayload decodes the message's gob payload into v. An empty payload
// leaves v untouched.
func (m *Message) ParsePayload(v any) error {
	return decodePayload(m.Payload, v)
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
// Both callers write straight to an unbuffered unix socket, so separate
// writes for the length, the type and codec bytes, the 36-byte id and the data
// would be four syscalls. On the input path the user waits through them with
// the client's whole-client mutex held. One assembled frame and one write
// measured 1140ns for a keystroke, against 3346ns for four writes.
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
	buf[4], buf[5] = byte(msg), wireCodecGob
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

// ReadDirPayload asks for one directory's names.
type ReadDirPayload struct {
	// WindowID is the pane the listing is about, or empty for a directory the
	// user named by hand. It is what lets the daemon answer whether the pane
	// announced a directory its shell is not in; without a pane there is no
	// such question.
	WindowID string `json:"window_id,omitempty"`
	Dir      string `json:"dir"`
	// Max bounds the listing. Zero means the daemon's own bound.
	Max int `json:"max,omitempty"`
	// Pinned marks a directory the user walked into by hand, so the pane's
	// own directory is no longer what is being listed.
	//
	// The window still travels with it, because the window says which machine
	// the files are on and that is true wherever the user has browsed to. What
	// it must not do is answer the spoof question: comparing a folder somebody
	// chose against the folder the pane's shell is in reports every step away
	// from the pane as the pane lying about where it is, which is what "read
	// only: wrong folder" on a hand-picked directory was.
	//
	// False is a pane-steered listing, which is what an older client sends and
	// what the check has always applied to.
	Pinned bool `json:"pinned,omitempty"`
}

// DirEntry is one name in a listing. Only the two facts a directory read
// already returns: anything more would cost a stat per name, and the icon and
// the ordering are the client's business.
type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
}

// DirListingPayload is the answer.
type DirListingPayload struct {
	Dir     string     `json:"dir"`
	Entries []DirEntry `json:"entries,omitempty"`
	// Capped reports that the directory holds more names than were sent.
	Capped bool `json:"capped,omitempty"`
	// Spoofed reports that the pane announced this directory and its shell is
	// somewhere else. The daemon answers it because the daemon is the only side
	// holding both facts: the announcement arrived in its window state and the
	// shell is its own process. A client cannot compute it for a pane on
	// another machine without reading a pid that means nothing where it is.
	Spoofed bool `json:"spoofed,omitempty"`
	// Err is the reason there is no listing, already in words a person can act
	// on, or empty on success.
	Err string `json:"err,omitempty"`
}
