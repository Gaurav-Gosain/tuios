package vt_test

import (
	"fmt"
	"image/color"
	"os"
	"strconv"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
	"tuios.dev/vt/vtgen"
)

const (
	metaCols = 40
	metaRows = 12
)

func cellText(c *uv.Cell) string {
	if c == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q w=%d fg=%v bg=%v attrs=%x ul=%d",
		c.Content, c.Width, c.Style.Fg, c.Style.Bg, c.Style.Attrs, c.Style.Underline)
}

func sameRGB(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar == br && ag == bg && ab == bb
}

func sameStyle(a, b uv.Style) bool {
	return a.Attrs == b.Attrs && a.Underline == b.Underline &&
		sameRGB(a.Fg, b.Fg) && sameRGB(a.Bg, b.Bg) &&
		sameRGB(a.UnderlineColor, b.UnderlineColor)
}

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

	if d := compareGrids(whole, split, "whole", "split"); d != "" {
		return "the same bytes split at different boundaries drew a different screen: " + d
	}
	if wp, sp := whole.CursorPosition(), split.CursorPosition(); wp != sp {
		return fmt.Sprintf("the same bytes split at different boundaries left the cursor elsewhere: whole=%v split=%v", wp, sp)
	}
	return ""
}

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

	dst := vt.NewEmulator(w, h)
	if _, err := dst.WriteString(strings.ReplaceAll(frame, "\n", "\r\n")); err != nil {
		return fmt.Sprintf("replaying the frame: %v", err)
	}
	if d := compareGrids(src, dst, "screen", "frame"); d != "" {
		return "the frame the emulator emitted does not redraw its own screen: " + d
	}
	return ""
}

func brokenSig(broken string) string {
	if broken == "" {
		return ""
	}
	if before, _, ok := strings.Cut(broken, "cell ("); ok {
		return before + "cell"
	}
	if before, _, ok := strings.Cut(broken, ":"); ok {
		return before
	}
	return broken
}

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
		seed := splitSeed(data)
		if broken := splitEquivalence(script, seed); broken != "" {
			replay := func(s vtgen.Script) string { return splitEquivalence(s, seed) }
			small := shrinkSame(script, brokenSig(broken), replay)
			t.Fatalf("%s\n\nreduced from %d steps to %d:\n%s\n%s",
				broken, len(script), len(small), small, pinnable(small, seed, broken))
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
			t.Fatalf("%s\n\nreduced from %d steps to %d:\n%s\n%s",
				broken, len(script), len(small), small, pinnable(small, 0, broken))
		}
	})
}

// TestVTGen_Metamorphic is the deterministic half of both properties.
//
// It runs on every build. The two bugs it has caught (a zero-width character
// eating a visible cell, and the screen depending on where a PTY read boundary
// fell) are also pinned by TestVTGen_ZeroWidthAttachesOrDrops and the grapheme
// cell tests.
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
// these compare an emulator to itself, so a mistake in the harness (reading
// the same emulator twice, comparing an empty screen to an empty screen)
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

// TestVTGen_ZeroWidthAttachesOrDrops pins a bug the render round trip found.
// A zero-width character with nothing to attach to must not get a cell of its
// own: taking one from whatever was there leaves the row with one more cell
// than it has columns. Render emits the row without it, everything after
// shifts one column left, and the last column falls off the end (with DECALN,
// an E the emulator still believes is on screen).
//
// The emulator follows ghostty and xterm: a zero-width arrival combines with
// the cell before the cursor (the cursor's own cell when a print is parked at
// the margin), and is dropped when there is nothing there or when it cannot
// extend that cell's cluster. A bidi control breaks the cluster where a
// combining mark extends it.
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

	src := vt.NewEmulator(w, h)
	if _, err := src.WriteString("ab\a\u0301"); err != nil {
		t.Fatal(err)
	}
	if c := src.CellAt(1, 0); c == nil || c.Content != "b\u0301" {
		t.Errorf("cell (1,0) = %s, want the mark combined with the b", cellText(c))
	}
	if _, err := src.WriteString("\u202e"); err != nil {
		t.Fatal(err)
	}
	if c := src.CellAt(1, 0); c == nil || c.Content != "b\u0301" {
		t.Errorf("cell (1,0) = %s after a bidi control, want it unchanged", cellText(c))
	}
}
