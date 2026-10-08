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

// preambleScanLimit bounds how much junk the hub reads while looking for the
// preamble. A remote that never sends it is a misconfiguration, not something
// to keep reading.
const preambleScanLimit = 8 << 10 // 8 KiB

// streamBufferFrames is how many data frames a stream may hold before its
// reader is considered behind. The read loop is shared by every stream on the
// link, so a consumer that stops reading would otherwise stall the control
// stream and every other attach with it. See deliver for what happens instead.
const streamBufferFrames = 64

// defaultStallLimit is how long a full stream may leave the read loop waiting
// before the stream is dropped. A relay whose local client has not drained a
// megabyte-scale backlog in this long is not slow, it is gone.
//
// It is the limit for the control stream, and the control stream alone is what
// the number was chosen for: one call on it is bounded at DefaultCallTimeout,
// so a control stream still full ten seconds later has nobody reading it.
const defaultStallLimit = 10 * time.Second

// connectionStallLimit is the same bound for a relayed connection, which is an
// attached session rather than a listing.
//
// Ten seconds is the wrong number there and it cost the maintainer his session.
// The reader at the end of a relayed connection is a person's terminal, and a
// terminal legitimately stops reading for a while: a large paint, a client
// swapped out under memory pressure, a laptop lid closed and opened. Ten
// seconds of that is not a dead consumer, and dropping the stream for it threw
// away a session the far daemon was still running perfectly.
//
// Thirty seconds is the trade in the other direction. The read loop is shared,
// so a stream this side is waiting on freezes every other stream on the link
// for as long as the wait lasts, and that is what stops the limit being raised
// further. A reader gone for thirty seconds has lost nothing but time, and a
// stream dropped after thirty seconds is one the client reconnects.
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

// maxStreams caps concurrent streams on one link.
//
// It is sized for panes, which are what hold streams for a long time. A pane
// on another machine holds two (its terminal and its report channel), and the
// link also carries the control stream, the fleet stream and every attach. At
// 32 the sixteenth pane on one machine was refused, across every session this
// daemon holds, and in a global session that read as a machine that had
// stopped making terminals. 256 leaves room for over a hundred panes per
// machine. An idle stream costs a map entry and a channel, so the bound still
// does its job of stopping a peer from opening streams without end.
const maxStreams = 256

// Stream ids are split between the two ends of a link: the side that dials
// allocates odd ids, the side that answers allocates even ones. This is the
// usual scheme (ssh and HTTP/2 both do it) and it is load bearing here for one
// specific reason.
//
// If both ends started at 1, the hub's control stream (id 1) and the first
// stream a peer opened would share an id, so handleOpen's duplicate check would
// answer it with a close before the inbound-open refusal above was consulted.
// The peer would see a closed stream either way, which makes the refusal
// untestable: deleting it changes nothing a test could see. The split means an
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
	w io.Writer
	r io.Reader
	c io.Closer
	// wl serialises frames onto the pipe, ordinary streams first. See
	// bulk.go.
	wl *prioLock

	// accept handles an open frame from the peer. Nil means opens are refused.
	accept func(*Stream)

	// stallLimit is how long deliver waits on a full stream before dropping
	// it, for a stream that does not set its own. Tests shorten it.
	stallLimit time.Duration

	// onStall is told which stream was dropped for a reader that stopped
	// reading. It is the only way anyone finds out that this happened, and
	// telling a stalled attach apart from a dead sshd is the whole point of
	// having it. Nil means nobody is listening.
	onStall func(id uint32)

	// lastRead is when the read loop last took a frame off the pipe, in Unix
	// nanoseconds, and blocked counts the read loop's own waits inside
	// deliver. Together they answer one question: is this pipe alive? A
	// control call that timed out does not answer it, and used to be treated
	// as if it did.
	lastRead atomic.Int64
	blocked  atomic.Int32

	mu      sync.Mutex
	streams map[uint32]*Stream
	nextID  uint32
	closed  bool
	err     error

	// self is put in the From of every stream this side opens that does not
	// name one, so the far machine knows which machine's policy applies.
	// Set before the mux is used and never changed.
	self string

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

// alive reports whether the pipe under this mux is still carrying traffic.
//
// It exists because a control call that did not answer in time proves nothing
// about the link. The link is the pipe, and the pipe is alive when a frame came
// off it recently, or when the read loop is parked handing a frame to a stream
// whose reader is behind. Treating a slow answer as a dead machine is what tore
// a working link down and took the person's attached session with it.
//
// A mux that has read nothing at all is reported alive: no frame yet is the
// state of a link that has just come up, not evidence of a dead one.
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

// Done is closed when the mux stops, whatever stopped it.
func (m *mux) Done() <-chan struct{} { return m.done }

// Err reports why the mux stopped, or nil while it runs.
func (m *mux) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// Open starts a new stream and tells the peer to open its end.
func (m *mux) Open() (*Stream, error) { return m.open(0, StreamOpen{}) }

// OpenWithStall is Open for a stream that needs its own limit for a reader
// that falls behind. The limit is set before the stream can receive anything,
// which is why it is a parameter rather than a field written afterwards: the
// read loop may deliver a frame on it the instant the peer answers.
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
	if info.Bulk {
		s.makeBulk(true)
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

// writeFrame serialises one frame onto the pipe, ahead of any bulk frame
// that waits.
func (m *mux) writeFrame(t frameType, id uint32, payload []byte) error {
	return m.writeFrameLane(t, id, payload, true)
}

// writeFrameLane is writeFrame in the given lane: urgent for every ordinary
// frame, not urgent for a bulk stream's data.
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

// dropStalled ends one stream whose reader fell behind, and tells the peer. The
// stream's own Read reports ErrStreamStalled so the relay above it can say why.
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
	open := decodeStreamOpen(payload)
	s := newStreamSized(m, id, open.Bulk)
	s.open = open
	if open.Bulk {
		s.makeBulk(false)
	}
	m.streams[id] = s
	m.mu.Unlock()
	if open.Bulk {
		// The answer that says this side takes part in the window. It goes
		// before the accept handler can write a byte, so the opener hears it
		// ahead of any data.
		_ = m.writeFrame(frameCredit, id, encodeCredit(0))
	}
	m.wg.Go(func() {
		m.accept(s)
	})
}

// handleCredit takes a reader's credit for a bulk stream this side writes.
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
		// The first credit is the far side saying it takes part.
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

	// stall is this stream's own limit, zero meaning the mux's. It is set
	// once, before the stream is registered, so the read loop can read it
	// without a lock. A relayed connection sets it: see connectionStallLimit.
	stall time.Duration

	// open is what the side that opened the stream said about it, on the
	// side that accepted it. It is set before the accept handler runs and not
	// written again.
	open StreamOpen

	// bulk streams only. win holds this side's writes to the far reader's
	// window; creditsOK says the far side takes part, so this side may send
	// credit frames and must keep to the window. read counts the bytes the
	// reader here has taken and credited the last count sent.
	win       *window
	creditsOK atomic.Bool
	read      atomic.Uint64
	credited  atomic.Uint64

	mu       sync.Mutex
	buf      []byte
	incoming chan []byte
	closed   chan struct{}
	closeOne sync.Once
	err      error
}

func newStream(m *mux, id uint32) *Stream {
	return newStreamSized(m, id, false)
}

// newStreamSized makes a stream whose receive buffer fits a bulk stream's
// whole window when bulk is set.
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

// makeBulk turns a stream into a bulk stream. The opener waits for the far
// side's first credit before it keeps to a window or sends credits; the
// accepter knows at once, because only a peer that speaks credits opens one.
func (s *Stream) makeBulk(opener bool) {
	if s.win == nil {
		s.win = newWindow()
	}
	if len(s.incoming) == 0 && cap(s.incoming) < bulkBufferFrames {
		s.incoming = make(chan []byte, bulkBufferFrames)
	}
	if !opener {
		s.creditsOK.Store(true)
		s.win.enable()
	}
}

// Bulk reports whether the stream is a bulk stream.
func (s *Stream) Bulk() bool { return s.win != nil }

// WindowSize is a bulk stream's window now, zero for any other stream.
func (s *Stream) WindowSize() uint64 {
	if s.win == nil {
		return 0
	}
	return s.win.Size()
}

// noteRead counts bytes the reader took off a bulk stream and grants the far
// writer room for more once enough were read.
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

// deliver hands a data frame to the stream's reader.
//
// It runs on the mux's read loop, which every stream on the link shares. A
// reader that is behind makes it wait, which is the only backpressure there is
// on the pipe, and that wait is bounded: a stream still full after stallLimit
// is dropped, alone, and the loop goes on serving the others. Without the bound
// one client that stopped draining its attach would freeze the control stream,
// the listings would time out, and the whole link would be torn down for it.
func (s *Stream) deliver(payload []byte) {
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
	// The read loop is about to wait, and a link whose read loop is waiting is
	// not a link that has gone quiet. Saying so is what stops a control call
	// timing out behind this wait from being read as a dead machine.
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

// peerClosed ends the stream because the far side closed it or the link died.
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
	s.mu.Unlock()

	select {
	case chunk := <-s.incoming:
		return s.take(p, chunk), nil
	case <-s.closed:
	case <-s.m.done:
	}
	// The stream or the whole link has ended. Drain anything already
	// delivered before reporting the end, so a peer that answered and then
	// hung up does not lose its answer. Both arms drain: select picks among
	// ready arms at random, so a link that closed with chunks still queued
	// used to drop them about half the time.
	select {
	case chunk := <-s.incoming:
		return s.take(p, chunk), nil
	default:
	}
	return 0, s.readErr()
}

// take copies a chunk off the stream into p and keeps what does not fit for
// the next Read.
func (s *Stream) take(p, chunk []byte) int {
	s.mu.Lock()
	n := copy(p, chunk)
	if n < len(chunk) {
		s.buf = chunk[n:]
	}
	s.mu.Unlock()
	s.noteRead(n)
	return n
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
	if s.win != nil {
		return s.writeBulk(p)
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

// writeBulk writes a bulk stream's bytes in small frames, behind every other
// stream, inside the window the far reader grants.
func (s *Stream) writeBulk(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := min(len(p), bulkFramePayload)
		if !s.win.reserve(n) {
			return written, ErrStreamClosed
		}
		if err := s.m.writeFrameLane(frameData, s.id, p[:n], false); err != nil {
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
