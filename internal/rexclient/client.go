package rexclient

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// APIRevision is the Rex control API revision this client was built
// against.
const APIRevision = 19

// The subprotocols and paths of the two connections.
const (
	controlProtocol = "rex.control.v1"
	dataProtocol    = "rex.data.terminal.v1"
)

// CloseBlockEnded is the close code of a data connection whose block ended.
const CloseBlockEnded = 4000

// ShellFlavor is the terminal creator's shell flavor.
const ShellFlavor = "com.superlogical.terminal.shell"

// Control is a control connection. Call sends a request and waits for its
// reply. Events that arrive are passed to the Events channel, and dropped
// when nobody reads it.
type Control struct {
	ws      *conn
	ep      Endpoint
	next    atomic.Uint64
	mu      sync.Mutex
	waiting map[string]chan reply
	Events  chan []byte
	done    chan struct{}
	err     error
}

type reply struct {
	payload []byte
	err     error
}

// envelope is a message on the control connection.
type envelope struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Dial opens the control connection and says hello as a client named name.
func Dial(ep Endpoint, name string) (*Control, error) {
	ws, err := dialWS(ep, "/api/control", controlProtocol)
	if err != nil {
		return nil, err
	}
	hello, _ := jsonx.Marshal(map[string]any{
		"type":             "hello",
		"api_revision":     APIRevision,
		"api_min_revision": APIRevision,
		"client":           map[string]string{"kind": "app", "platform": "linux", "name": name, "label": name},
	})
	if err := ws.write(opText, hello); err != nil {
		_ = ws.close()
		return nil, err
	}
	_, msg, err := ws.read()
	if err != nil {
		_ = ws.close()
		return nil, errfmt.Errorf("the Rex server did not answer hello: %w", err)
	}
	var env envelope
	if jsonx.Unmarshal(msg, &env) != nil || env.Type != "welcome" {
		_ = ws.close()
		return nil, errfmt.Errorf("the Rex server refused hello: %s", oneLine(string(msg)))
	}
	c := &Control{ws: ws, ep: ep, waiting: map[string]chan reply{}, Events: make(chan []byte, 64), done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

func (c *Control) readLoop() {
	defer close(c.done)
	for {
		_, msg, err := c.ws.read()
		if err != nil {
			c.mu.Lock()
			c.err = err
			for id, ch := range c.waiting {
				ch <- reply{err: err}
				delete(c.waiting, id)
			}
			c.mu.Unlock()
			return
		}
		var env envelope
		if jsonx.Unmarshal(msg, &env) != nil {
			continue
		}
		if env.ID != "" && (env.Type == "response" || env.Type == "error" || env.Error != nil) {
			c.mu.Lock()
			ch := c.waiting[env.ID]
			delete(c.waiting, env.ID)
			c.mu.Unlock()
			if ch != nil {
				r := reply{payload: env.Payload}
				if env.Error != nil {
					r.err = errfmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
				}
				ch <- r
			}
			continue
		}
		select {
		case c.Events <- msg:
		default:
		}
	}
}

// Done is closed when the connection ends.
func (c *Control) Done() <-chan struct{} { return c.done }

// Send sends a request and does not wait for its reply.
func (c *Control) Send(method string, payload any) error {
	_, err := c.send(method, payload, false)
	return err
}

// Call sends a request and returns its reply payload.
func (c *Control) Call(method string, payload any) ([]byte, error) {
	ch, err := c.send(method, payload, true)
	if err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, errfmt.Errorf("%s: %w", method, r.err)
		}
		return r.payload, nil
	case <-c.done:
		return nil, errfmt.Errorf("%s: %w", method, c.err)
	}
}

func (c *Control) send(method string, payload any, wait bool) (chan reply, error) {
	if payload == nil {
		payload = struct{}{}
	}
	id := strconv.FormatUint(c.next.Add(1), 10)
	msg, err := jsonx.Marshal(map[string]any{"type": "request", "id": id, "method": method, "payload": payload})
	if err != nil {
		return nil, err
	}
	var ch chan reply
	if wait {
		ch = make(chan reply, 1)
		c.mu.Lock()
		if c.err != nil {
			c.mu.Unlock()
			return nil, c.err
		}
		c.waiting[id] = ch
		c.mu.Unlock()
	}
	if err := c.ws.write(opText, msg); err != nil {
		return nil, err
	}
	return ch, nil
}

// Close ends the connection.
func (c *Control) Close() error { return c.ws.close() }

// Block names a block in a session.
type Block struct {
	SessionID string `json:"session_id"`
	BlockID   string `json:"block_id"`
}

// BlockCall calls a block method, such as com.superlogical.terminal.format.
func (c *Control) BlockCall(b Block, method string, args any) ([]byte, error) {
	return c.Call(method, map[string]any{"session_id": b.SessionID, "block_id": b.BlockID, "args": args})
}

// Session is one row of session.list.
type Session struct {
	ID    string `json:"session_id"`
	Label string `json:"label"`
}

// Sessions lists the server's sessions.
func (c *Control) Sessions() ([]Session, error) {
	raw, err := c.Call("session.list", nil)
	if err != nil {
		return nil, err
	}
	var res struct {
		Sessions []Session `json:"sessions"`
	}
	return res.Sessions, jsonx.Unmarshal(raw, &res)
}

// SessionNamed is the id of the session with the label, "" for none.
func (c *Control) SessionNamed(label string) (string, error) {
	list, err := c.Sessions()
	if err != nil {
		return "", err
	}
	for _, s := range list {
		if s.Label == label {
			return s.ID, nil
		}
	}
	return "", nil
}

// BlockInfo is one row of session.list_blocks.
type BlockInfo struct {
	BlockID string `json:"block_id"`
	Owner   string `json:"owner"`
	Label   string `json:"label"`
}

// Blocks lists every block of a session, detached ones too. The connection
// must be attached to it.
func (c *Control) Blocks(sessionID string) ([]BlockInfo, error) {
	raw, err := c.Call("session.list_blocks", map[string]string{"session_id": sessionID})
	if err != nil {
		return nil, err
	}
	var res struct {
		Blocks []BlockInfo `json:"blocks"`
	}
	return res.Blocks, jsonx.Unmarshal(raw, &res)
}

// NewShell starts a shell block in the session with the label, and creates
// the session when it has none. The connection ends up attached to it.
func (c *Control) NewShell(label string, options map[string]any) (Block, error) {
	layout := map[string]any{"block": map[string]any{"flavor": ShellFlavor, "options": options}}
	id, err := c.SessionNamed(label)
	if err != nil {
		return Block{}, err
	}
	if id != "" {
		if _, err := c.Call("session.attach", map[string]string{"session_id": id}); err == nil {
			raw, err := c.Call("session.new_window", map[string]any{"session_id": id, "layout": layout})
			if err != nil {
				return Block{}, err
			}
			b := firstBlock(raw)
			if b == "" {
				return Block{}, errors.New("session.new_window gave no block")
			}
			return Block{SessionID: id, BlockID: b}, nil
		}
	}
	raw, err := c.Call("session.create", map[string]any{"label": label, "initial_windows": []any{map[string]any{"layout": layout}}})
	if err != nil {
		return Block{}, err
	}
	var res struct {
		SessionID string            `json:"session_id"`
		Windows   []json.RawMessage `json:"initial_windows"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil || res.SessionID == "" || len(res.Windows) == 0 {
		return Block{}, errors.New("session.create gave no session or window")
	}
	b := firstBlock(res.Windows[0])
	if b == "" {
		return Block{}, errors.New("session.create gave no block")
	}
	if _, err := c.Call("session.attach", map[string]string{"session_id": res.SessionID}); err != nil {
		return Block{}, err
	}
	return Block{SessionID: res.SessionID, BlockID: b}, nil
}

// firstBlock reads the first block of a window, as session.create and
// session.new_window give it.
func firstBlock(raw []byte) string {
	var w struct {
		BlockIDs []string `json:"block_ids"`
	}
	if jsonx.Unmarshal(raw, &w) != nil || len(w.BlockIDs) == 0 {
		return ""
	}
	return w.BlockIDs[0]
}

// FindBlock attaches to the first session of labels that holds the block,
// and returns it.
func (c *Control) FindBlock(blockID string, labels ...string) (Block, error) {
	for _, label := range labels {
		id, err := c.SessionNamed(label)
		if err != nil {
			return Block{}, err
		}
		if id == "" {
			continue
		}
		if _, err := c.Call("session.attach", map[string]string{"session_id": id}); err != nil {
			continue
		}
		blocks, err := c.Blocks(id)
		if err != nil {
			return Block{}, err
		}
		for _, b := range blocks {
			if b.BlockID == blockID || shortID(b.BlockID) == blockID {
				return Block{SessionID: id, BlockID: b.BlockID}, nil
			}
		}
	}
	return Block{}, errfmt.Errorf("no running shell %s in the Rex sessions %v", blockID, labels)
}

func shortID(id string) string {
	if len(id) > 6 {
		return id[len(id)-6:]
	}
	return id
}

// Data is a block's terminal data stream, its Ghostty snapshot left out.
type Data struct {
	ws     *conn
	prefix []byte
	snap   []byte
	inSnap bool
	held   []byte
}

// OpenData opens the block's terminal stream. The connection must be
// attached to the block's session.
func (c *Control) OpenData(b Block) (*Data, error) {
	raw, err := c.Call("data.connect", map[string]string{"session_id": b.SessionID, "block_id": b.BlockID, "data_ref_id": "terminal", "subprotocol": dataProtocol})
	if err != nil {
		return nil, err
	}
	var t struct {
		Ticket string `json:"ticket"`
	}
	if err := jsonx.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	ticket, err := base64.RawURLEncoding.DecodeString(t.Ticket)
	if err != nil || len(ticket) != 16 {
		return nil, errors.New("data.connect gave a ticket this client cannot read")
	}
	ws, err := dialWS(c.ep, "/api/data", dataProtocol)
	if err != nil {
		return nil, err
	}
	if err := ws.write(opBinary, append([]byte{1}, ticket...)); err != nil {
		_ = ws.close()
		return nil, err
	}
	_, welcome, err := ws.read()
	if err != nil {
		_ = ws.close()
		return nil, errfmt.Errorf("the Rex server did not open the data stream: %w", err)
	}
	if len(welcome) >= 2 && welcome[0] == 0x82 {
		_ = ws.close()
		return nil, errfmt.Errorf("the Rex server refused the data stream (reason %d)", welcome[1])
	}
	if len(welcome) != 32 || welcome[0] != 0x81 {
		_ = ws.close()
		return nil, errors.New("the Rex server answered the data stream in a form this client does not read")
	}
	nonce := binary.LittleEndian.Uint64(welcome[24:32])
	return &Data{ws: ws, prefix: []byte("\x1b_rex1;" + strconv.FormatUint(nonce, 10) + ";"), inSnap: true}, nil
}

// Read returns the next live output: the bytes the block's program wrote,
// with Rex's metadata taken out. The snapshot at the start is skipped. A
// block that ended returns ErrBlockEnded.
func (d *Data) Read() ([]byte, error) {
	for {
		_, msg, err := d.ws.read()
		if err != nil {
			if errors.Is(err, errClosed) && d.ws.closeCode == CloseBlockEnded {
				return nil, ErrBlockEnded
			}
			return nil, err
		}
		if d.inSnap {
			d.snap = append(d.snap, msg...)
			end, err := snapshotEnd(d.snap)
			if err != nil {
				return nil, err
			}
			if end < 0 {
				continue
			}
			msg = d.snap[end:]
			d.snap, d.inSnap = nil, false
		}
		if out := d.filter(msg); len(out) > 0 {
			return out, nil
		}
	}
}

// Close ends the data stream.
func (d *Data) Close() error { return d.ws.close() }

// ErrBlockEnded is the end of a block's stream because the block ended.
var ErrBlockEnded = errors.New("the Rex shell ended")

// snapshotEnd is the length of the Ghostty snapshot at the start of b, -1
// while its FINISH record has not arrived.
func snapshotEnd(b []byte) (int, error) {
	const envelope = "GHOSTSNP\x01\x00"
	const headerLen, finish = 10, 6
	if len(b) < len(envelope) {
		return -1, nil
	}
	if string(b[:len(envelope)]) != envelope {
		return 0, errors.New("the Rex data stream does not start with a snapshot this client knows")
	}
	off := len(envelope)
	for len(b) >= off+headerLen {
		tag := binary.LittleEndian.Uint16(b[off:])
		n := int(binary.LittleEndian.Uint32(b[off+2:]))
		off += headerLen + n
		if tag == finish {
			if off <= len(b) {
				return off, nil
			}
			return -1, nil
		}
	}
	return -1, nil
}

// filter takes Rex's ESC _ rex1;<nonce>;... ESC \ metadata out of live
// output. A sequence split across messages is held until it ends.
func (d *Data) filter(in []byte) []byte {
	data := append(d.held, in...)
	d.held = nil
	var out []byte
	for {
		i := bytes.IndexByte(data, 0x1b)
		if i < 0 {
			return append(out, data...)
		}
		out = append(out, data[:i]...)
		rest := data[i:]
		switch {
		case bytes.HasPrefix(rest, d.prefix):
			end := bytes.Index(rest[len(d.prefix):], []byte("\x1b\\"))
			if end < 0 {
				if len(rest) > 1<<20 {
					return append(out, rest...)
				}
				d.held = append([]byte(nil), rest...)
				return out
			}
			data = rest[len(d.prefix)+end+2:]
		case len(rest) < len(d.prefix) && bytes.HasPrefix(d.prefix, rest):
			d.held = append([]byte(nil), rest...)
			return out
		default:
			out = append(out, 0x1b)
			data = rest[1:]
		}
	}
}

func oneLine(s string) string {
	s = string(bytes.Join(bytes.Fields([]byte(s)), []byte(" ")))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
