package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A key bound in [keybindings.lists] moves the cursor in a list through the
// real entry point, and the key it replaced does not.
func TestListKeysFromConfigMoveTheCommandPalette(t *testing.T) {
	o := osWithBindings(t, func(kb *config.KeybindingsConfig) {
		kb.Lists[config.ActionListDown] = []string{"ctrl+j", "down"}
		kb.Lists[config.ActionListUp] = []string{"ctrl+k", "up"}
	})
	o.Width, o.Height = 120, 40
	o.OpenCommandPalette()
	if paletteCursor(o) != 0 {
		t.Fatalf("palette opened on row %d, want 0", paletteCursor(o))
	}

	press := func(code rune) {
		t.Helper()
		o, _ = HandleKeyPress(ctrlKey(code), o)
	}

	press('j')
	press('j')
	if paletteCursor(o) != 2 {
		t.Fatalf("after ctrl+j twice the palette is on row %d, want 2", paletteCursor(o))
	}
	press('k')
	if paletteCursor(o) != 1 {
		t.Fatalf("after ctrl+k the palette is on row %d, want 1", paletteCursor(o))
	}
	// ctrl+n was Down's default. The action names other keys now, so it is free.
	press('n')
	if paletteCursor(o) != 1 {
		t.Fatalf("ctrl+n moved the palette to row %d, but list_down no longer binds it", paletteCursor(o))
	}
}

func TestListKeysDefaultsMoveTheCommandPalette(t *testing.T) {
	o := osWithBindings(t, func(*config.KeybindingsConfig) {})
	o.Width, o.Height = 120, 40
	o.OpenCommandPalette()
	for _, code := range []rune{'n', 'n', 'p'} {
		o, _ = HandleKeyPress(ctrlKey(code), o)
	}
	if paletteCursor(o) != 1 {
		t.Fatalf("ctrl+n, ctrl+n, ctrl+p left the palette on row %d, want 1", paletteCursor(o))
	}
}
