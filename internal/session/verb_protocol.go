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

	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	ErrVerbSessionExists   = "session_exists"    // new-session was given a name the daemon already holds
	ErrVerbWindowNotFound  = "window_not_found"  // window target did not resolve
	ErrVerbNoWindows       = "no_windows"        // session has no windows to act on
	ErrVerbPTYNotFound     = "pty_not_found"     // the target window has no live PTY
	ErrVerbNeedsClient     = "needs_client"      // verb needs a live renderer that is not attached
	ErrVerbOptionNotFound  = "option_not_found"  // get-option key was never set
	ErrVerbCommandFailed   = "command_failed"    // a verb routed to the attached client came back failed
	ErrVerbTimeout         = "timeout"           // a wait-for condition did not match before its timeout
	ErrVerbInternal        = "internal"          // unexpected server-side failure

	// ErrVerbNotReady reports that a cross-agent verb declined to act because
	// the target agent was mid-turn. It is distinct from timeout: nothing was
	// waited for in vain, the daemon refused to type over a working agent.
	ErrVerbNotReady = "not_ready"
	// ErrVerbAgentBlocked reports that ask-agent declined to type at an agent on
	// needs_input. Such an agent is waiting on a prompt, most often a permission
	// menu, and text typed there is read as the answer. Nothing was written. It
	// is distinct from not_ready because waiting does not clear it: somebody has
	// to read the prompt and answer it.
	ErrVerbAgentBlocked = "agent_blocked"
	// ErrVerbLoopRefused reports a call refused because it would loop: a pane
	// addressing itself, or an ask that would close a cycle with one already in
	// flight. Its remedy is to restructure, which is why it does not share a
	// code with the rate cap, whose remedy is to wait.
	ErrVerbLoopRefused = "loop_refused"
	// ErrVerbNoKeyboard reports an ask addressed to the person, who has an
	// inbox and no pane. The remedy is a message, which the hint spells out.
	ErrVerbNoKeyboard = "no_keyboard"
	// ErrVerbRateLimited reports a sender over the message rate cap.
	ErrVerbRateLimited = "rate_limited"
	// ErrVerbPromptStalled reports a prompt that was pasted and submitted, after
	// which the pane showed no sign of taking it within the stall window: its
	// agent state did not turn working or needs_input, and for a harness that
	// cannot show working, it printed nothing either. The text was typed, so the
	// remedy is to look at the pane, not to send it again. See prompt_gate.go.
	ErrVerbPromptStalled = "prompt_stalled"
	// ErrVerbForbidden reports a call refused because the caller may not do
	// what it asked. Today that is a process running inside a pane of this
	// daemon asking to act as the person: sending from human, or asking from
	// human. Nothing was done. See human_origin.go.
	ErrVerbForbidden = "forbidden"
	// ErrVerbNotHuman reports a call only the person at an attached client
	// may make, made without the nonce that attach issued. dismiss-attention
	// and reply-approval raise it: an agent cannot clear what is waiting for
	// the person, or answer a permission prompt for them.
	ErrVerbNotHuman = "not_human"
	// ErrVerbPromptChanged reports a respond that pressed nothing because the
	// prompt it would answer is not the one the caller meant: the pane left
	// needs_input, no rule reads a prompt on it now, the prompt differs from
	// the prompt_id the caller read, or another client answered it first. The
	// remedy is to read the prompt again.
	ErrVerbPromptChanged = "prompt_changed"
	// ErrVerbNotResumable reports a resume-agent call for a pane with no
	// conversation it can resume: none was recorded, the harness has no
	// resume command, the recorded id cannot be typed safely, or the pane is
	// on another machine. Nothing was typed.
	ErrVerbNotResumable = "not_resumable"
	// ErrVerbNoShellIntegration reports a call that needs the pane's shell to
	// mark its commands with OSC 133, made on a pane whose shell never has:
	// run, and capture-pane with source last-command-output. Nothing was
	// typed.
	ErrVerbNoShellIntegration = "no_shell_integration"
	// ErrVerbNotAtPrompt reports a run refused because the pane's shell is
	// not at its prompt: a command is running there, and text typed now would
	// go to it. Nothing was typed.
	ErrVerbNotAtPrompt = "not_at_prompt"
	// ErrVerbConfirmRequired reports a write addressed by selector that was
	// not sent: it named no confirm token, or a token for a different set of
	// panes than the selector matches now. The hint lists the set and carries
	// its token. See selector.go.
	ErrVerbConfirmRequired = "confirm_required"

	// ErrVerbProtocolMismatch reports that the caller's protocol version is
	// outside the range this daemon accepts. It is only ever produced by the
	// hello verb, which exists so a mismatch is reported in this shape rather
	// than surfacing later as a framing or decode failure.
	ErrVerbProtocolMismatch = "protocol_mismatch"
)

// The two federation error codes live in verb_hosts.go beside the verbs that
// raise them: ErrVerbUnknownHost and ErrVerbHostUnreachable. Both are final.
// A caller that gets either must report it, never retry into a different host
// name, because reaching the wrong machine is worse than reaching none.

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
	Type        string   `json:"type"` // string | int | bool | []string | []int | object
	Required    bool     `json:"required,omitempty"`
	Description string   `json:"description"`
	Accepted    []string `json:"accepted,omitempty"` // closed value set, when there is one
	Default     string   `json:"default,omitempty"`
	// Nullable says the field can be null as well as its type. Only a
	// returned field sets it: a parameter is omitted rather than sent null.
	Nullable bool `json:"nullable,omitempty"`
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

// identityReturn, confidenceReturn and evidenceAgeReturn are the three
// detection fields get-agent-state, explain-agent-detect and list-agents share,
// declared once so the three verbs describe them in the same words.
var (
	identityReturn    = verbParam{Name: "identity", Type: "string", Description: "What named the agent: report (the harness named itself), manifest (a manifest rule matched the process), list (a name list matched the process) or hint (TUIOS_AGENT in the process environment). Empty when nothing named it.", Accepted: []string{"report", "manifest", "list", "hint", ""}}
	confidenceReturn  = verbParam{Name: "confidence", Type: "string", Description: "How sure the identity is: certain for report, strong for manifest, list and hint, none when nothing named the agent.", Accepted: []string{"certain", "strong", "none"}}
	evidenceAgeReturn = verbParam{Name: "evidence_age_ms", Type: "int", Nullable: true, Description: "Milliseconds since the last evidence about the state arrived. For a state the agent or a rule reported, that is the report. For a state the detector or the silence timer inferred (source detect or stall), it is the later of that and the pane's last output. A look that reads back the same claim does not reset it. null when nothing ever set a state."}
)

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

// selectorSyntax is the one sentence every select param shares.
const selectorSyntax = "A selector: space-separated key:value terms, all of which must match, each with comma-separated alternatives. Keys: harness (id or program name), state, needs:you, session (glob), group (fan-out group, glob), host (local or a host name, glob), name (window name, glob), cwd (the directory or under it; ~ is home)."

// selectWriteParams are the two params a write addressed by selector takes.
// what says what the write does to the panes the selector matches.
func selectWriteParams(what string) []verbParam {
	return []verbParam{
		{Name: "select", Type: "string", Description: selectorSyntax + " " + what + " It reaches agent panes on this machine, in every session, and takes no session and no window. Without confirm nothing is sent: the call fails with confirm_required, whose hint lists the panes in available and carries the token in confirm."},
		{Name: "confirm", Type: "string", Description: "The token for the set of panes the selector matches, from a confirm_required hint or from list-agents with the same selector. The write goes ahead only when the selector still matches exactly that set; otherwise it fails with confirm_required again and the new set."},
	}
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
		"restrict-connection": {
			description: "Give up authority on this connection for as long as it is open. scope own reaches only the caller's own session, its fan group and the sessions a fan from it started; read_only refuses every verb that types into a pane. The caller's pane is found from the kernel's record of its pid, and only when that finds none from pane_id and pane_token. A later call may narrow further and never widen. Every verb a restricted connection may not call answers forbidden.",
			params: []verbParam{
				{Name: "scope", Type: "string", Description: "own restricts every call to the caller's own session and fan group. all leaves the sessions alone, for read_only by itself.", Accepted: scopeNames, Default: ScopeOwn},
				{Name: "read_only", Type: "bool", Description: "Refuse send-text, send-keys, ask-agent, respond and fan. The caller may still report its own pane's state and meta and leave mail.", Default: "false"},
				{Name: "pane_id", Type: "string", Description: "The caller's pane, normally $TUIOS_PANE_ID. Used only when the kernel places the caller in no pane, and then only with the matching pane_token. When the kernel places the caller, a different pane_id is refused."},
				{Name: "pane_token", Type: "string", Description: "The pane's $TUIOS_PANE_TOKEN, which proves pane_id. It is good for one pane of one daemon start."},
			},
			returns: []verbParam{
				{Name: "scope", Type: "string", Description: "own or all, as the connection now stands.", Accepted: scopeNames},
				{Name: "read_only", Type: "bool", Description: "Whether the connection is now read-only."},
				{Name: "window", Type: "string", Description: "The caller's pane, empty when it runs in no pane of this daemon. Under scope own such a connection reaches no session."},
				{Name: "session", Type: "string", Description: "The session of the caller's pane."},
				{Name: "via", Type: "string", Description: "How the pane was found: pid from the kernel, token from pane_token, empty when it was not.", Accepted: []string{"pid", "token"}},
				{Name: "sessions", Type: "[]string", Description: "Under scope own, the sessions the connection reaches now. Sessions a fan starts later join it."},
			},
			examples: []string{
				`{"id":1,"verb":"restrict-connection","params":{"scope":"own","read_only":true}}`,
				`{"id":1,"verb":"restrict-connection","params":{"scope":"all","read_only":true}}`,
			},
			handler: (*Daemon).verbRestrictConnection,
		},
		"pane-grants": {
			description: "Say what the caller may do through tuios: the pane it runs in and the grants that pane holds (read, write, fan, respond, admin), or that it runs in no pane and no pane grants apply. The pane is found from the kernel's record of the caller's pid, and only when that finds none from pane_id and pane_token, which then place this connection in that pane for as long as it is open. Every JSON verb and client protocol message from a pane is held to its grants; a call they do not cover answers forbidden and names the missing grant.",
			params: []verbParam{
				{Name: "pane_id", Type: "string", Description: "The caller's pane, normally $TUIOS_PANE_ID. Used only when the kernel places the caller in no pane, and then only with the matching pane_token. When the kernel places the caller, a different pane_id is refused."},
				{Name: "pane_token", Type: "string", Description: "The pane's $TUIOS_PANE_TOKEN, which proves pane_id. It is good for one pane of one daemon start."},
				{Name: "peer_pid", Type: "int", Description: "Answer for the process with this pid instead of the caller, placed the way a caller is. The tmux shim's pane holder asks this for the process on its own socket before it replaces the pane's command. The answer echoes peer_pid. A pane without admin asking about a process in another pane is told only pane and admin. Not with pane_id or pane_token, and refused over a link."},
				{Name: "peer_start", Type: "int", Description: "With peer_pid: the process's start time as the asker read it when the process connected. A process with another start time now is not the one that connected, and is answered as one that cannot be read."},
			},
			returns: []verbParam{
				{Name: "pane", Type: "bool", Description: "Whether the caller runs in a pane of this daemon. When false, no pane grants apply to it: the person's own CLI and client keep full rights."},
				{Name: "restart_needed", Type: "bool", Description: "True when config.toml gives panes more than they hold now. A reload applies only what narrows [agents.permissions]; the rest applies after a daemon restart."},
				{Name: "window", Type: "string", Description: "The caller's pane."},
				{Name: "session", Type: "string", Description: "The session of the caller's pane."},
				{Name: "via", Type: "string", Description: "How the pane was found: pid from the kernel, env from the process's TUIOS_PANE_ID while the pane was being created, token from pane_token.", Accepted: []string{"pid", "env", "token"}},
				{Name: "grants", Type: "[]string", Description: "The grants the pane holds now. admin implies read, write and fan; respond is never implied.", Accepted: config.PaneGrantNames},
				{Name: "explicit", Type: "bool", Description: "True when the pane was given grants of its own, false when it holds the default."},
				{Name: "mode", Type: "string", Description: "[agents.permissions] mode: open gives a pane started with no grants admin, strict gives it default_grants.", Accepted: config.PaneModes},
				{Name: "default_grants", Type: "[]string", Description: "What a pane started with no grants of its own holds now."},
			},
			examples: []string{
				`{"id":1,"verb":"pane-grants"}`,
				`{"id":1,"verb":"pane-grants","params":{"pane_id":"<$TUIOS_PANE_ID>","pane_token":"<$TUIOS_PANE_TOKEN>"}}`,
			},
			handler: (*Daemon).verbPaneGrants,
		},
		"set-pane-grants": {
			description: "Give a pane grants, or with reset the default of [agents.permissions]. Grants are read (its own session and fan group), write (type into its own session), fan (write in its fan group and start agents), respond (answer prompts without the person's nonce) and admin (everything else, as before grants existed). From outside every pane anything may be given. A pane may change only its own grants unless it holds admin, and may never give more than it holds, so a pane cannot widen itself. Refused over a link.",
			params: []verbParam{
				{Name: "session", Type: "string", Description: "Session of the pane. Omit from a pane for the pane's own session, and from outside every pane for the most recently active one."},
				{Name: "window", Type: "string", Description: "The pane, by window id or name. Omit from a pane to mean the caller's own."},
				{Name: "grants", Type: "[]string", Description: "The grants to give, or none for no grants at all.", Accepted: grantAccepted},
				{Name: "reset", Type: "bool", Description: "Give the pane the default of [agents.permissions] instead of grants of its own.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string", Description: "The pane's session."},
				{Name: "window", Type: "string", Description: "The pane's window id."},
				{Name: "grants", Type: "[]string", Description: "What the pane holds now."},
				{Name: "explicit", Type: "bool", Description: "False after reset: the pane holds the default and follows it when the config changes."},
				{Name: "previous", Type: "[]string", Description: "What the pane held before."},
				{Name: "previous_explicit", Type: "bool", Description: "Whether what it held before was its own."},
			},
			examples: []string{
				`{"id":1,"verb":"set-pane-grants","params":{"session":"work","window":"a1b2c3d4","grants":["read","write"]}}`,
				`{"id":1,"verb":"set-pane-grants","params":{"grants":["read"]}}`,
				`{"id":1,"verb":"set-pane-grants","params":{"session":"work","window":"a1b2c3d4","reset":true}}`,
			},
			handler: (*Daemon).verbSetPaneGrants,
		},
		"apply-config": {
			description: "Apply config.toml now, including the changes that give panes or linked machines more: [agents.permissions], [hosts] and their link policies. A change to the file applies only what narrows those; the rest waits for this verb or a daemon restart. Only the person may call it: it is refused from inside a pane and over a link.",
			params: []verbParam{
				{Name: "host", Type: "string", Description: "Apply only this host's entry, or its removal, and nothing else in the file. tuios hosts add sends it."},
			},
			returns: []verbParam{
				{Name: "mode", Type: "string", Description: "[agents.permissions] mode now in force."},
				{Name: "default_grants", Type: "[]string", Description: "What a pane started with no grants of its own holds now."},
				{Name: "changes", Type: "[]string", Description: "What changed, one sentence each: the default grants, hosts added, removed or dialled another way, and link policies."},
				{Name: "still_waiting", Type: "bool", Description: "True when config.toml still holds a change that waits, which happens when only one host was applied."},
			},
			examples: []string{`{"id":1,"verb":"apply-config"}`, `{"id":1,"verb":"apply-config","params":{"host":"build"}}`},
			handler:  (*Daemon).verbApplyConfig,
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
		"list-keys": {
			description: "List the send-keys key grammar: every key name with its aliases, the modifiers and their spellings, and the other kinds of token. The list is closed: a key name or modifier missing from it is refused.",
			returns: []verbParam{
				{Name: "keys", Type: "[]object", Description: "Every named key, as {name, aliases}. name is the canonical spelling. aliases are the other spellings, lower case, since names are matched without case, hyphens or underscores."},
				{Name: "modifiers", Type: "[]object", Description: "Every modifier, as {name, spellings}. super reaches only an attached client's window manager, never a pane."},
				{Name: "ctrl_characters", Type: "[]string", Description: "The characters other than letters that ctrl combines with."},
				{Name: "escape_prefixes", Type: "[]string", Description: "The prefixes that make a token an escape sequence, sent as it is."},
				{Name: "prefix_token", Type: "string", Description: "The token for the leader key."},
				{Name: "separators", Type: "[]string", Description: "The characters keys are split on."},
				{Name: "max_repeat", Type: "int", Description: "The largest repeat send-keys takes."},
			},
			examples: []string{`{"id":1,"verb":"list-keys"}`},
			handler:  (*Daemon).verbListKeys,
		},
		"list-hooks": {
			description: "List the hook table and what each hook command last did: how many times it ran, its last exit code, when it last ran and its last error.",
			params: []verbParam{
				sessionParam,
				{Name: "event", Type: "string", Description: "Only the hooks on this event. Omit for every event."},
			},
			returns: []verbParam{
				{Name: "hooks", Type: "[]object", Description: "One row per registered command: event, side, command, runs, last_exit, last_run, last_error and last_ms. Side is session for the hooks the daemon runs and client for the ones an attached client runs."},
				{Name: "total", Type: "int", Description: "How many rows the filter matched."},
				{Name: "events", Type: "[]string", Description: "Every event a hook can be written on. An event outside this list is ignored when the config loads."},
				{Name: "client_attached", Type: "bool", Description: "Whether a client answered for its half of the table. False means the client rows are missing because nobody is attached, not that no client hooks exist."},
			},
			examples: []string{
				`{"id":1,"verb":"list-hooks"}`,
				`{"id":1,"verb":"list-hooks","params":{"event":"after-new-window"}}`,
			},
			handler: (*Daemon).verbListHooks,
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
		"pip": {
			description: "Pin a pane as the attached client's picture-in-picture view: a small live copy of the pane in a corner of the screen while another pane has the focus. Naming the pinned pane again unpins it. The view belongs to the client, not the session, and needs an attached client.",
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string", Description: "Window id or name to pin. Omit to pin the client's focused pane, or to unpin it when it is the pinned one."},
				{Name: "off", Type: "bool", Description: "Unpin whatever is pinned. Takes no window.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "pinned", Type: "bool", Description: "Whether a pane is pinned now."},
				{Name: "window_id", Type: "string", Description: "Id of the pinned pane, empty when nothing is pinned."},
			},
			examples: []string{
				`{"id":1,"verb":"pip","params":{"session":"work","window":"build"}}`,
				`{"id":1,"verb":"pip","params":{"off":true}}`,
			},
			handler: (*Daemon).verbPiP,
		},
		"list-sessions": {
			description: "List all sessions the daemon holds.",
			examples:    []string{`{"id":1,"verb":"list-sessions"}`},
			handler:     (*Daemon).verbListSessions,
		},
		"new-session": {
			description: "Create a session in the daemon, with its first window. The session runs detached until a client attaches to it.",
			params: []verbParam{
				{Name: "name", Type: "string", Description: "Name for the new session. Omit to have one generated. A name the daemon already holds is refused."},
				{Name: "width", Type: "int", Description: "Nominal width in columns. An attached client replaces it with its own viewport.", Default: "80"},
				{Name: "height", Type: "int", Description: "Nominal height in rows. An attached client replaces it with its own viewport.", Default: "24"},
				{Name: "window", Type: "bool", Description: "Create the first window. Pass false for an empty session you place every window in yourself.", Default: "true"},
				{Name: "window_name", Type: "string", Description: "Name for the first window. Omit to use the shell's title."},
				{Name: "cwd", Type: "string", Description: "Directory to start the first window's shell in. Omit to inherit the daemon's."},
				{Name: "command", Type: "[]string", Description: "Argv to exec as the first window's process instead of a shell. No shell parses it, so nothing needs quoting."},
			},
			returns: []verbParam{
				{Name: "session", Type: "string", Description: "Name of the new session. Use it as the session parameter of every later call."},
				{Name: "session_id", Type: "string", Description: "Id of the new session."},
				{Name: "width", Type: "int", Description: "Nominal width the session was created at."},
				{Name: "height", Type: "int", Description: "Nominal height the session was created at."},
				{Name: "windows", Type: "int", Description: "How many windows the session holds: 1, or 0 when window was false."},
				{Name: "window_id", Type: "string", Description: "Id of the first window. Absent when window was false."},
				{Name: "window_name", Type: "string", Description: "Name of the first window. Absent when window was false."},
				{Name: "pty_id", Type: "string", Description: "Id of the first window's PTY. Absent when window was false."},
			},
			examples: []string{
				`{"id":1,"verb":"new-session"}`,
				`{"id":1,"verb":"new-session","params":{"name":"work","window_name":"build","cwd":"/src/api"}}`,
				`{"id":1,"verb":"new-session","params":{"name":"empty","window":false}}`,
			},
			handler: (*Daemon).verbNewSession,
		},
		"paste-image": {
			description: "Write an image the person pasted to the machine where a pane's process runs, and answer with the path there. For a pane on another machine the image crosses the link and is written there. The client pastes the path into the pane as text. Only the person's attached client may call it: it needs that client's attach nonce and is refused from inside a pane. The file is readable by its owner only and is deleted after an hour, or when the daemon stops.",
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string", Description: "The window to paste into. Omit for the focused window."},
				{Name: "content", Type: "string", Required: true, Description: "The image, base64. PNG, JPEG, GIF, WebP, BMP or TIFF, at most 8 MB decoded. The type is read from the bytes."},
				{Name: "human_nonce", Type: "string", Required: true, Description: "The attach nonce of the person's client, attached to this session."},
			},
			returns: []verbParam{
				{Name: "path", Type: "string", Description: "The image's path on the machine where the pane's process runs."},
				{Name: "host", Type: "string", Description: "The machine the image was written to, when it is not this one."},
				{Name: "window", Type: "string", Description: "The window the path is for."},
				{Name: "bytes", Type: "int", Description: "How many bytes were written."},
			},
			examples: []string{`{"id":1,"verb":"paste-image","params":{"session":"work","window":"a1b2","content":"iVBORw0KGgo=","human_nonce":"<from the attach reply>"}}`},
			handler:  (*Daemon).verbPasteImage,
		},
		"read-dir": {
			description: "List a directory on this machine, as the rail's file section reads it. The machine with the process is the machine with the files, so a pane running here is listed here.",
			params: []verbParam{
				{Name: "dir", Type: "string", Description: "The directory to list."},
				{Name: "max", Type: "int", Description: "At most this many names. Omit for the built-in cap."},
			},
			returns: []verbParam{
				{Name: "dir", Type: "string", Description: "The directory listed."},
				{Name: "entries", Type: "[]string", Description: "One entry per name, carrying the name and whether it is a directory. Directories first, then names, case insensitively."},
				{Name: "capped", Type: "bool", Description: "The directory holds more names than were sent."},
				{Name: "err", Type: "string", Description: "Why there is no listing, in words a person can act on."},
			},
			examples: []string{`{"id":1,"verb":"read-dir","params":{"dir":"/home/ubuntu"}}`},
			handler:  (*Daemon).verbReadDir,
		},
		"wait-dir": {
			description: "Wait until the names in a directory on this machine change: a file added, removed or renamed. The daemon that owns a window on another machine asks it, so the rail's file section can follow that machine's disk. It answers changed false when the timeout ends first.",
			params: []verbParam{
				{Name: "dir", Type: "string", Required: true, Description: "The directory to watch."},
				{Name: "timeout", Type: "int", Description: "Milliseconds to wait. Omit for 30 seconds."},
			},
			returns: []verbParam{
				{Name: "dir", Type: "string", Description: "The directory watched."},
				{Name: "changed", Type: "bool", Description: "The names changed before the timeout ended."},
			},
			examples: []string{`{"id":1,"verb":"wait-dir","params":{"dir":"/tmp","timeout":1}}`},
			handler:  (*Daemon).verbWaitDir,
		},
		"session-info": {
			description: "Report details about one session.",
			params:      []verbParam{sessionParam},
			examples:    []string{`{"id":1,"verb":"session-info","params":{"session":"work"}}`},
			handler:     (*Daemon).verbSessionInfo,
		},
		"list-windows": {
			description: "List the windows in a session. Each window carries a host when its process runs on another machine, and omits it when the process is on this one. A window whose shell marks its commands with OSC 133 also carries at_prompt, command_seq, marks_commands (the shell has sent a command-start mark), prompt_marks_only when it ran a command without one, running_cmdline while a command runs, and last_cmdline, last_exit_code and last_duration_ms once one has finished. A pane given grants of its own carries them as grants; a pane on the default of [agents.permissions] omits it.",
			params:      []verbParam{sessionParam},
			examples:    []string{`{"id":1,"verb":"list-windows","params":{"session":"work"}}`},
			handler:     (*Daemon).verbListWindows,
		},
		"get-window": {
			description: "Describe one window, as the client protocol's GetWindow does: an attached client answers with its cursor and process fields, and with none attached the daemon answers with the window's list-windows entry. It is a read, which a pane holding read may call on its own session.",
			params:      []verbParam{sessionParam, windowParam},
			returns: []verbParam{
				{Name: "window_id", Type: "string", Description: "Id of the window."},
				{Name: "display_name", Type: "string", Description: "Its name as shown: the custom name, or the shell's title."},
				{Name: "agent_state", Type: "string", Description: "Its agent state."},
			},
			examples: []string{`{"id":1,"verb":"get-window","params":{"session":"work"}}`},
			handler:  (*Daemon).verbGetWindow,
		},
		"new-window": {
			description: "Create a new window, optionally on a named workspace and in a named directory.",
			params: []verbParam{
				sessionParam,
				{Name: "name", Type: "string", Description: "Name for the new window. Omit to use the shell's title."},
				{Name: "workspace", Type: "int", Description: "Workspace number to create the window on. Omit for the current workspace."},
				{Name: "cwd", Type: "string", Description: "Directory to start the shell in. Omit to inherit the daemon's."},
				{Name: "focus", Type: "bool", Description: "Focus the new window. Pass false to leave the focus where it is.", Default: "true"},
				{Name: "command", Type: "[]string", Description: "Argv to exec as the window's process instead of a shell. No shell parses it, so nothing needs quoting. The window closes when the program exits."},
				{Name: "host", Type: "string", Description: "Run the window's process on another machine, named as it is in the [hosts] config table. The window belongs to this session and is drawn and sized here; only the process is there. Omit, or pass \"local\", for this machine."},
				grantsParam,
				{Name: "close_on_exit", Type: "bool", Description: "Close the window when its process exits, also with no client attached. Without it, a detached session keeps a window whose process exited until something closes it.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string", Description: "Id of the new window. Use it to address the window in later calls."},
				{Name: "host", Type: "string", Description: "The machine the window's process runs on. Omitted for a window on this machine."},
				{Name: "name", Type: "string", Description: "The window's name, generated when none was given."},
				{Name: "workspace", Type: "int", Description: "Workspace the window was created on."},
				{Name: "pty_id", Type: "string", Description: "Id of the window's PTY."},
				{Name: "focused", Type: "bool", Description: "Whether the window took the focus."},
				{Name: "unplaced", Type: "bool", Description: "True while the window's geometry is a placeholder. An attached client replaces it. On a detached session it stays true and the reported size is nominal."},
			},
			examples: []string{
				`{"id":1,"verb":"new-window","params":{"session":"work","name":"build"}}`,
				`{"id":1,"verb":"new-window","params":{"session":"work","name":"tests","workspace":2,"cwd":"/src/api","focus":false}}`,
				`{"id":1,"verb":"new-window","params":{"session":"work","name":"htop","command":["/usr/bin/htop"]}}`,
			},
			handler: (*Daemon).verbNewWindow,
		},
		"popup": {
			description: "Open a popup: a floating pane that runs one command and closes when the command exits. Needs an attached client.",
			params: []verbParam{
				sessionParam,
				{Name: "command", Type: "[]string", Required: true, Description: "Argv to run in the popup. No shell parses it, so nothing needs quoting. The popup closes when the program exits."},
				{Name: "width", Type: "string", Description: "Popup width, in cells (\"60\") or as a share of the pane region (\"60%\").", Default: PopupDefaultWidth},
				{Name: "height", Type: "string", Description: "Popup height, in cells (\"20\") or as a share of the pane region (\"50%\").", Default: PopupDefaultHeight},
				{Name: "name", Type: "string", Description: "Name for the popup. Omit to use the program's title."},
				{Name: "cwd", Type: "string", Description: "Directory to run the command in. Omit to inherit the daemon's."},
				{Name: "workspace", Type: "int", Description: "Workspace to open the popup on. Omit for the current one."},
				{Name: "wait", Type: "bool", Description: "Keep the call open until the command exits, and return its exit_code in a popup_result.", Default: "false"},
				{Name: "capture_stdout", Type: "bool", Description: "With wait: send the command's standard output to a pipe the daemon reads instead of the popup, and return it as stdout. A picker such as fzf draws on the terminal and prints only the choice, so the choice comes back. Not on Windows.", Default: "false"},
				{Name: "timeout", Type: "int", Description: "With wait: milliseconds to wait before failing with the timeout code. The popup stays open. 0 waits as long as it is open.", Default: "0"},
				{Name: "scratch_name", Type: "string", Description: "With scratch: the scratch pane's name. A session has one scratch pane per name. Empty is the built-in scratch terminal.", Default: ""},
				{Name: "scratch", Type: "bool", Description: "Open the session's scratch terminal, the popup the toggle_scratch key shows and hides. command is optional and defaults to the shell. A session has one scratch terminal.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "exit_code", Type: "int", Description: "With wait: the command's exit status, -1 when a signal ended it, as closing the popup does."},
				{Name: "stdout", Type: "string", Description: "With capture_stdout: what the command printed to standard output, at most 1 MiB."},
				{Name: "stdout_truncated", Type: "bool", Description: "With capture_stdout: true when the output was cut to 1 MiB."},
				{Name: "window_id", Type: "string", Description: "Id of the popup. Use it to address the popup in later calls."},
				{Name: "name", Type: "string", Description: "The popup's name, generated when none was given."},
				{Name: "workspace", Type: "int", Description: "Workspace the popup was opened on."},
				{Name: "pty_id", Type: "string", Description: "Id of the popup's PTY."},
				{Name: "width", Type: "string", Description: "The width the popup uses, with the default filled in."},
				{Name: "height", Type: "string", Description: "The height the popup uses, with the default filled in."},
			},
			examples: []string{
				`{"id":1,"verb":"popup","params":{"session":"work","command":["fzf"]}}`,
				`{"id":1,"verb":"popup","params":{"session":"work","command":["htop"],"width":"90%","height":"80%"}}`,
				`{"id":1,"verb":"popup","params":{"session":"work","command":["fzf"],"wait":true,"capture_stdout":true}}`,
			},
			handler: (*Daemon).verbPopup,
		},
		"split-window": {
			description: "Split a pane and put a new one beside it. Needs an attached client and tiling on.",
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
			description: "Move the focus to a pane. Pass exactly one of window, relative or direction.",
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string", Description: "Window id or name to focus. Switches to that window's workspace."},
				{Name: "relative", Type: "string", Description: "Focus the next or previous window on the current workspace.", Accepted: focusRelatives},
				{Name: "direction", Type: "string", Description: "Focus the neighbouring pane in this direction. Needs an attached client.", Accepted: focusDirections},
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
				{Name: "current_workspace", Type: "int", Description: "Workspace showing after the call. It changes only when follow is true."},
			},
			examples: []string{`{"id":1,"verb":"move-window","params":{"session":"work","window":"build","workspace":2,"follow":true}}`},
			handler:  (*Daemon).verbMoveWindow,
		},
		"set-window": {
			description: "Change a window's name or minimized state. Pass only the fields to change.",
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string", Description: "Window to change. Omit for the focused one."},
				{Name: "name", Type: "string", Description: "New name. Pass an empty string to clear it and fall back to the shell's title."},
				{Name: "minimized", Type: "bool", Description: "Minimize the window, or restore it."},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string", Description: "Id of the window that changed."},
				{Name: "display_name", Type: "string", Description: "The name shown now. After a clear this is the shell's title."},
				{Name: "minimized", Type: "bool", Description: "Whether it is minimized now."},
			},
			examples: []string{`{"id":1,"verb":"set-window","params":{"session":"work","window":"build","name":"api tests","minimized":false}}`},
			handler:  (*Daemon).verbSetWindow,
		},
		"select-workspace": {
			description: "Show a workspace. To rename or reorder workspaces, use set-workspace-name and set-workspace-order.",
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
			description: "Turn tiling on or off, tidy the splits, and shape the master-stack layout. Needs an attached client.",
			params: []verbParam{
				sessionParam,
				{Name: "tiling", Type: "bool", Description: "Tile the panes automatically, or let them float."},
				{Name: "equalize", Type: "bool", Description: "Reset every split ratio so the panes share the space evenly.", Default: "false"},
				{Name: "rotate", Type: "bool", Description: "Flip the axis of the split holding the focused pane.", Default: "false"},
				{Name: "master_position", Type: "string", Description: "Side the master panes take on the current workspace: left, right, top, bottom or center."},
				{Name: "master_count", Type: "int", Description: "How many panes are master panes on the current workspace, 1 to 9."},
			},
			returns: []verbParam{
				{Name: "tiling_mode", Type: "string", Description: `"tiling" or "floating".`},
				{Name: "layout_mode", Type: "string", Description: `Which tiling layout is in effect: bsp, master-stack, scrolling, or "unknown" on a session no client has reported one for.`},
				{Name: "master_ratio", Type: "float", Description: "Fraction of the screen the master pane takes."},
				{Name: "master_position", Type: "string", Description: `Side the master panes take on the current workspace, or "default" when the workspace uses the client's configured side.`},
				{Name: "master_count", Type: "int", Description: "How many panes are master panes on the current workspace, or 0 when the workspace uses the client's configured count."},
			},
			examples: []string{
				`{"id":1,"verb":"set-layout","params":{"session":"work","tiling":true,"equalize":true}}`,
				`{"id":2,"verb":"set-layout","params":{"session":"work","master_position":"center","master_count":1}}`,
			},
			handler: (*Daemon).verbSetLayout,
		},
		"run-command": {
			description: "Run one tape command (the command names the keybindings use). Prefer a verb where one exists: a verb reports what changed, this reports only that the command ran.",
			params: []verbParam{
				sessionParam,
				{Name: "command", Type: "string", Required: true, Description: `Tape command name, e.g. "ToggleZoom" or "SnapLeft". The keymap's name for the same action, e.g. "toggle_zoom", is accepted too.`},
				{Name: "args", Type: "[]string", Description: "Arguments for the command."},
			},
			returns: []verbParam{
				{Name: "command", Type: "string", Description: "The command that ran."},
				{Name: "routed", Type: "bool", Description: "True when an attached client ran it, false when the daemon did."},
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
		"close-workspace": {
			description: "Close every pane on a workspace, like tmux kill-window. Scratch panes stay unless the workspace named is their scratch workspace. A pane that calls it needs the admin grant.",
			params: []verbParam{
				sessionParam,
				{Name: "workspace", Type: "int", Description: "Workspace whose panes to close. Omit for the current one."},
			},
			returns: []verbParam{
				{Name: "workspace", Type: "int", Description: "The workspace whose panes were closed."},
				{Name: "closed", Type: "[]string", Description: "Ids of the windows that were closed, in window order."},
			},
			examples: []string{`{"id":1,"verb":"close-workspace","params":{"session":"work","workspace":2}}`},
			handler:  (*Daemon).verbCloseWorkspace,
		},
		"send-keys": {
			description: "Send keys to a window's program: Up, PageDown, ctrl+c. With a window they go to that window's terminal; without one, to an attached client (the window manager) or else the focused window.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "keys", Type: "string", Required: true, Description: `Keys split on spaces and commas, e.g. "Down Down PageDown" or "ctrl+c". A key is a name (Enter Tab BTab Space Comma Escape Backspace Up Down Right Left Home End PageUp PageDown Insert Delete F1-F12, case-insensitive, also as arrow-up, KEY_UP, <Up>, PgDn), one character, a name or character after ctrl+, alt+ or shift+ (or tmux C-, M-, S-), an escape sequence written \e[A, or PREFIX for the leader key. list-keys returns the full list.`},
				{Name: "literal", Type: "bool", Description: "Send the keys to the PTY without parsing them as key names.", Default: "false"},
				{Name: "raw", Type: "bool", Description: "Treat every character as its own key instead of splitting on spaces and commas.", Default: "false"},
				{Name: "repeat", Type: "int", Description: "Send the whole sequence this many times, 1 to 1000.", Default: "1"},
			},
			returns: []verbParam{
				{Name: "sent_to", Type: "string", Description: "window when the keys were written to a window's terminal, client when an attached client took them as the person's keys."},
				{Name: "window_id", Type: "string", Description: "The window the keys were written to, when sent_to is window."},
				{Name: "window", Type: "string", Description: "That window's name, or its title when it has no name."},
				{Name: "keys", Type: "int", Description: "How many keys were sent, counting repeats."},
			},
			examples: []string{
				`{"id":1,"verb":"send-keys","params":{"session":"work","window":"review","keys":"Down","repeat":5}}`,
				`{"id":1,"verb":"send-keys","params":{"session":"work","window":"build","keys":"ctrl+c"}}`,
			},
			handler: (*Daemon).verbSendKeys,
		},
		"send-text": {
			description: "Send literal text to a window's PTY.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "text", Type: "string", Required: true, Description: "Text written verbatim to the PTY."},
				{Name: "paste", Type: "bool", Description: "Send the text as a paste. Control characters other than tab, line feed and carriage return are removed. The text is wrapped in the bracketed paste delimiters when the program in the pane turned bracketed paste on.", Default: "false"},
				{Name: "submit", Type: "bool", Description: "Paste the text and submit it with the Enter key of the harness in the pane, after a short wait for the paste to be taken in, as ask-agent does.", Default: "false"},
			},
			examples: []string{
				`{"id":1,"verb":"send-text","params":{"session":"work","text":"echo hi\n"}}`,
				`{"id":1,"verb":"send-text","params":{"session":"work","window":"build","text":"line one\nline two","paste":true}}`,
				`{"id":1,"verb":"send-text","params":{"session":"work","text":"Fix the test\nthen commit","submit":true}}`,
			},
			handler: (*Daemon).verbSendText,
		},
		"capture-pane": {
			description: "Capture a pane's content.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "source", Type: "string", Description: "Which buffer to capture. last-command-output is what the pane's last finished command printed, read between its shell's OSC 133 marks; it is plain text, adds cmdline, exit_code, command_seq and truncated to the result, and fails with no_shell_integration when no command has finished under the marks.", Accepted: captureSources, Default: "visible"},
				{Name: "styled", Type: "bool", Description: "Include ANSI styling in the captured text.", Default: "false"},
				// scrollback and ansi predate source and styled and are still
				// accepted; they are declared so a caller reading only list-verbs
				// can see the whole call shape.
				{Name: "scrollback", Type: "bool", Description: `Older spelling of source "recent".`, Default: "false"},
				{Name: "ansi", Type: "bool", Description: "Older spelling of styled.", Default: "false"},
				{Name: "resolved", Type: "bool", Description: "Rewrite ANSI index colours (30-37, 90-97, 40-47, 100-107, 38;5;n<16, 48;5;n<16) to 24-bit RGB so the capture matches what a themed client paints. Indices above 15 and true colour pass through untouched.", Default: "false"},
				{Name: "palette", Type: "[]string", Description: "The 16 hex colours (#rrggbb) the client's theme paints indices 0-15 with, used by resolved captures. Must be exactly 16 entries when present; absent means the xterm defaults.", Default: "xterm defaults"},
				{Name: "lines", Type: "int", Description: "Keep only the last N lines. Blank rows below the cursor do not count. Ignored when start or end is given."},
				{Name: "start", Type: "int", Description: "1-based inclusive first line of the region to keep."},
				{Name: "end", Type: "int", Description: "1-based inclusive last line of the region to keep."},
			},
			returns: []verbParam{
				{Name: "content", Type: "string", Description: "The captured text, one line per row."},
				{Name: "source", Type: "string", Description: "The buffer captured.", Accepted: captureSources},
				{Name: "styled", Type: "bool", Description: "Whether content carries ANSI styling."},
				{Name: "resolved", Type: "bool", Description: "Whether index colours were rewritten to 24-bit RGB."},
				{Name: "history_rows", Type: "int", Description: "How many scrollback lines the pane holds above its screen. A recent capture of more lines than this plus the screen has nothing older to show. Not on a last-command-output capture."},
				{Name: "revision", Type: "int", Description: "A number that grows each time the pane's content can change: output reaches it, or it is resized. Two captures of one source with one revision have the same content. It counts from 0 again when the daemon restarts. Not on a last-command-output capture."},
				{Name: "boot_id", Type: "string", Description: "The daemon start the revision belongs to, the boot_id of subscribe and list-attention. Compare revisions only when boot_id is the same."},
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
			description: "List every settable configuration path with its type, default, accepted values and description. Use it to find an option path instead of guessing one.",
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
			description: "Set a configuration option. An attached client applies it live. The path and value are checked against the option registry, so a bad call fails instead of reporting success.",
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
			description: "List the registered themes. Name one to also get its colours as hex and the contrast of each against its own background.",
			params: []verbParam{
				sessionParam,
				{Name: "theme", Type: "string", Description: "Describe this theme as well as listing. Omit to list only."},
				{Name: "filter", Type: "string", Description: "Only ids containing this, case-insensitively, e.g. catppuccin."},
			},
			returns: []verbParam{
				{Name: "themes", Type: "[]string", Description: "Matching theme ids, capped at 100. truncated reports when the cap applied."},
				{Name: "total", Type: "int", Description: "How many themes are registered in all."},
				{Name: "matched", Type: "int", Description: "How many the filter matched, before the cap."},
				{Name: "active", Type: "string", Description: "The theme this session is set to. Empty means no theme, which is the terminal's own colours."},
				{Name: "active_source", Type: "string", Description: `"session" for a theme set on this session, "default" for the built-in.`, Accepted: []string{"session", "default"}},
				{Name: "themes_dir", Type: "string", Description: "Where a custom theme file goes. Writing <id>.json here registers it. No restart is needed."},
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
			description: "Read an option. Reports the session's override when one is set, otherwise the default.",
			params: []verbParam{
				sessionParam,
				{Name: "key", Type: "string", Required: true, Description: "Option path to read."},
			},
			returns: []verbParam{
				{Name: "key", Type: "string", Description: "The option that was read."},
				{Name: "value", Type: "string", Description: "The value in effect."},
				{Name: "source", Type: "string", Description: `Where the value came from: "session" for an override set on this session, "config" for the daemon's config file (daemon.window_size only), "default" for the built-in.`, Accepted: []string{"session", "config", "default"}},
				{Name: "default", Type: "string", Description: "The built-in default, so a caller can tell an override from a default that happens to match."},
				{Name: "option_type", Type: "string", Description: "bool, int or string."},
			},
			examples: []string{`{"id":1,"verb":"get-option","params":{"session":"work","key":"appearance.dockbar_position"}}`},
			handler:  (*Daemon).verbGetOption,
		},
		"rename-session": {
			description: "Rename a session. The new name is what ls lists, what attach and every verb take, and what new panes get as TUIOS_SESSION. The old name still reaches the session for panes that already run. The session's display label is cleared.",
			params: []verbParam{
				sessionParam,
				{Name: "name", Type: "string", Required: true, Description: "The new name. It must be unique, must not be empty, and must not hold a slash, a backslash or a control character."},
			},
			returns: []verbParam{
				{Name: "type", Type: "string", Description: "Always session_renamed."},
				{Name: "session", Type: "string", Description: "The session's new name."},
				{Name: "old_name", Type: "string", Description: "The name it had before. It still reaches the session for panes that already run."},
			},
			examples: []string{`{"id":1,"verb":"rename-session","params":{"session":"work","name":"payments"}}`},
			handler:  (*Daemon).verbRenameSession,
		},
		"set-session-name": {
			description: "Set a session's display label. The session keeps its name for addressing, persistence and TUIOS_SESSION. To change the name, use rename-session.",
			params: []verbParam{
				sessionParam,
				{Name: "name", Type: "string", Description: "Display label for the session. Omit or pass an empty string to clear it and fall back to the session name."},
			},
			examples: []string{`{"id":1,"verb":"set-session-name","params":{"session":"work","name":"Payments API"}}`},
			handler:  (*Daemon).verbSetSessionName,
		},
		"set-session-accent": {
			description: "Set a session's accent colour. Every attached client shares it, and it survives a reattach.",
			params: []verbParam{
				sessionParam,
				{Name: "accent", Type: "string", Description: "An ANSI colour name (\"cyan\", \"bright blue\") or a #rrggbb value, recorded verbatim. Omit or pass an empty string to clear it and let the client pick the session's colour."},
			},
			examples: []string{`{"id":1,"verb":"set-session-accent","params":{"session":"work","accent":"cyan"}}`},
			handler:  (*Daemon).verbSetSessionAccent,
		},
		"set-workspace-name": {
			description: "Name a workspace. The workspace keeps its number for addressing. An unnamed workspace shows its number.",
			params: []verbParam{
				sessionParam,
				{Name: "workspace", Type: "int", Required: true, Description: "Workspace number to name."},
				{Name: "name", Type: "string", Description: "Label for the workspace. Omit or pass an empty string to clear it and fall back to the number."},
			},
			examples: []string{`{"id":1,"verb":"set-workspace-name","params":{"session":"work","workspace":2,"name":"review"}}`},
			handler:  (*Daemon).verbSetWorkspaceName,
		},
		"set-workspace-order": {
			description: "Set the order the workspaces are shown in. Only the display order changes: verbs, keys and windows still address each workspace by its number.",
			params: []verbParam{
				sessionParam,
				{Name: "order", Type: "[]int", Required: true, Description: "Workspace numbers in the order to show them. Numbers outside the session's range and repeats are dropped. A workspace the list omits keeps its place after the ones named. An ascending order clears the arrangement."},
			},
			examples: []string{`{"id":1,"verb":"set-workspace-order","params":{"session":"work","order":[3,1,2]}}`},
			handler:  (*Daemon).verbSetWorkspaceOrder,
		},
		"wait-for": {
			description: "Block until a condition matches, or fail with the timeout code.",
			params: []verbParam{
				{Name: "condition", Type: "string", Required: true, Description: "Condition to wait for.", Accepted: waitConditions},
				{Name: "session", Type: "string", Description: "Session name. session-exists requires it: it is the session to wait for. For the other conditions, omit to target the most recently active session."},
				windowParam,
				{Name: "any_session", Type: "bool", Description: "For agent-state only: watch every session on the daemon, including ones created during the wait. Takes no session or window. The result names the session that matched.", Default: "false"},
				{Name: "pattern", Type: "string", Description: "Regular expression, required by window-output."},
				{Name: "source", Type: "string", Description: "Which buffer window-output matches against. The default includes scrollback, so output that has already scrolled past still matches.", Accepted: waitOutputSources, Default: "recent"},
				{Name: "idle", Type: "int", Description: "Milliseconds of silence that count as idle, for window-idle.", Default: "500"},
				{Name: "until", Type: "string", Description: "Agent state(s) to wait for, comma-separated, required by agent-state. With no window, any window in the session reaching one of them matches.", Accepted: AgentStateNames},
				{Name: "thread", Type: "int", Description: "Narrow agent-message to one thread. Pass any message id in the thread. A thread the ring holds nothing from never matches."},
				{Name: "command_seq", Type: "int", Description: "For command-finished with a window: match once the pane has finished more commands than this, which is already true when the command finished before the wait. Read it from list-windows or a run timeout. Without it, the next command to finish after the wait starts matches."},
				{Name: "timeout", Type: "int", Description: "Milliseconds to wait before failing with the timeout code, at most 86400000 (24 hours). The wait also ends when the caller closes the connection.", Default: "30000"},
				{Name: "select", Type: "string", Description: selectorSyntax + " For agent-state only: watch the agent panes it matches, in every session, including panes that open during the wait. Takes no session, window or any_session. Put the state to wait for in until, not in the selector."},
				{Name: "every", Type: "bool", Description: "With select: match only when at least one pane matches and every matched pane is in one of the until states, and answer with all of them in panes. Without it, the first matched pane to reach one matches.", Default: "false"},
			},
			examples: []string{
				`{"id":1,"verb":"wait-for","params":{"condition":"window-output","session":"work","pattern":"done","timeout":10000}}`,
				`{"id":1,"verb":"wait-for","params":{"condition":"agent-state","session":"work","until":"needs_input,idle"}}`,
				`{"id":1,"verb":"wait-for","params":{"condition":"agent-message","session":"work","window":"$TUIOS_PANE_ID"}}`,
				`{"id":1,"verb":"wait-for","params":{"condition":"agent-message","session":"work","window":"$TUIOS_PANE_ID","thread":12}}`,
				`{"id":1,"verb":"wait-for","params":{"condition":"agent-state","any_session":true,"until":"needs_input"}}`,
				`{"id":1,"verb":"wait-for","params":{"condition":"command-finished","session":"work","window":"build","command_seq":4,"timeout":600000}}`,
				`{"id":1,"verb":"wait-for","params":{"condition":"agent-state","select":"group:fan/add-retry","until":"idle,done","every":true}}`,
			},
			handler: (*Daemon).verbWaitFor,
		},
	}
	// The verbs tuios-slim leaves out register from files tagged !slim. See
	// verb_registry_full.go.
	addFeatureVerbs(verbRegistry)
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
		if cs.takeover != nil {
			// The verb's reply is on the wire, and from here the connection
			// is not a verb connection. See connState.takeover. It is cleared
			// before it runs, because link-peer's takeover serves the same
			// connection again, and a stale one would run after every verb.
			take := cs.takeover
			cs.takeover = nil
			take(br)
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
		return d.writeVerbError(cs, nil, "", newVerbError(ErrVerbInvalidRequest, "malformed JSON request: "+err.Error()))
	}

	if req.Verb == "" {
		return d.writeVerbError(cs, req.ID, "",
			hintedVerbError(ErrVerbInvalidRequest, "request is missing the \"verb\" field", &VerbHint{
				Param:     "verb",
				Verb:      "list-verbs",
				Available: knownVerbNames(),
				Detail:    `Every request line is an object of the form {"id":1,"verb":"list-verbs","params":{}}.`,
			}))
	}

	entry, params, verr := d.admitVerb(cs, req.Verb, req.Params)
	if verr != nil {
		return d.writeVerbError(cs, req.ID, req.Verb, verr)
	}
	req.Params = params

	// A report from a pane this machine runs for another machine goes to the
	// machine that owns the pane's window. See hosted_calls.go.
	if result, verr, handled := d.forwardHostedCall(cs, req.Verb, req.Params); handled {
		if verr != nil {
			return d.writeVerbError(cs, req.ID, req.Verb, verr)
		}
		return d.writeVerbResponse(cs, &verbResponse{ID: req.ID, Result: result})
	}

	result, verr := entry.handler(d, cs, req.Params)
	replyFailed := cs.replyFailed
	cs.replyFailed = nil
	if verr != nil {
		return d.writeVerbError(cs, req.ID, req.Verb, verr)
	}
	if err := d.writeVerbResponse(cs, &verbResponse{ID: req.ID, Result: result}); err != nil {
		if replyFailed != nil {
			replyFailed()
		}
		return err
	}
	// A subscribe verb stashes its fresh subscription for the streamer, which must
	// start only after the ack line above is on the wire so no event precedes it.
	d.startPendingStream(cs)
	return nil
}

// admitVerb runs every check a verb call passes before its handler: the verb
// exists, its parameters are declared, a call over a link is held to that
// link's policy, and a call from a pane is held to the pane's grants and a
// restricted connection to its scope. It returns the verb's entry and the
// params the handler should see, which the checks may have filled in.
//
// Every caller of a verb goes through here: the verb socket, and the herdr
// socket's adapters (herdr_api.go), so a herdr client in a pane is held to
// exactly what the same call as a verb would be.
func (d *Daemon) admitVerb(cs *connState, verb string, params json.RawMessage) (verbEntry, json.RawMessage, *verbError) {
	entry, ok := verbRegistry[verb]
	if !ok {
		if verr := missingVerbError(verb); verr != nil {
			return verbEntry{}, nil, verr
		}
		known := knownVerbNames()
		return verbEntry{}, nil, hintedVerbError(ErrVerbUnknownVerb, "unknown verb "+echoName(verb), &VerbHint{
			Verb:       "list-verbs",
			Command:    "tuios list-verbs",
			DidYouMean: closestMatch(verb, known),
			Available:  known,
			Detail:     "Call list-verbs for every verb with its parameter schema and examples.",
		})
	}

	// A call for a feature of the verb that this build leaves out is refused
	// before its parameters are checked: the parameters it carries are the
	// left-out feature's, and "no parameter until" would hide why.
	if verr := missingCallError(verb, params); verr != nil {
		return verbEntry{}, nil, verr
	}
	if verr := checkParamNames(verb, entry, params); verr != nil {
		return verbEntry{}, nil, verr
	}

	// A call from another machine is held to that machine's link policy
	// before its handler runs. See link_policy.go.
	if verr := d.checkLinkVerb(cs, verb); verr != nil {
		return verbEntry{}, nil, verr
	}
	if verb != linkPolicyVerb {
		markLinkServed(cs)
	}

	// A call from a pane is held to what the pane holds, and a connection
	// that restricted itself is held to that too, before anything,
	// forwarding included, sees the call. See pane_grants.go and
	// conn_scope.go.
	// A session named by a name it was renamed from is named by its current
	// one from here on. See session_rename.go.
	params = d.followRenamedSession(verb, params)
	granted, verr := d.checkGrants(cs, verb, params)
	if verr != nil {
		return verbEntry{}, nil, verr
	}
	scoped, verr := d.checkScope(cs, verb, granted)
	if verr != nil {
		return verbEntry{}, nil, verr
	}
	return entry, scoped, nil
}

// callVerb runs one verb for cs the way the verb socket does, checks and
// all, and returns its result. It is how another protocol served by the
// daemon (the herdr socket) reuses a verb rather than doing its work again.
// A verb that streams (subscribe) is not called this way.
func (d *Daemon) callVerb(cs *connState, verb string, params any) (any, *verbError) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode params")
	}
	entry, admitted, verr := d.admitVerb(cs, verb, raw)
	if verr != nil {
		return nil, verr
	}
	if result, verr, handled := d.forwardHostedCall(cs, verb, admitted); handled {
		return result, verr
	}
	result, verr := entry.handler(d, cs, admitted)
	cs.replyFailed = nil
	return result, verr
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
				Detail:     "An unknown parameter is refused rather than silently ignored. Fix the name and retry.",
			})
	}
	return nil
}

// writeVerbError records the refusal and writes the error envelope. Every verb
// failure leaves the daemon through here, so the log line and the response
// cannot drift apart.
//
// The caller already learns why its call failed, from the code and the hint. The
// gap this closes is on the other side: the daemon kept no memory of what it
// refused, so a harness author debugging a wrapper could see their own traffic
// but not the daemon's reading of it. One line per refusal in `tuios logs -f`
// is that reading.
//
// The line carries the verb name, the client id and the refusal code, and not
// the message. A message quotes what the caller sent, which for a path or a
// title is content, and the level boundary keeps content out of basic.
func (d *Daemon) writeVerbError(cs *connState, id json.RawMessage, verb string, verr *verbError) error {
	name := verb
	if name == "" {
		name = "<none>"
	}
	client := "<unknown>"
	if cs != nil {
		client = cs.clientID
	}
	LogBasic("Verb %s refused for client %s: %s", name, client, verr.Code)

	return d.writeVerbResponse(cs, &verbResponse{ID: id, Error: verr})
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
// verbListKeys reports the send-keys key grammar. It reads no session.
func (d *Daemon) verbListKeys(_ *connState, _ json.RawMessage) (any, *verbError) {
	return keyList(), nil
}

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
			if verr := missingVerbError(p.Verb); verr != nil {
				return nil, verr
			}
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
	"transport": "One JSON object per line on the daemon socket. One response line per request line.",
	"request":   `{"id":<any>,"verb":"<name>","params":{...}}`,
	"success":   `{"id":<echoed>,"result":{"type":"<result type>",...}}`,
	"failure":   `{"id":<echoed>,"error":{"code":"<stable code>","message":"...","hint":{...}}}`,
	"hint":      "Present on most failures. Names the verb or CLI command that fixes it, the bad parameter and its accepted values, the closest matching name, and the values that do exist.",
}

// VerbDoc is one verb as list-verbs describes it.
type VerbDoc = verbDoc

// VerbParamDoc is one parameter or result field of a VerbDoc.
type VerbParamDoc = verbParam

// VerbDocs returns every verb as list-verbs describes it, sorted by name. It
// is read from the table the daemon dispatches from, so a program in this
// binary that builds a schema from it (tuios mcp) describes the same verbs,
// with the same parameters, as the daemon of the same build serves.
func VerbDocs() []VerbDoc {
	names := knownVerbNames()
	out := make([]VerbDoc, 0, len(names))
	for _, name := range names {
		out = append(out, describeVerb(name, verbRegistry[name]))
	}
	return out
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
		"instance":       d.instance,
		"sessions":       len(d.manager.ListSessions()),
		// link_policy says this daemon holds links to a policy, so a proxy
		// must reach it on a link socket and never on this one. See
		// DialForLink in link_dial.go.
		"link_policy": true,
		// pane_grants says this daemon holds calls from panes to their
		// grants and takes a pane's token with pane-grants, which the CLI
		// presents where the kernel cannot name the caller's pane.
		"pane_grants": true,
	}, nil
}
