package app

import (
	"image/color"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestAccentPickerOpensOnOneAnswer pins the state the audit found incoherent:
// with no accent set the hue marker sat on red, the shade marker on white, and
// the swatch and hex both read #000000, so the picker opened showing three
// different answers and offered harmony chips derived from black.
//
// Everything the picker draws on open comes off one seed now. This holds the
// property rather than the fix: the markers, the working colour and the hex all
// have to name the same colour, and it is never black.
func TestAccentPickerOpensOnOneAnswer(t *testing.T) {
	blue := color.RGBA{R: 0x3a, G: 0xa0, B: 0xff, A: 0xff}
	for _, prior := range []bool{false, true} {
		m := &OS{Width: 120, Height: 40, WorkspaceFocus: map[int]int{}, NumWorkspaces: 9, CurrentWorkspace: 1}
		win := &terminal.Window{ID: "w1", CustomName: "pane", Workspace: 1}
		m.Windows = []*terminal.Window{win}
		if prior {
			m.SetWindowAccent("w1", RGBAccent(blue))
		}

		m.OpenAccentPicker("w1")
		s := m.AccentPicker

		if s.Cur.R == 0 && s.Cur.G == 0 && s.Cur.B == 0 {
			t.Errorf("prior=%v: the picker opened on black", prior)
		}
		if got := hexString(s.Cur); got != s.Hex {
			t.Errorf("prior=%v: the hex field reads %q while the working colour is %q", prior, s.Hex, got)
		}
		if s.Base != s.Cur {
			t.Errorf("prior=%v: the harmony chips hang off %v, not the working colour %v", prior, s.Base, s.Cur)
		}
		// The markers sit on the grid cell nearest the working colour. The grid
		// quantises, so the test is the round trip: the colour under the markers
		// resolves back to the cell they are on. A picker showing red markers
		// over a black swatch fails this by the width of the grid.
		cols, rows := m.accentGridSize()
		under := accentCellColor(s.Hue, s.Col, s.Row, cols, rows)
		if _, col, row := accentCellFor(under, s.Hue, cols, rows); col != s.Col || row != s.Row {
			t.Errorf("prior=%v: the markers are at (%d,%d) but the colour under them lives at (%d,%d)",
				prior, s.Col, s.Row, col, row)
		}
		if _, col, row := accentCellFor(s.Cur, s.Hue, cols, rows); col != s.Col || row != s.Row {
			t.Errorf("prior=%v: the swatch colour %v belongs at (%d,%d), markers are at (%d,%d)",
				prior, s.Cur, col, row, s.Col, s.Row)
		}
		if prior && s.Cur != blue {
			t.Errorf("the picker opened on %v rather than the accent the pane wears", s.Cur)
		}
	}
}

// TestAccentSeedFallbackIsAChromeColour checks the fallback seed is a colour the
// chrome actually uses, so a pane with no accent still opens on something the
// user has seen.
func TestAccentSeedFallbackIsAChromeColour(t *testing.T) {
	m := &OS{Width: 120, Height: 40, WorkspaceFocus: map[int]int{}, NumWorkspaces: 9, CurrentWorkspace: 1}
	m.Windows = []*terminal.Window{{ID: "w1", Workspace: 1}}
	m.OpenAccentPicker("w1")
	if got, want := m.AccentPicker.Cur, toRGBA(theme.UI().Accent); got != want {
		t.Errorf("the picker seeded %v, want the chrome accent %v", got, want)
	}
}
