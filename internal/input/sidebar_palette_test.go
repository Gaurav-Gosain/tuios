package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

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

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}, o)
	if paletteQuery(o) != "j" {
		t.Errorf("the palette query is %q: the rail swallowed the keystroke", paletteQuery(o))
	}

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEscape}, o)
	if o.ShowCommandPalette {
		t.Fatal("esc did not close the palette")
	}
	if !o.SidebarFocused {
		t.Error("esc out of the palette also left the rail")
	}
}

// TestRailCtrlPOpensThePalette checks that the global bind for the palette works
// from the rail, the same as it does over a pane.
//
// The rail swallows every key it does not bind and returns before the mode
// handlers that run the global section, so ctrl+p did nothing there.
func TestRailCtrlPOpensThePalette(t *testing.T) {
	prev := config.Global.SidebarEnabled
	config.Global.SidebarEnabled = true
	t.Cleanup(func() { config.Global.SidebarEnabled = prev })

	o := twoPaneOS(t)
	o.SidebarFocused = true

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}, o)
	if !o.ShowCommandPalette {
		t.Fatal("ctrl+p in the rail did not open the palette")
	}
	if !o.SidebarFocused {
		t.Error("opening the palette dropped rail focus, so closing it would not come back to the row")
	}
}
