package app

import (
	"image/color"
	"math"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type accentChannel uint8

const (
	accentChanR accentChannel = iota
	accentChanG
	accentChanB
	accentChanS
	accentChanL
	accentChanCount
)

func (ch accentChannel) isHSL() bool { return ch >= accentChanS }

func (ch accentChannel) label() string {
	return [accentChanCount]string{"R", "G", "B", "S", "L"}[ch]
}

func (ch accentChannel) max() int {
	if ch.isHSL() {
		return 100
	}
	return 255
}

func (ch accentChannel) coarse() int {
	if ch.isHSL() {
		return 5
	}
	return 10
}

func (ch accentChannel) focus() accentFocus { return accentFocusR + accentFocus(ch) }

var _ = [1]struct{}{}[accentFocusR+accentFocus(accentChanCount)-accentFocusHarmony]

func (ch accentChannel) runColor(pal overlay.Palette) color.Color {
	if ch.isHSL() {
		if ch == accentChanS {
			return pal.Accent
		}
		return pal.FgDim
	}
	ansi := theme.GetANSIPalette()
	return [3]color.Color{ansi[9], ansi[10], ansi[12]}[ch]
}

func (f accentFocus) sliderChannel() (accentChannel, bool) {
	if f < accentFocusR || f >= accentFocusR+accentFocus(accentChanCount) {
		return 0, false
	}
	return accentChannel(f - accentFocusR), true
}

func (s *accentPickerState) sliderValue(ch accentChannel) int {
	switch ch {
	case accentChanR:
		return int(s.Cur.R)
	case accentChanG:
		return int(s.Cur.G)
	case accentChanB:
		return int(s.Cur.B)
	case accentChanS:
		return int(math.Round(s.Sat * 100))
	default:
		return int(math.Round(s.Light * 100))
	}
}

func (ch accentChannel) text(v int) string {
	if ch.isHSL() {
		return strconv.Itoa(v) + "%"
	}
	return strconv.Itoa(v)
}

const accentSliderPad = 8

func accentSliderBarWidth(inner int) int { return max(inner-accentSliderPad, 1) }

func accentSliderValueAt(col, barW, maxV int) int {
	if barW <= 1 {
		return 0
	}
	col = clampInt(col, 0, barW-1)
	return clampInt(int(float64(col)/float64(barW-1)*float64(maxV)+0.5), 0, maxV)
}

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
	s := &m.AccentPicker
	f := float64(v) / 100
	switch ch {
	case accentChanR:
		c := s.Cur
		c.R = uint8(v)
		m.accentPickerAdopt(c)
	case accentChanG:
		c := s.Cur
		c.G = uint8(v)
		m.accentPickerAdopt(c)
	case accentChanB:
		c := s.Cur
		c.B = uint8(v)
		m.accentPickerAdopt(c)
	case accentChanS:
		m.accentPickerSetHSL(f, s.Light)
	default:
		m.accentPickerSetHSL(s.Sat, f)
	}
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
//
// S and L step the fraction they hold rather than the percent they print. A
// grid cell lands on 9.09 % saturation, and stepping the printed 9 would move
// the colour on the way out and again on the way back; stepping the fraction
// puts the exact cell colour back.
func (m *OS) AccentPickerSliderStep(ch accentChannel, delta int) {
	if !m.ShowAccentPicker {
		return
	}
	s := &m.AccentPicker
	switch ch {
	case accentChanS:
		m.accentPickerSetHSL(s.Sat+float64(delta)/100, s.Light)
	case accentChanL:
		m.accentPickerSetHSL(s.Sat, s.Light+float64(delta)/100)
	default:
		m.AccentPickerSetSlider(ch, s.sliderValue(ch)+delta)
	}
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
