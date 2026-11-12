package acp

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

// JSON-RPC and ACP error codes.
const (
	CodeInvalidRequest             = -32600
	CodeMethodNotFound             = -32601
	CodeInvalidParams              = -32602
	CodeRequestCancelled           = -32800
	CodeAuthRequired               = -32000
	CodeResourceNotFound           = -32002
	CodeUnsupportedProtocolVersion = -32022
)

// methodCancelRequest is the notification either side sends to cancel a
// request it sent.
const methodCancelRequest = "$/cancel_request"

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

// Request is a request the peer sent. Exactly one of Reply or ReplyError
// answers it; a second answer is dropped.
type Request struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
	conn   *Conn
	once   sync.Once
	// cancelled is closed when the peer cancels the request with
	// $/cancel_request, or the connection ends, before it is answered.
	cancelled chan struct{}
	cancelOne sync.Once
}

// Reply answers the request with result.
func (r *Request) Reply(result any) {
	raw := mustJSON(result)
	if raw == nil {
		// A result is required in a response, and null is the empty one.
		raw = json.RawMessage("null")
	}
	r.answer(message{ID: r.ID, Result: raw})
}

// ReplyError answers the request with an error.
func (r *Request) ReplyError(code int, msg string) {
	r.answer(message{ID: r.ID, Error: &RPCError{Code: code, Message: msg}})
}

func (r *Request) answer(m message) {
	r.once.Do(func() {
		r.conn.forget(r)
		_ = r.conn.send(m)
	})
}

// Cancelled is closed when the peer withdraws the request before it is
// answered, with $/cancel_request or by ending the connection. The request
// must still be answered: ACP wants a response for every request, even a
// cancelled one.
func (r *Request) Cancelled() <-chan struct{} { return r.cancelled }

func (r *Request) cancel() { r.cancelOne.Do(func() { close(r.cancelled) }) }

// Conn is one JSON-RPC connection over a pair of streams.
//
// Notifications and requests from the peer are handed to the handlers in the
// order they arrive, on the connection's read goroutine, so a handler must not
// block: one that waits for a person answers from another goroutine.
type Conn struct {
	w           io.Writer
	wmu         sync.Mutex
	omitVersion bool
	cancels     bool

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan message
	open    map[string]*Request
	err     error
	done    chan struct{}

	onNotify  func(method string, params json.RawMessage)
	onRequest func(*Request)
}

// ConnOptions configure a Conn.
type ConnOptions struct {
	// OmitVersion leaves the "jsonrpc" member out of what is sent, for a peer
	// that speaks JSON-RPC without it (the Codex app-server).
	OmitVersion bool
	// Cancels turns on ACP's $/cancel_request: a call whose context ends is
	// cancelled at the peer, and a cancel from the peer closes the request's
	// Cancelled channel. The notification is not handed to OnNotify.
	Cancels bool
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
		cancels:     o.Cancels,
		pending:     make(map[string]chan message),
		open:        make(map[string]*Request),
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
			if err := unmarshal(m.Result, out); err != nil {
				return errorf("reading the answer to %s: %w", method, err)
			}
		}
		return nil
	case <-c.done:
		return ErrClosed
	case <-ctx.Done():
		if c.cancels {
			// The caller stopped waiting: tell the peer, so it can stop
			// the work and drop any question it shows for it.
			_ = c.Notify(methodCancelRequest, map[string]json.RawMessage{"requestId": json.RawMessage(id)})
		}
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
	data, err := marshal(m)
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
		if line[0] == '[' {
			// ACP uses no JSON-RPC batches: one Invalid Request answers an
			// array, with a null id.
			_ = c.send(message{ID: json.RawMessage("null"), Error: &RPCError{Code: CodeInvalidRequest, Message: "batches are not used"}})
			continue
		}
		var m message
		if unmarshal(line, &m) != nil {
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
	open := c.open
	c.open = map[string]*Request{}
	c.mu.Unlock()
	for _, r := range open {
		r.cancel()
	}
	close(c.done)
}

func (c *Conn) dispatch(m message) {
	hasID := len(m.ID) > 0 && string(m.ID) != "null"
	switch {
	case m.Method != "" && hasID:
		req := &Request{ID: m.ID, Method: m.Method, Params: m.Params, conn: c, cancelled: make(chan struct{})}
		if c.onRequest == nil {
			req.ReplyError(CodeMethodNotFound, "method not found: "+m.Method)
			return
		}
		if c.cancels {
			c.mu.Lock()
			c.open[idKey(m.ID)] = req
			c.mu.Unlock()
		}
		c.onRequest(req)
	case m.Method == methodCancelRequest && c.cancels:
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if unmarshal(m.Params, &p) != nil || len(p.RequestID) == 0 {
			return
		}
		// An id that is unknown or already answered is ignored.
		c.mu.Lock()
		req := c.open[idKey(p.RequestID)]
		c.mu.Unlock()
		if req != nil {
			req.cancel()
		}
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

// forget drops an answered request from the open set.
func (c *Conn) forget(r *Request) {
	if !c.cancels {
		return
	}
	c.mu.Lock()
	if c.open[idKey(r.ID)] == r {
		delete(c.open, idKey(r.ID))
	}
	c.mu.Unlock()
}

// idKey is an id as the pending map keys it: the number or the string, so a
// peer that echoes 7 as "7" is still matched.
func idKey(raw json.RawMessage) string {
	var s string
	if unmarshal(raw, &s) == nil {
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
	data, err := marshal(v)
	if err != nil {
		return nil
	}
	return data
}
