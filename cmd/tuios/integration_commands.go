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

Each harness is wired through its own configuration: hook entries in
Claude Code, Gemini CLI, Qwen Code, Qoder CLI, Droid and Devin CLI's
settings, Codex's hooks.json, Crush's crush.json, Cursor's hooks.json,
a named block in Antigravity CLI's hooks.json, [[hooks]] tables in Kimi
Code CLI's config.toml, a hook file of tuios's own in GitHub Copilot CLI and
Grok CLI's hooks directories, and a plugin for opencode, Kilo, Amp, Pi and
Hermes Agent (which is also turned on in its config.yaml).

Claude Code, Codex, Gemini CLI, opencode, Kilo, Amp, Kimi and Pi report the
pane's state. The rest report only the conversation id, so a pane can be
resumed, and leave the state to the pane's screen rules: their hooks miss
events a state needs, and a state from a hook outranks every screen rule.

Every entry tuios writes runs "tuios agent-hook" and carries a version
marker, so install replaces an older one, uninstall removes exactly what
tuios wrote, and status says whether what is there is current. A file tuios
owns whole carries the marker in its text, and a file of the same name that
tuios did not write is never overwritten or removed. The user's own settings
and hooks are kept in place and as written, and every file is replaced
atomically, after all of them were worked out, so a file tuios cannot read
leaves every file unchanged. A settings file that is a symlink stays one: the
file it points to is rewritten. The first rewrite keeps the file as it was
beside it with a .tuios.bak suffix, and later rewrites leave that copy alone.`,
	}
	cmd.AddCommand(newIntegrationInstallCommand(), newIntegrationUninstallCommand(), newIntegrationStatusCommand())
	return cmd
}

func newIntegrationInstallCommand() *cobra.Command {
	var all, mcp, mcpWrite, statusLine bool
	var command, then string
	cmd := &cobra.Command{
		Use:   "install [harness...]",
		Short: "Write tuios's hook entries into a harness's configuration",
		Long: `Write tuios's hook entries into a harness's configuration.

With --mcp, also register tuios mcp as an MCP server named tuios, for the
harnesses that read MCP servers from a file tuios can edit: ` + strings.Join(integration.MCPHarnessIDs(), ", ") + `.
The server is read-only and reaches only the session of the pane the harness
runs in. --mcp-write registers it with --write, which adds the tools that
type into panes.

With --statusline, also point Claude Code's status line at "tuios agent-statusline",
which writes the model, context use and cost to the pane's agent metadata
for the rail and the Inbox, and prints nothing. It is opt in, and a status
line of your own is never replaced: install then refuses and prints the
command that keeps it. --then CMD chains to your command, which is run with
the same input and whose output is printed unchanged, so your status line
stays as it was. Uninstall puts your command back.`,
		Example: `  tuios integration install claude-code
  tuios integration install claude-code --mcp
  tuios integration install claude-code --statusline
  tuios integration install claude-code --statusline --then '~/.claude/statusline.sh'
  tuios integration install --all`,
		ValidArgsFunction: completeIntegrationHarness,
		RunE: func(_ *cobra.Command, args []string) error {
			env := integration.SystemEnv()
			targets, err := integrationTargets(env, args, all, true)
			if err != nil {
				return err
			}
			if mcpWrite {
				mcp = true
			}
			if then != "" {
				statusLine = true
			}
			if statusLine && !all {
				for _, t := range targets {
					if !t.SupportsStatusLine() {
						return fmt.Errorf("%s has no status line command tuios can feed from. Only Claude Code does", t.Name)
					}
				}
			}
			if mcp && !all {
				for _, t := range targets {
					if !t.SupportsMCP() {
						return fmt.Errorf("tuios cannot register an MCP server with %s. It can with %s", t.Name, strings.Join(integration.MCPHarnessIDs(), ", "))
					}
				}
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
					printOtherPaths(res)
				default:
					fmt.Printf("%s: already installed and current in %s\n", t.Name, res.Path)
				}
				for _, n := range res.Notes {
					fmt.Printf("  note: %s\n", n)
				}
				if mcp && t.SupportsMCP() && err == nil {
					if !installMCPFor(t, env, command, mcpWrite) {
						failed = append(failed, t.ID+" (mcp)")
					}
				}
				if statusLine && t.SupportsStatusLine() && err == nil {
					if !installStatusLineFor(os.Stdout, os.Stderr, t, env, command, then) {
						failed = append(failed, t.ID+" (statusline)")
					}
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
	cmd.Flags().BoolVar(&mcp, "mcp", false, "Also register tuios mcp, read-only, as an MCP server named tuios")
	cmd.Flags().BoolVar(&mcpWrite, "mcp-write", false, "Register tuios mcp with --write, which adds the tools that type into panes. Implies --mcp")
	cmd.Flags().BoolVar(&statusLine, "statusline", false, "Also feed the model, context use and cost to tuios from Claude Code's status line")
	cmd.Flags().StringVar(&then, "then", "", "Your own status line command, run by the tuios status line with the same input and printed unchanged. Implies --statusline")
	return cmd
}

// installStatusLineFor points the harness's status line at the wrapper and
// says what it did. A status line of the person's own is refused with the
// command that keeps it.
func installStatusLineFor(stdout, stderr io.Writer, t *integration.Target, env integration.Env, command, then string) bool {
	res, err := t.InstallStatusLine(env, command, then)
	var owned *integration.StatusLineOwnedError
	switch {
	case errors.As(err, &owned):
		fmt.Fprintf(stderr, "%s: status line: %v\n", t.Name, err)
		if owned.Command != "" {
			fmt.Fprintf(stderr, "  keep it and feed tuios with:\n    tuios integration install %s --statusline --then %s\n", t.ID, shellQuote(owned.Command))
		}
		return false
	case err != nil:
		fmt.Fprintf(stderr, "%s: status line: %v\n", t.Name, err)
		return false
	case res.Changed:
		fmt.Fprintf(stdout, "%s: status line installed in %s", t.Name, res.Path)
		if then != "" {
			fmt.Fprintf(stdout, ", chained to %s", then)
		}
		fmt.Fprintln(stdout)
	default:
		fmt.Fprintf(stdout, "%s: status line already installed and current in %s\n", t.Name, res.Path)
	}
	return true
}

// shellQuote quotes s as one POSIX shell word for a command the person copies.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// installMCPFor registers the MCP server with one harness and says what it did.
func installMCPFor(t *integration.Target, env integration.Env, command string, write bool) bool {
	res, err := t.InstallMCP(env, command, write)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "%s: MCP server: %v\n", t.Name, err)
		return false
	case res.Changed:
		fmt.Printf("%s: MCP server registered in %s", t.Name, res.Path)
		if res.Backup != "" {
			fmt.Printf(" (previous copy in %s)", res.Backup)
		}
		fmt.Println()
	default:
		fmt.Printf("%s: MCP server already registered and current in %s\n", t.Name, res.Path)
	}
	for _, n := range res.Notes {
		fmt.Printf("  note: %s\n", n)
	}
	return true
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
					printOtherPaths(res)
				default:
					fmt.Printf("%s: nothing of tuios's installed\n", t.Name)
				}
				if t.SupportsStatusLine() {
					sres, serr := t.UninstallStatusLine(env)
					switch {
					case serr != nil:
						fmt.Fprintf(os.Stderr, "%s: status line: %v\n", t.Name, serr)
						failed = append(failed, t.ID+" (statusline)")
					case sres.Changed:
						fmt.Printf("%s: status line removed from %s\n", t.Name, sres.Path)
					}
				}
				if t.SupportsMCP() {
					mres, merr := t.UninstallMCP(env)
					switch {
					case merr != nil:
						fmt.Fprintf(os.Stderr, "%s: MCP server: %v\n", t.Name, merr)
						failed = append(failed, t.ID+" (mcp)")
					case mres.Changed:
						fmt.Printf("%s: MCP server removed from %s\n", t.Name, mres.Path)
					}
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

// printOtherPaths names the files an integration changed besides its main one.
func printOtherPaths(res integration.Result) {
	for _, p := range res.Paths {
		if p != res.Path {
			fmt.Printf("  also changed %s\n", p)
		}
	}
}

// integrationVerdict is a status in a few words.
func integrationVerdict(s integration.Status) string {
	v := integrationVerdictOnly(s)
	if s.Reports == integration.ReportsSession {
		v += " [reports the session id; state from screen rules]"
	}
	return v
}

func integrationVerdictOnly(s integration.Status) string {
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
		if v := mcpVerdict(s); v != "" {
			fmt.Fprintf(w, "%-12s mcp: %s\n", "", v)
		}
		if v := statusLineVerdict(s); v != "" {
			fmt.Fprintf(w, "%-12s statusline: %s\n", "", v)
		}
		for _, n := range s.Notes {
			fmt.Fprintf(w, "%-12s note: %s\n", "", n)
		}
	}
	return nil
}

// mcpVerdict says whether the MCP server is registered, "" when it is not and
// nothing is in its way, so the report stays short for the common case.
func mcpVerdict(s integration.Status) string {
	m := s.MCP
	if m == nil {
		return ""
	}
	mode := "read-only"
	if m.Write {
		mode = "--write"
	}
	switch {
	case m.Installed && m.Current:
		return fmt.Sprintf("registered, %s, current (v%d)", mode, m.Version)
	case m.Installed:
		return "registered, out of date: run tuios integration install " + s.Harness + " --mcp"
	case m.Foreign:
		return "a server named tuios is registered that tuios did not write; left alone"
	}
	return ""
}

// statusLineVerdict says whether the status line feed is installed, "" when it
// is not: it is opt in, so its absence is the ordinary case and says nothing.
func statusLineVerdict(s integration.Status) string {
	sl := s.StatusLine
	if sl == nil || !sl.Installed {
		return ""
	}
	v := fmt.Sprintf("installed, current (v%d)", sl.Version)
	if !sl.Current {
		v = "installed, out of date: run tuios integration install " + s.Harness + " --statusline"
	}
	if sl.Then != "" {
		v += ", chained to " + sl.Then
	}
	return v
}
