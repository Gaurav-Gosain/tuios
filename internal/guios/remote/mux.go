package remote

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"slices"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// Heartbeats: a ping every few seconds, and a link that answers none, or
// takes no write, in a while more is dead, so that a dropped connection,
// as a laptop's that slept, shows as reconnecting within seconds. A
// stream's window stays small, so that the pings and the other streams'
// frames, which wait behind a write of it, wait seconds at most on a slow
// link, however much a stream sends.
const (
	pingEvery   = 5 * time.Second
	pingTimeout = 20 * time.Second
	maxWindow   = 256 << 10
)

func muxConfig(lg *log.Logger) *yamux.Config {
	c := yamux.DefaultConfig()
	c.KeepAliveInterval = pingEvery
	c.ConnectionWriteTimeout = pingTimeout
	c.MaxStreamWindowSize = maxWindow
	c.StreamOpenTimeout = 30 * time.Second
	c.StreamCloseTimeout = time.Minute
	c.LogOutput = nil
	if lg != nil {
		c.Logger = lg
	} else {
		c.Logger = log.New(io.Discard, "", 0)
	}
	return c
}

// maxLine bounds the JSON lines that open a connection and its streams.
const maxLine = 64 << 10

// readLine reads a line of at most maxLine bytes.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxLine {
			return nil, errors.New("remote: line too long")
		}
		if err == nil {
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}

// buffered is a stream whose reads go through a reader that has read
// ahead of it.
type buffered struct {
	io.Reader
	io.Writer
	io.Closer
}

// Options are what a client says of itself as it dials.
type Options struct {
	App    string // the version of Kero
	Client string // "Kero 2.1.0 on laptop"
}

// Conn is a remote connection: streams to kerod on a host.
type Conn struct {
	Addr    Addr
	Welcome proto.MuxWelcome
	sess    *yamux.Session
	tr      io.Closer
}

// Dial connects to a host, within ctx.
func Dial(ctx context.Context, a Addr, o Options) (*Conn, error) {
	cached := Windows(a)
	c, err := dial(ctx, a, o)
	// A shell remembered may have changed, as the SSH server's default:
	// it is found again.
	if err != nil && cached && ctx.Err() == nil && !errors.Is(err, ErrNotInstalled) && !errors.Is(err, ErrAuth) {
		shells.Delete(a.String())
		return dial(ctx, a, o)
	}
	return c, err
}

func dial(ctx context.Context, a Addr, o Options) (*Conn, error) {
	tr, err := transport(ctx, a, o.Client)
	if err != nil {
		return nil, err
	}
	c, err := handshake(ctx, tr, a, o)
	// A host of Windows has no sh: the bridge again, as its shell takes
	// it, which the host's next connections run at once, should it run.
	var se *shellError
	if !errors.As(err, &se) {
		return c, err
	}
	shells.Store(a.String(), se.shell)
	if tr, err = transport(ctx, a, o.Client); err == nil {
		c, err = handshake(ctx, tr, a, o)
	}
	if err != nil && !errors.Is(err, ErrNotInstalled) {
		shells.Delete(a.String())
	}
	return c, err
}

// handshake opens a remote connection over a transport, which it closes
// should it fail.
func handshake(ctx context.Context, tr io.ReadWriteCloser, a Addr, o Options) (*Conn, error) {
	fail := func(err error) (*Conn, error) {
		// The program's end, rather than what a host that answered said,
		// says why.
		if c, ok := tr.(*cmdConn); ok && ctx.Err() == nil && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, os.ErrClosed)) {
			err = c.failure()
		}
		tr.Close()
		return nil, err
	}
	// The handshake gives up with ctx: a host that never answers, as one
	// whose ssh waits on a network that is gone, ends the transport.
	stop := context.AfterFunc(ctx, func() { tr.Close() })
	hello := proto.MuxHello{Kind: proto.KindMux, Mux: proto.MuxVersion, Version: proto.Version, App: o.App,
		Compress: []string{deflate}, Client: o.Client}
	b, _ := json.Marshal(hello)
	if _, err := tr.Write(append(b, '\n')); err != nil {
		if !stop() {
			err = ctx.Err()
		}
		return fail(err)
	}
	br := bufio.NewReaderSize(tr, 32<<10)
	w, err := readWelcome(br)
	if !stop() {
		err = ctx.Err()
	}
	if err != nil {
		return fail(err)
	}
	if w.Error != "" {
		tr.Close()
		return nil, errors.New(w.Error)
	}
	if w.Mux != proto.MuxVersion {
		tr.Close()
		return nil, fmt.Errorf("Kero %s on the host connects otherwise: update it, or this app", w.Host.Kero)
	}
	var rwc io.ReadWriteCloser = buffered{br, tr, tr}
	if w.Compress == deflate {
		rwc = newFlateConn(rwc)
	}
	sess, err := yamux.Client(rwc, muxConfig(nil))
	if err != nil {
		tr.Close()
		return nil, err
	}
	return &Conn{Addr: a, Welcome: w, sess: sess, tr: tr}, nil
}

// readWelcome reads the host's MuxWelcome. Lines before it, which a
// shell's startup files print on a login over SSH, are skipped, as long
// as they are few.
func readWelcome(br *bufio.Reader) (proto.MuxWelcome, error) {
	var w proto.MuxWelcome
	var junk []byte
	for range 100 {
		line, err := readLine(br)
		if err != nil {
			return w, err
		}
		// MuxWelcome's first field is mux; what a startup file printed
		// without a newline may come before it.
		if i := bytes.Index(line, []byte(`{"mux":`)); i >= 0 {
			if err := json.Unmarshal(line[i:], &w); err == nil {
				return w, nil
			}
		}
		if len(junk) < 200 {
			junk = append(junk, line...)
		}
	}
	return w, fmt.Errorf("the host answered what is not Kero: %q", bytes.TrimSpace(junk[:min(len(junk), 200)]))
}

// Open opens a stream: a connection to kerod, as the local socket gives.
func (c *Conn) Open() (net.Conn, error) {
	st, err := c.sess.OpenStream()
	if err != nil {
		return nil, err
	}
	return stream{st}, nil
}

// Dial opens a stream, as client.Dialer does.
func (c *Conn) Dial() (net.Conn, error) { return c.Open() }

// Done is closed once the connection ended.
func (c *Conn) Done() <-chan struct{} { return c.sess.CloseChan() }

// Close ends the connection, and every stream of it: the transport first,
// which the session's reader waits on as it closes.
func (c *Conn) Close() error {
	err := c.tr.Close()
	c.sess.Close()
	return err
}

// Ping measures the round trip to the host.
func (c *Conn) Ping() (time.Duration, error) { return c.sess.Ping() }

// Host asks the host to restart kerod or update Kero (proto.KindRestart,
// proto.KindUpdate), waiting as long as ctx lets it.
func (c *Conn) Host(ctx context.Context, req proto.HostRequest) error {
	st, err := c.Open()
	if err != nil {
		return err
	}
	defer st.Close()
	stop := context.AfterFunc(ctx, func() { st.Close() })
	defer stop()
	b, _ := json.Marshal(req)
	if _, err := st.Write(append(b, '\n')); err != nil {
		return err
	}
	line, err := readLine(bufio.NewReader(st))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	var r proto.HostReply
	if err := json.Unmarshal(line, &r); err != nil {
		return err
	}
	if !r.OK {
		return errors.New(r.Error)
	}
	return nil
}

// stream is a stream of a remote connection, whose Close wakes its own
// reads too, as closing a socket does: a yamux stream closed goes on
// reading until the other side closes it.
type stream struct{ *yamux.Stream }

func (s stream) Close() error {
	err := s.Stream.Close()
	s.Stream.SetReadDeadline(time.Now())
	return err
}

// Server answers remote connections: the bridge's, and kerod's network
// listeners'.
type Server struct {
	// Info describes the host, for the client.
	Info func() proto.HostInfo
	// Stream serves a stream that opens with a Hello for kerod, hello,
	// which s reads on from. mux is what the client said of itself.
	Stream func(s net.Conn, hello []byte, mux proto.MuxHello)
	// Restart and Update answer proto.KindRestart and KindUpdate; nil
	// refuses them. What Restart returns to do runs once the answer is
	// written: a kerod that restarts itself ends with it.
	Restart func() (then func(), err error)
	Update  func(app string) error
	// Closed, when set, runs as the connection ends.
	Closed func()
	Log    *log.Logger
}

// Serve answers a remote connection until it ends.
func (s *Server) Serve(rwc io.ReadWriteCloser) error {
	defer rwc.Close()
	if c, ok := rwc.(net.Conn); ok {
		c.SetReadDeadline(time.Now().Add(15 * time.Second))
	}
	br := bufio.NewReaderSize(rwc, 32<<10)
	line, err := readLine(br)
	if err != nil {
		return err
	}
	if c, ok := rwc.(net.Conn); ok {
		c.SetReadDeadline(time.Time{})
	}
	var hello proto.MuxHello
	w := proto.MuxWelcome{Mux: proto.MuxVersion, Host: s.Info()}
	switch {
	case json.Unmarshal(line, &hello) != nil || hello.Kind != proto.KindMux:
		w.Error = "this is Kero's remote connection: connect with Kero"
	case hello.Mux != proto.MuxVersion:
		w.Error = fmt.Sprintf("Kero %s on %s connects otherwise than this app: update the older one", w.Host.Kero, w.Host.Hostname)
	}
	if w.Error == "" && slices.Contains(hello.Compress, deflate) {
		w.Compress = deflate
	}
	b, _ := json.Marshal(w)
	if _, err := rwc.Write(append(b, '\n')); err != nil || w.Error != "" {
		return err
	}
	var t io.ReadWriteCloser = buffered{br, rwc, rwc}
	if w.Compress == deflate {
		t = newFlateConn(t)
	}
	sess, err := yamux.Server(t, muxConfig(s.Log))
	if err != nil {
		return err
	}
	if s.Closed != nil {
		go func() {
			<-sess.CloseChan()
			s.Closed()
		}()
	}
	defer func() {
		rwc.Close()
		sess.Close()
	}()
	for {
		st, err := sess.AcceptStream()
		if err != nil {
			if sess.IsClosed() {
				return nil
			}
			// A stream whose opening took too long to answer, on a slow
			// link: the others go on.
			continue
		}
		go s.stream(st, hello)
	}
}

// stream serves a stream: a host stream here, else kerod's.
func (s *Server) stream(st *yamux.Stream, mux proto.MuxHello) {
	br := bufio.NewReaderSize(st, 4<<10)
	st.SetReadDeadline(time.Now().Add(15 * time.Second))
	line, err := readLine(br)
	st.SetReadDeadline(time.Time{})
	if err != nil {
		stream{st}.Close()
		return
	}
	var req proto.HostRequest
	json.Unmarshal(line, &req)
	reply := func(err error) {
		r := proto.HostReply{OK: err == nil, App: req.App}
		if err != nil {
			r.Error = err.Error()
		}
		b, _ := json.Marshal(r)
		st.Write(append(b, '\n'))
		stream{st}.Close()
	}
	switch req.Kind {
	case proto.KindRestart:
		if s.Restart == nil {
			reply(errors.New("this host restarts kerod otherwise"))
			return
		}
		then, err := s.Restart()
		reply(err)
		if then != nil {
			then()
		}
	case proto.KindUpdate:
		if s.Update == nil {
			reply(errors.New("Kero on this host updates as it was installed: update it there"))
			return
		}
		reply(s.Update(req.App))
	default:
		s.Stream(&streamConn{stream: stream{st}, r: br}, line, mux)
	}
}

// streamConn is a stream whose first line was read already, through r.
type streamConn struct {
	stream
	r io.Reader
}

func (c *streamConn) Read(p []byte) (int, error) { return c.r.Read(p) }
