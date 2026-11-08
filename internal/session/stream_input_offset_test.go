package session

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Wire compatibility of stream-pane input. An I frame is what every client
// before J frames sends, and its rules must not move: queued in order while
// the queue has room, then refused as busy, with every frame after it, until
// the pane has read what waits. A J frame names its offset and is typed only
// at the offset the stream expects, so a frame a client sent before it went
// back to resend from a refusal is never typed, even once the queue drained.

func TestInputFrameReadsIAndJ(t *testing.T) {
	at, data, ok := inputFrame(paneFrameInput, []byte("abc"))
	if !ok || at != -1 || string(data) != "abc" {
		t.Errorf("an I frame reads as at %d, %q, %v; want -1, the payload whole", at, data, ok)
	}
	j := append(binary.BigEndian.AppendUint64(nil, 42), "xyz"...)
	at, data, ok = inputFrame(paneFrameInputAt, j)
	if !ok || at != 42 || string(data) != "xyz" {
		t.Errorf("a J frame reads as at %d, %q, %v; want 42, xyz", at, data, ok)
	}
	if _, _, ok := inputFrame(paneFrameInputAt, []byte{0, 0, 0}); ok {
		t.Errorf("a J frame shorter than its offset was read")
	}
	if _, _, ok := inputFrame(paneFrameInputAt, binary.BigEndian.AppendUint64(nil, 1<<63)); ok {
		t.Errorf("a J frame whose offset is negative as an int64 was read")
	}
}

// typed is what the pane got: the frames popped, in order.
func typed(q *paneInput) []byte {
	frames, _ := q.pop()
	var out []byte
	for _, f := range frames {
		out = append(out, f...)
		q.written(len(f))
	}
	return out
}

func TestIFramesKeepTheirRules(t *testing.T) {
	var q paneInput
	big := bytes.Repeat([]byte("x"), paneInputBytes/2)
	for i := range 2 {
		if _, _, _, ok := q.push(-1, big); !ok {
			t.Fatalf("I frame %d of a queue with room was refused", i)
		}
	}
	accepted, _, first, ok := q.push(-1, []byte("late"))
	if ok || !first || accepted != int64(paneInputBytes) {
		t.Fatalf("an I frame past a full queue: ok %v first %v accepted %d, want refused, first, %d", ok, first, accepted, paneInputBytes)
	}
	if _, _, first, ok := q.push(-1, []byte("y")); ok || first {
		t.Errorf("a small I frame after a refusal was taken (ok %v, first %v): it would be typed before the refused one", ok, first)
	}
	if got := len(typed(&q)); got != paneInputBytes {
		t.Fatalf("the pane got %d bytes, want the %d queued", got, paneInputBytes)
	}
	if _, _, _, ok := q.push(-1, []byte("z")); !ok {
		t.Errorf("an I frame after the pane read what waited was refused")
	}
}

func TestJFramesAreTypedOnlyAtTheirOffset(t *testing.T) {
	var q paneInput
	if _, _, _, ok := q.push(0, []byte("AAA")); !ok {
		t.Fatalf("a J frame at offset 0 of a new stream was refused")
	}
	accepted, _, first, ok := q.push(10, []byte("BBB"))
	if ok || accepted != 3 || first {
		t.Errorf("a J frame at offset 10 after 3 bytes: ok %v accepted %d first %v, want refused with 3, no run of refusals", ok, accepted, first)
	}
	if _, _, _, ok := q.push(3, []byte("CCC")); !ok {
		t.Errorf("the J frame at the right offset after a stale one was refused")
	}
	if _, _, _, ok := q.push(-1, []byte("DDD")); !ok {
		t.Errorf("an I frame after J frames was refused")
	}
	if _, _, _, ok := q.push(6, []byte("again")); ok {
		t.Errorf("a J frame at offset 6 after an I frame took 3 more bytes (9) was typed")
	}
	if got := string(typed(&q)); got != "AAACCCDDD" {
		t.Errorf("the pane got %q, want AAACCCDDD", got)
	}
}

// The case only offsets catch: a refusal, then the pane reads everything,
// then a frame the client sent before it went back arrives. Without an
// offset it would be typed after the refused bytes were skipped.
func TestAStaleJFrameAfterTheQueueDrainedIsRefused(t *testing.T) {
	var q paneInput
	big := bytes.Repeat([]byte("x"), paneInputBytes)
	if _, _, _, ok := q.push(0, big); !ok {
		t.Fatalf("the first J frame was refused")
	}
	if _, _, _, ok := q.push(int64(paneInputBytes), []byte("refused")); ok {
		t.Fatalf("a J frame past a full queue was taken")
	}
	typed(&q) // the pane reads all that waited
	stale := int64(paneInputBytes) + int64(len("refused"))
	if accepted, _, _, ok := q.push(stale, []byte("stale")); ok || accepted != int64(paneInputBytes) {
		t.Errorf("a stale J frame after the queue drained: ok %v accepted %d, want refused with %d", ok, accepted, paneInputBytes)
	}
	if _, _, _, ok := q.push(int64(paneInputBytes), []byte("refused")); !ok {
		t.Errorf("the resend from accepted was refused")
	}
	if got := string(typed(&q)); got != "refused" {
		t.Errorf("after the drain the pane got %q, want the resend once", got)
	}
}
