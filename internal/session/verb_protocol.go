package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"slices"
	"time"
)

// This file implements the typed, line-delimited JSON verb protocol layered
// additively on the existing daemon socket. One request per line:
//
//	{"id": 1, "verb": "list-windows", "params": {"session": "work"}}
//
// and one response per line, either
//
//	{"id": 1, "result": {"type": "window_list", ...}}
//
// or
//
//	{"id": 1, "error": {"code": "session_not_found", "message": "..."}}
//
// The envelope id is opaque and echoed back verbatim. Error codes are stable
// strings so a caller never has to cross-reference a numeric table. The binary
// gob/PTY fast path is untouched; a connection is detected as JSON or binary
// from its first byte on accept (see detectJSONClient).

// VerbProtocolVersion is the version of the JSON verb protocol. It is reported
// by the list-verbs introspection verb so a client can gate on it. Bump it only
// on an incompatible change to the envelope or to an existing verb's contract;
// adding a new verb is backward compatible and does not require a bump.
const VerbProtocolVersion = 1

// Stable string error codes returned in the response error envelope. These are
// part of the public protocol surface; keep the string values stable.
const (
	ErrVerbInvalidRequest  = "invalid_request"   // line was not a valid request envelope
	ErrVerbUnknownVerb     = "unknown_verb"      // no such verb
	ErrVerbInvalidParams   = "invalid_params"    // params failed to decode or a required field was missing
	ErrVerbSessionNotFound = "session_not_found" // named session does not exist
	ErrVerbWindowNotFound  = "window_not_found"  // window target did not resolve
	ErrVerbNoWindows       = "no_windows"        // session has no windows to act on
	ErrVerbPTYNotFound     = "pty_not_found"     // the target window has no live PTY
	ErrVerbNeedsClient     = "needs_client"      // verb needs a live renderer that is not attached
	ErrVerbOptionNotFound  = "option_not_found"  // get-option key was never set
	ErrVerbCommandFailed   = "command_failed"    // a verb routed to the attached client came back failed
	ErrVerbTimeout         = "timeout"           // a wait-for condition did not match before its timeout
	ErrVerbInternal        = "internal"          // unexpected server-side failure

	// ErrVerbProtocolMismatch reports that the caller's protocol version is
	// outside the range this daemon accepts. It is only ever produced by the
	// hello verb, which exists so a mismatch is reported in this shape rather
	// than surfacing later as a framing or decode failure.
	ErrVerbProtocolMismatch = "protocol_mismatch"
)

// MinVerbProtocolVersion is the oldest protocol version this daemon still
// serves. A caller announcing anything older is told to upgrade rather than
// being allowed to proceed into undefined behavior.
const MinVerbProtocolVersion = 1

// verbRequest is one decoded request line. ID is opaque (number, string, or
// absent) and echoed back on the response.
type verbRequest struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Verb   string          `json:"verb"`
	Params json.RawMessage `json:"params,omitempty"`
}

// verbError is the error envelope with a stable string code. Hint, when
// present, names the verb, CLI command, parameter, or closest spelling that
// resolves the failure; it is additive and always omitempty, so a consumer that
// reads only code and message is unaffected.
type verbError struct {
	Code    string    `json:"code"`
	Message string    `json:"message"`
	Hint    *VerbHint `json:"hint,omitempty"`
}

func (e *verbError) Error() string { return e.Code + ": " + e.Message }

// newVerbError builds a *verbError with the given code and message.
func newVerbError(code, message string) *verbError {
	return &verbError{Code: code, Message: message}
}

// verbResponse is one response line. Exactly one of Result or Error is set.
type verbResponse struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Result any             `json:"result,omitempty"`
	Error  *verbError      `json:"error,omitempty"`
}

// verbHandler executes one verb. params carries the raw JSON of the request's
// params object (may be empty). It returns a result value to serialize, or a
// *verbError describing why it failed.
type verbHandler func(d *Daemon, cs *connState, params json.RawMessage) (any, *verbError)

// verbParam documents one parameter of a verb for the list-verbs introspection
// output, so an agent can discover the full call shape without reading the docs.
type verbParam struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // string | int | bool | []string
	Required    bool     `json:"required,omitempty"`
	Description string   `json:"description"`
	Accepted    []string `json:"accepted,omitempty"` // closed value set, when there is one
	Default     string   `json:"default,omitempty"`
}

// verbEntry pairs a handler with the documentation list-verbs reports: a
// one-line description, the parameter schema, the result shape, and
// copy-pasteable examples.
type verbEntry struct {
	description string
	params      []verbParam
	// returns names the fields of a successful result. A caller could learn how
	// to make the call from params alone and still had to guess what came back,
	// which is half a contract.
	returns  []verbParam
	examples []string
	handler  verbHandler
}

// verbDoc is the serialized form of a verbEntry in the list-verbs result.
type verbDoc struct {
	Verb        string      `json:"verb"`
	Description string      `json:"description"`
	Params      []verbParam `json:"params"`
	Returns     []verbParam `json:"returns,omitempty"`
	Examples    []string    `json:"examples,omitempty"`
}

// sessionParam is the session selector shared by nearly every verb.
var sessionParam = verbParam{
	Name:        "session",
	Type:        "string",
	Description: "Session name. Omit to target the most recently active session.",
}

// windowParam is the window selector shared by window-targeted verbs.
var windowParam = verbParam{
	Name:        "window",
	Type:        "string",
	Description: "Window id or name. Omit to target the focused window.",
}

// verbRegistry is the dispatch table for every JSON verb the daemon supports.
// It is built once at package init so list-verbs and dispatch share one source
// of truth. It is populated in init() to avoid a static initialization cycle
// (list-verbs reads the registry).
var verbRegistry map[string]verbEntry

func init() {
	verbRegistry = map[string]verbEntry{
		"hello": {
			description: "Handshake: report the protocol version this daemon speaks and the version range it accepts.",
			params: []verbParam{
				{Name: "client", Type: "string", Description: "Name of the calling program, for the daemon log."},
				{Name: "version", Type: "string", Description: "Version string of the calling program."},
				{Name: "protocol", Type: "int", Description: "Protocol version the caller speaks. The daemon reports a mismatch rather than failing later."},
			},
			examples: []string{`{"id":1,"verb":"hello","params":{"client":"tuios","version":"1.2.3","protocol":1}}`},
			handler:  (*Daemon).verbHello,
		},
		"list-verbs": {
			description: "List every supported verb with its parameter schema and examples, plus the protocol version and error-code catalog.",
			params: []verbParam{
				{Name: "verb", Type: "string", Description: "Describe only this verb. Omit to describe all of them."},
			},
			examples: []string{
				`{"id":1,"verb":"list-verbs"}`,
				`{"id":1,"verb":"list-verbs","params":{"verb":"capture-pane"}}`,
			},
			handler: (*Daemon).verbListVerbs,
		},
		"list-dock-components": {
			description: "List the dock's components: what the bar is made of, what each cell reads, and what each component's command last did.",
			params:      []verbParam{sessionParam},
			returns: []verbParam{
				{Name: "components", Type: "[]string", Description: "One entry per placed component, in draw order, carrying its name, side, source, refresh mode, current text, last exit code, last run time and last error."},
			},
			examples: []string{
				`{"id":1,"verb":"list-dock-components"}`,
				`{"id":1,"verb":"list-dock-components","params":{"session":"work"}}`,
			},
			handler: (*Daemon).verbListDockComponents,
		},
		"refresh-dock": {
			description: "Re-run a dock component now, whatever its refresh mode says.",
			params: []verbParam{
				sessionParam,
				{Name: "component", Type: "string", Description: "Component to re-run, named as in the config file. Omit to re-run every one."},
			},
			returns: []verbParam{
				{Name: "component", Type: "string", Description: "The component that was refreshed, or \"all\"."},
			},
			examples: []string{
				`{"id":1,"verb":"refresh-dock","params":{"component":"agents"}}`,
				`{"id":1,"verb":"refresh-dock"}`,
			},
			handler: (*Daemon).verbRefreshDock,
		},
		"list-sessions": {
			description: "List all sessions the daemon holds.",
			examples:    []string{`{"id":1,"verb":"list-sessions"}`},
			handler:     (*Daemon).verbListSessions,
		},
		"session-info": {
			description: "Report details about one session.",
			params:      []verbParam{sessionParam},
			examples:    []string{`{"id":1,"verb":"session-info","params":{"session":"work"}}`},
			handler:     (*Daemon).verbSessionInfo,
		},
		"list-windows": {
			description: "List the windows in a session.",
			params:      []verbParam{sessionParam},
			examples:    []string{`{"id":1,"verb":"list-windows","params":{"session":"work"}}`},
			handler:     (*Daemon).verbListWindows,
		},
		"new-window": {
			description: "Create a new window, optionally on a named workspace and in a named directory.",
			params: []verbParam{
				sessionParam,
				{Name: "name", Type: "string", Description: "Name for the new window. Omit to use the shell's title."},
				{Name: "workspace", Type: "int", Description: "Workspace number to create the window on. Omit for the current workspace."},
				{Name: "cwd", Type: "string", Description: "Directory to start the shell in. Omit to inherit the daemon's."},
				{Name: "focus", Type: "bool", Description: "Focus the new window. Pass false to open a pane to work in later without moving the user out of the one they are in.", Default: "true"},
				{Name: "command", Type: "[]string", Description: "Argv to exec as the window's process instead of a shell. No shell parses it, so nothing needs quoting; the window closes when the program exits."},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string", Description: "Id of the created window, which is what to address it by afterwards."},
				{Name: "name", Type: "string", Description: "The window's name, generated when none was given."},
				{Name: "workspace", Type: "int", Description: "Workspace the window was created on."},
				{Name: "pty_id", Type: "string", Description: "Id of the window's PTY."},
				{Name: "focused", Type: "bool", Description: "Whether the window took the focus."},
				{Name: "unplaced", Type: "bool", Description: "True while the geometry is a placeholder the daemon chose without a viewport. An attached client replaces it; on a detached session it stays true and the reported size is nominal."},
			},
			examples: []string{
				`{"id":1,"verb":"new-window","params":{"session":"work","name":"build"}}`,
				`{"id":1,"verb":"new-window","params":{"session":"work","name":"tests","workspace":2,"cwd":"/src/api","focus":false}}`,
				`{"id":1,"verb":"new-window","params":{"session":"work","name":"htop","command":["/usr/bin/htop"]}}`,
			},
			handler: (*Daemon).verbNewWindow,
		},
		"split-window": {
			description: "Divide a pane, putting a new one beside it. Needs an attached client and tiling on, because the division is a geometry only a renderer can compute.",
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string", Description: "Window to split. Omit to split the focused one."},
				{Name: "direction", Type: "string", Required: true, Description: "Axis to cut on.", Accepted: splitDirections},
				{Name: "name", Type: "string", Description: "Name for the new window."},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string", Description: "Id of the pane the split created."},
				{Name: "direction", Type: "string", Description: "The axis that was cut."},
				{Name: "name", Type: "string", Description: "The new pane's name, when one was given."},
			},
			examples: []string{`{"id":1,"verb":"split-window","params":{"session":"work","window":"build","direction":"vertical","name":"logs"}}`},
			handler:  (*Daemon).verbSplitWindow,
		},
		"focus-window": {
			description: "Move the focus to a pane, naming it by target, by position, or by direction. Pass exactly one of window, relative or direction.",
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string", Description: "Window id or name to focus. Switches to that window's workspace."},
				{Name: "relative", Type: "string", Description: "Focus the next or previous window on the current workspace.", Accepted: focusRelatives},
				{Name: "direction", Type: "string", Description: "Focus the neighbouring pane in this direction. Needs an attached client, because it is a question about the viewport.", Accepted: focusDirections},
			},
			returns: []verbParam{
				{Name: "focused_window_id", Type: "string", Description: "Id of the window that now has the focus."},
				{Name: "current_workspace", Type: "int", Description: "Workspace now showing."},
				{Name: "window", Type: "object", Description: "The focused window's full row, in the same shape list-windows reports."},
			},
			examples: []string{
				`{"id":1,"verb":"focus-window","params":{"session":"work","window":"build"}}`,
				`{"id":1,"verb":"focus-window","params":{"session":"work","relative":"next"}}`,
			},
			handler: (*Daemon).verbFocusWindow,
		},
		"move-window": {
			description: "Move a window to another workspace.",
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string", Description: "Window to move. Omit to move the focused one."},
				{Name: "workspace", Type: "int", Required: true, Description: "Workspace number to move the window to."},
				{Name: "follow", Type: "bool", Description: "Switch to that workspace after moving.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string", Description: "Id of the window that moved."},
				{Name: "from_workspace", Type: "int", Description: "Workspace it was on."},
				{Name: "workspace", Type: "int", Description: "Workspace it is on now."},
				{Name: "current_workspace", Type: "int", Description: "Workspace showing after the call, which follow decides."},
			},
			examples: []string{`{"id":1,"verb":"move-window","params":{"session":"work","window":"build","workspace":2,"follow":true}}`},
			handler:  (*Daemon).verbMoveWindow,
		},
		"set-window": {
			description: "Change a window's own properties: what it is called and whether it is minimized. Pass whichever you mean.",
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string", Description: "Window to change. Omit for the focused one."},
				{Name: "name", Type: "string", Description: "New name. Pass an empty string to clear it and fall back to the shell's title."},
				{Name: "minimized", Type: "bool", Description: "Minimize the window, or restore it."},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string", Description: "Id of the window that changed."},
				{Name: "display_name", Type: "string", Description: "The name it shows now, which is the shell's title when the custom name was cleared."},
				{Name: "minimized", Type: "bool", Description: "Whether it is minimized now."},
			},
			examples: []string{`{"id":1,"verb":"set-window","params":{"session":"work","window":"build","name":"api tests","minimized":false}}`},
			handler:  (*Daemon).verbSetWindow,
		},
		"select-workspace": {
			description: "Show a workspace. This changes which workspace is displayed; set-workspace-name and set-workspace-order change a workspace's label and its position.",
			params: []verbParam{
				sessionParam,
				{Name: "workspace", Type: "int", Required: true, Description: "Workspace number to show."},
			},
			returns: []verbParam{
				{Name: "current_workspace", Type: "int", Description: "Workspace now showing."},
				{Name: "focused_window_id", Type: "string", Description: "Window focused on it, empty when it holds none."},
				{Name: "window_count", Type: "int", Description: "How many windows it holds."},
			},
			examples: []string{`{"id":1,"verb":"select-workspace","params":{"session":"work","workspace":2}}`},
			handler:  (*Daemon).verbSelectWorkspace,
		},
		"list-workspaces": {
			description: "List every workspace with its name, how many windows it holds, and which one is showing.",
			params:      []verbParam{sessionParam},
			returns: []verbParam{
				{Name: "workspaces", Type: "[]object", Description: "One row per workspace: workspace, name, window_count, focused_window_id, current."},
				{Name: "current_workspace", Type: "int", Description: "Workspace showing."},
				{Name: "order", Type: "[]int", Description: "Display order, empty when the workspaces are in their plain ascending order."},
			},
			examples: []string{`{"id":1,"verb":"list-workspaces","params":{"session":"work"}}`},
			handler:  (*Daemon).verbListWorkspaces,
		},
		"set-layout": {
			description: "Turn tiling on or off and tidy the splits. Needs an attached client, because a layout is a geometry only a renderer can compute.",
			params: []verbParam{
				sessionParam,
				{Name: "tiling", Type: "bool", Description: "Tile the panes automatically, or let them float."},
				{Name: "equalize", Type: "bool", Description: "Reset every split ratio so the panes share the space evenly.", Default: "false"},
				{Name: "rotate", Type: "bool", Description: "Flip the axis of the split holding the focused pane.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "tiling_mode", Type: "string", Description: `"tiling" or "floating".`},
				{Name: "layout_mode", Type: "string", Description: `Which tiling layout is in effect: bsp, master-stack, scrolling, or "unknown" on a session no client has reported one for.`},
				{Name: "master_ratio", Type: "float", Description: "Fraction of the screen the master pane takes."},
			},
			examples: []string{`{"id":1,"verb":"set-layout","params":{"session":"work","tiling":true,"equalize":true}}`},
			handler:  (*Daemon).verbSetLayout,
		},
		"run-command": {
			description: "Run one tape command, the vocabulary the keybindings are written in. This is the escape hatch: prefer a verb where one exists, because a verb reports what it changed and this reports only that the command ran.",
			params: []verbParam{
				sessionParam,
				{Name: "command", Type: "string", Required: true, Description: `Tape command name, e.g. "ToggleZoom" or "SnapLeft".`},
				{Name: "args", Type: "[]string", Description: "Arguments for the command."},
			},
			returns: []verbParam{
				{Name: "command", Type: "string", Description: "The command that ran."},
				{Name: "routed", Type: "bool", Description: "True when an attached client ran it, false when the daemon did. The two are the same command; this says which had to be available for it to work."},
			},
			examples: []string{`{"id":1,"verb":"run-command","params":{"session":"work","command":"ToggleZoom"}}`},
			handler:  (*Daemon).verbRunCommand,
		},
		"close-window": {
			description: "Close a window.",
			params:      []verbParam{sessionParam, windowParam},
			examples:    []string{`{"id":1,"verb":"close-window","params":{"session":"work","window":"build"}}`},
			handler:     (*Daemon).verbCloseWindow,
		},
		"send-keys": {
			description: "Send parsed key tokens to a window.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "keys", Type: "string", Required: true, Description: `Key sequence, e.g. "ctrl+b,n" or "Hello World".`},
				{Name: "literal", Type: "bool", Description: "Send the keys to the PTY without parsing them as key names.", Default: "false"},
				{Name: "raw", Type: "bool", Description: "Treat every character as its own key instead of splitting on spaces and commas.", Default: "false"},
			},
			examples: []string{`{"id":1,"verb":"send-keys","params":{"session":"work","keys":"ls,Enter"}}`},
			handler:  (*Daemon).verbSendKeys,
		},
		"send-text": {
			description: "Send literal text to a window's PTY.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "text", Type: "string", Required: true, Description: "Text written verbatim to the PTY."},
			},
			examples: []string{`{"id":1,"verb":"send-text","params":{"session":"work","text":"echo hi\n"}}`},
			handler:  (*Daemon).verbSendText,
		},
		"capture-pane": {
			description: "Capture a pane's content.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "source", Type: "string", Description: "Which buffer to capture.", Accepted: captureSources, Default: "visible"},
				{Name: "styled", Type: "bool", Description: "Include ANSI styling in the captured text.", Default: "false"},
				// scrollback and ansi predate source and styled and are still
				// accepted; they are declared so a caller reading only list-verbs
				// can see the whole call shape.
				{Name: "scrollback", Type: "bool", Description: `Older spelling of source "recent".`, Default: "false"},
				{Name: "ansi", Type: "bool", Description: "Older spelling of styled.", Default: "false"},
				{Name: "lines", Type: "int", Description: "Keep only the last N non-empty-tailed lines, so the blank rows below the cursor do not count. Ignored when start or end is given."},
				{Name: "start", Type: "int", Description: "1-based inclusive first line of the region to keep."},
				{Name: "end", Type: "int", Description: "1-based inclusive last line of the region to keep."},
			},
			examples: []string{`{"id":1,"verb":"capture-pane","params":{"session":"work","source":"recent","lines":50}}`},
			handler:  (*Daemon).verbCapturePane,
		},
		"resize": {
			description: "Resize a window's PTY.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "width", Type: "int", Required: true, Description: "New width in columns. Must be positive."},
				{Name: "height", Type: "int", Required: true, Description: "New height in rows. Must be positive."},
			},
			examples: []string{`{"id":1,"verb":"resize","params":{"session":"work","width":120,"height":40}}`},
			handler:  (*Daemon).verbResize,
		},
		"kill-session": {
			description: "Terminate a session and every window in it.",
			params: []verbParam{
				{Name: "session", Type: "string", Required: true, Description: "Session to terminate."},
			},
			examples: []string{`{"id":1,"verb":"kill-session","params":{"session":"work"}}`},
			handler:  (*Daemon).verbKillSession,
		},
		"list-options": {
			description: "List every settable configuration path with its type, default, accepted values and description. This is how to find an option rather than guess it.",
			params: []verbParam{
				sessionParam,
				{Name: "section", Type: "string", Description: "Only options in this group, e.g. sidebar or dock. The full set of section names is reported on every call."},
				{Name: "prefix", Type: "string", Description: `Only options whose path starts with this, e.g. "appearance.sidebar.".`},
			},
			returns: []verbParam{
				{Name: "options", Type: "[]object", Description: "One row per option: path, type, section, description, default, and accepted/min/max/deprecated where they apply. session_value is present only where this session carries an override."},
				{Name: "sections", Type: "[]string", Description: "Every section name, whatever the filter matched."},
				{Name: "total", Type: "int", Description: "How many options the filter matched."},
			},
			examples: []string{
				`{"id":1,"verb":"list-options"}`,
				`{"id":1,"verb":"list-options","params":{"section":"sidebar"}}`,
			},
			handler: (*Daemon).verbListOptions,
		},
		"set-option": {
			description: "Set a configuration option, applied live when a client is attached. The path and the value are both checked against the option registry, so a call that could have no effect fails rather than reporting success.",
			params: []verbParam{
				sessionParam,
				{Name: "key", Type: "string", Required: true, Description: `Option path, e.g. "appearance.sidebar.enabled". Call list-options for the full set.`},
				{Name: "value", Type: "string", Description: "New value, as a string. Booleans take true/false/on/off/1/0/yes/no."},
			},
			returns: []verbParam{
				{Name: "key", Type: "string", Description: "The option that was set."},
				{Name: "value", Type: "string", Description: "The value recorded."},
				{Name: "applied", Type: "bool", Description: "Whether an attached client applied it to the live display."},
				{Name: "reason", Type: "string", Description: "Why applied is false, when it is. Present only then."},
				{Name: "deprecated", Type: "string", Description: "Why this path is deprecated and what replaced it. Present only for a deprecated path."},
			},
			examples: []string{
				`{"id":1,"verb":"set-option","params":{"session":"work","key":"appearance.sidebar.enabled","value":"true"}}`,
				`{"id":1,"verb":"set-option","params":{"session":"work","key":"appearance.dockbar_position","value":"top"}}`,
			},
			handler: (*Daemon).verbSetOption,
		},
		"list-themes": {
			description: "List the registered themes and describe one: its colours as hex and the contrast of each against its own background. A theme is the one part of the appearance list-options cannot describe, because its value is a name from an open set standing for a palette kept elsewhere.",
			params: []verbParam{
				sessionParam,
				{Name: "theme", Type: "string", Description: "Describe this theme as well as listing. Omit to list only."},
				{Name: "filter", Type: "string", Description: "Only ids containing this, case-insensitively, e.g. catppuccin."},
			},
			returns: []verbParam{
				{Name: "themes", Type: "[]string", Description: "Matching theme ids, capped at 100; truncated says when the cap applied."},
				{Name: "total", Type: "int", Description: "How many themes are registered in all."},
				{Name: "matched", Type: "int", Description: "How many the filter matched, before the cap."},
				{Name: "active", Type: "string", Description: "The theme this session is set to. Empty means no theme, which is the terminal's own colours."},
				{Name: "active_source", Type: "string", Description: `"session" for a theme set on this session, "default" for the built-in.`, Accepted: []string{"session", "default"}},
				{Name: "themes_dir", Type: "string", Description: "Where a custom theme file goes. Writing <id>.json here registers it; no restart is needed."},
				{Name: "problems", Type: "[]string", Description: "One line per theme file that could not be read, with the reason. Present only when a file is malformed."},
				{Name: "palette", Type: "object", Description: "Present when theme was given: id, display_name, dark, bg, fg, cursor, swatches (each with hex, ratio, floor, passes) and illegible, the names of the swatches that did not clear their floor."},
			},
			examples: []string{
				`{"id":1,"verb":"list-themes","params":{"filter":"catppuccin"}}`,
				`{"id":1,"verb":"list-themes","params":{"session":"work","theme":"catppuccin_mocha"}}`,
			},
			handler: (*Daemon).verbListThemes,
		},
		"list-glyphs": {
			description: "List the glyph sets and describe one: the roles it names, and the characters that would actually be drawn if it were selected. A glyph set is the shape half of a rice, the way a theme is the colour half, and like a theme its value is a name from an open set standing for a document kept elsewhere.",
			params: []verbParam{
				sessionParam,
				{Name: "glyphs", Type: "string", Description: "Describe this set as well as listing. Omit to list only."},
			},
			returns: []verbParam{
				{Name: "sets", Type: "[]string", Description: "Every set id, built-ins first and then the user's."},
				{Name: "roles", Type: "[]string", Description: "Every role a set can name, which is what to write in a set file."},
				{Name: "total", Type: "int", Description: "How many sets there are."},
				{Name: "glyphs_dir", Type: "string", Description: "Directory user sets are read from; write <id>.json there."},
				{Name: "active", Type: "string", Description: "The set in effect, with active_source saying whether it came from the session or the default."},
				{Name: "problems", Type: "[]string", Description: "One line per set file that could not be read and per role dropped for being the wrong width. Present only when there are any."},
				{Name: "set", Type: "object", Description: "Present when glyphs was given: id, display_name, inherits, ascii, names (the roles the set states) and drawn (the character each role would actually render as, defaults folded in)."},
			},
			examples: []string{
				`{"id":1,"verb":"list-glyphs","params":{}}`,
				`{"id":1,"verb":"list-glyphs","params":{"session":"work","glyphs":"heavy"}}`,
			},
			handler: (*Daemon).verbListGlyphs,
		},
		"get-option": {
			description: "Read an option, preferring what this session was told and falling back to what the option does untold.",
			params: []verbParam{
				sessionParam,
				{Name: "key", Type: "string", Required: true, Description: "Option path to read."},
			},
			returns: []verbParam{
				{Name: "key", Type: "string", Description: "The option that was read."},
				{Name: "value", Type: "string", Description: "The value in effect."},
				{Name: "source", Type: "string", Description: `Where the value came from: "session" for an override set on this session, "default" for the built-in.`, Accepted: []string{"session", "default"}},
				{Name: "default", Type: "string", Description: "What the option does untold, so a caller can tell an override from the default it matches."},
				{Name: "option_type", Type: "string", Description: "bool, int or string."},
			},
			examples: []string{`{"id":1,"verb":"get-option","params":{"session":"work","key":"appearance.dockbar_position"}}`},
			handler:  (*Daemon).verbGetOption,
		},
		"subscribe": {
			description: "Open a long-lived event stream on this connection. Events are delivered from the moment of subscription; there is no backfill.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "types", Type: "[]string", Description: "Only deliver these event types. Omit for all of them.", Accepted: knownEventTypes},
				{Name: "queue", Type: "int", Description: "Buffered events before the stream marks a gap.", Default: "256"},
			},
			examples: []string{`{"id":1,"verb":"subscribe","params":{"session":"work","types":["window-created","window-closed"]}}`},
			handler:  (*Daemon).verbSubscribe,
		},
		"unsubscribe": {
			description: "Close this connection's event stream.",
			examples:    []string{`{"id":1,"verb":"unsubscribe"}`},
			handler:     (*Daemon).verbUnsubscribe,
		},
		"set-session-name": {
			description: "Set a session's display name. The session's identity is unchanged: it keeps the same name for addressing, persistence and TUIOS_SESSION.",
			params: []verbParam{
				sessionParam,
				{Name: "name", Type: "string", Description: "Display label for the session. Omit or pass an empty string to clear it and fall back to the session name."},
			},
			examples: []string{`{"id":1,"verb":"set-session-name","params":{"session":"work","name":"Payments API"}}`},
			handler:  (*Daemon).verbSetSessionName,
		},
		"set-session-accent": {
			description: "Set a session's accent, shared by every client attached to it and kept across a reattach.",
			params: []verbParam{
				sessionParam,
				{Name: "accent", Type: "string", Description: "Colour name from the ANSI sixteen (\"cyan\", \"bright blue\") or a #rrggbb literal, recorded verbatim. Omit or pass an empty string to clear it and let the client pick the session's colour."},
			},
			examples: []string{`{"id":1,"verb":"set-session-accent","params":{"session":"work","accent":"cyan"}}`},
			handler:  (*Daemon).verbSetSessionAccent,
		},
		"set-workspace-name": {
			description: "Name a workspace. The number stays the workspace's identity and is the label an unnamed workspace shows.",
			params: []verbParam{
				sessionParam,
				{Name: "workspace", Type: "int", Required: true, Description: "Workspace number to name."},
				{Name: "name", Type: "string", Description: "Label for the workspace. Omit or pass an empty string to clear it and fall back to the number."},
			},
			examples: []string{`{"id":1,"verb":"set-workspace-name","params":{"session":"work","workspace":2,"name":"review"}}`},
			handler:  (*Daemon).verbSetWorkspaceName,
		},
		"set-workspace-order": {
			description: "Arrange the workspaces. This is the order they are shown in and nothing else: every workspace keeps its number, which is what the verbs, the keys and each window go on addressing it by.",
			params: []verbParam{
				sessionParam,
				{Name: "order", Type: "[]int", Required: true, Description: "Workspace numbers in the order to show them. Numbers outside the session's range and repeats of one already placed are dropped; any workspace the list omits keeps its place after the ones it names. An ascending order clears the arrangement."},
			},
			examples: []string{`{"id":1,"verb":"set-workspace-order","params":{"session":"work","order":[3,1,2]}}`},
			handler:  (*Daemon).verbSetWorkspaceOrder,
		},
		"set-agent-state": {
			description: "Set the agent state a window's pane reports (working, needs_input, idle, done, errored, or none to clear). A pane reports its own state by calling this against the daemon socket.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "state", Type: "string", Required: true, Description: "The agent state to record.", Accepted: AgentStateNames},
				{Name: "message", Type: "string", Description: "Optional short note reported with the state, e.g. what the agent is waiting for."},
				{Name: "source", Type: "string", Description: "Where the state came from. A source ranked below the one that last set the window is refused, and the result reports applied false with the state that stands.", Accepted: AgentSourceNames, Default: "report"},
				{Name: "harness", Type: "string", Description: "Optional id of the harness the state is about, reported back by get-agent-state."},
			},
			examples: []string{
				`{"id":1,"verb":"set-agent-state","params":{"session":"work","state":"needs_input","message":"awaiting approval"}}`,
				`{"id":1,"verb":"set-agent-state","params":{"session":"work","state":"working","source":"osc","harness":"claude-code"}}`,
			},
			handler: (*Daemon).verbSetAgentState,
		},
		"get-agent-state": {
			description: "Read the agent state a window's pane last reported, with its optional message, the time it was set, and which source and harness it came from.",
			params:      []verbParam{sessionParam, windowParam},
			examples:    []string{`{"id":1,"verb":"get-agent-state","params":{"session":"work","window":"build"}}`},
			handler:     (*Daemon).verbGetAgentState,
		},
		"explain-agent-detect": {
			description: "Show what the foreground-process detector sees in a pane and what every harness manifest makes of it: the comm, argv and executable it read, which manifest matched and on which predicate, and for each that refused, what it was comparing against.",
			params:      []verbParam{sessionParam, windowParam},
			examples: []string{
				`{"id":1,"verb":"explain-agent-detect","params":{"session":"work","window":"build"}}`,
			},
			handler: (*Daemon).verbExplainAgentDetect,
		},
		"explain-agent-screen": {
			description: "Dump a pane's screen tail exactly as the harness screen rules read it, with what every rule made of it and which one fired. This is the tool for writing a rule: it says what the classifier saw and, for each rule that refused, which strings were the reason.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "harness", Type: "string", Description: "Run this harness's rules instead of the one the pane is attributed to, for trying a rule against a pane nothing has claimed yet."},
				{Name: "lines", Type: "int", Description: "Read this many lines from the bottom instead of the manifest's, for checking whether a rule needs to see further up."},
			},
			examples: []string{
				`{"id":1,"verb":"explain-agent-screen","params":{"session":"work","window":"build"}}`,
				`{"id":1,"verb":"explain-agent-screen","params":{"session":"work","harness":"codex","lines":20}}`,
			},
			handler: (*Daemon).verbExplainAgentScreen,
		},
		"wait-for": {
			description: "Block until a condition matches, or fail with the timeout code.",
			params: []verbParam{
				{Name: "condition", Type: "string", Required: true, Description: "Condition to wait for.", Accepted: waitConditions},
				sessionParam,
				windowParam,
				{Name: "pattern", Type: "string", Description: "Regular expression, required by window-output."},
				{Name: "source", Type: "string", Description: "Which buffer window-output matches against. The default includes scrollback, so output that has already scrolled past still matches.", Accepted: captureSources, Default: "recent"},
				{Name: "idle", Type: "int", Description: "Milliseconds of silence that count as idle, for window-idle.", Default: "500"},
				{Name: "until", Type: "string", Description: "Agent state(s) to wait for, comma-separated, required by agent-state. With no window, any window in the session reaching one of them matches.", Accepted: AgentStateNames},
				{Name: "timeout", Type: "int", Description: "Milliseconds to wait before failing with the timeout code.", Default: "30000"},
			},
			examples: []string{
				`{"id":1,"verb":"wait-for","params":{"condition":"window-output","session":"work","pattern":"done","timeout":10000}}`,
				`{"id":1,"verb":"wait-for","params":{"condition":"agent-state","session":"work","until":"needs_input,idle"}}`,
			},
			handler: (*Daemon).verbWaitFor,
		},
	}
}

// detectJSONClient inspects the first byte of the connection without consuming
// it. A JSON verb-protocol client's first byte is '{' or leading whitespace; a
// binary client's is the high byte of a big-endian length prefix (0x00/0x01 for
// any sub-16MB frame), so the two never collide. It returns true when the
// connection should be handled as JSON. On any read error it returns false and
// lets the (short) binary path observe the same error and clean up.
func (d *Daemon) detectJSONClient(cs *connState, br *bufio.Reader) bool {
	conn := cs.conn
	for {
		select {
		case <-d.ctx.Done():
			return false
		case <-cs.done:
			return false
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		peeked, err := br.Peek(1)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			// EOF or hard error: not JSON; the binary loop will re-observe it.
			_ = conn.SetReadDeadline(time.Time{})
			return false
		}

		_ = conn.SetReadDeadline(time.Time{})
		switch peeked[0] {
		case '{', ' ', '\t', '\n', '\r':
			return true
		default:
			return false
		}
	}
}

// handleJSONConnection runs the read/dispatch/respond loop for a JSON client. It
// reads newline-delimited request objects, dispatches each, and writes one
// response line per request. It blocks until the connection closes (which
// shutdown and drop both trigger, unblocking the read).
func (d *Daemon) handleJSONConnection(cs *connState, br *bufio.Reader) {
	// No aggressive read deadline: an idle JSON control connection should not be
	// dropped mid-wait. Shutdown and drop close the connection, which unblocks
	// the scan and ends the loop.
	_ = cs.conn.SetReadDeadline(time.Time{})

	LogBasic("Client %s using JSON verb protocol", cs.clientID)

	sc := bufio.NewScanner(br)
	// Cap a single request line at the same 16MB ceiling as a binary frame so a
	// runaway client cannot exhaust memory.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for sc.Scan() {
		select {
		case <-d.ctx.Done():
			return
		case <-cs.done:
			return
		default:
		}

		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		// Copy the line: Scanner reuses its buffer on the next Scan, and a routed
		// verb may block (routeToTUISync) while holding a reference to params.
		lineCopy := make([]byte, len(line))
		copy(lineCopy, line)

		if err := d.dispatchVerbLine(cs, lineCopy); err != nil {
			// A write failure means the connection is gone; stop.
			return
		}
	}
}

// dispatchVerbLine parses one request line, runs its verb, and writes the
// response. It returns an error only when writing the response fails (the
// connection is unusable); verb-level failures are returned to the client as an
// error envelope, not as a Go error.
func (d *Daemon) dispatchVerbLine(cs *connState, line []byte) error {
	var req verbRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return d.writeVerbResponse(cs, &verbResponse{
			Error: newVerbError(ErrVerbInvalidRequest, "malformed JSON request: "+err.Error()),
		})
	}

	if req.Verb == "" {
		return d.writeVerbResponse(cs, &verbResponse{
			ID: req.ID,
			Error: hintedVerbError(ErrVerbInvalidRequest, "request is missing the \"verb\" field", &VerbHint{
				Param:     "verb",
				Verb:      "list-verbs",
				Available: knownVerbNames(),
				Detail:    `Every request line is an object of the form {"id":1,"verb":"list-verbs","params":{}}.`,
			}),
		})
	}

	entry, ok := verbRegistry[req.Verb]
	if !ok {
		known := knownVerbNames()
		return d.writeVerbResponse(cs, &verbResponse{
			ID: req.ID,
			Error: hintedVerbError(ErrVerbUnknownVerb, "unknown verb "+echoName(req.Verb), &VerbHint{
				Verb:       "list-verbs",
				Command:    "tuios list-verbs",
				DidYouMean: closestMatch(req.Verb, known),
				Available:  known,
				Detail:     "Call list-verbs for every verb with its parameter schema and examples.",
			}),
		})
	}

	if verr := checkParamNames(req.Verb, entry, req.Params); verr != nil {
		return d.writeVerbResponse(cs, &verbResponse{ID: req.ID, Error: verr})
	}

	result, verr := entry.handler(d, cs, req.Params)
	if verr != nil {
		return d.writeVerbResponse(cs, &verbResponse{ID: req.ID, Error: verr})
	}
	if err := d.writeVerbResponse(cs, &verbResponse{ID: req.ID, Result: result}); err != nil {
		return err
	}
	// A subscribe verb stashes its fresh subscription for the streamer, which must
	// start only after the ack line above is on the wire so no event precedes it.
	d.startPendingStream(cs)
	return nil
}

// checkParamNames refuses a request carrying a parameter the verb does not
// declare, before the handler ever sees it.
//
// Dropping an unknown field is what encoding/json does by default, and it is the
// worst answer available to a machine caller: new-window with a workspace the
// verb did not yet take reported a created window and put it wherever it liked,
// with a success envelope and no way to tell. A caller that guessed a name, or
// that is newer than the daemon it reached, has to learn that from the response
// rather than from the pane it is looking at.
//
// The check runs against the same schema list-verbs publishes, so the two cannot
// drift: a parameter a handler reads but does not declare is unreachable, and a
// caller that read list-verbs can always spell every accepted name.
func checkParamNames(verb string, entry verbEntry, params json.RawMessage) *verbError {
	if len(bytes.TrimSpace(params)) == 0 {
		return nil
	}
	var got map[string]json.RawMessage
	// A params value that is not an object at all is left to the handler's
	// decode, which already reports it as invalid_params with the decode error.
	if err := json.Unmarshal(params, &got); err != nil {
		return nil
	}

	accepted := make([]string, 0, len(entry.params))
	for _, p := range entry.params {
		accepted = append(accepted, p.Name)
	}

	for name := range got {
		if slices.ContainsFunc(entry.params, func(p verbParam) bool { return p.Name == name }) {
			continue
		}
		return hintedVerbError(ErrVerbInvalidParams,
			"verb "+verb+" has no parameter "+echoName(name),
			&VerbHint{
				Param:      name,
				Verb:       "list-verbs",
				Command:    "tuios list-verbs " + verb,
				DidYouMean: closestMatch(name, accepted),
				Accepted:   accepted,
				Detail:     "an unrecognised parameter is refused rather than ignored, so a call that cannot do what it asked for never reports success.",
			})
	}
	return nil
}

// writeVerbResponse serializes resp as one newline-terminated JSON line and
// writes it under the connection's send mutex with a write deadline.
func (d *Daemon) writeVerbResponse(cs *connState, resp *verbResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		// Should not happen; fall back to a minimal internal error line.
		data = []byte(`{"error":{"code":"internal","message":"failed to encode response"}}`)
	}
	data = append(data, '\n')

	cs.sendMu.Lock()
	defer cs.sendMu.Unlock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, werr := cs.conn.Write(data)
	return werr
}

// verbListVerbs implements the list-verbs introspection verb. It reports every
// verb with its parameter schema and examples, the protocol version range, and
// the error-code catalog, which together are enough to drive the control plane
// without reading the documentation. Naming a verb narrows the output to that
// one verb.
func (d *Daemon) verbListVerbs(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Verb string `json:"verb"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	if p.Verb != "" {
		entry, ok := verbRegistry[p.Verb]
		if !ok {
			known := knownVerbNames()
			return nil, hintedVerbError(ErrVerbUnknownVerb, "unknown verb "+echoName(p.Verb), &VerbHint{
				Param:      "verb",
				DidYouMean: closestMatch(p.Verb, known),
				Available:  known,
			})
		}
		return map[string]any{
			"type":           "verb_list",
			"version":        VerbProtocolVersion,
			"min_version":    MinVerbProtocolVersion,
			"daemon_version": d.version,
			"verbs":          []verbDoc{describeVerb(p.Verb, entry)},
			"error_codes":    errorCodeCatalog,
			"envelope":       verbEnvelopeDoc,
		}, nil
	}

	names := knownVerbNames()
	verbs := make([]verbDoc, 0, len(names))
	for _, name := range names {
		verbs = append(verbs, describeVerb(name, verbRegistry[name]))
	}
	return map[string]any{
		"type":           "verb_list",
		"version":        VerbProtocolVersion,
		"min_version":    MinVerbProtocolVersion,
		"daemon_version": d.version,
		"verbs":          verbs,
		"error_codes":    errorCodeCatalog,
		"envelope":       verbEnvelopeDoc,
	}, nil
}

// verbEnvelopeDoc describes the request and response envelopes themselves, so a
// caller that has only ever seen list-verbs knows how to frame a call.
var verbEnvelopeDoc = map[string]any{
	"transport": "One JSON object per line on the daemon socket; one response line per request line.",
	"request":   `{"id":<any>,"verb":"<name>","params":{...}}`,
	"success":   `{"id":<echoed>,"result":{"type":"<result type>",...}}`,
	"failure":   `{"id":<echoed>,"error":{"code":"<stable code>","message":"...","hint":{...}}}`,
	"hint":      "Present on most failures. Names the verb or CLI command that resolves it, the offending parameter and its accepted values, the closest matching name, and what does exist.",
}

// describeVerb renders one registry entry as its documented form.
func describeVerb(name string, entry verbEntry) verbDoc {
	params := entry.params
	if params == nil {
		params = []verbParam{}
	}
	return verbDoc{
		Verb:        name,
		Description: entry.description,
		Params:      params,
		Returns:     entry.returns,
		Examples:    entry.examples,
	}
}

// verbHello implements the handshake verb. It exists so a version mismatch is
// reported as a protocol_mismatch error on a live connection rather than
// surfacing as a framing failure or a reset connection several calls later.
//
// A daemon that predates this verb answers unknown_verb, which still identifies
// it as a working but older daemon; a daemon that predates the whole JSON
// protocol closes the connection, which the client reports as a mismatch too.
func (d *Daemon) verbHello(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Client   string `json:"client"`
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	if p.Protocol > VerbProtocolVersion {
		return nil, hintedVerbError(ErrVerbProtocolMismatch,
			fmt.Sprintf("client speaks protocol %d but this daemon only speaks up to %d", p.Protocol, VerbProtocolVersion),
			&VerbHint{
				Command: "tuios kill-server",
				Detail: fmt.Sprintf("The daemon (version %s) is older than the client (version %s) and was left running across an upgrade. Restarting it lets the newer client connect.",
					d.version, p.Version),
			})
	}
	if p.Protocol > 0 && p.Protocol < MinVerbProtocolVersion {
		return nil, hintedVerbError(ErrVerbProtocolMismatch,
			fmt.Sprintf("client speaks protocol %d but this daemon no longer serves anything below %d", p.Protocol, MinVerbProtocolVersion),
			&VerbHint{
				Detail: fmt.Sprintf("The client (version %s) is older than the daemon (version %s). Upgrade the client.", p.Version, d.version),
			})
	}

	if p.Client != "" {
		LogBasic("Client %s identified as %s %s (protocol %d)", cs.clientID, p.Client, p.Version, p.Protocol)
	}

	return map[string]any{
		"type":           "hello",
		"protocol":       VerbProtocolVersion,
		"min_protocol":   MinVerbProtocolVersion,
		"daemon_version": d.version,
		"pid":            os.Getpid(),
		"sessions":       len(d.manager.ListSessions()),
	}, nil
}
