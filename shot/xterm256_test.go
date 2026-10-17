package shot

import "testing"

// referenceXTerm256 is the xterm 256-colour table worked out from its
// definition: the basic 16 as xterm paints them, a 6x6x6 cube whose nonzero
// levels are 55+40v, and a grey ramp of 8+10n. It is written independently of
// XTerm256 so a change to the table behind XTerm256 cannot move a colour
// without this test noticing.
func referenceXTerm256(i int) (r, g, b uint8) {
	basic := [16][3]uint8{
		{0x00, 0x00, 0x00}, {0xcd, 0x00, 0x00}, {0x00, 0xcd, 0x00}, {0xcd, 0xcd, 0x00},
		{0x00, 0x00, 0xee}, {0xcd, 0x00, 0xcd}, {0x00, 0xcd, 0xcd}, {0xe5, 0xe5, 0xe5},
		{0x7f, 0x7f, 0x7f}, {0xff, 0x00, 0x00}, {0x00, 0xff, 0x00}, {0xff, 0xff, 0x00},
		{0x5c, 0x5c, 0xff}, {0xff, 0x00, 0xff}, {0x00, 0xff, 0xff}, {0xff, 0xff, 0xff},
	}
	level := func(v int) uint8 {
		if v == 0 {
			return 0
		}
		return uint8(55 + 40*v)
	}
	switch {
	case i < 16:
		c := basic[i]
		return c[0], c[1], c[2]
	case i < 232:
		n := i - 16
		return level(n / 36), level(n / 6 % 6), level(n % 6)
	default:
		v := uint8(8 + 10*(i-232))
		return v, v, v
	}
}

func TestXTerm256MatchesTheXtermTable(t *testing.T) {
	for i := range 256 {
		r, g, b := referenceXTerm256(i)
		want := RGB(r, g, b)
		if got := XTerm256(i); got != want {
			t.Errorf("XTerm256(%d) = %s, want %s", i, Hex(got), Hex(want))
		}
	}
	for _, i := range []int{-1, 256, 1000} {
		if got := XTerm256(i); got != (Color{}) {
			t.Errorf("XTerm256(%d) = %v, want the zero colour", i, got)
		}
	}
}

func TestXTerm256DoesNotAllocate(t *testing.T) {
	var sink Color
	allocs := testing.AllocsPerRun(100, func() {
		for i := range 256 {
			sink = XTerm256(i)
		}
	})
	_ = sink
	if allocs != 0 {
		t.Fatalf("XTerm256 allocated %.0f times over 256 indexes, want 0", allocs)
	}
}
