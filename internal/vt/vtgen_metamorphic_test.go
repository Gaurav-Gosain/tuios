package vt_test

// Oracles that need nobody to write down the right answer.
//
// The existing generated-input targets check three structural invariants: the
// screen is not negative, the scroll region is inside it, the cursor is inside
// it, and no cell claims more columns than the row has. A campaign measured
// over internal/vt reaches 55% of its statements and 590k executions found
// none of the eleven bugs a conformance round found by hand. That is not
// because the generator fails to produce the sequences - it produces DECSED,
// DECSTBM and the rest thousands of times - but because nothing looks at what
// they did. An emulator that silently does the wrong thing satisfies all four
// invariants.
//
// The two properties below need no expectations. They relate one run of the
// emulator to another run of the same emulator, so they are as strong as the
// emulator is self-consistent, and a violation is a bug by construction.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz/vtgen"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	uv "github.com/charmbracelet/ultraviolet"
)

const (
	metaCols = 40
	metaRows = 12
)

// cellText renders a cell for a mismatch message.
func cellText(c *uv.Cell) string {
	if c == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q w=%d fg=%v bg=%v attrs=%x ul=%d",
		c.Content, c.Width, c.Style.Fg, c.Style.Bg, c.Style.Attrs, c.Style.Underline)
}

// sameCell treats the several spellings of an empty cell as one, the way a
// renderer does: a nil, an empty string and an unstyled space all draw
// nothing.
func sameCell(a, b *uv.Cell) bool {
	blank := func(c *uv.Cell) bool {
		return c == nil || ((c.Content == "" || c.Content == " ") &&
			c.Style.IsZero() && c.Link.URL == "")
	}
	if blank(a) && blank(b) {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	ac, bc := a.Content, b.Content
	if ac == "" {
		ac = " "
	}
	if bc == "" {
		bc = " "
	}
	aw, bw := a.Width, b.Width
	if aw == 0 {
		aw = 1
	}
	if bw == 0 {
		bw = 1
	}
	return ac == bc && aw == bw && a.Style == b.Style && a.Link == b.Link
}

// compareGrids reports the first cell two emulators disagree about.
func compareGrids(a, b *vt.Emulator, aName, bName string) string {
	w, h := a.Width(), a.Height()
	if bw, bh := b.Width(), b.Height(); bw != w || bh != h {
		return fmt.Sprintf("size %s=%dx%d %s=%dx%d", aName, w, h, bName, bw, bh)
	}
	for y := range h {
		for x := range w {
			ac, bc := a.CellAt(x, y), b.CellAt(x, y)
			if !sameCell(ac, bc) {
				return fmt.Sprintf("cell (%d,%d)\n  %-8s %s\n  %-8s %s",
					x, y, aName, cellText(ac), bName, cellText(bc))
			}
		}
	}
	return ""
}

// splitEquivalence is the property that where a read boundary fell cannot
// change what ends up on screen.
//
// A PTY hands the parser whatever the kernel had ready, so the same output
// arrives in different pieces on every run. If the screen depends on the
// pieces, a pane renders differently depending on machine load, which is the
// shape of bug that gets reported as "intermittent" and never reproduced.
// Both runs get the same bytes; only the write boundaries differ.
func splitEquivalence(s vtgen.Script, seed uint64) (broken string) {
	defer func() {
		if r := recover(); r != nil {
			broken = fmt.Sprintf("panic: %v", r)
		}
	}()

	whole := vt.NewEmulator(metaCols, metaRows)
	if _, err := whole.WriteString(s.Bytes()); err != nil {
		return fmt.Sprintf("whole write: %v", err)
	}

	split := vt.NewEmulator(metaCols, metaRows)
	writes := s.SplitWrites(seed)
	for i, w := range writes {
		if _, err := split.WriteString(w); err != nil {
			return fmt.Sprintf("split write %d: %v", i+1, err)
		}
	}

	// The piece count is deliberately kept out of these messages: it changes
	// with every cut the shrinker makes, and brokenSig would then treat each
	// reduction as a different failure and refuse to reduce at all.
	if d := compareGrids(whole, split, "whole", "split"); d != "" {
		return "the same bytes split at different boundaries drew a different screen: " + d
	}
	if wp, sp := whole.CursorPosition(), split.CursorPosition(); wp != sp {
		return fmt.Sprintf("the same bytes split at different boundaries left the cursor elsewhere: whole=%v split=%v", wp, sp)
	}
	_ = writes
	return ""
}

// renderRoundTrip is the property that a frame the emulator emits describes
// the screen the emulator holds.
//
// Render is what the app sends to the host terminal. Nothing else checks that
// it is faithful: the grid tests read cells, and the frame goes out unread. If
// a frame drops an attribute or mis-sizes a wide character, every pane is
// wrong on screen while every test passes, and the round trip catches it
// without anyone predicting which attribute.
//
// The frame is replayed into a fresh emulator of the same size, so what is
// compared is the picture rather than the spelling: several encodings of the
// same colour are all correct, and re-parsing normalises them.
func renderRoundTrip(s vtgen.Script) (broken string) {
	defer func() {
		if r := recover(); r != nil {
			broken = fmt.Sprintf("panic: %v", r)
		}
	}()

	src := vt.NewEmulator(metaCols, metaRows)
	for i, seq := range s {
		if seq.Kind == "resize" {
			src.Resize(seq.Cols, seq.Rows)
			continue
		}
		if _, err := src.WriteString(seq.Bytes); err != nil {
			return fmt.Sprintf("step %d: %v", i+1, err)
		}
	}

	w, h := src.Width(), src.Height()
	if w <= 0 || h <= 0 {
		return ""
	}
	frame := src.Render()

	// The frame separates rows with a bare LF, the way a host with ONLCR
	// expects; replayed into an emulator without the carriage return, every
	// row lands one column further right than the last and the screen
	// scrolls out from under itself. Feeding it back is only a fair test
	// with the carriage return the host would have added.
	dst := vt.NewEmulator(w, h)
	if _, err := dst.WriteString(strings.ReplaceAll(frame, "\n", "\r\n")); err != nil {
		return fmt.Sprintf("replaying the frame: %v", err)
	}
	if d := compareVisible(src, dst, "screen", "frame"); d != "" {
		return "the frame the emulator emitted does not redraw its own screen: " + d
	}
	return ""
}

// invisibleOnly reports whether a cell occupies no columns, so a renderer
// draws the same thing with or without it.
//
// The pure emulator stores zero-width input as a cell of its own - a bidi
// control, a combining mark with no base, a C1 code point arriving as text -
// and Render emits none of them. Nothing on screen moves, because the cell
// claims no columns, but the grid and the frame then describe different
// things, which matters to anything that compares the two: a snapshot
// serialised from the grid and a client redrawing from a frame do not agree
// about what the pane holds. Pinned by TestVTGen_RenderDropsInvisibleCells
// and excluded here so the round trip reports what would actually be seen.
func invisibleOnly(c *uv.Cell) bool {
	if c == nil || c.Content == "" {
		return false
	}
	if c.Width != 0 {
		return false
	}
	// A zero-width cell holding a format character is the clearest case; the
	// check is kept general because combining marks and C1 code points reach
	// the same state.
	for _, r := range c.Content {
		if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Cc, r) {
			continue
		}
		return false
	}
	return true
}

// compareVisible is compareGrids with the invisible-only cells forgiven.
func compareVisible(a, b *vt.Emulator, aName, bName string) string {
	w, h := a.Width(), a.Height()
	if bw, bh := b.Width(), b.Height(); bw != w || bh != h {
		return fmt.Sprintf("size %s=%dx%d %s=%dx%d", aName, w, h, bName, bw, bh)
	}
	for y := range h {
		for x := range w {
			ac, bc := a.CellAt(x, y), b.CellAt(x, y)
			if sameCell(ac, bc) || invisibleOnly(ac) || invisibleOnly(bc) {
				continue
			}
			return fmt.Sprintf("cell (%d,%d)\n  %-8s %s\n  %-8s %s",
				x, y, aName, cellText(ac), bName, cellText(bc))
		}
	}
	return ""
}

// brokenSig strips the varying detail from a failure so the shrinker can
// insist a candidate still fails the SAME way. An oracle of "still fails"
// reduces a script holding two bugs to whichever survives the cuts, and then
// prints that reduction under a report about the other one.
func brokenSig(broken string) string {
	if broken == "" {
		return ""
	}
	if i := strings.Index(broken, "cell ("); i >= 0 {
		return broken[:i] + "cell"
	}
	if i := strings.IndexByte(broken, ':'); i >= 0 {
		return broken[:i]
	}
	return broken
}

// shrinkSame reduces to the smallest script that still fails the same way.
func shrinkSame(s vtgen.Script, want string, replay func(vtgen.Script) string) vtgen.Script {
	return vtgen.Shrink(s, func(c vtgen.Script) bool { return brokenSig(replay(c)) == want })
}

// metamorphicFuzzGate keeps these targets opt-in for the same reason
// TestVTGen_Metamorphic is: both properties currently fail on real bugs, and
// a fuzz target's seed corpus runs on every `go test`, so leaving them
// ungated turns the ordinary suite red. The pinned tests below carry the
// regression value meanwhile.
func metamorphicFuzzGate(f *testing.F) {
	f.Helper()
	if os.Getenv("TUIOS_METAMORPHIC_FUZZ") == "" {
		f.Skip("set TUIOS_METAMORPHIC_FUZZ=1 to run the metamorphic fuzz targets; " +
			"they report the open bugs pinned in this file")
	}
}

// FuzzEmulatorSplitEquivalence is split equivalence as a guided campaign.
func FuzzEmulatorSplitEquivalence(f *testing.F) {
	metamorphicFuzzGate(f)
	for _, seed := range [][]byte{
		{},
		{0x01},
		[]byte("tuios"),
		[]byte("\xe4\xb8\x96\xe4\xb8\x96"),
		[]byte("the quick brown fox"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		script := vtgen.FromBytes(data).Script(120)
		seed := uint64(len(data))*1099511628211 + 7
		if broken := splitEquivalence(script, seed); broken != "" {
			replay := func(s vtgen.Script) string { return splitEquivalence(s, seed) }
			small := shrinkSame(script, brokenSig(broken), replay)
			t.Fatalf("%s\n\nreduced from %d steps to %d:\n%s",
				broken, len(script), len(small), small)
		}
	})
}

// FuzzEmulatorRenderRoundTrip is the round trip as a guided campaign.
func FuzzEmulatorRenderRoundTrip(f *testing.F) {
	metamorphicFuzzGate(f)
	for _, seed := range [][]byte{
		{},
		{0x02},
		[]byte("tuios"),
		[]byte("\x1b[31mred\x1b[0m"),
		[]byte("the quick brown fox"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		script := vtgen.FromBytes(data).Script(120)
		if broken := renderRoundTrip(script); broken != "" {
			small := shrinkSame(script, brokenSig(broken), renderRoundTrip)
			t.Fatalf("%s\n\nreduced from %d steps to %d:\n%s",
				broken, len(script), len(small), small)
		}
	})
}

// TestVTGen_Metamorphic is the deterministic half of both properties.
//
// It is opt-in because both properties currently fail, and the failures are
// real: the two root causes are pinned below with minimal inputs, and until
// they are fixed a default-on sweep would be a permanently red test that
// people learn to ignore. TUIOS_METAMORPHIC_SEEDS switches it on and says how
// many seeds to run. Once the pinned bugs are fixed this should lose the gate
// and run everywhere.
func TestVTGen_Metamorphic(t *testing.T) {
	seeds := 0
	if v := os.Getenv("TUIOS_METAMORPHIC_SEEDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("TUIOS_METAMORPHIC_SEEDS=%q is not a number", v)
		}
		seeds = n
	}
	if seeds <= 0 {
		t.Skip("set TUIOS_METAMORPHIC_SEEDS=<seeds> to sweep the metamorphic properties")
	}
	steps := 120
	if testing.Short() {
		steps = 50
	}

	var failures int
	for seed := range uint64(seeds) {
		script := vtgen.New(seed).Script(steps)

		if broken := renderRoundTrip(script); broken != "" {
			small := shrinkSame(script, brokenSig(broken), renderRoundTrip)
			t.Errorf("seed %d, render round trip: %s\n\nreduced from %d steps to %d:\n%s",
				seed, broken, len(script), len(small), small)
			failures++
		}
		if broken := splitEquivalence(script, seed); broken != "" {
			replay := func(s vtgen.Script) string { return splitEquivalence(s, seed) }
			small := shrinkSame(script, brokenSig(broken), replay)
			t.Errorf("seed %d, split equivalence: %s\n\nreduced from %d steps to %d:\n%s",
				seed, broken, len(script), len(small), small)
			failures++
		}
		if failures >= 3 {
			t.Fatalf("stopping after %d failures; the rest would be more of the same", failures)
		}
	}
}

// TestVTGen_MetamorphicOraclesCanFail guards the oracles themselves.
//
// A property that cannot fail passes forever and proves nothing, and both of
// these compare an emulator to itself, so a mistake in the harness - reading
// the same emulator twice, comparing an empty screen to an empty screen -
// would be invisible. Feeding a deliberately mismatched pair proves each
// comparison is looking at something.
func TestVTGen_MetamorphicOraclesCanFail(t *testing.T) {
	a := vt.NewEmulator(metaCols, metaRows)
	b := vt.NewEmulator(metaCols, metaRows)
	if _, err := a.WriteString("hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteString("world"); err != nil {
		t.Fatal(err)
	}
	if d := compareGrids(a, b, "a", "b"); d == "" {
		t.Fatal("compareGrids called two different screens equal")
	}
	if d := compareGrids(a, a, "a", "a"); d != "" {
		t.Fatalf("compareGrids called a screen different from itself: %s", d)
	}
	if !strings.Contains(compareGrids(a, b, "a", "b"), "cell (") {
		t.Error("a mismatch message should name the cell")
	}
}

// TestVTGen_ZeroWidthCharacterLosesACell pins the bug the render round trip
// found, reduced to two sequences.
//
// A zero-width character with nothing to attach to - a combining mark at the
// start of a row, a bidi control, a C1 code point arriving as text - is given
// a cell of its own, and it takes that cell from whatever was already there.
// The row then holds one more cell than it has columns. Render emits the row
// without the zero-width cell, so everything after it shifts one column left
// and the last column falls off the end.
//
// The lost column is not invisible. DECALN fills the screen with E, and after
// a single zero-width character the frame is missing the E in the last column
// while the emulator still believes it is there. That is a character on
// screen that never reaches the host, and the emulator's own grid and its own
// frame disagree about the pane.
//
// The ghostty differential found the same defect from the other direction:
// the library discards a mark with no base rather than storing it, which is
// TestGhosttyDivergence_OrphanCombiningMark. Not storing it would close both.
func TestVTGen_ZeroWidthCharacterLosesACell(t *testing.T) {
	const w, h = 40, 4

	cases := []struct{ name, zeroWidth string }{
		{"bidi control", "‮"},
		{"combining mark with no base", "́"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// DECALN fills every cell with E, so any cell the frame fails to
			// carry shows up as a blank that should not be there.
			src := vt.NewEmulator(w, h)
			if _, err := src.WriteString("\x1b#8" + tc.zeroWidth); err != nil {
				t.Fatal(err)
			}

			// The zero-width character took the first cell from the E that
			// DECALN put there.
			first := src.CellAt(0, 0)
			if first == nil || first.Content != tc.zeroWidth {
				t.Fatalf("cell (0,0) = %s, expected it to hold the zero-width character; "+
					"if the emulator stopped storing these, delete this test",
					cellText(first))
			}
			if first.Width != 0 {
				t.Errorf("cell (0,0) width = %d, want 0", first.Width)
			}

			// The last column still holds its E as far as the grid knows.
			if last := src.CellAt(w-1, 0); last == nil || last.Content != "E" {
				t.Fatalf("cell (%d,0) = %s, want the E that DECALN wrote", w-1, cellText(last))
			}

			// The frame does not carry it.
			dst := vt.NewEmulator(w, h)
			if _, err := dst.WriteString(strings.ReplaceAll(src.Render(), "\n", "\r\n")); err != nil {
				t.Fatal(err)
			}
			got := dst.CellAt(w-1, 0)
			if got != nil && got.Content == "E" {
				t.Fatalf("the frame now carries the E in the last column, so the row no " +
					"longer shifts; delete this test and ungate TestVTGen_Metamorphic")
			}

			// And what it does carry is shifted one column left.
			if shifted := dst.CellAt(0, 0); shifted == nil || shifted.Content != "E" {
				t.Errorf("frame cell (0,0) = %s, want the E from column 1 shifted left",
					cellText(shifted))
			}

			// A mark that DOES have a base is handled correctly, which keeps
			// this test honest about what the bug is: the failure is about
			// having nothing to attach to, not about zero width as such.
			ok := vt.NewEmulator(w, h)
			if _, err := ok.WriteString("\x1b#8x́"); err != nil {
				t.Fatal(err)
			}
			okDst := vt.NewEmulator(w, h)
			if _, err := okDst.WriteString(strings.ReplaceAll(ok.Render(), "\n", "\r\n")); err != nil {
				t.Fatal(err)
			}
			if c := okDst.CellAt(w-1, 0); c == nil || c.Content != "E" {
				t.Errorf("a mark with a base also lost the last column (%s); "+
					"the bug is wider than this test claims", cellText(c))
			}
		})
	}
}

// TestVTGen_SplitEquivalenceIsOpen records that the second property still
// fails, without pinning a seed.
//
// The first version of this named seed 28. Adding two modes to the generator
// moved every seed's script and the test went green while the bug was
// untouched, which is exactly the false all-clear a pinned seed invites: the
// seed is a coordinate in the generator's output, not a property of the
// emulator. So this searches a bounded range instead and reports the first
// script it finds that draws one screen written whole and another written in
// the pieces a PTY reader would have handed over.
//
// It fails when NOTHING in the range diverges, which is the day the property
// starts holding and the gate on TestVTGen_Metamorphic should come off. The
// range is wide because the condition is rare: three of the first two
// thousand seeds hit it, and all three land in the last two columns, which
// is where the right margin is.
//
// This is the class of bug that gets reported as a pane that "sometimes"
// corrupts: nothing about the guest's output changed, only how much of it the
// kernel had ready on each read.
func TestVTGen_SplitEquivalenceIsOpen(t *testing.T) {
	const searched = 300
	for seed := range uint64(searched) {
		script := vtgen.New(seed).Script(120)
		broken := splitEquivalence(script, seed)
		if broken == "" {
			continue
		}
		small := shrinkSame(script, brokenSig(broken), func(c vtgen.Script) string {
			return splitEquivalence(c, seed)
		})
		t.Logf("seed %d still diverges on write boundaries: %s\n\nreduced from %d steps to %d:\n%s",
			seed, broken, len(script), len(small), small)
		return
	}
	t.Fatalf("none of the first %d seeds diverged on write boundaries any more; "+
		"the property may now hold, so ungate TestVTGen_Metamorphic and delete this test",
		searched)
}
