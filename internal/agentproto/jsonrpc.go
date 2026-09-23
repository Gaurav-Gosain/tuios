// Package agentproto runs a coding agent headless, over a structured protocol,
// and shows it in a tuios pane.
//
// An agent TUI in a pane is read by its screen, its hooks and its transcript
// file. Some agents also speak a protocol meant for exactly this: a client
// starts the agent with pipes, sends it prompts, and gets its turns, tool calls,
// diffs and permission requests as messages. This package is such a client for
// two of them:
//
//   - ACP, the Agent Client Protocol (agentclientprotocol.com), protocol
//     version 1, which Zed and others use and which many agents implement
//     (opencode acp, Gemini CLI, adapters for Claude Code and Codex).
//   - The Codex app-server protocol (codex app-server), its v2 thread and turn
//     methods.
//
// Both are JSON-RPC 2.0 over the agent's stdin and stdout, one message per
// line. The Codex app-server leaves out the "jsonrpc" member, and reads a
// message without it.
//
// The client offers the agent nothing beyond the conversation: it advertises no
// file system and no terminal capability, so the agent cannot ask tuios to read
// a file, write one or run a command, and a request for anything the client
// does not handle is answered with method not found. What the agent does, it
// does itself, under its own sandbox and approval rules, exactly as it would in
// its own TUI.
//
// The Session in session.go is the pane program: it renders the conversation as
// a transcript, reads prompts from the keyboard, and reports the agent's state
// and permission requests to the daemon through a Reporter.
package agentproto

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// maxMessage bounds one JSON-RPC message. A diff of a large file arrives in one
// message, so the bound is generous; a line past it ends the connection rather
// than growing without limit.
const maxMessage = 32 << 20

// JSON-RPC error codes the client answers with.
const (
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// RPCError is an error a peer answered a request with.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
}

// ErrClosed is returned by a call on a connection that has ended.
var ErrClosed = errors.New("the agent's connection closed")

// message is any JSON-RPC message, decoded far enough to route it.
type message struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Request is a request the agent sent the client. Exactly one of Reply or
// ReplyError answers it; a second answer is dropped.
type Request struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
	conn   *Conn
	once   sync.Once
}

// Reply answers the request with result.
func (r *Request) Reply(result any) {
	raw := mustJSON(result)
	if raw == nil {
		// A result is required in a response, and null is the empty one.
		raw = json.RawMessage("null")
	}
	r.once.Do(func() { _ = r.conn.send(message{ID: r.ID, Result: raw}) })
}

// ReplyError answers the request with an error.
func (r *Request) ReplyError(code int, msg string) {
	r.once.Do(func() { _ = r.conn.send(message{ID: r.ID, Error: &RPCError{Code: code, Message: msg}}) })
}

// Conn is one JSON-RPC connection over a pair of streams.
//
// Notifications and requests from the peer are handed to the handlers in the
// order they arrive, on the connection's read goroutine, so a handler must not
// block: one that waits for a person answers from another goroutine.
type Conn struct {
	w           io.Writer
	wmu         sync.Mutex
	omitVersion bool

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan message
	err     error
	done    chan struct{}

	onNotify  func(method string, params json.RawMessage)
	onRequest func(*Request)
}

// ConnOptions configure a Conn.
type ConnOptions struct {
	// OmitVersion leaves the "jsonrpc" member out of what is sent, for the
	// Codex app-server's JSON-RPC without it.
	OmitVersion bool
	// OnNotify receives each notification.
	OnNotify func(method string, params json.RawMessage)
	// OnRequest receives each request. Nil answers every request with method
	// not found.
	OnRequest func(*Request)
}

// NewConn starts a connection reading r and writing w. It reads until r ends
// or fails, and then fails every call still waiting.
func NewConn(r io.Reader, w io.Writer, o ConnOptions) *Conn {
	c := &Conn{
		w:           w,
		omitVersion: o.OmitVersion,
		pending:     make(map[string]chan message),
		done:        make(chan struct{}),
		onNotify:    o.OnNotify,
		onRequest:   o.OnRequest,
	}
	go c.readLoop(r)
	return c
}

// Done is closed when the connection has ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the connection ended, nil while it runs.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Call sends a request and decodes its result into out, which may be nil.
func (c *Conn) Call(ctx context.Context, method string, params, out any) error {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return ErrClosed
	}
	c.nextID++
	id := strconv.FormatInt(c.nextID, 10)
	ch := make(chan message, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.send(message{ID: json.RawMessage(id), Method: method, Params: mustJSON(params)}); err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 {
			if err := json.Unmarshal(m.Result, out); err != nil {
				return fmt.Errorf("reading the answer to %s: %w", method, err)
			}
		}
		return nil
	case <-c.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	return c.send(message{Method: method, Params: mustJSON(params)})
}

func (c *Conn) send(m message) error {
	if !c.omitVersion {
		m.JSONRPC = "2.0"
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(data)
	return err
}

func (c *Conn) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxMessage)
	var err error
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m message
		if json.Unmarshal(line, &m) != nil {
			// A line that is not JSON is not a message: some agents print a
			// banner or a log line on stdout. It is skipped.
			continue
		}
		c.dispatch(m)
	}
	if err = sc.Err(); err == nil {
		err = io.EOF
	}
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
	close(c.done)
}

func (c *Conn) dispatch(m message) {
	hasID := len(m.ID) > 0 && string(m.ID) != "null"
	switch {
	case m.Method != "" && hasID:
		req := &Request{ID: m.ID, Method: m.Method, Params: m.Params, conn: c}
		if c.onRequest == nil {
			req.ReplyError(codeMethodNotFound, "method not found: "+m.Method)
			return
		}
		c.onRequest(req)
	case m.Method != "":
		if c.onNotify != nil {
			c.onNotify(m.Method, m.Params)
		}
	case hasID:
		c.mu.Lock()
		ch := c.pending[idKey(m.ID)]
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

// idKey is an id as the pending map keys it: the number or the string, so a
// peer that echoes 7 as "7" is still matched.
func idKey(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(bytes.TrimSpace(raw))
}

func mustJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}
