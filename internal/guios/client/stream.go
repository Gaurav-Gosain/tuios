package client

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

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
	sid     string
	window  string
	// shown is the last F frame sent: session.PaneHidden, PaneVisible or
	// PaneFocused. Guarded by c.viewMu.
	shown   byte
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
	s := &Stream{ps: ps, c: c, session: name, sid: sid, window: reply.Window, closed: make(chan struct{}), Gone: make(chan struct{})}
	c.addStream(s)
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
func (s *Stream) ResizePixels(cols, rows, cellW, cellH int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return s.ps.LeaseCell(cols, rows, cellW, cellH)
}

// Claim makes the pane take this view's size, which ResizePixels does at
// once with the daemon: nothing more to tell.
func (s *Stream) Claim() error { return nil }

// UseTheme tells the daemon the colours the view shows, so a program that
// asks the terminal for them (OSC 10, 11 and 4) gets this view's while no
// TUI client shows the session.
func (s *Stream) UseTheme(th *terminal.Theme) error {
	if th == nil {
		return nil
	}
	hex := func(c ui.Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
	pal := make([]string, len(th.Palette))
	for i, c := range th.Palette {
		pal[i] = hex(c)
	}
	return s.ps.Viewer(session.ViewerReport{Bg: hex(th.Background), Fg: hex(th.Foreground), Palette: strings.Join(pal, ",")})
}

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
		s.c.removeStream(s)
		close(s.closed)
		err = s.ps.Close()
	})
	return err
}

// What the person looks at. The app says which session it shows ("view")
// and which pane of it has the focus ("focus"). Each stream tells the
// daemon whether its pane is hidden, on screen, or on screen with the
// focus, as a TUI client's focus does: a finished turn on the focused pane
// counts as seen, and the pane gets focus reports.

func (c *Client) addStream(s *Stream) {
	c.viewMu.Lock()
	if c.streams == nil {
		c.streams = map[*Stream]struct{}{}
	}
	c.streams[s] = struct{}{}
	c.viewMu.Unlock()
	c.applyView()
}

func (c *Client) removeStream(s *Stream) {
	c.viewMu.Lock()
	delete(c.streams, s)
	c.viewMu.Unlock()
}

// setView records the session the app shows, "" for none.
func (c *Client) setView(sid string) {
	c.viewMu.Lock()
	c.viewing = sid
	c.viewMu.Unlock()
	c.applyView()
}

// setFocus records the pane that has the focus in a session.
func (c *Client) setFocus(sid, pane string) {
	c.viewMu.Lock()
	if c.focused == nil {
		c.focused = map[string]string{}
	}
	c.focused[sid] = pane
	c.viewMu.Unlock()
	c.applyView()
}

// applyView sends each stream whose state changed its F frame.
func (c *Client) applyView() {
	c.viewMu.Lock()
	defer c.viewMu.Unlock()
	for s := range c.streams {
		var want byte = session.PaneHidden
		if s.sid != "" && s.sid == c.viewing {
			want = session.PaneVisible
			if f := c.focused[s.sid]; f == "" || f == s.window {
				want = session.PaneFocused
			}
		}
		if want != s.shown {
			s.shown = want
			_ = s.ps.Shown(want)
		}
	}
}
