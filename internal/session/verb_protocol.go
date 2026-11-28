package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/agentproto"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
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
	ErrVerbFanExists       = "fan_exists"        // fan was given the name of a fan that exists, with no join
	ErrVerbBranchExists    = "branch_exists"     // new-worktree with new_branch named a branch, folder or session that exists
	ErrVerbWindowNotFound  = "window_not_found"  // window target did not resolve
	ErrVerbNoWindows       = "no_windows"        // session has no windows to act on
	ErrVerbPTYNotFound     = "pty_not_found"     // the target window has no live PTY
	ErrVerbLeaseExpired    = "lease_expired"     // a stream-pane client did not renew its lease (an E frame)
	ErrVerbNeedsClient     = "needs_client"      // verb needs a live renderer that is not attached
	ErrVerbOptionNotFound  = "option_not_found"  // get-option key was never set
	ErrVerbCommandFailed   = "command_failed"    // a verb routed to the attached client came back failed
	ErrVerbTimeout         = "timeout"           // a wait-for condition did not match before its timeout
	ErrVerbInternal        = "internal"          // unexpected server-side failure

	// ErrVerbUpgradeRefused reports a daemon-upgrade that did not start:
	// the binary failed the preflight, the daemon holds something that
	// cannot be handed over, or this platform cannot hand off. Nothing was
	// changed. The message says which.
	ErrVerbUpgradeRefused = "upgrade_refused"
	// ErrVerbDaemonUpgrading ends a call the daemon was still working on, or
	// waiting in, when a live upgrade replaced its image. The connection
	// closes after it. A call that reads or waits can be made again, and the
	// new image answers it. A call that changes something may have partly
	// run, so its caller checks what it did before making it again.
	ErrVerbDaemonUpgrading = "daemon_upgrading"

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
	// ErrVerbNoTranscript reports an agent-transcript call for a pane that is
	// not joined to a transcript, or whose transcript is gone.
	ErrVerbNoTranscript = "no_transcript"
	// ErrVerbUnsupportedHarness reports an agent-transcript call for a pane
	// whose harness keeps no transcript this daemon can read.
	ErrVerbUnsupportedHarness = "unsupported_harness"

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
	// Problems lists what is wrong with a TML document, on invalid_tml.
	Problems any `json:"problems,omitempty"`
}

func (e *verbError) Error() string { return e.Code + ": " + e.Message }

// newVerbError builds a *verbError with the given code and message.
//
//go:noinline
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

// verbEntry pairs a handler with the schema list-verbs reports: the parameters
// and the result shape. The verb's description, the description of each
// parameter and result field, and its copy-pasteable examples are text in the
// .verbs.json file beside the Go file that registers the verb. See
// verb_docs.go.
type verbEntry struct {
	params []verbParam
	// returns names the fields of a successful result. A caller could learn how
	// to make the call from params alone and still had to guess what came back,
	// which is half a contract.
	returns []verbParam
	handler verbHandler
	// scope is what the verb does, for a restricted connection and for a
	// pane's grants (see conn_scope.go). An entry that names none is refused
	// to every restricted caller, and a test fails on it.
	scope scopeKind
	// link is what the verb needs on a link connection (see link_policy.go):
	// needNothing for nothing. An entry that names none is refused over every
	// link, and a test fails on it.
	link linkNeed
	// stream is set on a verb that keeps its connection after the reply, as
	// an event stream, a byte stream or a connection to something else. A
	// one-shot caller such as tuios call reads it to refuse the verb, and
	// list-verbs publishes it.
	stream *verbStream
}

// verbStream says when a verb keeps its connection after the reply. With no
// Param it always does. With Param, it does when that param is one of Values,
// a bool param as "true".
type verbStream struct {
	Param  string   `json:"param,omitempty"`
	Values []string `json:"values,omitempty"`
}

// verbDoc is the serialized form of a verbEntry in the list-verbs result.
type verbDoc struct {
	Verb        string      `json:"verb"`
	Description string      `json:"description"`
	Params      []verbParam `json:"params"`
	Returns     []verbParam `json:"returns,omitempty"`
	Examples    []string    `json:"examples,omitempty"`
	// Stream is present on a verb that keeps the connection after its reply.
	Stream *verbStream `json:"stream,omitempty"`
}

// HoldsConnection reports whether a call of the verb with params keeps the
// connection after its reply, so a caller that sends one request and reads
// one reply cannot make it.
func (doc verbDoc) HoldsConnection(params map[string]any) bool {
	st := doc.Stream
	if st == nil {
		return false
	}
	if st.Param == "" {
		return true
	}
	v, ok := params[st.Param]
	if !ok {
		return false
	}
	var got string
	switch v := v.(type) {
	case string:
		got = v
	case bool:
		got = strconv.FormatBool(v)
	default:
		return false
	}
	return slices.Contains(st.Values, got)
}

// identityReturn, confidenceReturn and evidenceAgeReturn are the three
// detection fields get-agent-state, explain-agent-detect and list-agents share,
// declared once so the three verbs describe them in the same words.
var (
	identityReturn    = verbParam{Name: "identity", Type: "string", Accepted: []string{"report", "manifest", "list", "hint", ""}}
	confidenceReturn  = verbParam{Name: "confidence", Type: "string", Accepted: []string{"certain", "strong", "none"}}
	evidenceAgeReturn = verbParam{Name: "evidence_age_ms", Type: "int", Nullable: true}
)

// sessionParam is the session selector shared by nearly every verb.
var sessionParam = verbParam{
	Name: "session",
	Type: "string",
}

// windowParam is the window selector shared by window-targeted verbs.
var windowParam = verbParam{
	Name: "window",
	Type: "string",
}

// selectWriteParams are the two params a write addressed by selector takes.
var selectWriteParams = []verbParam{
	{Name: "select", Type: "string"},
	{Name: "confirm", Type: "string"},
}

// verbRegistry returns the dispatch table for every JSON verb the daemon
// supports, so list-verbs and dispatch share one source of truth.
//
// It is built on first use, not in init. A table that init builds is reachable
// in every program that links this package, and so is every handler it names,
// which is most of the daemon. The browser build links this package for its
// types and never runs a daemon, and the table alone kept about 3 MB of daemon
// code in it. A function rather than a package-level initializer, because the
// table names list-verbs, which reads the table: an initializer would be a
// static initialization cycle.
func verbRegistry() map[string]verbEntry {
	verbRegistryOnce.Do(func() { verbRegistryMap = buildVerbRegistry() })
	return verbRegistryMap
}

var (
	verbRegistryOnce sync.Once
	verbRegistryMap  map[string]verbEntry
)

// buildVerbRegistry makes the table verbRegistry returns.
func buildVerbRegistry() map[string]verbEntry {
	table := map[string]verbEntry{
		"hello": {
			scope: scopeOpen,
			link:  needNothing,
			params: []verbParam{
				{Name: "client", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "protocol", Type: "int"},
				{Name: "term", Type: "string"},
				{Name: "color_term", Type: "string"},
			},
			handler: (*Daemon).verbHello,
		},
		"daemon-upgrade": {
			// daemon-upgrade replaces the daemon every session runs under.
			scope: scopeDeny,
			// daemon-upgrade replaces the daemon's image. Its handler refuses it over
			// a link too: an upgrade is run on the machine whose daemon it is.
			link: needRelay,
			params: []verbParam{
				{Name: "binary", Type: "string"},
			},
			returns: []verbParam{
				{Name: "pid", Type: "int"},
				{Name: "instance", Type: "string"},
				{Name: "from", Type: "string"},
				{Name: "to", Type: "string"},
				{Name: "binary", Type: "string"},
				{Name: "handoff", Type: "int"},
				{Name: "panes", Type: "int"},
				{Name: "manifest_bytes", Type: "int"},
				{Name: "clients", Type: "int"},
				{Name: "preflight_ms", Type: "int"},
			},
			handler: (*Daemon).verbDaemonUpgrade,
		},
		"restrict-connection": {
			scope: scopeOpen,
			// restrict-connection only gives up authority on the calling
			// connection, so it needs nothing.
			link: needNothing,
			params: []verbParam{
				{Name: "scope", Type: "string", Accepted: scopeNames, Default: ScopeOwn},
				{Name: "read_only", Type: "bool", Default: "false"},
				{Name: "pane_id", Type: "string"},
				{Name: "pane_token", Type: "string"},
			},
			returns: []verbParam{
				{Name: "scope", Type: "string", Accepted: scopeNames},
				{Name: "read_only", Type: "bool"},
				{Name: "window", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "via", Type: "string", Accepted: []string{"pid", "token"}},
				{Name: "sessions", Type: "[]string"},
			},
			handler: (*Daemon).verbRestrictConnection,
		},
		"pane-grants": {
			// pane-grants reports the caller's own grants and touches no session.
			// set-pane-grants changes what a pane may do, which is not for a
			// restricted caller.
			scope: scopeOpen,
			// pane-grants only reports; over a link it says no pane grants apply.
			// set-pane-grants is refused over a link by its handler whatever the
			// policy, and needs every capability here as well.
			link: needNothing,
			params: []verbParam{
				{Name: "pane_id", Type: "string"},
				{Name: "pane_token", Type: "string"},
				{Name: "peer_pid", Type: "int"},
				{Name: "peer_start", Type: "int"},
				{Name: "plugin_id", Type: "string"},
				{Name: "plugin_token", Type: "string"},
			},
			returns: []verbParam{
				{Name: "pane", Type: "bool"},
				{Name: "restart_needed", Type: "bool"},
				{Name: "window", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "via", Type: "string", Accepted: []string{"pid", "env", "token"}},
				{Name: "grants", Type: "[]string", Accepted: config.PaneGrantNames},
				{Name: "explicit", Type: "bool"},
				{Name: "mode", Type: "string", Accepted: config.PaneModes},
				{Name: "default_grants", Type: "[]string"},
			},
			handler: (*Daemon).verbPaneGrants,
		},
		"set-pane-grants": {
			scope: scopeDeny,
			link:  needRelay,
			params: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "grants", Type: "[]string", Accepted: grantAccepted},
				{Name: "reset", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "grants", Type: "[]string"},
				{Name: "explicit", Type: "bool"},
				{Name: "previous", Type: "[]string"},
				{Name: "previous_explicit", Type: "bool"},
			},
			handler: (*Daemon).verbSetPaneGrants,
		},
		"get-keep-awake": {
			scope: scopeGlobal,
			link:  needList,
			returns: []verbParam{
				{Name: "mode", Type: "string", Accepted: config.KeepAwakeModes},
				{Name: "held", Type: "bool"},
				{Name: "working", Type: "int"},
				{Name: "method", Type: "string"},
				{Name: "error", Type: "string"},
			},
			handler: (*Daemon).verbGetKeepAwake,
		},
		"set-keep-awake": {
			// It changes what the person's machine does and saves config.toml.
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				{Name: "mode", Type: "string", Required: true, Accepted: config.KeepAwakeModes},
			},
			returns: []verbParam{
				{Name: "mode", Type: "string"},
				{Name: "held", Type: "bool"},
				{Name: "working", Type: "int"},
				{Name: "method", Type: "string"},
				{Name: "error", Type: "string"},
				{Name: "saved", Type: "bool"},
				{Name: "save_error", Type: "string"},
			},
			handler: (*Daemon).verbSetKeepAwake,
		},
		"apply-config": {
			// apply-config is for the person, outside every pane.
			scope: scopeDeny,
			// apply-config is refused over a link by its handler as well.
			link: needRelay,
			params: []verbParam{
				{Name: "host", Type: "string"},
			},
			returns: []verbParam{
				{Name: "mode", Type: "string"},
				{Name: "default_grants", Type: "[]string"},
				{Name: "changes", Type: "[]string"},
				{Name: "still_waiting", Type: "bool"},
			},
			handler: (*Daemon).verbApplyConfig,
		},
		"list-verbs": {
			scope: scopeOpen,
			link:  needNothing,
			params: []verbParam{
				{Name: "verb", Type: "string"},
			},
			handler: (*Daemon).verbListVerbs,
		},
		"list-keys": {
			scope: scopeOpen,
			link:  needNothing,
			returns: []verbParam{
				{Name: "keys", Type: "[]object"},
				{Name: "modifiers", Type: "[]object"},
				{Name: "ctrl_characters", Type: "[]string"},
				{Name: "escape_prefixes", Type: "[]string"},
				{Name: "prefix_token", Type: "string"},
				{Name: "separators", Type: "[]string"},
				{Name: "max_repeat", Type: "int"},
			},
			handler: (*Daemon).verbListKeys,
		},
		"list-hooks": {
			scope: scopeGlobal,
			link:  needList,
			params: []verbParam{
				sessionParam,
				{Name: "event", Type: "string"},
			},
			returns: []verbParam{
				{Name: "hooks", Type: "[]object"},
				{Name: "total", Type: "int"},
				{Name: "events", Type: "[]string"},
				{Name: "client_attached", Type: "bool"},
			},
			handler: (*Daemon).verbListHooks,
		},
		"list-dock-components": {
			scope:  scopeDeny,
			link:   needList,
			params: []verbParam{sessionParam},
			returns: []verbParam{
				{Name: "components", Type: "[]string"},
			},
			handler: (*Daemon).verbListDockComponents,
		},
		"plugin-cell": {
			// A plugin's own verbs. checkPluginGrants holds them to the ui grant.
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				{Name: "id", Type: "string", Required: true},
				{Name: "text", Type: "string"},
				{Name: "action", Type: "string"},
				{Name: "pane", Type: "string"},
				{Name: "clear", Type: "bool"},
			},
			returns: []verbParam{{Name: "id", Type: "string"}},
			handler: (*Daemon).verbPluginCell,
		},
		"plugin-notify": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				{Name: "text", Type: "string", Required: true},
				{Name: "level", Type: "string", Accepted: []string{"info", "warning", "error"}},
			},
			returns: []verbParam{{Name: "type", Type: "string"}},
			handler: (*Daemon).verbPluginNotify,
		},
		"refresh-dock": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "component", Type: "string"},
			},
			returns: []verbParam{
				{Name: "component", Type: "string"},
			},
			handler: (*Daemon).verbRefreshDock,
		},
		"pip": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "off", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "pinned", Type: "bool"},
				{Name: "window_id", Type: "string"},
			},
			handler: (*Daemon).verbPiP,
		},
		"list-sessions": {
			scope:   scopeGlobal,
			link:    needList,
			handler: (*Daemon).verbListSessions,
		},
		"list-clients": {
			scope: scopeGlobal,
			link:  needList,
			returns: []verbParam{
				{Name: "clients", Type: "[]object"},
			},
			handler: (*Daemon).verbListClients,
		},
		"new-session": {
			scope: scopeDeny,
			link:  needOpen,
			params: []verbParam{
				{Name: "name", Type: "string"},
				{Name: "width", Type: "int", Default: "80"},
				{Name: "height", Type: "int", Default: "24"},
				{Name: "window", Type: "bool", Default: "true"},
				{Name: "window_name", Type: "string"},
				{Name: "cwd", Type: "string"},
				{Name: "command", Type: "[]string"},
				{Name: "space", Type: "string"},
				{Name: "env", Type: "object"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "space", Type: "string"},
				{Name: "session_id", Type: "string"},
				{Name: "width", Type: "int"},
				{Name: "height", Type: "int"},
				{Name: "windows", Type: "int"},
				{Name: "window_id", Type: "string"},
				{Name: "window_name", Type: "string"},
				{Name: "pty_id", Type: "string"},
			},
			handler: (*Daemon).verbNewSession,
		},
		"switch-session": {
			// switch-session moves a client and detach-client takes clients off
			// their sessions: client-level actions, like attach.
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				{Name: "name", Type: "string"},
				{Name: "host", Type: "string"},
				{Name: "create", Type: "bool", Default: "false"},
				{Name: "cwd", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "client", Type: "string"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "host", Type: "string"},
				{Name: "client_id", Type: "string"},
				{Name: "created", Type: "bool"},
				{Name: "already", Type: "bool"},
			},
			handler: (*Daemon).verbSwitchSession,
		},
		"detach-client": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				{Name: "client", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "all_other", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "detached", Type: "array"},
			},
			handler: (*Daemon).verbDetachClient,
		},
		"ssh-agent-path": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				{Name: "session", Type: "string"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "path", Type: "string"},
				{Name: "follow", Type: "bool"},
				{Name: "target", Type: "string"},
			},
			handler: (*Daemon).verbSSHAgentPath,
		},
		"display-env": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				{Name: "session", Type: "string"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "follow", Type: "bool"},
				{Name: "own", Type: "bool"},
				{Name: "link", Type: "string"},
				{Name: "target", Type: "string"},
				{Name: "env", Type: "object"},
			},
			handler: (*Daemon).verbDisplayEnv,
		},
		"new-worktree": {
			scope: scopeDeny,
			link:  needOpen,
			params: append([]verbParam{
				{Name: "repo", Type: "string"},
				{Name: "branch", Type: "string"},
				{Name: "base", Type: "string"},
				{Name: "name", Type: "string"},
				{Name: "command", Type: "[]string"},
				{Name: "new_branch", Type: "bool"},
				{Name: "prompt", Type: "string"},
			}, slices.Concat(setupParams, repoSourceParams)...),
			returns: append([]verbParam{
				{Name: "session", Type: "string"},
				{Name: "session_id", Type: "string"},
				{Name: "repo", Type: "string"},
				{Name: "repo_root", Type: "string"},
				{Name: "branch", Type: "string"},
				{Name: "created_branch", Type: "bool"},
				{Name: "path", Type: "string"},
				{Name: "window_id", Type: "string"},
				{Name: "pty_id", Type: "string"},
				{Name: "cloned", Type: "bool"},
				{Name: "hook_error", Type: "string"},
				{Name: "prompt_status", Type: "string"},
				{Name: "prompt_note", Type: "string"},
			}, setupReturns...),
			handler: (*Daemon).verbNewWorktree,
		},
		"list-worktrees": {
			scope: scopeGlobal,
			link:  needList,
			params: []verbParam{
				{Name: "repo", Type: "string"},
				{Name: "group", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "changes", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "worktrees", Type: "[]object"},
				{Name: "total", Type: "int"},
			},
			handler: (*Daemon).verbListWorktrees,
		},
		"remove-worktree": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "stash", Type: "bool", Default: "false"},
				{Name: "force", Type: "bool", Default: "false"},
				{Name: "keep_session", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "branch", Type: "string"},
				{Name: "path", Type: "string"},
				{Name: "changes", Type: "int"},
				{Name: "stashed", Type: "bool"},
				{Name: "discarded", Type: "bool"},
				{Name: "session_killed", Type: "bool"},
				{Name: "clients_switched", Type: "int"},
				{Name: "branch_kept", Type: "bool"},
			},
			handler: (*Daemon).verbRemoveWorktree,
		},
		"fan": {
			scope: scopeLaunch,
			link:  needOpen,
			params: append([]verbParam{
				{Name: "count", Type: "int"},
				// agent is documented without Required on purpose: agents
				// stands in for it. See Changes to existing verbs.
				{Name: "agent", Type: "string"},
				{Name: "agents", Type: "[]string"},
				{Name: "prompt", Type: "string"},
				{Name: "prompts", Type: "[]string"},
				{Name: "repo", Type: "string"},
				{Name: "base", Type: "string"},
				{Name: "name", Type: "string"},
				{Name: "join", Type: "string"},
				{Name: "ready_timeout", Type: "int", Default: "600000"},
				{Name: "env", Type: "object"},
				{Name: "task", Type: "string"},
				{Name: "task_session", Type: "string"},
				{Name: "human_nonce", Type: "string"},
				{Name: "model", Type: "string"},
				{Name: "effort", Type: "string"},
				{Name: "permission_mode", Type: "string"},
				grantsParam,
			}, slices.Concat(setupParams, repoSourceParams)...),
			returns: []verbParam{
				{Name: "group", Type: "string"},
				{Name: "repo", Type: "string"},
				{Name: "repo_root", Type: "string"},
				{Name: "cloned", Type: "bool"},
				{Name: "agent", Type: "string"},
				{Name: "command", Type: "string"},
				{Name: "prompt", Type: "string"},
				{Name: "sessions", Type: "[]object"},
				{Name: "total", Type: "int"},
				{Name: "task", Type: "object"},
				{Name: "task_note", Type: "string"},
			},
			handler: (*Daemon).verbFan,
		},
		"bundle-worktree": {
			// From the host targeting work: reading a worktree's files out is for
			// the person's own CLI (worktree pull), not a restricted caller.
			scope: scopeDeny,
			// bundle-worktree reads a worktree's files out, which write already
			// reaches through a shell, and list must not.
			link: needWrite,
			params: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "full", Type: "bool", Default: "false"},
				{Name: "token", Type: "string"},
				{Name: "offset", Type: "int", Default: "0"},
				{Name: "release", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "token", Type: "string"},
				{Name: "content", Type: "string"},
				{Name: "offset", Type: "int"},
				{Name: "next", Type: "int"},
				{Name: "done", Type: "bool"},
				{Name: "repo", Type: "string"},
				{Name: "origin_url", Type: "string"},
				{Name: "branch", Type: "string"},
				{Name: "base", Type: "string"},
				{Name: "base_commit", Type: "string"},
				{Name: "head", Type: "string"},
				{Name: "full", Type: "bool"},
				{Name: "bundle_bytes", Type: "int"},
				{Name: "patch_bytes", Type: "int"},
				{Name: "changes", Type: "int"},
				{Name: "size", Type: "int"},
				{Name: "sha256", Type: "string"},
			},
			handler: (*Daemon).verbBundleWorktree,
		},
		"start-agent": {
			scope: scopeLaunch,
			link:  needOpen,
			params: append([]verbParam{
				{Name: "session", Type: "string"},
				{Name: "agent", Type: "string"},
				{Name: "args", Type: "[]string"},
				{Name: "name", Type: "string"},
				{Name: "cwd", Type: "string"},
				{Name: "repo", Type: "string"},
				{Name: "workspace", Type: "int"},
				{Name: "focus", Type: "bool", Default: "false"},
				{Name: "prompt", Type: "string"},
				{Name: "ready_timeout", Type: "int", Default: "120000"},
				{Name: "no_wait", Type: "bool", Default: "false"},
				{Name: "env", Type: "object"},
				{Name: "protocol", Type: "string", Accepted: agentproto.Protocols},
				{Name: "task", Type: "string"},
				{Name: "task_host", Type: "string"},
				{Name: "task_session", Type: "string"},
				{Name: "task_mail", Type: "bool"},
				{Name: "human_nonce", Type: "string"},
				{Name: "parent", Type: "string"},
				{Name: "model", Type: "string"},
				{Name: "effort", Type: "string"},
				{Name: "permission_mode", Type: "string"},
				{Name: "preset", Type: "string"},
				{Name: "add_dirs", Type: "[]string"},
				{Name: "after_ready", Type: "[]string"},
				grantsParam,
			}, repoSourceParams...),
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "session_id", Type: "string"},
				{Name: "created_session", Type: "bool"},
				{Name: "pty_id", Type: "string"},
				{Name: "workspace", Type: "int"},
				{Name: "cwd", Type: "string"},
				{Name: "cloned", Type: "bool"},
				{Name: "parent", Type: "object"},
				{Name: "root", Type: "object"},
				{Name: "window_id", Type: "string"},
				{Name: "name", Type: "string"},
				{Name: "agent", Type: "string"},
				{Name: "command", Type: "string"},
				{Name: "protocol", Type: "string"},
				{Name: "ready", Type: "bool"},
				{Name: "ready_by", Type: "string"},
				{Name: "state", Type: "string"},
				{Name: "outcome", Type: "string", Accepted: []string{string(agentStartReady), string(agentStartBlocked), string(agentStartTimeout), string(agentStartWindowClosed), string(agentStartSessionEnded), string(agentStartShutdown), string(agentStartStarted), string(agentStartExited)}},
				{Name: "path_from", Type: "string", Accepted: []string{"caller", "daemon"}},
				{Name: "blocked_by", Type: "string"},
				{Name: "reason", Type: "string"},
				{Name: "prompt_status", Type: "string"},
				{Name: "prompt_note", Type: "string"},
				{Name: "after_ready_status", Type: "string"},
				{Name: "after_ready_note", Type: "string"},
				{Name: "task", Type: "object"},
				{Name: "task_note", Type: "string"},
			},
			handler: (*Daemon).verbStartAgent,
		},
		"agent-preset": {
			scope: scopeLaunch,
			link:  needOpen,
			params: []verbParam{
				{Name: "preset", Type: "string"},
				{Name: "params", Type: "object"},
			},
			returns: []verbParam{
				{Name: "params", Type: "object"},
			},
			handler: (*Daemon).verbAgentPreset,
		},
		"agent-readiness": {
			scope: scopeLaunch,
			link:  needOpen,
			params: []verbParam{
				{Name: "harnesses", Type: "[]string"},
				{Name: "env", Type: "object"},
				{Name: "repo_url", Type: "string"},
				{Name: "repos_root", Type: "string"},
				{Name: "peer_instance", Type: "string"},
			},
			returns: []verbParam{
				{Name: "agents", Type: "[]object"},
				{Name: "repo", Type: "string"},
				{Name: "load", Type: "object"},
				{Name: "cap", Type: "int"},
				{Name: "back", Type: "string"},
			},
			handler: (*Daemon).verbAgentReadiness,
		},
		"place-agent": {
			scope: scopeLaunch,
			link:  needOpen,
			params: []verbParam{
				{Name: "agent", Type: "string"},
				{Name: "repo_url", Type: "string"},
				{Name: "repos_root", Type: "string"},
				{Name: "clone", Type: "bool", Default: "false"},
				{Name: "local_origin", Type: "string"},
				{Name: "task", Type: "string"},
				{Name: "task_session", Type: "string"},
				{Name: "reserve", Type: "bool", Default: "true"},
				{Name: "exclude", Type: "[]string"},
				{Name: "env", Type: "object"},
			},
			returns: []verbParam{
				{Name: "host", Type: "string"},
				{Name: "name", Type: "string"},
				{Name: "why", Type: "string"},
				{Name: "task_host", Type: "string"},
				{Name: "clone", Type: "bool"},
				{Name: "machines", Type: "[]object"},
			},
			handler: (*Daemon).verbPlaceAgent,
		},
		"delegate": {
			scope: scopeLaunch,
			link:  needOpen,
			params: []verbParam{
				{Name: "prompt", Type: "string", Required: true},
				{Name: "agent", Type: "string"},
				{Name: "args", Type: "[]string"},
				{Name: "role", Type: "string", Accepted: delegateRoles, Default: "general"},
				{Name: "isolation", Type: "string", Accepted: []string{delegateShared, delegateWorktree}, Default: delegateShared},
				{Name: "name", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "cwd", Type: "string"},
				{Name: "parent", Type: "string"},
				{Name: "wait", Type: "int"},
				{Name: "ready_timeout", Type: "int", Default: "120000"},
				{Name: "env", Type: "object"},
				grantsParam,
			},
			returns: []verbParam{
				{Name: "delegated", Type: "bool"},
				{Name: "window_id", Type: "string"},
				{Name: "role", Type: "string"},
				{Name: "isolation", Type: "string"},
				{Name: "mode", Type: "string"},
				{Name: "next", Type: "string"},
				{Name: "worktree", Type: "string"},
				{Name: "outcome", Type: "string", Accepted: []string{delegateDone, delegateErrored, "question", "approval", delegateClosed, "timeout"}},
				{Name: "answer", Type: "string"},
				{Name: "question", Type: "object"},
				{Name: "wait_note", Type: "string"},
			},
			handler: (*Daemon).verbDelegate,
		},
		"list-routes": {
			scope: scopeGlobal,
			link:  needList,
			returns: []verbParam{
				{Name: "routes", Type: "[]object"},
			},
			handler: (*Daemon).verbListRoutes,
		},
		"list-hosts": {
			scope: scopeGlobal,
			link:  needList,
			returns: []verbParam{
				{Name: "hosts", Type: "[]string"},
				{Name: "total", Type: "int"},
				{Name: "events_push", Type: "bool"},
				{Name: "config_problems", Type: "[]string"},
			},
			handler: (*Daemon).verbListHosts,
		},
		"retry-host": {
			// retry-host is the person's act from the rail or from 'tuios hosts
			// signin'. A restricted caller has no reason to make a link dial.
			scope: scopeDeny,
			// Another machine has no reason to make this one's links dial.
			link: needRelay,
			params: []verbParam{
				{Name: "host", Type: "string"},
			},
			returns: []verbParam{
				{Name: "host", Type: "string"},
				{Name: "status", Type: "string"},
				{Name: "approval_url", Type: "string"},
				{Name: "approval_refused", Type: "bool"},
			},
			handler: (*Daemon).verbRetryHost,
		},
		"open-host-connection": {
			stream: &verbStream{},
			scope:  scopeDeny,
			link:   needRelay,
			params: []verbParam{
				{Name: "host", Type: "string"},
				{Name: "ssh_auth_sock", Type: "string"},
			},
			returns: []verbParam{
				{Name: "host", Type: "string"},
				{Name: "daemon_version", Type: "string"},
				{Name: "protocol", Type: "int"},
			},
			handler: (*Daemon).verbOpenHostConnection,
		},
		"open-pane": {
			stream: &verbStream{},
			scope:  scopeDeny,
			link:   needOpen,
			params: []verbParam{
				{Name: "width", Type: "int"},
				{Name: "height", Type: "int"},
				{Name: "cwd", Type: "string"},
				{Name: "command", Type: "[]string"},
				{Name: "term", Type: "string"},
				{Name: "color_term", Type: "string"},
				{Name: "shell", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "resumable", Type: "bool"},
				{Name: "resume", Type: "object"},
				{Name: "kitty_media", Type: "bool"},
			},
			returns: []verbParam{
				{Name: "pane", Type: "string"},
				{Name: "calls_token", Type: "string"},
				{Name: "resume_token", Type: "string"},
				{Name: "grace", Type: "int"},
				{Name: "resumed", Type: "bool"},
				{Name: "pixels", Type: "bool"},
				{Name: "kitty_media_token", Type: "string"},
				{Name: "gap", Type: "bool"},
			},
			handler: (*Daemon).verbOpenPane,
		},
		"close-pane": {
			scope: scopeDeny,
			link:  needOpen,
			params: []verbParam{
				{Name: "pane", Type: "string"},
			},
			returns: []verbParam{
				{Name: "pane", Type: "string"},
				{Name: "closed", Type: "bool"},
			},
			handler: (*Daemon).verbClosePane,
		},
		"link-peer": {
			stream: &verbStream{},
			// From the host policy and dropped-link work: the link handshake,
			// ending a hosted pane, and passing on held mail are for a link
			// connection or the person's client, never a restricted caller.
			scope: scopeDeny,
			link:  needNothing,
			params: []verbParam{
				{Name: "peer", Type: "string"},
				{Name: "pinned", Type: "bool"},
				{Name: "ssh_auth_sock", Type: "string"},
				{Name: "closes", Type: "string"},
			},
			returns: []verbParam{
				{Name: "peer", Type: "string"},
				{Name: "close_token", Type: "string"},
			},
			handler: (*Daemon).verbLinkPeer,
		},
		"resize-pane": {
			scope: scopeDeny,
			link:  needOpen,
			params: []verbParam{
				{Name: "pane", Type: "string"},
				{Name: "width", Type: "int"},
				{Name: "height", Type: "int"},
				{Name: "xpixel", Type: "int"},
				{Name: "ypixel", Type: "int"},
			},
			returns: []verbParam{
				{Name: "pane", Type: "string"},
				{Name: "width", Type: "int"},
				{Name: "height", Type: "int"},
			},
			handler: (*Daemon).verbResizePane,
		},
		"pane-cwd": {
			scope: scopeDeny,
			link:  needOpen,
			params: []verbParam{
				{Name: "pane", Type: "string"},
			},
			returns: []verbParam{
				{Name: "pane", Type: "string"},
				{Name: "cwd", Type: "string"},
			},
			handler: (*Daemon).verbPaneCwd,
		},
		"pane-agent": {
			scope: scopeDeny,
			link:  needOpen,
			params: []verbParam{
				{Name: "pane", Type: "string"},
			},
			returns: []verbParam{
				{Name: "pane", Type: "string"},
				{Name: "running", Type: "bool"},
				{Name: "comm", Type: "string"},
				{Name: "argv", Type: "array"},
				{Name: "exe", Type: "string"},
				{Name: "pid", Type: "int"},
				{Name: "shell_pid", Type: "int"},
			},
			handler: (*Daemon).verbPaneAgent,
		},
		"paste-image": {
			// paste-image is the person's act and paste-pane-image the owning
			// daemon's. Neither is for a restricted caller.
			scope: scopeDeny,
			link:  needWrite | needFiles,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "content", Type: "string"},
				{Name: "human_nonce", Type: "string"},
			},
			returns: []verbParam{
				{Name: "path", Type: "string"},
				{Name: "host", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "bytes", Type: "int"},
			},
			handler: (*Daemon).verbPasteImage,
		},
		"paste-pane-image": {
			scope: scopeDeny,
			// paste-pane-image writes a file for a hosted pane, which the peer
			// opened, and needs the pane's token as well. paste-image writes a file
			// and has its path typed into a pane, which is what write already
			// reaches. Its handler also wants the person. Both put a file from the
			// other machine on this one, so both need files too: a person who took
			// files away from a machine expects nothing it sends to land on disk.
			link: needOpen | needFiles,
			params: []verbParam{
				{Name: "pane", Type: "string"},
				{Name: "token", Type: "string"},
				{Name: "content", Type: "string"},
			},
			returns: []verbParam{
				{Name: "pane", Type: "string"},
				{Name: "path", Type: "string"},
				{Name: "bytes", Type: "int"},
			},
			handler: (*Daemon).verbPastePaneImage,
		},
		"pane-calls": {
			stream: &verbStream{},
			scope:  scopeDeny,
			link:   needOpen,
			params: []verbParam{
				{Name: "pane", Type: "string"},
				{Name: "token", Type: "string"},
			},
			returns: []verbParam{
				{Name: "pane", Type: "string"},
			},
			handler: (*Daemon).verbPaneCalls,
		},
		"pane-kitty-medium": {
			stream: &verbStream{},
			scope:  scopeDeny,
			// pane-kitty-medium carries the bytes of a file a hosted pane named in a
			// kitty graphics command: a file read, so it needs files.
			link: needFiles,
			params: []verbParam{
				{Name: "pane", Type: "string"},
				{Name: "token", Type: "string"},
				{Name: "id", Type: "string"},
			},
			returns: []verbParam{
				{Name: "pane", Type: "string"},
				{Name: "size", Type: "int"},
				{Name: "compressed", Type: "bool"},
			},
			handler: (*Daemon).verbPaneKittyMedium,
		},
		"read-dir": {
			scope: scopeDeny,
			link:  needList,
			params: []verbParam{
				{Name: "dir", Type: "string"},
				{Name: "max", Type: "int"},
			},
			returns: []verbParam{
				{Name: "dir", Type: "string"},
				{Name: "entries", Type: "[]string"},
				{Name: "capped", Type: "bool"},
				{Name: "err", Type: "string"},
			},
			handler: (*Daemon).verbReadDir,
		},
		"wait-dir": {
			scope: scopeDeny,
			link:  needList,
			params: []verbParam{
				{Name: "dir", Type: "string"},
				{Name: "timeout", Type: "int"},
				{Name: "after", Type: "string"},
			},
			returns: []verbParam{
				{Name: "dir", Type: "string"},
				{Name: "changed", Type: "bool"},
				{Name: "names", Type: "string"},
			},
			handler: (*Daemon).verbWaitDir,
		},
		"list-host-sessions": {
			scope: scopeGlobal,
			link:  needList,
			params: []verbParam{
				{Name: "host", Type: "string"},
			},
			returns: []verbParam{
				{Name: "hosts", Type: "[]string"},
			},
			handler: (*Daemon).verbListHostSessions,
		},
		"list-host-agents": {
			scope: scopeGlobal,
			link:  needList,
			params: []verbParam{
				{Name: "host", Type: "string"},
				{Name: "all", Type: "bool"},
				{Name: "select", Type: "string"},
			},
			returns: []verbParam{
				{Name: "hosts", Type: "[]string"},
				{Name: "select", Type: "string"},
			},
			handler: (*Daemon).verbListHostAgents,
		},
		"session-info": {
			scope:   scopeRead,
			link:    needList,
			params:  []verbParam{sessionParam},
			handler: (*Daemon).verbSessionInfo,
		},
		"list-windows": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				sessionParam,
				{Name: "rail", Type: "bool", Default: "false"},
			},
			handler: (*Daemon).verbListWindows,
		},
		"rail-pane": {
			scope: scopeDeny,
			link:  needOpen,
			params: []verbParam{
				sessionParam,
				{Name: "key", Type: "string"},
				{Name: "command", Type: "string[]"},
				{Name: "cwd", Type: "string"},
				{Name: "width", Type: "int"},
				{Name: "height", Type: "int"},
				{Name: "off", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "created", Type: "bool"},
			},
			handler: (*Daemon).verbRailPane,
		},
		"get-window": {
			scope:  scopeRead,
			link:   needList,
			params: []verbParam{sessionParam, windowParam},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "display_name", Type: "string"},
				{Name: "agent_state", Type: "string"},
			},
			handler: (*Daemon).verbGetWindow,
		},
		"new-window": {
			scope: scopeDeny,
			link:  needOpen,
			params: []verbParam{
				sessionParam,
				{Name: "name", Type: "string"},
				{Name: "workspace", Type: "int"},
				{Name: "cwd", Type: "string"},
				{Name: "focus", Type: "bool", Default: "true"},
				{Name: "command", Type: "[]string"},
				{Name: "host", Type: "string"},
				grantsParam,
				{Name: "close_on_exit", Type: "bool", Default: "false"},
				{Name: "keep_open", Type: "bool", Default: "false"},
				{Name: "wait", Type: "bool", Default: "false"},
				{Name: "timeout", Type: "int", Default: "0"},
				{Name: "kind", Type: "string", Default: "terminal", Accepted: WindowKinds},
				{Name: "uri", Type: "string"},
				{Name: "env", Type: "object"},
				{Name: "shell_integration", Type: "string", Accepted: config.ShellIntegrationModes},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "host", Type: "string"},
				{Name: "name", Type: "string"},
				{Name: "workspace", Type: "int"},
				{Name: "pty_id", Type: "string"},
				{Name: "focused", Type: "bool"},
				{Name: "unplaced", Type: "bool"},
				{Name: "keep_open", Type: "bool"},
				{Name: "exit_code", Type: "int"},
				{Name: "signal", Type: "int"},
			},
			handler: (*Daemon).verbNewWindow,
		},
		"popup": {
			scope: scopeDeny,
			link:  needOpen,
			params: []verbParam{
				sessionParam,
				{Name: "command", Type: "[]string", Required: true},
				{Name: "width", Type: "string", Default: PopupDefaultWidth},
				{Name: "height", Type: "string", Default: PopupDefaultHeight},
				{Name: "dim", Type: "int"},
				{Name: "name", Type: "string"},
				{Name: "cwd", Type: "string"},
				{Name: "cwd_from", Type: "string"},
				{Name: "workspace", Type: "int"},
				{Name: "wait", Type: "bool", Default: "false"},
				{Name: "capture_stdout", Type: "bool", Default: "false"},
				{Name: "timeout", Type: "int", Default: "0"},
				{Name: "scratch_name", Type: "string", Default: ""},
				{Name: "scratch", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "exit_code", Type: "int"},
				{Name: "stdout", Type: "string"},
				{Name: "stdout_truncated", Type: "bool"},
				{Name: "window_id", Type: "string"},
				{Name: "name", Type: "string"},
				{Name: "workspace", Type: "int"},
				{Name: "pty_id", Type: "string"},
				{Name: "width", Type: "string"},
				{Name: "height", Type: "string"},
			},
			handler: (*Daemon).verbPopup,
		},
		"tml-open": {
			stream: &verbStream{Param: "events", Values: []string{"true"}},
			// A pane opens, changes and closes a TML surface with the write grant,
			// in its own session, and only its own surfaces (tmlOwnerCheck). The
			// input verb acts as the person.
			scope: scopeWrite,
			// A link needs every relay capability to open or change a surface,
			// which the default policy does not give: a surface names its owner on
			// its frame, and a surface opened from another machine would need that
			// machine's name there.
			link: needRelay,
			params: []verbParam{
				sessionParam,
				{Name: "doc", Type: "string"},
				{Name: "target", Type: "string", Default: TMLTargetPopup, Accepted: tmlTargets},
				{Name: "name", Type: "string"},
				{Name: "width", Type: "string", Default: PopupDefaultWidth},
				{Name: "height", Type: "string", Default: PopupDefaultHeight},
				{Name: "workspace", Type: "int"},
				{Name: "wait", Type: "bool", Default: "false"},
				{Name: "timeout", Type: "int", Default: "0"},
				{Name: "events", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "surface", Type: "string"},
				{Name: "seq", Type: "int"},
				{Name: "boot_id", Type: "string"},
				{Name: "window_id", Type: "string"},
				{Name: "target", Type: "string"},
				{Name: "workspace", Type: "int"},
				{Name: "value", Type: "any"},
				{Name: "reason", Type: "string"},
			},
			handler: (*Daemon).verbTMLOpen,
		},
		"tml-close": {
			scope: scopeWrite,
			link:  needRelay,
			params: []verbParam{
				{Name: "surface", Type: "string"},
				{Name: "value", Type: "any"},
			},
			returns: []verbParam{
				{Name: "surface", Type: "string"},
			},
			handler: (*Daemon).verbTMLClose,
		},
		"tml-input": {
			scope: scopeDeny,
			// A client attached over a link draws this machine's surfaces (see
			// tml_surface.go, "Surfaces over a link"). Reading one is reading a
			// screen. What the person does on one, and where it sits, is what the
			// attach already does to a pane: tml-input and tml-place also need the
			// attach nonce of a client on the link-human socket. A pane() action a
			// tml-input carries is held to the verb it runs as well.
			link: needWrite,
			params: []verbParam{
				{Name: "surface", Type: "string"},
				{Name: "human_nonce", Type: "string"},
				{Name: "ops", Type: "[]object"},
				{Name: "close", Type: "bool", Default: "false"},
				{Name: "value", Type: "any"},
				{Name: "events", Type: "[]object"},
				{Name: "actions", Type: "[]object"},
				{Name: "view", Type: "object"},
			},
			returns: []verbParam{
				{Name: "version", Type: "int"},
				{Name: "actions", Type: "[]object"},
			},
			handler: (*Daemon).verbTMLInput,
		},
		"tml-patch": {
			scope: scopeWrite,
			link:  needRelay,
			params: []verbParam{
				{Name: "surface", Type: "string"},
				{Name: "ops", Type: "[]object", Required: true},
			},
			returns: []verbParam{
				{Name: "version", Type: "int"},
				{Name: "changed", Type: "[]string"},
			},
			handler: (*Daemon).verbTMLPatch,
		},
		"tml-replace": {
			scope: scopeWrite,
			link:  needRelay,
			params: []verbParam{
				{Name: "surface", Type: "string"},
				{Name: "doc", Type: "string"},
				{Name: "keep_state", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "version", Type: "int"},
			},
			handler: (*Daemon).verbTMLReplace,
		},
		"tml-get": {
			// tml-get checks the surface's session and the read grant itself
			// (tmlReach), so the pane that opened a surface reads it with write.
			scope: scopeOpen,
			link:  needList,
			params: []verbParam{
				{Name: "surface", Type: "string"},
			},
			returns: []verbParam{
				{Name: "doc", Type: "object"},
				{Name: "state", Type: "object"},
				{Name: "version", Type: "int"},
				{Name: "window_id", Type: "string"},
				{Name: "title", Type: "string"},
				{Name: "owner", Type: "string"},
			},
			handler: (*Daemon).verbTMLGet,
		},
		"tml-list": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				sessionParam,
			},
			returns: []verbParam{
				{Name: "surfaces", Type: "[]object"},
			},
			handler: (*Daemon).verbTMLList,
		},
		"tml-place": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				{Name: "surface", Type: "string"},
				{Name: "target", Type: "string", Accepted: tmlTargets},
				{Name: "human_nonce", Type: "string"},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "target", Type: "string"},
			},
			handler: (*Daemon).verbTMLPlace,
		},
		"artifact-list": {
			// artifact-list checks the pane and the read grant itself, so a pane
			// reads its own artifacts with no grant.
			scope: scopeOpen,
			link:  needRelay,
			params: []verbParam{
				{Name: "window", Type: "string"},
			},
			returns: []verbParam{
				{Name: "artifacts", Type: "[]object"},
			},
			handler: (*Daemon).verbArtifactList,
		},
		"tml-validate": {
			// tml-validate reads only the document it is given.
			scope: scopeOpen,
			link:  needNothing,
			params: []verbParam{
				{Name: "doc", Type: "string"},
			},
			returns: []verbParam{
				{Name: "warnings", Type: "[]object"},
			},
			handler: (*Daemon).verbTMLValidate,
		},
		"split-window": {
			scope: scopeDeny,
			link:  needOpen,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "direction", Type: "string", Accepted: splitDirections},
				{Name: "side", Type: "string", Accepted: splitSides},
				{Name: "ratio", Type: "int"},
				{Name: "name", Type: "string"},
				{Name: "command", Type: "[]string"},
				{Name: "cwd", Type: "string"},
				{Name: "focus", Type: "bool", Default: "true"},
				{Name: "keep_open", Type: "bool", Default: "false"},
				{Name: "close_on_exit", Type: "bool", Default: "false"},
				{Name: "env", Type: "object"},
				{Name: "host", Type: "string"},
				{Name: "shell_integration", Type: "string", Accepted: config.ShellIntegrationModes},
				grantsParam,
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "direction", Type: "string"},
				{Name: "side", Type: "string"},
				{Name: "name", Type: "string"},
				{Name: "workspace", Type: "int"},
				{Name: "unplaced", Type: "bool"},
			},
			handler: (*Daemon).verbSplitWindow,
		},
		"layout-template": {
			// It opens panes and types their commands, as split-window
			// opens one: the person's tool, not a restricted caller's.
			scope: scopeDeny,
			link:  needOpen,
			params: []verbParam{
				{Name: "action", Type: "string", Required: true, Accepted: layoutTemplateActions},
				sessionParam,
				{Name: "name", Type: "string"},
				{Name: "workspace", Type: "int"},
			},
			returns: []verbParam{
				{Name: "layouts", Type: "[]object"},
				{Name: "dir", Type: "string"},
				{Name: "name", Type: "string"},
				{Name: "panes", Type: "int"},
				{Name: "mode", Type: "string"},
				{Name: "workspace", Type: "int"},
				{Name: "path", Type: "string"},
				{Name: "focus", Type: "string"},
				{Name: "opened", Type: "int"},
				{Name: "reused", Type: "int"},
				{Name: "minimized", Type: "int"},
				{Name: "deleted", Type: "bool"},
			},
			handler: (*Daemon).verbLayoutTemplate,
		},
		"zoom-window": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "zoom", Type: "bool"},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "zoomed", Type: "bool"},
				{Name: "changed", Type: "bool"},
			},
			handler: (*Daemon).verbZoomWindow,
		},
		"swap-window": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "with", Type: "string", Required: true},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "with", Type: "string"},
			},
			handler: (*Daemon).verbSwapWindow,
		},
		"resize-window": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "direction", Type: "string", Required: true, Accepted: focusDirections},
				{Name: "amount", Type: "int", Default: "5"},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "changed", Type: "bool"},
			},
			handler: (*Daemon).verbResizeWindow,
		},
		"focus-window": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "relative", Type: "string", Accepted: focusRelatives},
				{Name: "direction", Type: "string", Accepted: focusDirections},
			},
			returns: []verbParam{
				{Name: "focused_window_id", Type: "string"},
				{Name: "current_workspace", Type: "int"},
				{Name: "window", Type: "object"},
			},
			handler: (*Daemon).verbFocusWindow,
		},
		"move-window": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "workspace", Type: "int"},
				{Name: "follow", Type: "bool", Default: "false"},
				{Name: "to_session", Type: "string"},
				{Name: "beside", Type: "string"},
				{Name: "side", Type: "string", Default: "right", Accepted: splitSides},
				{Name: "ratio", Type: "int"},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "from_session", Type: "string"},
				{Name: "from_workspace", Type: "int"},
				{Name: "workspace", Type: "int"},
				{Name: "current_workspace", Type: "int"},
			},
			handler: (*Daemon).verbMoveWindow,
		},
		"set-window": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "name", Type: "string"},
				{Name: "minimized", Type: "bool"},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "display_name", Type: "string"},
				{Name: "minimized", Type: "bool"},
			},
			handler: (*Daemon).verbSetWindow,
		},
		"select-workspace": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "workspace", Type: "int", Required: true},
				{Name: "return_to", Type: "int", Default: "0"},
			},
			returns: []verbParam{
				{Name: "current_workspace", Type: "int"},
				{Name: "focused_window_id", Type: "string"},
				{Name: "window_count", Type: "int"},
			},
			handler: (*Daemon).verbSelectWorkspace,
		},
		"list-workspaces": {
			scope:  scopeRead,
			link:   needList,
			params: []verbParam{sessionParam},
			returns: []verbParam{
				{Name: "workspaces", Type: "[]object"},
				{Name: "current_workspace", Type: "int"},
				{Name: "order", Type: "[]int"},
			},
			handler: (*Daemon).verbListWorkspaces,
		},
		"set-layout": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "tiling", Type: "bool"},
				{Name: "equalize", Type: "bool", Default: "false"},
				{Name: "rotate", Type: "bool", Default: "false"},
				{Name: "master_position", Type: "string"},
				{Name: "master_count", Type: "int"},
			},
			returns: []verbParam{
				{Name: "tiling_mode", Type: "string"},
				{Name: "layout_mode", Type: "string"},
				{Name: "master_ratio", Type: "float"},
				{Name: "master_position", Type: "string"},
				{Name: "master_count", Type: "int"},
			},
			handler: (*Daemon).verbSetLayout,
		},
		"run-command": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "command", Type: "string"},
				{Name: "args", Type: "[]string"},
			},
			returns: []verbParam{
				{Name: "command", Type: "string"},
				{Name: "routed", Type: "bool"},
			},
			handler: (*Daemon).verbRunCommand,
		},
		"close-window": {
			scope:   scopeDeny,
			link:    needWrite,
			params:  []verbParam{sessionParam, windowParam},
			handler: (*Daemon).verbCloseWindow,
		},
		"close-workspace": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "workspace", Type: "int"},
			},
			returns: []verbParam{
				{Name: "workspace", Type: "int"},
				{Name: "closed", Type: "[]string"},
			},
			handler: (*Daemon).verbCloseWorkspace,
		},
		"send-keys": {
			scope: scopeWrite,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "keys", Type: "string"},
				{Name: "literal", Type: "bool", Default: "false"},
				{Name: "raw", Type: "bool", Default: "false"},
				{Name: "repeat", Type: "int", Default: "1"},
				typingAllowBlockedParam,
			},
			returns: []verbParam{
				{Name: "sent_to", Type: "string"},
				{Name: "window_id", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "keys", Type: "int"},
			},
			handler: (*Daemon).verbSendKeys,
		},
		"send-text": {
			scope: scopeWrite,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "text", Type: "string"},
				{Name: "paste", Type: "bool", Default: "false"},
				{Name: "submit", Type: "bool", Default: "false"},
				typingAllowBlockedParam,
			},
			handler: (*Daemon).verbSendText,
		},
		"run": {
			// From the shell command and ask-human work. run types a line at a
			// prompt. ask-human asks the person as the caller's own pane, which
			// is a report about that pane, like set-agent-state. answer-ask
			// answers for the person, which no restricted caller does.
			scope: scopeWrite,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "command", Type: "string"},
				{Name: "timeout", Type: "int", Default: "30000"},
				{Name: "lines", Type: "int"},
			},
			returns: []verbParam{
				{Name: "exit_code", Type: "int"},
				{Name: "output", Type: "string"},
				{Name: "truncated", Type: "bool"},
				{Name: "cmdline", Type: "string"},
				{Name: "duration_ms", Type: "int"},
				{Name: "command_seq", Type: "int"},
				{Name: "window", Type: "string"},
				{Name: "session", Type: "string"},
			},
			handler: (*Daemon).verbRun,
		},
		"stream-pane": {
			stream: &verbStream{},
			scope:  scopeRead,
			// stream-pane reads a pane. Its input and lease frames are checked
			// as send-text and resize, frame by frame, so they need write.
			link: needList,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "from_seq", Type: "int"},
				{Name: "boot_id", Type: "string"},
				{Name: "lease_cols", Type: "int"},
				{Name: "lease_rows", Type: "int"},
				{Name: "session_id", Type: "string"},
				{Name: "fit", Type: "bool"},
				{Name: "viewer", Type: "object"},
			},
			returns: []verbParam{
				{Name: "mode", Type: "string", Accepted: []string{"resume", "snapshot"}},
				{Name: "seq", Type: "int"},
				{Name: "cols", Type: "int"},
				{Name: "rows", Type: "int"},
				{Name: "boot_id", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "session_id", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "title", Type: "string"},
			},
			handler: (*Daemon).verbStreamPane,
		},
		"plugin-confirm": {
			// A restricted connection is automation, and gets no dialog.
			scope: scopeDeny,
			// The handler refuses it over a link: a change to a machine's
			// plugins is asked on that machine.
			link: needRelay,
			params: []verbParam{
				{Name: "change", Type: "string", Required: true},
				{Name: "plugin_id", Type: "string"},
				{Name: "path", Type: "string"},
				{Name: "source", Type: "string"},
			},
			returns: []verbParam{
				{Name: "needed", Type: "bool"},
				{Name: "token", Type: "string"},
				{Name: "plugin_id", Type: "string"},
			},
			handler: (*Daemon).verbPluginConfirm,
		},
		"attach-presence": {
			// A restricted connection is automation, never the person.
			scope: scopeDeny,
			// attach-presence only names the person on this connection. What it
			// lets the connection answer is checked by those verbs (respond).
			link: needList,
			params: []verbParam{
				{Name: "session", Type: "string"},
			},
			returns: []verbParam{
				{Name: "human_nonce", Type: "string"},
				{Name: "client_id", Type: "string"},
				{Name: "session", Type: "string"},
			},
			handler: (*Daemon).verbAttachPresence,
		},
		"register-push": {
			// The phones for Web Push are the person's, like attach-presence.
			scope: scopeDeny,
			// A phone registered for Web Push gets what waits for the person, so
			// the push verbs need what answering it needs.
			link: needRespond,
			params: []verbParam{
				{Name: "endpoint", Type: "string"},
				{Name: "p256dh", Type: "string"},
				{Name: "auth", Type: "string"},
				{Name: "device", Type: "string"},
				{Name: "kinds", Type: "string[]"},
				{Name: "human_nonce", Type: "string"},
			},
			returns: []verbParam{
				{Name: "device", Type: "string"},
				{Name: "kinds", Type: "string[]"},
				{Name: "replaced", Type: "bool"},
				{Name: "vapid_public_key", Type: "string"},
				{Name: "machine", Type: "string"},
			},
			handler: (*Daemon).verbRegisterPush,
		},
		"list-push": {
			scope: scopeDeny,
			link:  needRespond,
			params: []verbParam{
				{Name: "human_nonce", Type: "string"},
			},
			returns: []verbParam{
				{Name: "devices", Type: "object[]"},
				{Name: "vapid_public_key", Type: "string"},
				{Name: "machine", Type: "string"},
			},
			handler: (*Daemon).verbListPush,
		},
		"remove-push": {
			scope: scopeDeny,
			link:  needRespond,
			params: []verbParam{
				{Name: "device", Type: "string"},
				{Name: "human_nonce", Type: "string"},
			},
			returns: []verbParam{
				{Name: "device", Type: "string"},
				{Name: "removed", Type: "bool"},
			},
			handler: (*Daemon).verbRemovePush,
		},
		"capture-pane": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "source", Type: "string", Accepted: captureSources, Default: "visible"},
				{Name: "styled", Type: "bool", Default: "false"},
				// scrollback and ansi predate source and styled and are still
				// accepted; they are declared so a caller reading only list-verbs
				// can see the whole call shape.
				{Name: "scrollback", Type: "bool", Default: "false"},
				{Name: "ansi", Type: "bool", Default: "false"},
				{Name: "resolved", Type: "bool", Default: "false"},
				{Name: "palette", Type: "[]string", Default: "xterm defaults"},
				{Name: "lines", Type: "int"},
				{Name: "commands", Type: "int"},
				{Name: "start", Type: "int"},
				{Name: "end", Type: "int"},
				{Name: "unwrap", Type: "bool", Default: "false"},
				{Name: "trim", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "content", Type: "string"},
				{Name: "unwrapped", Type: "bool"},
				{Name: "trimmed", Type: "bool"},
				{Name: "lines_omitted", Type: "int"},
				{Name: "command_marks", Type: "bool"},
				{Name: "commands", Type: "[]object"},
				{Name: "commands_available", Type: "int"},
				{Name: "source", Type: "string", Accepted: captureSources},
				{Name: "styled", Type: "bool"},
				{Name: "resolved", Type: "bool"},
				{Name: "history_rows", Type: "int"},
				{Name: "revision", Type: "int"},
				{Name: "boot_id", Type: "string"},
			},
			handler: (*Daemon).verbCapturePane,
		},
		"clear-history": {
			// Clearing a pane's history changes what every viewer of it sees.
			scope: scopeWrite,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "screen", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "window", Type: "string"},
				{Name: "screen", Type: "bool"},
			},
			handler: (*Daemon).verbClearHistory,
		},
		"screenshot": {
			scope: scopeDeny,
			link:  needList,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "format", Type: "string", Accepted: screenshotFormats, Default: "png"},
				{Name: "frame", Type: "string", Accepted: screenshotFrames, Default: "window"},
				{Name: "theme", Type: "string"},
				{Name: "scrollback", Type: "bool", Default: "false"},
				{Name: "lines", Type: "int"},
				{Name: "cursor", Type: "bool", Default: "false"},
				{Name: "out", Type: "string"},
			},
			returns: []verbParam{
				{Name: "path", Type: "string"},
				{Name: "host", Type: "string"},
				{Name: "format", Type: "string"},
				{Name: "cols", Type: "int"},
				{Name: "rows", Type: "int"},
				{Name: "bytes", Type: "int"},
				{Name: "warnings", Type: "[]string"},
			},
			handler: (*Daemon).verbScreenshot,
		},
		"resize": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "width", Type: "int", Required: true},
				{Name: "height", Type: "int", Required: true},
			},
			handler: (*Daemon).verbResize,
		},
		"kill-session": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				{Name: "session", Type: "string"},
			},
			handler: (*Daemon).verbKillSession,
		},
		"list-options": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				sessionParam,
				{Name: "section", Type: "string"},
				{Name: "prefix", Type: "string"},
			},
			returns: []verbParam{
				{Name: "options", Type: "[]object"},
				{Name: "sections", Type: "[]string"},
				{Name: "total", Type: "int"},
			},
			handler: (*Daemon).verbListOptions,
		},
		"set-option": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "key", Type: "string"},
				{Name: "value", Type: "string"},
				{Name: "no_save", Type: "bool"},
			},
			returns: []verbParam{
				{Name: "key", Type: "string"},
				{Name: "value", Type: "string"},
				{Name: "applied", Type: "bool"},
				{Name: "no_save", Type: "bool"},
				{Name: "reason", Type: "string"},
				{Name: "deprecated", Type: "string"},
			},
			handler: (*Daemon).verbSetOption,
		},
		"list-themes": {
			scope: scopeGlobal,
			link:  needList,
			params: []verbParam{
				sessionParam,
				{Name: "theme", Type: "string"},
				{Name: "filter", Type: "string"},
			},
			returns: []verbParam{
				{Name: "themes", Type: "[]string"},
				{Name: "total", Type: "int"},
				{Name: "matched", Type: "int"},
				{Name: "active", Type: "string"},
				{Name: "active_source", Type: "string", Accepted: []string{"session", "config", "default"}},
				{Name: "themes_dir", Type: "string"},
				{Name: "problems", Type: "[]string"},
				{Name: "palette", Type: "object"},
			},
			handler: (*Daemon).verbListThemes,
		},
		"list-glyphs": {
			scope: scopeGlobal,
			link:  needList,
			params: []verbParam{
				sessionParam,
				{Name: "glyphs", Type: "string"},
			},
			returns: []verbParam{
				{Name: "sets", Type: "[]string"},
				{Name: "roles", Type: "[]string"},
				{Name: "total", Type: "int"},
				{Name: "glyphs_dir", Type: "string"},
				{Name: "active", Type: "string"},
				{Name: "problems", Type: "[]string"},
				{Name: "set", Type: "object"},
			},
			handler: (*Daemon).verbListGlyphs,
		},
		"get-option": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				sessionParam,
				{Name: "key", Type: "string"},
			},
			returns: []verbParam{
				{Name: "key", Type: "string"},
				{Name: "value", Type: "string"},
				{Name: "source", Type: "string", Accepted: []string{"session", "config", "default"}},
				{Name: "default", Type: "string"},
				{Name: "option_type", Type: "string"},
			},
			handler: (*Daemon).verbGetOption,
		},
		"subscribe": {
			stream: &verbStream{},
			scope:  scopeRead,
			link:   needList,
			params: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "types", Type: "[]string", Accepted: knownEventTypes},
				{Name: "queue", Type: "int", Default: "256"},
				{Name: "after_seq", Type: "int"},
				{Name: "boot_id", Type: "string"},
				{Name: "hosts", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "seq", Type: "int"},
				{Name: "boot_id", Type: "string"},
				{Name: "replayed", Type: "int"},
			},
			handler: (*Daemon).verbSubscribe,
		},
		"unsubscribe": {
			scope:   scopeOpen,
			link:    needList,
			handler: (*Daemon).verbUnsubscribe,
		},
		"rename-session": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "name", Type: "string"},
			},
			returns: []verbParam{
				{Name: "type", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "old_name", Type: "string"},
			},
			handler: (*Daemon).verbRenameSession,
		},
		"set-session-name": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "name", Type: "string"},
			},
			handler: (*Daemon).verbSetSessionName,
		},
		"set-session-accent": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "accent", Type: "string"},
			},
			handler: (*Daemon).verbSetSessionAccent,
		},
		"set-note": {
			// The return notes (verb_note.go): a line of text on a session or a
			// pane, written in the caller's own session. Nothing is typed.
			scope: scopeWrite,
			// The return notes (verb_note.go).
			link: needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "note", Type: "string"},
			},
			returns: []verbParam{
				{Name: "type", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "note", Type: "string"},
			},
			handler: (*Daemon).verbSetNote,
		},
		"get-note": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
			},
			returns: []verbParam{
				{Name: "type", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "note", Type: "string"},
			},
			handler: (*Daemon).verbGetNote,
		},
		"set-session-space": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "space", Type: "string"},
				{Name: "global", Type: "bool"},
			},
			returns: []verbParam{
				{Name: "type", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "space", Type: "string"},
				{Name: "global", Type: "bool"},
			},
			handler: (*Daemon).verbSetSessionSpace,
		},
		"set-workspace-name": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "workspace", Type: "int", Required: true},
				{Name: "name", Type: "string"},
			},
			handler: (*Daemon).verbSetWorkspaceName,
		},
		"set-session-order": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				{Name: "order", Type: "[]string", Required: true},
				{Name: "skip_missing", Type: "bool"},
			},
			returns: []verbParam{
				{Name: "type", Type: "string"},
				{Name: "order", Type: "[]string"},
				{Name: "missing", Type: "[]string"},
			},
			handler: (*Daemon).verbSetSessionOrder,
		},
		"set-workspace-order": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "order", Type: "[]int", Required: true},
			},
			handler: (*Daemon).verbSetWorkspaceOrder,
		},
		"set-agent-state": {
			scope: scopeSelf,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "state", Type: "string", Accepted: AgentStateNames},
				{Name: "message", Type: "string"},
				{Name: "source", Type: "string", Accepted: AgentSourceNames, Default: "report"},
				{Name: "harness", Type: "string"},
				{Name: "kind", Type: "string", Accepted: agentKindNames},
				{Name: "agent_session_id", Type: "string"},
				{Name: "transcript_path", Type: "string"},
				{Name: "transcript_leaf", Type: "string"},
				{Name: "if_state", Type: "string", Accepted: AgentStateNames},
				{Name: "subagent_id", Type: "string"},
				{Name: "if_subagent_id", Type: "string"},
				{Name: "notification", Type: "bool"},
				{Name: "harness_pid", Type: "int"},
				{Name: "session_start", Type: "bool"},
				{Name: "agent_cwd", Type: "string"},
				{Name: "activity", Type: "object"},
			},
			returns: []verbParam{
				{Name: "state", Type: "string"},
				{Name: "applied", Type: "bool"},
				{Name: "reason", Type: "string", Accepted: []string{agentRefusedOutranked, agentRefusedIfState, agentRefusedForeignSession, agentRefusedForeignHarness, agentRefusedForeignPane}},
				{Name: "activity_recorded", Type: "bool"},
				{Name: "transcript_refused", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
			},
			handler: (*Daemon).verbSetAgentState,
		},
		"set-agent-session": {
			scope: scopeSelf,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "harness", Type: "string"},
				{Name: "agent_session_id", Type: "string"},
				{Name: "transcript_path", Type: "string"},
				{Name: "transcript_leaf", Type: "string"},
				{Name: "harness_pid", Type: "int"},
			},
			returns: []verbParam{
				{Name: "agent_session_id", Type: "string"},
				{Name: "transcript_refused", Type: "string"},
				{Name: "applied", Type: "bool"},
				{Name: "reason", Type: "string", Accepted: []string{agentRefusedForeignSession, agentRefusedForeignHarness, agentRefusedForeignPane}},
			},
			handler: (*Daemon).verbSetAgentSession,
		},
		"report-agent-activity": {
			// set-agent-state's activity with no state: the same record.
			scope: scopeSelf,
			// set-agent-state's activity with no state, so the same capability.
			link: needWrite,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "activity", Type: "object", Required: true, Accepted: reportActivityEvents},
				{Name: "harness", Type: "string"},
				{Name: "agent_session_id", Type: "string"},
				{Name: "harness_pid", Type: "int"},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "state", Type: "string", Accepted: AgentStateNames},
				{Name: "recorded", Type: "bool"},
				{Name: "reason", Type: "string", Accepted: []string{agentRefusedForeignSession, agentRefusedForeignHarness, agentRefusedForeignPane}},
				{Name: "subagents", Type: "int"},
			},
			handler: (*Daemon).verbReportAgentActivity,
		},
		"resume-agent": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "dry_run", Type: "bool"},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "harness", Type: "string"},
				{Name: "agent_session_id", Type: "string"},
				{Name: "argv", Type: "[]string"},
				{Name: "command", Type: "string"},
				{Name: "typed", Type: "bool"},
			},
			handler: (*Daemon).verbResumeAgent,
		},
		"get-agent-state": {
			scope:  scopeRead,
			link:   needList,
			params: []verbParam{sessionParam, windowParam},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "state", Type: "string", Accepted: AgentStateNames},
				{Name: "reason", Type: "string"},
				{Name: "message", Type: "string"},
				{Name: "agent_state_at", Type: "int"},
				{Name: "source", Type: "string"},
				{Name: "harness_id", Type: "string"},
				identityReturn,
				confidenceReturn,
				evidenceAgeReturn,
				{Name: "needs_you", Type: "bool"},
				{Name: "activity", Type: "string"},
				{Name: "ready", Type: "bool"},
				{Name: "blocked_by", Type: "string"},
				{Name: "waits_on", Type: "string"},
				{Name: "agent_session_id", Type: "string"},
				{Name: "meta", Type: "object"},
				{Name: "queued", Type: "int"},
				{Name: "settled", Type: "bool"},
				{Name: "subagents", Type: "int"},
				subagentListReturn,
			},
			handler: (*Daemon).verbGetAgentState,
		},
		"resolve-pane": {
			scope: scopeDeny,
			link:  needList,
			params: []verbParam{
				{Name: "sid", Type: "int"},
				{Name: "pids", Type: "[]int"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "window_id", Type: "string"},
				{Name: "by", Type: "string", Accepted: []string{"tty", "pid"}},
				{Name: "pid", Type: "int"},
			},
			handler: (*Daemon).verbResolvePane,
		},
		"set-agent-quiet": {
			scope: scopeDeny,
			// It closes Inbox items, so it needs what dismiss-attention needs too.
			link: needWrite | needRespond,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "quiet", Type: "bool", Default: "true"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "window_id", Type: "string"},
				{Name: "quiet", Type: "bool"},
				{Name: "closed", Type: "int"},
			},
			handler: (*Daemon).verbSetAgentQuiet,
		},
		"set-agent-meta": {
			scope: scopeSelf,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "tokens", Type: "object"},
				{Name: "source", Type: "string"},
				{Name: "ttl_ms", Type: "int", Default: "0"},
				{Name: "clear", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "meta", Type: "object"},
				{Name: "truncated", Type: "[]string"},
			},
			handler: (*Daemon).verbSetAgentMeta,
		},
		"report-program-status": {
			// An OSC 7501 report the pane's process could not write to its terminal.
			scope: scopeSelf,
			// An OSC 7501 report, which sets the pane's agent state as set-agent-state does.
			link: needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "report", Type: "string", Required: true},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "program_status", Type: "[]object"},
			},
			handler: (*Daemon).verbReportProgramStatus,
		},
		"explain-agent-detect": {
			scope:  scopeDeny,
			link:   needList,
			params: []verbParam{sessionParam, windowParam},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "verdict", Type: "string"},
				{Name: "evidence", Type: "[]string"},
				{Name: "state", Type: "string", Accepted: AgentStateNames},
				{Name: "source", Type: "string"},
				{Name: "harness_id", Type: "string"},
				{Name: "auto_detected", Type: "bool"},
				identityReturn,
				confidenceReturn,
				evidenceAgeReturn,
				{Name: "running", Type: "bool"},
				{Name: "matched", Type: "bool"},
				{Name: "matched_rule", Type: "string"},
				{Name: "matched_harness", Type: "string"},
				{Name: "matched_via", Type: "[]string"},
				{Name: "name_list", Type: "string"},
				{Name: "note", Type: "string"},
				{Name: "reason", Type: "string"},
				{Name: "group", Type: "[]object"},
				{Name: "process", Type: "object"},
				{Name: "manifests", Type: "[]object"},
				{Name: "ignored", Type: "[]string"},
			},
			handler: (*Daemon).verbExplainAgentDetect,
		},
		"explain-agent-screen": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "harness", Type: "string"},
				{Name: "lines", Type: "int"},
			},
			handler: (*Daemon).verbExplainAgentScreen,
		},
		"wait-for": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				{Name: "condition", Type: "string", Accepted: waitConditions},
				{Name: "session", Type: "string"},
				windowParam,
				{Name: "any_session", Type: "bool", Default: "false"},
				{Name: "pattern", Type: "string"},
				{Name: "source", Type: "string", Accepted: waitOutputSources, Default: "recent"},
				{Name: "new", Type: "bool", Default: "false"},
				{Name: "idle", Type: "int", Default: "500"},
				{Name: "until", Type: "string", Accepted: AgentStateNames},
				{Name: "thread", Type: "int"},
				{Name: "command_seq", Type: "int"},
				{Name: "timeout", Type: "int", Default: "30000"},
				{Name: "select", Type: "string"},
				{Name: "under", Type: "string"},
				{Name: "every", Type: "bool", Default: "false"},
				{Name: "name", Type: "string"},
				{Name: "pid", Type: "int"},
				{Name: "path", Type: "string"},
			},
			returns: []verbParam{
				{Name: "condition", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "exit_code", Type: "int"},
				{Name: "signal", Type: "int"},
				{Name: "new", Type: "bool"},
			},
			handler: (*Daemon).verbWaitFor,
		},
		"list-agents": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				sessionParam,
				{Name: "all", Type: "bool", Default: "false"},
				{Name: "all_sessions", Type: "bool", Default: "false"},
				{Name: "select", Type: "string"},
			},
			returns: []verbParam{
				{Name: "agents", Type: "[]object"},
				{Name: "total", Type: "int"},
				{Name: "select", Type: "string"},
				{Name: "confirm", Type: "string"},
				{Name: "holds", Type: "[]object"},
			},
			handler: (*Daemon).verbListAgents,
		},
		"send-agent-message": {
			scope: scopeMail,
			link:  needMail,
			params: []verbParam{
				sessionParam,
				{Name: "to", Type: "string"},
				{Name: "from", Type: "string"},
				{Name: "from_host", Type: "string"},
				{Name: "subject", Type: "string"},
				{Name: "text", Type: "string"},
				{Name: "parts", Type: "[]object"},
				{Name: "reply_to", Type: "int"},
				{Name: "attachments", Type: "[]string"},
				{Name: "human_nonce", Type: "string"},
				{Name: "host", Type: "string"},
				selectWriteParams[0],
				selectWriteParams[1],
			},
			returns: []verbParam{
				{Name: "results", Type: "[]object"},
				{Name: "sent", Type: "int"},
				{Name: "failed", Type: "int"},
				{Name: "message_id", Type: "int"},
				{Name: "queued", Type: "bool"},
				{Name: "queue_id", Type: "int"},
				{Name: "waiting", Type: "int"},
				{Name: "kind", Type: "string", Accepted: []string{agentMsgDirect, agentMsgNotice}},
				{Name: "to", Type: "string"},
				{Name: "to_name", Type: "string"},
				{Name: "from", Type: "string"},
				{Name: "sent_at", Type: "int"},
				{Name: "reply_to", Type: "int"},
				{Name: "thread_id", Type: "int"},
				{Name: "reply_to_missing", Type: "bool"},
				{Name: "origin", Type: "string"},
				{Name: "origin_host", Type: "string"},
				{Name: "verified_human", Type: "bool"},
				{Name: "claimed_human", Type: "bool"},
				{Name: "held", Type: "bool"},
				{Name: "held_for", Type: "string"},
			},
			handler: (*Daemon).verbSendAgentMessage,
		},
		"release-agent-message": {
			scope: scopeDeny,
			link:  needRespond,
			params: []verbParam{
				sessionParam,
				{Name: "id", Type: "int", Required: true},
				{Name: "human_nonce", Type: "string"},
			},
			returns: []verbParam{
				{Name: "held_id", Type: "int"},
				{Name: "message_id", Type: "int"},
				{Name: "to", Type: "string"},
				{Name: "to_name", Type: "string"},
				{Name: "thread_id", Type: "int"},
			},
			handler: (*Daemon).verbReleaseAgentMessage,
		},
		"list-attention": {
			scope: scopeGlobal,
			link:  needList,
			params: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "kinds", Type: "[]string", Accepted: AttentionKindNames},
				{Name: "host", Type: "string"},
				{Name: "select", Type: "string"},
				{Name: "include_snoozed", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "items", Type: "[]object"},
				{Name: "counts", Type: "object"},
				{Name: "total", Type: "int"},
				{Name: "seq", Type: "int"},
				{Name: "boot_id", Type: "string"},
			},
			handler: (*Daemon).verbListAttention,
		},
		"dismiss-attention": {
			scope: scopeDeny,
			link:  needRespond,
			params: []verbParam{
				{Name: "id", Type: "string"},
				{Name: "human_nonce", Type: "string"},
			},
			returns: []verbParam{
				{Name: "id", Type: "string"},
				{Name: "kind", Type: "string", Accepted: AttentionKindNames},
				{Name: "session", Type: "string"},
				{Name: "host", Type: "string"},
				{Name: "dismissed", Type: "bool"},
			},
			handler: (*Daemon).verbDismissAttention,
		},
		"peek-prompt": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
			},
			returns: []verbParam{
				{Name: "window", Type: "string"},
				{Name: "name", Type: "string"},
				{Name: "harness", Type: "string"},
				{Name: "state", Type: "string"},
				{Name: "state_at", Type: "int"},
				{Name: "waiting_ms", Type: "int"},
				{Name: "blocked", Type: "bool"},
				{Name: "found", Type: "bool"},
				{Name: "answerable", Type: "bool"},
				{Name: "reason", Type: "string"},
				{Name: "source", Type: "string", Accepted: []string{harness.PromptSourceScreen, harness.PromptSourceTitle}},
				{Name: "kind", Type: "string"},
				{Name: "message", Type: "string"},
				{Name: "prompt_id", Type: "string"},
				{Name: "lines", Type: "[]string"},
				{Name: "options", Type: "[]object"},
				{Name: "actions", Type: "[]string", Accepted: harness.AnswerActions},
				{Name: "untrusted", Type: "bool"},
			},
			handler: (*Daemon).verbPeekPrompt,
		},
		"respond": {
			scope: scopeWrite,
			link:  needRespond,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "action", Type: "string", Accepted: harness.AnswerActions},
				{Name: "value", Type: "string"},
				{Name: "prompt_id", Type: "string"},
				{Name: "human_nonce", Type: "string"},
				{Name: "timeout", Type: "int", Default: "5000"},
				{Name: "risk_ack", Type: "[]string"},
			},
			returns: []verbParam{
				{Name: "window", Type: "string"},
				{Name: "action", Type: "string"},
				{Name: "sent", Type: "string"},
				{Name: "prompt_id", Type: "string"},
				{Name: "settled_by", Type: "string", Accepted: []string{respondSettledState, respondSettledPrompt, respondSettledGone, respondSettledTimeout}},
				{Name: "state", Type: "string"},
				{Name: "message", Type: "string"},
				{Name: "by_pane", Type: "string"},
			},
			handler: (*Daemon).verbRespond,
		},
		"request-approval": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "harness", Type: "string"},
				{Name: "options", Type: "[]string", Accepted: approvalDecisions},
				{Name: "summary", Type: "string"},
				{Name: "always_scope", Type: "[]string"},
				{Name: "kind", Type: "string", Accepted: approvalKinds, Default: "approval"},
				{Name: "plan", Type: "string"},
				{Name: "tool", Type: "string"},
				{Name: "target", Type: "string"},
				{Name: "deny_message", Type: "bool", Default: "false"},
				{Name: "cwd", Type: "string"},
			},
			returns: []verbParam{
				{Name: "request_id", Type: "string"},
				{Name: "decision", Type: "string", Accepted: approvalDecisions},
				{Name: "message", Type: "string"},
				{Name: "reason", Type: "string"},
				{Name: "answered_by", Type: "string"},
			},
			handler: (*Daemon).verbRequestApproval,
		},
		"reply-approval": {
			scope: scopeDeny,
			link:  needRespond,
			params: []verbParam{
				{Name: "request_id", Type: "string"},
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "decision", Type: "string", Accepted: approvalReplies},
				{Name: "message", Type: "string"},
				{Name: "human_nonce", Type: "string"},
				{Name: "summary", Type: "string"},
				{Name: "risk_ack", Type: "[]string"},
				{Name: "plan_sha", Type: "string"},
			},
			returns: []verbParam{
				{Name: "request_id", Type: "string"},
				{Name: "decision", Type: "string"},
				{Name: "applied", Type: "bool"},
				{Name: "reason", Type: "string"},
				{Name: "summary", Type: "string"},
				{Name: "answered_by", Type: "string"},
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
			},
			handler: (*Daemon).verbReplyApproval,
		},
		"ask-human": {
			scope: scopeSelf,
			// ask-human's own handler refuses every link caller as well.
			link: needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "question", Type: "string"},
				{Name: "options", Type: "[]string"},
				{Name: "timeout", Type: "int", Default: "120000"},
				{Name: "wait", Type: "bool", Default: "true"},
				{Name: "request_id", Type: "string"},
			},
			returns: []verbParam{
				{Name: "request_id", Type: "string"},
				{Name: "status", Type: "string"},
				{Name: "answer", Type: "string"},
				{Name: "answer_index", Type: "int"},
				{Name: "answered_by", Type: "string"},
				{Name: "verified_human", Type: "bool"},
			},
			handler: (*Daemon).verbAskHuman,
		},
		"answer-ask": {
			scope: scopeDeny,
			link:  needRespond,
			params: []verbParam{
				{Name: "request_id", Type: "string"},
				{Name: "answer", Type: "string"},
				{Name: "human_nonce", Type: "string"},
				{Name: "question", Type: "string"},
				{Name: "upgrade_dialog", Type: "bool"},
			},
			returns: []verbParam{
				{Name: "request_id", Type: "string"},
				{Name: "answer", Type: "string"},
				{Name: "applied", Type: "bool"},
				{Name: "reason", Type: "string"},
				{Name: "answered_by", Type: "string"},
			},
			handler: (*Daemon).verbAnswerAsk,
		},
		"prompt": {
			scope: scopeSelf,
			// prompt's own handler refuses every link caller as well.
			link: needWrite,
			params: []verbParam{
				{Name: "kind", Type: "string"},
				{Name: "title", Type: "string"},
				{Name: "placeholder", Type: "string"},
				{Name: "default", Type: "string"},
				{Name: "items", Type: "[]object"},
				{Name: "multiple", Type: "bool", Default: "false"},
				{Name: "allow_other", Type: "bool", Default: "false"},
				{Name: "timeout", Type: "int", Default: "120000"},
			},
			returns: []verbParam{
				{Name: "status", Type: "string"},
				{Name: "request_id", Type: "string"},
				{Name: "value", Type: "string"},
				{Name: "ids", Type: "[]string"},
				{Name: "other", Type: "string"},
				{Name: "answered_by", Type: "string"},
			},
			handler: (*Daemon).verbPrompt,
		},
		"answer-prompt": {
			scope: scopeDeny,
			link:  needRespond,
			params: []verbParam{
				{Name: "request_id", Type: "string"},
				{Name: "human_nonce", Type: "string"},
				{Name: "cancel", Type: "bool"},
				{Name: "value", Type: "string"},
				{Name: "ids", Type: "[]string"},
				{Name: "other", Type: "string"},
			},
			returns: []verbParam{
				{Name: "request_id", Type: "string"},
				{Name: "applied", Type: "bool"},
				{Name: "status", Type: "string"},
			},
			handler: (*Daemon).verbAnswerPrompt,
		},
		"read-agent-messages": {
			scope: scopeRead,
			link:  needMail,
			params: []verbParam{
				sessionParam,
				{Name: "to", Type: "string"},
				{Name: "unread", Type: "bool", Default: "false"},
				{Name: "notices", Type: "bool", Default: "false"},
				{Name: "peek", Type: "bool", Default: "false"},
				{Name: "thread", Type: "int"},
				{Name: "limit", Type: "int", Default: "20"},
				{Name: "archive", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "messages", Type: "[]object"},
				{Name: "thread", Type: "int"},
				{Name: "untrusted", Type: "bool"},
				{Name: "unread", Type: "int"},
				{Name: "seen", Type: "int"},
				{Name: "marked_read", Type: "int"},
				{Name: "marked_seen", Type: "int"},
				{Name: "total", Type: "int"},
				{Name: "evicted", Type: "int"},
				{Name: "evicted_unread", Type: "int"},
				{Name: "peek_forced", Type: "bool"},
				{Name: "archive", Type: "bool"},
			},
			handler: (*Daemon).verbReadAgentMessages,
		},
		"agent-mail-notice": {
			// It marks the pane's own mail announced, so it is the pane's record.
			scope: scopeSelf,
			link:  needMail,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "reset", Type: "bool", Default: "false"},
				{Name: "lease", Type: "bool", Default: "false"},
				{Name: "commit", Type: "int"},
				{Name: "harness", Type: "string"},
				{Name: "agent_session_id", Type: "string"},
				{Name: "harness_pid", Type: "int"},
			},
			returns: []verbParam{
				{Name: "window_id", Type: "string"},
				{Name: "unread", Type: "int"},
				{Name: "notice", Type: "string"},
				{Name: "lease", Type: "int"},
				{Name: "committed", Type: "bool"},
				{Name: "reason", Type: "string", Accepted: []string{agentRefusedForeignSession, agentRefusedForeignHarness, agentRefusedForeignPane}},
			},
			handler: (*Daemon).verbAgentMailNotice,
		},
		"ask-agent": {
			scope: scopeWrite,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "from", Type: "string"},
				{Name: "from_host", Type: "string"},
				{Name: "text", Type: "string"},
				{Name: "ready_timeout", Type: "int", Default: "30000"},
				{Name: "settle", Type: "int", Default: "2000"},
				{Name: "timeout", Type: "int", Default: "300000"},
				{Name: "lines", Type: "int", Default: "200"},
				{Name: "force", Type: "bool", Default: "false"},
				{Name: "allow_blocked", Type: "bool", Default: "false"},
				{Name: "stall_timeout", Type: "int", Default: "5000"},
				selectWriteParams[0],
				selectWriteParams[1],
			},
			returns: []verbParam{
				{Name: "replies", Type: "[]object"},
				{Name: "answered", Type: "int"},
				{Name: "failed", Type: "int"},
				{Name: "window", Type: "string"},
				{Name: "waited_for", Type: "string"},
				{Name: "queued_behind", Type: "int"},
				{Name: "waited_turn_ms", Type: "int"},
				{Name: "settled_by", Type: "string", Accepted: []string{"agent-state", "idle", "timeout", "window-closed", "session-closed", "shutdown"}},
				{Name: "state", Type: "string"},
				{Name: "untrusted", Type: "bool"},
				{Name: "reply", Type: "string"},
				{Name: "lines", Type: "int"},
				{Name: "truncated", Type: "bool"},
			},
			handler: (*Daemon).verbAskAgent,
		},

		// The session stash. Two verbs and one contiguous block, because the
		// store's whole surface is put and list; see stash.go for why there is
		// no get and no delete.
		"stash-put": {
			scope: scopeMail,
			link:  needMail,
			params: []verbParam{
				sessionParam,
				{Name: "path", Type: "string"},
				{Name: "content", Type: "string"},
			},
			returns: []verbParam{
				{Name: "path", Type: "string"},
				{Name: "hash", Type: "string"},
				{Name: "bytes", Type: "int"},
				{Name: "media_type", Type: "string"},
				{Name: "kind", Type: "string", Accepted: []string{"image", "file"}},
				{Name: "stored_at", Type: "int"},
				{Name: "deduped", Type: "bool"},
				{Name: "evicted", Type: "int"},
				{Name: "evictions", Type: "int"},
				{Name: "session_bytes", Type: "int"},
				{Name: "session_entries", Type: "int"},
				{Name: "max_file_bytes", Type: "int"},
				{Name: "max_bytes", Type: "int"},
			},
			handler: (*Daemon).verbStashPut,
		},
		"stash-list": {
			scope: scopeRead,
			link:  needMail,
			params: []verbParam{
				sessionParam,
			},
			returns: []verbParam{
				{Name: "dir", Type: "string"},
				{Name: "entries", Type: "[]object"},
				{Name: "total", Type: "int"},
				{Name: "bytes", Type: "int"},
				{Name: "evicted", Type: "int"},
				{Name: "max_file_bytes", Type: "int"},
				{Name: "max_bytes", Type: "int"},
			},
			handler: (*Daemon).verbStashList,
		},
		"stash-get": {
			scope: scopeRead,
			link:  needMail,
			params: []verbParam{
				sessionParam,
				{Name: "path", Type: "string"},
			},
			returns: []verbParam{
				{Name: "path", Type: "string"},
				{Name: "name", Type: "string"},
				{Name: "hash", Type: "string"},
				{Name: "bytes", Type: "int"},
				{Name: "media_type", Type: "string"},
				{Name: "kind", Type: "string", Accepted: []string{"image", "file"}},
				{Name: "content", Type: "string"},
			},
			handler: (*Daemon).verbStashGet,
		},
	}
	// The verbs of the agent review, triage, queue and approval work, kept in
	// a file of their own. See verb_protocol_agents.go.
	maps.Copy(table, agentWorkVerbs())
	maps.Copy(table, taskVerbs())
	// exec, what tuios run --host sends. See verb_exec.go.
	maps.Copy(table, execVerbs())
	// The sender's view of its mail. See verb_receipts.go.
	maps.Copy(table, receiptVerbs())
	maps.Copy(table, holdVerbs())
	maps.Copy(table, taskVerifyVerbs())
	maps.Copy(table, taskDispatchVerbs(verbParam{Name: "human_nonce", Type: "string"}))
	// The checkpoint verbs. See verb_checkpoint.go.
	maps.Copy(table, checkpointVerbs())
	// The ship verbs. See verb_ship.go.
	maps.Copy(table, shipVerbs())
	// The paste buffer verbs. See verb_buffers.go.
	maps.Copy(table, bufferVerbs())
	// The file verbs, transfers, the drop folder and previews. See
	// verb_files.go, transfer.go, drop_files.go and verb_preview.go.
	maps.Copy(table, fileVerbs())
	maps.Copy(table, transferVerbs())
	maps.Copy(table, dropVerbs())
	maps.Copy(table, previewVerbs())
	maps.Copy(table, previewRunVerbs())
	// The tree stream, the check-ahead and the link speed test. See
	// transfer_tree.go and transfer_speed.go.
	maps.Copy(table, treeVerbs())
	maps.Copy(table, speedVerbs())
	// search-panes, command-history and list-ports. See verb_find.go and
	// verb_ports.go.
	maps.Copy(table, findVerbs())
	// record-pane and the verbs around it. See pane_record.go.
	maps.Copy(table, recordVerbs())
	return table
}

// detectJSONClient inspects the first byte of the connection without consuming
// it. A JSON verb-protocol client's first byte is '{' or leading whitespace; a
// binary client's is the high byte of a big-endian length prefix (0x00/0x01 for
// any sub-16MB frame), so the two never collide. It returns true when the
// connection should be handled as JSON. When nothing arrives within
// firstByteDeadline it returns errFirstByteTimeout, and the caller closes the
// connection. On any other read error it returns false and lets the (short)
// binary path observe the same error and clean up.
func (d *Daemon) detectJSONClient(cs *connState, br *bufio.Reader) (bool, error) {
	select {
	case <-d.ctx.Done():
		return false, nil
	case <-cs.done:
		return false, nil
	default:
	}
	// One deadline for the first byte, with no poll: shutdown and drop close
	// the connection, which ends the wait. Every client speaks first, so a
	// connection that stays silent this long is closed and gives its slot
	// back. See frame_budget.go.
	_ = cs.conn.SetReadDeadline(time.Now().Add(firstByteDeadline))
	peeked, err := br.Peek(1)
	_ = cs.conn.SetReadDeadline(time.Time{})
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return false, errFirstByteTimeout
		}
		// EOF or hard error: not JSON; the binary loop will re-observe it.
		return false, nil
	}
	return isJSONStart(peeked[0]), nil
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
	// A buffer upload the connection did not finish goes with it.
	defer d.dropUploads(cs)

	// Each line is memory of its own, bounded and charged to the daemon's
	// budget when it is large. See verb_lines.go.
	lr := &verbLineReader{d: d, cs: cs, br: br}
	defer lr.done()

	for {
		raw, err := lr.next()
		if err != nil {
			// The rest of the line cannot be read as a request, so the
			// connection ends after the answer.
			switch {
			case errors.Is(err, errVerbLineBusy):
				_ = d.writeVerbError(cs, nil, "", newVerbError(ErrVerbBusy, err.Error()+"; nothing was done. Try again."))
			case errors.Is(err, errVerbLineTooLong):
				_ = d.writeVerbError(cs, nil, "", newVerbError(ErrVerbInvalidRequest, err.Error()+"; nothing was done"))
			}
			return
		}
		select {
		case <-d.ctx.Done():
			return
		case <-cs.done:
			return
		default:
		}

		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		// The line is the reader's own copy, so a routed verb that blocks
		// (routeToTUISync) may keep a reference to its params.
		err = d.dispatchVerbLine(cs, line, lr.done)
		if err != nil {
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
//
// release, when not nil, gives back the read budget the line holds. It is
// called once the request envelope is decoded, before the verb runs, so a
// verb that waits (a send-text into a pane that does not read, a routed verb)
// does not hold the budget while it waits. See verb_lines.go.
func (d *Daemon) dispatchVerbLine(cs *connState, line []byte, release func()) error {
	// A verb can change what the session listing shows.
	d.wakes.listing.Set()
	var req verbRequest
	err := jsonx.Unmarshal(line, &req)
	if release != nil {
		release()
	}
	if err != nil {
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

	cs.verbCall.begin(req.ID, req.Verb)
	result, verr := entry.handler(d, cs, req.Params)
	if !cs.verbCall.end() {
		// A live upgrade answered the call while it ran, and is closing the
		// connection.
		return errVerbAnswered
	}
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
	entry, ok := verbRegistry()[verb]
	if !ok {
		known := knownVerbNames()
		return verbEntry{}, nil, hintedVerbError(ErrVerbUnknownVerb, "unknown verb "+echoName(verb), &VerbHint{
			Verb:       "list-verbs",
			Command:    "tuios list-verbs",
			DidYouMean: closestMatch(verb, known),
			Available:  known,
			Detail:     "Call list-verbs for every verb with its parameter schema and examples.",
		})
	}

	if verr := checkParamNames(verb, entry, params); verr != nil {
		return verbEntry{}, nil, verr
	}

	// An agent verb while the agent features are off. See agents_switch.go.
	if verr := d.agentsOffRefusal(verb, params); verr != nil {
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
	// Follow the pane's own stale session name before a rename alias can
	// expand it to another session. See session_rename.go.
	params = d.followOwnPane(cs, verb, params)
	// A session named by a name it was renamed from is named by its current
	// one from here on. See session_rename.go.
	params = d.followRenamedSession(verb, params)
	// A window moved out of the session named goes to the one it is in now.
	// See window_move_session.go.
	params = d.followMovedWindow(params)
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
	raw, err := jsonx.Marshal(params)
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
	if err := jsonx.Unmarshal(params, &got); err != nil {
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
	data, err := jsonx.Marshal(resp)
	if err != nil {
		// Should not happen; fall back to a minimal internal error line.
		data = []byte(`{"error":{"code":"internal","message":"failed to encode response"}}`)
	}
	data = append(escapeC1(data), '\n')

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
		entry, ok := verbRegistry()[p.Verb]
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
		verbs = append(verbs, describeVerb(name, verbRegistry()[name]))
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

// VerbDocs returns every verb as list-verbs describes it, sorted by name. It
// is read from the table the daemon dispatches from, so a program in this
// binary that builds a schema from it (tuios mcp) describes the same verbs,
// with the same parameters, as the daemon of the same build serves.
func VerbDocs() []VerbDoc {
	names := knownVerbNames()
	out := make([]VerbDoc, 0, len(names))
	for _, name := range names {
		out = append(out, describeVerb(name, verbRegistry()[name]))
	}
	return out
}

// describeVerb renders one registry entry as its documented form: the schema
// from the registry and the text from the verb docs.
func describeVerb(name string, entry verbEntry) verbDoc {
	text := verbDocTexts()[name]
	params := withDescriptions(entry.params, text.Params)
	if params == nil {
		params = []verbParam{}
	}
	return verbDoc{
		Verb:        name,
		Description: text.Description,
		Params:      params,
		Returns:     withDescriptions(entry.returns, text.Returns),
		Examples:    text.Examples,
		Stream:      entry.stream,
	}
}

// VerbHoldsConnection reports whether a call of verb with params keeps the
// connection after its reply, as the verb's doc's HoldsConnection does. It
// reads only the registry, so a caller that checks every call does not load
// the verb docs.
func VerbHoldsConnection(verb string, params map[string]any) bool {
	entry, ok := verbRegistry()[verb]
	return ok && verbDoc{Stream: entry.stream}.HoldsConnection(params)
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
		Client    string `json:"client"`
		Version   string `json:"version"`
		Protocol  int    `json:"protocol"`
		Term      string `json:"term"`
		ColorTerm string `json:"color_term"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	// Recorded as the binary protocol's hello records it, so new-session
	// gives a session this connection creates the caller's terminal. The
	// shell is left to the daemon, as every hello leaves it.
	cs.mu.Lock()
	cs.hello = &HelloPayload{Term: helloTermValue(p.Term), ColorTerm: helloTermValue(p.ColorTerm)}
	cs.mu.Unlock()

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

	infos := d.manager.ListSessions()
	res := map[string]any{
		"type":           "hello",
		"protocol":       VerbProtocolVersion,
		"min_protocol":   MinVerbProtocolVersion,
		"daemon_version": d.version,
		"pid":            os.Getpid(),
		"instance":       d.instance,
		"sessions":       len(infos),
		// load is this machine's load, which a linked daemon reads off the
		// hello its heartbeat sends anyway. See machine_load.go.
		"load": machineLoad(infos),
		// link_policy says this daemon holds links to a policy, so a proxy
		// must reach it on a link socket and never on this one. See
		// DialForLink in link_dial.go.
		"link_policy": true,
		// pane_grants says this daemon holds calls from panes to their
		// grants and takes a pane's token with pane-grants, which the CLI
		// presents where the kernel cannot name the caller's pane.
		"pane_grants": true,
		// os is the daemon's GOOS. A client attached from another machine
		// builds a command line for this one: sh -c here, cmd /C on Windows.
		"os": runtime.GOOS,
		// handoff is the live handoff manifest version this image writes,
		// which the binary tuios daemon upgrade names must read. The CLI
		// running the upgrade may be of another build.
		"handoff": HandoffVersion,
	}
	// mail_waiting is the mail this daemon keeps for the machine a link
	// connection named, while its own link there is down. See held_mail.go.
	if held := d.mailWaitingFor(cs); held != nil {
		res["mail_waiting"] = held
	}
	// upgrade says this image took its panes over in a live upgrade, and
	// what that measured. daemon upgrade waits for it.
	if st := d.lastHandoff.Load(); st != nil {
		res["upgrade"] = st
	}
	return res, nil
}

// maxHelloTermLen bounds a TERM or COLORTERM a hello names. Real names are a
// few dozen bytes at most.
const maxHelloTermLen = 64

// helloTermValue is v, a TERM or COLORTERM a hello named, when it is short
// printable ASCII with no spaces, and empty otherwise. It goes into the
// environment of every pane of a session the connection creates, and only
// PaneTerm on Unix checks TERM after this, so a link peer could otherwise put
// control characters or a long string there. An empty value gives the
// daemon's fallback. It is dropped rather than refused, because a refused
// hello would fail every command of that user.
func helloTermValue(v string) string {
	if len(v) > maxHelloTermLen || !guestenv.ValidTermName(v) {
		return ""
	}
	for i := range len(v) {
		if v[i] > '~' {
			return ""
		}
	}
	return v
}
