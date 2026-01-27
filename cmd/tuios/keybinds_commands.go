package main

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func listKeybindings() error {
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		fmt.Fprintln(os.Stderr, "Using default keybindings...")
		userConfig = config.DefaultConfig()
	}

	registry := config.NewKeybindRegistry(userConfig)

	printKeybindingsTable(registry)
	return nil
}

func generateWorkspaceActions() []string {
	actions := []string{}
	for i := 1; i <= 9; i++ {
		actions = append(actions, fmt.Sprintf("switch_workspace_%d", i))
	}
	for i := 1; i <= 9; i++ {
		actions = append(actions, fmt.Sprintf("move_and_follow_%d", i))
	}
	return actions
}

func printKeybindingsTable(registry *config.KeybindRegistry) {
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Padding(0, 1)

	cellStyle := lipgloss.NewStyle().
		Padding(0, 1)

	sections := []struct {
		Title   string
		Actions []string
	}{
		{
			Title: "Window Management",
			Actions: []string{
				"new_window", "close_window", "rename_window",
				"minimize_window", "restore_all",
				"next_window", "prev_window",
			},
		},
		{
			Title:   "Workspaces",
			Actions: generateWorkspaceActions(),
		},
		{
			Title: "Layout",
			Actions: []string{
				"snap_left", "snap_right", "snap_fullscreen", "unsnap",
				"toggle_tiling", "swap_left", "swap_right", "swap_up", "swap_down",
			},
		},
		{
			Title: "Modes",
			Actions: []string{
				"enter_terminal_mode", "enter_window_mode",
				"toggle_help", "quit",
			},
		},
		{
			Title: "Selection",
			Actions: []string{
				"toggle_selection", "toggle_selection_term",
				"copy_selection", "paste_clipboard", "clear_selection",
			},
		},
		{
			Title: "System",
			Actions: []string{
				"toggle_logs", "toggle_cache_stats",
			},
		},
	}

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14")).Render("TUIOS Keybindings"))
	fmt.Println()

	for _, section := range sections {
		rows := [][]string{}

		for _, action := range section.Actions {
			keys := registry.GetKeys(action)
			if len(keys) == 0 {
				continue
			}

			desc := config.ActionDescriptions[action]
			if desc == "" {
				desc = action
			}

			keysStr := strings.Join(keys, ", ")
			rows = append(rows, []string{keysStr, desc})
		}

		if len(rows) == 0 {
			continue
		}

		t := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
			Headers("Keys", "Action").
			Rows(rows...).
			StyleFunc(func(row, _ int) lipgloss.Style {
				if row == -1 {
					return headerStyle
				}
				return cellStyle
			})

		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11")).Render(section.Title))
		fmt.Println(t.Render())
		fmt.Println()
	}

	note := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Italic(true).
		Render("Note: Ctrl+B is the prefix key (not configurable). Press Ctrl+B followed by another key for prefix commands.")
	fmt.Println(note)
	fmt.Println()
}

func listCustomKeybindings() error {
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}

	defaultConfig := config.DefaultConfig()

	customizations := findCustomizations(userConfig, defaultConfig)

	if len(customizations) == 0 {
		fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("No custom keybindings configured. All keybindings are using defaults."))
		fmt.Println()
		fmt.Println("Run 'tuios keybinds list' to see all keybindings.")
		return nil
	}

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Padding(0, 1)

	cellStyle := lipgloss.NewStyle().
		Padding(0, 1)

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14")).Render("Custom Keybindings"))
	fmt.Println()

	rows := [][]string{}
	for _, custom := range customizations {
		rows = append(rows, []string{
			custom.Action,
			custom.DefaultKeys,
			custom.CustomKeys,
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("Action", "Default", "Custom").
		Rows(rows...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == -1 {
				return headerStyle
			}
			return cellStyle
		})

	fmt.Println(t.Render())
	fmt.Println()

	note := lipgloss.NewStyle().
		Foreground(lipgloss.Color("11")).
		Render(fmt.Sprintf("Found %d customized keybinding(s)", len(customizations)))
	fmt.Println(note)
	fmt.Println()
	return nil
}

type Customization struct {
	Action      string
	DefaultKeys string
	CustomKeys  string
}

func findCustomizations(userCfg, defaultCfg *config.UserConfig) []Customization {
	var customizations []Customization

	compareSections := func(userSection, defaultSection map[string][]string) {
		for action, defaultKeys := range defaultSection {
			userKeys, exists := userSection[action]
			if !exists {
				continue
			}

			if !stringSlicesEqual(userKeys, defaultKeys) {
				customizations = append(customizations, Customization{
					Action:      formatActionName(action),
					DefaultKeys: strings.Join(defaultKeys, ", "),
					CustomKeys:  strings.Join(userKeys, ", "),
				})
			}
		}
	}

	compareSections(userCfg.Keybindings.WindowManagement, defaultCfg.Keybindings.WindowManagement)
	compareSections(userCfg.Keybindings.Workspaces, defaultCfg.Keybindings.Workspaces)
	compareSections(userCfg.Keybindings.Layout, defaultCfg.Keybindings.Layout)
	compareSections(userCfg.Keybindings.ModeControl, defaultCfg.Keybindings.ModeControl)
	compareSections(userCfg.Keybindings.System, defaultCfg.Keybindings.System)
	compareSections(userCfg.Keybindings.PrefixMode, defaultCfg.Keybindings.PrefixMode)
	compareSections(userCfg.Keybindings.WindowPrefix, defaultCfg.Keybindings.WindowPrefix)
	compareSections(userCfg.Keybindings.MinimizePrefix, defaultCfg.Keybindings.MinimizePrefix)
	compareSections(userCfg.Keybindings.WorkspacePrefix, defaultCfg.Keybindings.WorkspacePrefix)

	return customizations
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func formatActionName(action string) string {
	if desc, ok := config.ActionDescriptions[action]; ok {
		return desc
	}
	return strings.ReplaceAll(action, "_", " ")
}
