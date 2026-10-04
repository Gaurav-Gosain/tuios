package vt_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"runtime"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// kittyFrame is one frame of a 513x720 RGBA kitty graphics stream: 1.5 MB of
// base64 sent as a=T in 4096-byte chunks, the shape a compositor streaming a
// window into a pane writes.
func kittyFrame() []byte {
	px := bytes.Repeat([]byte{1, 2, 3, 255}, 513*720)
	enc := base64.StdEncoding.EncodeToString(px)
	var frame bytes.Buffer
	for off := 0; off < len(enc); off += 4096 {
		end := min(off+4096, len(enc))
		more := 0
		if end < len(enc) {
			more = 1
		}
		if off == 0 {
			fmt.Fprintf(&frame, "\x1b_Ga=T,i=7,f=32,s=513,v=720,q=2,m=%d;", more)
		} else {
			fmt.Fprintf(&frame, "\x1b_Gm=%d;", more)
		}
		frame.WriteString(enc[off:end])
		frame.WriteString("\x1b\\")
	}
	return frame.Bytes()
}

// BenchmarkWriteKittyAPC measures how fast the emulator takes one kitty
// graphics frame (see kittyFrame).
func BenchmarkWriteKittyAPC(b *testing.B) {
	data := kittyFrame()
	e := vt.NewEmulator(80, 24)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for b.Loop() {
		_, _ = e.Write(data)
	}
}

// BenchmarkWriteBareAPC is a kitty frame with the graphics prefix taken
// away, so no handler claims it: what it measures is the parser alone.
func BenchmarkWriteBareAPC(b *testing.B) {
	data := bytes.ReplaceAll(kittyFrame(), []byte("\x1b_G"), []byte("\x1b_X"))
	e := vt.NewEmulator(80, 24)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for b.Loop() {
		_, _ = e.Write(data)
	}
}

// BenchmarkCopyKittyFrame is the reference for the APC budget: one copy of
// the bytes of a kitty frame.
func BenchmarkCopyKittyFrame(b *testing.B) {
	data := kittyFrame()
	dst := make([]byte, len(data))
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for b.Loop() {
		copy(dst, data)
	}
}

// TestAPCPayloadCostsAboutACopy is the budget: the parser takes the payload
// of an APC string at a small multiple of the cost of copying it. It took a
// payload one byte at a time through its state machine, at about 170 times
// the cost of the copy, and a pane that streams kitty frames spent half of
// the daemon and of the client there. The bulk path is about 10 times.
func TestAPCPayloadCostsAboutACopy(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("a timing budget; run without -short and without -race")
	}
	apc := testing.Benchmark(BenchmarkWriteBareAPC).NsPerOp()
	cp := testing.Benchmark(BenchmarkCopyKittyFrame).NsPerOp()
	t.Logf("one kitty frame: %d ns through the parser, %d ns to copy", apc, cp)
	if apc > 40*cp {
		t.Errorf("the parser takes %d ns for an APC payload, over 40 times the %d ns of a copy", apc, cp)
	}
}

// TestKittyFrameCopiesPayloadOnce is the allocation budget for a kitty frame
// with a passthrough reading it, as the client has: one copy of the sequence
// for the passthrough, which RawPayload shares, and the decoded image, about
// three quarters of the base64. RawPayload used to be a second copy, three
// times the frame in all.
func TestKittyFrameCopiesPayloadOnce(t *testing.T) {
	data := kittyFrame()
	e := vt.NewEmulator(80, 24)
	e.SetKittyPassthroughFunc(func(*vt.KittyCommand, []byte) {})
	_, _ = e.Write(data)
	const frames = 4
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range frames {
		_, _ = e.Write(data)
	}
	runtime.ReadMemStats(&after)
	alloced := (after.TotalAlloc - before.TotalAlloc) / frames
	per := float64(alloced) / float64(len(data))
	t.Logf("one kitty frame of %d bytes allocates %d bytes (%.2fx)", len(data), alloced, per)
	if per > 2.4 {
		t.Errorf("a kitty frame allocates %.2f times its size, want at most 2.4", per)
	}
}
