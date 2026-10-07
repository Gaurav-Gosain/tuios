package config

// The [keybindings.gui] section: the desktop chords of a native GUI client
// (tuios-gpui). They need no leader, for people who come from a desktop
// terminal. The GUI looks them up before the leader and the pane, and the
// terminal client never looks them up at all.
//
// A row names a tuios action where tuios has one that does the same thing
// (new_window, toggle_zoom, swap_left), so the palette, the help and the
// config use one name for it. The GUI sends those to the bridge's action
// command. A row whose job only a GUI can do (a pane picker drawn over the
// panes, the font size, a native find bar) names a gui_ action from
// GUIActionDescriptions, and the GUI runs it itself.
//
// On macOS the GUI reads ctrl+shift as cmd. The file spells the keys the same
// way on every platform.

// GUIActionDescriptions describes the actions only a GUI client runs. They are
// kept apart from ActionDescriptions because no key handler in internal/input
// runs them, and every name there must be one that a terminal key can run.
var GUIActionDescriptions = map[string]string{
	"gui_inbox":            "Open the Inbox",
	"gui_switcher":         "Switch to a recent pane. A quick tap goes to the last pane",
	"gui_switcher_back":    "Move back in the recent pane list",
	"gui_split_root_right": "Add a pane at the right of the whole stage",
	"gui_split_root_down":  "Add a pane at the bottom of the whole stage",
	"gui_pane_picker":      "Pick a pane by its letter",
	"gui_resize_left":      "Move the pane's edge 2 columns left",
	"gui_resize_right":     "Move the pane's edge 2 columns right",
	"gui_resize_up":        "Move the pane's edge 1 row up",
	"gui_resize_down":      "Move the pane's edge 1 row down",
	"gui_toggle_float":     "Float or tile the pane",
	"gui_reopen_closed":    "Open the last closed pane again",
	"gui_find":             "Find text in the pane's history",
	"gui_copy_mode":        "Start copy mode in the pane",
	"gui_toggle_sidebar":   "Show or hide the sidebar",
	"gui_copy":             "Copy the selection",
	"gui_paste":            "Paste",
	"gui_keys":             "Show every key",
	"gui_quit":             "Quit the app. Sessions keep running",
	"gui_font_bigger":      "Make the text bigger",
	"gui_font_smaller":     "Make the text smaller",
	"gui_font_reset":       "Reset the text size",
}

// getDefaultGUIKeybinds returns the GUI's default chords, the table in the
// tuios-gpui v2 plan (2.15).
func getDefaultGUIKeybinds() map[string][]string {
	kb := map[string][]string{
		"command_palette":          {"ctrl+shift+p"},
		"gui_inbox":                {"ctrl+shift+i"},
		"prefix_next_attention":    {"ctrl+shift+j"},
		"gui_switcher":             {"ctrl+tab"},
		"gui_switcher_back":        {"ctrl+shift+tab"},
		"new_window":               {"ctrl+shift+t"},
		"split_vertical":           {"ctrl+shift+d"},
		"split_horizontal":         {"ctrl+shift+e"},
		"close_window":             {"ctrl+shift+w"},
		"gui_split_root_right":     {"ctrl+shift+alt+d"},
		"gui_split_root_down":      {"ctrl+shift+alt+e"},
		"toggle_zoom":              {"ctrl+shift+z"},
		"gui_pane_picker":          {"ctrl+shift+space"},
		"terminal_focus_left":      {"alt+left"},
		"terminal_focus_right":     {"alt+right"},
		"terminal_focus_up":        {"alt+up"},
		"terminal_focus_down":      {"alt+down"},
		"gui_resize_left":          {"alt+shift+left"},
		"gui_resize_right":         {"alt+shift+right"},
		"gui_resize_up":            {"alt+shift+up"},
		"gui_resize_down":          {"alt+shift+down"},
		"swap_left":                {"ctrl+shift+alt+left"},
		"swap_right":               {"ctrl+shift+alt+right"},
		"swap_up":                  {"ctrl+shift+alt+up"},
		"swap_down":                {"ctrl+shift+alt+down"},
		"equalize_splits":          {"ctrl+shift+="},
		"rotate_split":             {"ctrl+shift+r"},
		"gui_toggle_float":         {"ctrl+shift+alt+f"},
		"toggle_multifocus_active": {"ctrl+shift+alt+i"},
		"gui_reopen_closed":        {"ctrl+shift+u"},
		"prev_session":             {"ctrl+shift+["},
		"next_session":             {"ctrl+shift+]"},
		"gui_find":                 {"ctrl+shift+f"},
		"gui_copy_mode":            {"ctrl+shift+x"},
		"gui_toggle_sidebar":       {"ctrl+shift+b"},
		"gui_copy":                 {"ctrl+shift+c"},
		"gui_paste":                {"ctrl+shift+v"},
		"gui_keys":                 {"ctrl+shift+/"},
		"gui_quit":                 {"ctrl+shift+q"},
		"gui_font_bigger":          {"ctrl+="},
		"gui_font_smaller":         {"ctrl+-"},
		"gui_font_reset":           {"ctrl+0"},
	}
	for n := '1'; n <= '9'; n++ {
		kb["switch_workspace_"+string(n)] = []string{"alt+" + string(n)}
		kb["move_and_follow_"+string(n)] = []string{"ctrl+shift+alt+" + string(n)}
	}
	return kb
}
