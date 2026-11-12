package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/spf13/cobra"
)

// newActionsCommand builds `tuios actions`: the actions a program outside
// tuios offers, such as Rex's command palette, and the user actions of
// config.toml, listed, explained and run.
func newActionsCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "actions",
		Short: "List the actions a launcher can offer, and your own actions",
		Long: `With no subcommand: the rows of tuios's command palette that run as a key
would, for a launcher outside tuios to offer, such as Rex's palette
(docs/REX.md), then your own actions, user:NAME. Each runs in the client
that shows the session with tuios run-command -s SESSION Action NAME.
--json adds the agent harnesses tuios start-agent knows. It reads only this
binary, config.toml and your harness manifests, so it needs no daemon, and
it writes no config.toml when there is none.

list and run: your actions, the [[actions]] and [[keybindings.command]]
entries of config.toml, each the action user:NAME. See KEYBINDINGS.md,
"Your actions".`,
		Example: `  tuios actions --json
  tuios actions list
  tuios actions list open-in-editor
  tuios actions run open-in-editor --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			list := outsideActions()
			own, note := userOutsideActions()
			list = append(list, own...)
			if asJSON {
				data, err := jsonx.Marshal(outsideList{Actions: list, Harnesses: outsideHarnesses(), ConfigNote: note})
				if err != nil {
					return err
				}
				_, err = os.Stdout.Write(append(data, '\n'))
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tCATEGORY\tTITLE")
			for _, a := range list {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", a.Name, a.Category, a.Title)
			}
			if note != "" {
				fmt.Fprintln(os.Stderr, note)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON, with the harnesses")
	cmd.AddCommand(newActionsListCommand(), newActionsRunCommand())
	return cmd
}

// outsideList is tuios actions --json.
type outsideList struct {
	Actions   []outsideAction  `json:"actions"`
	Harnesses []outsideHarness `json:"harnesses"`
	// ConfigNote says why the person's own actions are missing: config.toml
	// could not be read. The built-in actions are there all the same, so a
	// launcher still loads.
	ConfigNote string `json:"config_note,omitempty"`
}

// outsideAction is one action a launcher outside tuios offers.
type outsideAction struct {
	// Name is the action as run-command's Action takes it.
	Name string `json:"name"`
	// Title is the command palette's row, Category its section.
	Title    string `json:"title"`
	Category string `json:"category"`
	// Description is the action table's line for it, or what a user action
	// runs.
	Description string `json:"description"`
	// User marks an action of the person's config.toml, named user:NAME.
	User bool `json:"user,omitempty"`
}

// userCategory is the section the person's own actions are listed under.
const userCategory = "Your actions"

// userOutsideActions are the person's own actions, as a launcher offers
// them: each [[actions]] and [[keybindings.command]] entry tuios uses, named
// user:NAME. An action offered only on a card, a file or another thing that
// is not a pane is left out, since a launcher runs it on the focused pane.
// The labels and commands are the person's own text, so each is made one
// plain line. note says why there are none when config.toml has an error.
func userOutsideActions() (out []outsideAction, note string) {
	cfg, err := config.LoadExistingUserConfig()
	if err != nil {
		return nil, "Your own actions are left out: " + plainLine(err.Error())
	}
	if cfg == nil {
		return nil, ""
	}
	for _, r := range actionRows(cfg) {
		if len(r.Targets) > 0 && !slices.Contains(r.Targets, "pane") {
			continue
		}
		out = append(out, outsideAction{Name: r.Action, Title: plainLine(r.Label), Category: userCategory,
			Description: plainLine("Your action. It runs " + r.Runs + "."), User: true})
	}
	return out, ""
}

// outsideHarness is one agent tuios start-agent knows by its manifest.
type outsideHarness struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
}

// outsideActions are the command palette's static rows that the dispatcher
// runs, in the palette's order, one per action. A row that opens a view's
// own key (RunView) or names nothing in the action table is left out, since
// run-command cannot run it from outside the view.
func outsideActions() []outsideAction {
	seen := map[string]bool{}
	var out []outsideAction
	for _, it := range app.GetCommandPaletteItems() {
		spec, ok := config.LookupAction(it.Bind)
		if it.Bind == "" || !ok || spec.Run != config.RunDispatch {
			continue
		}
		name := config.CanonicalAction(it.Bind)
		if seen[name] {
			continue
		}
		seen[name] = true
		desc := spec.Desc
		if desc == "" {
			desc = it.Name
		}
		out = append(out, outsideAction{Name: name, Title: it.Name, Category: it.Category, Description: desc})
	}
	return out
}

// outsideHarnesses are the bundled and the person's harness manifests.
func outsideHarnesses() []outsideHarness {
	out := []outsideHarness{}
	reg, _ := harness.Load(harness.UserDir())
	if reg == nil {
		return out
	}
	for _, id := range reg.IDs() {
		m := reg.Lookup(id)
		out = append(out, outsideHarness{ID: id, Name: m.DisplayName, Command: m.Command()})
	}
	return out
}

// actionRow is one action as tuios actions list gives it.
type actionRow struct {
	Name    string   `json:"name"`
	Action  string   `json:"action"`
	Label   string   `json:"label"`
	Source  string   `json:"source"`
	Body    string   `json:"body"`
	Runs    string   `json:"runs"`
	Key     string   `json:"key,omitempty"`
	Targets []string `json:"targets,omitempty"`
}

// loadActions reads config.toml and gives its actionRows.
func loadActions() ([]actionRow, *config.UserConfig, error) {
	cfg, err := config.LoadUserConfig()
	if err != nil {
		return nil, nil, errfmt.Errorf("failed to read config.toml: %w", err)
	}
	return actionRows(cfg), cfg, nil
}

// actionRows is every user action of cfg: the [[actions]] entries, then the
// command entries no [[actions]] entry shadows.
func actionRows(cfg *config.UserConfig) []actionRow {
	var rows []actionRow
	seen := map[string]bool{}
	for _, a := range cfg.UserActions(config.IsBuiltinAction) {
		seen[a.Name] = true
		rows = append(rows, actionRow{Name: a.Name, Action: a.ActionName(), Label: a.DisplayLabel(), Source: "actions",
			Body: a.Body(), Runs: actionRuns(a), Key: a.Key, Targets: a.Targets})
	}
	for _, c := range cfg.Keybindings.Commands() {
		name := c.ResolvedName()
		if seen[name] {
			continue
		}
		rows = append(rows, actionRow{Name: name, Action: config.UserActionPrefix + name, Label: c.Label(), Source: "command key",
			Body: "command", Runs: c.ResolvedType() + ": " + c.Command, Key: c.Key})
	}
	return rows
}

// actionRuns says what a runs, in one line.
func actionRuns(a config.UserAction) string {
	switch a.Body() {
	case "action":
		return "the action " + a.Action
	case "plugin":
		return "the plugin action " + a.Plugin
	case "run":
		return a.ResolvedIn() + ": " + strings.Join(showArgs(a.Run), " ")
	}
	return ""
}

// showArgs shows each argument so its edges read: an argument with a space
// or nothing in it is quoted.
func showArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'") {
			a = strconv.Quote(a)
		}
		out[i] = plainLine(a)
	}
	return out
}

func newActionsListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list [NAME]",
		Short: "List your actions, or show one",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			rows, cfg, err := loadActions()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				r, ok := findAction(rows, args[0])
				if !ok {
					return actionNotFound(cfg, args[0])
				}
				rows = []actionRow{r}
			}
			if asJSON {
				data, err := jsonx.Marshal(map[string]any{"actions": cmpOrRows(rows)})
				if err != nil {
					return err
				}
				_, err = os.Stdout.Write(append(data, '\n'))
				return err
			}
			if len(rows) == 0 {
				fmt.Println("You have no actions. KEYBINDINGS.md, \"Your actions\", shows how to add one.")
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tKEY\tSOURCE\tRUNS")
			for _, r := range rows {
				key := r.Key
				if key == "" {
					key = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", plainLine(r.Name), plainLine(key), r.Source, r.Runs)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

// cmpOrRows is rows, or an empty list.
func cmpOrRows(rows []actionRow) []actionRow {
	if rows == nil {
		return []actionRow{}
	}
	return rows
}

// findAction is the row called name, with or without user:.
func findAction(rows []actionRow, name string) (actionRow, bool) {
	name = strings.TrimPrefix(name, config.UserActionPrefix)
	for _, r := range rows {
		if r.Name == name {
			return r, true
		}
	}
	return actionRow{}, false
}

// actionNotFound is the error for a name no action has, with the problem
// tuios found in an entry of that name when there is one.
func actionNotFound(cfg *config.UserConfig, name string) error {
	name = strings.TrimPrefix(name, config.UserActionPrefix)
	if res := config.ValidateConfig(cfg); res != nil {
		for _, w := range res.Warnings {
			if strings.HasPrefix(w.Field, "actions[") && w.Key == name {
				return errfmt.Errorf("the action %s is in config.toml, and tuios ignores it: %s", name, w.Message)
			}
		}
	}
	return errfmt.Errorf("no action is called %s. Run 'tuios actions list' for the names", name)
}

func newActionsRunCommand() *cobra.Command {
	var f taskFlags
	var window string
	var dry bool
	cmd := &cobra.Command{
		Use:   "run NAME [-w PANE] [--dry-run]",
		Short: "Run an action in the session's client",
		Long: `Run an action as its key would, in the client attached to the session, on
the focused pane. A process inside a pane needs the respond grant, as
run-command does. --dry-run prints what the action would run, with the
placeholders filled from the pane -w names (default: the focused pane).`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			rows, cfg, err := loadActions()
			if err != nil {
				return err
			}
			r, ok := findAction(rows, args[0])
			if !ok {
				return actionNotFound(cfg, args[0])
			}
			if dry {
				return dryRunAction(f, window, cfg, r)
			}
			t, err := dialSessionTarget(f.session)
			if err != nil {
				return err
			}
			defer t.Close()
			raw, err := t.client.Call("run-command", t.params(map[string]any{"command": r.Action}))
			if err != nil {
				return reportVerbError(t.explain("run-command", err), f.json)
			}
			if f.json {
				return printVerbResultOn(t, raw, true)
			}
			fmt.Printf("Ran %s%s.\n", plainLine(r.Label), t.on())
			return nil
		},
	}
	f.register(cmd)
	cmd.Flags().StringVarP(&window, "window", "w", "", "With --dry-run, the pane whose facts fill the placeholders (default: the focused pane)")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "Print what the action would run, and run nothing")
	return cmd
}

// dryRunAction prints what r would run on the pane window names.
func dryRunAction(f taskFlags, window string, cfg *config.UserConfig, r actionRow) error {
	a, ok := cfg.UserActionFor(r.Action)
	if !ok || a.Body() != "run" {
		fmt.Printf("%s runs %s.\n", plainLine(r.Label), r.Runs)
		return nil
	}
	t, err := dialSessionTarget(f.session)
	if err != nil {
		return err
	}
	defer t.Close()
	raw, err := t.client.Call("list-windows", t.params(nil))
	if err != nil {
		return reportVerbError(t.explain("list-windows", err), f.json)
	}
	var res struct {
		Workspace int `json:"current_workspace"`
		Windows   []struct {
			ID      string `json:"id"`
			Name    string `json:"display_name"`
			Cwd     string `json:"cwd"`
			Program string `json:"foreground_cmd"`
			Focused bool   `json:"focused"`
		} `json:"windows"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to read the panes: %w", err)
	}
	facts := map[string]string{"session": t.session, "workspace": strconv.Itoa(res.Workspace), "host": t.host}
	found := false
	for _, w := range res.Windows {
		if (window == "" && w.Focused) || (window != "" && (w.ID == window || strings.HasPrefix(w.ID, window) || w.Name == window)) {
			facts["pane.id"], facts["pane.title"], facts["pane.cwd"], facts["pane.program"] = w.ID, w.Name, w.Cwd, w.Program
			found = true
			break
		}
	}
	if !found {
		return errors.New("no pane matches; name one with -w")
	}
	// The selection is the client's; a dry run shows where it would go.
	facts["selection"] = "<selection>"
	argv, err := config.ExpandRun(a.Run, facts)
	if err != nil {
		return err
	}
	fmt.Printf("%s would run, %s, in %s:\n", plainLine(r.Label), a.ResolvedIn(), plainLine(facts["pane.cwd"]))
	for i, arg := range argv {
		fmt.Printf("  [%d] %s\n", i, strconv.Quote(arg))
	}
	return nil
}
