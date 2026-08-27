package federation

import (
	"errors"
	"io"
	"sync"
)

// LinkPreamble is the first line the proxy writes on a fresh link, before any
// frame. It exists because the pipe is not guaranteed to be clean: an ssh
// login can print a banner, a shell rc file can echo, and either would be read
// as a frame header and kill the link with an unreadable error. The hub skips
// lines until it sees this one, so noise ahead of the proxy is discarded and
// noise that never ends is reported as "this host did not answer as a tuios
// link" instead of as a framing failure.
const LinkPreamble = "TUIOS-LINK 1"

// preambleScanLimit bounds how much junk the hub reads while looking for the
// preamble. A remote that never sends it is a misconfiguration, not something
// to keep reading.
const preambleScanLimit = 8 << 10 // 8 KiB

// streamBufferFrames is how many data frames a stream may hold before the mux
// read loop blocks. The loop is shared, so a stalled consumer stalls the link;
// with one control stream in stage 1 there is nothing to starve, and closing
// the link is what unwedges it. A stream per attach (stage 3) needs a real
// per-stream window instead.
const streamBufferFrames = 64

var (
	// ErrLinkClosed reports use of a mux whose pipe is gone.
	ErrLinkClosed = errors.New("federation: link is closed")
	// ErrStreamClosed reports use of a stream after either end closed it.
	ErrStreamClosed = errors.New("federation: stream is closed")
	// ErrTooManyStreams reports an open refused because the peer already holds
	// the maximum. It bounds what one untrusted peer can make this side
	// allocate.
	ErrTooManyStreams = errors.New("federation: too many open streams")
)

// maxStreams caps concurrent streams on one link.
const maxStreams = 32

// Stream ids are split between the two ends of a link: the side that dials
// allocates odd ids, the side that answers allocates even ones. This is the
// usual scheme (ssh and HTTP/2 both do it) and it is load bearing here for one
// specific reason.
//
// Both ends used to start at 1. The hub's control stream is therefore id 1, and
// the first stream a peer opened was also id 1, so handleOpen's duplicate check
// answered it with a close before the inbound-open refusal above was ever
// consulted. The peer saw a closed stream either way, which made the refusal
// untestable: deleting it changed nothing a test could see. The split means an
// inbound open can only ever name an id this side does not own, so the refusal
// is the only thing that can answer it.
const (
	dialerFirstID   = 1
	answererFirstID = 2
	idStride        = 2
)

// mux interleaves logical streams over one byte pipe.
//
// Both ends run the same type. The hub side opens streams and passes a nil
// accept function, so an open arriving from the remote is refused: section 1's
// first invariant is that a remote daemon never gets a channel into the hub,
// and this is where that is enforced rather than assumed. The proxy side passes
// an accept function that dials the local daemon socket.
type mux struct {
	w  io.Writer
	r  io.Reader
	c  io.Closer
	wm sync.Mutex

	// accept handles an open frame from the peer. Nil means opens are refused.
	accept func(*Stream)

	mu      sync.Mutex
	streams map[uint32]*Stream
	nextID  uint32
	closed  bool
	err     error

	done chan struct{}
	once sync.Once

	// wg tracks the accept handlers this side started, so a proxy can wait for
	// its socket pumps to finish before the process exits.
	wg sync.WaitGroup
}

// newMux wraps a pipe. accept may be nil, which refuses peer-initiated streams.
// firstID is dialerFirstID or answererFirstID, whichever end this is.
func newMux(rwc io.ReadWriteCloser, accept func(*Stream), firstID uint32) *mux {
	return newMuxRW(rwc, rwc, rwc, accept, firstID)
}

// newMuxRW is newMux with the three halves given separately. The hub needs it
// because it reads the preamble through a bufio.Reader, which may already hold
// the first frame's bytes: handing the mux the raw pipe instead would lose
// them.
func newMuxRW(r io.Reader, w io.Writer, c io.Closer, accept func(*Stream), firstID uint32) *mux {
	return &mux{
		w:       w,
		r:       r,
		c:       c,
		accept:  accept,
		streams: make(map[uint32]*Stream),
		nextID:  firstID,
		done:    make(chan struct{}),
	}
}

// Done is closed when the mux stops, whatever stopped it.
func (m *mux) Done() <-chan struct{} { return m.done }

// Err reports why the mux stopped, or nil while it runs.
func (m *mux) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// Open starts a new stream and tells the peer to open its end.
func (m *mux) Open() (*Stream, error) {
	m.mu.Lock()
	if m.closed {
		err := m.err
		m.mu.Unlock()
		if err == nil {
			err = ErrLinkClosed
		}
		return nil, err
	}
	if len(m.streams) >= maxStreams {
		m.mu.Unlock()
		return nil, ErrTooManyStreams
	}
	id := m.nextID
	m.nextID += idStride
	s := newStream(m, id)
	m.streams[id] = s
	m.mu.Unlock()

	if err := m.writeFrame(frameOpen, id, nil); err != nil {
		m.dropStream(id)
		return nil, err
	}
	return s, nil
}

// writeFrame serialises one frame onto the pipe.
func (m *mux) writeFrame(t frameType, id uint32, payload []byte) error {
	m.wm.Lock()
	defer m.wm.Unlock()
	select {
	case <-m.done:
		return ErrLinkClosed
	default:
	}
	return writeFrame(m.w, t, id, payload)
}

func (m *mux) dropStream(id uint32) {
	m.mu.Lock()
	s := m.streams[id]
	delete(m.streams, id)
	m.mu.Unlock()
	if s != nil {
		s.peerClosed(io.EOF)
	}
}

// run reads frames until the pipe ends or a protocol error occurs. It returns
// the reason it stopped.
func (m *mux) run() error {
	for {
		f, err := readFrame(m.r)
		if err != nil {
			m.stop(err)
			return err
		}
		switch f.Type {
		case frameOpen:
			m.handleOpen(f.Stream)
		case frameData:
			m.handleData(f.Stream, f.Payload)
		case frameClose:
			m.dropStream(f.Stream)
		}
	}
}

func (m *mux) handleOpen(id uint32) {
	if m.accept == nil {
		// The hub refuses every inbound open. Answering with a close rather
		// than killing the link keeps a confused peer from taking the listing
		// down with it, and the refusal is the security property, not a
		// convenience.
		_ = m.writeFrame(frameClose, id, nil)
		return
	}
	m.mu.Lock()
	if m.closed || len(m.streams) >= maxStreams {
		m.mu.Unlock()
		_ = m.writeFrame(frameClose, id, nil)
		return
	}
	if _, dup := m.streams[id]; dup {
		m.mu.Unlock()
		_ = m.writeFrame(frameClose, id, nil)
		return
	}
	s := newStream(m, id)
	m.streams[id] = s
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.accept(s)
	}()
}

func (m *mux) handleData(id uint32, payload []byte) {
	m.mu.Lock()
	s := m.streams[id]
	m.mu.Unlock()
	if s == nil {
		// Data for a stream this side already closed. Dropping it is correct:
		// the close raced the data, and the peer will see the close.
		return
	}
	s.deliver(payload)
}

// stop tears the mux down once, recording why.
func (m *mux) stop(cause error) {
	m.once.Do(func() {
		m.mu.Lock()
		m.closed = true
		if m.err == nil {
			m.err = cause
		}
		streams := make([]*Stream, 0, len(m.streams))
		for _, s := range m.streams {
			streams = append(streams, s)
		}
		m.streams = map[uint32]*Stream{}
		m.mu.Unlock()

		close(m.done)
		for _, s := range streams {
			s.peerClosed(cause)
		}
		if m.c != nil {
			_ = m.c.Close()
		}
	})
}

// Close shuts the mux and its pipe down.
func (m *mux) Close() error {
	m.stop(ErrLinkClosed)
	return nil
}

// Stream is one logical connection on a link. It is an io.ReadWriteCloser, so
// the JSON verb caller and the proxy's socket pump both use it as an ordinary
// connection.
type Stream struct {
	m  *mux
	id uint32

	mu       sync.Mutex
	buf      []byte
	incoming chan []byte
	closed   chan struct{}
	closeOne sync.Once
	err      error
}

func newStream(m *mux, id uint32) *Stream {
	return &Stream{
		m:        m,
		id:       id,
		incoming: make(chan []byte, streamBufferFrames),
		closed:   make(chan struct{}),
	}
}

// deliver hands a data frame to the stream's reader. It blocks when the reader
// is behind, which is the mux's only backpressure. See streamBufferFrames.
func (s *Stream) deliver(payload []byte) {
	select {
	case s.incoming <- payload:
	case <-s.closed:
	case <-s.m.done:
	}
}

// peerClosed ends the stream because the far side closed it or the link died.
func (s *Stream) peerClosed(cause error) {
	s.closeOne.Do(func() {
		s.mu.Lock()
		if s.err == nil {
			s.err = cause
		}
		s.mu.Unlock()
		close(s.closed)
	})
}

func (s *Stream) Read(p []byte) (int, error) {
	s.mu.Lock()
	if len(s.buf) > 0 {
		n := copy(p, s.buf)
		s.buf = s.buf[n:]
		s.mu.Unlock()
		return n, nil
	}
	s.mu.Unlock()

	select {
	case chunk := <-s.incoming:
		s.mu.Lock()
		n := copy(p, chunk)
		if n < len(chunk) {
			s.buf = chunk[n:]
		}
		s.mu.Unlock()
		return n, nil
	case <-s.closed:
		// Drain anything already buffered before reporting the end, so a peer
		// that answered and then hung up does not lose its answer.
		select {
		case chunk := <-s.incoming:
			s.mu.Lock()
			n := copy(p, chunk)
			if n < len(chunk) {
				s.buf = chunk[n:]
			}
			s.mu.Unlock()
			return n, nil
		default:
		}
		return 0, s.readErr()
	case <-s.m.done:
		return 0, s.readErr()
	}
}

// readErr reports the end of a stream. A clean close reads as io.EOF, which is
// what every bufio reader above this expects.
func (s *Stream) readErr() error {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err == nil || errors.Is(err, ErrLinkClosed) || errors.Is(err, ErrStreamClosed) {
		return io.EOF
	}
	return err
}

func (s *Stream) Write(p []byte) (int, error) {
	select {
	case <-s.closed:
		return 0, ErrStreamClosed
	case <-s.m.done:
		return 0, ErrLinkClosed
	default:
	}
	written := 0
	for len(p) > 0 {
		n := min(len(p), MaxFramePayload)
		if err := s.m.writeFrame(frameData, s.id, p[:n]); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// Close ends this side of the stream and tells the peer.
func (s *Stream) Close() error {
	already := true
	s.closeOne.Do(func() {
		already = false
		s.mu.Lock()
		if s.err == nil {
			s.err = ErrStreamClosed
		}
		s.mu.Unlock()
		close(s.closed)
	})
	s.m.mu.Lock()
	delete(s.m.streams, s.id)
	s.m.mu.Unlock()
	if already {
		return nil
	}
	return s.m.writeFrame(frameClose, s.id, nil)
}
