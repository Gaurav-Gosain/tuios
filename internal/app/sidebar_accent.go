package app

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// An accent is stored as an index into the theme's own ANSI slots, never as a
// hex value, so a row keeps its identity across a theme switch and stays
// legible on whatever the new theme paints behind it. Fifteen of the sixteen
// slots are offered, the eight bright ones first so an index stored before the
// set grew still means what it meant, then the seven normal ones. Black is
// skipped: an accent nobody can see is not a choice.
//
// A hex entry would nearly double the options and cost the model everything a
// stored hex cannot do: re-resolve against a new theme, or promise legibility
// on two grounds in two theme polarities.
const accentSwatchCount = 15

// accentBrightCount is how many of the slots are the bright half.
const accentBrightCount = 8

// accentNames label the swatches. They name the ANSI slot, not the pixels: what
// "red" looks like is the theme's business. Lowercase, like the rest of the
// dialog's furniture.
var accentNames = [accentSwatchCount]string{
	"gray", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
	"dark red", "dark green", "dark yellow", "dark blue", "dark magenta", "dark cyan", "dark white",
}

// accentColor resolves an accent index against the live theme: the first eight
// are ANSI 8-15, the rest ANSI 1-7.
func accentColor(idx int) color.Color {
	pal := theme.GetANSIPalette()
	idx = clampInt(idx, 0, accentSwatchCount-1)
	if idx < accentBrightCount {
		return pal[accentBrightCount+idx]
	}
	return pal[idx-accentBrightCount+1]
}

// accentMark is the one-cell chip an accented row wears in its glyph column.
func accentMark() string {
	if overlay.UseASCII() {
		return "|"
	}
	return "▌"
}

// WindowAccent returns the accent index a window carries, and whether it has
// one at all.
func (m *OS) WindowAccent(windowID string) (int, bool) {
	idx, ok := m.SidebarAccents[windowID]
	if !ok || idx < 0 || idx >= accentSwatchCount {
		return 0, false
	}
	return idx, true
}

// SetWindowAccent gives a window an accent, or takes it away with a negative
// index, and persists the change with the rest of the sidebar's state.
func (m *OS) SetWindowAccent(windowID string, idx int) {
	if windowID == "" {
		return
	}
	if idx < 0 || idx >= accentSwatchCount {
		delete(m.SidebarAccents, windowID)
	} else {
		if m.SidebarAccents == nil {
			m.SidebarAccents = make(map[string]int, 1)
		}
		m.SidebarAccents[windowID] = idx
	}
	m.saveSidebarState()
}

// OpenAccentPicker opens the swatch picker for a window, landing on the accent
// it already has so the picker opens showing the truth.
func (m *OS) OpenAccentPicker(windowID string) {
	if windowID == "" {
		return
	}
	m.ShowAccentPicker = true
	m.AccentPickerWindowID = windowID
	m.AccentPickerScroll = 0
	m.AccentPickerSelected = accentSwatchCount // the clear row
	if idx, ok := m.WindowAccent(windowID); ok {
		m.AccentPickerSelected = idx
	}
}

// AccentPickerMove moves the picker's selection, clear row included.
func (m *OS) AccentPickerMove(delta int) {
	m.AccentPickerSelected = clampInt(m.AccentPickerSelected+delta, 0, accentSwatchCount)
}

// accentPreview is the slot the rail draws the picker's target in while the
// picker is open, so the mark on the row shows the choice under the cursor
// before it is applied. Derived from the picker's own state rather than stored
// beside it: one fewer thing that can disagree with what is on screen, and the
// three fields it reads are in the rail's signature, so the preview repaints on
// the keystrokes that change it and on nothing else.
func (m *OS) accentPreview(windowID string) (int, bool) {
	if !m.ShowAccentPicker || windowID == "" || windowID != m.AccentPickerWindowID {
		return 0, false
	}
	if m.AccentPickerSelected < 0 || m.AccentPickerSelected >= accentSwatchCount {
		return 0, false // the clear row previews no mark at all
	}
	return m.AccentPickerSelected, true
}

// AccentPickerApply commits the row at idx and closes the picker. The row past
// the swatches is the one that clears.
func (m *OS) AccentPickerApply(idx int) {
	if idx < 0 || idx > accentSwatchCount {
		return
	}
	if idx == accentSwatchCount {
		idx = -1
	}
	m.SetWindowAccent(m.AccentPickerWindowID, idx)
	m.CloseAccentPicker()
}

// AccentPickerClear drops the target's accent without moving the selection
// there first, which is what the picker's clear key does.
func (m *OS) AccentPickerClear() { m.AccentPickerApply(accentSwatchCount) }

// CloseAccentPicker dismisses the picker, changing nothing.
func (m *OS) CloseAccentPicker() {
	m.ShowAccentPicker = false
	m.AccentPickerWindowID = ""
}
