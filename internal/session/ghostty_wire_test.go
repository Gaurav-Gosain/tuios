//go:build ghostty

package session

// Wire-snapshot fidelity across emulator implementations: a daemon on one
// backend snapshots, a client on the other restores, and both directions
// must land on the same observable state. This is the daemon-attach path,
// and for the libghostty backend it exercises the synthesized restore.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
	uv "github.com/charmbracelet/ultraviolet"
)

// wireCase writes a stream, snapshots the source, restores into the
// destination and compares. packed picks the form the snapshot takes on the
// wire; a client on this backend may be answered by a daemon of either age,
// so the callers run both.
func runWireCase(t *testing.T, name, stream string, src, dst vt.Terminal, packed bool) {
	t.Helper()
	if _, err := src.Write([]byte(stream)); err != nil {
		t.Fatalf("%s: write: %v", name, err)
	}
	w, h := src.Width(), src.Height()
	state := TerminalStateOf(src, w, h, 200, 0)
	ApplyTerminalState(dst, throughWire(t, state, packed))
	compareEmulators(t, src, dst)
	// Screen text agrees cell by cell.
	for y := 0; y < h; y++ {
		var sb1, sb2 strings.Builder
		for x := 0; x < w; x++ {
			if c := src.CellAt(x, y); c != nil && c.Content != "" {
				sb1.WriteString(c.Content)
			} else {
				sb1.WriteByte(' ')
			}
			if c := dst.CellAt(x, y); c != nil && c.Content != "" {
				sb2.WriteString(c.Content)
			} else {
				sb2.WriteByte(' ')
			}
		}
		a, b := strings.TrimRight(sb1.String(), " "), strings.TrimRight(sb2.String(), " ")
		if a != b {
			t.Errorf("%s: row %d\n src %q\n dst %q", name, y, a, b)
		}
	}
	if a, b := src.ScrollbackLen(), dst.ScrollbackLen(); a != b {
		t.Errorf("%s: scrollback len src=%d dst=%d", name, a, b)
	}
}

// compareScreenText checks the visible text row by row.
func compareScreenText(t *testing.T, name string, src, dst vt.Terminal) {
	t.Helper()
	w, h := src.Width(), src.Height()
	for y := 0; y < h; y++ {
		var sb1, sb2 strings.Builder
		for x := 0; x < w; x++ {
			if c := src.CellAt(x, y); c != nil && c.Content != "" {
				sb1.WriteString(c.Content)
			} else {
				sb1.WriteByte(' ')
			}
			if c := dst.CellAt(x, y); c != nil && c.Content != "" {
				sb2.WriteString(c.Content)
			} else {
				sb2.WriteByte(' ')
			}
		}
		a, b := strings.TrimRight(sb1.String(), " "), strings.TrimRight(sb2.String(), " ")
		if a != b {
			t.Errorf("%s: row %d\n src %q\n dst %q", name, y, a, b)
		}
	}
}

// wireLineText renders one history line for comparison.
func wireLineText(line uv.Line) string {
	var sb strings.Builder
	for _, c := range line {
		if c.Content == "" {
			sb.WriteByte(' ')
			continue
		}
		sb.WriteString(c.Content)
	}
	return strings.TrimRight(sb.String(), " ")
}

// compareScrollbackText checks that dst holds exactly the history src holds,
// line for line. Comparing against src rather than a fixed count keeps the
// ring's cap handled the same way on both sides.
func compareScrollbackText(t *testing.T, name string, src, dst vt.Terminal) {
	t.Helper()
	a, b := src.ScrollbackLen(), dst.ScrollbackLen()
	if a != b {
		t.Errorf("%s: scrollback len src=%d dst=%d", name, a, b)
	}
	bad := 0
	for i := 0; i < min(a, b); i++ {
		s, d := wireLineText(src.ScrollbackLine(i)), wireLineText(dst.ScrollbackLine(i))
		if s != d {
			bad++
			if bad <= 4 {
				t.Errorf("%s: scrollback line %d\n src %q\n dst %q", name, i, s, d)
			}
		}
	}
}

// numbered writes lines prefix-from to prefix-to, each ended by CR LF, so a
// history that shifted or lost its head is visible by number.
func numbered(prefix string, from, to int) string {
	var sb strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&sb, "%s-%d\r\n", prefix, i)
	}
	return sb.String()
}

// runWireCaseAgain is the workspace-switch shape, which is the one every
// surviving emulator takes and the one a single apply into a fresh
// destination never reaches. dst is brought level with src, either by
// following the same bytes (a client that watched the pane) or by a first
// snapshot (a client that attached), src moves on alone, and a second
// snapshot carries only the rows dst lacks. dst must then hold what src
// holds: the history it had, extended by the rows it missed, under the
// screen src shows.
func runWireCaseAgain(t *testing.T, name, stream, more string, src, dst vt.Terminal, followed bool) {
	t.Helper()
	write := func(term vt.Terminal, s string) {
		t.Helper()
		if _, err := term.Write([]byte(s)); err != nil {
			t.Fatalf("%s: write: %v", name, err)
		}
	}
	w, h := src.Width(), src.Height()
	write(src, stream)
	if followed {
		write(dst, stream)
	} else {
		ApplyTerminalState(dst, TerminalStateOf(src, w, h, 10000, 0))
	}
	compareScrollbackText(t, name+" (before the switch)", src, dst)
	write(src, more)
	have := dst.ScrollbackLen()
	if have == 0 {
		t.Fatalf("%s: dst holds no history, so the second apply would take the fresh-emulator branch", name)
	}
	ApplyTerminalState(dst, TerminalStateOf(src, w, h, 10000, have))
	compareEmulators(t, src, dst)
	compareScreenText(t, name, src, dst)
	compareScrollbackText(t, name, src, dst)
}

// wireAgainStreams pairs a stream that leaves history behind with the bytes
// src alone sees afterwards. Each "more" is chosen to expose one way the
// synthesized restore of a surviving emulator can go wrong: history that
// grows, history that stays, live rows that shrink, and the region, origin,
// charset and alternate screen the emulator had in force when the rows it
// missed were typed into it.
var wireAgainStreams = []struct {
	name   string
	stream string
	more   string
}{
	{"history-grows", numbered("OLD", 1, 100) + "visible", numbered("OLD", 101, 400) + "tail"},
	{"history-still", numbered("OLD", 1, 30) + "visible", "\r\x1b[Kv2"},
	{"styled", numbered("OLD", 1, 30) + "\x1b[1;31mred\x1b[0m", numbered("\x1b[44mNEW\x1b[0m", 1, 20) + "\x1b[32mgreen"},
	{"wide", strings.Repeat("日本語\r\n", 12) + "next 中", strings.Repeat("中文\r\n", 8) + "end"},
	{"region-origin", numbered("OLD", 1, 12) + "\x1b[2;4r\x1b[?6hbody", "\x1b[?6l\x1b[r" + numbered("NEW", 1, 8) + "\x1b[2;4r\x1b[?6hbody2"},
	{"charset", numbered("OLD", 1, 12) + "\x1b(0lqk", "\x1b(B\r\n" + numbered("lqk", 1, 8) + "\x1b(0lqk"},
	{"pen", numbered("OLD", 1, 12) + "text\x1b[1;33;44m", "\r\n" + numbered("NEW", 1, 8) + "more\x1b[0;35m"},
	{"altscreen-over-history", numbered("OLD", 1, 12) + "\x1b[?1049h\x1b[Halt content", "\x1b[2;1Hmore alt"},
	{"leaves-altscreen", numbered("OLD", 1, 12) + "\x1b[?1049h\x1b[Halt content", "\x1b[?1049l" + numbered("NEW", 1, 8) + "back"},
	{"enters-altscreen", numbered("OLD", 1, 12) + "shell", numbered("NEW", 1, 8) + "\x1b[?1049h\x1b[Halt content"},
	{"kitty-kbd", numbered("OLD", 1, 12) + "\x1b[>5utext", numbered("NEW", 1, 8) + "more"},
}

func runWireAgain(t *testing.T, newSrc, newDst func() vt.Terminal) {
	t.Helper()
	for _, tc := range wireAgainStreams {
		for _, followed := range []bool{true, false} {
			variant := "attached"
			if followed {
				variant = "followed"
			}
			t.Run(tc.name+"/"+variant, func(t *testing.T) {
				src, dst := newSrc(), newDst()
				defer closeTerminal(src)
				defer closeTerminal(dst)
				runWireCaseAgain(t, tc.name, tc.stream, tc.more, src, dst, followed)
			})
		}
	}
}

func closeTerminal(term vt.Terminal) {
	if c, ok := term.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}

func newPure40x6() vt.Terminal    { return vt.NewEmulator(40, 6) }
func newGhostty40x6() vt.Terminal { return vt.NewGhosttyTerminal(40, 6) }

// TestGhosttyWireAgainFromPure: pure daemon, ghostty client, across a
// workspace switch.
func TestGhosttyWireAgainFromPure(t *testing.T) {
	runWireAgain(t, newPure40x6, newGhostty40x6)
}

// TestGhosttyWireAgainFromGhostty: ghostty daemon, pure client.
func TestGhosttyWireAgainFromGhostty(t *testing.T) {
	runWireAgain(t, newGhostty40x6, newPure40x6)
}

// TestGhosttyWireAgainGhosttyToGhostty: both sides on the library, which is
// what scripts/install.sh builds by default.
func TestGhosttyWireAgainGhosttyToGhostty(t *testing.T) {
	runWireAgain(t, newGhostty40x6, newGhostty40x6)
}

var wireStreams = []struct {
	name   string
	stream string
}{
	{"plain", "hello\r\nworld"},
	{"styled", "\x1b[1;31mred\x1b[0m \x1b[44mblue-bg\x1b[0m\r\nnext"},
	{"scrollback", strings.Repeat("line of history\r\n", 30) + "visible"},
	{"modes", "\x1b[?1000h\x1b[?2004h\x1b[?1hcontent"},
	{"altscreen", "main content\x1b[?1049h\x1b[Halt content"},
	{"scroll-region", "\x1b[2;4rheader\r\nbody"},
	{"pen", "text\x1b[1;33;44m"},
	{"hidden-cursor", "\x1b[?25lhidden"},
	{"charset", "\x1b(0lqk\x1b(B"},
	{"wide", "日本語\r\nnext 中"},
	{"kitty-kbd", "\x1b[>5utext"},
	{"cursor-shape", "\x1b[6 q$ "},
}

// TestGhosttyWireFromPure: pure daemon snapshot restored into a ghostty
// client.
func TestGhosttyWireFromPure(t *testing.T) {
	for _, form := range wireForms {
		for _, tc := range wireStreams {
			t.Run(form.name+"/"+tc.name, func(t *testing.T) {
				src := vt.NewEmulator(40, 6)
				dst := vt.NewGhosttyTerminal(40, 6)
				defer dst.Close()
				runWireCase(t, tc.name, tc.stream, src, dst, form.packed)
			})
		}
	}
}

// TestGhosttyWireFromGhostty: ghostty daemon snapshot restored into a pure
// client.
func TestGhosttyWireFromGhostty(t *testing.T) {
	for _, form := range wireForms {
		for _, tc := range wireStreams {
			t.Run(form.name+"/"+tc.name, func(t *testing.T) {
				src := vt.NewGhosttyTerminal(40, 6)
				defer src.Close()
				dst := vt.NewEmulator(40, 6)
				runWireCase(t, tc.name, tc.stream, src, dst, form.packed)
			})
		}
	}
}

// TestGhosttyWireGhosttyToGhostty: both sides on the library, which is what
// a ghostty-tagged release runs.
func TestGhosttyWireGhosttyToGhostty(t *testing.T) {
	for _, form := range wireForms {
		for _, tc := range wireStreams {
			t.Run(form.name+"/"+tc.name, func(t *testing.T) {
				src := vt.NewGhosttyTerminal(40, 6)
				defer src.Close()
				dst := vt.NewGhosttyTerminal(40, 6)
				defer dst.Close()
				runWireCase(t, tc.name, tc.stream, src, dst, form.packed)
			})
		}
	}
}
