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
				"minimize_window", "restore_all",
				"next_window", "prev_window",
				"terminal_next_window", "terminal_prev_window",
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
				"split_horizontal", "split_vertical", "rotate_split",
			}),
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

	return bindings
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
		"prefix_window", "prefix_detach", "prefix_selection",
		"prefix_help", "prefix_quit", "prefix_fullscreen", "prefix_settings",
	}

	// Add debug commands (Leader Key + D ...)
	debugCommands := []string{"d_logs", "d_cache_stats", "d_showkeys"}
	for _, cmd := range debugCommands {
		// Add debug commands with special display format
		bindings = append(bindings, HelpBinding{
			Action:      "debug_" + cmd,
			Keys:        []string{config.LeaderKey + ", d, " + cmd[2:]}, // Extract the command part (logs, cache_stats, showkeys)
			Description: getDebugCommandDescription(cmd),
			Category:    "Prefix Commands",
		})
	}

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

// getDebugCommandDescription returns the description for debug commands
func getDebugCommandDescription(cmd string) string {
	switch cmd {
	case "d_logs":
		return "Toggle log viewer"
	case "d_cache_stats":
		return "Toggle cache statistics"
	case "d_showkeys":
		return "Toggle showkeys overlay"
	default:
		return formatActionName(cmd)
	}
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

// Help overlay layout constants.
const (
	helpPanelInnerWidth = 74
	helpVisibleRows     = 14
	helpKeyColMax       = 30
)

// helpTabNames maps full category names to short tab labels.
var helpTabNames = map[string]string{
	"Window Management": "Windows",
	"Workspaces":        "Workspaces",
	"Layout":            "Layout",
	"Tiling":            "Tiling",
	"BSP":               "BSP",
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

	body := m.renderHelpBody(bindings, inSearch, showCategoryTag, pal)

	panel := overlay.Panel{
		Glyph: "", // keyboard
		Title: "Keybindings",
		Width: helpPanelInnerWidth,
		Body:  body,
		Hints: helpHints(inSearch),
	}
	if !inSearch {
		panel.Tabs = make([]string, len(categories))
		for i, cat := range categories {
			panel.Tabs[i] = helpTabLabel(cat.Name)
		}
		panel.ActiveTab = m.HelpCategory
	}

	return panel.Render(pal)
}

// renderHelpBody builds the multi-line body: an optional search box, the
// scrolling list of binding rows, and a scroll indicator, padded to a fixed
// height so the panel never jumps.
func (m *OS) renderHelpBody(bindings []HelpBinding, inSearch, showCategoryTag bool, pal overlay.Palette) string {
	bg := pal.Surface
	var lines []string

	if inSearch {
		cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
		prompt := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("Search ") +
			overlay.Style(bg).Foreground(pal.Fg).Render(m.HelpSearchQuery) + cursor
		lines = append(lines, prompt, overlay.Rule(helpPanelInnerWidth, bg, pal))
	}

	// Clamp scroll to the row count.
	maxScroll := max(len(bindings)-helpVisibleRows, 0)
	m.HelpScrollOffset = max(0, min(m.HelpScrollOffset, maxScroll))

	// Compute a stable key column width from the visible window.
	keyColW := 0
	end := min(m.HelpScrollOffset+helpVisibleRows, len(bindings))
	for i := m.HelpScrollOffset; i < end; i++ {
		w := lipgloss.Width(overlay.KeyBadges(bindings[i].Keys, bg, pal))
		keyColW = max(keyColW, w)
	}
	keyColW = min(keyColW, helpKeyColMax)

	if len(bindings) == 0 {
		msg := "No matching keybindings"
		if !inSearch {
			msg = "No keybindings in this section"
		}
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+msg))
	}

	rowCount := 0
	for i := m.HelpScrollOffset; i < end; i++ {
		lines = append(lines, helpBindingRow(bindings[i], keyColW, showCategoryTag, pal))
		rowCount++
	}
	// Pad to a fixed number of rows so the panel height is stable.
	for rowCount < helpVisibleRows {
		lines = append(lines, overlay.Style(bg).Render(" "))
		rowCount++
	}

	// Scroll indicator.
	if len(bindings) > helpVisibleRows {
		info := fmt.Sprintf("%d-%d of %d", m.HelpScrollOffset+1, end, len(bindings))
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+info))
	} else {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}

	return strings.Join(lines, "\n")
}

// helpBindingRow renders one keybinding row: key badges in a fixed-width gutter,
// the description, and an optional right-aligned category tag (in search view).
func helpBindingRow(b HelpBinding, keyColW int, showCategoryTag bool, pal overlay.Palette) string {
	bg := pal.Surface
	badges := overlay.KeyBadges(b.Keys, bg, pal)
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

	descMax := helpPanelInnerWidth - keyColW - 2 - tagW
	desc := b.Description
	if descMax > 1 && lipgloss.Width(desc) > descMax {
		desc = overlay.Truncate(desc, descMax)
	}

	line := badges + overlay.Style(bg).Render("  ") + overlay.Style(bg).Foreground(pal.Fg).Render(desc)
	if tag != "" {
		used := lipgloss.Width(line)
		gap := helpPanelInnerWidth - used - lipgloss.Width(tag)
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
