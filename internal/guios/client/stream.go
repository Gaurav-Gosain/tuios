package client

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Stream is a pane's terminal stream: an io.ReadWriteCloser for the terminal
// plugin's Conn, with ResizePixels and UseTheme. It is the daemon's
// stream-pane: a snapshot of the pane with up to 500 rows of history, then
// the pane's output as the program wrote it, resizes in band.
//
// A stream of a pane on a linked machine outlives its link: when the
// connection drops without the pane ending, Read waits and opens the stream
// again, and the new snapshot paints over the last frame in place. The
// terminal keeps showing that frame meanwhile, so a reconnect never blanks
// the pane. Lost reports the wait.
type Stream struct {
	c       *Client
	session string
	sid     string
	window  string
	params  map[string]any
	// shown is the last F frame sent: session.PaneHidden, PaneVisible or
	// PaneFocused. Guarded by c.viewMu.
	shown   byte
	Welcome proto.Welcome

	// psMu guards ps, which a reconnect replaces.
	psMu sync.RWMutex
	ps   *session.PaneStream

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
	// OnLink, when set, hears that a linked machine's stream lost its
	// connection (false) and has it again (true).
	OnLink func(up bool)
	lost   atomic.Bool

	// renew says the daemon took no yield: its fit lease lapses after the
	// daemon's TTL (30 s) unless sent again, so the last one is sent
	// every leaseRenew while the stream is open. With yield the daemon
	// keeps a local lease, and no timer runs.
	renew    bool
	leaseMu  sync.Mutex
	lease    [4]int
	renewing bool
	theme    *session.ViewerReport
}

// leaseRenew is how often a lease is sent again on a daemon without yield,
// well inside its 30 s TTL.
const leaseRenew = 10 * time.Second

// Attach opens a pane's stream on this machine's daemon.
func Attach(id string) (*Stream, error) { return AttachVia(Local{NoStart: true}, id) }

// clients are the clients the app connected last, by host ("" for this
// machine), for the panes' streams.
var (
	currentMu sync.Mutex
	clients   = map[string]*Client{}
)

func setCurrent(c *Client) {
	currentMu.Lock()
	clients[c.host] = c
	currentMu.Unlock()
}

func currentOf(host string) *Client {
	currentMu.Lock()
	defer currentMu.Unlock()
	return clients[host]
}

// AttachVia opens a pane's stream on the daemon d names: Local or a Host.
// id is a pane (a tuios window), or a session, whose focused pane it then
// streams.
func AttachVia(d Dialer, id string) (*Stream, error) {
	host := ""
	if h, ok := d.(Host); ok {
		host = string(h)
	}
	c := currentOf(host)
	if c == nil {
		if host != "" {
			return nil, fmt.Errorf("not connected to the tuios daemon on %s", host)
		}
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
	// The pane takes this view's size only while no TUI client shows the
	// session (fit, yield). While one does, the pane keeps the size the
	// TUI gave it, and the view draws that grid: a TUI's panes never change
	// size because guios shows them.
	params := map[string]any{"session_id": sid, "session": name, "fit": true, "yield": true}
	if window != "" {
		params["window"] = window
	}
	ps, reply, renew, err := openPane(host, params)
	debugf("attach %s %s: %v (%dx%d)", host, id, err, reply.Cols, reply.Rows)
	if err != nil {
		return nil, err
	}
	if window == "" {
		// A reconnect opens the pane it streamed, not the session's
		// focused one of that time.
		params["window"] = reply.Window
	}
	s := &Stream{ps: ps, c: c, session: name, sid: sid, window: reply.Window, params: params, closed: make(chan struct{}), Gone: make(chan struct{}), renew: renew}
	c.addStream(s)
	s.Welcome = proto.Welcome{Version: proto.Version, Cols: reply.Cols, Rows: reply.Rows}
	return s, nil
}

// openPane dials the host's daemon and opens a pane's stream on it.
func openPane(host string, params map[string]any) (*session.PaneStream, session.PaneStreamReply, bool, error) {
	vc, err := dialHost(host)
	if err != nil {
		return nil, session.PaneStreamReply{}, false, err
	}
	ps, reply, err := vc.OpenPaneStream(params)
	renew := false
	if err != nil && strings.Contains(err.Error(), "parameter yield") {
		// A daemon from before yielding leases: its fit lease holds a TUI
		// client's pane at most at this view's size.
		delete(params, "yield")
		ps, reply, err = vc.OpenPaneStream(params)
		renew = true
	}
	if err != nil {
		_ = vc.Close()
		return nil, reply, false, err
	}
	return ps, reply, renew, nil
}

// backs wake the streams of a host that wait for its link: closed and
// replaced when the link is back (LinkBack).
var (
	backMu sync.Mutex
	backs  = map[string]chan struct{}{}
)

func linkBack(host string) <-chan struct{} {
	backMu.Lock()
	defer backMu.Unlock()
	ch := backs[host]
	if ch == nil {
		ch = make(chan struct{})
		backs[host] = ch
	}
	return ch
}

// LinkBack tells the streams of a host that wait for its link that the link
// is up again: they try at once rather than at their next turn.
func LinkBack(host string) {
	backMu.Lock()
	if ch := backs[host]; ch != nil {
		close(ch)
		delete(backs, host)
	}
	backMu.Unlock()
}

// Lost reports whether a linked machine's stream lost its connection and
// waits to open it again.
func (s *Stream) Lost() bool { return s.lost.Load() }

func (s *Stream) pane() *session.PaneStream {
	s.psMu.RLock()
	defer s.psMu.RUnlock()
	return s.ps
}

// Read gives the pane's bytes: the snapshot first, which paints the pane on
// a reset terminal, then the output.
func (s *Stream) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		kind, payload, err := s.pane().ReadFrame()
		if err != nil {
			if s.c.host != "" && s.reopen() {
				continue
			}
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
			if paneEnded(string(payload)) {
				// The program exited, or the window closed: the app
				// closes the pane, or keeps it with its last frame
				// (keep_open). An end of the stream here would have the
				// terminal write "[Disconnected]" over that frame.
				<-s.closed
			}
			return 0, io.EOF
		}
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

// paneEnded reports whether a stream's exit reason says the pane's program
// ended or its window closed ("exited 0", "killed 15", "closed"), and not
// that the daemon stopped or refused the stream.
func paneEnded(reason string) bool {
	return reason == "closed" || strings.HasPrefix(reason, "exited ") || strings.HasPrefix(reason, "killed ")
}

// reopenGiveUp is how long a linked machine's stream waits for its pane
// to come back before it ends. The far daemon keeps the pane's program for
// as long as it runs, so the wait is only a bound on a host gone for good.
const reopenGiveUp = 30 * time.Minute

// reopen opens a lost stream of a linked machine's pane again, waiting
// longer each time, up to 5 s between tries. It reports false when the
// stream was closed, the pane is gone, or the wait passed reopenGiveUp.
func (s *Stream) reopen() bool {
	select {
	case <-s.closed:
		return false
	default:
	}
	s.lost.Store(true)
	if s.OnLink != nil {
		s.OnLink(false)
	}
	_ = s.pane().Close()
	until := time.Now().Add(reopenGiveUp)
	wait := 250 * time.Millisecond
	for time.Now().Before(until) {
		select {
		case <-s.closed:
			return false
		case <-time.After(wait):
		case <-linkBack(s.c.host):
		}
		wait = min(wait*2, 5*time.Second)
		params := maps.Clone(s.params)
		if s.renew {
			delete(params, "yield")
		}
		ps, reply, _, err := openPane(s.c.host, params)
		debugf("reopen %s %s: %v (%dx%d) lease %v", s.c.host, s.window, err, reply.Cols, reply.Rows, s.lease)
		if err != nil {
			var ve *session.VerbCallError
			if errors.As(err, &ve) && (ve.Code == session.ErrVerbSessionNotFound || ve.Code == session.ErrVerbWindowNotFound) {
				return false
			}
			continue
		}
		s.psMu.Lock()
		s.ps = ps
		s.psMu.Unlock()
		// The new stream knows nothing of this view yet.
		s.leaseMu.Lock()
		if s.lease[0] > 0 {
			_ = s.sendLease()
		}
		th := s.theme
		s.leaseMu.Unlock()
		if th != nil && !s.renew {
			_ = ps.Viewer(*th)
		}
		if c := currentOf(s.c.host); c != nil && c != s.c {
			s.c.removeStream(s)
			s.c = c
		}
		s.c.viewMu.Lock()
		s.shown = 0
		s.c.viewMu.Unlock()
		s.c.addStream(s)
		s.lost.Store(false)
		if s.OnLink != nil {
			s.OnLink(true)
		}
		return true
	}
	return false
}

// Write sends what the terminal encoded from keys, paste and the mouse.
func (s *Stream) Write(p []byte) (int, error) {
	for off := 0; off < len(p); off += session.PaneStreamMaxInput {
		end := min(off+session.PaneStreamMaxInput, len(p))
		if s.lost.Load() {
			// The link is down: what the person types now would reach
			// the program late, or never. The pane says it reconnects.
			return len(p), nil
		}
		if err := s.pane().Input(p[off:end]); err != nil {
			if s.c.host != "" {
				return len(p), nil
			}
			return off, err
		}
	}
	return len(p), nil
}

// debugStreams prints the streams' opens, reopens and leases on stderr.
var debugStreams = os.Getenv("GUIOS_DEBUG_STREAMS") != ""

func debugf(f string, args ...any) {
	if debugStreams {
		fmt.Fprintf(os.Stderr, "guios: "+f+"\n", args...)
	}
}

// ResizePixels asks for the pane at this view's size, as the stream's
// lease. The daemon applies it only while no TUI client shows the session,
// and answers with a resize frame when the pane's size changes.
func (s *Stream) ResizePixels(cols, rows, cellW, cellH int) error {
	debugf("lease %s %s %dx%d", s.c.host, s.window, cols, rows)
	if cols <= 0 || rows <= 0 {
		return nil
	}
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	s.lease = [4]int{cols, rows, cellW, cellH}
	// A daemon without yield lets a lease lapse after its TTL, and so does
	// a linked machine's daemon for a stream over a link, yield or not.
	if (s.renew || s.c.host != "") && !s.renewing {
		s.renewing = true
		renewAdd(s)
	}
	return s.sendLease()
}

// sendLease sends the last lease. A daemon without yield predates the cell
// pixels too, and takes only the 4-byte L frame. Hold leaseMu.
func (s *Stream) sendLease() error {
	l := s.lease
	ps := s.pane()
	if s.renew {
		return ps.Lease(l[0], l[1])
	}
	return ps.LeaseCell(l[0], l[1], l[2], l[3])
}

// The streams whose leases lapse unless sent again: one ticker sends them
// all every leaseRenew, and stops once none is left, so a window of remote
// panes wakes once every leaseRenew, not once per pane.
var (
	renewMu  sync.Mutex
	renewSet = map[*Stream]struct{}{}
	renewOn  bool
)

func renewAdd(s *Stream) {
	renewMu.Lock()
	defer renewMu.Unlock()
	renewSet[s] = struct{}{}
	if !renewOn {
		renewOn = true
		go renewLoop()
	}
}

func renewLoop() {
	t := time.NewTicker(leaseRenew)
	defer t.Stop()
	for range t.C {
		renewMu.Lock()
		if len(renewSet) == 0 {
			renewOn = false
			renewMu.Unlock()
			return
		}
		list := slices.Collect(maps.Keys(renewSet))
		renewMu.Unlock()
		for _, s := range list {
			drop := false
			select {
			case <-s.closed:
				drop = true
			default:
				if s.lost.Load() {
					continue
				}
				s.leaseMu.Lock()
				err := s.sendLease()
				s.leaseMu.Unlock()
				drop = err != nil && s.c.host == ""
			}
			if drop {
				renewMu.Lock()
				delete(renewSet, s)
				renewMu.Unlock()
			}
		}
	}
}

// Claim makes the pane take this view's size, which ResizePixels does at
// once with the daemon: nothing more to tell.
func (s *Stream) Claim() error { return nil }

// UseTheme tells the daemon the colours the view shows, so a program that
// asks the terminal for them (OSC 10, 11 and 4) gets this view's while no
// TUI client shows the session.
func (s *Stream) UseTheme(th *terminal.Theme) error {
	// A daemon without yield predates the V frame too.
	if th == nil || s.renew {
		return nil
	}
	hex := func(c ui.Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
	pal := make([]string, len(th.Palette))
	for i, c := range th.Palette {
		pal[i] = hex(c)
	}
	r := session.ViewerReport{Bg: hex(th.Background), Fg: hex(th.Foreground), Palette: strings.Join(pal, ",")}
	s.leaseMu.Lock()
	s.theme = &r
	s.leaseMu.Unlock()
	return s.pane().Viewer(r)
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
		err = s.pane().Close()
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
		if want != s.shown && !s.renew {
			s.shown = want
			_ = s.pane().Shown(want)
		}
	}
}
