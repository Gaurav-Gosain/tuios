package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// HelpBinding represents a single keybinding for the help menu
type HelpBinding struct {
	Action      string   // Action name (e.g., "new_window")
	Keys        []string // Keybindings (e.g., ["n", "ctrl+n"])
	Description string   // Human-readable description
	Category    string   // Category name
}

// HelpCategory represents a category of keybindings
type HelpCategory struct {
	Name     string        // Display name
	Bindings []HelpBinding // Bindings in this category
}

// GetHelpCategories generates all help categories from the keybind registry
func GetHelpCategories(registry *config.KeybindRegistry) []HelpCategory {
	categories := []HelpCategory{
		{
			Name: "Window Management",
			Bindings: generateCategoryBindings(registry, "Window Management", []string{
				"new_window", "close_window", "rename_window",
				"minimize_window", "restore_all", "toggle_zoom",
				"next_window", "prev_window",
				"terminal_next_window", "terminal_prev_window",
				"copy_selection", "focus_sidebar",
				"next_session", "prev_session",
			}),
		},
		{
			Name:     "Workspaces",
			Bindings: generateWorkspaceBindings(registry),
		},
		{
			Name: "Layout",
			Bindings: generateCategoryBindings(registry, "Layout", []string{
				"snap_left", "snap_right", "snap_fullscreen", "unsnap",
				"snap_corner_1", "snap_corner_2", "snap_corner_3", "snap_corner_4",
			}),
		},
		{
			Name: "Tiling",
			Bindings: generateCategoryBindings(registry, "Tiling", []string{
				"toggle_tiling", "swap_left", "swap_right", "swap_up", "swap_down",
				"resize_master_shrink", "resize_master_grow", "resize_height_shrink", "resize_height_grow",
				"resize_master_shrink_left", "resize_master_grow_left", "resize_height_shrink_top", "resize_height_grow_top",
			}),
		},
		{
			Name: "BSP",
			Bindings: generateCategoryBindings(registry, "BSP", []string{
				"split_horizontal", "split_vertical", "rotate_split", "equalize_splits",
				"preselect_left", "preselect_right", "preselect_up", "preselect_down",
			}),
		},
		{
			Name:     "Mouse",
			Bindings: generateMouseBindings(),
		},
		{
			Name:     "Sidebar",
			Bindings: generateSidebarBindings(registry),
		},
		{
			Name:     "Copy Mode",
			Bindings: generateCopyModeBindings(),
		},
		{
			Name: "Modes",
			Bindings: generateCategoryBindings(registry, "Modes", []string{
				"enter_terminal_mode", "enter_window_mode",
				"terminal_exit_mode",
				"toggle_help", "quit",
			}),
		},
		{
			Name:     "Debug",
			Bindings: generateDebugBindings(),
		},
		{
			Name:     "Tape",
			Bindings: generateTapeBindings(),
		},
		{
			Name:     "Prefix",
			Bindings: generatePrefixBindings(registry),
		},
	}

	// Filter out empty categories
	filteredCategories := []HelpCategory{}
	for _, cat := range categories {
		if len(cat.Bindings) > 0 {
			filteredCategories = append(filteredCategories, cat)
		}
	}

	return filteredCategories
}

// generateCategoryBindings generates bindings for a specific category
func generateCategoryBindings(registry *config.KeybindRegistry, categoryName string, actions []string) []HelpBinding {
	bindings := []HelpBinding{}
	for _, action := range actions {
		keys := registry.GetKeys(action)
		if len(keys) == 0 {
			continue // Skip unbound actions
		}

		desc := config.ActionDescriptions[action]
		if desc == "" {
			desc = formatActionName(action)
		}

		bindings = append(bindings, HelpBinding{
			Action:      action,
			Keys:        keys,
			Description: desc,
			Category:    categoryName,
		})
	}
	return bindings
}

// generateWorkspaceBindings generates all workspace-related bindings
func generateWorkspaceBindings(registry *config.KeybindRegistry) []HelpBinding {
	bindings := []HelpBinding{}

	// Add all 9 workspace switches
	for i := 1; i <= 9; i++ {
		action := fmt.Sprintf("switch_workspace_%d", i)
		keys := registry.GetKeys(action)
		if len(keys) > 0 {
			bindings = append(bindings, HelpBinding{
				Action:      action,
				Keys:        keys,
				Description: fmt.Sprintf("Switch to workspace %d", i),
				Category:    "Workspaces",
			})
		}
	}

	// Add all 9 move and follow actions
	for i := 1; i <= 9; i++ {
		action := fmt.Sprintf("move_and_follow_%d", i)
		keys := registry.GetKeys(action)
		if len(keys) > 0 {
			bindings = append(bindings, HelpBinding{
				Action:      action,
				Keys:        keys,
				Description: fmt.Sprintf("Move to workspace %d and follow", i),
				Category:    "Workspaces",
			})
		}
	}

	// Renaming a workspace is a chord rather than a plain key, so its row is
	// built from the same whole-chord hint the pill menu shows.
	if chord := contextMenuHint(registry, "workspace_prefix_rename"); chord != "" {
		bindings = append(bindings, HelpBinding{
			Action:      "workspace_prefix_rename",
			Keys:        []string{chord},
			Description: "Rename workspace",
			Category:    "Workspaces",
		})
	}

	return bindings
}

// generateMouseBindings lists the pointer gestures. They are written out rather
// than derived because there is no registry for them: the gestures are decided
// by internal/input's press/motion/release handlers, and this list is the only
// place a user can read what those handlers do. Rail gestures live in the
// Sidebar section, next to the rail's keys.
func generateMouseBindings() []HelpBinding {
	const cat = "Mouse"
	return []HelpBinding{
		{Keys: []string{"click"}, Description: "Focus a pane and start typing in it", Category: cat},
		{Keys: []string{"double / triple click"}, Description: "Select the word / line under the pointer", Category: cat},
		{Keys: []string{"drag title bar"}, Description: "Move a pane", Category: cat},
		{Keys: []string{"ctrl+drag"}, Description: "Move a pane; drops when ctrl is let go", Category: cat},
		{Keys: []string{"ctrl+shift+click"}, Description: "Add or remove a pane from multi-select", Category: cat},
		{Keys: []string{"drag pane border"}, Description: "Resize one edge: divider or floating side", Category: cat},
		{Keys: []string{"right-drag"}, Description: "Resize a pane from the nearest corner", Category: cat},
		{Keys: []string{"right-click"}, Description: "Pane menu, unless the app tracks the mouse", Category: cat},
		// One badge rather than two: the key column drops the second when a row
		// carries more than it can hold, and losing the shift half would read as
		// if ctrl were the only way in.
		{Keys: []string{"ctrl/shift + right-click"}, Description: "Pane menu, even over a mouse-aware app", Category: cat},
		{Keys: []string{"wheel"}, Description: "Scroll the scrollback, or the app itself", Category: cat},
		{Keys: []string{"drag right edge"}, Description: "Drag the scrollbar, where there is one", Category: cat},
		{Keys: []string{"right-click desktop"}, Description: "Desktop menu", Category: cat},
		{Keys: []string{"click dock entry"}, Description: "Restore that minimized window", Category: cat},
		{Keys: []string{"right-click dock"}, Description: "Dock menu, or the entry's own menu", Category: cat},
		{Keys: []string{"right-click workspace tab"}, Description: "Switch or rename that workspace", Category: cat},
		{Keys: []string{"drag a panel"}, Description: "Move an overlay; click outside to close", Category: cat},
	}
}

// generateSidebarBindings lists the rail: how to reach its keyboard scope, the
// keys inside it, and the gestures that do the same jobs with the pointer. The
// keys come from the [keybindings.sidebar] section, which the global keymap
// deliberately does not carry, so they are read through GetSidebarKeys.
func generateSidebarBindings(registry *config.KeybindRegistry) []HelpBinding {
	const cat = "Sidebar"
	row := func(action, desc string) HelpBinding {
		return HelpBinding{
			Action:      action,
			Keys:        registry.GetSidebarKeys(action),
			Description: desc,
			Category:    cat,
		}
	}

	bindings := generateCategoryBindings(registry, cat, []string{"focus_sidebar"})
	for _, action := range []string{"prefix_explore", "prefix_toggle_sidebar"} {
		for _, key := range registry.GetKeys(action) {
			desc := config.ActionDescriptions[action]
			if desc == "" {
				desc = formatActionName(action)
			}
			bindings = append(bindings, HelpBinding{
				Action:      action,
				Keys:        []string{config.LeaderKey + ", " + key},
				Description: desc,
				Category:    cat,
			})
		}
	}

	// new_session and new_window are bound in the rail's scope but only notify
	// that they are not available yet, so listing them would be a lie.
	bindings = append(bindings,
		row("exit", "Leave the rail, back to the panes"),
		row("cursor_down", "Move the cursor down a row"),
		row("cursor_up", "Move the cursor up a row"),
		row("first", "Jump to the first row"),
		row("last", "Jump to the last row"),
		row("collapse", "Step up to the previous section"),
		row("expand", "Step down to the next section"),
		row("activate", "Activate the row: attach, or focus the pane"),
	)
	if first, last := registry.GetSidebarKeys("jump_1"), registry.GetSidebarKeys("jump_9"); len(first) > 0 && len(last) > 0 {
		bindings = append(bindings, HelpBinding{
			Keys:        []string{first[0] + "-" + last[0]},
			Description: "Jump to a session by its position in the rail",
			Category:    cat,
		})
	}
	bindings = append(bindings,
		row("reorder_down", "Move the session down the rail"),
		row("reorder_up", "Move the session up the rail"),
		row("section", "Cycle the sessions, terminals and agents sections"),
		row("agents_filter", "Agents: all sessions, or this one"),
		row("agents_sort", "Agents: by priority, or by recency"),
		row("narrow", "Collapse the rail to its glyph strip"),
		row("widen", "Expand the rail back to its width"),
		row("menu", "Open the menu for the row under the cursor"),
		row("kill", "Open the session menu, which is where Kill lives"),
		row("rename", "Rename the window under the cursor"),
		row("accent", "Recolor the window under the cursor"),
	)

	// Drop rows whose action is unbound, exactly as generateCategoryBindings does.
	bound := bindings[:0]
	for _, b := range bindings {
		if len(b.Keys) > 0 {
			bound = append(bound, b)
		}
	}
	bindings = bound

	return append(bindings,
		HelpBinding{Keys: []string{"click a row"}, Description: "Attach to that session, or focus that pane", Category: cat},
		HelpBinding{Keys: []string{"hover a session"}, Description: "Preview its panes in the terminals section", Category: cat},
		HelpBinding{Keys: []string{"drag a session"}, Description: "Reorder the sessions", Category: cat},
		HelpBinding{Keys: []string{"right-click a row"}, Description: "Open that row's menu", Category: cat},
		HelpBinding{Keys: []string{"drag the rail edge"}, Description: "Resize the rail", Category: cat},
		HelpBinding{Keys: []string{"hover a clipped row"}, Description: "Scroll its text past the edge to read the rest", Category: cat},
		HelpBinding{Keys: []string{"wheel"}, Description: "Scroll the rail", Category: cat},
	)
}

// generateCopyModeBindings generates copy mode keybindings
func generateCopyModeBindings() []HelpBinding {
	return []HelpBinding{
		{Keys: []string{config.LeaderKey + ", ["}, Description: "Enter copy mode", Category: "Copy Mode"},
		{Keys: []string{"h, j, k, l"}, Description: "Move cursor", Category: "Copy Mode"},
		{Keys: []string{"w, b, e"}, Description: "Word fwd/back/end", Category: "Copy Mode"},
		{Keys: []string{"0, ^, $"}, Description: "Line start/first/end", Category: "Copy Mode"},
		{Keys: []string{"gg, G"}, Description: "Jump top/bottom", Category: "Copy Mode"},
		{Keys: []string{"ctrl+u, ctrl+d"}, Description: "Half page up/down", Category: "Copy Mode"},
		{Keys: []string{"/, ?, n, N"}, Description: "Search", Category: "Copy Mode"},
		{Keys: []string{"v, V"}, Description: "Visual char/line", Category: "Copy Mode"},
		{Keys: []string{"y, c"}, Description: "Yank to clipboard", Category: "Copy Mode"},
		{Keys: []string{"i, q, Esc"}, Description: "Exit copy mode", Category: "Copy Mode"},
	}
}

// generateDebugBindings generates debug keybindings
func generateDebugBindings() []HelpBinding {
	return []HelpBinding{
		{Keys: []string{config.LeaderKey + ", D, l"}, Description: "Toggle log viewer", Category: "Debug"},
		{Keys: []string{config.LeaderKey + ", D, c"}, Description: "Toggle cache stats", Category: "Debug"},
		{Keys: []string{config.LeaderKey + ", D, k"}, Description: "Toggle showkeys", Category: "Debug"},
		{Keys: []string{config.LeaderKey + ", D, a"}, Description: "Toggle animations", Category: "Debug"},
	}
}

// generateTapeBindings generates tape scripting bindings
func generateTapeBindings() []HelpBinding {
	bindings := []HelpBinding{}

	// Add tape commands with prefix notation
	bindings = append(bindings, HelpBinding{
		Action:      "tape_manager",
		Keys:        []string{config.LeaderKey + ", T, m"},
		Description: "Open tape manager",
		Category:    "Tape Scripting",
	})
	bindings = append(bindings, HelpBinding{
		Action:      "tape_record",
		Keys:        []string{config.LeaderKey + ", T, r"},
		Description: "Start recording",
		Category:    "Tape Scripting",
	})
	bindings = append(bindings, HelpBinding{
		Action:      "tape_stop",
		Keys:        []string{config.LeaderKey + ", T, s"},
		Description: "Stop recording",
		Category:    "Tape Scripting",
	})

	return bindings
}

// generatePrefixBindings generates prefix command bindings
func generatePrefixBindings(registry *config.KeybindRegistry) []HelpBinding {
	bindings := []HelpBinding{}

	// Get all prefix actions from the config
	prefixActions := []string{
		"prefix_new_window", "prefix_close_window", "prefix_rename_window",
		"prefix_next_window", "prefix_prev_window",
		"prefix_select_0", "prefix_select_1", "prefix_select_2",
		"prefix_select_3", "prefix_select_4", "prefix_select_5",
		"prefix_select_6", "prefix_select_7", "prefix_select_8", "prefix_select_9",
		"prefix_toggle_tiling", "prefix_workspace", "prefix_minimize",
		"prefix_window", "prefix_detach", "prefix_close_session", "prefix_selection",
		"prefix_help", "prefix_quit", "prefix_fullscreen", "prefix_settings",
		"prefix_split_horizontal", "prefix_split_vertical", "prefix_rotate_split",
		"prefix_equalize_splits", "prefix_layout",
		"prefix_scrollback", "prefix_command_palette", "prefix_session_switcher",
		"prefix_workspace_switcher",
		"prefix_toggle_sidebar", "prefix_explore",
		"prefix_jump_notif",
	}

	// Debug commands are deliberately not listed here. They used to be, built
	// from action names by slicing off a prefix, which rendered them as the
	// action name rather than the key ("ctrl+b, d, cache_stats" for what is
	// actually ctrl+b, D, c). The Debug category above lists the real keys.

	for _, action := range prefixActions {
		keys := registry.GetKeys(action)
		if len(keys) == 0 {
			continue
		}

		desc := config.ActionDescriptions[action]
		if desc == "" {
			desc = formatActionName(action)
		}

		// Prefix all keys with the leader key for display
		prefixedKeys := []string{}
		for _, key := range keys {
			prefixedKeys = append(prefixedKeys, config.LeaderKey+", "+key)
		}

		bindings = append(bindings, HelpBinding{
			Action:      action,
			Keys:        prefixedKeys,
			Description: desc,
			Category:    "Prefix Commands",
		})
	}

	return bindings
}

// formatActionName formats an action name for display
func formatActionName(action string) string {
	// Remove prefix_ if present
	action = strings.TrimPrefix(action, "prefix_")
	// Replace underscores with spaces and title case
	parts := strings.Split(action, "_")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}

// FuzzyMatch performs fuzzy matching on a string
func FuzzyMatch(query, target string) (bool, []int) {
	query = strings.ToLower(query)
	target = strings.ToLower(target)

	if query == "" {
		return true, []int{}
	}

	matchIndices := []int{}
	queryIdx := 0

	for i := 0; i < len(target) && queryIdx < len(query); i++ {
		if target[i] == query[queryIdx] {
			matchIndices = append(matchIndices, i)
			queryIdx++
		}
	}

	return queryIdx == len(query), matchIndices
}

// SearchBindings performs fuzzy search across all bindings
func SearchBindings(query string, categories []HelpCategory) []HelpBinding {
	if query == "" {
		return []HelpBinding{}
	}

	results := []HelpBinding{}

	for _, category := range categories {
		for _, binding := range category.Bindings {
			// Search in description
			if matched, _ := FuzzyMatch(query, binding.Description); matched {
				results = append(results, binding)
				continue
			}

			// Search in keys
			for _, key := range binding.Keys {
				if matched, _ := FuzzyMatch(query, key); matched {
					results = append(results, binding)
					break
				}
			}

			// Search in action name
			if matched, _ := FuzzyMatch(query, binding.Action); matched {
				results = append(results, binding)
			}
		}
	}

	return results
}

// Help overlay layout constants. These are the preferred sizes; a screen
// narrower or shorter than the panel prefers gets a panel fitted to it (see
// overlay_fit.go).
const (
	helpPanelInnerWidth = 74
	helpVisibleRows     = 14
	helpKeyColMax       = 30

	// helpCategoryTagWidth is the narrowest inner width that still has room for
	// the right-aligned category tag next to a search result. Below it the tag
	// is dropped so the description keeps the space.
	helpCategoryTagWidth = 48
)

// helpTabNames maps full category names to short tab labels.
// The strip is one row at the panel's preferred width and has to stay that way,
// so a label is only as long as it needs to be to pick its section out from the
// neighbours: "Snap" says which of the three layout sections it is, and "Rail"
// is what the sidebar is called everywhere else.
var helpTabNames = map[string]string{
	"Window Management": "Windows",
	"Workspaces":        "Spaces",
	"Layout":            "Snap",
	"Tiling":            "Tiling",
	"BSP":               "BSP",
	"Mouse":             "Mouse",
	"Sidebar":           "Rail",
	"Copy Mode":         "Copy",
	"Modes":             "Modes",
	"Debug":             "Debug",
	"Tape":              "Tape",
	"Prefix":            "Prefix",
	"Selection":         "Selection",
	"System":            "System",
	"Prefix Commands":   "Prefix",
}

func helpTabLabel(name string) string {
	if short, ok := helpTabNames[name]; ok {
		return short
	}
	return name
}

// RenderHelpMenu renders the keybindings overlay on the shared panel grammar.
func (m *OS) RenderHelpMenu() (string, overlay.Geometry) {
	categories := GetHelpCategories(m.KeybindRegistry)
	if len(categories) == 0 {
		return "", overlay.Geometry{}
	}

	// Auto-select an appropriate category based on mode when first opened.
	if m.HelpCategory < 0 {
		m.HelpCategory = 0
		if m.Mode == TerminalMode {
			for i, cat := range categories {
				if cat.Name == "Modes" {
					m.HelpCategory = i
					break
				}
			}
		}
	}
	if m.HelpCategory >= len(categories) {
		m.HelpCategory = len(categories) - 1
	}

	pal := theme.UI()
	inSearch := m.HelpSearchMode

	var bindings []HelpBinding
	showCategoryTag := false
	if inSearch {
		bindings = SearchBindings(m.HelpSearchQuery, categories)
		showCategoryTag = true
	} else {
		bindings = categories[m.HelpCategory].Bindings
	}

	var tabs []string
	if !inSearch {
		tabs = make([]string, len(categories))
		for i, cat := range categories {
			tabs[i] = helpTabLabel(cat.Name)
		}
	}

	width := m.panelWidth(helpPanelInnerWidth)
	hints := helpHints(inSearch)
	// Body lines that are not binding rows: the scroll indicator, plus the
	// search prompt and its rule when searching.
	extra := 1
	if inSearch {
		extra += 2
	}
	rows, hints := m.panelBody(helpVisibleRows, extra, width, tabs, hints)
	if width < helpCategoryTagWidth {
		showCategoryTag = false
	}

	body := m.renderHelpBody(bindings, inSearch, showCategoryTag, pal, width, rows)

	panel := overlay.Panel{
		Glyph: "", // keyboard
		Title: "Keybindings",
		Width: width,
		Body:  body,
		Hints: hints,
	}
	if !inSearch {
		panel.Tabs = tabs
		panel.ActiveTab = m.HelpCategory
	}

	return panel.Render(pal)
}

// renderHelpBody builds the multi-line body: an optional search box, the
// scrolling list of binding rows, and a scroll indicator, padded to a fixed
// height so the panel never jumps.
func (m *OS) renderHelpBody(bindings []HelpBinding, inSearch, showCategoryTag bool, pal overlay.Palette, width, visibleRows int) string {
	bg := pal.Surface
	var lines []string

	if inSearch {
		cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
		prompt := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("Search ") +
			overlay.Style(bg).Foreground(pal.Fg).Render(m.HelpSearchQuery) + cursor
		lines = append(lines, prompt, overlay.Rule(width, bg, pal))
	}

	// Clamp scroll to the row count.
	maxScroll := max(len(bindings)-visibleRows, 0)
	m.HelpScrollOffset = max(0, min(m.HelpScrollOffset, maxScroll))

	// Compute a stable key column width from the visible window. On a narrow
	// panel the badges may not leave a usable description column, so the key
	// column never takes more than half the width.
	keyColW := 0
	end := min(m.HelpScrollOffset+visibleRows, len(bindings))
	for i := m.HelpScrollOffset; i < end; i++ {
		w := lipgloss.Width(overlay.KeyBadges(bindings[i].Keys, bg, pal))
		keyColW = max(keyColW, w)
	}
	keyColW = min(keyColW, min(helpKeyColMax, max(width/2, 6)))

	if len(bindings) == 0 {
		msg := "No matching keybindings"
		if !inSearch {
			msg = "No keybindings in this section"
		}
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgDim).Italic(true).Render("  "+msg))
	}

	rowCount := 0
	for i := m.HelpScrollOffset; i < end; i++ {
		lines = append(lines, helpBindingRow(bindings[i], keyColW, showCategoryTag, pal, width))
		rowCount++
	}
	// Pad to a fixed number of rows so the panel height is stable.
	for rowCount < visibleRows {
		lines = append(lines, overlay.Style(bg).Render(" "))
		rowCount++
	}

	// Scroll indicator.
	if len(bindings) > visibleRows {
		info := fmt.Sprintf("%d-%d of %d", m.HelpScrollOffset+1, end, len(bindings))
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgDim).Italic(true).Render("  "+info))
	} else {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}

	return strings.Join(lines, "\n")
}

// helpBindingRow renders one keybinding row: key badges in a fixed-width gutter,
// the description, and an optional right-aligned category tag (in search view).
func helpBindingRow(b HelpBinding, keyColW int, showCategoryTag bool, pal overlay.Palette, width int) string {
	bg := pal.Surface
	// A binding with more key combos than the column can hold drops the extra
	// combos, then shortens the last one, rather than pushing the description
	// off the panel.
	keys := b.Keys
	badges := overlay.KeyBadges(keys, bg, pal)
	for len(keys) > 1 && lipgloss.Width(badges) > keyColW {
		keys = keys[:len(keys)-1]
		badges = overlay.KeyBadges(keys, bg, pal)
	}
	if len(keys) == 1 && lipgloss.Width(badges) > keyColW {
		badges = overlay.KeyBadge(overlay.Truncate(keys[0], max(keyColW-2, 1)), pal)
	}
	bw := lipgloss.Width(badges)
	if bw < keyColW {
		badges += overlay.Style(bg).Render(strings.Repeat(" ", keyColW-bw))
	}

	// Reserve space for a right-aligned category tag when searching.
	tag := ""
	tagW := 0
	if showCategoryTag && b.Category != "" {
		label := helpTabLabel(b.Category)
		tag = overlay.Style(bg).Foreground(pal.FgMute).Render(label)
		tagW = lipgloss.Width(label) + 2
	}

	descMax := width - keyColW - 2 - tagW
	desc := b.Description
	if lipgloss.Width(desc) > descMax {
		desc = overlay.Truncate(desc, descMax)
	}

	line := badges + overlay.Style(bg).Render("  ") + overlay.Style(bg).Foreground(pal.Fg).Render(desc)
	if tag != "" {
		used := lipgloss.Width(line)
		gap := width - used - lipgloss.Width(tag)
		if gap > 0 {
			line += overlay.Style(bg).Render(strings.Repeat(" ", gap)) + tag
		}
	}
	return line
}

// helpHints returns the footer key hints for the current help mode.
func helpHints(inSearch bool) []overlay.Hint {
	if inSearch {
		return []overlay.Hint{
			{Key: "type", Label: "filter"},
			{Key: "↑↓", Label: "scroll"},
			{Key: "esc", Label: "clear"},
			{Key: "?", Label: "close"},
		}
	}
	return []overlay.Hint{
		{Key: "/", Label: "search"},
		{Key: "←→", Label: "section"},
		{Key: "↑↓", Label: "scroll"},
		{Key: "?", Label: "close"},
	}
}
