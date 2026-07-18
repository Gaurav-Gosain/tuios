package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
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
// one-line description, the parameter schema, and copy-pasteable examples.
type verbEntry struct {
	description string
	params      []verbParam
	examples    []string
	handler     verbHandler
}

// verbDoc is the serialized form of a verbEntry in the list-verbs result.
type verbDoc struct {
	Verb        string      `json:"verb"`
	Description string      `json:"description"`
	Params      []verbParam `json:"params"`
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
			description: "Create a new window.",
			params: []verbParam{
				sessionParam,
				{Name: "name", Type: "string", Description: "Name for the new window. Omit to use the shell's title."},
			},
			examples: []string{`{"id":1,"verb":"new-window","params":{"session":"work","name":"build"}}`},
			handler:  (*Daemon).verbNewWindow,
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
				{Name: "lines", Type: "int", Description: "Keep only the last N lines. Ignored when start or end is given."},
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
		"set-option": {
			description: "Set a session option, applied live when a client is attached.",
			params: []verbParam{
				sessionParam,
				{Name: "key", Type: "string", Required: true, Description: `Option path, e.g. "appearance.dockbar_position".`},
				{Name: "value", Type: "string", Description: "New value, as a string."},
			},
			examples: []string{`{"id":1,"verb":"set-option","params":{"session":"work","key":"appearance.dockbar_position","value":"top"}}`},
			handler:  (*Daemon).verbSetOption,
		},
		"get-option": {
			description: "Read a session option previously set with set-option.",
			params: []verbParam{
				sessionParam,
				{Name: "key", Type: "string", Required: true, Description: "Option path to read."},
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
		"wait-for": {
			description: "Block until a condition matches, or fail with the timeout code.",
			params: []verbParam{
				{Name: "condition", Type: "string", Required: true, Description: "Condition to wait for.", Accepted: waitConditions},
				sessionParam,
				windowParam,
				{Name: "pattern", Type: "string", Description: "Regular expression, required by window-output."},
				{Name: "idle", Type: "int", Description: "Milliseconds of silence that count as idle, for window-idle.", Default: "500"},
				{Name: "timeout", Type: "int", Description: "Milliseconds to wait before failing with the timeout code.", Default: "30000"},
			},
			examples: []string{`{"id":1,"verb":"wait-for","params":{"condition":"window-output","session":"work","pattern":"done","timeout":10000}}`},
			handler:  (*Daemon).verbWaitFor,
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
			Error: hintedVerbError(ErrVerbUnknownVerb, "unknown verb "+req.Verb, &VerbHint{
				Verb:       "list-verbs",
				Command:    "tuios list-verbs",
				DidYouMean: closestMatch(req.Verb, known),
				Available:  known,
				Detail:     "Call list-verbs for every verb with its parameter schema and examples.",
			}),
		})
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
			return nil, hintedVerbError(ErrVerbUnknownVerb, "unknown verb "+p.Verb, &VerbHint{
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
