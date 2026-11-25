package rexclient

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// The subprotocols and paths of the two connections.
const (
	controlProtocol = "rex.control.v1"
	dataProtocol    = "rex.data.terminal.v1"
)

// CloseBlockEnded is the close code of a data connection whose block ended.
const CloseBlockEnded = 4000

// ShellFlavor is the terminal creator's shell flavor.
const ShellFlavor = "com.superlogical.terminal.shell"

// DetachSequence is what tuios's rex_detach action types into a Rex pane.
// tuios rex-attach reads it from its input, lets the shell go and exits. It
// is an APC string, which a terminal never sends for a key.
const DetachSequence = "\x1b_tuios-rex-detach\x1b\\"

// Control is a control connection. Call sends a request and waits for its
// reply. Events from the server are dropped.
type Control struct {
	ws      *conn
	ep      Endpoint
	next    atomic.Uint64
	mu      sync.Mutex
	waiting map[string]chan reply
	done    chan struct{}
	err     error
}

// request is a request on the control connection.
type request struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Payload any    `json:"payload"`
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

// hello introduces this client as an app named tuios.
const hello = `{"type":"hello","api_revision":19,"api_min_revision":19,"client":{"kind":"app","platform":"` +
	runtime.GOOS + `","name":"tuios","label":"tuios"}}`

// Dial opens the control connection and says hello.
func Dial(ep Endpoint) (*Control, error) {
	ws, err := dialWS(ep, "/api/control", controlProtocol)
	if err != nil {
		return nil, err
	}
	if err := ws.write(opText, []byte(hello)); err != nil {
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
	c := &Control{ws: ws, ep: ep, waiting: map[string]chan reply{}, done: make(chan struct{})}
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
	msg, err := jsonx.Marshal(request{"request", id, method, payload})
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

// blockArgs is the payload of a block method.
type blockArgs struct {
	SessionID string `json:"session_id"`
	BlockID   string `json:"block_id"`
	Args      any    `json:"args"`
}

// BlockCall calls a block method, such as com.superlogical.terminal.format.
func (c *Control) BlockCall(b Block, method string, args any) ([]byte, error) {
	return c.Call(method, blockArgs{b.SessionID, b.BlockID, args})
}

// Size is a terminal size in cells.
type Size struct {
	Columns int `json:"columns"`
	Rows    int `json:"rows"`
}

// Resize sets the block's terminal size.
func (c *Control) Resize(b Block, s Size) error {
	return c.Send("com.superlogical.terminal.resize", blockArgs{b.SessionID, b.BlockID, s})
}

// Write types text into the block. client.action keeps it in order with
// the other input of this connection.
func (c *Control) Write(b Block, text string) error {
	type data struct {
		Data string `json:"data"`
	}
	return c.Send("client.action", struct {
		Action string `json:"action"`
		blockArgs
	}{"com.superlogical.terminal.write", blockArgs{b.SessionID, b.BlockID, data{text}}})
}

// Own makes this connection's size and theme the block's, or with own
// false lets the block go, running, for another client to show.
func (c *Control) Own(b Block, own bool) error {
	method := "session.disown_block"
	if own {
		method = "session.own_block"
	}
	_, err := c.Call(method, struct {
		SessionID string   `json:"session_id"`
		BlockIDs  []string `json:"block_ids"`
	}{b.SessionID, []string{b.BlockID}})
	return err
}

// sessionRef names a session in a request.
type sessionRef struct {
	SessionID string `json:"session_id"`
}

// SessionNamed attaches to the session with the label and returns its id,
// "" when there is none.
func (c *Control) SessionNamed(label string) (string, error) {
	raw, err := c.Call("session.list", nil)
	if err != nil {
		return "", err
	}
	var res struct {
		Sessions []struct {
			ID    string `json:"session_id"`
			Label string `json:"label"`
		} `json:"sessions"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	for _, s := range res.Sessions {
		if s.Label == label {
			_, err := c.Call("session.attach", sessionRef{s.ID})
			return s.ID, err
		}
	}
	return "", nil
}

// NewShell starts a shell block in a new window of the session with the
// label, in the folder cwd and at size s, and creates the session when there
// is none.
func (c *Control) NewShell(label, cwd string, s Size) (Block, error) {
	id, err := c.SessionNamed(label)
	if err != nil {
		return Block{}, err
	}
	if id == "" {
		raw, err := c.Call("session.create", struct {
			Label string `json:"label"`
		}{label})
		if err != nil {
			return Block{}, err
		}
		var ref sessionRef
		if jsonx.Unmarshal(raw, &ref) != nil || ref.SessionID == "" {
			return Block{}, errors.New("session.create gave no session")
		}
		if _, err := c.Call("session.attach", ref); err != nil {
			return Block{}, err
		}
		id = ref.SessionID
	}
	type options struct {
		Cwd  string `json:"cwd"`
		Size Size   `json:"size"`
	}
	type block struct {
		Flavor  string  `json:"flavor"`
		Options options `json:"options"`
	}
	type layout struct {
		Block block `json:"block"`
	}
	raw, err := c.Call("session.new_window", struct {
		SessionID string `json:"session_id"`
		Layout    layout `json:"layout"`
	}{id, layout{block{ShellFlavor, options{cwd, s}}}})
	if err != nil {
		return Block{}, err
	}
	var w struct {
		BlockIDs []string `json:"block_ids"`
	}
	if jsonx.Unmarshal(raw, &w) != nil || len(w.BlockIDs) == 0 {
		return Block{}, errors.New("session.new_window gave no block")
	}
	return Block{SessionID: id, BlockID: w.BlockIDs[0]}, nil
}

// FindBlock returns the block from the first session of labels that holds
// it, and attaches to that session.
func (c *Control) FindBlock(blockID string, labels ...string) (Block, error) {
	for _, label := range labels {
		id, err := c.SessionNamed(label)
		if err != nil || id == "" {
			continue
		}
		raw, err := c.Call("session.list_blocks", sessionRef{id})
		if err != nil {
			return Block{}, err
		}
		var res struct {
			Blocks []Block `json:"blocks"`
		}
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			return Block{}, err
		}
		for _, b := range res.Blocks {
			if b.BlockID == blockID {
				return Block{SessionID: id, BlockID: b.BlockID}, nil
			}
		}
	}
	return Block{}, errfmt.Errorf("no running shell %s in the Rex sessions %v", blockID, labels)
}

// Data is a block's terminal data stream, its Ghostty snapshot left out.
// Rex's metadata in the stream, ESC _ rex1;... ESC \, stays in: it is an APC
// string, which a terminal ignores.
type Data struct {
	ws     *conn
	snap   []byte
	inSnap bool
}

// OpenData opens the block's terminal stream. The connection must be
// attached to the block's session.
func (c *Control) OpenData(b Block) (*Data, error) {
	raw, err := c.Call("data.connect", struct {
		Block
		DataRef     string `json:"data_ref_id"`
		Subprotocol string `json:"subprotocol"`
	}{b, "terminal", dataProtocol})
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
	return &Data{ws: ws, inSnap: true}, nil
}

// Read returns the next live output. The snapshot at the start is skipped.
// A block that ended returns ErrBlockEnded.
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
		if len(msg) > 0 {
			return msg, nil
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

func oneLine(s string) string {
	return strings.ReplaceAll(s[:min(len(s), 200)], "\n", " ")
}
