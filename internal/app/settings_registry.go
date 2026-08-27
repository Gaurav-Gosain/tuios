package app

import (
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The settings page used to be a hand-written list. It reached 54 rows against
// a registry of 91 options, and every option added after a row was last written
// was reachable by an agent through set-config and by a person only by editing
// the file. Nothing failed when the two drifted, because nothing tied them
// together.
//
// A row is now derived from its registry entry. The registry already carries
// the type, the accepted set, the range, the description and the default, which
// is every question the panel was asking of each hand-written row: a bool is a
// toggle, an option with Accepted is a cycler, an int with a Min and Max is a
// stepper, and a string is a field. What is left hand-written is the handful of
// rows that genuinely behave differently (a picker, a value the config does not
// spell the way the row does), and TestSettingsPanelReachesEveryOption fails the
// build if an option has neither.

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

// settingLabels overrides the label derived from a path's last segment, for the
// paths where the derived one reads worse than the name the row has always had.
var settingLabels = map[string]string{
	"appearance.dockbar_position":       "Dock position",
	"appearance.glyphs":                 "Glyph set",
	"appearance.hide_clock":             "Clock in the badge",
	"appearance.hide_scrollbar":         "Scrollbar",
	"appearance.hide_window_buttons":    "Window buttons",
	"appearance.scrollbar.style":        "Scrollbar style",
	"appearance.show_clock":             "Clock",
	"appearance.show_cpu":               "CPU meter",
	"appearance.show_ram":               "RAM meter",
	"appearance.sidebar.enabled":        "Sidebar",
	"appearance.sidebar.position":       "Position",
	"appearance.sidebar.width":          "Width",
	"appearance.window_title_position":  "Window title",
	"appearance.whichkey_enabled":       "Which-key",
	"appearance.whichkey_position":      "Which-key position",
	"appearance.gap":                    "Pane gap",
	"appearance.niri_reverse_scroll":    "Reverse scroll",
	"appearance.animations_enabled":     "Animations",
	"appearance.confirm_quit":           "Confirm quit",
	"appearance.session_colors":         "Session colors",
	"appearance.links":                  "Links",
	"appearance.dock_pill_caps":         "Pill caps",
	"appearance.dock_workspace_tabs":    "Workspace tabs",
	"appearance.dock_workspace_tooltip": "Workspace name on hover",
	"dock.clock.format":                 "Dock clock format",
	"debug.show_key_events":             "Show keys",
	"daemon.log_level":                  "Log level",
	"tape.auto_review":                  "Auto-open review",

	"appearance.sidebar.show_agents":       "Agents section",
	"appearance.sidebar.sections":          "Section layout",
	"appearance.sidebar.file_icons":        "File icons",
	"appearance.sidebar.folder_click":      "Click a folder",
	"appearance.dock_workspace_tab_format": "Workspace tab format",
	"appearance.zoom_max_width":            "Zoom width",
	"appearance.alt_drag":                  "Alt-drag to move",
	"startup.tiled":                        "Start tiled",
	"startup.daemon":                       "Daemon by default",

	// The [notifications] rows sit under one tab, where a row called "Duration"
	// or "Sound" does not say which of the three durations or whose sound.
	"notifications.duration":                     "Info duration",
	"notifications.warning_duration":             "Warning duration",
	"notifications.error_duration":               "Error duration",
	"notifications.error_sticky":                 "Errors wait for esc",
	"notifications.agent.enabled":                "Agent alerts",
	"notifications.agent.notify":                 "Agent alert on the desktop",
	"notifications.agent.dock":                   "Agent alert in the dock",
	"notifications.agent.sound":                  "Agent alert sound",
	"notifications.agent.sound_mode":             "How an alert sounds",
	"notifications.agent.sound_cooldown_seconds": "Gap between sounds",
	"notifications.agent.settle_seconds":         "Wait before alerting",
	"notifications.agent.suppress_focused":       "Skip the pane you are in",
	"notifications.agent.quiet_hours":            "Agent quiet hours",
	"notifications.agent.command":                "Agent alert command",
	"notifications.agent.states.working":         "Alert when it starts work",
	"notifications.agent.states.idle":            "Alert when it goes quiet",
	"notifications.agent.states.done":            "Alert when it finishes",
	"notifications.agent.states.needs_input":     "Alert when it waits for you",
	"notifications.agent.states.errored":         "Alert when it fails",
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

// settingPlaceholders are the examples shown on a text row's description line,
// where the registry description alone does not say what a value looks like.
var settingPlaceholders = map[string]string{
	"appearance.window_title_format":       "{index}: {title}",
	"appearance.dock_workspace_tab_format": "{index}: {name}",
	"appearance.sidebar.sections":          "sessions:25,terminals,files:25,agents:34",
	"appearance.clock_format":              "Mon 3:04PM",
	"dock.clock.format":                    "15:04",
	"appearance.preferred_shell":           "/bin/bash",
	"appearance.word_characters":           "@-./_~?&=%+#",
	"notifications.agent.command":          "notify-send {state} {title}",
	"notifications.agent.quiet_hours":      "22:00-08:00",
}

// settingUnset is what a text row shows when nothing is set: what happens
// instead, in the row's own terms.
var settingUnset = map[string]string{
	"appearance.window_title_format":       "(raw title)",
	"appearance.dock_workspace_tab_format": "(name only)",
	"appearance.preferred_shell":           "(auto-detect)",
	"appearance.clock_format":              config.DefaultClockFormat,
	"dock.clock.format":                    "(appearance.clock_format)",
}

// settingSteps is the stride an int row moves by, for the ranges where one is
// too fine to arrow through. Anything absent steps by one.
var settingSteps = map[string]int{
	"appearance.scrollback_lines": 1000,
	"appearance.zoom_max_width":   10,
	"appearance.dim_unfocused":    5,
	"appearance.sidebar.width":    2,
}

// settingLabel is the row's name: an override where there is one, otherwise the
// path's last segment with its underscores opened out.
func settingLabel(path string) string {
	if label, ok := settingLabels[path]; ok {
		return label
	}
	segment := path
	if i := strings.LastIndex(path, "."); i >= 0 {
		segment = path[i+1:]
	}
	words := strings.ReplaceAll(segment, "_", " ")
	if words == "" {
		return path
	}
	return strings.ToUpper(words[:1]) + words[1:]
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
			// Registry Default is what DefaultConfig writes, which for four
			// enums is empty meaning "the built-in". The built-in is the value
			// the option accepts first, which is the order the registry lists
			// them in and what TestEnumDefaultIsTheFirstAccepted pins. Without
			// this the window title row drew an empty cycler for a title that
			// was plainly being drawn along the bottom.
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
	if err := m.setConfigFromRegistry(path, value); err != nil {
		m.ShowNotification(err.Error(), "error", config.NotificationDuration)
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
		Path:  path,
		Label: settingLabel(path),
		Desc:  o.Description,
	}

	switch {
	case o.Color:
		// The colour rows already know how to open the picker and how to show
		// the colour in force rather than the value stored.
		coloured := colorSettingItem(path)
		coloured.Path = path
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
		item.value = func(m *OS) string { return strconv.Itoa(m.optionInt(path)) }
		item.adjust = func(m *OS, dir int) {
			m.setOption(path, strconv.Itoa(clampInt(m.optionInt(path)+dir*step, lo, hi)))
		}
		// A gauge only where the registry enforces a ceiling. On an option
		// without one the bar would be drawn against a number this file picked,
		// which is a scale the setting does not actually have.
		if o.Max > 0 {
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
		item.Placeholder = settingPlaceholders[path]
		item.Unset = settingUnset[path]
		if item.Unset == "" {
			item.Unset = "(default)"
		}
		if item.Placeholder != "" {
			item.Desc += ", e.g. " + item.Placeholder
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
