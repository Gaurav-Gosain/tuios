package vt

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// TestASCIIRunMatchesPerCharacterPath writes the same random mix of ASCII runs,
// wide characters, cursor moves and scrolls into two emulators. One takes the
// printable-ASCII run path; the other has a cursor callback set, which sends
// every byte through handlePrint one at a time. The run path stores cells
// straight into the row when nothing wide is in the way, and this holds it to
// what the per-character path does, wide characters it lands on included.
func TestASCIIRunMatchesPerCharacterPath(t *testing.T) {
	pieces := []string{"abc", "hello world", "x", "漢字", "漢", "🇬🇧", "e\u0301",
		"\x1b[31m", "\x1b[42m", "\x1b[0m", "\r\n", "\n", "\b\b", "\x1b[4h", "\x1b[4l"}
	rng := rand.New(rand.NewPCG(7, 11))
	for round := range 300 {
		const w, h = 13, 5
		fast := NewEmulator(w, h)
		slow := NewEmulator(w, h)
		slow.SetCallbacks(Callbacks{CursorPosition: func(uv.Position, uv.Position) {}})
		var in strings.Builder
		for range 60 {
			switch rng.IntN(4) {
			case 0:
				in.WriteString("\x1b[" + strconv.Itoa(1+rng.IntN(h)) + ";" + strconv.Itoa(1+rng.IntN(w)) + "H")
			default:
				in.WriteString(pieces[rng.IntN(len(pieces))])
			}
		}
		s := in.String()
		_, _ = fast.WriteString(s)
		_, _ = slow.WriteString(s)
		if got, want := fast.Render(), slow.Render(); got != want {
			t.Fatalf("round %d, input %q:\nrun path\n%s\nper-character path\n%s", round, s, got, want)
		}
		for y := range h {
			for x := range w {
				if got, want := fast.CellAt(x, y), slow.CellAt(x, y); !got.Equal(want) {
					t.Fatalf("round %d, input %q: cell (%d,%d) is %#v on the run path, %#v per character", round, s, x, y, got, want)
				}
			}
		}
	}
}
