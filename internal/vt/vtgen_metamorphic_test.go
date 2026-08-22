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
	"image/color"
	"os"
	"strconv"
	"strings"
	"testing"

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

// sameRGB compares colors by the value a frame can carry: the frame writer
// treats palette 16 and rgb(0,0,0) as one color and emits a single SGR run
// for both, and no SGR encodes an alpha, so an oracle comparing spellings or
// alpha would call a faithful frame wrong. nil only matches nil, because it
// means the terminal default.
func sameRGB(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar == br && ag == bg && ab == bb
}

// sameStyle is style equality with colors compared by value.
func sameStyle(a, b uv.Style) bool {
	return a.Attrs == b.Attrs && a.Underline == b.Underline &&
		sameRGB(a.Fg, b.Fg) && sameRGB(a.Bg, b.Bg) &&
		sameRGB(a.UnderlineColor, b.UnderlineColor)
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
	return ac == bc && aw == bw && sameStyle(a.Style, b.Style) && a.Link == b.Link
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
	if d := compareGrids(src, dst, "screen", "frame"); d != "" {
		return "the frame the emulator emitted does not redraw its own screen: " + d
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

// FuzzEmulatorSplitEquivalence is split equivalence as a guided campaign.
func FuzzEmulatorSplitEquivalence(f *testing.F) {
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
// It runs on every build now that both properties hold; the bugs it used to
// gate on - a zero-width character eating a visible cell, and the screen
// depending on where a PTY read boundary fell - are fixed and pinned by
// TestVTGen_ZeroWidthAttachesOrDrops and the grapheme cell tests.
// TUIOS_METAMORPHIC_SEEDS widens the sweep for a longer campaign; the
// default keeps an ordinary `go test` fast.
func TestVTGen_Metamorphic(t *testing.T) {
	seeds := 300
	if testing.Short() {
		seeds = 50
	}
	if v := os.Getenv("TUIOS_METAMORPHIC_SEEDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("TUIOS_METAMORPHIC_SEEDS=%q is not a number", v)
		}
		seeds = n
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

// TestVTGen_ZeroWidthAttachesOrDrops pins the fix for the bug the render
// round trip found: a zero-width character with nothing to attach to used to
// be given a cell of its own, taken from whatever was there, and the row
// then held one more cell than it had columns. Render emitted the row
// without it, everything after shifted one column left, and the last column
// fell off the end - with DECALN, an E the emulator still believed was on
// screen.
//
// The fix follows what ghostty and xterm do: a zero-width arrival combines
// with the cell before the cursor (the cursor's own cell when a print is
// parked at the margin), and is dropped when there is nothing there or when
// it cannot extend that cell's cluster - a bidi control breaks the cluster
// where a combining mark extends it.
func TestVTGen_ZeroWidthAttachesOrDrops(t *testing.T) {
	const w, h = 40, 4

	frameCell := func(src *vt.Emulator, x, y int) *uv.Cell {
		dst := vt.NewEmulator(src.Width(), src.Height())
		if _, err := dst.WriteString(strings.ReplaceAll(src.Render(), "\n", "\r\n")); err != nil {
			t.Fatal(err)
		}
		return dst.CellAt(x, y)
	}

	for _, tc := range []struct{ name, zeroWidth string }{
		{"bidi control", "\u202e"},
		{"combining mark with no base", "\u0301"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// DECALN fills every cell with E and homes the cursor, so there is
			// no cell before it to combine with: the arrival is dropped and
			// every E survives, on the grid and in the frame.
			src := vt.NewEmulator(w, h)
			if _, err := src.WriteString("\x1b#8" + tc.zeroWidth); err != nil {
				t.Fatal(err)
			}
			if first := src.CellAt(0, 0); first == nil || first.Content != "E" {
				t.Errorf("cell (0,0) = %s, want the E that DECALN wrote", cellText(first))
			}
			if last := frameCell(src, w-1, 0); last == nil || last.Content != "E" {
				t.Errorf("frame cell (%d,0) = %s, want the E that DECALN wrote", w-1, cellText(last))
			}
		})
	}

	// A mark with a base joins it, whether the base is in the same write or
	// the cluster was closed in between: after a control it combines with the
	// cell just written, which is where ghostty and xterm put it.
	src := vt.NewEmulator(w, h)
	if _, err := src.WriteString("ab\a\u0301"); err != nil {
		t.Fatal(err)
	}
	if c := src.CellAt(1, 0); c == nil || c.Content != "b\u0301" {
		t.Errorf("cell (1,0) = %s, want the mark combined with the b", cellText(c))
	}
	// The bidi control cannot extend the b\u0301 cluster and is dropped.
	if _, err := src.WriteString("\u202e"); err != nil {
		t.Fatal(err)
	}
	if c := src.CellAt(1, 0); c == nil || c.Content != "b\u0301" {
		t.Errorf("cell (1,0) = %s after a bidi control, want it unchanged", cellText(c))
	}
}
