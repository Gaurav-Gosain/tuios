package app

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/charmbracelet/lipgloss/v2"
	"github.com/charmbracelet/lipgloss/v2/table"
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

// HelpDimensions holds fixed dimensions for consistent table rendering
type HelpDimensions struct {
	MaxKeyWidth      int // Maximum width of key column across ALL content
	MaxActionWidth   int // Maximum width of action column across ALL content
	MaxCategoryWidth int // Maximum width of category column (for search)
	FixedRows        int // Fixed number of rows to always display (15)
}

// GetHelpCategories generates all help categories from the keybind registry
func GetHelpCategories(registry *config.KeybindRegistry) []HelpCategory {
	categories := []HelpCategory{
		{
			Name:     "Window Management",
			Bindings: generateCategoryBindings(registry, "Window Management", []string{
				"new_window", "close_window", "rename_window",
				"minimize_window", "restore_all",
				"next_window", "prev_window",
			}),
		},
		{
			Name:     "Workspaces",
			Bindings: generateWorkspaceBindings(registry),
		},
		{
			Name:     "Layout",
			Bindings: generateCategoryBindings(registry, "Layout", []string{
				"snap_left", "snap_right", "snap_fullscreen", "unsnap",
				"toggle_tiling", "swap_left", "swap_right", "swap_up", "swap_down",
			}),
		},
		{
			Name:     "Modes",
			Bindings: generateCategoryBindings(registry, "Modes", []string{
				"enter_terminal_mode", "enter_window_mode",
				"toggle_help", "quit",
			}),
		},
		{
			Name:     "System",
			Bindings: generateCategoryBindings(registry, "System", []string{
				"toggle_logs", "toggle_cache_stats",
			}),
		},
		{
			Name:     "Prefix Commands",
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
		"prefix_help", "prefix_quit", "prefix_fullscreen",
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

		// Prefix all keys with "Ctrl+B, " for display
		prefixedKeys := []string{}
		for _, key := range keys {
			prefixedKeys = append(prefixedKeys, "ctrl+b, "+key)
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

// formatKeysWithStyle styles individual key combos with pill-shaped background
func formatKeysWithStyle(keys []string) string {
	// Unicode half circles for pill shape
	const (
		LeftHalfCircle  = string(rune(0xe0b6))
		RightHalfCircle = string(rune(0xe0b4))
	)

	styledKeys := []string{}
	for _, key := range keys {
		// Create pill-style key badge
		bgColor := "5" // Darker purple/magenta
		fgColor := "0" // Black text

		leftCircle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(bgColor)).
			Render(LeftHalfCircle)

		keyLabel := lipgloss.NewStyle().
			Background(lipgloss.Color(bgColor)).
			Foreground(lipgloss.Color(fgColor)).
			Render(" " + key + " ")

		rightCircle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(bgColor)).
			Render(RightHalfCircle)

		styledKeys = append(styledKeys, leftCircle+keyLabel+rightCircle)
	}
	return strings.Join(styledKeys, " ")
}

// truncateString safely truncates a string to maxLen, accounting for multi-byte chars
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen < 3 {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}

// CalculateHelpDimensions scans all categories and calculates fixed dimensions
// This ensures all tables have consistent sizes and don't jump when switching tabs/searching
func CalculateHelpDimensions(categories []HelpCategory) HelpDimensions {
	dims := HelpDimensions{
		MaxKeyWidth:      len("Keys"),      // Start with header width
		MaxActionWidth:   len("Action"),    // Start with header width
		MaxCategoryWidth: len("Category"),  // Start with header width
		FixedRows:        15,               // Always display 15 rows
	}

	// Scan ALL bindings in ALL categories to find maximum widths
	for _, category := range categories {
		// Check category name width for search results
		if len(category.Name) > dims.MaxCategoryWidth {
			dims.MaxCategoryWidth = len(category.Name)
		}

		for _, binding := range category.Bindings {
			// Check keys width
			keysStr := strings.Join(binding.Keys, ", ")
			if len(keysStr) > dims.MaxKeyWidth {
				dims.MaxKeyWidth = len(keysStr)
			}

			// Check action/description width
			if len(binding.Description) > dims.MaxActionWidth {
				dims.MaxActionWidth = len(binding.Description)
			}
		}
	}

	// Cap maximum widths to prevent table overflow
	// Need to be conservative because search results have 3 columns
	// Table border chars + padding add ~10-15 chars of overhead per column
	// Target total width: ~100 chars to fit comfortably

	// Keys column: shorter is better (most keys are concise)
	if dims.MaxKeyWidth > 25 {
		dims.MaxKeyWidth = 25
	}

	// Action column: medium width (descriptions can be longer)
	if dims.MaxActionWidth > 45 {
		dims.MaxActionWidth = 45
	}

	// Category column: short (category names are brief)
	if dims.MaxCategoryWidth > 18 {
		dims.MaxCategoryWidth = 18
	}

	return dims
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

// RenderHelpMenu renders the new table-based help menu
func (m *OS) RenderHelpMenu(width, height int) string {
	categories := GetHelpCategories(m.KeybindRegistry)

	// Auto-select appropriate category based on mode
	if m.HelpCategory < 0 {
		// Auto-select "Modes" category when opening from terminal mode
		if m.Mode == TerminalMode {
			for i, cat := range categories {
				if cat.Name == "Modes" {
					m.HelpCategory = i
					break
				}
			}
		} else {
			m.HelpCategory = 0
		}
	}

	// Ensure category index is valid
	if m.HelpCategory >= len(categories) {
		m.HelpCategory = len(categories) - 1
	}

	// Calculate FIXED dimensions for all tables
	// This ensures tables NEVER change size when switching tabs or searching
	dims := CalculateHelpDimensions(categories)

	// Hide tabs when in search mode
	showTabs := !m.HelpSearchMode
	inSearchMode := m.HelpSearchMode && m.HelpSearchQuery != ""

	// Render bindings table
	var bindingsTable string
	var rowCount int
	if inSearchMode {
		results := SearchBindings(m.HelpSearchQuery, categories)
		bindingsTable, rowCount = renderSearchResults(results, m.HelpScrollOffset, dims)
	} else if m.HelpSearchMode {
		// Search mode but no query - show placeholder centered
		placeholder := lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Italic(true).
			Align(lipgloss.Center).
			Render("Type to search across all keybindings...")
		bindingsTable = placeholder
		rowCount = 0
	} else {
		if m.HelpCategory < len(categories) {
			bindingsTable, rowCount = renderCategoryTable(categories[m.HelpCategory], m.HelpScrollOffset, dims)
		}
	}

	// Constrain scroll offset
	maxScroll := rowCount - dims.FixedRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.HelpScrollOffset > maxScroll {
		m.HelpScrollOffset = maxScroll
	}
	if m.HelpScrollOffset < 0 {
		m.HelpScrollOffset = 0
	}

	hasScrollIndicator := rowCount > dims.FixedRows

	// Build content with EXACT same structure for all states
	var lines []string

	// Line 1-2: Tabs or Search box (2 lines total including blank)
	if showTabs {
		tabs := renderCategoryTabs(categories, m.HelpCategory)
		centeredTabs := lipgloss.NewStyle().Width(85).Align(lipgloss.Center).Render(tabs)
		lines = append(lines, centeredTabs, "")
	} else {
		searchBox := renderSearchBox(m.HelpSearchQuery)
		centeredSearch := lipgloss.NewStyle().Width(85).Align(lipgloss.Center).Render(searchBox)
		lines = append(lines, centeredSearch, "")
	}

	// Line 3: Table (centered)
	centeredTable := lipgloss.NewStyle().Width(85).Align(lipgloss.Center).Render(bindingsTable)
	lines = append(lines, centeredTable)

	// Line 4-5: Scroll indicator space (always 2 lines to maintain height)
	if hasScrollIndicator {
		scrollInfo := lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Italic(true).
			Width(85).
			Align(lipgloss.Center).
			Render(fmt.Sprintf("Row %d-%d of %d", m.HelpScrollOffset+1, min(m.HelpScrollOffset+dims.FixedRows, rowCount), rowCount))
		lines = append(lines, "", scrollInfo)
	} else {
		lines = append(lines, "", "") // Empty lines to maintain height
	}

	// Line 6-7: Footer (always 2 lines)
	footer := renderHelpFooter(m.HelpSearchMode, hasScrollIndicator)
	centeredFooter := lipgloss.NewStyle().Width(85).Align(lipgloss.Center).Render(footer)
	lines = append(lines, "", centeredFooter)

	helpContent := strings.Join(lines, "\n")

	// Add border
	helpBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("14")).
		Padding(1, 2).
		Render(helpContent)

	// Center the entire box
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, helpBox)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// renderCategoryTabs renders the category navigation tabs
func renderCategoryTabs(categories []HelpCategory, activeIdx int) string {
	if len(categories) == 0 {
		return ""
	}

	// Shorter tab names to prevent wrapping
	tabNames := map[string]string{
		"Window Management": "Windows",
		"Workspaces":        "Workspaces",
		"Layout":            "Layout",
		"Modes":             "Modes",
		"Selection":         "Selection",
		"System":            "System",
		"Prefix Commands":   "Prefix",
	}

	tabs := []string{}
	for i, cat := range categories {
		displayName := tabNames[cat.Name]
		if displayName == "" {
			displayName = cat.Name
		}

		var style lipgloss.Style
		if i == activeIdx {
			// Active tab
			style = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("12")).
				Padding(0, 1)
		} else {
			// Inactive tab
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Padding(0, 1)
		}
		tabs = append(tabs, style.Render(displayName))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

// renderSearchBox renders the search input box
func renderSearchBox(query string) string {
	searchLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color("11")).
		Render("Search: ")

	queryStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("15")).
		Render(query + "█") // Blinking cursor effect

	return searchLabel + queryStyle
}

// renderCategoryTable renders a table for a single category using FIXED dimensions
// This ensures the table NEVER changes size regardless of content
func renderCategoryTable(category HelpCategory, scrollOffset int, dims HelpDimensions) (string, int) {
	// Build all rows with styled keys and gap rows for clarity
	allRows := [][]string{}
	for _, binding := range category.Bindings {
		// Format keys with styling (each key gets purple background)
		keysStr := formatKeysWithStyle(binding.Keys)

		// Don't truncate - let it overflow
		desc := binding.Description

		// Add the actual row
		allRows = append(allRows, []string{keysStr, desc})
		// Add a gap row for visual separation
		allRows = append(allRows, []string{"", ""})
	}

	totalRows := len(allRows)

	// Apply scrolling
	startIdx := scrollOffset
	endIdx := scrollOffset + dims.FixedRows
	if startIdx >= totalRows {
		startIdx = totalRows - 1
		if startIdx < 0 {
			startIdx = 0
		}
	}
	if endIdx > totalRows {
		endIdx = totalRows
	}

	// Get visible rows
	displayRows := [][]string{}
	if startIdx < endIdx {
		displayRows = allRows[startIdx:endIdx]
	}

	// ALWAYS pad to exactly FixedRows (15)
	for len(displayRows) < dims.FixedRows {
		displayRows = append(displayRows, []string{"", ""})
	}

	// Create table - allow vertical overflow for readability
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Padding(0, 1)

	keyStyle := lipgloss.NewStyle().
		Padding(0, 1)

	actionStyle := lipgloss.NewStyle().
		Padding(0, 1)

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("Keys", "Action").
		Rows(displayRows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == -1 {
				return headerStyle
			}
			if col == 0 {
				return keyStyle
			}
			return actionStyle
		})

	return t.Render(), totalRows
}

// renderSearchResults renders search results using FIXED dimensions
// This ensures the table NEVER changes size regardless of content
func renderSearchResults(results []HelpBinding, scrollOffset int, dims HelpDimensions) (string, int) {
	// Build all rows with styled keys and gap rows for clarity
	allRows := [][]string{}
	for _, binding := range results {
		// Format keys with styling (each key gets purple background)
		keysStr := formatKeysWithStyle(binding.Keys)

		// Don't truncate - let it overflow
		desc := binding.Description
		cat := binding.Category

		// Add the actual row
		allRows = append(allRows, []string{keysStr, desc, cat})
		// Add a gap row for visual separation
		allRows = append(allRows, []string{"", "", ""})
	}

	totalRows := len(allRows)

	// Apply scrolling
	startIdx := scrollOffset
	endIdx := scrollOffset + dims.FixedRows
	if startIdx >= totalRows {
		startIdx = totalRows - 1
		if startIdx < 0 {
			startIdx = 0
		}
	}
	if endIdx > totalRows {
		endIdx = totalRows
	}

	// Get visible rows
	displayRows := [][]string{}
	if startIdx < endIdx {
		displayRows = allRows[startIdx:endIdx]
	}

	// ALWAYS pad to exactly FixedRows (15)
	for len(displayRows) < dims.FixedRows {
		displayRows = append(displayRows, []string{"", "", ""})
	}

	// Create table - allow vertical overflow for readability
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Padding(0, 1)

	keyStyle := lipgloss.NewStyle().
		Padding(0, 1)

	actionStyle := lipgloss.NewStyle().
		Padding(0, 1)

	categoryStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Padding(0, 1)

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("Keys", "Action", "Category").
		Rows(displayRows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == -1 {
				return headerStyle
			}
			switch col {
			case 0:
				return keyStyle
			case 1:
				return actionStyle
			case 2:
				return categoryStyle
			}
			return lipgloss.NewStyle()
		})

	// Return just the table - no result count (to maintain consistent height)
	// The scroll indicator will show the count
	return t.Render(), totalRows
}

// renderHelpFooter renders the help menu footer with instructions
func renderHelpFooter(searchMode bool, hasScroll bool) string {
	instructions := []string{}

	if searchMode {
		instructions = []string{
			"Type to search",
			"Backspace: Delete",
			"Esc: Clear/Exit",
			"?: Close help",
		}
	} else {
		instructions = []string{
			"←/→: Navigate categories",
			"/: Search",
			"?: Close help",
		}
	}

	if hasScroll {
		instructions = append(instructions, "↑/↓: Scroll")
	}

	footerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Italic(true)

	return footerStyle.Render(strings.Join(instructions, "  •  "))
}
