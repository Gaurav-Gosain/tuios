package vt

import (
	"image/color"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func themePalette() [16]color.Color {
	var pal [16]color.Color
	for i := range pal {
		pal[i] = color.RGBA{R: uint8(i*16 + 1), G: 0x20, B: 0x40, A: 255}
	}
	return pal
}

// TestThemeOffRestoresHostPalette pins the rule that a palette index belongs to
// the user's terminal: once a theme is removed, an indexed color has to reach
// the host as an index again so the host resolves it with the user's own
// palette. Leaving the old table in place repaints panes in a theme the user
// has just turned off.
func TestThemeOffRestoresHostPalette(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()

	e.SetThemeColors(color.White, color.Black, color.White, themePalette())
	e.Write([]byte("\x1b[31mx"))
	if _, ok := e.scr.cur.Pen.Fg.(ansi.BasicColor); ok {
		t.Fatal("SGR 31 stayed an index while a theme was active")
	}

	// Turning theming off, exactly as applyTheme("none") does.
	e.SetThemeColors(nil, nil, nil, [16]color.Color{})

	e.Write([]byte("\x1b[0m\x1b[31mx"))
	if got, ok := e.scr.cur.Pen.Fg.(ansi.BasicColor); !ok || got != ansi.BasicColor(1) {
		t.Errorf("SGR 31 after theme off = %#v, want ansi.BasicColor(1)", e.scr.cur.Pen.Fg)
	}
	if got := e.PaletteColor(1); got != ansi.BasicColor(1) {
		t.Errorf("PaletteColor(1) after theme off = %#v, want ansi.BasicColor(1)", got)
	}
	if e.hasThemeColors() {
		t.Error("emulator still reports theme colors after theme off")
	}
}

// TestGuestPaletteEntryDoesNotThemeTheRest pins that a guest OSC 4 on one slot
// colors that slot alone. The other fifteen are still the user's, and have to
// travel as indices.
func TestGuestPaletteEntryDoesNotThemeTheRest(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()

	e.Write([]byte("\x1b]4;1;#00ff00\x1b\\"))

	e.Write([]byte("\x1b[31mx"))
	if _, ok := e.scr.cur.Pen.Fg.(ansi.BasicColor); ok {
		t.Error("SGR 31 ignored the guest's OSC 4 palette entry")
	}

	e.Write([]byte("\x1b[0m\x1b[32mx"))
	if got, ok := e.scr.cur.Pen.Fg.(ansi.BasicColor); !ok || got != ansi.BasicColor(2) {
		t.Errorf("SGR 32 = %#v, want ansi.BasicColor(2): slot 2 was never set", e.scr.cur.Pen.Fg)
	}
}

// TestOSC104ResetsGuestPaletteNotTheme pins that a guest resetting the palette
// gets the user's terminal back, or the user's theme when one is set, and never
// another guest's idea of red.
func TestOSC104ResetsGuestPaletteNotTheme(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()

	e.Write([]byte("\x1b]4;1;#00ff00\x1b\\"))
	e.Write([]byte("\x1b]104;1\x1b\\"))
	if got := e.PaletteColor(1); got != ansi.BasicColor(1) {
		t.Errorf("PaletteColor(1) after OSC 104;1 = %#v, want ansi.BasicColor(1)", got)
	}

	e.SetThemeColors(color.White, color.Black, color.White, themePalette())
	e.Write([]byte("\x1b]4;1;#00ff00\x1b\\"))
	e.Write([]byte("\x1b]104\x1b\\"))
	if got := e.PaletteColor(1); got != themePalette()[1] {
		t.Errorf("PaletteColor(1) after bare OSC 104 = %#v, want the theme entry", got)
	}
}

// TestRISClearsGuestPalette pins that a full reset leaves no palette state
// behind for whatever runs in the pane next.
func TestRISClearsGuestPalette(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()

	e.Write([]byte("\x1b]4;2;#00ff00\x1b\\"))
	e.Write([]byte("\x1bc"))
	if got := e.PaletteColor(2); got != ansi.BasicColor(2) {
		t.Errorf("PaletteColor(2) after RIS = %#v, want ansi.BasicColor(2)", got)
	}
}

// TestOSC4MalformedIndexIgnored pins that a garbled index is dropped instead of
// being read as slot 0, which would have the guest repaint black by accident.
func TestOSC4MalformedIndexIgnored(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()

	e.Write([]byte("\x1b]4;x1;#00ff00\x1b\\"))
	if got := e.PaletteColor(0); got != ansi.BasicColor(0) {
		t.Errorf("PaletteColor(0) = %#v after a malformed OSC 4, want ansi.BasicColor(0)", got)
	}
}
