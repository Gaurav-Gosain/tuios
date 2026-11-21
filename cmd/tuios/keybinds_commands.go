package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/spf13/cobra"
)

func newKeybindsCommand() *cobra.Command {
	keybindsCmd := &cobra.Command{
		Use:     "keybinds",
		Aliases: []string{"keys", "kb"},
		Short:   "List, check and change keybindings",
		Long: `List the keybindings, check them for conflicts, and change them in config.toml.

Start with "tuios keybinds list". Use "tuios keybinds explain <key>" to see
what one key does.`,
	}

	keybindsCmd.AddCommand(
		newKeybindsListCommand(),
		newKeybindsCustomCommand(),
		newKeybindsDoctorCommand(),
		newKeybindsExplainCommand(),
		newKeybindsUnbindCommand(),
		newKeybindsFreeCommand(),
		newKeybindsPresetCommand(),
		newKeybindsCheatSheetCommand(),
	)

	return keybindsCmd
}

func newKeybindsListCommand() *cobra.Command {
	var keybindsListJSON bool
	keybindsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List every keybinding and the action it runs",
		Long: `List every action and the keys that run it, as your config.toml sets them.

The list has one table for each scope. A scope is where the keys act: window
mode, terminal mode, the sidebar, the Inbox, or a prefix menu such as ctrl+b L.
Each key in a prefix menu shows with its chord.

After the scopes, the list shows the fixed keys. tuios reads these keys itself,
and you cannot rebind them: copy mode, hints mode, the message view, the list
keys and the mouse. The last table shows the actions that have no key.

Use --json to get the same rows as a JSON array.`,
		Example: `  # Show every keybinding
  tuios keybinds list

  # Find the keys of one action
  tuios keybinds list --json | jq '.[] | select(.action == "toggle_tiling")'`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return listKeybindings(keybindsListJSON)
		},
	}
	addJSONFlag(keybindsListCmd, &keybindsListJSON, "print the rows as a JSON array")
	return keybindsListCmd
}

func newKeybindsCustomCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list-custom",
		Short: "List the keybindings that differ from the defaults",
		Long: `List only the keybindings that your config.toml changes.

Each row shows the default keys and your keys.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return listCustomKeybindings()
		},
	}
}

func newKeybindsDoctorCommand() *cobra.Command {
	var (
		keybindsJSON  bool
		keybindsGuest string
	)
	keybindsDoctorCmd := &cobra.Command{
		Use:   "doctor",
		Short: "Report keybinding conflicts",
		Long: `Report each key that two actions claim, each key that tuios takes from the
pane, and each of those keys that a common program uses.

Each finding names its evidence. "certain" comes from the tuios key routing.
"observed" comes from a pane. "reference" comes from a list of common program
defaults, and is not detection. --json prints the same report that the keybind
manager shows.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return keybindsDoctor(keybindsJSON, keybindsGuest)
		},
	}
	addJSONFlag(keybindsDoctorCmd, &keybindsJSON, "emit the report as JSON")
	keybindsDoctorCmd.Flags().StringVar(&keybindsGuest, "guest", "", "treat this program as the one running in the pane")

	return keybindsDoctorCmd
}

func newKeybindsExplainCommand() *cobra.Command {
	var (
		keybindsJSON  bool
		keybindsGuest string
	)

	keybindsExplainCmd := &cobra.Command{
		Use:   "explain <key>",
		Short: "Say what tuios does with one key",
		Long: `Show each scope that the key acts in, and whether the program in the pane
gets the key. Also show the key that the terminal sends the same way, and the
common programs that use the key. The key recorder in the keybind manager
shows the same answer.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return keybindsExplain(args[0], keybindsJSON, keybindsGuest)
		},
	}
	addJSONFlag(keybindsExplainCmd, &keybindsJSON, "emit the answer as JSON")
	keybindsExplainCmd.Flags().StringVar(&keybindsGuest, "guest", "", "treat this program as the one running in the pane")

	return keybindsExplainCmd
}

func newKeybindsUnbindCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "unbind <action> [key]",
		Short: "Take a key off one action",
		Long: `Take a key off one action and write the change to config.toml.

Name a key to remove that one. Name none and the action loses every key it has.
An action with no keys is written as an empty list, which is different from
leaving it out of the file: an action the file does not mention gets its default
back at the next load, and an empty list does not.

This changes one action. To stop tuios taking a key at all, so the program in
your pane receives it, use ` + "`tuios keybinds free`" + `.`,
		Example: `  # Stop w closing a window, leaving x
  tuios keybinds unbind close_window w

  # Leave the action with no key at all
  tuios keybinds unbind close_window`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			key := ""
			if len(args) > 1 {
				key = args[1]
			}
			return keybindsUnbind(args[0], key)
		},
	}
}

func newKeybindsFreeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "free <key>",
		Short: "Hand a key back to the program in the pane",
		Long: `Take one key off every action in every scope and write the change to
config.toml.

Every scope at once is the point. A key tuios still claims anywhere is a key the
program in your pane never sees, so freeing one table at a time does not free
the key. Each action that runs out of keys is written as an empty list, so the
default does not come back at the next load.

Two things this cannot take. The leader key is keybindings.leader_key rather
than an entry in a table, so it is moved rather than unbound. A few keys are
read by the input path itself and have no config entry. Either way the command
says so instead of reporting a success.`,
		Example: `  # Give alt+left back to your shell
  tuios keybinds free alt+left

  # Check first: this says every scope the key acts in
  tuios keybinds explain alt+left`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return keybindsFree(args[0])
		},
	}
}

func newKeybindsPresetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "preset [name]",
		Short: "Show or set the keybind preset: tuios, or the keys of tmux",
		Long: `With no name, print the preset in use and its leader. With a name, use that
preset and write it to config.toml.

A preset changes the leader, the keys after the leader and the terminal-mode
keys. Window mode, the sidebar, copy mode and the Inbox keep the tuios keys.
A tuios feature whose key the preset uses moves to the tuios menu: press the
leader, then ctrl+t, then the key it had.

Your own bindings in config.toml win over the preset. Setting a preset
removes keybindings.leader_key, so the leader is the preset's.`,
		Example: `  tuios keybinds preset
  tuios keybinds preset tmux
  tuios keybinds preset list
  tuios keybinds preset show tmux`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return keybindsPreset("")
			case args[0] == "list":
				return keybindsPresetList()
			case args[0] == "show" && len(args) == 2:
				return keybindsPresetShow(args[1])
			case len(args) == 1:
				return keybindsPreset(args[0])
			}
			return errfmt.Errorf("use: tuios keybinds preset [name | list | show NAME]")
		},
	}
}

func newKeybindsCheatSheetCommand() *cobra.Command {
	var cheatSheetJSON bool

	keybindsCheatSheetCmd := &cobra.Command{
		Use:   "cheatsheet [preset]",
		Short: "Show what a keybind preset keeps, moves and does not have",
		Long: `Print a preset's keys in three groups: the keys that do what they do in the
other multiplexer, the tuios keys the preset moved and where they are now, and
what the other multiplexer has that tuios does not. With no name, the preset in
use.`,
		Example: `  tuios keybinds cheatsheet tmux
  tuios keybinds cheatsheet --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return keybindsCheatSheet(name, cheatSheetJSON)
		},
	}
	addJSONFlag(keybindsCheatSheetCmd, &cheatSheetJSON)

	return keybindsCheatSheetCmd
}

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
	if p := userConfig.Keybindings.Preset; p != "" && p != config.PresetTuios {
		fmt.Printf("Preset: %s. Run `tuios keybinds cheatsheet` to see what it changed.\n\n", p)
	}
	printKeybindingsTable(rows, leader)
	return nil
}

type keybindGroup struct {
	title string
	rows  []keybindRow
}

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
		case r.Section == config.SectionAction:
			add("\x00actions", "Your actions ([[actions]], tuios actions list)", r)
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
		desc := r.Description
		if len(r.Also) > 0 {
			desc += " (also: " + strings.Join(r.Also, ", ") + ")"
		}
		out = append(out, []string{keys, action, desc})
	}
	return out
}

func listCustomKeybindings() error {
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		return errfmt.Errorf("error loading config: %w", err)
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

			if !slices.Equal(userKeys, defaultKeys) {
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

func formatActionName(action string) string {
	if desc, ok := config.ActionDescriptions[action]; ok {
		return desc
	}
	return strings.ReplaceAll(action, "_", " ")
}

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
