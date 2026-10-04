package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/explore"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The opt-in explorers: tuios help -i, tuios config browse and tuios keybinds
// browse. Each opens only when asked for by that flag or subcommand, never
// because stdout is a terminal: an agent in a pane has a terminal too, and a
// command it runs for text must not turn into a program it cannot leave.
// Each shows only what the plain command also prints with --json.

// addExplorers wires the explorers into the command tree.
func addExplorers(root *cobra.Command) {
	root.SetHelpCommand(newHelpCommand())
	if cfg, _, err := root.Find([]string{"config"}); err == nil && cfg != root {
		cfg.AddCommand(newConfigBrowseCommand())
	}
	if kb, _, err := root.Find([]string{"keybinds"}); err == nil && kb != root {
		kb.AddCommand(newKeybindsBrowseCommand())
	}
}

// commandDoc is one command as tuios help --json prints it.
type commandDoc struct {
	Path    string    `json:"path"`
	Use     string    `json:"use"`
	Short   string    `json:"short"`
	Long    string    `json:"long,omitempty"`
	Example string    `json:"example,omitempty"`
	Aliases []string  `json:"aliases,omitempty"`
	Flags   []flagDoc `json:"flags,omitempty"`
	Depth   int       `json:"depth"`
}

// flagDoc is one flag of a command.
type flagDoc struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
	Usage     string `json:"usage"`
}

// commandDocs lists c and every command under it that help lists, in the
// order help lists them.
func commandDocs(c *cobra.Command) []commandDoc {
	var out []commandDoc
	base := strings.Count(c.CommandPath(), " ")
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		d := commandDoc{
			Path:    c.CommandPath(),
			Use:     c.UseLine(),
			Short:   c.Short,
			Long:    c.Long,
			Example: c.Example,
			Aliases: c.Aliases,
			Depth:   strings.Count(c.CommandPath(), " ") - base,
		}
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden || f.Name == "help" {
				return
			}
			fd := flagDoc{Name: f.Name, Shorthand: f.Shorthand, Type: f.Value.Type(), Usage: f.Usage}
			if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "[]" && f.DefValue != "0" {
				fd.Default = f.DefValue
			}
			d.Flags = append(d.Flags, fd)
		})
		out = append(out, d)
		for _, sub := range c.Commands() {
			if sub.IsAvailableCommand() || sub.Name() == "help" {
				walk(sub)
			}
		}
	}
	walk(c)
	return out
}

// newHelpCommand is cobra's help command with two opt-in additions:
// --json prints the command tree, and -i opens it in an explorer. With
// neither, it does exactly what cobra's own help command does.
func newHelpCommand() *cobra.Command {
	var interactive, jsonOut bool
	cmd := &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Long: `Help prints the help of any command.
Type tuios help [path to command] for full details.

--json prints the command and every command under it, with each flag.
-i opens the same list in an explorer you can search. Press q or esc to leave it.`,
		Example: `  tuios help checkpoint
  tuios help --json
  tuios help -i`,
		ValidArgsFunction: func(c *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			var out []cobra.Completion
			cmd, _, err := c.Root().Find(args)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			if cmd == nil {
				cmd = c.Root()
			}
			for _, sub := range cmd.Commands() {
				if sub.IsAvailableCommand() && strings.HasPrefix(sub.Name(), toComplete) {
					out = append(out, cobra.CompletionWithDesc(sub.Name(), sub.Short))
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
		Run: func(c *cobra.Command, args []string) {
			target, _, err := c.Root().Find(args)
			if !interactive && !jsonOut {
				if target == nil || err != nil {
					c.Printf("Unknown help topic %#q\n", args)
					cobra.CheckErr(c.Root().Usage())
					return
				}
				if target.Context() == nil {
					target.SetContext(c.Context())
				}
				target.InitDefaultHelpFlag()
				target.InitDefaultVersionFlag()
				cobra.CheckErr(target.Help())
				return
			}
			if target == nil || err != nil {
				cobra.CheckErr(fmt.Errorf("no command %q. Run tuios help to list the commands", strings.Join(args, " ")))
				return
			}
			docs := commandDocs(target)
			if jsonOut {
				out, err := json.MarshalIndent(map[string]any{"type": "command_tree", "commands": docs}, "", "  ")
				cobra.CheckErr(err)
				fmt.Println(string(out))
				return
			}
			cobra.CheckErr(explore.Run(helpExplorer(docs)))
		},
	}
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "Open the command tree in an explorer you can search")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print the command tree as JSON")
	return cmd
}

// helpExplorer is the explorer config for the command tree.
func helpExplorer(docs []commandDoc) explore.Config {
	items := make([]explore.Item, 0, len(docs))
	for _, d := range docs {
		name := d.Path
		if i := strings.LastIndex(name, " "); i >= 0 && d.Depth > 0 {
			name = name[i+1:]
		}
		detail := []string{"Usage: " + d.Use}
		if len(d.Aliases) > 0 {
			detail = append(detail, "Aliases: "+strings.Join(d.Aliases, ", "))
		}
		if d.Long != "" {
			detail = append(detail, d.Long)
		} else if d.Short != "" {
			detail = append(detail, d.Short)
		}
		for _, f := range d.Flags {
			flag := "--" + f.Name
			if f.Shorthand != "" {
				flag = "-" + f.Shorthand + ", " + flag
			}
			line := "Flag " + flag + ": " + f.Usage
			if f.Default != "" {
				line += " (default " + f.Default + ")"
			}
			detail = append(detail, line)
		}
		if d.Example != "" {
			detail = append(detail, "Examples:\n"+d.Example)
		}
		items = append(items, explore.Item{
			Name:     name,
			Note:     d.Short,
			Detail:   detail,
			Search:   d.Path,
			Key:      d.Path,
			Indent:   d.Depth,
			FullName: strings.TrimPrefix(d.Path, "tuios "),
		})
	}
	title := "tuios commands"
	if len(docs) > 0 && docs[0].Depth == 0 && docs[0].Path != "tuios" {
		title = docs[0].Path + " commands"
	}
	return explore.Config{Title: title, Items: items, NameWidth: 30}
}

// configOption is one option as config browse shows it: the list-options
// row and the get-config answer.
type configOption struct {
	optionRow
	Value  string `json:"value"`
	Source string `json:"source"`
}

// newConfigBrowseCommand opens the settable options in an explorer.
func newConfigBrowseCommand() *cobra.Command {
	var sessionName string
	cmd := &cobra.Command{
		Use:   "browse",
		Short: "Search the settable options and set one, in an explorer",
		Long: `Open the settable options in an explorer you can search.

Each option shows its type, default, current value and where the value
comes from. These are what tuios list-options --json and tuios get-config
--json print. Press enter on an option to set it. The explorer sets it the
way tuios set-config does, with the same refusals.

It needs a running daemon. Press q or esc to leave it.`,
		Example: `  tuios config browse
  tuios config browse -s work`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			opts, err := loadConfigOptions(sessionName)
			if err != nil {
				return err
			}
			return explore.Run(configExplorer(sessionName, opts))
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Target session (default: most recently active)")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

// loadConfigOptions reads every option and its value in effect over one
// connection: list-options, then get-option for each path.
func loadConfigOptions(sessionName string) ([]configOption, error) {
	client, err := dialVerb()
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	raw, err := client.Call("list-options", map[string]any{"session": sessionName})
	if err != nil {
		return nil, explainVerbError("list-options", err)
	}
	var res struct {
		Options []optionRow `json:"options"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("failed to parse the option list: %w", err)
	}
	out := make([]configOption, 0, len(res.Options))
	for _, o := range res.Options {
		co := configOption{optionRow: o}
		co.Value, co.Source = readConfigValue(client.Call, sessionName, o.Path)
		out = append(out, co)
	}
	slices.SortStableFunc(out, func(a, b configOption) int {
		if c := strings.Compare(a.Section, b.Section); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out, nil
}

// readConfigValue is get-config --json's value and source for path, or
// empty strings when the daemon does not answer.
func readConfigValue(call func(string, any) (json.RawMessage, error), sessionName, path string) (string, string) {
	raw, err := call("get-option", map[string]any{"session": sessionName, "key": path})
	if err != nil {
		return "", ""
	}
	var v struct {
		Value  string `json:"value"`
		Source string `json:"source"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Value, v.Source
}

// configItem is the explorer row of one option.
func configItem(o configOption) explore.Item {
	value := o.Value
	if value == "" {
		value = getConfigText(o.Path, "")
	}
	note := value
	if o.Source != "" && o.Source != "default" {
		note += "  (" + o.Source + ")"
	}
	detail := []string{
		"Path: " + o.Path,
		"Type: " + o.Type,
		"Default: " + orNone(o.Default),
		"Current: " + orNone(value),
		"Source: " + orNone(o.Source),
	}
	if len(o.Accepted) > 0 {
		detail = append(detail, "Accepted: "+strings.Join(o.Accepted, ", "))
	}
	if o.Max > 0 {
		r := fmt.Sprintf("Range: %d to %d", o.Min, o.Max)
		if o.Auto {
			r += ", or auto"
		}
		detail = append(detail, r)
	}
	if o.Description != "" {
		detail = append(detail, o.Description)
	}
	if o.Deprecated != "" {
		detail = append(detail, "Deprecated: "+o.Deprecated)
	}
	return explore.Item{
		Name:   o.Path,
		Note:   note,
		Group:  o.Section,
		Detail: detail,
		Search: o.Type,
		Key:    o.Path,
	}
}

// configExplorer is the explorer config for the options.
func configExplorer(sessionName string, opts []configOption) explore.Config {
	byPath := map[string]configOption{}
	var groups []string
	items := make([]explore.Item, 0, len(opts))
	for _, o := range opts {
		byPath[o.Path] = o
		if !slices.Contains(groups, o.Section) {
			groups = append(groups, o.Section)
		}
		items = append(items, configItem(o))
	}
	return explore.Config{
		Title:     "tuios options",
		Items:     items,
		Groups:    groups,
		NameWidth: 44,
		Edit: &explore.Edit{
			Start: func(it explore.Item) (string, bool) {
				o, ok := byPath[it.Key]
				if !ok {
					return "", false
				}
				return o.Value, true
			},
			Apply: func(it explore.Item, value string) (explore.Item, string, error) {
				o := byPath[it.Key]
				raw, err := setConfigOption(sessionName, o.Path, value)
				if err != nil {
					return it, "", err
				}
				if client, err := dialVerb(); err == nil {
					o.Value, o.Source = readConfigValue(client.Call, sessionName, o.Path)
					_ = client.Close()
				} else {
					o.Value = value
				}
				byPath[o.Path] = o
				msg := "Set " + o.Path + " = " + value
				if note := setConfigNote(raw); note != "" {
					msg += "\n" + note
				}
				return configItem(o), msg, nil
			},
		},
	}
}

// newKeybindsBrowseCommand opens the keybinding list in an explorer.
func newKeybindsBrowseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "browse [search]",
		Short: "Search the keybindings in an explorer",
		Long: `Open every keybinding in an explorer you can search.

The rows are the rows of tuios keybinds list --json: each action with its keys
in each scope, and its description. Press / to search, and tab or a click on a
tab to show one scope. Press q or esc to leave it.`,
		Example: `  tuios keybinds browse
  tuios keybinds browse spotlight`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := config.LoadUserConfig()
			if err != nil {
				cfg = config.DefaultConfig()
			}
			c := keybindsExplorer(keybindRows(cfg))
			c.Query = strings.Join(args, " ")
			return explore.Run(c)
		},
	}
}

// keybindsExplorer is the explorer config for the keybinding rows.
func keybindsExplorer(rows []keybindRow) explore.Config {
	var groups []string
	items := make([]explore.Item, 0, len(rows))
	for _, r := range rows {
		scope := r.ScopeName
		if scope == "" {
			scope = r.Scope
		}
		if !slices.Contains(groups, scope) {
			groups = append(groups, scope)
		}
		keys := strings.Join(r.Keys, ", ")
		if r.Unbound {
			keys = "(no key)"
		}
		detail := []string{"Keys: " + keys}
		if r.Action != "" {
			detail = append(detail, "Action: "+r.Action)
		}
		detail = append(detail, "Scope: "+scope)
		if r.Chord != "" {
			detail = append(detail, "Reached with: "+r.Chord)
		}
		if r.Section != "" {
			detail = append(detail, "Config table: "+r.Section)
		}
		if len(r.Shadowed) > 0 {
			detail = append(detail, "Shadowed: "+strings.Join(r.Shadowed, ", ")+". Another action in this scope takes these keys first.")
		}
		if r.Fixed {
			detail = append(detail, "Fixed: tuios reads this key itself. No config table binds it.")
		}
		if r.Description != "" {
			detail = append(detail, r.Description)
		}
		items = append(items, explore.Item{
			Name:   keys,
			Note:   r.Description,
			Group:  scope,
			Detail: detail,
			Search: r.Action + " " + r.Scope,
			Key:    r.Scope + "\x00" + r.Action + "\x00" + keys,
		})
	}
	return explore.Config{Title: "tuios keybindings", Items: items, Groups: groups, NameWidth: 28}
}
