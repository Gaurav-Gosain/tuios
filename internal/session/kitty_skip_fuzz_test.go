package session

import (
	"bytes"
	"fmt"
	"testing"
)

// FuzzFrameSkipping drives broadcast with a generated pane stream and three
// clients: one that takes everything at once, one that falls behind, and one
// that subscribes partway. The ways it can fail, written down first:
//
//  1. A client that keeps up loses, doubles or reorders a byte.
//  2. A client that falls behind is handed part of a frame, or text out of
//     order, or loses text.
//  3. A frame is dropped that something after it depends on: the last frame
//     of an image, a frame a placement or delete came after, or a frame that
//     moved the cursor before text that did not set it.
//  4. A client that subscribes partway gets bytes the catch-up gave it again,
//     or misses bytes between the catch-up and the live stream.
//  5. The accounting drifts: after everything is taken a client still counts
//     bytes or frames waiting, so it would hold the pane forever.
func FuzzFrameSkipping(f *testing.F) {
	f.Add([]byte{1, 1, 1, 1, 0, 1, 2, 1}, uint8(3), uint8(7), uint8(2))
	f.Add([]byte{1, 4, 1, 5, 1, 3, 1, 1, 2, 1}, uint8(1), uint8(50), uint8(0))
	f.Add([]byte{6, 1, 6, 1, 6, 1, 0, 6, 1}, uint8(9), uint8(200), uint8(4))
	f.Add([]byte{7, 1, 7, 1, 1, 1}, uint8(2), uint8(13), uint8(1))
	// A client that takes nothing until the end, in small and large reads.
	f.Add([]byte{1, 0, 1, 3, 1, 6, 1, 9, 1, 0, 5, 4, 1, 3}, uint8(0), uint8(9), uint8(3))
	f.Add([]byte{2, 3, 2, 6, 2, 9, 2, 12, 2, 15}, uint8(0), uint8(63), uint8(0))

	f.Fuzz(func(t *testing.T, ops []byte, slowEvery, readSize, joinAt uint8) {
		if len(ops) > 64 {
			ops = ops[:64]
		}
		stream := genPaneStream(ops)
		size := int(readSize)%64 + 1

		p := queuePTY()
		fastCh := p.Subscribe("fast", 0)
		slowCh := p.Subscribe("slow", 0)
		fast, slow := p.subscriberFor("fast"), p.subscriberFor("slow")
		var fastOut, slowOut, lateOut []byte
		var lateCh <-chan ptyChunk
		var late *ptySubscriber
		var lateFrom int64

		drain := func(ch <-chan ptyChunk, sub *ptySubscriber, out []byte) []byte {
			for len(ch) > 0 {
				out = takeChunk(out, <-ch, sub)
			}
			return out
		}
		reads := 0
		for i := 0; i < len(stream); i += size {
			p.feedRing(stream[i:min(i+size, len(stream))])
			reads++
			fastOut = drain(fastCh, fast, fastOut)
			if slowEvery > 0 && reads%int(slowEvery) == 0 {
				slowOut = drain(slowCh, slow, slowOut)
			}
			if reads == int(joinAt) {
				p.outputMu.RLock()
				lateFrom = p.outputSeq - int64(p.outputPos)
				p.outputMu.RUnlock()
				lateCh = p.Subscribe("late", 0)
				late = p.subscriberFor("late")
			}
			if lateCh != nil {
				lateOut = drain(lateCh, late, lateOut)
			}
		}
		// The pane writes nothing more, so whatever the scanner carries
		// stays with it: compare against the bytes broadcast handed out.
		handed := stream[:len(stream)-len(p.gfx.carry)]
		slowOut = drain(slowCh, slow, slowOut)

		if !bytes.Equal(fastOut, handed) {
			t.Fatalf("the client that kept up got %d bytes, want %d\n got %q\nwant %q",
				len(fastOut), len(handed), fastOut, handed)
		}
		// A client that joined after the last read also has the carry, from
		// the ring.
		if lateCh != nil && (!bytes.HasPrefix(stream[lateFrom:], lateOut) || int64(len(lateOut)) < int64(len(handed))-lateFrom) {
			t.Fatalf("the client that subscribed at %d got\n%q\nwant\n%q", lateFrom, lateOut, handed[lateFrom:])
		}
		checkSkipped(t, handed, slowOut)
		for name, sub := range map[string]*ptySubscriber{"fast": fast, "slow": slow, "late": late} {
			if sub == nil {
				continue
			}
			if q, n := sub.queued.Load(), sub.framesWaiting.Load(); q != 0 || n != 0 {
				t.Fatalf("after everything was taken the %s client still counts %d bytes and %d frames", name, q, n)
			}
		}
	})
}

// genPaneStream turns ops into a pane's output: text, cursor moves, frames of
// a few images in one or several chunks, and placements. Every frame's
// payload is unique, so a dropped frame cannot be mistaken for another.
func genPaneStream(ops []byte) []byte {
	var b bytes.Buffer
	n := 0
	for i := 0; i < len(ops); i++ {
		arg := 0
		if i+1 < len(ops) {
			arg = int(ops[i+1])
		}
		switch ops[i] % 8 {
		case 0:
			fmt.Fprintf(&b, "text %d \x1b[1mbold\x1b[0m\r\n", arg)
		case 1, 2:
			n++
			payload := fmt.Sprintf("FRAME%04dPAYLOAD%s", n, bytes.Repeat([]byte("A"), arg%40))
			id := 1 + arg%3
			b.WriteString("\x1b[H")
			writeFrame(&b, fmt.Sprintf("a=T,f=32,s=1,v=1,i=%d,C=1,q=2", id), payload, 1+arg%3)
		case 3:
			fmt.Fprintf(&b, "\x1b_Ga=p,i=%d,q=2\x1b\\", 1+arg%3)
		case 4:
			fmt.Fprintf(&b, "\x1b[%d;%dH", 1+arg%20, 1+arg%60)
		case 5:
			// An a=T that moves the cursor, followed by text or not.
			n++
			writeFrame(&b, fmt.Sprintf("a=T,f=32,s=1,v=1,i=%d,q=2", 1+arg%3), fmt.Sprintf("MOVE%04d", n), 1)
		case 6:
			// A transmission with no image id: never replaced.
			n++
			writeFrame(&b, "a=T,f=32,s=1,v=1,C=1,q=2", fmt.Sprintf("NOID%04d", n), 1+arg%2)
		case 7:
			// Text between the chunks of one transmission.
			n++
			fmt.Fprintf(&b, "\x1b_Ga=t,f=32,s=1,v=1,i=%d,m=1,q=2;MID%04d\x1b\\", 1+arg%3, n)
			b.WriteString("between")
			b.WriteString("\x1b_Gm=0;END\x1b\\")
		}
	}
	return b.Bytes()
}

func writeFrame(b *bytes.Buffer, keys, payload string, chunks int) {
	per := (len(payload) + chunks - 1) / chunks
	for c := 0; c < chunks; c++ {
		part := payload[min(c*per, len(payload)):min((c+1)*per, len(payload))]
		m := 0
		if c < chunks-1 {
			m = 1
		}
		if c == 0 {
			fmt.Fprintf(b, "\x1b_G%s,m=%d;%s\x1b\\", keys, m, part)
		} else {
			fmt.Fprintf(b, "\x1b_Gm=%d;%s\x1b\\", m, part)
		}
	}
}

// checkSkipped checks that got is stream with some whole frames left out, and
// that each frame left out was safe to leave out.
func checkSkipped(t *testing.T, stream, got []byte) {
	t.Helper()
	var s gfxScanner
	segs, _ := s.scan(stream, int64(len(stream)))
	type item struct {
		from, to int
		frame    bool // a whole frame in one run
		id       uint32
		moves    bool
		pins     bool
	}
	var items []item
	for i := 0; i < len(segs); i++ {
		sg := segs[i]
		from := int(sg.end) - len(sg.b)
		if sg.frame && sg.first {
			j := i
			for !segs[j].last && j+1 < len(segs) && segs[j+1].frame && !segs[j+1].first {
				j++
			}
			if segs[j].last {
				items = append(items, item{from: from, to: int(segs[j].end), frame: true, id: sg.id, moves: sg.moves})
				i = j
				continue
			}
		}
		items = append(items, item{from: from, to: int(sg.end), pins: sg.pins})
	}

	at := 0
	for k, it := range items {
		want := stream[it.from:it.to]
		if bytes.HasPrefix(got[at:], want) {
			at += len(want)
			continue
		}
		if !it.frame || it.id == 0 {
			t.Fatalf("the slow client is missing bytes %d-%d that are not a whole frame: %q\n got from there: %q",
				it.from, it.to, want, got[at:min(at+80, len(got))])
		}
		// Left out. Something later must replace it, with nothing between
		// that depends on it.
		replaced := false
		for m, next := range items[k+1:] {
			if next.pins {
				break
			}
			if m == 0 && it.moves && (next.frame || !setsCursor(stream[next.from:next.to])) {
				break
			}
			if next.frame && next.id == it.id {
				replaced = true
				break
			}
		}
		if !replaced {
			t.Fatalf("the slow client lost frame %d-%d of image %d, which nothing replaced", it.from, it.to, it.id)
		}
	}
	if at != len(got) {
		t.Fatalf("the slow client got %d bytes more than the stream holds: %q", len(got)-at, got[at:])
	}
}
