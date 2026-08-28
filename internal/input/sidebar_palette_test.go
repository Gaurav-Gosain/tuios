package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The rail lists what exists; the palette searches it. "/" is the reach from one
// to the other, and the rail scope swallows every key it does not bind, so the
// palette in front of it has to be an exception or the query would never be
// typed.

// TestRailSlashOpensThePaletteAndKeepsItsKeys checks both halves: the rail's own
// "/" opens the palette, and the characters that follow reach the query rather
// than the rail's cursor keys.
func TestRailSlashOpensThePaletteAndKeepsItsKeys(t *testing.T) {
	prev := config.Global.SidebarEnabled
	config.Global.SidebarEnabled = true
	t.Cleanup(func() { config.Global.SidebarEnabled = prev })

	o := twoPaneOS(t)
	o.SidebarFocused = true

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: '/', Text: "/"}, o)
	if !o.ShowCommandPalette {
		t.Fatal("/ in the rail did not open the palette")
	}
	if !o.SidebarFocused {
		t.Fatal("opening the palette dropped rail focus, so closing it would not come back to the row")
	}

	// "j" is the rail's cursor-down. With the palette in front it is a character
	// of the query.
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}, o)
	if o.CommandPaletteQuery != "j" {
		t.Errorf("the palette query is %q: the rail swallowed the keystroke", o.CommandPaletteQuery)
	}

	// esc closes the palette and leaves the keyboard with the rail, which is
	// where it came from.
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEscape}, o)
	if o.ShowCommandPalette {
		t.Fatal("esc did not close the palette")
	}
	if !o.SidebarFocused {
		t.Error("esc out of the palette also left the rail")
	}
}
