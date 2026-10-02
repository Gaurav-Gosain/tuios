package session

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// FuzzFrameSkipping drives broadcast with a generated pane stream and three
// clients: one that takes everything at once, one that falls behind, and one
// that subscribes partway. The ways it can fail, written down first:
//
//  1. A client that keeps up loses, doubles or reorders a byte.
//  2. A client that falls behind is handed part of a frame, or text out of
//     order, or loses text.
//  3. A frame is dropped that something after it depends on: the last frame
//     of an image, a frame a placement or delete came after, a frame that
//     moved the cursor, a frame that names a file, or a frame with no id that
//     the next one is not drawn exactly over.
//  4. A client that subscribes partway gets bytes the catch-up gave it again,
//     or misses bytes between the catch-up and the live stream.
//  5. The accounting drifts: after everything is taken a client still counts
//     bytes or frames waiting, so it would hold the pane forever.
//  6. A client that falls behind ends with another screen than one that kept
//     up: other text, another cursor, other images. A frame that moved the
//     cursor at the bottom row scrolled the screen, and dropping it lost the
//     scroll even when a cursor move came after it.
func FuzzFrameSkipping(f *testing.F) {
	f.Add([]byte{1, 1, 1, 1, 0, 1, 2, 1}, uint8(3), uint8(7), uint8(2))
	f.Add([]byte{1, 4, 1, 5, 1, 3, 1, 1, 2, 1}, uint8(1), uint8(50), uint8(0))
	f.Add([]byte{6, 1, 6, 1, 6, 1, 0, 6, 1}, uint8(9), uint8(200), uint8(4))
	f.Add([]byte{7, 1, 7, 1, 1, 1}, uint8(2), uint8(13), uint8(1))
	// A client that takes nothing until the end, in small and large reads.
	f.Add([]byte{1, 0, 1, 3, 1, 6, 1, 9, 1, 0, 5, 4, 1, 3}, uint8(0), uint8(9), uint8(3))
	f.Add([]byte{2, 3, 2, 6, 2, 9, 2, 12, 2, 15}, uint8(0), uint8(63), uint8(0))
	// A frame that scrolls at the bottom row, a cursor move, and the same
	// image again.
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 8, 0, 8, 0}, uint8(0), uint8(7), uint8(0))
	// Frames with no id, one after another at one place, then a cursor move
	// between two of them.
	f.Add([]byte{9, 9, 9, 9, 9, 9, 4, 9, 9, 9}, uint8(0), uint8(6), uint8(0))
	// A query between frames.
	f.Add([]byte{1, 1, 10, 1, 1, 1}, uint8(0), uint8(5), uint8(0))

	f.Fuzz(func(t *testing.T, ops []byte, slowEvery, readSize, joinAt uint8) {
		if err := frameSkipping(ops, slowEvery, readSize, joinAt); err != nil {
			t.Fatal(err)
		}
	})
}

// TestFrameSkippingSweep runs the checks of FuzzFrameSkipping over a fixed
// set of random streams, so the ordinary suite covers more than the seeds.
func TestFrameSkippingSweep(t *testing.T) {
	r := rand.New(rand.NewSource(345))
	runs := 3000
	if testing.Short() {
		runs = 300
	}
	failed := 0
	var first error
	for range runs {
		ops := make([]byte, 4+r.Intn(60))
		r.Read(ops)
		err := frameSkipping(ops, uint8(r.Intn(12)), uint8(r.Intn(64)), uint8(r.Intn(8)))
		if err != nil {
			failed++
			if first == nil {
				first = fmt.Errorf("ops %v: %w", ops, err)
			}
		}
	}
	if failed > 0 {
		t.Fatalf("%d of %d streams failed; the first: %v", failed, runs, first)
	}
}

// frameSkipping drives broadcast with the stream ops make and three clients:
// one that takes everything at once, one that falls behind, and one that
// subscribes partway. It returns the first failure it finds.
func frameSkipping(ops []byte, slowEvery, readSize, joinAt uint8) error {
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
		return fmt.Errorf("the client that kept up got %d bytes, want %d\n got %q\nwant %q",
			len(fastOut), len(handed), fastOut, handed)
	}
	// A client that joined after the last read also has the carry, from
	// the ring.
	if lateCh != nil && (!bytes.HasPrefix(stream[lateFrom:], lateOut) || int64(len(lateOut)) < int64(len(handed))-lateFrom) {
		return fmt.Errorf("the client that subscribed at %d got\n%q\nwant\n%q", lateFrom, lateOut, handed[lateFrom:])
	}
	if err := checkSkipped(handed, slowOut); err != nil {
		return err
	}
	for name, sub := range map[string]*ptySubscriber{"fast": fast, "slow": slow, "late": late} {
		if sub == nil {
			continue
		}
		if q, n := sub.queued.Load(), sub.framesWaiting.Load(); q != 0 || n != 0 {
			return fmt.Errorf("after everything was taken the %s client still counts %d bytes and %d frames", name, q, n)
		}
	}
	if fs, ss := replayClient(fastOut), replayClient(slowOut); fs != ss {
		return fmt.Errorf("the client that fell behind ends in another state\n--- kept up\n%s\n--- fell behind\n%s\n--- stream %q", fs, ss, stream)
	}
	return nil
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
		op := ops[i] % 16
		if op > 10 {
			op %= 8
		}
		switch op {
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
			// A transmission with no image id that is not drawn at a known
			// place: never replaced.
			n++
			writeFrame(&b, "a=T,f=32,s=1,v=1,C=1,q=2", fmt.Sprintf("NOID%04d", n), 1+arg%2)
		case 7:
			// Text between the chunks of one transmission.
			n++
			fmt.Fprintf(&b, "\x1b_Ga=t,f=32,s=1,v=1,i=%d,m=1,q=2;MID%04d\x1b\\", 1+arg%3, n)
			b.WriteString("between")
			b.WriteString("\x1b_Gm=0;END\x1b\\")
		case 8:
			// An a=T at the bottom row that moves the cursor past three
			// rows, so the screen scrolls, and a cursor move after it.
			n++
			fmt.Fprintf(&b, "\x1b[%d;1H", replayRows)
			writeFrame(&b, fmt.Sprintf("a=T,f=32,s=1,v=1,i=%d,r=3,c=2,q=2", 1+arg%2), fmt.Sprintf("SCROLL%04d", n), 1+arg%2)
			fmt.Fprintf(&b, "\x1b[%d;1H", replayRows-1)
		case 9:
			// A frame with no id, as mpv sends: a cursor move, then f=24
			// with C=1. Two places, so only some replace each other.
			n++
			fmt.Fprintf(&b, "\x1b[%d;1H", 1+arg%2)
			writeFrame(&b, "a=T,f=24,s=1,v=1,C=1,q=2", fmt.Sprintf("MPV%04d", n), 1+arg%3)
		case 10:
			// A query, which a program sends to learn the protocol.
			fmt.Fprintf(&b, "\x1b_Gi=%d,s=1,v=1,a=q,t=d,f=24,q=2;AAAA\x1b\\", 31+arg%2)
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
func checkSkipped(stream, got []byte) error {
	var s gfxScanner
	segs, _ := s.scan(stream, int64(len(stream)))
	type item struct {
		from, to int
		frame    bool // a whole frame in one run
		id       uint32
		keep     bool
		key      string
		follows  bool
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
				items = append(items, item{from: from, to: int(segs[j].end), frame: true, id: sg.id, keep: sg.keep, key: sg.key, follows: sg.follows})
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
		if !it.frame || it.keep {
			return fmt.Errorf("the slow client is missing bytes %d-%d that are not a frame it may skip: %q\n got from there: %q",
				it.from, it.to, want, got[at:min(at+80, len(got))])
		}
		// Left out. Something later must replace it, with nothing between
		// that depends on it.
		replaced := false
		for m, next := range items[k+1:] {
			if next.pins {
				break
			}
			if it.id == 0 {
				// Only the next frame, with nothing but a cursor move
				// before it.
				if next.frame {
					replaced = next.follows && next.id == 0 && next.key != "" && next.key == it.key
					break
				}
				if m > 0 {
					break
				}
				continue
			}
			if next.frame && next.id == it.id {
				replaced = true
				break
			}
		}
		if !replaced {
			return fmt.Errorf("the slow client lost frame %d-%d of image %d, which nothing replaced", it.from, it.to, it.id)
		}
	}
	if at != len(got) {
		return fmt.Errorf("the slow client got %d bytes more than the stream holds: %q", len(got)-at, got[at:])
	}
	return nil
}

// The screen replayClient lays a stream out on.
const replayCols, replayRows = 40, 10

// replayClient replays what a client got the way a tuios client shows it: the
// emulator for the text, kitty commands put back together from their chunks,
// and the rows a client reserves for an a=T that moves the cursor
// (ReserveImageSpace). It returns the screen, the cursor and the images.
func replayClient(out []byte) string {
	e := vt.NewEmulator(replayCols, replayRows)
	defer e.Close()
	go func() { // take the emulator's replies, so a write never waits on one
		buf := make([]byte, 4096)
		for {
			if _, err := e.Read(buf); err != nil {
				return
			}
		}
	}()
	type loading struct {
		cmd  vt.KittyCommand
		x, y int
		data strings.Builder
	}
	images := map[uint32]string{}
	places := map[[2]uint32]bool{}
	idless := map[string]string{} // place and size of an image with no id
	var ld *loading
	e.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, _ []byte) {
		if ld == nil {
			switch cmd.Action {
			case vt.KittyActionTransmit, vt.KittyActionTransmitPlace:
				pos := e.CursorPosition()
				ld = &loading{cmd: *cmd, x: pos.X, y: pos.Y}
				ld.data.WriteString(cmd.RawPayload)
			case vt.KittyActionPlace:
				if _, ok := images[cmd.ImageID]; ok {
					places[[2]uint32{cmd.ImageID, cmd.PlacementID}] = true
				}
				return
			case vt.KittyActionDelete:
				for k := range places {
					if cmd.Delete == vt.KittyDeleteAll || k[0] == cmd.ImageID {
						delete(places, k)
					}
				}
				return
			default:
				return
			}
		} else {
			ld.data.WriteString(cmd.RawPayload)
		}
		if cmd.More {
			return
		}
		l := ld
		ld = nil
		c := l.cmd
		if c.ImageID == 0 {
			if c.Action == vt.KittyActionTransmitPlace {
				// A newer image at the same cells, of the same size, covers
				// the older one.
				idless[fmt.Sprintf("%d,%d %dx%d f=%d c=%d r=%d", l.x, l.y, c.Width, c.Height, c.Format, c.Columns, c.Rows)] = l.data.String()
			}
		} else {
			images[c.ImageID] = l.data.String()
			for k := range places {
				if k[0] == c.ImageID {
					delete(places, k)
				}
			}
			if c.Action == vt.KittyActionTransmitPlace {
				places[[2]uint32{c.ImageID, c.PlacementID}] = true
			}
		}
		if c.Action == vt.KittyActionTransmitPlace && c.CursorMove == 0 && !c.Virtual && c.Rows > 0 {
			e.ReserveImageSpace(c.Rows, max(c.Columns, 1))
		}
	})
	_, _ = e.Write(out)
	pos := e.CursorPosition()
	return fmt.Sprintf("screen:\n%s\ncursor %d,%d\nimages %s\nplaces %s\nno id %s",
		e.String(), pos.X, pos.Y, sortedMap(images), sortedMap(places), sortedMap(idless))
}

func sortedMap[K comparable, V any](m map[K]V) string {
	var out []string
	for k, v := range m {
		out = append(out, fmt.Sprintf("%v:%v", k, v))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}
