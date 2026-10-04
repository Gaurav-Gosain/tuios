package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/herdrcli"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/spf13/cobra"
)

// tuios plugins: the herdr plugins tuios runs. See internal/herdrplugin for
// where plugins are found and internal/session/plugin_host.go for what runs.
//
// list and info read the files and need no daemon. enable, disable, link and
// unlink change [plugins] in config.toml. With a daemon running they go
// through the daemon, which applies the change at once and refuses it from a
// pane. run, open and log need the daemon.

func newPluginsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "plugins",
		Aliases: []string{"plugin"},
		Short:   "List, enable and run herdr plugins",
		Long: `List, enable and run herdr plugins (herdr-plugin.toml).

tuios finds plugins in four places: the folders [plugins] dirs names, the tuios plugins folder, herdr's plugins.json and herdr's managed checkouts. Finding a plugin runs nothing. A plugin runs only after you enable it.

An enabled plugin runs its commands with your rights, outside every pane, as it does in herdr. Enable only plugins you trust. You must run enable, disable, link, unlink and build from a terminal outside tuios.`,
	}
	cmd.AddCommand(newPluginsListCommand(), newPluginsInfoCommand(),
		newPluginsEnableCommand(true), newPluginsEnableCommand(false),
		newPluginsLinkCommand(), newPluginsUnlinkCommand(), newPluginsBuildCommand(),
		newPluginsRunCommand(), newPluginsOpenCommand(), newPluginsLogCommand())
	return cmd
}

// pluginsConfigPath is config.toml.
func pluginsConfigPath() (string, error) {
	return config.GetConfigPath()
}

// discoverPlugins lists the plugins as config.toml says.
func discoverPlugins() ([]herdrplugin.Entry, string, error) {
	path, err := pluginsConfigPath()
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.PluginsInFile(path)
	if err != nil {
		return nil, path, err
	}
	return herdrplugin.Discover(herdrplugin.DefaultDirs(path, cfg.Dirs), cfg.Enabled), path, nil
}

// inPane reports whether this command runs inside a tuios pane.
func inPane() bool {
	return os.Getenv("TUIOS_PANE_ID") != "" || os.Getenv("TUIOS_WINDOW_ID") != ""
}

// errFromPane is the refusal of a trust change from inside a pane.
func errFromPane(what string) error {
	return fmt.Errorf("tuios plugins %s is for the person, and this command runs inside a tuios pane. Nothing was changed. Run it from a terminal outside tuios", what)
}

// pluginCall sends one plugin method to the daemon's herdr socket. ok is
// false when no daemon runs.
func pluginCall(method string, params map[string]any, timeout time.Duration) (map[string]any, bool, error) {
	path, err := herdrSocketPath()
	if err != nil {
		return nil, false, err
	}
	resp, err := herdrcli.Request(path, method, params, timeout)
	if err != nil {
		if herdrcli.NotRunning(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%s: %v", method, err)
	}
	if e, ok := resp["error"].(map[string]any); ok {
		return nil, true, fmt.Errorf("%v (%v)", e["message"], e["code"])
	}
	result, _ := resp["result"].(map[string]any)
	return result, true, nil
}

// needDaemon is pluginCall for a method that has no meaning without one.
func needDaemon(method string, params map[string]any, timeout time.Duration) (map[string]any, error) {
	res, running, err := pluginCall(method, params, timeout)
	if err != nil {
		return nil, err
	}
	if !running {
		return nil, errors.New("no tuios daemon is running. Start one with tuios, then try again")
	}
	return res, nil
}

func printJSONValue(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func entryState(e herdrplugin.Entry) string {
	switch {
	case e.Err != nil:
		return "error"
	case e.Enabled:
		return "on"
	}
	return "off"
}

func newPluginsListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the plugins tuios finds",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			entries, _, err := discoverPlugins()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				if entries == nil {
					entries = []herdrplugin.Entry{}
				}
				return printJSONValue(out, entries)
			}
			if len(entries) == 0 {
				fmt.Fprintln(out, "No plugins found. Put a plugin folder in the tuios plugins folder, or run tuios plugins link DIR.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTATE\tFROM\tVERSION\tNAME")
			for _, e := range entries {
				name, ver := "", ""
				if e.Plugin != nil {
					name, ver = e.Plugin.Name, e.Plugin.Version
				}
				if e.Err != nil {
					name = e.Err.Msg
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.ID, entryState(e), e.Origin, ver, name)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func newPluginsInfoCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "info ID",
		Short: "Show what a plugin contains and what tuios runs of it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, _, err := discoverPlugins()
			if err != nil {
				return err
			}
			var found *herdrplugin.Entry
			for i := range entries {
				if entries[i].ID == args[0] {
					found = &entries[i]
					break
				}
			}
			if found == nil {
				return fmt.Errorf("no plugin has the id %s. Run tuios plugins list to see the ids", args[0])
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSONValue(out, found)
			}
			printPluginInfo(out, *found)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func printPluginInfo(w io.Writer, e herdrplugin.Entry) {
	fmt.Fprintf(w, "%s\n", e.ID)
	fmt.Fprintf(w, "  state     %s\n", entryState(e))
	fmt.Fprintf(w, "  from      %s\n", e.Origin)
	fmt.Fprintf(w, "  manifest  %s\n", e.Path)
	if e.Err != nil {
		fmt.Fprintf(w, "  error     %s: %s\n", e.Err.Code, e.Err.Msg)
	}
	p := e.Plugin
	if p == nil {
		return
	}
	fmt.Fprintf(w, "  name      %s %s\n", p.Name, p.Version)
	if p.Description != "" {
		fmt.Fprintf(w, "  about     %s\n", p.Description)
	}
	if p.Platforms != nil {
		fmt.Fprintf(w, "  platforms %s\n", strings.Join(p.Platforms, ", "))
	}
	for _, warn := range p.Warnings {
		fmt.Fprintf(w, "  warning   %s\n", warn)
	}
	section := func(title string, n int) bool {
		if n == 0 {
			return false
		}
		fmt.Fprintf(w, "\n%s:\n", title)
		return true
	}
	if section("Build commands (run only by tuios plugins build)", len(p.Build)) {
		for _, b := range p.Build {
			fmt.Fprintf(w, "  %s\n", strings.Join(b.Command, " "))
		}
	}
	if section("Startup commands (run when the daemon starts)", len(p.Startup)) {
		for _, s := range p.Startup {
			fmt.Fprintf(w, "  %s\n", strings.Join(s.Command, " "))
		}
	}
	if section("Actions", len(p.Actions)) {
		for _, a := range p.Actions {
			fmt.Fprintf(w, "  %-20s %s\n", a.ID, a.Title)
		}
	}
	if section("Panes", len(p.Panes)) {
		for _, x := range p.Panes {
			fmt.Fprintf(w, "  %-20s %s (%s)\n", x.ID, x.Title, x.Placement)
		}
	}
	if section("Event hooks", len(p.Events)) {
		for _, ev := range p.Events {
			fmt.Fprintf(w, "  %-26s %s\n", ev.On, strings.Join(ev.Command, " "))
		}
	}
	if section("Link handlers (tuios does not run these yet)", len(p.LinkHandlers)) {
		for _, h := range p.LinkHandlers {
			fmt.Fprintf(w, "  %-20s %s -> %s\n", h.ID, h.Pattern, h.Action)
		}
	}
}

func newPluginsEnableCommand(on bool) *cobra.Command {
	use, short, method := "disable ID", "Stop running a plugin", "plugin.disable"
	if on {
		use, short, method = "enable ID", "Run a plugin: its startup, actions, panes and event hooks", "plugin.enable"
	}
	verb := strings.Fields(use)[0]
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inPane() {
				return errFromPane(verb)
			}
			id := args[0]
			entries, path, err := discoverPlugins()
			if err != nil {
				return err
			}
			e := herdrplugin.Find(entries, id)
			if e == nil {
				return fmt.Errorf("no plugin has the id %s. Run tuios plugins list to see the ids", id)
			}
			if on && e.Plugin == nil {
				return fmt.Errorf("the plugin %s cannot be enabled: %s", id, e.Err.Msg)
			}
			_, running, err := pluginCall(method, map[string]any{"plugin_id": id}, 10*time.Second)
			if err != nil {
				return err
			}
			if !running {
				if _, err := config.SetPluginEnabledInFile(path, id, on); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			if on {
				fmt.Fprintf(out, "The plugin %s is enabled. It runs with your rights.\n", id)
				if !running {
					fmt.Fprintln(out, "It starts with the next tuios daemon.")
				}
			} else {
				fmt.Fprintf(out, "The plugin %s is disabled. Its processes are stopped.\n", id)
			}
			return nil
		},
	}
}

func newPluginsLinkCommand() *cobra.Command {
	var enable bool
	cmd := &cobra.Command{
		Use:   "link DIR",
		Short: "Add a plugin folder to [plugins] dirs",
		Long:  "Add a plugin folder to [plugins] dirs. The plugin is listed and stays off. Add --enable to enable it too.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inPane() {
				return errFromPane("link")
			}
			p, perr := herdrplugin.Load(args[0])
			if perr != nil {
				return fmt.Errorf("the plugin cannot be read: %s: %s", perr.Code, perr.Msg)
			}
			_, running, err := pluginCall("plugin.link", map[string]any{"path": p.PluginRoot, "enabled": enable}, 10*time.Second)
			if err != nil {
				return err
			}
			if !running {
				path, err := pluginsConfigPath()
				if err != nil {
					return err
				}
				if _, err := config.AddPluginDirInFile(path, p.PluginRoot); err != nil {
					return err
				}
				if enable {
					if _, err := config.SetPluginEnabledInFile(path, p.PluginID, true); err != nil {
						return err
					}
				}
			}
			state := "It is off. Run tuios plugins enable " + p.PluginID + " to run it."
			if enable {
				state = "It is enabled and runs with your rights."
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Linked %s from %s. %s\n", p.PluginID, p.PluginRoot, state)
			return nil
		},
	}
	cmd.Flags().BoolVar(&enable, "enable", false, "Enable the plugin too")
	return cmd
}

func newPluginsUnlinkCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "unlink ID",
		Short: "Remove a linked plugin folder and disable the plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inPane() {
				return errFromPane("unlink")
			}
			id := args[0]
			_, running, err := pluginCall("plugin.unlink", map[string]any{"plugin_id": id}, 10*time.Second)
			if err != nil {
				return err
			}
			if !running {
				entries, path, err := discoverPlugins()
				if err != nil {
					return err
				}
				for _, e := range entries {
					if e.ID == id && e.Origin == herdrplugin.OriginConfig {
						cfg, _ := config.PluginsInFile(path)
						for _, dir := range cfg.Dirs {
							if abs, _ := filepath.Abs(dir); abs == filepath.Dir(e.Path) || dir == filepath.Dir(e.Path) || dir == e.Path {
								if _, err := config.RemovePluginDirInFile(path, dir); err != nil {
									return err
								}
							}
						}
					}
				}
				if _, err := config.SetPluginEnabledInFile(path, id, false); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "The plugin %s is unlinked and disabled.\n", id)
			return nil
		},
	}
}

func newPluginsBuildCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "build ID",
		Short: "Run a plugin's build commands",
		Long:  "Run the [[build]] commands of a plugin in its folder, one at a time. Each command is printed before it runs. Nothing else runs build commands.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inPane() {
				return errFromPane("build")
			}
			entries, _, err := discoverPlugins()
			if err != nil {
				return err
			}
			e := herdrplugin.Find(entries, args[0])
			if e == nil || e.Plugin == nil {
				return fmt.Errorf("no plugin with the id %s can be read. Run tuios plugins list to see the ids", args[0])
			}
			out := cmd.OutOrStdout()
			ran := 0
			for _, b := range e.Plugin.Build {
				if e.Plugin.Supported(b.Platforms, "build") != nil {
					continue
				}
				fmt.Fprintf(out, "Running in %s: %s\n", e.Plugin.PluginRoot, strings.Join(b.Command, " "))
				prog, perr := herdrplugin.ResolveProgram(b.Command[0], e.Plugin.PluginRoot)
				if perr != nil {
					return errors.New(perr.Msg)
				}
				c := exec.Command(prog, b.Command[1:]...) //nolint:gosec // the person asked to build this plugin
				c.Dir = e.Plugin.PluginRoot
				c.Stdout, c.Stderr = out, cmd.ErrOrStderr()
				c.Env = buildEnv(os.Environ())
				if err := c.Run(); err != nil {
					return fmt.Errorf("the build command failed: %v", err)
				}
				ran++
			}
			if ran == 0 {
				fmt.Fprintf(out, "The plugin %s has no build commands for this platform.\n", e.ID)
				return nil
			}
			fmt.Fprintf(out, "The plugin %s is built.\n", e.ID)
			return nil
		},
	}
}

// buildEnv drops the variables herdr drops for a build: the ones that name
// a server, a pane or a plugin.
func buildEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "HERDR_") || strings.HasPrefix(k, "TUIOS_PANE") || k == "TUIOS_WINDOW_ID" || k == "TUIOS_SESSION" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func newPluginsRunCommand() *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "run ID ACTION",
		Short: "Run an action of an enabled plugin",
		Long:  "Run an action of an enabled plugin. The daemon runs it outside every pane. With --wait, the command waits for the action to end and prints its output.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := needDaemon("plugin.action.invoke", map[string]any{
				"plugin_id": args[0], "action_id": args[1],
				"context": map[string]any{"invocation_source": "cli"},
			}, 10*time.Second)
			if err != nil {
				return err
			}
			log, _ := res["log"].(map[string]any)
			id, _ := log["log_id"].(string)
			out := cmd.OutOrStdout()
			if !wait {
				fmt.Fprintf(out, "Started %s.%s as %s. Run tuios plugins log %s to see its output.\n", args[0], args[1], id, args[0])
				return nil
			}
			for {
				time.Sleep(100 * time.Millisecond)
				res, err := needDaemon("plugin.log.list", map[string]any{"plugin_id": args[0], "limit": 200}, 10*time.Second)
				if err != nil {
					return err
				}
				logs, _ := res["logs"].([]any)
				for _, l := range logs {
					m, _ := l.(map[string]any)
					if m["log_id"] != id || m["status"] == "running" {
						continue
					}
					if s, _ := m["stdout"].(string); s != "" {
						fmt.Fprint(out, s)
					}
					if s, _ := m["stderr"].(string); s != "" {
						fmt.Fprint(cmd.ErrOrStderr(), s)
					}
					if m["status"] != "succeeded" {
						return fmt.Errorf("the action failed: exit code %v %v", m["exit_code"], m["error"])
					}
					return nil
				}
			}
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the action to end and print its output")
	return cmd
}

func newPluginsOpenCommand() *cobra.Command {
	var placement string
	cmd := &cobra.Command{
		Use:   "open ID PANE",
		Short: "Open a pane of an enabled plugin",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]any{"plugin_id": args[0], "entrypoint": args[1], "focus": true}
			if placement != "" {
				params["placement"] = placement
			}
			res, err := needDaemon("plugin.pane.open", params, 30*time.Second)
			if err != nil {
				return err
			}
			if pid, ok := dig(res, "plugin_pane", "pane", "pane_id").(string); ok {
				fmt.Fprintf(cmd.OutOrStdout(), "Opened %s.%s in pane %s.\n", args[0], args[1], pid)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Opened %s.%s.\n", args[0], args[1])
			return nil
		},
	}
	cmd.Flags().StringVar(&placement, "placement", "", "Where the pane opens: overlay, popup, split, tab or zoomed. Omit for the plugin's choice")
	return cmd
}

func dig(m map[string]any, path ...string) any {
	var v any = m
	for _, p := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[p]
	}
	return v
}

func newPluginsLogCommand() *cobra.Command {
	var limit int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "log [ID]",
		Short: "Show the last runs of plugin commands and their output",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]any{"limit": limit}
			if len(args) == 1 {
				params["plugin_id"] = args[0]
			}
			res, err := needDaemon("plugin.log.list", params, 10*time.Second)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSONValue(out, res["logs"])
			}
			logs, _ := res["logs"].([]any)
			if len(logs) == 0 {
				fmt.Fprintln(out, "No plugin commands ran yet.")
				return nil
			}
			for _, l := range logs {
				m, _ := l.(map[string]any)
				what := m["action_id"]
				if what == nil {
					what = m["event"]
				}
				fmt.Fprintf(out, "%v %v %v %v", m["log_id"], m["plugin_id"], what, m["status"])
				if c, ok := m["exit_code"]; ok {
					fmt.Fprintf(out, " exit %v", c)
				}
				fmt.Fprintln(out)
				for _, k := range []string{"stdout", "stderr", "error"} {
					if s, _ := m[k].(string); strings.TrimSpace(s) != "" {
						fmt.Fprintf(out, "  %s: %s\n", k, strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n    "))
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "How many runs to show, from 1 to 200")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

// pluginConfigDir makes and returns a plugin's HERDR_PLUGIN_CONFIG_DIR, for
// herdr plugin config-dir.
func pluginConfigDir(id string) (string, error) {
	n, ok := herdrplugin.NormalizeID(id)
	if !ok {
		return "", fmt.Errorf("invalid plugin id %q", id)
	}
	path, err := pluginsConfigPath()
	if err != nil {
		return "", err
	}
	dirs := herdrplugin.DefaultDirs(path, nil)
	if err := dirs.EnsureUserDirs(n); err != nil {
		return "", err
	}
	return dirs.ConfigDir(n), nil
}
