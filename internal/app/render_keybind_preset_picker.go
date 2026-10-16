package app

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const presetPickerWidth = 64

var presetPickerHints = []overlay.Hint{
	{Key: "↑↓", Label: "move"},
	{Key: overlay.EnterGlyph, Label: "use"},
	{Key: "esc", Label: "cancel"},
}

// presetPickerLayout is the fitted width and row count. The body has the list
// and three footer lines: what the shell loses, where the tuios keys are, and
// a blank.
func (m *OS) presetPickerLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(presetPickerWidth)
	rows, hints = m.panelBody(len(config.Presets()), 4, width, nil, presetPickerHints)
	return width, rows, hints
}

// renderPresetPicker draws the picker.
func (m *OS) renderPresetPicker() (string, overlay.Geometry, []overlayRowHit) {
	p := &m.presetPicker
	width, visible, hints := m.presetPickerLayout()
	names := config.Presets()
	current := m.currentPreset()
	return renderLivePicker(livePickerPanel{
		Title:    "Keybind preset",
		Items:    names,
		Selected: &p.selected,
		Scroll:   &p.scroll,
		Width:    width,
		Visible:  visible,
		Hints:    hints,
		Empty:    "No presets",
		Row: func(name string, selected bool, pal overlay.Palette, width int) string {
			st := overlay.RowState{Cursor: selected, Focused: true}
			bg := pal.Ground(st, pal.Surface)
			var about config.PresetAbout
			var leader string
			if i := slices.Index(names, name); i >= 0 && i < len(p.about) {
				about, leader = p.about[i], p.leaders[i]
			}
			samples := about.Samples
			marker := "  "
			if selected {
				marker = "› "
			}
			label := name
			if name == current {
				label += " (in use)"
			}
			left := overlay.Truncate(label, 15) + strings.Repeat(" ", max(15-lipgloss.Width(label), 1)) +
				overlay.Truncate(about.Summary, 22)
			line := marker + left
			sample := leader + " then " + strings.Join(samples, ", ")
			gap := width - lipgloss.Width(line) - lipgloss.Width(sample)
			if gap < 2 {
				sample, gap = "", max(width-lipgloss.Width(line), 0)
			}
			text := overlay.Style(bg).Foreground(pal.Fg).Bold(selected).Render(line) +
				overlay.Style(bg).Render(strings.Repeat(" ", gap)) +
				overlay.Style(bg).Foreground(pal.FgDim).Render(sample)
			return pal.Mark(text, st)
		},
		Footer: func(pal overlay.Palette) []string {
			bg := pal.Surface
			dim := overlay.Style(bg).Foreground(pal.FgDim)
			if p.selected < 0 || p.selected >= len(names) {
				return nil
			}
			name := names[p.selected]
			loses := "Your shell loses: nothing new."
			if keys := p.loses[name]; len(keys) > 0 {
				loses = "Your shell loses: " + strings.Join(keys, ", ") + "."
			}
			menu := ""
			if name != config.PresetTuios && p.selected < len(p.leaders) {
				menu = p.leaders[p.selected] + " ctrl+t opens the tuios keys " + name + " moved."
			}
			return []string{
				"",
				dim.Render("  " + overlay.Truncate(loses, width-2)),
				dim.Render("  " + overlay.Truncate(menu, width-2)),
			}
		},
	})
}
