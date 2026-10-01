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

// TestGhosttyScan holds the scanner to what it forwards to libghostty and the
// events it fires, and to the property the whole design leans on: any chunking
// of the same stream forwards the same bytes and fires the same events. OSC 52
// and OSC 66 are the numbers withheld here.
func TestGhosttyScan(t *testing.T) {
	withhold := func(n int) bool { return n == 52 || n == 66 }
	for _, tc := range []struct {
		name   string
		in     string
		fwd    string   // what is forwarded; "" when only chunking is checked
		events []string // nil when only chunking is checked
	}{
		{
			name:   "a plain stream is forwarded whole",
			in:     "hello \x1b[1;32mworld\x1b[0m\r\nnext ▀ line \x1b[38;2;1;2;3mX",
			fwd:    "hello \x1b[1;32mworld\x1b[0m\r\nnext ▀ line \x1b[38;2;1;2;3mX",
			events: []string{`csi:..m:"1;32"`, `csi:..m:"0"`, `csi:..m:"38;2;1;2;3"`},
		},
		{
			// Kitty APC and sixel are gone; the non-kitty APC survives.
			name:   "kitty and sixel are withheld",
			in:     "a\x1b_Gf=100,a=T;QUJD\x1b\\b\x1bP0;1;0q#0;2;0;0;0-\x1b\\c\x1b_other\x1b\\d",
			fwd:    "abc\x1b_other\x1b\\d",
			events: []string{`kitty:"Gf=100,a=T;QUJD"`, `sixel:"0;1;0":"#0;2;0;0;0-"`},
		},
		{
			name:   "an OSC is forwarded or withheld by number",
			in:     "x\x1b]0;title\ay\x1b]66;s=2;Big\az",
			fwd:    "x\x1b]0;title\ayz",
			events: []string{`osc:0:"0;title"`, `osc:66:"66;s=2;Big"`},
		},
		{
			name:   "ST terminates an OSC",
			in:     "x\x1b]133;A\x1b\\y",
			fwd:    "x\x1b]133;A\x1b\\y",
			events: []string{`osc:133:"133;A"`},
		},
		{
			// Cyrillic А is D0 90 (0x90 = 8-bit DCS), Ü is C3 9C (0x9C =
			// 8-bit ST). Neither may open or close a sequence.
			name:   "UTF-8 with C1 lookalike bytes",
			in:     "А Ü \x1b]0;tÜtle\a done",
			fwd:    "А Ü \x1b]0;tÜtle\a done",
			events: []string{`osc:0:"0;tÜtle"`},
		},
		{
			// DECRQSS: DCS $ q m ST has an intermediate, so it is not sixel
			// and is forwarded byte for byte.
			name:   "a DCS that is not sixel passes through",
			in:     "x\x1bP$qm\x1b\\y",
			fwd:    "x\x1bP$qm\x1b\\y",
			events: []string{},
		},
		{
			name:   "CSI events",
			in:     "\x1b[?1049h\x1b[3;10r\x1b[>1u\x1b[ q",
			fwd:    "\x1b[?1049h\x1b[3;10r\x1b[>1u\x1b[ q",
			events: []string{`csi:?.h:"1049"`, `csi:..r:"3;10"`, `csi:>.u:"1"`, `csi:. q:""`},
		},
		{
			name:   "ESC events",
			in:     "\x1b(0\x1bc\x1b7",
			fwd:    "\x1b(0\x1bc\x1b7",
			events: []string{`esc:(0`, `esc:.c`, `esc:.7`},
		},
		{
			// The withheld payload disappears, and the CSI still parses.
			name:   "an OSC aborted by a CSI",
			in:     "x\x1b]0;half\x1b[2Jy",
			fwd:    "x\x1b[2Jy",
			events: []string{`csi:..J:"2"`},
		},
		{
			name: "a mixed stream",
			in: "plain А Ü text\x1b[1;31mred\x1b[0m\x1b_Gf=32,s=2,v=2,a=T;AAAA\x1b\\tail" +
				"\x1b]133;B\a\x1bP0q##\x1b\\\x1b]0;tit\x1b\\\x1b(B\x1b[?2026h\x1b[?2026l" +
				"\x1b]52;c;?\a\x1bP+q544e\x1b\\mid\x1b[38;2;9;9;9mZ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := []byte(tc.in)
			whole := scanAll(in, len(in), withhold)
			if tc.events != nil {
				if got := whole.forwarded.String(); got != tc.fwd {
					t.Fatalf("forwarded = %q, want %q", got, tc.fwd)
				}
				if fmt.Sprint(whole.events) != fmt.Sprint(tc.events) || len(whole.events) != len(tc.events) {
					t.Fatalf("events = %q, want %q", whole.events, tc.events)
				}
			}
			for _, chunk := range []int{1, 2, 3, 7, 16} {
				got := scanAll(in, chunk, withhold)
				if got.forwarded.String() != whole.forwarded.String() {
					t.Fatalf("chunk=%d forwarded = %q, want %q", chunk, got.forwarded.String(), whole.forwarded.String())
				}
				if fmt.Sprint(got.events) != fmt.Sprint(whole.events) {
					t.Fatalf("chunk=%d events = %v, want %v", chunk, got.events, whole.events)
				}
			}
		})
	}
}
