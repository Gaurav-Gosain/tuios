package session

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// The other half of the snapshot's cost: what the client pays to read it.
//
// BenchmarkWireTerminalState prices building and encoding a snapshot on the
// daemon. Every one of those bytes is then decoded on the client and applied
// cell by cell into an emulator, and that side had no number. It runs on the
// client's UI goroutine during a workspace switch, so it is paid once per pane
// on the target workspace while the user waits.

// wirePTYTruecolor is wirePTY painted in RGB the way a syntax-highlighting
// editor or an agent harness paints: a new colour every run of eight cells.
// The palette form is the cheap one on the wire: a truecolor cell carries a
// seven-byte hex string each way.
func wirePTYTruecolor(tb testing.TB, cols, rows, scrollback int) *PTY {
	return wirePTYRGB(tb, cols, rows, scrollback, 8)
}

// wirePTYGradient is the worst case for any style table: every cell its own
// colour, which is what an image viewer or a gradient banner leaves behind.
func wirePTYGradient(tb testing.TB, cols, rows, scrollback int) *PTY {
	return wirePTYRGB(tb, cols, rows, scrollback, 1)
}

func wirePTYRGB(tb testing.TB, cols, rows, scrollback, run int) *PTY {
	tb.Helper()
	em := vt.NewEmulator(cols, rows)
	for i := range rows + scrollback {
		var line []byte
		for x := range cols - 1 {
			if x%run == 0 {
				line = fmt.Appendf(line, "\x1b[38;2;%d;%d;%dm", (i*7+x)%256, (x*3)%256, (i*11)%256)
			}
			line = append(line, byte('a'+x%26))
		}
		line = append(line, "\x1b[m\r\n"...)
		if _, err := em.Write(line); err != nil {
			tb.Fatalf("emulator write: %v", err)
		}
	}
	if got := em.ScrollbackLen(); got < scrollback {
		tb.Fatalf("wanted %d scrollback lines, emulator kept %d", scrollback, got)
	}
	return &PTY{ID: "wire-bench", terminal: em, width: cols, height: rows}
}

// BenchmarkWireTerminalStateApply is the client side of one snapshot: decode
// the message and apply it into a fresh emulator, which is what a cold attach
// and a route that rebuilds a window both do. "decode" is the gob alone, and
// "apply" is decode plus ApplyTerminalState.
func BenchmarkWireTerminalStateApply(b *testing.B) {
	codec := GetCodec(CodecGob)
	for _, tc := range []struct {
		name   string
		build  func(testing.TB, int, int, int) *PTY
		depth  int
		packed bool
	}{
		{"cells/palette/screen-only", wirePTY, 0, false},
		{"cells/palette/scrollback-1000", wirePTY, 1000, false},
		{"cells/truecolor/screen-only", wirePTYTruecolor, 0, false},
		{"cells/truecolor/scrollback-1000", wirePTYTruecolor, 1000, false},
		{"cells/gradient/screen-only", wirePTYGradient, 0, false},
		{"packed/palette/screen-only", wirePTY, 0, true},
		{"packed/palette/scrollback-1000", wirePTY, 1000, true},
		{"packed/truecolor/screen-only", wirePTYTruecolor, 0, true},
		{"packed/truecolor/scrollback-1000", wirePTYTruecolor, 1000, true},
		{"packed/gradient/screen-only", wirePTYGradient, 0, true},
	} {
		pty := tc.build(b, benchWireCols, benchWireRows, tc.depth)
		snapshot := func() *TerminalState {
			st := pty.GetTerminalState(tc.depth, 0)
			if tc.packed {
				st.Pack()
			}
			return st
		}
		data, err := codec.Encode(&TerminalStatePayload{PTYID: pty.ID, State: snapshot()})
		if err != nil {
			b.Fatal(err)
		}
		b.Run(tc.name+"/encode", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := codec.Encode(&TerminalStatePayload{PTYID: pty.ID, State: snapshot()}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(data)), "wire-bytes")
		})
		b.Run(tc.name+"/decode", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var payload TerminalStatePayload
				if err := codec.Decode(data, &payload); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/apply", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var payload TerminalStatePayload
				if err := codec.Decode(data, &payload); err != nil {
					b.Fatal(err)
				}
				em := vt.NewEmulator(benchWireCols, benchWireRows)
				ApplyTerminalState(em, payload.State)
			}
		})
	}
}

// BenchmarkSnapshotPack is the packing itself, apart from gob: what the daemon
// spends turning cells into the packed form and what the client spends
// turning them back.
func BenchmarkSnapshotPack(b *testing.B) {
	for _, tc := range []struct {
		name  string
		build func(testing.TB, int, int, int) *PTY
	}{
		{"palette", wirePTY},
		{"truecolor", wirePTYTruecolor},
		{"gradient", wirePTYGradient},
	} {
		pty := tc.build(b, benchWireCols, benchWireRows, 0)
		b.Run(tc.name+"/pack", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				st := pty.GetTerminalState(0, 0)
				st.Pack()
			}
		})
		packed := pty.GetTerminalState(0, 0)
		packed.Pack()
		b.Run(tc.name+"/unpack", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := unpackRows(packed.PackedScreen, packed.Styles); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
