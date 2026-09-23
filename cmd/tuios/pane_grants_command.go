package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/spf13/cobra"
)

// newPaneGrantsCommand builds `tuios pane-grants`: what the calling shell's
// pane may do through tuios.
func newPaneGrantsCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "pane-grants",
		Short: "Show what this pane may do through tuios",
		Long: `Show the pane this command runs in and what it may do through tuios.

Every pane holds grants:

  read     read its own session and its fan group
  write    type into the panes of its own session, and leave mail there
  fan      write in its fan group, and start agents with fan and start-agent
  respond  answer another pane's prompt without the person
  admin    everything else, as every pane could before grants existed

admin implies read, write and fan, and never respond. A pane holds the grants
it was started with (start-agent, fan and new-window take --grants), or else
the default of [agents.permissions] in config.toml: admin under mode open,
which is the default, and the grants list under mode strict.

Run outside every pane, it says so: the person's own shell and client are held
to nothing new.`,
		Example: `  # From inside an agent's pane
  tuios pane-grants

  # The same, for a script
  tuios pane-grants --json | jq -r '.grants | join(",")'`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runPaneGrants(jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

// newSetPaneGrantsCommand builds `tuios set-pane-grants`.
func newSetPaneGrantsCommand() *cobra.Command {
	var sessionName, window string
	var grants []string
	var reset, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "set-pane-grants",
		Short: "Give a pane grants, or the default back",
		Long: `Say what a pane may do through tuios: read, write, fan, respond, admin, or
none. See 'tuios pane-grants' for what each allows. --reset gives the pane the
default of [agents.permissions] again, which then follows the config.

From outside every pane anything may be given. From inside a pane, a pane may
change only its own grants unless it holds admin, and never give more than it
holds, so a script can drop its own pane's grants before it starts an agent,
and no agent can raise its own. The change applies to the pane's next call; the
TUIOS_PANE_GRANTS its process started with is not rewritten.`,
		Example: `  # Let the reviewer pane only read
  tuios set-pane-grants -w reviewer --grants read

  # Let a supervisor answer its workers' prompts
  tuios set-pane-grants -w supervisor --grants read,write,fan,respond

  # Drop this pane's grants before starting an agent in it
  tuios set-pane-grants --grants read,write && exec claude

  # Back to the default
  tuios set-pane-grants -w reviewer --reset`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			switch {
			case reset && len(grants) > 0:
				return errors.New("pass --grants or --reset, not both")
			case !reset && len(grants) == 0:
				return errors.New("--grants is required: the grants to give, comma separated, or none; or --reset for the default")
			}
			return runSetPaneGrants(sessionName, window, grants, reset, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session of the pane (default: this pane's, else the most recently active)")
	cmd.Flags().StringVarP(&window, "window", "w", "", "The pane, by name or id (default: this pane)")
	cmd.Flags().StringSliceVar(&grants, "grants", nil, "The grants, comma separated: read, write, fan, respond, admin, or none")
	cmd.Flags().BoolVar(&reset, "reset", false, "Give the pane the default of [agents.permissions]")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	_ = cmd.RegisterFlagCompletionFunc("grants", completeGrantNames)
	return cmd
}

// completeGrantNames completes --grants.
func completeGrantNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return append(append([]string{}, config.PaneGrantNames...), "none"), cobra.ShellCompDirectiveNoFileComp
}

func runPaneGrants(jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	raw, err := client.Call("pane-grants", nil)
	if err != nil {
		return reportVerbError(explainVerbError("pane-grants", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	var res struct {
		Pane          bool     `json:"pane"`
		Window        string   `json:"window"`
		Session       string   `json:"session"`
		Grants        []string `json:"grants"`
		Explicit      bool     `json:"explicit"`
		Mode          string   `json:"mode"`
		DefaultGrants []string `json:"default_grants"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Print(describePaneGrants(res.Pane, res.Window, res.Session, res.Grants, res.Explicit, res.Mode, res.DefaultGrants))
	return nil
}

// describePaneGrants is the text pane-grants prints.
func describePaneGrants(pane bool, window, sess string, grants []string, explicit bool, mode string, defaults []string) string {
	if !pane {
		return fmt.Sprintf("This command runs in no pane of tuios, so no pane grants apply to it.\nMode %s: a pane started with no grants of its own holds %s.\n",
			plainLine(mode), grantList(defaults))
	}
	source := "the default of [agents.permissions], mode " + plainLine(mode)
	if explicit {
		source = "given to this pane"
	}
	return fmt.Sprintf("Pane %s in session %s holds %s (%s).\n", shortWindowID(window), plainLine(sess), grantList(grants), source)
}

// grantList joins grant names for a sentence.
func grantList(names []string) string {
	if len(names) == 0 {
		return "no grants"
	}
	clean := make([]string, len(names))
	for i, n := range names {
		clean[i] = plainLine(n)
	}
	return strings.Join(clean, ", ")
}

func runSetPaneGrants(sessionName, window string, grants []string, reset, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	params := map[string]any{}
	if sessionName != "" {
		params["session"] = sessionName
	}
	if window != "" {
		params["window"] = window
	}
	if reset {
		params["reset"] = true
	} else {
		params["grants"] = grants
	}
	raw, err := client.Call("set-pane-grants", params)
	if err != nil {
		return reportVerbError(explainVerbError("set-pane-grants", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	var res struct {
		Window   string   `json:"window"`
		Grants   []string `json:"grants"`
		Explicit bool     `json:"explicit"`
		Previous []string `json:"previous"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	what := grantList(res.Grants)
	if !res.Explicit {
		what += ", the default"
	}
	fmt.Printf("Pane %s now holds %s (it held %s).\n", shortWindowID(res.Window), what, grantList(res.Previous))
	return nil
}
