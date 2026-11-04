package client

import (
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/egoist/mygo/plugins/terminal"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Stream is a pane's terminal stream: an io.ReadWriteCloser for the terminal
// plugin's Conn, with ResizePixels and UseTheme. It is the daemon's
// stream-pane: a snapshot of the pane with up to 500 rows of history, then
// the pane's output as the program wrote it, resizes in band.
type Stream struct {
	ps      *session.PaneStream
	c       *Client
	session string
	window  string
	Welcome proto.Welcome

	mu     sync.Mutex
	buf    []byte
	closed chan struct{}
	once   sync.Once
	// Gone is closed when the pane's program ended or its window closed.
	Gone     chan struct{}
	goneOnce sync.Once
	// OnResized, when set, gets the sizes the pane takes, within Read,
	// before the output after them: set it before the first Read.
	OnResized func(cols, rows int)
}

// Attach opens a pane's stream on this machine's daemon.
func Attach(id string) (*Stream, error) { return AttachVia(Local{NoStart: true}, id) }

// current is the client the app connected last, for the panes' streams.
var (
	currentMu sync.Mutex
	current   *Client
)

func setCurrent(c *Client) {
	currentMu.Lock()
	current = c
	currentMu.Unlock()
}

// AttachVia opens a pane's stream. id is a pane (a tuios window), or a
// session, whose focused pane it then streams.
func AttachVia(_ Dialer, id string) (*Stream, error) {
	currentMu.Lock()
	c := current
	currentMu.Unlock()
	if c == nil {
		return nil, errors.New("not connected to the tuios daemon")
	}
	sid, name, ok := c.st.lookup(id)
	if !ok {
		return nil, errors.New("no such pane")
	}
	window := id
	if window == sid {
		window = ""
	}
	vc, err := session.DialVerbClientAs(Version)
	if err != nil {
		return nil, err
	}
	// The pane takes this view's size only while no TUI client shows the
	// session (fit, yield). While one does, the pane keeps the size the
	// TUI gave it, and the view draws that grid: a TUI's panes never change
	// size because guios shows them.
	params := map[string]any{"session_id": sid, "session": name, "fit": true, "yield": true}
	if window != "" {
		params["window"] = window
	}
	ps, reply, err := vc.OpenPaneStream(params)
	if err != nil && strings.Contains(err.Error(), "parameter yield") {
		// A daemon from before yielding leases: its fit lease holds a TUI
		// client's pane at most at this view's size.
		delete(params, "yield")
		ps, reply, err = vc.OpenPaneStream(params)
	}
	if err != nil {
		_ = vc.Close()
		return nil, err
	}
	s := &Stream{ps: ps, c: c, session: name, window: reply.Window, closed: make(chan struct{}), Gone: make(chan struct{})}
	s.Welcome = proto.Welcome{Version: proto.Version, Cols: reply.Cols, Rows: reply.Rows}
	return s, nil
}

// Read gives the pane's bytes: the snapshot first, which paints the pane on
// a reset terminal, then the output.
func (s *Stream) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		kind, payload, err := s.ps.ReadFrame()
		if err != nil {
			s.goneOnce.Do(func() { close(s.Gone) })
			return 0, io.EOF
		}
		switch kind {
		case session.PaneFrameSnapshot:
			if len(payload) < 12 {
				continue
			}
			cols, rows := int(binary.BigEndian.Uint16(payload[8:])), int(binary.BigEndian.Uint16(payload[10:]))
			if s.OnResized != nil {
				s.OnResized(cols, rows)
			}
			// A full reset first: the snapshot paints a fresh terminal.
			s.buf = append([]byte("\x1bc"), payload[12:]...)
		case session.PaneFrameOutput:
			if len(payload) > 8 {
				s.buf = payload[8:]
			}
		case session.PaneFrameResize:
			if len(payload) >= 12 && s.OnResized != nil {
				s.OnResized(int(binary.BigEndian.Uint16(payload[8:])), int(binary.BigEndian.Uint16(payload[10:])))
			}
		case session.PaneFrameEnd:
			s.goneOnce.Do(func() { close(s.Gone) })
			return 0, io.EOF
		}
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

// Write sends what the terminal encoded from keys, paste and the mouse.
func (s *Stream) Write(p []byte) (int, error) {
	for off := 0; off < len(p); off += session.PaneStreamMaxInput {
		end := min(off+session.PaneStreamMaxInput, len(p))
		if err := s.ps.Input(p[off:end]); err != nil {
			return off, err
		}
	}
	return len(p), nil
}

// ResizePixels asks for the pane at this view's size, as the stream's
// lease. The daemon applies it only while no TUI client shows the session,
// and answers with a resize frame when the pane's size changes.
func (s *Stream) ResizePixels(cols, rows, _, _ int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return s.ps.Lease(cols, rows)
}

// Claim makes the pane take this view's size, which ResizePixels does at
// once with the daemon: nothing more to tell.
func (s *Stream) Claim() error { return nil }

// UseTheme is the colours the view shows. The daemon takes a viewer's
// colours when the stream opens. Mid-stream changes wait for a verb.
func (s *Stream) UseTheme(*terminal.Theme) error { return nil }

// Clear clears the pane's history, and its screen above the prompt with
// proto.ClearScreen, for every client.
func (s *Stream) Clear(what byte) error {
	_, err := s.c.verb("clear-history", map[string]any{"session": s.session, "window": s.window, "screen": what == proto.ClearScreen})
	return err
}

// Close ends the stream.
func (s *Stream) Close() error {
	var err error
	s.once.Do(func() {
		close(s.closed)
		err = s.ps.Close()
	})
	return err
}
