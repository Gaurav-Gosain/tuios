package session

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// What a client actually costs the wire.
//
// Everything the daemon sends a TUI client is whole-object gob: there is no
// delta encoding anywhere except the raw PTY byte stream. That makes the size
// of each object the whole story, so these benchmarks report wire-bytes as the
// headline number and the encode time as the secondary one.
//
// The sizes here are per message. An attach pays TerminalState once per window,
// so the per-window number is what to multiply by a session's pane count.

// benchWireCols and benchWireRows are the maintainer's real host size, matching
// the render benchmarks. Cell count is what TerminalState scales with.
const (
	benchWireCols = 207
	benchWireRows = 55
)

// wirePTY builds a PTY carrying only what GetTerminalState reads: an emulator,
// and the size it was made at. No shell is forked, because a real one would
// make the scrollback depth a race rather than a parameter, and depth is the
// variable these benchmarks exist to sweep.
func wirePTY(tb testing.TB, cols, rows, scrollback int) *PTY {
	tb.Helper()
	em := vt.NewEmulator(cols, rows)
	// Styled text, one colour run per line: a blank screen would encode to
	// almost nothing and report a cost no real pane has.
	for i := range rows + scrollback {
		line := fmt.Sprintf("line %05d ", i)
		for len(line) < cols-14 {
			line += "content word "
		}
		if _, err := em.Write(fmt.Appendf(nil, "\x1b[38;5;%dm%s\x1b[m\r\n", 16+(i%200), line)); err != nil {
			tb.Fatalf("emulator write: %v", err)
		}
	}
	if got := em.ScrollbackLen(); got < scrollback {
		tb.Fatalf("wanted %d scrollback lines, emulator kept %d", scrollback, got)
	}
	return &PTY{ID: "wire-bench", terminal: em, width: cols, height: rows}
}

// BenchmarkWireTerminalState measures the message a client receives once per
// window on attach, and again on every workspace switch.
//
// The sweep is over scrollback depth because that is the field under
// suspicion: GetTerminalState serialises up to 1000 scrollback lines into
// every reply, and the only consumer of a TerminalState on the client
// (restoreTerminalContent) never reads them. The screen-only figure at depth 0
// is what the client would cost if it were sent only what it uses, so the gap
// between depth 0 and depth 1000 is dead weight, not a payload.
func BenchmarkWireTerminalState(b *testing.B) {
	codec := GetCodec(CodecGob)
	for _, depth := range []int{0, 100, 1000} {
		b.Run(fmt.Sprintf("scrollback-%d", depth), func(b *testing.B) {
			pty := wirePTY(b, benchWireCols, benchWireRows, depth)
			data, err := codec.Encode(&TerminalStatePayload{PTYID: pty.ID, State: pty.GetTerminalState(depth, 0)})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := codec.Encode(&TerminalStatePayload{
					PTYID: pty.ID, State: pty.GetTerminalState(depth, 0),
				}); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(data)), "wire-bytes")
		})
	}
}

// BenchmarkWireTerminalStateCaughtUp is the same message on the path that
// actually dominates: a workspace switch, where the client's own emulator
// survived and already holds the pane's history.
//
// The client merges only the rows that scrolled off while it was away, so rows
// it already has are decoded and thrown away on arrival. "behind-N" is a client
// missing the newest N rows of a 1000-row daemon buffer; "caught-up" is one
// missing none, which is the common case for a pane the user switches back to
// without it having printed anything meanwhile.
func BenchmarkWireTerminalStateCaughtUp(b *testing.B) {
	codec := GetCodec(CodecGob)
	const depth = 1000
	for _, behind := range []int{0, 50, 1000} {
		name := fmt.Sprintf("behind-%d", behind)
		if behind == 0 {
			name = "caught-up"
		}
		b.Run(name, func(b *testing.B) {
			pty := wirePTY(b, benchWireCols, benchWireRows, depth)
			have := depth - behind
			data, err := codec.Encode(&TerminalStatePayload{
				PTYID: pty.ID, State: pty.GetTerminalState(depth, have),
			})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := codec.Encode(&TerminalStatePayload{
					PTYID: pty.ID, State: pty.GetTerminalState(depth, have),
				}); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(data)), "wire-bytes")
		})
	}
}

// BenchmarkWireKeystroke measures what one keypress costs the wire.
//
// A keystroke sends two messages, not one: the raw key bytes, and then a full
// SessionState push, because input.HandleInput calls SyncStateToDaemon after
// every key that might have changed something. The raw part is a fixed frame
// plus the key; the state part scales with the window count and is resent
// whole whatever changed. Reported separately so the fixed cost and the one
// that grows with the session are not averaged into a single misleading number.
func BenchmarkWireKeystroke(b *testing.B) {
	codec := GetCodec(CodecGob)

	b.Run("raw-input", func(b *testing.B) {
		// Header, the 36-byte PTY UUID the binary framing carries, and the key.
		const header = 4 + 1 + 1
		const uuid = 36
		b.ReportMetric(float64(header+uuid+1), "wire-bytes")
	})

	for _, n := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("state-push/windows-%d", n), func(b *testing.B) {
			st := benchState(n)
			data, err := codec.Encode(st)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := codec.Encode(st); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(data)), "wire-bytes")
		})
	}
}

// BenchmarkWireAttach measures the AttachedPayload, the one frame that carries
// the whole session's layout. It is sent once per attach and is the floor of
// what reattaching costs before any pane content is fetched.
func BenchmarkWireAttach(b *testing.B) {
	codec := GetCodec(CodecGob)
	for _, n := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("windows-%d", n), func(b *testing.B) {
			st := benchState(n)
			payload := &AttachedPayload{
				SessionName: "bench", SessionID: "bench-id",
				Width: benchWireCols, Height: benchWireRows,
				WindowCount: n, State: st,
			}
			data, err := codec.Encode(payload)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := codec.Encode(payload); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(data)), "wire-bytes")
		})
	}
}
