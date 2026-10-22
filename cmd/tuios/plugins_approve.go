package main

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/spf13/cobra"
)

// tuios plugins approve: the person's answer for a native plugin on this
// machine, from a terminal. The attached client asks the same question in
// a dialog (session/plugin_approval.go).

func newPluginsApproveCommand() *cobra.Command {
	var deny, yes bool
	var grants []string
	cmd := &cobra.Command{
		Use:   "approve ID",
		Short: "Allow a tuios plugin to run on this machine, or deny it",
		Long: `Allow a tuios plugin to run on this machine, or deny it with --deny.

A tuios plugin runs only after you allow it on this machine. tuios plugins install asks you when it installs the plugin. A plugin that comes from plugins.lock on another machine, from tuios plugins link or from a plugin folder waits for you. tuios also asks in a dialog when you attach.

The approval is for the plugin as it is now. When its manifest changes, tuios asks again.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inPane() {
				return errFromPane("approve")
			}
			return approvePlugin(cmd.OutOrStdout(), args[0], !deny, slices.Contains(grants, "admin"), yes)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&deny, "deny", false, "Deny the plugin. It stays off until you allow it")
	f.BoolVarP(&yes, "yes", "y", false, "Answer without the question")
	f.StringSliceVar(&grants, "grant", nil, "Give the admin grant to a plugin that asks for it")
	return cmd
}

func approvePlugin(out io.Writer, id string, allow, admin, yes bool) error {
	entries, _, err := discoverPlugins()
	if err != nil {
		return err
	}
	e := herdrplugin.Find(entries, id)
	switch {
	case e == nil:
		return fmt.Errorf("no plugin has the id %s. Run tuios plugins list to see the ids", id)
	case e.Plugin == nil:
		return fmt.Errorf("the manifest of %s cannot be read: %s", id, e.Err.Msg)
	case e.Plugin.Format != herdrplugin.FormatTuios:
		return fmt.Errorf("%s is a herdr plugin. Only a tuios plugin needs an approval. Run tuios plugins enable %s", id, id)
	}
	p := e.Plugin
	if allow && slices.Contains(p.Grants, "admin") && !admin {
		return fmt.Errorf("%s asks for the admin grant, which reaches every pane and verb. To give it, run the command again with --grant admin", id)
	}
	if allow {
		fmt.Fprintf(out, "\nAllow %s %s (tuios plugin)\n", p.PluginID, shownArgv(p.Version))
		if e.Lock != nil {
			fmt.Fprintf(out, "  from    %s", shownSource(e.Lock.Source))
			if e.Lock.Commit != "" {
				fmt.Fprintf(out, " @ %.12s", e.Lock.Commit)
			}
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "  folder  %s\n\nRuns when enabled, outside every pane, with your rights:\n", p.PluginRoot)
		runs := herdrplugin.RunsLines(p)
		if len(runs) == 0 {
			runs = []string{"nothing"}
		}
		for _, line := range runs {
			fmt.Fprintf(out, "  %s\n", line)
		}
		fmt.Fprintf(out, "\n%s", grantsText(p))
	}
	question := "Allow it on this machine?"
	if !allow {
		question = "Deny " + id + " on this machine?"
	}
	ok, err := newLookPrompt(yes, false).confirm(out, question)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("nothing was changed")
	}
	// Every daemon that runs confirms that the person runs this command,
	// before any answer is written, also under --server. With no daemon to
	// ask, nothing proves it, so nothing is written. See approval.go.
	if err := confirmPerson(); err != nil {
		return err
	}
	params := map[string]any{"plugin_id": p.PluginID, "trust": p.Trust, "allow": allow, "admin": admin}
	_, running, err := pluginCall("plugin.approve", params, 30*time.Second)
	if err != nil {
		return err
	}
	if !running {
		// The daemon this command names does not run, and every daemon
		// that runs confirmed the person above. The next daemon reads the
		// answer.
		dirs := herdrplugin.DefaultDirs(pluginsConfigPathOrEmpty(), nil)
		if allow {
			err = herdrplugin.Approve(dirs.ApprovalsPath(), p, p.Trust, admin)
		} else {
			err = herdrplugin.Deny(dirs.ApprovalsPath(), p, p.Trust)
		}
		if err != nil {
			return err
		}
	}
	switch {
	case !allow:
		fmt.Fprintf(out, "You denied %s on this machine. It does not run.\n", id)
	case !e.Enabled:
		fmt.Fprintf(out, "You allowed %s on this machine. It is off. Run tuios plugins enable %s to run it.\n", id, id)
	case running:
		fmt.Fprintf(out, "You allowed %s on this machine. It runs now.\n", id)
	default:
		fmt.Fprintf(out, "You allowed %s on this machine. It starts with the next tuios daemon.\n", id)
	}
	return nil
}

// pluginsConfigPathOrEmpty is config.toml, or "" when it cannot be found.
func pluginsConfigPathOrEmpty() string {
	p, err := pluginsConfigPath()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(p)
}
