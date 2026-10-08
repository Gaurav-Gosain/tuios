package guibridge

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// The palette event: tuios's own command palette, so the renderer's palette
// offers the same commands as the terminal client's (plan 2.14).
//
// A row either names an action, which the renderer runs the way it runs the
// key bound to it (a registry action through the action command, or a gui_
// action it draws itself), or it names nothing and runs through the palette
// command, which calls the row's own code in the model. A row whose only
// effect is a view the bridge cannot show, and that the renderer has no view
// for, is left out: offered, it would do nothing a person can see.

// PaletteRow is one row of the palette event.
type PaletteRow struct {
	// Name is the row's text, and what the palette command names it by.
	Name string `json:"name"`
	// Category is the row's section: Window, Layout, Navigation, Session,
	// Agents, Commands, Plugins.
	Category string `json:"category"`
	// Shortcut is the key tuios shows for the row, such as "prefix+c", or a
	// short note in place of a key.
	Shortcut string `json:"shortcut,omitempty"`
	// Action is set when the row is the same as an action: the renderer runs
	// it as it runs the action's key, and shows that key on the row.
	Action string `json:"action,omitempty"`
}

// paletteActions maps the rows that do what one action does to the action.
// The renderer runs these itself, so a row and its key never differ, and the
// row shows the user's own key for it.
var paletteActions = map[string]string{
	"Run a program":                             "gui_launcher",
	"New window":                                "new_window",
	"Close window":                              "close_window",
	"Rename window":                             "gui_rename",
	"Toggle zoom":                               "toggle_zoom",
	"Minimize window":                           "minimize_window",
	"Restore all minimized":                     "restore_all",
	"Toggle tiling":                             "toggle_tiling",
	"Split horizontal":                          "split_horizontal",
	"Split vertical":                            "split_vertical",
	"Rotate split":                              "rotate_split",
	"Equalize splits":                           "equalize_splits",
	"Cycle tiling scheme":                       "cycle_tiling_scheme",
	"Set tiling scheme spiral":                  "set_tiling_scheme_spiral",
	"Set tiling scheme longest side":            "set_tiling_scheme_longest_side",
	"Set tiling scheme alternate":               "set_tiling_scheme_alternate",
	"Set tiling scheme smart split":             "set_tiling_scheme_smart_split",
	"Cycle master position":                     "cycle_master_position",
	"Set master position left":                  "set_master_position_left",
	"Set master position right":                 "set_master_position_right",
	"Set master position top":                   "set_master_position_top",
	"Set master position bottom":                "set_master_position_bottom",
	"Set master position center":                "set_master_position_center",
	"Add a master pane":                         "add_master",
	"Remove a master pane":                      "remove_master",
	"Swap with master pane":                     "swap_with_master",
	"Focus master pane":                         "focus_master",
	"Snap fullscreen":                           "snap_fullscreen",
	"Next window":                               "next_window",
	"Previous window":                           "prev_window",
	"Toggle multifocus":                         "toggle_multifocus_active",
	"Toggle multifocus on all panes":            "toggle_multifocus_all",
	"Toggle floating":                           "gui_toggle_float",
	"New session":                               "gui_new_session",
	"Settings":                                  "gui_settings",
	"Theme picker":                              "gui_themes",
	"Toggle sidebar":                            "gui_toggle_sidebar",
	"Switch session":                            "gui_sessions",
	"Agents Inbox, what is waiting for you":     "gui_inbox",
	"Agents go to the oldest waiting":           "prefix_next_attention",
	"Show help":                                 "gui_keys",
	"Hints label text on the pane to copy it":   "gui_hints",
	"Scratch show or hide the scratch terminal": "toggle_scratch",
	"Panes label the panes and focus one":       "gui_pane_picker",
	"Enter copy mode":                           "gui_copy_mode",
	"Save layout":                               "gui_save_layout",
	"Load layout":                               "gui_load_layout",
}

// paletteRuns are the rows with no action of their own that the palette
// command runs in the model. Each changes the session, which the next state
// shows.
var paletteRuns = map[string]bool{
	"Smart split":                                      true,
	"Next layout":                                      true,
	"Previous layout":                                  true,
	"Clear multifocus":                                 true,
	"Layout: BSP tiling":                               true,
	"Layout: master-stack":                             true,
	"Layout: scrolling (niri-style)":                   true,
	"Layout: disable tiling":                           true,
	"Scroll: cycle column width":                       true,
	"Scroll: maximize column width":                    true,
	"Scroll: move window into the column below":        true,
	"Scroll: move window out to its own column":        true,
	"Reload config":                                    true,
	"Picture in picture pin or unpin the focused pane": true,
}

// palette returns the rows of the palette event.
func (m *model) palette() []PaletteRow {
	items := m.os.BridgePaletteItems()
	rows := make([]PaletteRow, 0, len(items))
	for _, it := range items {
		row := PaletteRow{Name: it.Name, Category: it.Category, Shortcut: it.Shortcut}
		if a, ok := paletteActions[it.Name]; ok {
			row.Action = a
		} else if !paletteRunnable(it) {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

// paletteRunnable says whether the palette command runs a row.
func paletteRunnable(it app.CommandPaletteItem) bool {
	return paletteRuns[it.Name] || it.Category == app.PaletteCategoryCommands || it.Category == app.PaletteCategoryPlugins
}

// sendPalette sends the palette event when its rows changed.
func (m *model) sendPalette() {
	rows := m.palette()
	key := fmt.Sprint(rows)
	if key == m.paletteSent {
		return
	}
	m.paletteSent = key
	m.out.JSON(Event{Type: "palette", Palette: rows})
}

// runPalette runs one row of the palette by its name.
func (m *model) runPalette(name string) (tea.Cmd, error) {
	if name == "" {
		return nil, fmt.Errorf("palette needs name, a row of the palette event")
	}
	if a, ok := paletteActions[name]; ok {
		return nil, fmt.Errorf("%q is the action %s: run that action", name, a)
	}
	for _, it := range m.os.BridgePaletteItems() {
		if it.Name != name {
			continue
		}
		if !paletteRunnable(it) || it.Action == nil {
			return nil, fmt.Errorf("%q shows something only a screen can show, so the bridge does not run it", name)
		}
		next, cmd := it.Action(m.os)
		if next != nil {
			m.os = next
		}
		return cmd, nil
	}
	return nil, fmt.Errorf("no palette row %q", strings.TrimSpace(name))
}
