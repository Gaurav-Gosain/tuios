package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/spf13/cobra"
)

// integrationTargets resolves the harness arguments of an integration
// command. --all takes every harness whose configuration directory exists
// when installing, and every harness when removing or reporting.
func integrationTargets(env integration.Env, args []string, all, onlyPresent bool) ([]*integration.Target, error) {
	if all {
		if len(args) > 0 {
			return nil, errors.New("pass harness names or --all, not both")
		}
		var out []*integration.Target
		for _, t := range integration.Targets() {
			if onlyPresent {
				if fi, err := os.Stat(t.ConfigDir(env)); err != nil || !fi.IsDir() {
					continue
				}
			}
			out = append(out, t)
		}
		return out, nil
	}
	if len(args) == 0 {
		return nil, errors.New("name a harness (" + strings.Join(integration.HarnessIDs(), ", ") + ") or pass --all")
	}
	var out []*integration.Target
	for _, a := range args {
		t, ok := integration.LookupTarget(a)
		if !ok {
			return nil, fmt.Errorf("no integration for %q. Available: %s", a, strings.Join(integration.HarnessIDs(), ", "))
		}
		out = append(out, t)
	}
	return out, nil
}

func completeIntegrationHarness(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return integration.HarnessIDs(), cobra.ShellCompDirectiveNoFileComp
}

func newIntegrationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Wire coding-agent harnesses' hooks to tuios agent state",
		Long: `Install, remove and check the hook entries that report a harness's state to
the tuios pane it runs in.

Each harness is wired through its own configuration: Claude Code's
settings.json hooks, Codex's hooks.json, Gemini CLI's settings.json hooks,
and an opencode plugin. Every entry tuios writes runs "tuios agent-hook" and
carries a version marker, so install replaces an older one, uninstall
removes exactly what tuios wrote, and status says whether what is there is
current. The user's own settings and hooks are kept in place and as written,
and the file is replaced atomically. A settings file that is a symlink stays
one: the file it points to is rewritten. The first rewrite keeps the file as
it was beside it with a .tuios.bak suffix, and later rewrites leave that copy
alone.`,
	}
	cmd.AddCommand(newIntegrationInstallCommand(), newIntegrationUninstallCommand(), newIntegrationStatusCommand())
	return cmd
}

func newIntegrationInstallCommand() *cobra.Command {
	var all bool
	var command string
	cmd := &cobra.Command{
		Use:   "install [harness...]",
		Short: "Write tuios's hook entries into a harness's configuration",
		Example: `  tuios integration install claude-code
  tuios integration install --all`,
		ValidArgsFunction: completeIntegrationHarness,
		RunE: func(_ *cobra.Command, args []string) error {
			env := integration.SystemEnv()
			targets, err := integrationTargets(env, args, all, true)
			if err != nil {
				return err
			}
			if len(targets) == 0 {
				fmt.Println("No supported harness has a configuration directory here. Run the harness once, then install.")
				return nil
			}
			var failed []string
			for _, t := range targets {
				res, err := t.Install(env, command)
				switch {
				case err != nil:
					fmt.Fprintf(os.Stderr, "%s: %v\n", t.Name, err)
					failed = append(failed, t.ID)
				case res.Changed:
					fmt.Printf("%s: installed in %s", t.Name, res.Path)
					if res.Backup != "" {
						fmt.Printf(" (previous copy in %s)", res.Backup)
					}
					fmt.Println()
				default:
					fmt.Printf("%s: already installed and current in %s\n", t.Name, res.Path)
				}
				for _, n := range res.Notes {
					fmt.Printf("  note: %s\n", n)
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("install failed for %s", strings.Join(failed, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Install for every supported harness whose configuration directory exists")
	cmd.Flags().StringVar(&command, "command", "tuios", "Program the hooks run, when tuios is not on the harness's PATH")
	return cmd
}

func newIntegrationUninstallCommand() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:               "uninstall [harness...]",
		Short:             "Remove the hook entries tuios wrote, and nothing else",
		Example:           `  tuios integration uninstall claude-code`,
		ValidArgsFunction: completeIntegrationHarness,
		RunE: func(_ *cobra.Command, args []string) error {
			env := integration.SystemEnv()
			targets, err := integrationTargets(env, args, all, false)
			if err != nil {
				return err
			}
			var failed []string
			for _, t := range targets {
				res, err := t.Uninstall(env)
				switch {
				case err != nil:
					fmt.Fprintf(os.Stderr, "%s: %v\n", t.Name, err)
					failed = append(failed, t.ID)
				case res.Changed:
					fmt.Printf("%s: removed from %s\n", t.Name, res.Path)
				default:
					fmt.Printf("%s: nothing of tuios's installed\n", t.Name)
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("uninstall failed for %s", strings.Join(failed, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Remove from every supported harness")
	return cmd
}

func newIntegrationStatusCommand() *cobra.Command {
	var asJSON bool
	var command string
	cmd := &cobra.Command{
		Use:               "status [harness...]",
		Short:             "Say whether each harness's integration is installed and current",
		Example:           `  tuios integration status --json`,
		ValidArgsFunction: completeIntegrationHarness,
		RunE: func(_ *cobra.Command, args []string) error {
			env := integration.SystemEnv()
			targets, err := integrationTargets(env, args, len(args) == 0, false)
			if err != nil {
				return err
			}
			statuses := make([]integration.Status, 0, len(targets))
			for _, t := range targets {
				statuses = append(statuses, t.Status(env, command))
			}
			return printIntegrationStatus(os.Stdout, statuses, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the report as JSON")
	cmd.Flags().StringVar(&command, "command", "tuios", "Program a current install runs")
	return cmd
}

// integrationVerdict is a status in a few words.
func integrationVerdict(s integration.Status) string {
	switch {
	case s.Installed && s.Current:
		return fmt.Sprintf("installed, current (v%d)", s.Version)
	case s.Installed:
		return fmt.Sprintf("installed, out of date (v%d, this tuios installs v%d): run tuios integration install %s", s.Version, s.WantVersion, s.Harness)
	case !s.ConfigDirExists:
		return "not installed; " + s.Name + " has not run here"
	default:
		return "not installed: run tuios integration install " + s.Harness
	}
}

func printIntegrationStatus(w io.Writer, statuses []integration.Status, asJSON bool) error {
	if asJSON {
		out, err := json.MarshalIndent(statuses, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(out))
		return err
	}
	for _, s := range statuses {
		fmt.Fprintf(w, "%-12s %s\n", s.Harness, integrationVerdict(s))
		for _, n := range s.Notes {
			fmt.Fprintf(w, "%-12s note: %s\n", "", n)
		}
	}
	return nil
}
