package app

import (
	"image/color"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The picker's sliders. The grid is a coarse map of the colour space and the
// hue strip a coarse walk around it; these are the fine control, one channel at
// a time, and the only place in the dialog where a number is the thing being
// edited rather than a readout.
//
// The value is the model and the bar is a projection of it. A bar 24 cells wide
// carries ~11 units of red per cell, so a one-unit key step usually leaves the
// thumb where it was; the printed number always moves, and it is never derived
// from the thumb's column, so the bar cannot quietly round what the user holds.

// accentChannel names one slider.
type accentChannel uint8

const (
	accentChanR accentChannel = iota
	accentChanG
	accentChanB
	accentChanCount
)

// label is the one letter the slider is fronted with.
func (ch accentChannel) label() string {
	return [accentChanCount]string{"R", "G", "B"}[ch]
}

// max is the top of the channel's range. RGB reads 0-255 rather than a
// percentage so the three numbers are the three bytes of the hex printed above
// them.
func (ch accentChannel) max() int { return 255 }

// coarse is the shifted key step: an eyeballing jump rather than a nudge.
func (ch accentChannel) coarse() int { return 10 }

// focus is the keyboard stop this slider owns.
func (ch accentChannel) focus() accentFocus { return accentFocusR + accentFocus(ch) }

// runColor is the colour the filled part of the track is drawn in: the channel
// itself, from the terminal's own palette, so the three bars read as red, green
// and blue in whatever theme is loaded.
func (ch accentChannel) runColor() color.Color {
	pal := theme.GetANSIPalette()
	return [accentChanCount]color.Color{pal[9], pal[10], pal[12]}[ch]
}

// sliderChannel is the channel a focus stop drives, if it drives one.
func (f accentFocus) sliderChannel() (accentChannel, bool) {
	if f < accentFocusR || f >= accentFocusR+accentFocus(accentChanCount) {
		return 0, false
	}
	return accentChannel(f - accentFocusR), true
}

// sliderValue reads the channel off the colour the picker holds.
func (s *accentPickerState) sliderValue(ch accentChannel) int {
	switch ch {
	case accentChanR:
		return int(s.Cur.R)
	case accentChanG:
		return int(s.Cur.G)
	default:
		return int(s.Cur.B)
	}
}

// sliderText is what the slider prints beside its bar.
func (ch accentChannel) text(v int) string { return strconv.Itoa(v) }

// accentSliderPad is the furniture a slider row spends outside its bar: the
// focus sigil, the label and its space, the gap, and four cells of value.
const accentSliderPad = 8

// accentSliderBarWidth is how many cells of track fit in a column inner wide.
func accentSliderBarWidth(inner int) int { return max(inner-accentSliderPad, 1) }

// accentSliderValueAt maps a column inside the bar to a value, which is what a
// click on the track means.
func accentSliderValueAt(col, barW, maxV int) int {
	if barW <= 1 {
		return 0
	}
	col = clampInt(col, 0, barW-1)
	return clampInt(int(float64(col)/float64(barW-1)*float64(maxV)+0.5), 0, maxV)
}

// accentSliderCol is the bar column the thumb sits in for a value, the inverse
// of accentSliderValueAt over the values a column can produce.
func accentSliderCol(v, barW, maxV int) int {
	if barW <= 1 || maxV <= 0 {
		return 0
	}
	v = clampInt(v, 0, maxV)
	return clampInt(int(float64(v)/float64(maxV)*float64(barW-1)+0.5), 0, barW-1)
}

// AccentPickerSetSlider puts one channel of the current colour at a value. The
// colour it produces is a literal like every other control's but the slot row's,
// and the grid cursor and held hue follow it so the rest of the dialog agrees.
func (m *OS) AccentPickerSetSlider(ch accentChannel, v int) {
	if !m.ShowAccentPicker {
		return
	}
	v = clampInt(v, 0, ch.max())
	c := m.AccentPicker.Cur
	switch ch {
	case accentChanR:
		c.R = uint8(v)
	case accentChanG:
		c.G = uint8(v)
	default:
		c.B = uint8(v)
	}
	m.accentPickerAdopt(c)
}

// AccentPickerSliderAt drives a slider from a column inside its bar, which is
// what a press and every motion of a drag hand it. The bar's width comes from
// the rect the renderer recorded, never from the layout arithmetic again.
func (m *OS) AccentPickerSliderAt(ch accentChannel, col, barW int) {
	if !m.ShowAccentPicker {
		return
	}
	m.AccentPicker.Focus = ch.focus()
	m.AccentPickerSetSlider(ch, accentSliderValueAt(col, barW, ch.max()))
}

// AccentPickerSliderStep nudges the focused slider, keeping the keyboard on it.
func (m *OS) AccentPickerSliderStep(ch accentChannel, delta int) {
	m.AccentPickerSetSlider(ch, m.AccentPicker.sliderValue(ch)+delta)
}

// AccentPickerSliderEnd sends the focused slider to one end of its range.
func (m *OS) AccentPickerSliderEnd(hi bool) {
	if !m.ShowAccentPicker {
		return
	}
	ch, ok := m.AccentPicker.Focus.sliderChannel()
	if !ok {
		return
	}
	if hi {
		m.AccentPickerSetSlider(ch, ch.max())
		return
	}
	m.AccentPickerSetSlider(ch, 0)
}
