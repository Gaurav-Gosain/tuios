package main

import (
	"github.com/spf13/cobra"
)

// tuios plugin-cell and tuios plugin-notify: what a plugin's process shows
// the person. The daemon places the caller and holds it to the ui grant
// (session/plugin_ui.go). A person's shell is refused.

func pluginVerb(verb string, params map[string]any) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer client.Close()
	_, err = client.Call(verb, params)
	return err
}

func newPluginCellCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin-cell",
		Short: "Set or clear a dock cell, from a plugin",
		Long:  "Set or clear a dock cell of the plugin that runs this command. The manifest names the cell in [[cell]], and the plugin needs the ui grant. The daemon keeps the text, so the command can exit.",
	}
	var action string
	set := &cobra.Command{
		Use:   "set ID TEXT",
		Short: "Set the text of a cell",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return pluginVerb("plugin-cell", map[string]any{"id": args[0], "text": args[1], "action": action})
		},
	}
	set.Flags().StringVar(&action, "action", "", "The action of the plugin that a click runs")
	clear := &cobra.Command{
		Use:   "clear ID",
		Short: "Remove a cell",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return pluginVerb("plugin-cell", map[string]any{"id": args[0], "clear": true})
		},
	}
	cmd.AddCommand(set, clear)
	return cmd
}

func newPluginNotifyCommand() *cobra.Command {
	var level string
	cmd := &cobra.Command{
		Use:   "plugin-notify TEXT",
		Short: "Show the person a notification, from a plugin",
		Long:  "Show the person a notification under the plugin's name. The plugin needs the ui grant.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return pluginVerb("plugin-notify", map[string]any{"text": args[0], "level": level})
		},
	}
	cmd.Flags().StringVar(&level, "level", "", "info, warning or error")
	return cmd
}
