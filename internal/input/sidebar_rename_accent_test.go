package input

import (
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// railFocusedOS is a two-pane client with the rail holding the keyboard.
func railFocusedOS(t *testing.T) *app.OS {
	t.Helper()
	prev := config.SidebarEnabled
	config.SidebarEnabled = true
	t.Cleanup(func() { config.SidebarEnabled = prev })

	o := twoPaneOS(t)
	o.SidebarFocused = true
	return o
}

// TestRailRenameKeyStartsARename checks r reaches the rail's rename rather than
// the pane binding of the same letter, and that the editor it starts owns the
// keyboard afterwards: the rail scope swallows unbound keys, so a rename typed
// into it would otherwise vanish.
func TestRailRenameKeyStartsARename(t *testing.T) {
	o := railFocusedOS(t)
	o.SidebarCursor = 0
	o.SidebarNav = nil // no rows built: the key must still be handled by the rail

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'r', Text: "r"}, o)
	if o.RenamingWindow {
		t.Fatal("r renamed something with the cursor on no row at all")
	}

	// With a real target, the same key opens the editor and typing lands in the
	// buffer instead of moving the rail cursor.
	o.BeginRenameWindow(o.Windows[1])
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}, o)
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"}, o)
	if o.RenameBuffer != "rightjk" {
		t.Fatalf("rename buffer = %q, want the typed keys appended to the seeded name", o.RenameBuffer)
	}

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter}, o)
	if o.RenamingWindow {
		t.Fatal("enter did not end the rename")
	}
	if o.Windows[1].CustomName != "rightjk" {
		t.Fatalf("window name = %q, want the committed buffer", o.Windows[1].CustomName)
	}
	if !o.SidebarFocused {
		t.Error("committing a rename dropped rail focus")
	}
}

// TestAccentPickerOwnsTheKeyboard checks the picker takes keys ahead of the rail
// it was opened from: a motion key moves its cursor rather than the rail's, a
// hex digit types into its field, and enter applies what that spells.
func TestAccentPickerOwnsTheKeyboard(t *testing.T) {
	o := railFocusedOS(t)
	o.Width, o.Height = 120, 40
	o.OpenAccentPicker(o.Windows[0].ID)

	// j would move the rail cursor; here it moves the picker's grid cursor.
	before := o.AccentPicker.Row
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'j', Text: "j"}, o)
	if o.AccentPicker.Row <= before {
		t.Fatalf("a motion key did not reach the picker (row %d -> %d)", before, o.AccentPicker.Row)
	}

	// A hex spelled out on the keyboard is the colour the picker is holding.
	for _, r := range "#3b82f6" {
		o, _ = HandleKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)}, o)
	}
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter}, o)
	if o.ShowAccentPicker {
		t.Fatal("enter did not apply and close the picker")
	}
	got, ok := o.WindowAccent(o.Windows[0].ID)
	if !ok || got.Hex() != "#3b82f6" {
		t.Fatalf("accent = %+v (%s), want the typed #3b82f6", got, got.Hex())
	}
	if !o.SidebarFocused {
		t.Error("closing the picker dropped rail focus")
	}
}

// TestAccentPickerEscRestores: esc is cancel, and cancel has to put back the
// exact colour the window had rather than the one that was being previewed.
func TestAccentPickerEscRestores(t *testing.T) {
	o := railFocusedOS(t)
	o.Width, o.Height = 120, 40

	id := o.Windows[0].ID
	o.SetWindowAccent(id, app.RGBAccent(color.RGBA{R: 0x33, G: 0x99, B: 0x66, A: 0xff}))
	before, _ := o.WindowAccent(id)

	o.OpenAccentPicker(id)
	for _, r := range "#ff0000" {
		o, _ = HandleKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)}, o)
	}
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEscape}, o)
	if o.ShowAccentPicker {
		t.Fatal("esc did not close the picker")
	}
	if got, ok := o.WindowAccent(id); !ok || got != before {
		t.Fatalf("esc left the accent as %+v, want the exact colour it had (%+v)", got, before)
	}
}
