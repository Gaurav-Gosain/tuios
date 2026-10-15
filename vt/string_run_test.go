package vt

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// Emulator.Write takes a run of APC or DCS payload bytes in one copy
// (seqParser.putRun) instead of one byte at a time through the state machine.
// What these tests hold it to is that nothing a guest can observe changes:
// every APC and DCS dispatch carries the bytes, command and parameters the
// byte-at-a-time parser produces, however the stream is split across writes.
//
// The ways the bulk path can go wrong, written down before the tests:
//   - a byte that is not plain payload is swallowed into the run: a
//     terminator (ESC, or the 8-bit ST if it were honoured), CAN or SUB,
//     which abort, or a C0 control or DEL, which take the table's path;
//   - a byte that is payload is left out of the run or counted twice, at the
//     start of the run, at its end, or at the end of a write;
//   - the run grows the buffer past dataCap, or keeps fewer bytes than put()
//     would before the cap;
//   - the run is taken in a state where a payload byte is not a plain put,
//     such as DCS entry, where the first byte starts the string;
//   - a write that ends inside a run leaves the parser in a different state.

// stringDispatch is one APC or DCS dispatch as a guest can observe it.
type stringDispatch struct {
	kind   string
	cmd    ansi.Cmd
	params string
	data   string
}

func (d stringDispatch) String() string {
	return fmt.Sprintf("%s cmd=%d params=%s data=%q", d.kind, d.cmd, d.params, d.data)
}

func paramString(params ansi.Params) string {
	var b strings.Builder
	for _, p := range params {
		fmt.Fprintf(&b, "%d;", p.Param(-1))
	}
	return b.String()
}

// recordStrings wraps h so that every APC and DCS dispatch is appended to out
// before h sees it.
func recordStrings(h ansi.Handler, out *[]stringDispatch) ansi.Handler {
	apc, dcs := h.HandleApc, h.HandleDcs
	h.HandleApc = func(data []byte) {
		*out = append(*out, stringDispatch{kind: "APC", data: string(data)})
		if apc != nil {
			apc(data)
		}
	}
	h.HandleDcs = func(cmd ansi.Cmd, params ansi.Params, data []byte) {
		*out = append(*out, stringDispatch{kind: "DCS", cmd: cmd, params: paramString(params), data: string(data)})
		if dcs != nil {
			dcs(cmd, params, data)
		}
	}
	return h
}

// byteAtATime runs in through a bare parser one byte at a time, with no bulk
// path, and returns what it dispatched and the state it ends in.
func byteAtATime(in []byte, dataCap int) ([]stringDispatch, byte) {
	var out []stringDispatch
	p := newSeqParser(dataCap)
	p.SetHandler(recordStrings(ansi.Handler{}, &out))
	for _, b := range in {
		p.Advance(b)
	}
	return out, p.State()
}

// throughEmulator writes in to an emulator split at the given offsets and
// returns what its parser dispatched and the state it ends in.
func throughEmulator(in []byte, dataCap int, cuts []int) ([]stringDispatch, byte) {
	var out []stringDispatch
	e := NewEmulator(20, 4)
	e.parser.SetDataCap(dataCap)
	e.parser.SetHandler(recordStrings(e.parser.handler, &out))
	prev := 0
	for _, c := range cuts {
		_, _ = e.Write(in[prev:c])
		prev = c
	}
	_, _ = e.Write(in[prev:])
	return out, e.parser.State()
}

// splits returns the ways a case is cut across writes: whole, one byte per
// write, three bytes per write, and at every single offset.
func splits(n int) [][]int {
	out := [][]int{nil}
	var one, three []int
	for i := 1; i < n; i++ {
		one = append(one, i)
		if i%3 == 0 {
			three = append(three, i)
		}
	}
	out = append(out, one, three)
	for i := 1; i < n; i++ {
		out = append(out, []int{i})
	}
	return out
}

func TestConform_StringPayloadRuns(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		dataCap int
		want    []stringDispatch
	}{
		{
			name: "an APC ended by ESC backslash arrives whole",
			in:   "A\x1b_Ga=T,f=32;QUJDRA==\x1b\\B",
			want: []stringDispatch{{kind: "APC", data: "Ga=T,f=32;QUJDRA=="}},
		},
		{
			// BEL ends an OSC, not an APC or a DCS: in those it is a C0
			// control the table puts into the string.
			name: "BEL inside an APC is payload",
			in:   "\x1b_Gq=2;AA\x07BB\x1b\\",
			want: []stringDispatch{{kind: "APC", data: "Gq=2;AA\x07BB"}},
		},
		{
			name: "CAN aborts an APC and nothing is dispatched",
			in:   "\x1b_Gpayload\x18after\x1b_Gnext\x1b\\",
			want: []stringDispatch{{kind: "APC", data: "Gnext"}},
		},
		{
			name: "SUB aborts an APC and nothing is dispatched",
			in:   "\x1b_Gpayload\x1aafter\x1b_Gnext\x1b\\",
			want: []stringDispatch{{kind: "APC", data: "Gnext"}},
		},
		{
			// A UTF-8 terminal does not honour 8-bit C1 controls, so 0x9c
			// (ST), 0x90 (DCS) and 0x9f (APC) are payload, as is the middle
			// byte of U+2733.
			name: "C1 bytes inside an APC are payload",
			in:   "\x1b_G\x9c\x90\x9f\xe2\x9c\xb3\xff\x1b\\",
			want: []stringDispatch{{kind: "APC", data: "G\x9c\x90\x9f\xe2\x9c\xb3\xff"}},
		},
		{
			name: "DEL and C0 controls inside an APC are payload",
			in:   "\x1b_Ga\x7fb\x01c\x1fd\x1b\\",
			want: []stringDispatch{{kind: "APC", data: "Ga\x7fb\x01c\x1fd"}},
		},
		{
			name:    "an APC past the cap is cut at the cap and the next is whole",
			in:      "\x1b_G" + strings.Repeat("x", 40) + "\x1b\\\x1b_Gshort\x1b\\",
			dataCap: 16,
			want: []stringDispatch{
				{kind: "APC", data: "G" + strings.Repeat("x", 15)},
				{kind: "APC", data: "Gshort"},
			},
		},
		{
			name: "a sixel DCS keeps its parameters and payload",
			in:   "\x1bP0;1;0q\"1;1;2;2#0;2;0;0;0#0~~\x1b\\",
			want: []stringDispatch{{kind: "DCS", cmd: 'q', params: "0;1;0;", data: "\"1;1;2;2#0;2;0;0;0#0~~"}},
		},
		{
			name: "C1 bytes, BEL and DEL inside a DCS are payload",
			in:   "\x1bP+q\x9c\x07\x7f\xe2\x9c\xb3\x1b\\",
			want: []stringDispatch{{kind: "DCS", cmd: 'q' | '+'<<parser.IntermedShift, data: "\x9c\x07\x7f\xe2\x9c\xb3"}},
		},
		{
			name: "CAN aborts a DCS and nothing is dispatched",
			in:   "\x1bPqpayload\x18\x1bPqok\x1b\\",
			want: []stringDispatch{{kind: "DCS", cmd: 'q', data: "ok"}},
		},
		{
			name:    "a DCS past the cap is cut at the cap",
			in:      "\x1bPq" + strings.Repeat("~", 40) + "\x1b\\",
			dataCap: 16,
			want:    []stringDispatch{{kind: "DCS", cmd: 'q', data: strings.Repeat("~", 16)}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dataCap := tc.dataCap
			if dataCap == 0 {
				dataCap = maxSequenceData
			}
			in := []byte(tc.in)
			ref, _ := byteAtATime(in, dataCap)
			if !slices.Equal(ref, tc.want) {
				t.Fatalf("the byte-at-a-time parser dispatched\n %v\nwant\n %v", ref, tc.want)
			}
			for _, cuts := range splits(len(in)) {
				got, _ := throughEmulator(in, dataCap, cuts)
				if !slices.Equal(got, tc.want) {
					t.Fatalf("written in pieces cut at %v, the emulator dispatched\n %v\nwant\n %v", cuts, got, tc.want)
				}
			}
		})
	}
}

// FuzzStringPayloadRuns holds the bulk path to the byte-at-a-time parser on
// any input, split at any point, under a small cap so the cut is reached.
func FuzzStringPayloadRuns(f *testing.F) {
	for _, s := range []string{
		"\x1b_Ga=T;QUJD\x1b\\",
		"\x1b_Gq=2;AA\x07BB\x1b\\x",
		"\x1b_Gpay\x18x\x1b_Gn\x1b\\",
		"\x1b_Gpay\x1ax\x1b_Gn\x1b\\",
		"\x1b_G\x9c\x90\x9f\xe2\x9c\xb3\x1b\\",
		"\x1b_Ga\x7fb\x01c\x1b\\",
		"\x1b_G" + strings.Repeat("x", 80) + "\x1b\\\x1b_Gs\x1b\\",
		"\x1bP0;1;0q\"1;1;2;2#0~~\x1b\\",
		"\x1bP+q\x9c\x07\x7f\x1b\\",
		"\x1bPq" + strings.Repeat("~", 80) + "\x1b\\",
		"\x1bPtmux;\x1b\x1b_Gf=24;AAAA\x1b\x1b\\\x1b\\",
		"\x1b]0;t\x07\x1b_Gx\x1b\x1b_Gy\x1b\\",
	} {
		f.Add([]byte(s), uint16(3), uint16(7))
	}
	f.Fuzz(func(t *testing.T, in []byte, a, b uint16) {
		if len(in) > 4096 {
			return
		}
		const dataCap = 64
		var cuts []int
		for _, c := range []int{int(a), int(b)} {
			if len(in) > 0 {
				cuts = append(cuts, c%(len(in)+1))
			}
		}
		slices.Sort(cuts)
		want, wantState := byteAtATime(in, dataCap)
		got, gotState := throughEmulator(in, dataCap, cuts)
		if !slices.Equal(got, want) {
			t.Fatalf("input %q cut at %v: the emulator dispatched\n %v\nthe byte-at-a-time parser\n %v", in, cuts, got, want)
		}
		if gotState != wantState {
			t.Fatalf("input %q cut at %v: the emulator ends in state %d, the byte-at-a-time parser in %d", in, cuts, gotState, wantState)
		}
	})
}
