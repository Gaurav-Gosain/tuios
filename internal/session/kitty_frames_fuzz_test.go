package session

import (
	"bytes"
	"fmt"
	"testing"
)

// FuzzGfxScanner checks the cut that lets a slow client skip kitty frames. The
// ways it can fail, written down before the code:
//
//  1. A byte is lost, doubled or moved: the segments and the carry do not add
//     up to the input, in order.
//  2. The positions are wrong: a segment's end does not follow the one before
//     it, so a client is resumed or deduplicated at the wrong byte.
//  3. Where the reads fall changes the answer: a frame is found, cut or
//     bounded differently when the same bytes arrive in other pieces. A
//     client would then drop part of a frame, which prints its payload as
//     text.
//  4. A frame's first or last byte is marked wrong, so a client queues half a
//     frame as a whole one.
//  5. The carry grows without bound on input that never ends a command.
func FuzzGfxScanner(f *testing.F) {
	frame := func(id int, payload string, chunks int) string {
		var b bytes.Buffer
		b.WriteString("\x1b[H")
		n := (len(payload) + chunks - 1) / chunks
		for c := 0; c < chunks; c++ {
			part := payload[min(c*n, len(payload)):min((c+1)*n, len(payload))]
			m := 0
			if c < chunks-1 {
				m = 1
			}
			if c == 0 {
				fmt.Fprintf(&b, "\x1b_Ga=T,f=32,s=4,v=4,i=%d,q=2,C=1,m=%d;%s\x1b\\", id, m, part)
			} else {
				fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", m, part)
			}
		}
		return b.String()
	}
	f.Add([]byte(frame(1, "QUJDREVGR0g=", 1)+frame(1, "SUpLTE1OT1A=", 3)), uint64(0x0102030405))
	f.Add([]byte("hello \x1b[31mred\x1b[0m"+frame(7, "AAAA", 2)+"\x1b_Ga=p,i=7\x1b\\tail"), uint64(3))
	f.Add([]byte("\x1b_Ga=t,i=2,m=1;AA\x1b\\text between\x1b_Gm=0;BB\x1b\\"), uint64(1))
	f.Add([]byte("\x1b_Ga=T,i=3;CC\x1b\\\x1b_Ga=T,i=3;DD\x1b\\"), uint64(0xffffffff))
	f.Add([]byte("\x1b_Ga=T,f=100,m=1;AA\x1b\\\x1b_Gm=0;BB\x1b\\\x1b_Xnot graphics\x1b\\\x1b"), uint64(2))
	f.Add(append([]byte("\x1b_G"), bytes.Repeat([]byte("k=1,"), 400)...), uint64(5))
	// A frame on each screen, the switches split across reads.
	f.Add([]byte(frame(1, "AAAA", 1)+"\x1b[?1049h"+frame(1, "BBBB", 2)+"\x1b[?1;1049l\x1bc\x1b[?47h"+frame(1, "CCCC", 1)), uint64(0x3121))

	// An id-less frame, as mpv sends it, in three chunks, with the reads
	// cutting its payloads: every segment of it carries the same key.
	f.Add([]byte("\x1b[3;5H\x1b_Ga=T,f=24,s=2,v=2,C=1,m=1;AAAAAAAA\x1b\\\x1b_Gm=1;BBBBBBBB\x1b\\\x1b_Gm=0;CCCC\x1b\\"), uint64(0x9a9a9a9a))

	f.Fuzz(func(t *testing.T, stream []byte, splits uint64) {
		whole := scanAll(t, stream, nil)
		var cuts []int
		for s := splits; s != 0 && len(cuts) < 16; s >>= 4 {
			cuts = append(cuts, int(s&0xf)+1)
		}
		pieces := scanAll(t, stream, cuts)
		if whole != pieces {
			t.Fatalf("the cut depends on where the reads fall\nwhole:  %s\npieces: %s", whole, pieces)
		}
	})
}

// scanAll scans stream in pieces of the lengths in cuts, cycling, or whole
// when cuts is empty. It checks failures 1, 2, 4 and 5 and returns a
// description of every frame byte, for comparing two ways of reading.
func scanAll(t *testing.T, stream []byte, cuts []int) string {
	t.Helper()
	var s gfxScanner
	var got []byte
	var end int64
	var desc bytes.Buffer
	inFrame := false
	feed := func(piece []byte, pos int64) {
		segs, _ := s.scan(piece, pos)
		for _, sg := range segs {
			if sg.end-int64(len(sg.b)) != end {
				t.Fatalf("a segment starts at %d, the one before ended at %d", sg.end-int64(len(sg.b)), end)
			}
			end = sg.end
			got = append(got, sg.b...)
			if !sg.frame {
				continue
			}
			if sg.first == inFrame {
				t.Fatalf("a frame segment at %d says first=%v while a frame open=%v", end, sg.first, inFrame)
			}
			inFrame = !sg.last
			// The key goes in as a hash: it holds raw bytes, "]" among them.
			fmt.Fprintf(&desc, "[%d-%d id=%d alt=%v moves=%v keep=%v key=%d first=%v last=%v]",
				end-int64(len(sg.b)), end, sg.id, sg.alt, sg.moves, sg.keep, keyHash(sg.key), sg.first, sg.last)
		}
		if len(s.carry) > maxGfxHeader+4 {
			t.Fatalf("the carry holds %d bytes", len(s.carry))
		}
	}
	var pos int64
	for i, c := 0, 0; i < len(stream); c++ {
		n := len(stream) - i
		if len(cuts) > 0 {
			n = min(n, cuts[c%len(cuts)])
		}
		pos += int64(n)
		feed(stream[i:i+n], pos)
		i += n
	}
	got = append(got, s.carry...)
	if !bytes.Equal(got, stream) {
		t.Fatalf("the segments do not add up to the input\n got %q\nwant %q", got, stream)
	}
	// Frame segments are cut at read boundaries, so join the adjacent ones
	// before comparing.
	return joinRuns(desc.String())
}

// joinRuns merges frame segment descriptions that continue one another, so
// two scans that cut the same frame at different reads describe it the same.
func joinRuns(d string) string {
	type run struct {
		from, to           int64
		id                 uint32
		moves, first, last bool
		alt, keep          bool
		key                uint64
	}
	var runs []run
	for _, part := range bytes.Split([]byte(d), []byte("]")) {
		if len(part) == 0 {
			continue
		}
		var r run
		if _, err := fmt.Sscanf(string(part), "[%d-%d id=%d alt=%t moves=%t keep=%t key=%d first=%t last=%t",
			&r.from, &r.to, &r.id, &r.alt, &r.moves, &r.keep, &r.key, &r.first, &r.last); err != nil {
			panic(err)
		}
		// Only segments that describe the frame the same way join. A read
		// that cut a frame and gave the second half other attributes must
		// not compare equal to a read that did not cut it.
		if n := len(runs); n > 0 && runs[n-1].to == r.from && !runs[n-1].last && !r.first &&
			runs[n-1].id == r.id && runs[n-1].alt == r.alt && runs[n-1].moves == r.moves &&
			runs[n-1].keep == r.keep && runs[n-1].key == r.key {
			runs[n-1].to, runs[n-1].last = r.to, r.last
			continue
		}
		runs = append(runs, r)
	}
	return fmt.Sprint(runs)
}

// keyHash stands for an id-less frame's key in a segment description.
func keyHash(key string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= 1099511628211
	}
	return h
}
