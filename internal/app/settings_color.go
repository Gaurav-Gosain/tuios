package app

import (
	"image/color"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The colour-valued settings, and the one place that says what they are.
//
// The panel builds their rows from this, the picker is opened against it, and
// applying a colour comes back through it. Before, the two border colours were
// text fields with their own inline getters and setters and the tint was an
// enum whose literal form the panel could not reach at all; three descriptions
// of three settings that differ only in which field they hold.
//
// Which options belong here is not decided here: the registry marks the ones
// that hold a colour, and TestEveryColourOptionGetsThePicker walks the registry
// against this list, so an option marked later cannot quietly stay a text
// field.

// colorSetting is one option whose value is a colour.
type colorSetting struct {
	// Path is the registry path, and the identity the open picker carries. A
	// label would not do: the picker outlives the rebuilt row it came from.
	Path  string
	Label string
	Desc  string
	// Unset is what the row prints when nothing is set, in the row's own terms:
	// what happens instead, not the empty string.
	Unset string
	// apply pushes a value into the globals the renderer reads and repaints. The
	// config field itself is written through the registry, so this is only the
	// live half.
	apply func(m *OS, v string)
	// effective is the colour the setting is producing right now, whatever it is
	// set to. It is what the row's swatch and the picker's seed are painted in,
	// so an unset row still shows the colour it is inheriting rather than a
	// blank where a colour should be.
	effective func(ground color.Color, s *config.Settings) color.Color
	// namedColor is the colour one of the option's keywords produces, for the
	// picker's chips. Nil for an option with no keywords.
	namedColor func(keyword string, ground color.Color) color.Color
}

// colorSettings are the three, in the order they appear in the panel.
var colorSettings = []colorSetting{
	{
		Path:  "appearance.border_focused_color",
		Label: "Focused border color",
		Desc:  "Colour of the focused pane's border",
		Unset: "(theme)",
		apply: func(m *OS, _ string) { m.applyBorderColors() },
		effective: func(color.Color, *config.Settings) color.Color {
			return theme.BorderFocusedWindow()
		},
	},
	{
		Path:  "appearance.border_unfocused_color",
		Label: "Unfocused border color",
		Desc:  "Colour of every unfocused pane's border",
		Unset: "(theme)",
		apply: func(m *OS, _ string) { m.applyBorderColors() },
		effective: func(color.Color, *config.Settings) color.Color {
			return theme.BorderUnfocused()
		},
	},
	{
		Path:  "appearance.scrollbar.tint",
		Label: "Scrollbar tint",
		Desc:  "quiet: the pane's own ink, dimmed. border: the focused pane's accent. muted: one grey",
		Unset: "(quiet)",
		apply: func(m *OS, v string) {
			m.Settings.ScrollbarTint = v
			m.MarkAllDirty()
		},
		effective:  scrollbarTintColor,
		namedColor: scrollbarTintKeywordColor,
	},
}

// scrollbarTintColor is the colour the scrollbar's thumb is being drawn in,
// resolved against the ground the swatch will sit on.
//
// The quiet tint is derived from the pane it is drawn on and has no single
// answer, so the picker's own ground stands in for one. It is the colour the
// rule produces where the swatch is, which is the honest thing a swatch on this
// panel can show.
func scrollbarTintColor(ground color.Color, s *config.Settings) color.Color {
	if hex, ok := s.ScrollbarTintHex(); ok {
		return lipgloss.Color(hex)
	}
	return scrollbarTintKeywordColor(s.ScrollbarTintResolved(), ground)
}

// scrollbarTintKeywordColor is the colour one tint keyword produces. It mirrors
// scrollbarInk's rule for the two keywords that name a colour outright; quiet
// derives its ink from the pane it is drawn on, so the ground it is asked about
// stands in for one.
func scrollbarTintKeywordColor(keyword string, ground color.Color) color.Color {
	switch keyword {
	case config.ScrollbarTintMuted:
		return theme.BorderUnfocused()
	case config.ScrollbarTintBorder:
		return theme.BorderFocusedWindow()
	default:
		return scrollbarQuietInk(ground, scrollbarQuietThumbContrast)
	}
}

// lookupColorSetting finds the colour setting at a registry path.
func lookupColorSetting(path string) (colorSetting, bool) {
	for _, cs := range colorSettings {
		if cs.Path == path {
			return cs, true
		}
	}
	return colorSetting{}, false
}

// value reads what the option is set to, or "" when it is unset or no config is
// held (a bare OS in a unit test).
func (cs colorSetting) value(m *OS) string {
	if m.UserConfig == nil {
		return ""
	}
	v, _ := config.GetOptionValue(m.UserConfig, cs.Path)
	return v
}

// named are the non-colour values the option also takes: the scrollbar tint's
// keywords, and nothing for a border colour. They come from the registry's
// accepted set, so a keyword added there turns up in the picker without being
// written down twice.
func (cs colorSetting) named() []string {
	opt, ok := config.LookupOption(cs.Path)
	if !ok {
		return nil
	}
	return opt.Accepted
}

// label is what the row and the picker print for a value: the value itself when
// it is a colour or a keyword, and the unset marker when it is empty.
func (cs colorSetting) label(v string) string {
	if v == "" {
		return cs.Unset
	}
	return v
}

// setColorOption writes a colour option, applies it live, and hands back the
// save. An invalid value is refused rather than written: the picker cannot
// produce one, but this is also the funnel for anything else that sets a colour.
func (m *OS) setColorOption(path, value string) tea.Cmd {
	cs, ok := lookupColorSetting(path)
	if !ok {
		return nil
	}
	if m.UserConfig != nil {
		if err := config.SetOptionValue(m.UserConfig, path, value); err != nil {
			m.ShowNotification(err.Error(), "error", m.Settings.NotificationDuration)
			return nil
		}
	}
	cs.apply(m, value)
	return m.persistSettings()
}

// colorSettingItem is the panel row for the colour setting at a registry path.
// It has no stepper: the value is a colour, and there is no next colour to step
// to.
func colorSettingItem(path string) settingItem {
	setting, ok := lookupColorSetting(path)
	if !ok {
		// Unreachable while the registry and colorSettings agree, which a test
		// pins; a row that says so beats a blank one if they ever stop agreeing.
		return settingItem{Label: path, Desc: "no colour setting at this path", Control: controlColor}
	}
	return settingItem{
		Label:    setting.Label,
		Desc:     setting.Desc,
		Control:  controlColor,
		Unset:    setting.Unset,
		value:    func(m *OS) string { return setting.label(setting.value(m)) },
		swatch:   func(ground color.Color, s *config.Settings) color.Color { return setting.effective(ground, s) },
		activate: func(m *OS) tea.Cmd { m.OpenColorSetting(setting.Path); return nil },
	}
}

// colorSwatch is the mark a colour is shown as outside the picker: two cells
// painted in it, with a hairline of its own lifted ink around nothing at all
// when the colour is too close to the ground to see.
//
// A swatch is a mark, so it answers to MarkFloor rather than to the text floor.
// Painting the cells and stopping there is what makes a dark border colour on a
// dark panel a two-cell hole, which reads as a missing swatch rather than as a
// dark one. The glyph is drawn in the colour lifted to the mark floor when the
// fill alone cannot be seen, so the swatch always has an edge.
func colorSwatch(c, ground color.Color) string {
	if theme.ContrastRatio(c, ground) >= theme.MarkFloor {
		return overlay.Style(accentPaint(c)).Render("  ")
	}
	// Too close to the ground to show as a fill. Draw it as an outlined chip
	// instead: the fill is still the colour, and the edge is that colour lifted
	// until it clears the floor, so the swatch keeps a findable shape.
	return overlay.Style(accentPaint(c)).
		Foreground(theme.ReadableAt(c, ground, theme.MarkFloor)).
		Render(colorSwatchGlyph())
}

// colorSwatchGlyph is the two-cell outline a low-contrast swatch wears.
func colorSwatchGlyph() string {
	if overlay.UseASCII() {
		return "[]"
	}
	return "▕▏"
}
