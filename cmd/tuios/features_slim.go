//go:build slim

package main

import (
	"github.com/Gaurav-Gosain/tuios/internal/edition"
	"github.com/spf13/cobra"
)

// addFeatureCommands adds a hidden stub for each command tuios-slim leaves
// out. A stub takes any arguments and flags and hands them to
// runExtensionCommand, the one dispatcher for every missing command.
func addFeatureCommands(root *cobra.Command) {
	for _, name := range slimDroppedCommands {
		root.AddCommand(&cobra.Command{
			Use:                name,
			Hidden:             true,
			DisableFlagParsing: true,
			SilenceUsage:       true,
			RunE: func(_ *cobra.Command, args []string) error {
				return runExtensionCommand(name, args)
			},
		})
	}
}

// runAsLinkedProgram never takes over in tuios-slim: it has no tmux shim and
// no herdr command line, so every name runs as tuios-slim.
func runAsLinkedProgram(string, []string) (int, bool) { return 0, false }

// skillDocument: tuios-slim does not carry the agent skill.
func skillDocument(string) (string, error) { return "", edition.Missing("--skill") }
