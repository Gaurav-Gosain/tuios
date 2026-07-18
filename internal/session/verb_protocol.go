package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
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
	ErrVerbInternal        = "internal"          // unexpected server-side failure
)

// verbRequest is one decoded request line. ID is opaque (number, string, or
// absent) and echoed back on the response.
type verbRequest struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Verb   string          `json:"verb"`
	Params json.RawMessage `json:"params,omitempty"`
}

// verbError is the error envelope with a stable string code.
type verbError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
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

// verbEntry pairs a handler with a one-line description for list-verbs.
type verbEntry struct {
	description string
	handler     verbHandler
}

// verbRegistry is the dispatch table for every JSON verb the daemon supports.
// It is built once at package init so list-verbs and dispatch share one source
// of truth. It is populated in init() to avoid a static initialization cycle
// (list-verbs reads the registry).
var verbRegistry map[string]verbEntry

func init() {
	verbRegistry = map[string]verbEntry{
		"list-verbs": {
			description: "List every supported verb and the protocol version.",
			handler:     (*Daemon).verbListVerbs,
		},
		"list-sessions": {
			description: "List all sessions the daemon holds.",
			handler:     (*Daemon).verbListSessions,
		},
		"session-info": {
			description: "Report details about one session (params: session).",
			handler:     (*Daemon).verbSessionInfo,
		},
		"list-windows": {
			description: "List the windows in a session (params: session).",
			handler:     (*Daemon).verbListWindows,
		},
		"new-window": {
			description: "Create a new window (params: session, name).",
			handler:     (*Daemon).verbNewWindow,
		},
		"close-window": {
			description: "Close a window (params: session, window).",
			handler:     (*Daemon).verbCloseWindow,
		},
		"send-keys": {
			description: "Send parsed key tokens to a window (params: session, window, keys, literal, raw).",
			handler:     (*Daemon).verbSendKeys,
		},
		"send-text": {
			description: "Send literal text to a window's PTY (params: session, window, text).",
			handler:     (*Daemon).verbSendText,
		},
		"capture-pane": {
			description: "Capture a pane's content (params: session, window, source, styled, lines, start, end).",
			handler:     (*Daemon).verbCapturePane,
		},
		"resize": {
			description: "Resize a window's PTY (params: session, window, width, height).",
			handler:     (*Daemon).verbResize,
		},
		"kill-session": {
			description: "Terminate a session (params: session).",
			handler:     (*Daemon).verbKillSession,
		},
		"set-option": {
			description: "Set a session option (params: session, key, value).",
			handler:     (*Daemon).verbSetOption,
		},
		"get-option": {
			description: "Read a session option (params: session, key).",
			handler:     (*Daemon).verbGetOption,
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
			ID:    req.ID,
			Error: newVerbError(ErrVerbInvalidRequest, "request is missing the \"verb\" field"),
		})
	}

	entry, ok := verbRegistry[req.Verb]
	if !ok {
		return d.writeVerbResponse(cs, &verbResponse{
			ID:    req.ID,
			Error: newVerbError(ErrVerbUnknownVerb, "unknown verb "+req.Verb),
		})
	}

	result, verr := entry.handler(d, cs, req.Params)
	if verr != nil {
		return d.writeVerbResponse(cs, &verbResponse{ID: req.ID, Error: verr})
	}
	return d.writeVerbResponse(cs, &verbResponse{ID: req.ID, Result: result})
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

// verbListVerbs implements the list-verbs introspection verb.
func (d *Daemon) verbListVerbs(_ *connState, _ json.RawMessage) (any, *verbError) {
	verbs := make([]map[string]string, 0, len(verbRegistry))
	for name, entry := range verbRegistry {
		verbs = append(verbs, map[string]string{
			"verb":        name,
			"description": entry.description,
		})
	}
	// Stable order so output is deterministic.
	sortVerbEntries(verbs)
	return map[string]any{
		"type":    "verb_list",
		"version": VerbProtocolVersion,
		"verbs":   verbs,
	}, nil
}
