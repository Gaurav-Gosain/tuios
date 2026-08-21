package vt

import (
	"bytes"
	"fmt"
	"testing"
)

// scanRecorder captures everything a scanner run produced so runs can be
// compared across chunkings.
type scanRecorder struct {
	forwarded bytes.Buffer
	events    []string
}

func (r *scanRecorder) hooks(withholdOSC func(int) bool) ghosttyScanHooks {
	return ghosttyScanHooks{
		Forward: func(p []byte) { r.forwarded.Write(p) },
		KittyAPC: func(payload []byte) {
			r.events = append(r.events, fmt.Sprintf("kitty:%q", payload))
		},
		SixelDCS: func(params, payload []byte) {
			r.events = append(r.events, fmt.Sprintf("sixel:%q:%q", params, payload))
		},
		OSC: func(number int, payload []byte) bool {
			r.events = append(r.events, fmt.Sprintf("osc:%d:%q", number, payload))
			return withholdOSC == nil || !withholdOSC(number)
		},
		CSI: func(prefix, inter, final byte, params []byte) {
			r.events = append(r.events, fmt.Sprintf("csi:%c%c%c:%q", nz(prefix), nz(inter), final, params))
		},
		ESC: func(inter, final byte) {
			r.events = append(r.events, fmt.Sprintf("esc:%c%c", nz(inter), final))
		},
	}
}

func nz(b byte) byte {
	if b == 0 {
		return '.'
	}
	return b
}

// scanAll runs the scanner over input in the given chunk size.
func scanAll(input []byte, chunk int, withholdOSC func(int) bool) *scanRecorder {
	r := &scanRecorder{}
	s := newGhosttyScanner(r.hooks(withholdOSC))
	for off := 0; off < len(input); off += chunk {
		end := min(off+chunk, len(input))
		s.Scan(input[off:end])
	}
	return r
}

func TestGhosttyScanForwardsPlainStream(t *testing.T) {
	in := []byte("hello \x1b[1;32mworld\x1b[0m\r\nnext ▀ line \x1b[38;2;1;2;3mX")
	r := scanAll(in, len(in), nil)
	if got := r.forwarded.String(); got != string(in) {
		t.Fatalf("forwarded = %q, want %q", got, in)
	}
}

func TestGhosttyScanWithholdsKittyAndSixel(t *testing.T) {
	in := []byte("a\x1b_Gf=100,a=T;QUJD\x1b\\b\x1bP0;1;0q#0;2;0;0;0-\x1b\\c\x1b_other\x1b\\d")
	r := scanAll(in, len(in), nil)
	// Kitty APC and sixel are gone; the non-kitty APC survives.
	want := "abc\x1b_other\x1b\\d"
	if got := r.forwarded.String(); got != want {
		t.Fatalf("forwarded = %q, want %q", got, want)
	}
	wantEvents := []string{
		`kitty:"Gf=100,a=T;QUJD"`,
		`sixel:"0;1;0":"#0;2;0;0;0-"`,
	}
	if len(r.events) != 2 || r.events[0] != wantEvents[0] || r.events[1] != wantEvents[1] {
		t.Fatalf("events = %v, want %v", r.events, wantEvents)
	}
}

func TestGhosttyScanOSCForwardAndWithhold(t *testing.T) {
	in := []byte("x\x1b]0;title\ay\x1b]66;s=2;Big\az")
	r := scanAll(in, len(in), func(n int) bool { return n == 66 })
	want := "x\x1b]0;title\ayz"
	if got := r.forwarded.String(); got != want {
		t.Fatalf("forwarded = %q, want %q", got, want)
	}
}

func TestGhosttyScanOSCSTTerminator(t *testing.T) {
	in := []byte("x\x1b]133;A\x1b\\y")
	r := scanAll(in, len(in), nil)
	if got := r.forwarded.String(); got != string(in) {
		t.Fatalf("forwarded = %q, want %q", got, in)
	}
	if len(r.events) != 1 || r.events[0] != `osc:133:"133;A"` {
		t.Fatalf("events = %v", r.events)
	}
}

func TestGhosttyScanUTF8WithC1Lookalikes(t *testing.T) {
	// Cyrillic А is D0 90 (0x90 = 8-bit DCS), Ü is C3 9C (0x9C = 8-bit ST).
	// Neither may open or close a sequence.
	in := []byte("А Ü \x1b]0;tÜtle\a done")
	r := scanAll(in, len(in), nil)
	if got := r.forwarded.String(); got != string(in) {
		t.Fatalf("forwarded = %q, want %q", got, in)
	}
}

func TestGhosttyScanDCSPassthrough(t *testing.T) {
	// DECRQSS: DCS $ q m ST has an intermediate, so it is not sixel and is
	// forwarded byte for byte.
	in := []byte("x\x1bP$qm\x1b\\y")
	r := scanAll(in, len(in), nil)
	if got := r.forwarded.String(); got != string(in) {
		t.Fatalf("forwarded = %q, want %q", got, in)
	}
	if len(r.events) != 0 {
		t.Fatalf("events = %v, want none", r.events)
	}
}

func TestGhosttyScanCSIEvents(t *testing.T) {
	in := []byte("\x1b[?1049h\x1b[3;10r\x1b[>1u\x1b[ q")
	r := scanAll(in, len(in), nil)
	want := []string{
		`csi:?.h:"1049"`,
		`csi:..r:"3;10"`,
		`csi:>.u:"1"`,
		`csi:. q:""`,
	}
	if len(r.events) != len(want) {
		t.Fatalf("events = %v, want %v", r.events, want)
	}
	for i := range want {
		if r.events[i] != want[i] {
			t.Fatalf("event %d = %q, want %q", i, r.events[i], want[i])
		}
	}
	if got := r.forwarded.String(); got != string(in) {
		t.Fatalf("forwarded = %q, want %q", got, in)
	}
}

func TestGhosttyScanESCEvents(t *testing.T) {
	in := []byte("\x1b(0\x1bc\x1b7")
	r := scanAll(in, len(in), nil)
	want := []string{`esc:(0`, `esc:.c`, `esc:.7`}
	if len(r.events) != len(want) {
		t.Fatalf("events = %v, want %v", r.events, want)
	}
	if got := r.forwarded.String(); got != string(in) {
		t.Fatalf("forwarded = %q, want %q", got, in)
	}
}

// TestGhosttyScanChunkingInvariance is the property the whole design leans
// on: any chunking of the same stream must forward the same bytes and fire
// the same events.
func TestGhosttyScanChunkingInvariance(t *testing.T) {
	in := []byte("plain А Ü text\x1b[1;31mred\x1b[0m\x1b_Gf=32,s=2,v=2,a=T;AAAA\x1b\\tail" +
		"\x1b]133;B\a\x1bP0q##\x1b\\\x1b]0;tit\x1b\\\x1b(B\x1b[?2026h\x1b[?2026l" +
		"\x1b]52;c;?\a\x1bP+q544e\x1b\\mid\x1b[38;2;9;9;9mZ")
	whole := scanAll(in, len(in), func(n int) bool { return n == 52 })
	for _, chunk := range []int{1, 2, 3, 7, 16} {
		got := scanAll(in, chunk, func(n int) bool { return n == 52 })
		if got.forwarded.String() != whole.forwarded.String() {
			t.Fatalf("chunk=%d forwarded = %q, want %q", chunk, got.forwarded.String(), whole.forwarded.String())
		}
		if fmt.Sprint(got.events) != fmt.Sprint(whole.events) {
			t.Fatalf("chunk=%d events = %v, want %v", chunk, got.events, whole.events)
		}
	}
}

func TestGhosttyScanAbortedOSC(t *testing.T) {
	// An OSC aborted by a new CSI: the withheld payload disappears, the CSI
	// still parses.
	in := []byte("x\x1b]0;half\x1b[2Jy")
	r := scanAll(in, len(in), nil)
	if got := r.forwarded.String(); got != "x\x1b[2Jy" {
		t.Fatalf("forwarded = %q", got)
	}
	found := false
	for _, ev := range r.events {
		if ev == `csi:..J:"2"` {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing CSI event: %v", r.events)
	}
}
