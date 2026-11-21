package app

import (
	"image/color"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// OpenColorSetting opens the picker on a colour-valued option, seeded with the
// colour that option is producing right now.
//
// Seeding from the effective colour is what makes "change this colour" start
// from the colour being changed, and it does not pin the option to it: nothing
// is written unless the user picks something, and applying the seed unchanged
// writes nothing at all.
func (m *OS) OpenColorSetting(path string) {
	cs, ok := lookupColorSetting(path)
	if !ok {
		return
	}
	pal := theme.UI()
	value := cs.value(m)
	start := toRGBA(cs.effective(pal.Canvas, &m.Settings))

	src, word := accentSourceOwn, ""
	switch {
	case value == "":
		src, word = accentSourceDefault, cs.Unset
	case !config.IsHexColor(value):
		src, word = accentSourceDefault, value
	}

	m.openAccentPicker(AccentTargetSetting, path, RGBAccent(start), src)
	s := &m.AccentPicker
	s.SrcWord, s.NamedOpts = word, cs.named()
	if src == accentSourceDefault && value != "" {
		m.AccentPickerNamedValue(value)
	}
}

// AccentPickerNamedValue selects the named chip holding a keyword, if this
// target offers it.
func (m *OS) AccentPickerNamedValue(v string) {
	for i, name := range m.AccentPicker.NamedOpts {
		if name == v {
			m.AccentPickerNamed(i)
			return
		}
	}
}

// AccentPickerNamed selects one of the target's keywords, which makes the
// keyword the thing that would be applied. The swatch follows it to the colour
// that keyword produces, so the preview is still showing the choice.
func (m *OS) AccentPickerNamed(i int) {
	if !m.ShowAccentPicker {
		return
	}
	s := &m.AccentPicker
	if len(s.NamedOpts) == 0 {
		return
	}
	i = clampInt(i, 0, len(s.NamedOpts)-1)
	s.Focus = accentFocusNamed
	if c, ok := m.namedColor(s.NamedOpts[i]); ok {
		m.accentPickerAdopt(c)
	}
	s.Named = s.NamedOpts[i]
}

func (m *OS) namedColor(keyword string) (color.RGBA, bool) {
	cs, ok := lookupColorSetting(m.AccentPickerTargetID)
	if !ok || cs.namedColor == nil {
		return color.RGBA{}, false
	}
	return toRGBA(cs.namedColor(keyword, theme.UI().Canvas)), true
}

func (s *accentPickerState) settingSelection() string {
	if s.Named != "" {
		return s.Named
	}
	return overlay.Hex(s.Cur)
}

func (m *OS) applySettingColor(path string, sel string) tea.Cmd {
	cs, ok := lookupColorSetting(path)
	if !ok {
		return nil
	}
	if cs.value(m) == sel {
		return nil
	}
	return m.setColorOption(path, sel)
}
