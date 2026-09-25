package session

import (
	"bytes"
	"testing"
)

// queuePTY is a PTY with no process behind it: enough for the ring, the
// subscribers and broadcast.
func queuePTY() *PTY {
	return &PTY{
		ID:           "queue-test-pty",
		subscribers:  make(map[string]*ptySubscriber),
		outputBuffer: make([]byte, 64*1024),
	}
}

// feedRing appends one chunk to the ring and broadcasts it, as readLoop does.
func (p *PTY) feedRing(data []byte) int64 {
	p.outputMu.Lock()
	seq := p.appendToBuffer(data)
	p.outputMu.Unlock()
	p.broadcast(ptyChunk{data: data}, seq)
	return seq
}

// TestSubscriberQueueIsBoundedByBytes floods a stream nobody is taking and
// holds it to maxSubscriberQueue: past the bound the stream is gapped and
// takes nothing more, and once drained it resumes from the ring behind a
// clear, because the ring has rolled past where it stopped.
func TestSubscriberQueueIsBoundedByBytes(t *testing.T) {
	prev := maxSubscriberQueue
	maxSubscriberQueue = 256 << 10
	t.Cleanup(func() { maxSubscriberQueue = prev })

	p := queuePTY()
	ch := p.Subscribe("client-1", 0)
	sub := p.subscriberFor("client-1")
	if sub == nil {
		t.Fatal("no subscriber after Subscribe")
	}
	chunk := bytes.Repeat([]byte("o"), 16<<10)
	for range 64 { // 1 MiB, four times the bound
		p.feedRing(chunk)
	}
	if q := sub.queued.Load(); q > maxSubscriberQueue {
		t.Fatalf("a stream nobody takes holds %d bytes, the bound is %d", q, maxSubscriberQueue)
	}
	if !sub.gapped.Load() {
		t.Fatal("a stream past the bound was not marked gapped")
	}
	held := 0
	for len(ch) > 0 {
		c := <-ch
		held += len(c.data)
		sub.queued.Add(-int64(len(c.data)))
	}
	if held > int(maxSubscriberQueue) {
		t.Fatalf("the channel held %d bytes, the bound is %d", held, maxSubscriberQueue)
	}
	if held == 0 {
		t.Fatal("the channel held nothing: the bound dropped everything")
	}

	// Gapped and drained: nothing more is queued until the stream is rebuilt.
	p.feedRing(chunk)
	if len(ch) != 0 {
		t.Fatal("a gapped stream was handed another chunk, which would paint past the hole")
	}

	nch, nsub := p.resumeAfterGap("client-1")
	if nch == nil || nsub == nil {
		t.Fatal("a drained gapped stream was not rebuilt")
	}
	if nsub.gapped.Load() {
		t.Fatal("the rebuilt stream is still gapped")
	}
	first, ok := <-nch
	if !ok || !bytes.HasPrefix(first.data, resyncPrefix) {
		t.Fatalf("the rebuilt stream did not start with a clear, the ring rolled past where it stopped: %q", head(first.data))
	}
	nsub.queued.Add(-int64(len(first.data)))
	if len(nch) != 0 {
		t.Fatalf("the ring replay came as %d chunks, want one", 1+len(nch))
	}
	if q := nsub.queued.Load(); q != 0 {
		t.Fatalf("after taking the replay the stream still accounts %d bytes", q)
	}

	// Live output flows again.
	next := bytes.Repeat([]byte("n"), 100)
	p.feedRing(next)
	select {
	case c := <-nch:
		if !bytes.Equal(c.data, next) {
			t.Fatalf("after the resume the stream got %q, want the live chunk", head(c.data))
		}
	default:
		t.Fatal("after the resume the stream got nothing")
	}
}

// TestResumeAfterGapWaitsForTheDrain: a gapped stream that still holds chunks
// is left alone, so nothing it holds is thrown away.
func TestResumeAfterGapWaitsForTheDrain(t *testing.T) {
	prev := maxSubscriberQueue
	maxSubscriberQueue = 32 << 10
	t.Cleanup(func() { maxSubscriberQueue = prev })

	p := queuePTY()
	p.Subscribe("client-1", 0)
	chunk := bytes.Repeat([]byte("o"), 16<<10)
	for range 4 {
		p.feedRing(chunk)
	}
	if !p.subscriberFor("client-1").gapped.Load() {
		t.Fatal("the stream is not gapped")
	}
	if ch, _ := p.resumeAfterGap("client-1"); ch != nil {
		t.Fatal("a gapped stream that still holds chunks was rebuilt over them")
	}
	if ch, _ := p.resumeAfterGap("nobody"); ch != nil {
		t.Fatal("a client with no stream was given one")
	}
}

// TestResizeLeavesTheStreamPositionAlone: a resize carries no bytes, so it
// must not move the position a client resumes from. It used to store its
// zero, and a client that switched away after a resize came back to the
// whole ring painted over its screen.
func TestResizeLeavesTheStreamPositionAlone(t *testing.T) {
	p := queuePTY()
	p.Subscribe("client-1", 0)
	sub := p.subscriberFor("client-1")
	seq := p.feedRing(bytes.Repeat([]byte("o"), 1000))
	if got := sub.sent.Load(); got != seq {
		t.Fatalf("after a chunk the position is %d, want %d", got, seq)
	}
	p.broadcast(ptyChunk{width: 100, height: 40}, 0)
	if got := sub.sent.Load(); got != seq {
		t.Fatalf("after a resize the position is %d, want it left at %d", got, seq)
	}
	if got := p.Unsubscribe("client-1"); got != seq {
		t.Fatalf("Unsubscribe returned %d, want %d", got, seq)
	}
}

func head(b []byte) []byte {
	if len(b) > 24 {
		return b[:24]
	}
	return b
}
