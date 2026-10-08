package federation

import (
	"bytes"
	"crypto/sha256"
	"io"
	"sort"
	"sync"
	"testing"
	"time"
)

// slowPipe is one direction of a slow link with a deep buffer in the middle,
// which is what ssh's channel window and the kernel's socket buffers are to a
// link: bytes written go into the buffer at once while it has room, and leave
// it at the link's rate. A scheduler on the writing side cannot reorder what
// already sits in that buffer, which is why a bulk stream needs a window.
type slowPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	cap    int
	rate   int // bytes per second
	closed bool
	out    *io.PipeWriter
	in     *io.PipeReader
}

func newSlowPipe(capacity, rate int) *slowPipe {
	p := &slowPipe{cap: capacity, rate: rate}
	p.cond = sync.NewCond(&p.mu)
	p.in, p.out = io.Pipe()
	go p.drain()
	return p
}

func (p *slowPipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for n < len(b) {
		for len(p.buf) >= p.cap && !p.closed {
			p.cond.Wait()
		}
		if p.closed {
			return n, io.ErrClosedPipe
		}
		k := min(len(b)-n, p.cap-len(p.buf))
		p.buf = append(p.buf, b[n:n+k]...)
		n += k
		p.cond.Broadcast()
	}
	return n, nil
}

func (p *slowPipe) drain() {
	const tick = 2 * time.Millisecond
	per := max(p.rate*int(tick)/int(time.Second), 1)
	for {
		p.mu.Lock()
		for len(p.buf) == 0 && !p.closed {
			p.cond.Wait()
		}
		if p.closed && len(p.buf) == 0 {
			p.mu.Unlock()
			_ = p.out.Close()
			return
		}
		k := min(per, len(p.buf))
		chunk := append([]byte(nil), p.buf[:k]...)
		p.buf = p.buf[k:]
		p.cond.Broadcast()
		p.mu.Unlock()
		if _, err := p.out.Write(chunk); err != nil {
			return
		}
		time.Sleep(tick)
	}
}

func (p *slowPipe) Read(b []byte) (int, error) { return p.in.Read(b) }

func (p *slowPipe) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cond.Broadcast()
	return nil
}

// slowLink joins two muxes over two slow pipes. The answering side runs
// accept for every stream the hub opens.
func slowLink(t *testing.T, capacity, rate int, accept func(*Stream)) *mux {
	t.Helper()
	up := newSlowPipe(capacity, rate)
	down := newSlowPipe(capacity, rate)
	hub := newMuxRW(down, up, nil, nil, dialerFirstID)
	far := newMuxRW(up, down, nil, accept, answererFirstID)
	go func() { _ = hub.run() }()
	go func() { _ = far.run() }()
	t.Cleanup(func() {
		_ = hub.Close()
		_ = far.Close()
		_ = up.Close()
		_ = down.Close()
	})
	return hub
}

// TestABulkCopyLeavesTypingFast is the budget the scheduler is for. A link of
// 8 MB/s with 4 MiB of buffer in the middle, which is about what ssh's window
// and two socket buffers hold, carries a 24 MiB copy from the far machine.
// While it runs, a pane on the same machine echoes one byte at a time. Every
// echo must come back within 150 ms.
//
// Without the window the copy fills the buffer and every echo waits behind
// 4 MiB, half a second at this rate. Without the two lanes it waits behind a
// megabyte frame, an eighth of a second, on every echo. With both, an echo
// waits behind one 64 KiB frame and the window's few hundred kilobytes.
func TestABulkCopyLeavesTypingFast(t *testing.T) {
	const (
		rate     = 8 << 20
		capacity = 4 << 20
		size     = 24 << 20
	)
	payload := bytes.Repeat([]byte("0123456789abcdef"), size/16)
	want := sha256.Sum256(payload)

	hub := slowLink(t, capacity, rate, func(s *Stream) {
		if s.open.Bulk {
			_, _ = s.Write(payload)
			_ = s.Close()
			return
		}
		// A shell that echoes what it is sent.
		buf := make([]byte, 64)
		for {
			n, err := s.Read(buf)
			if err != nil {
				return
			}
			if _, err := s.Write(buf[:n]); err != nil {
				return
			}
		}
	})

	pane, err := hub.open(0, StreamOpen{})
	if err != nil {
		t.Fatalf("open the pane stream: %v", err)
	}
	copyStream, err := hub.open(0, StreamOpen{Bulk: true})
	if err != nil {
		t.Fatalf("open the bulk stream: %v", err)
	}

	got := make(chan [32]byte, 1)
	go func() {
		h := sha256.New()
		_, _ = io.Copy(h, copyStream)
		var sum [32]byte
		copy(sum[:], h.Sum(nil))
		got <- sum
	}()

	// Let the copy fill the link first: an echo before then proves nothing.
	time.Sleep(300 * time.Millisecond)

	var waits []time.Duration
	one := make([]byte, 1)
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		start := time.Now()
		if _, err := pane.Write([]byte{'k'}); err != nil {
			t.Fatalf("type: %v", err)
		}
		if _, err := io.ReadFull(pane, one); err != nil {
			t.Fatalf("echo: %v", err)
		}
		waits = append(waits, time.Since(start))
		time.Sleep(20 * time.Millisecond)
	}

	select {
	case sum := <-got:
		if sum != want {
			t.Fatalf("the copy arrived changed")
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the copy did not finish")
	}

	sort.Slice(waits, func(i, j int) bool { return waits[i] < waits[j] })
	worst := waits[len(waits)-1]
	median := waits[len(waits)/2]
	t.Logf("%d echoes during the copy: median %v, worst %v, window %d KiB", len(waits), median, worst, copyStream.WindowSize()>>10)
	if worst > 150*time.Millisecond {
		t.Fatalf("ASSERTION: an echo took %v during the copy, want under 150ms (median %v)", worst, median)
	}
}

// TestABulkStreamToAnOlderPeerWritesWithoutAWindow is the wire compatibility
// half. A far side that predates bulk streams never sends the credit that says
// it takes part. The opener must then neither wait for credit, which would
// hang the copy at the first window, nor send a credit frame, which that peer
// reads as an unknown frame type and drops the whole link for.
func TestABulkStreamToAnOlderPeerWritesWithoutAWindow(t *testing.T) {
	hubIn, farOut := io.Pipe()
	farIn, hubOut := io.Pipe()
	hub := newMuxRW(hubIn, hubOut, nil, nil, dialerFirstID)
	go func() { _ = hub.run() }()
	t.Cleanup(func() { _ = hub.Close(); _ = farOut.Close(); _ = hubOut.Close() })

	// The older peer: it reads frames and never answers with a credit, and it
	// sends some data on the stream, which the hub reads.
	sawCredit := make(chan bool, 1)
	received := make(chan int, 1)
	go func() {
		total := 0
		var id uint32
		for {
			f, err := readFrame(farIn)
			if err != nil {
				sawCredit <- false
				received <- total
				return
			}
			switch f.Type {
			case frameOpen:
				id = f.Stream
				_ = writeFrame(farOut, frameData, id, bytes.Repeat([]byte("x"), 512<<10))
			case frameCredit:
				sawCredit <- true
				received <- total
				return
			case frameData:
				total += len(f.Payload)
			case frameClose:
				sawCredit <- false
				received <- total
				return
			}
		}
	}()

	s, err := hub.open(0, StreamOpen{Bulk: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// The hub reads what the older peer sent: a credit would go out here.
	if _, err := io.ReadFull(s, make([]byte, 512<<10)); err != nil {
		t.Fatalf("read: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Write(bytes.Repeat([]byte("y"), 2<<20))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("ASSERTION: the write waited for a credit an older peer never sends")
	}
	_ = s.Close()
	if <-sawCredit {
		t.Fatalf("ASSERTION: the hub sent a credit frame to a peer that did not say it takes them")
	}
	if n := <-received; n != 2<<20 {
		t.Fatalf("the peer received %d bytes, want %d", n, 2<<20)
	}
}
