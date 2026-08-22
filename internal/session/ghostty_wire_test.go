//go:build ghostty

package session

// Wire-snapshot fidelity across emulator implementations: a daemon on one
// backend snapshots, a client on the other restores, and both directions
// must land on the same observable state. This is the daemon-attach path,
// and for the libghostty backend it exercises the synthesized restore.

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// wireCase writes a stream, snapshots the source, restores into the
// destination and compares.
func runWireCase(t *testing.T, name, stream string, src, dst vt.Terminal) {
	t.Helper()
	if _, err := src.Write([]byte(stream)); err != nil {
		t.Fatalf("%s: write: %v", name, err)
	}
	w, h := src.Width(), src.Height()
	state := TerminalStateOf(src, w, h, 200, 0)
	ApplyTerminalState(dst, state)
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
	for _, tc := range wireStreams {
		t.Run(tc.name, func(t *testing.T) {
			src := vt.NewEmulator(40, 6)
			dst := vt.NewGhosttyTerminal(40, 6)
			defer dst.Close()
			runWireCase(t, tc.name, tc.stream, src, dst)
		})
	}
}

// TestGhosttyWireFromGhostty: ghostty daemon snapshot restored into a pure
// client.
func TestGhosttyWireFromGhostty(t *testing.T) {
	for _, tc := range wireStreams {
		t.Run(tc.name, func(t *testing.T) {
			src := vt.NewGhosttyTerminal(40, 6)
			defer src.Close()
			dst := vt.NewEmulator(40, 6)
			runWireCase(t, tc.name, tc.stream, src, dst)
		})
	}
}

// TestGhosttyWireGhosttyToGhostty: both sides on the library, which is what
// a ghostty-tagged release runs.
func TestGhosttyWireGhosttyToGhostty(t *testing.T) {
	for _, tc := range wireStreams {
		t.Run(tc.name, func(t *testing.T) {
			src := vt.NewGhosttyTerminal(40, 6)
			defer src.Close()
			dst := vt.NewGhosttyTerminal(40, 6)
			defer dst.Close()
			runWireCase(t, tc.name, tc.stream, src, dst)
		})
	}
}
