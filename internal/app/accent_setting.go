package app

import (
	"image/color"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The colour picker pointed at a settings row.
//
// It is the picker the rail already uses, given a third kind of target rather
// than a second dialog: a user who has coloured a pane knows how to colour a
// border, and one overlay with three targets is one set of key bindings, one
// set of hit rects and one contrast story.
//
// A colour option has three states, not two, and the third is the reason a text
// field was the wrong control for it. It can hold a colour; it can be unset, and
// then it shows whatever the theme or the default produces; and the scrollbar
// tint can hold a keyword, which is neither. The picker says which of the three
// is in force in the readout, and reaches all three: the grid, the slots, the
// hex field and the harmony chips set a colour, the named chips set a keyword,
// and the clear control unsets it.

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
	start := toRGBA(cs.effective(pal.Canvas))

	src, word := accentSourceOwn, ""
	switch {
	case value == "":
		// Unset. The colour on screen is the fallback's, and the readout says so
		// in the row's own words rather than printing a hex the option does not
		// hold.
		src, word = accentSourceDefault, cs.Unset
	case !config.IsHexColor(value):
		// A keyword. Also not a colour the option holds, but the user did choose
		// it, so the word it is named by is what the readout shows.
		src, word = accentSourceDefault, value
	}

	m.openAccentPicker(AccentTargetSetting, path, RGBAccent(start), src)
	s := &m.AccentPicker
	s.SrcWord, s.NamedOpts = word, cs.named()
	if src == accentSourceDefault && value != "" {
		// Opened on a keyword: the keyword is the selection, so the picker starts
		// on the chip holding it rather than on a grid cell nobody chose.
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
	// The colour first, then the keyword: taking a colour clears Named, which is
	// exactly what every other control wants and what this one has to undo.
	if c, ok := m.namedColor(s.NamedOpts[i]); ok {
		m.accentPickerAdopt(c)
	}
	s.Named = s.NamedOpts[i]
}

// namedColor is the colour a keyword produces for the option the picker is on.
func (m *OS) namedColor(keyword string) (color.RGBA, bool) {
	cs, ok := lookupColorSetting(m.AccentPickerTargetID)
	if !ok || cs.namedColor == nil {
		return color.RGBA{}, false
	}
	return toRGBA(cs.namedColor(keyword, theme.UI().Canvas)), true
}

// settingSelection is the string the picker would write to the option: the
// keyword when a named chip is selected, and the colour's hex otherwise.
func (s *accentPickerState) settingSelection() string {
	if s.Named != "" {
		return s.Named
	}
	return hexString(s.Cur)
}

// applySettingColor commits the picker's selection to the option it was opened
// on. Applying what the option already holds writes nothing, which is what
// opening on the effective colour costs: a user who opened the picker and
// pressed enter has changed their mind about nothing, and writing the seed
// through would pin an inheriting option to whatever hex the theme resolves to
// today.
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
