package app

import (
	"sort"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

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
	m := &OS{Settings: config.Global, Width: 120, Height: 40, SidebarAgentsSeen: true}
	open := true
	m.settingsAgentsOpen = &open

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

	for path := range settingsUIExcluded {
		if _, ok := config.LookupOption(path); !ok {
			t.Errorf("settingsUIExcluded names %q, which the registry no longer carries", path)
		}
	}
}

var settingsUIExcluded = map[string]string{
	"mermaid.theme":      "read only by tuios mermaid, which takes --theme too",
	"mermaid.background": "read only by tuios mermaid, which takes --background too",

	"appearance.sidebar_enabled":         "alias of appearance.sidebar.enabled",
	"appearance.sidebar_position":        "alias of appearance.sidebar.position",
	"appearance.sidebar_width":           "alias of appearance.sidebar.width",
	"appearance.sidebar_show_windows":    "alias of appearance.sidebar.show_windows",
	"appearance.sidebar_show_glyphs":     "alias of appearance.sidebar.show_glyphs",
	"appearance.sidebar_show_counts":     "alias of appearance.sidebar.show_counts",
	"appearance.sidebar.workspaces":      "no longer used: panes name their own workspace, and switching lives on the dock and alt+1..9",
	"appearance.animations_enabled":      "replaced by appearance.motion (false is none)",
	"appearance.workspace_transition":    "old name of motion.workspace",
	"appearance.workspace_transition_ms": "old name of motion.workspace_ms",
	"appearance.zoom_animation":          "old name of motion.zoom",

	"appearance.sidebar.show_windows": "folded into appearance.sidebar.sections; the Sections editor is the row",
	"appearance.sidebar.show_agents":  "folded into appearance.sidebar.sections; the Sections editor is the row",
	"appearance.hide_clock":           "the positive spelling appearance.show_clock has the row",

	"notifications.agent.command":       "a shell command the host runs; not for a panel any client can open",
	"appearance.selection.copy_command": "a shell command the host runs on each yank; not for a panel any client can open",

	"screenshot.directory": "a server-side write path; not for a panel any client can open",
	"screenshot.font_file": "a server-side read path; not for a panel any client can open",
}
