package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

func listKeybindings(asJSON bool) error {
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		fmt.Fprintln(os.Stderr, "Using default keybindings...")
		userConfig = config.DefaultConfig()
	}
	rows := keybindRows(userConfig)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	leader := userConfig.Keybindings.LeaderKey
	if leader == "" {
		leader = config.DefaultLeaderKey
	}
	printKeybindingsTable(rows, leader)
	return nil
}

// keybindGroup is the rows of one table of `tuios keybinds list`.
type keybindGroup struct {
	title string
	rows  []keybindRow
}

// groupKeybindRows splits rows into the list's tables, in the order the rows
// come: one per scope, with the command entries in a table of their own.
func groupKeybindRows(rows []keybindRow) []keybindGroup {
	var groups []keybindGroup
	at := map[string]int{}
	add := func(id, title string, r keybindRow) {
		i, ok := at[id]
		if !ok {
			i = len(groups)
			at[id] = i
			groups = append(groups, keybindGroup{title: title})
		}
		groups[i].rows = append(groups[i].rows, r)
	}
	for _, r := range rows {
		switch {
		case r.Section == config.SectionCommand:
			add("\x00command", "Commands ([[keybindings.command]])", r)
		case r.Scope == keybindScopeUnbound:
			add(r.Scope, "No key (bind one in config.toml, or run it from the command palette)", r)
		case r.Chord != "":
			add(r.Scope, r.ScopeName+" ("+r.Chord+")", r)
		case r.Fixed || r.Section == "copy_pipe":
			add(r.Scope, r.ScopeName+" (fixed keys)", r)
		default:
			add(r.Scope, r.ScopeName, r)
		}
	}
	return groups
}

func printKeybindingsTable(rows []keybindRow, leader string) {
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Padding(0, 1)

	cellStyle := lipgloss.NewStyle().
		Padding(0, 1)

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14")).Render("TUIOS Keybindings"))
	fmt.Println()

	for _, g := range groupKeybindRows(rows) {
		t := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
			Headers("Keys", "Action", "Description").
			Rows(keybindTableRows(g.rows)...).
			StyleFunc(func(row, _ int) lipgloss.Style {
				if row == -1 {
					return headerStyle
				}
				return cellStyle
			})

		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11")).Render(g.title))
		fmt.Println(t.Render())
		fmt.Println()
	}

	note := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Italic(true).
		Render("Note: " + leader + " is the leader key. Press it, then the next key in the list.\n" +
			"Set keybindings.leader_key to move it.\n" +
			"`tuios keybinds list --json` prints these rows as JSON.\n" +
			"`tuios keybinds unbind <action>` takes a key off one action.\n" +
			"`tuios keybinds free <key>` hands a key back to the program in the pane.\n" +
			"`tuios keybinds doctor` reports keys that two actions claim.")
	fmt.Println(note)
	fmt.Println()
}

// keybindTableRows is the Keys, Action and Description columns of one table
// of 'tuios keybinds list'.
func keybindTableRows(rows []keybindRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		keys := strings.Join(r.Keys, ", ")
		if keys == "" {
			keys = "(none)"
		}
		action := r.Action
		switch {
		case r.Section == "copy_pipe":
			action = "copy_pipe"
		case r.Fixed && action == "":
			action = "-"
		}
		out = append(out, []string{keys, action, r.Description})
	}
	return out
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
		Render("Found " + plural.Count(len(customizations), "customized keybinding"))
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

	for _, name := range config.SectionNames() {
		compareSections(userCfg.Keybindings.SectionFor(name), defaultCfg.Keybindings.SectionFor(name))
	}

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

// loadKeybindConfig reads the user's config for a command that only reports
// keys. Unlike config.LoadUserConfig it never writes a default config file, and
// any failure falls back to the defaults. It reads the config the way the
// running app does: the keys tuios cannot read are dropped, and the defaults
// come back in their place. The dropped keys are on DroppedKeys.
func loadKeybindConfig() *config.UserConfig {
	path, err := config.GetConfigPath()
	if err != nil {
		return config.DefaultConfig()
	}
	lc, err := config.LoadLayered(path)
	if err != nil {
		return config.DefaultConfig()
	}
	data, err := lc.Bytes()
	if err != nil {
		return config.DefaultConfig()
	}
	cfg, err := config.ParseUserConfig(data)
	if err != nil {
		return config.DefaultConfig()
	}
	config.DropUnreadableKeys(cfg, lc)
	return cfg
}

// layoutSaveHint is what 'tuios layout list' prints when nothing is saved. A
// layout is saved from inside a session, with the layout prefix or the command
// palette, so the hint names the keys the user's config binds for it.
func layoutSaveHint(cfg *config.UserConfig) string {
	const palette = `pick "Save layout" in the command palette`
	kb := cfg.Keybindings
	leader := kb.LeaderKey
	if leader == "" {
		leader = "ctrl+b"
	}
	layoutKeys := kb.PrefixMode["prefix_layout"]
	saveKeys := kb.LayoutPrefix["layout_prefix_save"]
	if len(layoutKeys) == 0 || len(saveKeys) == 0 {
		return "No saved layouts. To save one, open a session and " + palette + "."
	}
	return fmt.Sprintf("No saved layouts. To save one, open a session and press %s %s %s, or %s.",
		leader, layoutKeys[0], saveKeys[0], palette)
}
