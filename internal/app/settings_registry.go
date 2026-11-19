package app

import (
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A settings row is derived from its registry entry, so an option an agent can
// reach through set-config is one a person can reach on the page too. The
// registry already carries the type, the accepted set, the range, the
// description and the default, which is every question the panel asks of a row:
// a bool is a toggle, an option with Accepted is a cycler, an int with a Min
// and Max is a stepper, and a string is a field. What is left hand-written is
// the handful of rows that genuinely behave differently (a picker, a value the
// config does not spell the way the row does), and
// TestSettingsPanelReachesEveryOption fails the build if an option has neither.

// settingsRow declares one row of a category: a registry path to derive from,
// or a row written by hand. Exactly one is set.
type settingsRow struct {
	path string
	item *settingItem
}

// opt declares a row derived from the registry entry at path.
func opt(path string) settingsRow { return settingsRow{path: path} }

// custom declares a hand-written row. covers names the registry path the row
// stands in for, so the coverage test can see it; empty for a row that is not
// a config option at all.
func custom(covers string, item settingItem) settingsRow {
	item.Path = covers
	return settingsRow{item: &item}
}

// settingInverted are the bool options whose row reads as the positive. The
// config spells three of them as a hide flag, and a toggle labelled "Window
// buttons" that is on when the buttons are there is the row a person expects;
// "Hide window buttons: off" is a double negative to answer a single question.
var settingInverted = map[string]bool{
	"appearance.hide_window_buttons": true,
	"appearance.hide_scrollbar":      true,
	"appearance.hide_clock":          true,
}

// settingSteps is the stride an int row moves by, for the ranges where one is
// too fine to arrow through. Anything absent steps by one.
var settingSteps = map[string]int{
	"appearance.scrollback_lines":          1000,
	"appearance.zoom_max_width":            10,
	"appearance.dim_unfocused":             5,
	"appearance.modal_dim":                 5,
	"appearance.popup_dim":                 5,
	"appearance.animation_speed":           25,
	"appearance.theme_transition_duration": 50,
	"motion.workspace_ms":                  50,
	"motion.layout_ms":                     50,
	"motion.zoom_ms":                       50,
	"motion.overlays_ms":                   20,
	"motion.scroll_ms":                     50,
	"hints.dim":                            5,
	"appearance.sidebar.width":             2,
}

// optionValue is what the config holds at path, or the option's default when no
// config is loaded.
//
// The config rather than the global the renderer reads, deliberately: this is
// the value get-config reports and the value the row writes, so the panel and
// an agent asking the same question get the same answer. The two differ only
// for a setting a CLI flag overrode for this session, and changing that row is
// what makes the file win again.
func (m *OS) optionValue(path string) string {
	o, ok := config.LookupOption(path)
	if !ok {
		return ""
	}
	if m.UserConfig == nil {
		return o.Default
	}
	value, ok := config.GetOptionValue(m.UserConfig, path)
	if !ok {
		return o.Default
	}
	return value
}

// optionEffective is the value in force. A field left at its zero value means
// "unset" for most of the config, and the app then acts on the default; a
// cycler showing an empty string for it said the setting was off rather than
// what it was doing.
func (m *OS) optionEffective(path string) string {
	o, ok := config.LookupOption(path)
	if !ok {
		return ""
	}
	value := m.optionValue(path)
	switch o.Type {
	case config.OptionString:
		if value == "" && len(o.Accepted) > 0 {
			if o.Default != "" {
				return o.Default
			}
			// Registry Default is what DefaultConfig writes, which for a few
			// enums is empty meaning "the built-in". The built-in is the value
			// the option accepts first, which is the order the registry lists
			// them in. No test pins that order now, so an enum whose built-in
			// is not listed first has to set Default. Without this the window
			// title row drew an empty cycler for a title that was plainly
			// being drawn along the bottom.
			return o.Accepted[0]
		}
	case config.OptionInt:
		if value == "0" && o.Default != "0" {
			return o.Default
		}
	}
	return value
}

// optionInt is the effective value of an int option.
func (m *OS) optionInt(path string) int {
	n, err := strconv.Atoi(m.optionEffective(path))
	if err != nil {
		return 0
	}
	return n
}

// optionBool is the effective value of a bool option, before any inversion the
// row applies for display.
func (m *OS) optionBool(path string) bool {
	return m.optionEffective(path) == "true"
}

// setOption writes an option by registry path and applies it live.
//
// setConfigFromRegistry is the funnel the control protocol's set-config already
// goes through: it validates against the registry, writes the config, pushes it
// onto the globals the renderer reads, and retiles. A row calling it is the
// same change an agent makes, which is the point.
//
// The write to disk is the caller's, through persistSettings, so a held arrow
// key does not put a file write on the Update goroutine per repeat.
func (m *OS) setOption(path, value string) {
	if m.UserConfig == nil {
		// No config was loaded, which in a real session means the file failed to
		// parse. There is nothing to write into and nothing safe to write out: a
		// default rendered over a file that did not parse would destroy it. A
		// default in memory is what lets the row work at all, and read-only is
		// what stops it reaching the file. Returning here instead left every row
		// on the page silently doing nothing.
		m.UserConfig = config.DefaultConfig()
		m.ConfigReadOnly = true
	}
	// The settings page saves every change, so a value held by set-config
	// --no-save is saved from here on.
	m.UserConfig.ReleaseOption(path)
	if err := m.setConfigFromRegistry(path, value); err != nil {
		m.ShowNotification(err.Error(), "error", m.Settings.NotificationDuration)
	}
}

// registryItem builds the panel row for a registry path.
func (m *OS) registryItem(path string) settingItem {
	o, ok := config.LookupOption(path)
	if !ok {
		// Unreachable while the coverage test passes; a row that says so beats
		// a blank one if it ever stops.
		return settingItem{Path: path, Label: path, Desc: "no registry entry at this path"}
	}

	item := settingItem{
		Path:    path,
		Label:   m.loc().Label(path),
		Desc:    m.loc().Desc(path),
		derived: true,
	}

	switch {
	case o.Color:
		// The colour rows already know how to open the picker and how to show
		// the colour in force rather than the value stored.
		coloured := colorSettingItem(path)
		coloured.Path = path
		coloured.derived = true
		return coloured

	case o.Type == config.OptionBool:
		invert := settingInverted[path]
		item.Control = controlBool
		item.boolVal = func(m *OS) bool { return m.optionBool(path) != invert }
		item.adjust = func(m *OS, _ int) {
			next := !(m.optionBool(path) != invert)
			m.setOption(path, strconv.FormatBool(next != invert))
		}

	case o.Type == config.OptionInt:
		lo, hi := o.Min, o.Max
		if hi <= 0 {
			// An option with no enforced ceiling still needs one to step
			// against, or the stepper runs away.
			hi = optionStepCeiling
		}
		step := 1
		if s, ok := settingSteps[path]; ok {
			step = s
		}
		item.Control = controlInt
		item.value = func(m *OS) string {
			return o.WithUnit(strconv.Itoa(m.optionInt(path)))
		}
		item.adjust = func(m *OS, dir int) {
			m.setOption(path, strconv.Itoa(clampInt(m.optionInt(path)+dir*step, lo, hi)))
		}
		item.setNum = func(m *OS, v int) {
			m.setOption(path, strconv.Itoa(clampInt(v, lo, hi)))
		}
		item.numMin, item.numMax = lo, hi
		// A gauge only on a proportion, where both ends of the range are places
		// the value would really sit. On a count or a timeout the ceiling is a
		// guard against a silly number and not the top of a scale, so a gauge
		// there would sit empty whatever the value is.
		if o.Percent {
			item.meter = func(m *OS) float64 {
				if hi <= lo {
					return 0
				}
				return float64(clampInt(m.optionInt(path), lo, hi)-lo) / float64(hi-lo)
			}
		}

	case len(o.Accepted) > 0:
		accepted := o.Accepted
		item.Control = controlEnum
		item.Options = accepted
		item.value = func(m *OS) string { return m.optionEffective(path) }
		item.adjust = func(m *OS, dir int) {
			m.setOption(path, cycleEnum(accepted, m.optionEffective(path), dir))
		}

	default:
		item.Control = controlString
		item.Placeholder = m.loc().Placeholder(path)
		item.Unset = m.loc().Unset(path)
		if item.Placeholder != "" {
			item.Desc += m.loc().Value("example_joiner") + item.Placeholder
		}
		item.value = func(m *OS) string { return m.optionValue(path) }
		item.setStr = func(m *OS, v string) { m.setOption(path, v) }
	}

	return item
}

// optionStepCeiling bounds an int row whose registry entry enforces no maximum.
// Nothing in the config is a sensible number above this, and a stepper with no
// ceiling at all only has to be held down to reach one that is not.
const optionStepCeiling = 1000000

// resolveRows turns a category's declaration into the rows the panel draws.
func (m *OS) resolveRows(rows []settingsRow) []settingItem {
	items := make([]settingItem, 0, len(rows))
	for _, row := range rows {
		if row.item != nil {
			items = append(items, *row.item)
			continue
		}
		items = append(items, m.registryItem(row.path))
	}
	return items
}
