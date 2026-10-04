package session

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// These are race regressions for the pacing of a graphics pane: the hold runs
// on the read goroutine while clients come and go on others, and the ways it
// can go wrong are a hold that never ends and a queue that never stops
// growing. The end-to-end behaviour is in e2e/tui/kitty_pacing_test.go.

// pacingPTY is a pane with no process behind it that has just streamed a
// frame, so it counts as a graphics pane.
func pacingPTY(t *testing.T) *PTY {
	t.Helper()
	p := queuePTY()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p.ctx, p.cancel = ctx, cancel
	p.paceWake = make(chan struct{}, 1)
	return p
}

// pacingFrame is one full frame of image 1 in 4 KiB chunks, as kitten icat
// sends it, behind a cursor home.
func pacingFrame(n, size int) []byte {
	var b bytes.Buffer
	payload := bytes.Repeat([]byte{'A' + byte(n%26)}, size)
	b.WriteString("\x1b[H")
	writeFrame(&b, "a=T,f=32,s=8,v=8,i=1,C=1,q=2", string(payload), (size+4095)/4096)
	return b.Bytes()
}

// feedReads hands data to broadcast in reads of at most 16 KiB, as readOutput does.
func (p *PTY) feedReads(data []byte) {
	for len(data) > 0 {
		n := min(len(data), 16<<10)
		p.feedRing(data[:n])
		data = data[n:]
	}
}

// holdReturns runs one hold and reports how long it took, or fails the test
// if it has not returned within limit.
func holdReturns(t *testing.T, p *PTY, limit time.Duration, during func()) time.Duration {
	t.Helper()
	done := make(chan time.Duration, 1)
	start := time.Now()
	go func() {
		p.holdForSlowSubscribers()
		done <- time.Since(start)
	}()
	if during != nil {
		deadline := time.Now().Add(time.Second)
		for !p.holding.Load() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if !p.holding.Load() {
			t.Fatal("the pane was never held")
		}
		during()
	}
	select {
	case d := <-done:
		return d
	case <-time.After(limit):
		t.Fatalf("the hold did not end within %s", limit)
		return 0
	}
}

func TestPacingHoldEndsWhenTheClientLeaves(t *testing.T) {
	prev := maxGraphicsHold
	maxGraphicsHold = 10 * time.Second
	t.Cleanup(func() { maxGraphicsHold = prev })

	for _, leave := range []string{"unsubscribe", "close", "catch up", "gap resume"} {
		t.Run(leave, func(t *testing.T) {
			p := pacingPTY(t)
			ch := p.Subscribe("stuck", 0)
			sub := p.subscriberFor("stuck")
			p.feedReads(pacingFrame(0, 8<<10))
			if !sub.behind() {
				t.Fatal("a client holding an untaken frame is not behind")
			}
			took := holdReturns(t, p, 2*time.Second, func() {
				switch leave {
				case "unsubscribe":
					p.Unsubscribe("stuck")
				case "close":
					p.cancel()
				case "catch up":
					for len(ch) > 0 {
						takeChunk(nil, <-ch, sub)
						p.wakePacer()
					}
				case "gap resume":
					sub.gapped.Store(true)
					for len(ch) > 0 {
						takeChunk(nil, <-ch, sub)
					}
					p.resumeAfterGap("stuck", sub)
				}
			})
			if took > time.Second {
				t.Fatalf("the hold took %s to see the client %s", took, leave)
			}
		})
	}
}

func TestPacingHoldIsCappedOncePerEpisode(t *testing.T) {
	prev := maxGraphicsHold
	maxGraphicsHold = 50 * time.Millisecond
	t.Cleanup(func() { maxGraphicsHold = prev })

	p := pacingPTY(t)
	ch := p.Subscribe("stuck", 0)
	sub := p.subscriberFor("stuck")
	p.feedReads(pacingFrame(0, 8<<10))

	if took := holdReturns(t, p, 2*time.Second, nil); took < maxGraphicsHold {
		t.Fatalf("the first hold ended after %s, before its cap of %s with the client still behind", took, maxGraphicsHold)
	}
	// The cap is spent: the next reads are not held while the same client
	// stays behind, so a stuck client costs the program one cap, not one per
	// read.
	for i := range 5 {
		if took := holdReturns(t, p, time.Second, nil); took > 10*time.Millisecond {
			t.Fatalf("read %d after the cap ran out was held for %s", i, took)
		}
	}
	// Once the client catches up, the next time it falls behind is a new
	// episode with a new cap.
	for len(ch) > 0 {
		takeChunk(nil, <-ch, sub)
	}
	p.holdForSlowSubscribers()
	p.feedReads(pacingFrame(1, 8<<10))
	if took := holdReturns(t, p, 2*time.Second, nil); took < maxGraphicsHold {
		t.Fatalf("a new episode was held for %s, want the full cap of %s", took, maxGraphicsHold)
	}
}

func TestPacingNeedsEveryPacingClientBehind(t *testing.T) {
	prev := maxGraphicsHold
	maxGraphicsHold = 10 * time.Second
	t.Cleanup(func() { maxGraphicsHold = prev })

	p := pacingPTY(t)
	p.Subscribe("stuck", 0)
	fastCh := p.Subscribe("fast", 0)
	fast := p.subscriberFor("fast")
	p.Subscribe("viewer", 0)
	p.SetPacing("viewer", false)
	p.feedReads(pacingFrame(0, 8<<10))
	for len(fastCh) > 0 {
		takeChunk(nil, <-fastCh, fast)
	}
	if took := holdReturns(t, p, time.Second, nil); took > 10*time.Millisecond {
		t.Fatalf("a client that keeps up did not set the pace: held %s", took)
	}

	// With only the viewer and the stuck client, the stuck one holds.
	p.Unsubscribe("fast")
	took := holdReturns(t, p, 2*time.Second, func() { p.Unsubscribe("stuck") })
	if took > time.Second {
		t.Fatalf("held for %s", took)
	}
	// The viewer alone never holds, however far behind it is.
	if took := holdReturns(t, p, time.Second, nil); took > 10*time.Millisecond {
		t.Fatalf("a read-only viewer held the pane for %s", took)
	}
}

func TestPacingTextPaneIsNeverHeld(t *testing.T) {
	prev := maxGraphicsHold
	maxGraphicsHold = 10 * time.Second
	t.Cleanup(func() { maxGraphicsHold = prev })

	p := pacingPTY(t)
	p.Subscribe("stuck", 0)
	sub := p.subscriberFor("stuck")
	line := []byte("\x1b[31mtext flood\x1b[0m " + string(bytes.Repeat([]byte("x"), 200)) + "\r\n")
	for !sub.behind() {
		p.feedReads(bytes.Repeat(line, 64))
	}
	if took := holdReturns(t, p, time.Second, nil); took > 10*time.Millisecond {
		t.Fatalf("a text pane was held for %s", took)
	}
}

// TestStuckClientQueueStaysBounded: a client that takes nothing while a pane
// streams 300 frames of 256 KiB holds only the newest frame, and it gets that
// frame whole.
func TestStuckClientQueueStaysBounded(t *testing.T) {
	p := pacingPTY(t)
	ch := p.Subscribe("stuck", 0)
	sub := p.subscriberFor("stuck")
	const size = 256 << 10
	var peak int64
	for n := range 300 {
		p.feedReads(pacingFrame(n, size))
		peak = max(peak, sub.queued.Load())
	}
	if sub.gapped.Load() {
		t.Fatal("the stuck client was gapped: its queue filled anyway")
	}
	// Two frames: the one waiting and the one being gathered when a read
	// lands. Plus the cursor homes between them.
	if limit := int64(2*size + 64<<10); peak > limit {
		t.Fatalf("the stuck client held up to %d bytes, want at most %d", peak, limit)
	}
	if got := sub.skipped.Load(); got != 299 {
		t.Fatalf("the stuck client skipped %d frames, want 299", got)
	}
	var out []byte
	for len(ch) > 0 {
		out = takeChunk(out, <-ch, sub)
	}
	last := pacingFrame(299, size)
	if !bytes.HasSuffix(out, last) {
		t.Fatalf("the stuck client's queue did not end with the newest frame whole (%d bytes)", len(out))
	}
	if n := bytes.Count(out, []byte("\x1b_Ga=T")); n != 1 {
		t.Fatalf("the stuck client got %d frames, want 1", n)
	}
	if n := bytes.Count(out, []byte("\x1b[H")); n != 300 {
		t.Fatalf("the stuck client got %d cursor homes, want 300: text was dropped", n)
	}
}

// TestFrameDropRacesTheTake: broadcast drops a waiting frame while the stream
// goroutine may be taking it. Exactly one side must win, and the client must
// get every frame it gets whole. Run it with -race.
func TestFrameDropRacesTheTake(t *testing.T) {
	p := pacingPTY(t)
	ch := p.Subscribe("client", 0)
	sub := p.subscriberFor("client")
	const size, frames = 20 << 10, 400
	got := make(chan []byte)
	go func() {
		var out []byte
		for c := range ch {
			out = takeChunk(out, c, sub)
		}
		got <- out
	}()
	for n := range frames {
		p.feedReads(pacingFrame(n, size))
	}
	p.Unsubscribe("client")
	out := <-got

	homes := bytes.Count(out, []byte("\x1b[H"))
	if homes != frames {
		t.Fatalf("the client got %d cursor homes, want %d", homes, frames)
	}
	kept := 0
	for _, part := range bytes.Split(out, []byte("\x1b[H"))[1:] {
		if len(part) == 0 {
			continue // a dropped frame
		}
		kept++
		n := int(part[bytes.IndexByte(part, ';')+1] - 'A')
		if !bytes.Equal(append([]byte("\x1b[H"), part...), pacingFrame(n, size)) {
			t.Fatalf("frame %d of %d reached the client cut", kept, frames)
		}
	}
	if q, n := sub.queued.Load(), sub.framesWaiting.Load(); q != 0 || n != 0 {
		t.Fatalf("after the stream ended the client still counts %d bytes and %d frames", q, n)
	}
	t.Logf("%d of %d frames kept, %d skipped", kept, frames, sub.skipped.Load())
}
