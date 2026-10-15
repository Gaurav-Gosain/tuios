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

// TestGuestPaletteResets pins what undoes a guest's OSC 4.
// OSC 104 gives back the user's terminal, or the user's theme when one is set,
// and never another guest's idea of red. A full reset leaves no palette state
// behind for whatever runs in the pane next. A garbled index is dropped rather
// than read as slot 0, which would have the guest repaint black by accident.
func TestGuestPaletteResets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		theme bool
		in    string
		slot  int
		want  color.Color
	}{
		{"OSC 104;1 resets the slot", false, "\x1b]4;1;#00ff00\x1b\\\x1b]104;1\x1b\\", 1, ansi.BasicColor(1)},
		{"a bare OSC 104 gives the theme back", true, "\x1b]4;1;#00ff00\x1b\\\x1b]104\x1b\\", 1, themePalette()[1]},
		{"RIS clears the guest palette", false, "\x1b]4;2;#00ff00\x1b\\\x1bc", 2, ansi.BasicColor(2)},
		{"a malformed index is ignored", false, "\x1b]4;x1;#00ff00\x1b\\", 0, ansi.BasicColor(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(80, 24)
			defer e.Close()
			if tc.theme {
				e.SetThemeColors(color.White, color.Black, color.White, themePalette())
			}
			e.Write([]byte(tc.in))
			if got := e.PaletteColor(tc.slot); got != tc.want {
				t.Errorf("PaletteColor(%d) = %#v, want %#v", tc.slot, got, tc.want)
			}
		})
	}
}
