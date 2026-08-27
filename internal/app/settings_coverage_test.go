package app

import (
	"sort"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// settingsUIExcluded names every registry option the settings page deliberately
// does not carry, one path at a time and with the reason on the line.
//
// Individually rather than by prefix on purpose: a wildcard would swallow the
// next option added under it, which is the drift this file exists to stop. An
// option belongs here only when a person has a better way to reach it than a
// row, never because writing the row was inconvenient.
var settingsUIExcluded = map[string]string{
	// The deprecated flat spellings. Each is an alias of a nested path that has
	// a row of its own, so a row here would be a second control for one value,
	// disagreeing with the first whenever the file used the other spelling.
	"appearance.sidebar_enabled":      "alias of appearance.sidebar.enabled",
	"appearance.sidebar_position":     "alias of appearance.sidebar.position",
	"appearance.sidebar_width":        "alias of appearance.sidebar.width",
	"appearance.sidebar_show_windows": "alias of appearance.sidebar.show_windows",
	"appearance.sidebar_show_glyphs":  "alias of appearance.sidebar.show_glyphs",
	"appearance.sidebar_show_counts":  "alias of appearance.sidebar.show_counts",
	"appearance.sidebar.workspaces":   "replaced by appearance.dock_workspace_tabs",

	// The two section switches. They fold into appearance.sidebar.sections on
	// load, which is the one place membership lives: a layout may carry two
	// spacers and a boolean per section has nowhere to put the second one. The
	// Sections editor is the row that turns a section off, so a row here would
	// be a second control for the same thing, disagreeing with the editor the
	// moment either was used.
	"appearance.sidebar.show_windows": "folded into appearance.sidebar.sections; the Sections editor is the row",
	"appearance.sidebar.show_agents":  "folded into appearance.sidebar.sections; the Sections editor is the row",
	"appearance.hide_clock":           "the positive spelling appearance.show_clock has the row",

	// A shell command the host runs on its own, with no further gesture from
	// anyone. The panel is reachable by every attached client and `tuios ssh`
	// authenticates none of them, so a row here is a way for whoever connected
	// to run a command on the machine hosting the session. It stays settable
	// through the config file and through set-config, both of which are the
	// host's own doing.
	"notifications.agent.command": "a shell command the host runs; not for a panel any client can open",

	// Two filesystem paths on the machine hosting the session. A row would let
	// whoever attached point server-side writes and server-side reads at any
	// path, and `tuios ssh` authenticates no client, which is the reason
	// notifications.agent.command has no row either. Both stay settable through
	// the config file and set-config, which are the host's own doing. A path is
	// also the one value no accepted set can check, so a row could not validate
	// what it took.
	"screenshot.directory": "a server-side write path; not for a panel any client can open",
	"screenshot.font_file": "a server-side read path; not for a panel any client can open",
}

// TestSettingsPanelReachesEveryOption is the guard this branch was opened for.
//
// The panel was hand-written and had drifted to 54 rows against a registry of
// 91 options: everything added after a row was last written was reachable by an
// agent over the control protocol and by a person only by editing the file.
// Nothing failed when the two parted, because nothing tied them together.
//
// This is that tie. An option with no row and no entry in settingsUIExcluded
// fails here, so an option cannot be added in future without someone deciding,
// in this file, how a person reaches it.
func TestSettingsPanelReachesEveryOption(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40}

	reached := map[string]string{}
	for _, cat := range m.settingsCategories() {
		for _, item := range cat.Items {
			if item.Path == "" {
				continue
			}
			if where, dup := reached[item.Path]; dup {
				t.Errorf("option %q has a row under both %q and %q; one value, two controls",
					item.Path, where, cat.Name)
			}
			reached[item.Path] = cat.Name
		}
	}

	var missing []string
	for _, o := range config.Options() {
		if _, ok := reached[o.Path]; ok {
			if reason, excluded := settingsUIExcluded[o.Path]; excluded {
				t.Errorf("option %q has a row and is also excluded (%q); drop the exclusion",
					o.Path, reason)
			}
			continue
		}
		if _, excluded := settingsUIExcluded[o.Path]; excluded {
			continue
		}
		missing = append(missing, o.Path)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d config option(s) cannot be reached from the settings page:\n  %s\n\n"+
			"Add a row in settingsCategories (usually one opt(%q) line), or name the path in\n"+
			"settingsUIExcluded with the reason a person does not need one.",
			len(missing), strings.Join(missing, "\n  "), missing[0])
	}

	// An exclusion for a path the registry dropped is a stale note that would
	// go on excusing an option that no longer exists.
	for path := range settingsUIExcluded {
		if _, ok := config.LookupOption(path); !ok {
			t.Errorf("settingsUIExcluded names %q, which the registry no longer carries", path)
		}
	}
}

// TestSettingsRowsAreUsable checks each row can actually be operated: the
// control the row declares has the function the input handler will call for it.
// A row that renders and does nothing is the failure the derived rows were
// meant to make impossible, and it is not visible from a screenshot.
func TestSettingsRowsAreUsable(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	for _, cat := range m.settingsCategories() {
		for _, item := range cat.Items {
			name := cat.Name + "/" + item.Label
			switch item.Control {
			case controlBool:
				if item.boolVal == nil {
					t.Errorf("%s: bool row has no value to show", name)
				}
				if item.adjust == nil {
					t.Errorf("%s: bool row cannot be toggled", name)
				}
			case controlEnum, controlInt:
				if item.value == nil {
					t.Errorf("%s: row has no value to show", name)
				}
				if item.adjust == nil && item.activate == nil {
					t.Errorf("%s: row can neither be stepped nor activated", name)
				}
			case controlString:
				if item.value == nil {
					t.Errorf("%s: text row has no value to show", name)
				}
				if item.setStr == nil {
					t.Errorf("%s: text row cannot be committed", name)
				}
			case controlColor:
				if item.activate == nil {
					t.Errorf("%s: colour row does not open the picker", name)
				}
			}
			if item.Label == "" {
				t.Errorf("%s: row has no label", name)
			}
			if item.Desc == "" {
				t.Errorf("%s: row has no description", name)
			}
		}
	}
}
