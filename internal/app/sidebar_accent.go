package app

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// An accent is stored as an index into the theme's eight bright ANSI colors,
// never as a hex value, so a row keeps its identity across a theme switch and
// stays legible on whatever the new theme paints behind it.
const accentSwatchCount = 8

// accentNames label the swatches. They name the ANSI slot, not the pixels: what
// "red" looks like is the theme's business.
var accentNames = [accentSwatchCount]string{
	"Gray", "Red", "Green", "Yellow", "Blue", "Magenta", "Cyan", "White",
}

// accentColor resolves an accent index against the live theme.
func accentColor(idx int) color.Color {
	pal := theme.GetANSIPalette()
	return pal[8+clampInt(idx, 0, accentSwatchCount-1)]
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
