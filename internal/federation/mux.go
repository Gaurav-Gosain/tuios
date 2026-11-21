package federation

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// LinkPreamble is the first line the proxy writes on a fresh link, before any
// frame. It exists because the pipe is not guaranteed to be clean: an ssh
// login can print a banner, a shell rc file can echo, and either would be read
// as a frame header and kill the link with an unreadable error. The hub skips
// lines until it sees this one, so noise ahead of the proxy is discarded and
// noise that never ends is reported as "this host did not answer as a tuios
// link" instead of as a framing failure.
const LinkPreamble = "TUIOS-LINK 1"

const preambleScanLimit = 8 << 10

const streamBufferFrames = 64

const defaultStallLimit = 10 * time.Second

const connectionStallLimit = 30 * time.Second

var (
	// ErrLinkClosed reports use of a mux whose pipe is gone.
	ErrLinkClosed = errors.New("federation: link is closed")
	// ErrStreamClosed reports use of a stream after either end closed it.
	ErrStreamClosed = errors.New("federation: stream is closed")
	// ErrTooManyStreams reports an open refused because the peer already holds
	// the maximum. It bounds what one untrusted peer can make this side
	// allocate.
	ErrTooManyStreams = errors.New("federation: too many open streams")
	// ErrStreamStalled reports a stream dropped because its reader fell too
	// far behind. The link itself survives.
	ErrStreamStalled = errors.New("federation: the stream's reader stopped reading")
)

const maxStreams = 256

const (
	dialerFirstID   = 1
	answererFirstID = 2
	idStride        = 2
)

type mux struct {
	w  io.Writer
	r  io.Reader
	c  io.Closer
	wl *prioLock

	accept func(*Stream)

	stallLimit time.Duration

	onStall func(id uint32)

	lastRead atomic.Int64
	blocked  atomic.Int32

	mu      sync.Mutex
	streams map[uint32]*Stream
	nextID  uint32
	closed  bool
	err     error

	self string

	done chan struct{}
	once sync.Once

	wg sync.WaitGroup
}

func newMux(rwc io.ReadWriteCloser, accept func(*Stream), firstID uint32) *mux {
	return newMuxRW(rwc, rwc, rwc, accept, firstID)
}

func newMuxRW(r io.Reader, w io.Writer, c io.Closer, accept func(*Stream), firstID uint32) *mux {
	return &mux{
		w:          w,
		r:          r,
		c:          c,
		wl:         newPrioLock(),
		accept:     accept,
		stallLimit: defaultStallLimit,
		streams:    make(map[uint32]*Stream),
		nextID:     firstID,
		done:       make(chan struct{}),
	}
}

func (m *mux) alive(quiet time.Duration) bool {
	select {
	case <-m.done:
		return false
	default:
	}
	if m.blocked.Load() > 0 {
		return true
	}
	last := m.lastRead.Load()
	if last == 0 {
		return true
	}
	return time.Since(time.Unix(0, last)) < quiet
}

func (m *mux) Done() <-chan struct{} { return m.done }

func (m *mux) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

func (m *mux) Open() (*Stream, error) { return m.open(0, StreamOpen{}) }

func (m *mux) OpenWithStall(stall time.Duration) (*Stream, error) {
	return m.open(stall, StreamOpen{})
}

func (m *mux) open(stall time.Duration, info StreamOpen) (*Stream, error) {
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
	s.stall = stall
	switch {
	case info.Bulk:
		s.makeBulk(true)
	case info.Window:
		s.makeWindowed(true)
	}
	m.streams[id] = s
	m.mu.Unlock()

	if info.From == "" {
		info.From = m.self
	}
	if err := m.writeFrame(frameOpen, id, info.encode()); err != nil {
		m.dropStream(id)
		return nil, err
	}
	return s, nil
}

func (m *mux) writeFrame(t frameType, id uint32, payload []byte) error {
	return m.writeFrameLane(t, id, payload, true)
}

func (m *mux) writeFrameLane(t frameType, id uint32, payload []byte, urgent bool) error {
	m.wl.lock(urgent)
	defer m.wl.unlock()
	select {
	case <-m.done:
		return ErrLinkClosed
	default:
	}
	return writeFrame(m.w, t, id, payload)
}

func (m *mux) dropStalled(s *Stream) {
	m.mu.Lock()
	delete(m.streams, s.id)
	m.mu.Unlock()
	s.peerClosed(ErrStreamStalled)
	_ = m.writeFrame(frameClose, s.id, nil)
	if m.onStall != nil {
		m.onStall(s.id)
	}
}

func (m *mux) dropStream(id uint32) {
	m.mu.Lock()
	s := m.streams[id]
	delete(m.streams, id)
	m.mu.Unlock()
	if s != nil {
		s.closedByPeer.Store(true)
		s.peerClosed(io.EOF)
	}
}

// ClosedByPeer reports whether the far side closed the stream on purpose, with
// a close frame on a link that is still up. A stream that ended because the
// link dropped reports false, though its reader sees the same end of file.
func (s *Stream) ClosedByPeer() bool { return s.closedByPeer.Load() }

func (m *mux) run() error {
	for {
		f, err := readFrame(m.r)
		if err != nil {
			m.stop(err)
			return err
		}
		m.lastRead.Store(time.Now().UnixNano())
		switch f.Type {
		case frameOpen:
			m.handleOpen(f.Stream, f.Payload)
		case frameData:
			m.handleData(f.Stream, f.Payload)
		case frameClose:
			m.dropStream(f.Stream)
		case frameCredit:
			m.handleCredit(f.Stream, f.Payload)
		}
	}
}

func (m *mux) handleOpen(id uint32, payload []byte) {
	if m.accept == nil {
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
	open := decodeStreamOpen(payload)
	s := newStreamSized(m, id, open.Bulk)
	s.open = open
	switch {
	case open.Bulk:
		s.makeBulk(false)
	case open.Window:
		s.makeWindowed(false)
	}
	m.streams[id] = s
	m.mu.Unlock()
	if s.win != nil {
		_ = m.writeFrame(frameCredit, id, encodeCredit(0))
	}
	m.wg.Go(func() {
		m.accept(s)
	})
}

func (m *mux) handleCredit(id uint32, payload []byte) {
	n, ok := decodeCredit(payload)
	if !ok {
		return
	}
	m.mu.Lock()
	s := m.streams[id]
	m.mu.Unlock()
	if s == nil || s.win == nil {
		return
	}
	if !s.creditsOK.Load() {
		s.creditsOK.Store(true)
		s.win.enable()
	}
	s.win.credit(n)
}

func (m *mux) handleData(id uint32, payload []byte) {
	m.mu.Lock()
	s := m.streams[id]
	m.mu.Unlock()
	if s == nil {
		return
	}
	s.deliver(payload)
}

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

	stall time.Duration

	open StreamOpen

	bulk bool

	win       *window
	creditsOK atomic.Bool
	read      atomic.Uint64
	credited  atomic.Uint64

	closedByPeer atomic.Bool

	mu       sync.Mutex
	buf      []byte
	incoming chan []byte
	over     [][]byte
	queued   int
	space    chan struct{}
	closed   chan struct{}
	closeOne sync.Once
	err      error
}

func newStream(m *mux, id uint32) *Stream {
	return newStreamSized(m, id, false)
}

func newStreamSized(m *mux, id uint32, bulk bool) *Stream {
	frames := streamBufferFrames
	if bulk {
		frames = bulkBufferFrames
	}
	return &Stream{
		m:        m,
		id:       id,
		incoming: make(chan []byte, frames),
		closed:   make(chan struct{}),
	}
}

func (s *Stream) makeBulk(opener bool) {
	s.bulk = true
	if len(s.incoming) == 0 && cap(s.incoming) < bulkBufferFrames {
		s.incoming = make(chan []byte, bulkBufferFrames)
	}
	s.makeWindowed(opener)
}

func (s *Stream) makeWindowed(opener bool) {
	if s.win == nil {
		s.win = newWindow()
	}
	s.space = make(chan struct{}, 1)
	if !opener {
		s.creditsOK.Store(true)
		s.win.enable()
	}
}

// WindowSize is a bulk stream's window now, zero for any other stream.
func (s *Stream) WindowSize() uint64 {
	if s.win == nil {
		return 0
	}
	return s.win.Size()
}

func (s *Stream) noteRead(n int) {
	if s.win == nil || n <= 0 {
		return
	}
	total := s.read.Add(uint64(n))
	if !s.creditsOK.Load() {
		return
	}
	if total-s.credited.Load() < bulkCreditEvery {
		return
	}
	s.credited.Store(total)
	_ = s.m.writeFrame(frameCredit, s.id, encodeCredit(total))
}

func (s *Stream) deliver(payload []byte) {
	if s.creditsOK.Load() && s.deliverWindowed(payload) {
		return
	}
	select {
	case s.incoming <- payload:
		return
	case <-s.closed:
		return
	case <-s.m.done:
		return
	default:
	}
	limit := s.stall
	if limit <= 0 {
		limit = s.m.stallLimit
	}
	s.m.blocked.Add(1)
	defer s.m.blocked.Add(-1)
	t := time.NewTimer(limit)
	defer t.Stop()
	select {
	case s.incoming <- payload:
	case <-s.closed:
	case <-s.m.done:
	case <-t.C:
		s.m.dropStalled(s)
	}
}

const windowRecvLimit = bulkWindowMax + bulkFramePayload

func (s *Stream) deliverWindowed(payload []byte) bool {
	if s.space == nil {
		return false
	}
	if s.enqueue(payload) {
		return true
	}
	limit := s.stall
	if limit <= 0 {
		limit = s.m.stallLimit
	}
	s.m.blocked.Add(1)
	defer s.m.blocked.Add(-1)
	t := time.NewTimer(limit)
	defer t.Stop()
	for {
		select {
		case <-s.space:
			if s.enqueue(payload) {
				return true
			}
		case <-s.closed:
			return true
		case <-s.m.done:
			return true
		case <-t.C:
			s.m.dropStalled(s)
			return true
		}
	}
}

func (s *Stream) enqueue(payload []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queued > 0 && s.queued+len(payload) > windowRecvLimit {
		return false
	}
	s.queued += len(payload)
	if len(s.over) == 0 {
		select {
		case s.incoming <- payload:
			return true
		default:
		}
	}
	s.over = append(s.over, payload)
	return true
}

func (s *Stream) next() ([]byte, bool) {
	var chunk []byte
	select {
	case chunk = <-s.incoming:
	default:
		if len(s.over) == 0 {
			return nil, false
		}
		chunk = s.over[0]
		s.over[0] = nil
		s.over = s.over[1:]
		if len(s.over) == 0 {
			s.over = nil
		}
	}
	s.unqueue(chunk)
	return chunk, true
}

func (s *Stream) peerClosed(cause error) {
	s.closeOne.Do(func() {
		s.mu.Lock()
		if s.err == nil {
			s.err = cause
		}
		s.mu.Unlock()
		close(s.closed)
		if s.win != nil {
			s.win.close()
		}
	})
}

func (s *Stream) Read(p []byte) (int, error) {
	s.mu.Lock()
	if len(s.buf) > 0 {
		n := copy(p, s.buf)
		s.buf = s.buf[n:]
		s.mu.Unlock()
		s.noteRead(n)
		return n, nil
	}
	if chunk, ok := s.next(); ok {
		return s.take(p, chunk), nil
	}
	s.mu.Unlock()

	select {
	case chunk := <-s.incoming:
		s.mu.Lock()
		s.unqueue(chunk)
		return s.take(p, chunk), nil
	case <-s.closed:
	case <-s.m.done:
	}
	s.mu.Lock()
	if chunk, ok := s.next(); ok {
		return s.take(p, chunk), nil
	}
	s.mu.Unlock()
	return 0, s.readErr()
}

func (s *Stream) unqueue(chunk []byte) {
	if s.space == nil {
		return
	}
	s.queued = max(0, s.queued-len(chunk))
	select {
	case s.space <- struct{}{}:
	default:
	}
}

func (s *Stream) take(p, chunk []byte) int {
	n := copy(p, chunk)
	if n < len(chunk) {
		s.buf = chunk[n:]
	}
	s.mu.Unlock()
	s.noteRead(n)
	return n
}

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
	if s.win != nil {
		return s.writeWindowed(p)
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

func (s *Stream) writeWindowed(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := min(len(p), bulkFramePayload)
		if !s.win.reserve(n) {
			return written, ErrStreamClosed
		}
		if err := s.m.writeFrameLane(frameData, s.id, p[:n], !s.bulk); err != nil {
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
		if s.win != nil {
			s.win.close()
		}
	})
	s.m.mu.Lock()
	delete(s.m.streams, s.id)
	s.m.mu.Unlock()
	if already {
		return nil
	}
	return s.m.writeFrame(frameClose, s.id, nil)
}
