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
	prev := config.Global.SidebarEnabled
	config.Global.SidebarEnabled = true
	t.Cleanup(func() { config.Global.SidebarEnabled = prev })

	o := twoPaneOS(t)
	o.SidebarFocused = true
	return o
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
